//go:build docker

// SSH 遠端主機（B11）的整合測試：開一台測試主機容器（dind＋sshd），經一條 SSH 連線在上面開 Bot 電腦，
// 走完 Hermes → hermes_host.py → 遠端 product.sock → 反向轉發 → 本機 product.sock → Model Proxy → 假模型。
// 中途切斷 TCP、殺掉 sshd，確認會自動重連且對話照常。需要 Docker 與 make box 的映像；沒有時略過。
//
// XBOT_B11_BENCH=1 時另外跑 TestSSHForwardBench（docs/spike-ssh-forward.md 的數字）。
package hosts_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
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
	"golang.org/x/crypto/ssh/knownhosts"

	"xbot/internal/box"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/hosts/sshtesthost"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// stack 是和 cmd/xbot/stack.go 相同的組裝，只是預設主機換成 SSH 主機。
type stack struct {
	dir     string
	db      *store.DB
	th      *sshtesthost.Host
	host    *hosts.SSH
	conns   *connLog
	keys    *proxy.WorkerKeys
	sockets *ipc.Server
	boxes   *box.Manager
	drv     *hermes.Driver
	rt      *runtime.Runtime
	fake    *hermestest.FakeModel
	fakeURL string
}

// connLog 記下 SSH 用的 TCP 連線，測試用來從本機這端直接切斷。
type connLog struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (l *connLog) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
	return c, err
}

func (l *connLog) killLast() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns[len(l.conns)-1].Close()
}

func boxImage() string {
	if v := os.Getenv("BOX_IMAGE"); v != "" {
		return v
	}
	return box.DefaultImage
}

func newStack(ctx context.Context, t *testing.T) *stack {
	t.Helper()
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, boxImage()); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", boxImage(), err)
	}
	start := time.Now()
	s := &stack{th: sshtesthost.Start(ctx, t, boxImage()), conns: &connLog{}}
	t.Logf("測試主機 %s 就緒（%s，含載入映像）", s.th.Addr, time.Since(start).Round(time.Millisecond))

	// 短一點的資料目錄：product.sock 的路徑不能超過 108 字元。
	s.dir, err = os.MkdirTemp("", "xbssh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(s.dir) })
	s.db, err = store.Open(ctx, filepath.Join(s.dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	row, err := s.db.CreateHost(ctx, store.Host{Name: "測試遠端", Kind: "ssh", Address: "ssh://xbot@" + s.th.Addr})
	if err != nil {
		t.Fatal(err)
	}

	kr, err := proxy.LoadKeyring(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(s.db)
	providers := proxy.NewProviders(pstore, kr)
	s.keys = proxy.NewWorkerKeys(pstore, kr)
	model := proxy.NewHandler(proxy.Options{Keys: s.keys, Providers: providers, Usage: pstore})
	s.sockets = ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(s.dir, id), "ipc") }, Model: model, Keys: s.keys})
	t.Cleanup(func() { s.sockets.Shutdown() })

	s.host, err = hosts.NewSSH(hosts.SSHOptions{
		ID: row.ID, Addr: s.th.Addr, User: "xbot", Signer: s.th.Signer,
		KnownHosts:  filepath.Join(s.dir, "hosts", row.ID, "known_hosts"),
		LocalSocket: s.sockets.Path, Dial: s.conns.dial,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.host.Close() })
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.host.WaitOnline(wctx); err != nil {
		t.Fatalf("連不上測試主機：%v", err)
	}

	comp := &runtime.Computers{DB: s.db, Keys: s.keys, Sockets: s.sockets, Providers: pstore}
	s.boxes = box.New(s.db, hosts.NewRegistry(s.host), box.Options{Image: boxImage(), Prepare: comp.Prepare})
	comp.Boxes = s.boxes
	s.drv = hermes.New(comp.Connect, nil)
	s.rt = runtime.New(runtime.Options{DB: s.db, Driver: s.drv, Bus: events.New(), Models: comp})
	pstore.Attribute = s.rt.Active

	s.fake = &hermestest.FakeModel{}
	srv := newHTTPServer(t, s.fake)
	s.fakeURL = srv + "/v1"
	if err := providers.Put(ctx, proxy.Provider{ID: "openai", BaseURL: s.fakeURL, APIKey: proxy.Secret("sk-FAKE-REAL-KEY-b11")}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetSetting(ctx, runtime.DefaultModelSetting, runtime.ModelRef{Provider: "openai", Model: "fake-model"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *stack) newBot(ctx context.Context, t *testing.T, name string) store.Bot {
	t.Helper()
	bot, err := s.db.CreateBot(ctx, store.Bot{Name: name, Engine: hermes.Name})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.drv.Close(bot.ID)
		if _, err := s.boxes.Remove(ctx, bot.ID, true); err != nil {
			t.Logf("清理 %s 失敗：%v", bot.ID, err)
		}
	})
	return bot
}

// chat 送一句話並等到完成，回傳花費時間。
func (s *stack) chat(ctx context.Context, t *testing.T, botID, threadID, text string) (string, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := s.rt.Chat(ctx, botID, threadID, text)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.rt.Wait(ctx, res.TaskID)
	if err != nil || task.Verdict != runtime.VerdictAnswer {
		t.Fatalf("對話沒有正常完成：%+v %v\n%s", task, err, s.boxLogs(context.Background(), botID))
	}
	return res.ThreadID, time.Since(start)
}

