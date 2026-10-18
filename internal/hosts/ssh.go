package hosts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"xbot/internal/limits"
)

// 遠端主機（B11 原型）：一條 SSH 連線同時提供
//   - Docker API：經 direct-streamlocal 連遠端的 /var/run/docker.sock；
//   - 本機埠轉發：容器發布在遠端 127.0.0.1 的 noVNC／Hermes 埠，在本機開 127.0.0.1:<隨機埠> 轉過去（Addr）；
//   - 反向 Unix socket 轉發：遠端 /var/lib/xbot/bots/<id>/ipc/product.sock（streamlocal-forward@openssh.com）
//     每條連線都接回本機那個 Bot 的 product.sock（PrepareIPC）。
//
// 連線斷了會自動退避重連，重連後反向轉發重新建立；本機的轉發埠不變，所以 box 表與 Hermes 驅動記住的位址仍然有效。
//
// 遠端 sshd 需要（docs/spike-ssh-forward.md「主機需求」）：
//
//	AllowTcpForwarding yes          # noVNC／Hermes 埠轉發
//	AllowStreamLocalForwarding yes  # Docker socket 與 product.sock
//	StreamLocalBindUnlink yes       # 重連時換掉上一條連線留下的 product.sock 檔
//	StreamLocalBindMask 0111        # socket 開成 0666：容器裡的 Bot 使用者（UID 1000）和 xbot 帳號不同

// SSH 主機的連線狀態（Health.State）。
const (
	SSHConnecting   = "connecting"
	SSHOnline       = "online"
	SSHReconnecting = "reconnecting"
	SSHFailed       = "failed" // 主機金鑰不符：不再自動重連，要人處理
	SSHClosed       = "closed"
)

// ErrHostKeyMismatch 表示遠端主機的金鑰和第一次連線時記下的不同（可能被冒充，或主機重灌）。
var ErrHostKeyMismatch = errors.New("主機金鑰和第一次連線時不同")

// ErrHostOffline 表示主機目前連不上（正在重連）。
var ErrHostOffline = errors.New("主機離線")

// SSHOptions 是遠端主機的連線設定。
type SSHOptions struct {
	// ID 是 host 資料表的主鍵。
	ID string
	// Addr 是 SSH 位址 host:port（tailnet 名稱或 IP）。
	Addr string
	// User 是遠端帳號，通常是 xbot。
	User string
	// Signer 是 XBot 的專用 SSH 私鑰。
	Signer ssh.Signer
	// KnownHosts 是這台主機專用的 known_hosts 檔（例：<data>/hosts/<id>/known_hosts）。
	// 檔案裡還沒有這台主機時，第一次連線會記下它的金鑰；之後金鑰不同就拒絕連線（ErrHostKeyMismatch）。
	KnownHosts string
	// RemoteDir 是遠端的資料目錄，空白時用 limits.SSHRemoteDataDir。
	RemoteDir string
	// DockerSocket 是遠端的 Docker socket，空白時用 /var/run/docker.sock。
	DockerSocket string
	// LocalSocket 回傳 Bot 在本機的 product.sock（ipc.Server.Path）；反向轉發進來的連線接到這裡。
	LocalSocket func(botID string) string
	// Dial 建立 TCP 連線，nil 時用 net.Dialer（測試用來模擬斷線）。
	Dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	Logger *slog.Logger
}

// Health 是 SSH 主機的連線狀態。
type Health struct {
	State string `json:"state"`
	// Since 是進入目前狀態的時間。
	Since time.Time `json:"since"`
	// LastError 是最近一次連線失敗或斷線的原因。
	LastError string `json:"last_error,omitempty"`
	// Connects 是成功連線的次數（第一次加上每次重連）。
	Connects int `json:"connects"`
	// RTT 是最近一次 keepalive 的往返時間。
	RTT time.Duration `json:"rtt"`
	// ReverseForwards、LocalForwards 是目前的反向（product.sock）與本機（noVNC／Hermes）轉發數。
	ReverseForwards int `json:"reverse_forwards"`
	LocalForwards   int `json:"local_forwards"`
}

