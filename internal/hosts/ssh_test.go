package hosts

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"
)

// fakeSSHD 是測試用的最小 sshd：在同一台機器上實作 direct-tcpip、direct-streamlocal、
// streamlocal-forward（含 StreamLocalBindUnlink 的行為）與 exec。真的 OpenSSH 見 ssh_docker_test.go。
type fakeSSHD struct {
	t       *testing.T
	hostKey ssh.Signer
	ln      net.Listener
	cfg     *ssh.ServerConfig
	mu      sync.Mutex
	conns   []net.Conn
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startSSHD(t *testing.T, hostKey ssh.Signer, user ssh.PublicKey) *fakeSSHD {
	t.Helper()
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(user.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeSSHD{t: t, ln: ln, cfg: cfg, hostKey: hostKey}
	t.Cleanup(d.close)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.conns = append(d.conns, c)
			d.mu.Unlock()
			go d.serve(c)
		}
	}()
	return d
}

func (d *fakeSSHD) addr() string { return d.ln.Addr().String() }

// dropAll 切斷所有連線（模擬 sshd 重啟或網路中斷）。
func (d *fakeSSHD) dropAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		c.Close()
	}
	d.conns = nil
}

func (d *fakeSSHD) close() {
	d.ln.Close()
	d.dropAll()
}

func (d *fakeSSHD) serve(c net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(c, d.cfg)
	if err != nil {
		c.Close()
		return
	}
	defer sc.Close()
	var lmu sync.Mutex
	var listeners []net.Listener
	defer func() {
		lmu.Lock()
		defer lmu.Unlock()
		for _, l := range listeners {
			l.Close()
		}
	}()
	go func() {
		for r := range reqs {
			switch r.Type {
			case "streamlocal-forward@openssh.com":
				var m struct{ Path string }
				if ssh.Unmarshal(r.Payload, &m) != nil {
					_ = r.Reply(false, nil)
					continue
				}
				_ = os.Remove(m.Path) // StreamLocalBindUnlink yes
				l, err := net.Listen("unix", m.Path)
				if err != nil {
					_ = r.Reply(false, nil)
					continue
				}
				_ = os.Chmod(m.Path, 0o666) // StreamLocalBindMask 0111
				lmu.Lock()
				listeners = append(listeners, l)
				lmu.Unlock()
				_ = r.Reply(true, nil)
				go func() {
					for {
						lc, err := l.Accept()
						if err != nil {
							return
						}
						ch, in, err := sc.OpenChannel("forwarded-streamlocal@openssh.com", ssh.Marshal(struct{ Path, Reserved string }{m.Path, ""}))
						if err != nil {
							lc.Close()
							continue
						}
						go ssh.DiscardRequests(in)
						go pipe(lc, chanConn{ch})
					}
				}()
			case "tcpip-forward":
				var m struct {
					Addr string
					Port uint32
				}
				if ssh.Unmarshal(r.Payload, &m) != nil {
					_ = r.Reply(false, nil)
					continue
				}
				l, err := net.Listen("tcp", net.JoinHostPort(m.Addr, strconv.Itoa(int(m.Port))))
				if err != nil {
					_ = r.Reply(false, nil)
					continue
				}
				lmu.Lock()
				listeners = append(listeners, l)
				lmu.Unlock()
				port := uint32(l.Addr().(*net.TCPAddr).Port)
				_ = r.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
				go func() {
					for {
						lc, err := l.Accept()
						if err != nil {
							return
						}
						ch, in, err := sc.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
							Addr       string
							Port       uint32
							OriginAddr string
							OriginPort uint32
						}{m.Addr, port, "127.0.0.1", uint32(lc.RemoteAddr().(*net.TCPAddr).Port)}))
						if err != nil {
							lc.Close()
							continue
						}
						go ssh.DiscardRequests(in)
						go pipe(lc, chanConn{ch})
					}
				}()
			default:
				if r.WantReply {
					_ = r.Reply(false, nil)
				}
			}
		}
	}()
	for nc := range chans {
		switch nc.ChannelType() {
		case "direct-tcpip":
			var m struct {
				Host     string
				Port     uint32
				OrigHost string
				OrigPort uint32
			}
			_ = ssh.Unmarshal(nc.ExtraData(), &m)
			d.dialAndPipe(nc, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(int(m.Port))))
		case "direct-streamlocal@openssh.com":
			var m struct {
				Path      string
				Reserved  string
				Reserved2 uint32
			}
			_ = ssh.Unmarshal(nc.ExtraData(), &m)
			d.dialAndPipe(nc, "unix", m.Path)
		case "session":
			ch, in, err := nc.Accept()
			if err != nil {
				continue
			}
			go func() {
				for r := range in {
					if r.Type != "exec" {
						_ = r.Reply(false, nil)
						continue
					}
					var m struct{ Cmd string }
					_ = ssh.Unmarshal(r.Payload, &m)
					_ = r.Reply(true, nil)
					out, err := exec.Command("sh", "-c", m.Cmd).CombinedOutput()
					_, _ = ch.Write(out)
					code := uint32(0)
					if err != nil {
						code = 1
					}
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
					ch.Close()
				}
			}()
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "no")
		}
	}
}