func (s *stack) boxLogs(ctx context.Context, botID string) string {
	out, _ := s.th.Exec(ctx, "docker", "logs", "--tail", "60", box.ContainerName(botID))
	hl, _ := s.th.Exec(ctx, "docker", "exec", box.ContainerName(botID), "sh", "-c", "tail -n 40 /hermes/logs/*.log")
	return out + "\n── Hermes 日誌 ──\n" + hl
}

// waitConnects 等到 SSH 主機重連成功（Connects 達到 n），回傳花了多久。
func (s *stack) waitConnects(t *testing.T, n int, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		h := s.host.Health()
		if h.State == hosts.SSHOnline && h.Connects >= n {
			return time.Since(start)
		}
		if time.Since(start) > timeout {
			t.Fatalf("沒有重連：%+v", h)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSSHHostEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	s := newStack(ctx, t)

	// 第一次連線時記下主機金鑰。
	kh, err := os.ReadFile(filepath.Join(s.dir, "hosts", s.host.ID(), "known_hosts"))
	if err != nil || !strings.Contains(string(kh), "ssh-ed25519") {
		t.Fatalf("known_hosts：%q %v", kh, err)
	}

	bot := s.newBot(ctx, t, "遠端")
	thread, took := s.chat(ctx, t, bot.ID, "", "回覆 OK")
	t.Logf("第一次對話（含開機）：%s", took.Round(time.Millisecond))
	if s.fake.ChatCalls() == 0 {
		t.Fatal("假模型應該被呼叫")
	}
	usage, _ := s.db.ListUsage(ctx, store.UsageFilter{BotID: bot.ID})
	if len(usage) == 0 {
		t.Fatal("用量應該記下來")
	}

	// 電腦真的在遠端：本機 Docker 沒有這個容器，遠端有；product.sock 是 sshd 建的 0666 socket。
	if out, err := exec.CommandContext(ctx, "docker", "inspect", box.ContainerName(bot.ID)).CombinedOutput(); err == nil {
		t.Fatalf("容器不應該在本機：%s", out)
	}
	sock := s.host.IPCDir(bot.ID) + "/product.sock"
	if out, err := s.th.Exec(ctx, "stat", "-c", "%A %U", sock); err != nil || !strings.HasPrefix(out, "srw-rw-rw-") || !strings.HasSuffix(out, "xbot") {
		t.Fatalf("遠端 product.sock：%q %v", out, err)
	}
	st, err := s.boxes.Status(ctx, bot.ID)
	if err != nil || !strings.HasPrefix(st.NoVNCAddr, "127.0.0.1:") || st.NoVNCAddr == "127.0.0.1:"+itoa(st.NoVNCPort) {
		t.Fatalf("noVNC 應該經本機轉發埠：%+v %v", st, err)
	}

	// 1. 從本機這端直接切斷 TCP。
	before := s.fake.ChatCalls()
	s.conns.killLast()
	t.Logf("切斷 TCP 後重連：%s", s.waitConnects(t, 2, time.Minute).Round(time.Millisecond))
	_, took = s.chat(ctx, t, bot.ID, thread, "再回覆一次 OK")
	t.Logf("重連後對話：%s", took.Round(time.Millisecond))

	// 2. 遠端 sshd 整個被殺掉（含這條連線的行程），幾百毫秒後重新啟動。
	if _, err := s.th.Exec(ctx, "pkill", "sshd"); err != nil {
		t.Fatalf("pkill sshd：%v", err)
	}
	t.Logf("sshd 重啟後重連：%s", s.waitConnects(t, 3, time.Minute).Round(time.Millisecond))
	_, took = s.chat(ctx, t, bot.ID, thread, "第三次回覆 OK")
	t.Logf("sshd 重啟後對話：%s", took.Round(time.Millisecond))
	if s.fake.ChatCalls() <= before {
		t.Fatal("重連後模型請求應該照常經反向轉發")
	}
	h := s.host.Health()
	t.Logf("健康狀態：%+v", h)
	if h.ReverseForwards != 1 || h.LocalForwards < 2 {
		t.Fatalf("轉發數：%+v", h)
	}

	// 主機金鑰不同就拒絕連線，而且不再重試。
	other, err := hosts.NewSSH(hosts.SSHOptions{
		ID: "fake", Addr: s.th.Addr, User: "xbot", Signer: s.th.Signer,
		KnownHosts: wrongKnownHosts(t, s.th.Addr), LocalSocket: s.sockets.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
	defer wcancel()
	if err := other.WaitOnline(wctx); err == nil || other.Health().State != hosts.SSHFailed || !strings.Contains(other.Health().LastError, hosts.ErrHostKeyMismatch.Error()) {
		t.Fatalf("主機金鑰不同應該拒絕：%v %+v", err, other.Health())
	}
}

func wrongKnownHosts(t *testing.T, addr string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func newHTTPServer(t *testing.T, h http.Handler) string {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func itoa(n int) string { return strconv.Itoa(n) }