// SSH 是經 SSH 連線的遠端 Docker 主機。
type SSH struct {
	o      SSHOptions
	log    *slog.Logger
	docker *client.Client
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	cl     *ssh.Client
	online chan struct{} // 連上時關閉；斷線時換新的
	health Health
	local  map[int]*localForward

	// fwdMu 讓建立反向轉發（PrepareIPC 與重連後的恢復）依序進行。
	fwdMu   sync.Mutex
	reverse map[string]*reverseForward
}

type reverseForward struct {
	botID, remote string
	cl            *ssh.Client // 建立 ln 的那條連線
	ln            net.Listener
}

type localForward struct {
	addr string
	ln   net.Listener
}

var _ Host = (*SSH)(nil)

// NewSSH 建立遠端主機並在背景開始連線（斷線自動重連）。用 WaitOnline 等第一次連上。
func NewSSH(o SSHOptions) (*SSH, error) {
	if o.Addr == "" || o.User == "" || o.Signer == nil || o.KnownHosts == "" || o.LocalSocket == nil {
		return nil, errors.New("SSH 主機設定不完整：需要位址、帳號、私鑰、known_hosts 與本機 socket")
	}
	if _, _, err := net.SplitHostPort(o.Addr); err != nil {
		o.Addr = net.JoinHostPort(o.Addr, "22")
	}
	if o.RemoteDir == "" {
		o.RemoteDir = limits.SSHRemoteDataDir
	}
	if o.DockerSocket == "" {
		o.DockerSocket = "/var/run/docker.sock"
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Dial == nil {
		d := &net.Dialer{KeepAlive: 30 * time.Second}
		o.Dial = d.DialContext
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &SSH{
		o: o, log: o.Logger.With("host", o.ID, "addr", o.Addr), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		online: make(chan struct{}), health: Health{State: SSHConnecting, Since: time.Now()},
		local: map[int]*localForward{}, reverse: map[string]*reverseForward{},
	}
	cli, err := client.New(
		// 主機名稱只用在 HTTP 標頭；實際連線一律經 SSH 到遠端的 Docker socket。
		client.WithHost("unix://"+o.DockerSocket),
		client.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
			cl, err := s.client(ctx)
			if err != nil {
				return nil, err
			}
			return cl.DialContext(ctx, "unix", s.o.DockerSocket)
		}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("無法建立遠端 Docker 連線：%w", err)
	}
	s.docker = cli
	go s.run()
	return s, nil
}

// ID 實作 Host。
func (s *SSH) ID() string { return s.o.ID }

// Kind 實作 Host。
func (s *SSH) Kind() string { return "ssh" }

// Docker 實作 Host。
func (s *SSH) Docker() DockerAPI { return s.docker }

// Health 回傳目前的連線狀態。
func (s *SSH) Health() Health {
	s.mu.Lock()
	h := s.health
	h.LocalForwards = len(s.local)
	s.mu.Unlock()
	s.fwdMu.Lock()
	h.ReverseForwards = len(s.reverse)
	s.fwdMu.Unlock()
	return h
}

// WaitOnline 等到連線建立；主機金鑰不符或已關閉時立刻回傳錯誤。
func (s *SSH) WaitOnline(ctx context.Context) error {
	_, err := s.clientWait(ctx)
	return err
}

// client 回傳目前的連線；正在重連時最多等 limits.SSHWaitOnline。
func (s *SSH) client(ctx context.Context) (*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.SSHWaitOnline)
	defer cancel()
	return s.clientWait(ctx)
}