func (d *fakeSSHD) dialAndPipe(nc ssh.NewChannel, network, addr string) {
	c, err := net.Dial(network, addr)
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, in, err := nc.Accept()
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(in)
	go pipe(c, chanConn{ch})
}

// chanConn 讓 ssh.Channel 可以交給 pipe。
type chanConn struct{ ssh.Channel }

func (chanConn) LocalAddr() net.Addr              { return &net.UnixAddr{} }
func (chanConn) RemoteAddr() net.Addr             { return &net.UnixAddr{} }
func (chanConn) SetDeadline(time.Time) error      { return nil }
func (chanConn) SetReadDeadline(time.Time) error  { return nil }
func (chanConn) SetWriteDeadline(time.Time) error { return nil }

// echoServer 回傳收到的每一行，前面加上 prefix。
func echoServer(t *testing.T, network, addr, prefix string) net.Listener {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					_, _ = io.WriteString(c, prefix+line)
				}
			}()
		}
	}()
	return ln
}

func roundTrip(t *testing.T, network, addr, msg string) string {
	t.Helper()
	c, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		t.Fatalf("連 %s：%v", addr, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, msg+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("讀 %s：%v", addr, err)
	}
	return strings.TrimSpace(line)
}

func waitConnects(t *testing.T, s *SSH, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h := s.Health()
		if h.State == SSHOnline && h.Connects >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("沒有重連：%+v", h)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSSHForwardsAndReconnect(t *testing.T) {
	// unix socket 路徑不能太長，不用 t.TempDir()。
	dir, err := os.MkdirTemp("", "xbssh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	hostKey, userKey := newSigner(t), newSigner(t)
	d := startSSHD(t, hostKey, userKey.PublicKey())

	// 「遠端」的 noVNC 埠與 Docker socket；「本機」的 product.sock。
	vnc := echoServer(t, "tcp", "127.0.0.1:0", "vnc:")
	dockerSock := filepath.Join(dir, "docker.sock")
	dln, err := net.Listen("unix", dockerSock)
	if err != nil {
		t.Fatal(err)
	}
	dsrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		_, _ = io.WriteString(w, "OK")
	})}
	go func() { _ = dsrv.Serve(dln) }()
	t.Cleanup(func() { dsrv.Close() })
	local := func(botID string) string { return filepath.Join(dir, "local-"+botID+".sock") }
	echoServer(t, "unix", local("b1"), "product:")

	kh := filepath.Join(dir, "hosts", "h1", "known_hosts")
	s, err := NewSSH(SSHOptions{ID: "h1", Addr: d.addr(), User: "xbot", Signer: userKey, KnownHosts: kh,
		RemoteDir: filepath.Join(dir, "remote"), DockerSocket: dockerSock, LocalSocket: local})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.WaitOnline(ctx); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(kh); err != nil || !strings.Contains(string(b), "ssh-ed25519") {
		t.Fatalf("第一次連線應該記下主機金鑰：%q %v", b, err)
	}
	if s.Kind() != "ssh" || s.ID() != "h1" {
		t.Fatal(s.Kind(), s.ID())
	}

	// Docker API 經 SSH 到遠端 socket。
	if _, err := s.Docker().Ping(ctx, client.PingOptions{}); err != nil {
		t.Fatalf("Docker ping：%v", err)
	}
	// 埠轉發：同一個遠端埠固定同一個本機位址。
	vncPort := vnc.Addr().(*net.TCPAddr).Port
	addr := s.Addr(vncPort)
	if addr == "" || addr != s.Addr(vncPort) {
		t.Fatalf("Addr：%q", addr)
	}
	if got := roundTrip(t, "tcp", addr, "hi"); got != "vnc:hi" {
		t.Fatalf("埠轉發：%q", got)
	}
	// 反向轉發：遠端的 product.sock 連回本機。
	ipcDir, err := s.PrepareIPC("b1")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(ipcDir); err != nil || st.Mode().Perm() != 0o711 {
		t.Fatalf("遠端 ipc 資料夾：%v %v", st, err)
	}
	if again, err := s.PrepareIPC("b1"); err != nil || again != ipcDir {
		t.Fatalf("重複 PrepareIPC：%q %v", again, err)
	}
	remoteSock := filepath.Join(ipcDir, "product.sock")
	if got := roundTrip(t, "unix", remoteSock, "model"); got != "product:model" {
		t.Fatalf("反向轉發：%q", got)
	}

	// 斷線兩次：每次都自動重連，三種轉發都恢復，本機位址不變。
	for i := 2; i <= 3; i++ {
		d.dropAll()
		waitConnects(t, s, i)
		if got := roundTrip(t, "unix", remoteSock, "again"); got != "product:again" {
			t.Fatalf("重連 %d 後反向轉發：%q", i, got)
		}
		if got := roundTrip(t, "tcp", s.Addr(vncPort), "again"); got != "vnc:again" || s.Addr(vncPort) != addr {
			t.Fatalf("重連 %d 後埠轉發：%q", i, got)
		}
		if _, err := s.Docker().Ping(ctx, client.PingOptions{}); err != nil {
			t.Fatalf("重連 %d 後 Docker：%v", i, err)
		}
	}
	h := s.Health()
	if h.ReverseForwards != 1 || h.LocalForwards != 1 || h.Connects != 3 {
		t.Fatalf("健康狀態：%+v", h)
	}
	if out, err := s.Run(ctx, "echo hello"); err != nil || strings.TrimSpace(out) != "hello" {
		t.Fatalf("Run：%q %v", out, err)
	}

	// 主機離線時：呼叫等一下就回報離線，不會卡住。
	s.ReleaseIPC("b1")
	d.ln.Close()
	d.dropAll()
	short, scancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer scancel()
	time.Sleep(50 * time.Millisecond)
	if _, err := s.Docker().Ping(short, client.PingOptions{}); err == nil {
		t.Fatal("主機離線時 Docker 呼叫應該失敗")
	}
	if st := s.Health().State; st != SSHReconnecting {
		t.Fatalf("離線狀態：%s", st)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if st := s.Health().State; st != SSHClosed || s.Addr(vncPort) != "" {
		t.Fatalf("關閉後：%s %q", st, s.Addr(vncPort))
	}
}

func TestSSHHostKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	userKey := newSigner(t)
	d1 := startSSHD(t, newSigner(t), userKey.PublicKey())
	kh := filepath.Join(dir, "known_hosts")
	opts := SSHOptions{ID: "h", Addr: d1.addr(), User: "xbot", Signer: userKey, KnownHosts: kh, LocalSocket: func(string) string { return "" }}
	s, err := NewSSH(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.WaitOnline(ctx); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 同一個位址換了主機金鑰（例：被冒充）：拒絕連線、不再重試。
	d1.close()
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		t.Skipf("無法重用埠：%v", err)
	}
	ln.Close()
	startSSHDOn(t, opts.Addr, newSigner(t), userKey.PublicKey())
	s2, err := NewSSH(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.WaitOnline(ctx); !errors.Is(err, ErrHostOffline) {
		t.Fatalf("應該拒絕：%v", err)
	}
	if h := s2.Health(); h.State != SSHFailed || !strings.Contains(h.LastError, ErrHostKeyMismatch.Error()) {
		t.Fatalf("狀態：%+v", h)
	}
}

func startSSHDOn(t *testing.T, addr string, hostKey ssh.Signer, user ssh.PublicKey) *fakeSSHD {
	t.Helper()
	d := startSSHD(t, hostKey, user)
	// startSSHD 用隨機埠；換成指定位址。
	d.ln.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("無法重用埠：%v", err)
	}
	d.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(c)
		}
	}()
	return d
}
