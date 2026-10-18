//go:build docker

package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/moby/moby/client"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/ops"
	"xbot/internal/opsclient"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/screen"
	"xbot/internal/store"
)

// slowModel 包住假模型：使用者訊息有「慢慢做」的那次請求先卡住（模擬 Bot 正在用電腦），直到請求被取消（接手時中斷）；
// 記下每次請求的最後一則使用者訊息。
type slowModel struct {
	fake    *hermestest.FakeModel
	mu      sync.Mutex
	prompts []string
	blocked chan struct{}
	once    sync.Once
}

func (m *slowModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		last := ""
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				last = string(msg.Content)
			}
		}
		m.mu.Lock()
		m.prompts = append(m.prompts, last)
		m.mu.Unlock()
		if strings.Contains(last, "慢慢做") && !strings.Contains(last, "使用者已接手") {
			m.once.Do(func() { close(m.blocked) })
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Minute):
			}
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
	}
	m.fake.ServeHTTP(w, r)
}

func (m *slowModel) saw(s string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.prompts {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// TestDockerScreenTakeover（B14，G1 第 2 步）：真的 Bot 電腦。經服務的 WebSocket 看畫面（收到 RFB 版本）、
// 擷取縮圖（JPEG）、Bot 做事時接手（那一輪中斷）、用 control 連線操作、交還帶說明（Bot 收到說明接著做）。
func TestDockerScreenTakeover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	model := &slowModel{fake: &hermestest.FakeModel{}, blocked: make(chan struct{})}
	modelSrv := httptest.NewServer(model)
	defer modelSrv.Close()

	dir, err := os.MkdirTemp("", "xbb14")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	// 和 cmd/xbot/stack.go、registry.go、cmd_serve.go 相同的組裝。
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	mp := proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore})
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") }, Model: mp, Keys: keys})
	defer sockets.Shutdown()
	comp := &runtime.Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore}
	registry := hosts.NewRegistry(local)
	// 和其他套件共用本機的網路防護（xbot-netguard）；網路防護測試在跑時先等它做完（見 guardlock）。
	guardlock.Shared(t)
	boxes := box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	bus := events.New()
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: comp})
	pstore.Attribute = rt.Active
	scr := screen.New(screen.Options{DB: db, Boxes: boxes, Hosts: registry, Runtime: rt, Bus: bus})

	reg := ops.New(ops.Config{DB: db, Notify: func(e store.Event) { bus.Publish(events.FromStore(e)) }})
	ops.RegisterBuiltin(reg)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore, Screen: scr})
	const accessToken = "owner-access-token-b14"
	tokens := &auth.Authenticator{DB: db, AccessToken: accessToken}
	srv := httptest.NewServer(api.New(api.Options{
		ModelProxy: mp, Registry: reg, Bus: bus, DB: db, VNC: scr.ServeVNC,
		Auth: auth.NewHTTP(auth.HTTPConfig{Tokens: tokens, AccessToken: accessToken}),
	}))
	defer srv.Close()

	c := &opsclient.Client{BaseURL: srv.URL, Token: accessToken}
	run := func(op string, in any, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := c.Call(ctx, op, raw, ops.Options{})
		if err != nil {
			t.Fatalf("%s：%v", op, err)
		}
		if out != nil {
			if err := json.Unmarshal(res.Output, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	type settled struct {
		Settled bool   `json:"settled"`
		Verdict string `json:"verdict"`
		Text    string `json:"text"`
	}
	wait := func(taskID string) settled {
		t.Helper()
		var w settled
		for i := 0; i < 3 && !w.Settled; i++ {
			run("xbot.chat.wait_until_settled", map[string]any{"task_id": taskID, "timeout_s": 300}, &w)
		}
		return w
	}

	run("xbot.models.set_provider", map[string]any{"id": "openai", "base_url": modelSrv.URL + "/v1", "api_key": "sk-fake-b14", "default_model": "fake-model"}, nil)
	var bot struct {
		ID string `json:"id"`
	}
	run("xbot.bots.create", map[string]any{"name": "小編"}, &bot)
	name := box.ContainerName(bot.ID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		drv.Close(bot.ID)
		if _, err := boxes.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	}()
	logs := func() string {
		out, _ := exec.Command("docker", "logs", "--tail", "60", name).CombinedOutput()
		return string(out)
	}

	// 第一件工作讓電腦開機。
	var sent struct {
		ThreadID string `json:"thread_id"`
		TaskID   string `json:"task_id"`
	}
	run("xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "回覆 OK"}, &sent)
	if w := wait(sent.TaskID); w.Verdict != "answer" {
		t.Fatalf("開機後的回覆：%+v\n%s", w, logs())
	}
	var sc screen.Screen
	run("xbot.computers.screen", map[string]any{"bot_id": bot.ID}, &sc)
	if sc.Box != box.StateRunning || sc.VNC == "" {
		t.Fatalf("畫面狀態：%+v", sc)
	}

	// 經服務看畫面：WebSocket 收到 RFB 版本。
	dial := func(mode string) *websocket.Conn {
		t.Helper()
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + sc.VNC
		if mode != "" {
			u += "&mode=" + mode
		}
		conn, res, err := websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: []string{"binary"},
			HTTPHeader: http.Header{"Authorization": {"Bearer " + accessToken}}})
		if err != nil {
			code := 0
			if res != nil {
				code = res.StatusCode
			}
			t.Fatalf("連畫面（%s）：%d %v", mode, code, err)
		}
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, banner, err := conn.Read(rctx)
		if err != nil || !strings.HasPrefix(string(banner), "RFB 003.") {
			t.Fatalf("RFB 版本：%q %v", banner, err)
		}
		return conn
	}
	view := dial("")
	if n := scr.Viewers(bot.ID); n != 1 {
		t.Fatalf("在看的人：%d", n)
	}
	// 沒登入不能連。
	if _, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+sc.VNC, nil); err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("沒登入：%v", err)
	}

	// 縮圖：JPEG。
	var snap screen.SnapshotOut
	run("xbot.computers.snapshot", map[string]any{"bot_id": bot.ID}, &snap)
	if !snap.Available || len(snap.Image) < 1000 || snap.Image[0] != 0xFF || snap.Image[1] != 0xD8 {
		t.Fatalf("縮圖：available=%v %d bytes", snap.Available, len(snap.Image))
	}

	// Bot 正在做事時接手：那一輪中斷。
	run("xbot.chat.send", map[string]any{"bot_id": bot.ID, "thread_id": sent.ThreadID, "text": "慢慢做：整理競品價格"}, &sent)
	select {
	case <-model.blocked:
	case <-time.After(2 * time.Minute):
		t.Fatalf("模型沒收到請求\n%s", logs())
	}
	run("xbot.computers.takeover", map[string]any{"bot_id": bot.ID}, &sc)
	if sc.Takeover == nil || !sc.You || sc.Takeover.TaskID != sent.TaskID {
		t.Fatalf("接手：%+v", sc)
	}
	if w := wait(sent.TaskID); w.Verdict != runtime.VerdictCancelled {
		t.Fatalf("接手時那一輪要停下：%+v", w)
	}
	control := dial(screen.ModeControl)

	// 交還：Bot 收到說明，在同一條對話接著做。
	var back screen.HandbackOut
	run("xbot.computers.handback", map[string]any{"bot_id": bot.ID, "note": "輸入了驗證碼"}, &back)
	if back.ResumedTaskID == "" || back.ThreadID != sent.ThreadID {
		t.Fatalf("交還：%+v", back)
	}
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	for {
		if _, _, err := control.Read(rctx); err != nil {
			break // 交還後接手的連線被關掉
		}
	}
	rcancel()
	if w := wait(back.ResumedTaskID); w.Verdict != "answer" {
		t.Fatalf("接著做：%+v\n%s", w, logs())
	}
	if !model.saw("使用者已接手你的電腦並完成：輸入了驗證碼") {
		t.Fatalf("Bot 沒收到交還的說明：%v", model.prompts)
	}
	view.Close(websocket.StatusNormalClosure, "")
}