func (s *SSH) clientWait(ctx context.Context) (*ssh.Client, error) {
	for {
		s.mu.Lock()
		cl, online, h := s.cl, s.online, s.health
		s.mu.Unlock()
		if cl != nil {
			return cl, nil
		}
		if h.State == SSHFailed || h.State == SSHClosed {
			return nil, fmt.Errorf("%w：%s", ErrHostOffline, h.LastError)
		}
		select {
		case <-online:
		case <-ctx.Done():
			msg := h.LastError
			if msg == "" {
				msg = "正在連線"
			}
			return nil, fmt.Errorf("%w（%s）：%w", ErrHostOffline, msg, ctx.Err())
		}
	}
}

// run 連線、恢復轉發、等斷線，再退避重連，直到 Close 或主機金鑰不符。
func (s *SSH) run() {
	defer close(s.done)
	// 穩定用了一陣子的連線斷掉時先立刻重試一次（sshd 沒事、只是這條 TCP 斷了），之後才從 SSHReconnectMin 開始退避；
	// 一連上就斷的話照樣退避，不會變成緊密迴圈。
	var backoff time.Duration
	for {
		cl, err := s.connect()
		if err == nil {
			s.restore(cl)
			s.setOnline(cl)
			stop := make(chan struct{})
			go s.keepalive(cl, stop)
			started := time.Now()
			err = cl.Wait()
			close(stop)
			if time.Since(started) >= limits.SSHStableAfter {
				backoff = 0
			}
			if err == nil {
				err = errors.New("連線被關閉")
			}
		}
		if s.ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrHostKeyMismatch) {
			s.setState(SSHFailed, err)
			s.log.Error("SSH 主機金鑰不符，停止重連", "err", err)
			return
		}
		s.setState(SSHReconnecting, err)
		s.log.Warn("SSH 連線中斷，稍後重連", "err", err, "backoff", backoff)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(max(backoff*2, limits.SSHReconnectMin), limits.SSHReconnectMax)
	}
}

func (s *SSH) setOnline(cl *ssh.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cl = cl
	s.health.State, s.health.Since, s.health.LastError = SSHOnline, time.Now(), ""
	s.health.Connects++
	close(s.online)
	s.log.Info("SSH 主機已連線", "connects", s.health.Connects)
}

func (s *SSH) setState(state string, err error) {
	s.mu.Lock()
	wasOnline := s.cl != nil
	if wasOnline {
		s.online = make(chan struct{})
	}
	s.cl = nil
	s.health.State, s.health.Since = state, time.Now()
	if err != nil {
		s.health.LastError = err.Error()
	}
	if state == SSHFailed || state == SSHClosed {
		// 叫醒所有等待中的呼叫，讓它們看到最終狀態。
		select {
		case <-s.online:
		default:
			close(s.online)
		}
	}
	s.mu.Unlock()
	if wasOnline {
		// 舊連線上閒置的 Docker HTTP 連線都已失效。
		_ = s.docker.Close()
	}
}

func (s *SSH) connect() (*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(s.ctx, limits.SSHDialTimeout)
	defer cancel()
	conn, err := s.o.Dial(ctx, "tcp", s.o.Addr)
	if err != nil {
		return nil, err
	}
	algos, err := s.pinnedAlgorithms()
	if err != nil {
		conn.Close()
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:              s.o.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(s.o.Signer)},
		HostKeyCallback:   s.checkHostKey,
		HostKeyAlgorithms: algos,
		Timeout:           limits.SSHDialTimeout,
	}
	_ = conn.SetDeadline(time.Now().Add(limits.SSHDialTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, s.o.Addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// keepalive 定期送 keepalive@openssh.com；逾時沒回應就關掉連線，讓 run 重連。
func (s *SSH) keepalive(cl *ssh.Client, stop <-chan struct{}) {
	t := time.NewTicker(limits.SSHKeepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-s.ctx.Done():
			cl.Close()
			return
		case <-t.C:
		}
		start := time.Now()
		res := make(chan error, 1)
		go func() {
			_, _, err := cl.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil {
				cl.Close()
				return
			}
			s.mu.Lock()
			s.health.RTT = time.Since(start)
			s.mu.Unlock()
		case <-time.After(limits.SSHKeepaliveTimeout):
			s.log.Warn("SSH keepalive 逾時，關閉連線")
			cl.Close()
			return
		case <-stop:
			return
		}
	}
}

// ── 主機金鑰（first-connect pinning）──────────────────────

func (s *SSH) knownKeys() ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(s.o.KnownHosts)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	want := knownhosts.Normalize(s.o.Addr)
	var keys []ssh.PublicKey
	for len(data) > 0 {
		_, hosts, key, _, rest, err := ssh.ParseKnownHosts(data)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("讀取 %s 失敗：%w", s.o.KnownHosts, err)
		}
		data = rest
		for _, h := range hosts {
			if h == want {
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}

// pinnedAlgorithms 讓握手時要求已記下的那種主機金鑰，避免伺服器改用另一種金鑰而被誤判成不符。
func (s *SSH) pinnedAlgorithms() ([]string, error) {
	keys, err := s.knownKeys()
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	var algos []string
	for _, k := range keys {
		if k.Type() == ssh.KeyAlgoRSA {
			algos = append(algos, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256)
		}
		algos = append(algos, k.Type())
	}
	return algos, nil
}

func (s *SSH) checkHostKey(_ string, _ net.Addr, key ssh.PublicKey) error {
	keys, err := s.knownKeys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		// 第一次連線：記下這台主機的金鑰。
		if err := os.MkdirAll(filepath.Dir(s.o.KnownHosts), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(s.o.KnownHosts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, werr := fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(s.o.Addr)}, key))
		if err := errors.Join(werr, f.Close()); err != nil {
			return err
		}
		s.log.Info("記下 SSH 主機金鑰", "fingerprint", ssh.FingerprintSHA256(key))
		return nil
	}
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), key.Marshal()) {
			return nil
		}
	}
	return fmt.Errorf("%w：收到 %s", ErrHostKeyMismatch, ssh.FingerprintSHA256(key))
}

// ── product.sock 反向轉發（PrepareIPC）──────────────────────

// IPCDir 回傳 Bot 的 product.sock 在遠端的資料夾。
func (s *SSH) IPCDir(botID string) string { return path.Join(s.o.RemoteDir, "bots", botID, "ipc") }

// PrepareIPC 實作 Host：在遠端建立 /var/lib/xbot/bots/<id>/ipc（0711），並把其中的 product.sock 反向轉發回本機。
// 可以重複呼叫（每次開機前都會呼叫）；已經轉發中就什麼都不做。
func (s *SSH) PrepareIPC(botID string) (string, error) {
	dir := s.IPCDir(botID)
	cl, err := s.client(s.ctx)
	if err != nil {
		return "", err
	}
	s.fwdMu.Lock()
	defer s.fwdMu.Unlock()
	f, ok := s.reverse[botID]
	if ok && f.cl == cl && f.ln != nil {
		return dir, nil
	}
	if err := run(cl, "mkdir -p "+shellQuote(dir)+" && chmod 0711 "+shellQuote(dir)); err != nil {
		return "", fmt.Errorf("無法在遠端主機建立 Bot 的通訊資料夾：%w", err)
	}
	if !ok {
		f = &reverseForward{botID: botID, remote: path.Join(dir, "product.sock")}
		s.reverse[botID] = f
	}
	if err := s.listen(cl, f); err != nil {
		return "", err
	}
	return dir, nil
}

// ReleaseIPC 停止 Bot 的反向轉發（刪除電腦時用）。
func (s *SSH) ReleaseIPC(botID string) {
	s.fwdMu.Lock()
	defer s.fwdMu.Unlock()
	if f, ok := s.reverse[botID]; ok {
		if f.ln != nil {
			_ = f.ln.Close()
		}
		delete(s.reverse, botID)
	}
}

// restore 在新連線上重新建立所有反向轉發（舊連線的 listener 已隨連線消失）。
func (s *SSH) restore(cl *ssh.Client) {
	s.fwdMu.Lock()
	defer s.fwdMu.Unlock()
	for _, f := range s.reverse {
		if err := s.listen(cl, f); err != nil {
			s.log.Warn("重連後無法恢復 product.sock 轉發", "bot", f.botID, "err", err)
		}
	}
}

// listen 在 cl 上聽 f.remote；呼叫端持有 fwdMu。
func (s *SSH) listen(cl *ssh.Client, f *reverseForward) error {
	if f.ln != nil {
		_ = f.ln.Close()
		f.ln, f.cl = nil, nil
	}
	ln, err := cl.ListenUnix(f.remote)
	if err != nil {
		return fmt.Errorf("無法在遠端主機建立 product.sock 的反向轉發（sshd 要開 AllowStreamLocalForwarding、StreamLocalBindUnlink）：%w", err)
	}
	f.ln, f.cl = ln, cl
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.bridgeIPC(c, f.botID)
		}
	}()
	return nil
}

func (s *SSH) bridgeIPC(remote net.Conn, botID string) {
	local, err := net.DialTimeout("unix", s.o.LocalSocket(botID), limits.SSHDialTimeout)
	if err != nil {
		s.log.Warn("轉發進來的 product.sock 連線接不上本機", "bot", botID, "err", err)
		remote.Close()
		return
	}
	pipe(remote, local)
}

// ── noVNC／Hermes 埠轉發（Addr）──────────────────────────

// Addr 實作 Host：在本機 127.0.0.1 開一個隨機埠，經 SSH 轉到遠端 127.0.0.1:hostPort。
// 同一個遠端埠沿用同一個本機埠（重連後不變）。開不了本機埠時回傳空字串（視為還沒就緒）。
func (s *SSH) Addr(hostPort int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.local[hostPort]; ok {
		return f.addr
	}
	if s.health.State == SSHClosed {
		return ""
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.log.Warn("無法開本機轉發埠", "port", hostPort, "err", err)
		return ""
	}
	f := &localForward{addr: ln.Addr().String(), ln: ln}
	s.local[hostPort] = f
	target := net.JoinHostPort("127.0.0.1", fmt.Sprint(hostPort))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				cl, err := s.client(s.ctx)
				if err != nil {
					c.Close()
					return
				}
				r, err := cl.Dial("tcp", target)
				if err != nil {
					c.Close()
					return
				}
				pipe(c, r)
			}()
		}
	}()
	return f.addr
}

// ReleaseAddr 關掉某個遠端埠的本機轉發（容器重建、埠號換了時用）。
func (s *SSH) ReleaseAddr(hostPort int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.local[hostPort]; ok {
		f.ln.Close()
		delete(s.local, hostPort)
	}
}

// Close 實作 Host：關掉連線與所有轉發。
func (s *SSH) Close() error {
	s.cancel()
	s.fwdMu.Lock()
	for id, f := range s.reverse {
		if f.ln != nil {
			_ = f.ln.Close()
		}
		delete(s.reverse, id)
	}
	s.fwdMu.Unlock()
	s.mu.Lock()
	cl := s.cl
	for p, f := range s.local {
		f.ln.Close()
		delete(s.local, p)
	}
	s.mu.Unlock()
	if cl != nil {
		cl.Close()
	}
	<-s.done
	s.setState(SSHClosed, errors.New("已關閉"))
	return s.docker.Close()
}

// Run 在遠端主機執行一個指令，回傳合併的輸出（給加入主機時的檢查、測試用）。
func (s *SSH) Run(ctx context.Context, cmd string) (string, error) {
	cl, err := s.client(ctx)
	if err != nil {
		return "", err
	}
	sess, err := cl.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

func run(cl *ssh.Client, cmd string) error {
	sess, err := cl.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("%w：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// pipe 雙向複製直到兩邊都結束；一邊讀完時對另一邊做半關閉（HTTP 等協定需要）。
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

// LoadSigner 讀 OpenSSH 格式的私鑰檔。
func LoadSigner(file string) (ssh.Signer, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}
