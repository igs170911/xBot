package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"xbot/internal/box"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

type fixedModel struct{}

func (fixedModel) ResolveModel(context.Context, store.Bot) (runtime.ModelRef, error) {
	return runtime.ModelRef{Provider: "openai", Model: "gpt-x"}, nil
}

// fakeBoxes 是假的 Box Manager：電腦開著，noVNC 位址指到假的 websockify。
type fakeBoxes struct {
	mu      sync.Mutex
	running bool
	addr    string
	touches atomic.Int32
	holds   atomic.Int32
}

func (f *fakeBoxes) Status(_ context.Context, botID string) (box.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := box.Status{BotID: botID, HostID: "local", ContainerID: "c1", State: box.StateSleeping}
	if f.running {
		s.State, s.Running, s.NoVNCAddr = box.StateRunning, true, f.addr
	}
	return s, nil
}

func (f *fakeBoxes) Touch(context.Context, string) error { f.touches.Add(1); return nil }

func (f *fakeBoxes) Hold(string) func() {
	f.holds.Add(1)
	return func() { f.holds.Add(-1) }
}

// fakeVNC 是假的 websockify：連上就送 RFB 版本，收到的位元組都記下來。
type fakeVNC struct {
	srv *httptest.Server
	mu  sync.Mutex
	got bytes.Buffer
}

func newFakeVNC(t *testing.T) *fakeVNC {
	v := &fakeVNC{}
	v.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		if err := c.Write(r.Context(), websocket.MessageBinary, []byte("RFB 003.008\n")); err != nil {
			return
		}
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			v.mu.Lock()
			v.got.Write(data)
			v.mu.Unlock()
		}
	}))
	t.Cleanup(v.srv.Close)
	return v
}

func (v *fakeVNC) received() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	return bytes.Clone(v.got.Bytes())
}

type fixture struct {
	db    *store.DB
	g     *hermestest.Gateway
	rt    *runtime.Runtime
	boxes *fakeBoxes
	vnc   *fakeVNC
	svc   *Service
	reg   *ops.Registry
	srv   *httptest.Server
	bot   store.Bot
	ids   map[string]ops.Identity
	now   atomic.Int64
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, g: hermestest.New("k"), vnc: newFakeVNC(t)}
	t.Cleanup(f.g.Close)
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: f.g.Addr(), Key: "k"}, nil
	}, nil)
	bus := events.New()
	f.rt = runtime.New(runtime.Options{DB: db, Driver: d, Bus: bus, Models: fixedModel{}})
	f.boxes = &fakeBoxes{running: true, addr: strings.TrimPrefix(f.vnc.srv.URL, "http://")}
	f.now.Store(time.Now().UnixMilli())
	f.svc = New(Options{DB: db, Boxes: f.boxes, Runtime: f.rt, Bus: bus, TakeoverIdle: time.Minute,
		Now: func() time.Time { return time.UnixMilli(f.now.Load()) }})
	f.reg = ops.New(ops.Config{DB: db, Notify: func(e store.Event) { bus.Publish(events.FromStore(e)) }})
	Register(f.reg, f.svc)
	if f.bot, err = db.CreateBot(ctx, store.Bot{Name: "小編"}); err != nil {
		t.Fatal(err)
	}
	f.ids = map[string]ops.Identity{
		"owner":   ops.Owner("ui"),
		"reader":  {Kind: ops.KindAgent, ID: "a1", Name: "唯讀代理", Scopes: []ops.Scope{ops.ScopeRead}},
		"other":   {Kind: ops.KindAgent, ID: "a2", Name: "別的 Bot 的代理", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}, Resources: ops.Resources{Bots: []string{"別的"}}},
		"operate": {Kind: ops.KindAgent, ID: "a3", Name: "操作代理", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}},
		"bot":     ops.Bot(f.bot.ID, "小編"),
	}
	// 測試用：X-Test-Id 指定身分（真的服務由 api 的 guard 驗證）。
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.svc.ServeVNC(w, r, f.ids[r.Header.Get("X-Test-Id")])
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) dial(t *testing.T, who, query string) (*websocket.Conn, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(f.srv.URL, "http") + VNCPath + "?" + query
	c, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"binary"}, HTTPHeader: http.Header{"X-Test-Id": {who}}})
	if err != nil {
		if res != nil {
			return nil, res.StatusCode
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c, http.StatusSwitchingProtocols
}

func (f *fixture) call(t *testing.T, who, op string, in any, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := f.reg.Invoke(context.Background(), f.ids[who], op, raw, ops.Options{})
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return nil
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("等不到：" + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var (
	rfbHello = []byte("RFB 003.008\n\x01\x01")
	rfbReq   = []byte{msgFBUpdateRequest, 0, 0, 0, 0, 0, 0, 10, 0, 10}
	rfbKey   = []byte{msgKeyEvent, 1, 0, 0, 0, 0, 0, 0x61}
)

func readBanner(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil || string(b) != "RFB 003.008\n" {
		t.Fatalf("RFB 版本：%q %v", b, err)
	}
}

func send(t *testing.T, c *websocket.Conn, parts ...[]byte) {
	t.Helper()
	for _, p := range parts {
		if err := c.Write(context.Background(), websocket.MessageBinary, p); err != nil {
			t.Fatal(err)
		}
	}
}

// 誰能看：你本人可以；代理要有 operate 而且能碰這個 Bot；電腦沒開機、還沒接手就要 control 都不行。
// 只能看的連線：要畫面的訊息送到電腦，鍵盤被丟掉；有人在看時記錄電腦正在使用。
func TestVNCAuthAndViewOnly(t *testing.T) {
	f := setup(t)
	q := "bot=" + f.bot.ID
	for who, want := range map[string]int{"reader": 403, "other": 403, "bot": 403, "": 403} {
		if _, code := f.dial(t, who, q); code != want {
			t.Errorf("%s：%d", who, code)
		}
	}
	if _, code := f.dial(t, "owner", "bot=沒有這個"); code != 404 {
		t.Errorf("沒有這個 Bot：%d", code)
	}
	if _, code := f.dial(t, "owner", q+"&mode=control"); code != 409 {
		t.Errorf("還沒接手就要操作：%d", code)
	}
	f.boxes.mu.Lock()
	f.boxes.running = false
	f.boxes.mu.Unlock()
	if _, code := f.dial(t, "owner", q); code != 409 {
		t.Errorf("沒開機：%d", code)
	}
	f.boxes.mu.Lock()
	f.boxes.running = true
	f.boxes.mu.Unlock()

	c, code := f.dial(t, "operate", q)
	if code != 101 {
		t.Fatalf("有 operate 的代理可以看：%d", code)
	}
	readBanner(t, c)
	send(t, c, rfbHello, rfbKey, rfbReq)
	want := append(append([]byte{}, rfbHello...), rfbReq...)
	eventually(t, "要畫面的訊息", func() bool { return bytes.Equal(f.vnc.received(), want) })
	if f.svc.Viewers(f.bot.ID) != 1 || f.boxes.touches.Load() == 0 || f.boxes.holds.Load() != 1 {
		t.Fatalf("看的人數 %d、touch %d", f.svc.Viewers(f.bot.ID), f.boxes.touches.Load())
	}
	c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "離開", func() bool { return f.svc.Viewers(f.bot.ID) == 0 && f.boxes.holds.Load() == 0 })
}

// 接手的狀態機（假的引擎）：Bot 正在做事時接手 → 那一輪中斷、對話記一行；別人不能搶；接手的人可以操作畫面；
// 只有接手的人（或你本人）能交還；交還時接手的連線被關掉，Bot 帶著說明在同一條對話接著做。
func TestTakeoverAndHandback(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var prompts []string
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		mu.Lock()
		prompts = append(prompts, tr.Text)
		n := len(prompts)
		mu.Unlock()
		if n == 1 {
			tr.Event("message.start", nil)
			started <- struct{}{}
			<-tr.Interrupted()
			tr.Complete("", "interrupted", nil)
			return
		}
		hermestest.ReplyOK(tr)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我發文")
	if err != nil {
		t.Fatal(err)
	}
	<-started

	var sc Screen
	if err := f.call(t, "operate", "xbot.computers.takeover", BotIn{BotID: f.bot.ID}, &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Takeover == nil || !sc.You || !sc.Paused || sc.Takeover.TaskID != res.TaskID || sc.Working {
		t.Fatalf("接手後：%+v", sc)
	}
	if task, _ := f.db.GetTask(ctx, res.TaskID); task.Verdict != runtime.VerdictCancelled {
		t.Fatalf("那一輪要中斷：%+v", task)
	}
	if err := f.call(t, "owner", "xbot.computers.takeover", BotIn{BotID: f.bot.ID}, nil); ops.AsError(err).Code != ops.CodeConflict {
		t.Fatalf("別人正在操作：%v", err)
	}
	// 你本人看得到「別人在操作」，但 control 連線只給接手的代理。
	if _, code := f.dial(t, "owner", "bot="+f.bot.ID+"&mode=control"); code != 403 {
		t.Fatalf("不是接手的人：%d", code)
	}
	c, code := f.dial(t, "operate", "bot="+f.bot.ID+"&mode=control")
	if code != 101 {
		t.Fatalf("接手的人可以操作：%d", code)
	}
	readBanner(t, c)
	send(t, c, rfbHello, rfbKey)
	want := append(append([]byte{}, rfbHello...), rfbKey...)
	eventually(t, "鍵盤送到電腦", func() bool { return bytes.Equal(f.vnc.received(), want) })

	if _, err := f.svc.Handback(ctx, f.bot.ID, "agent:a1", ""); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("別的代理不能交還：%v", err)
	}
	var out HandbackOut
	if err := f.call(t, "operate", "xbot.computers.handback", HandbackIn{BotID: f.bot.ID, Note: "輸入了驗證碼"}, &out); err != nil {
		t.Fatal(err)
	}
	eventually(t, "接手的連線關掉、不再 hold", func() bool { return f.boxes.holds.Load() == 0 })
	if out.ResumedTaskID == "" || out.ThreadID != res.ThreadID || out.Screen.Takeover != nil {
		t.Fatalf("交還：%+v", out)
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(rctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("交還後接手的連線要關掉：%v", err)
	}
	task, err := f.rt.Wait(rctx, out.ResumedTaskID)
	if err != nil || task.ThreadID != res.ThreadID {
		t.Fatalf("接著做：%+v %v", task, err)
	}
	mu.Lock()
	second := prompts[1]
	mu.Unlock()
	if !strings.Contains(second, "使用者已接手你的電腦並完成：輸入了驗證碼") {
		t.Fatalf("送給 Bot 的話：%q", second)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var lines []string
	for _, m := range msgs {
		if m.Kind == runtime.KindActivity {
			lines = append(lines, m.Content)
		}
	}
	if strings.Join(lines, "|") != "你接手了電腦，小編先暫停|已交還給小編：輸入了驗證碼" {
		t.Fatalf("對話裡的系統行：%v", lines)
	}
	if err := f.call(t, "owner", "xbot.computers.handback", HandbackIn{BotID: f.bot.ID}, nil); ops.AsError(err).Code != ops.CodeConflict {
		t.Fatalf("沒在接手：%v", err)
	}
}

// Bot 請你接手（登入、驗證碼）：待我處理出現 takeover 卡（同一件事不重開）；你接手、交還時 Bot 在原本那條對話接著做，卡片收掉。
// 接手後一直沒人連著畫面：自動交還。
func TestRequestTakeoverAndAutoHandback(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	var out RequestOut
	reqErr := make(chan error, 1)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		if strings.Contains(tr.Text, "發文") {
			// Bot 這一輪中呼叫 request_takeover（經 product.sock，身分就是它自己），再結束這一輪。
			reqErr <- f.call(t, "bot", "xbot.computers.request_takeover", RequestIn{Reason: "Threads 要輸入簡訊驗證碼"}, &out)
			tr.Complete("需要你輸入驗證碼", "complete", nil)
			return
		}
		hermestest.ReplyOK(tr)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我發文")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-reqErr; err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := f.rt.Wait(wctx, res.TaskID); err != nil {
		t.Fatal(err)
	}
	card, err := f.db.GetInboxItem(ctx, out.InboxID)
	if err != nil || card.Kind != InboxKind || card.ThreadID != res.ThreadID || card.Body != "Threads 要輸入簡訊驗證碼" || card.Title != "小編需要你接手電腦" {
		t.Fatalf("接手卡：%+v %v", card, err)
	}
	var again RequestOut
	if err := f.call(t, "bot", "xbot.computers.request_takeover", RequestIn{Reason: "再一次"}, &again); err != nil || !again.AlreadyPending || again.InboxID != out.InboxID {
		t.Fatalf("同一件事不重開：%+v %v", again, err)
	}
	if err := f.call(t, "owner", "xbot.computers.request_takeover", RequestIn{Reason: "x"}, nil); ops.AsError(err).Code != ops.CodeForbidden {
		t.Fatalf("只有 Bot 能請你接手：%v", err)
	}
	var sc Screen
	if err := f.call(t, "owner", "xbot.computers.screen", BotIn{BotID: f.bot.ID}, &sc); err != nil || sc.Request == nil || sc.Request.InboxID != out.InboxID {
		t.Fatalf("畫面狀態帶著請求：%+v %v", sc, err)
	}

	if err := f.call(t, "owner", "xbot.computers.takeover", BotIn{BotID: f.bot.ID}, &sc); err != nil || sc.Takeover.TaskID != "" {
		t.Fatalf("Bot 沒在做事時接手：%+v %v", sc, err)
	}
	// 30 分鐘（測試：1 分鐘）沒有人連著畫面：自動交還，帶預設說明，在 Bot 請你接手的那條對話接著做。
	f.svc.expire(ctx)
	if f.svc.TakeoverOf(f.bot.ID) == nil {
		t.Fatal("還沒到時間")
	}
	f.now.Add(time.Minute.Milliseconds())
	f.svc.expire(ctx)
	if f.svc.TakeoverOf(f.bot.ID) != nil {
		t.Fatal("應該自動交還了")
	}
	var resumed string
	eventually(t, "接著做", func() bool {
		tasks, _ := f.db.ListTasks(ctx, f.bot.ID, false)
		for _, task := range tasks {
			if task.ID != res.TaskID && task.ThreadID == res.ThreadID && strings.Contains(task.Input, DefaultNote) {
				resumed = task.ID
				return true
			}
		}
		return false
	})
	if _, err := f.rt.Wait(wctx, resumed); err != nil {
		t.Fatal(err)
	}
	card, _ = f.db.GetInboxItem(ctx, out.InboxID)
	if card.Status != store.InboxResolved || card.Resolution != ResolutionHandedBack {
		t.Fatalf("交還後卡片收掉：%+v", card)
	}
}

// 縮圖：電腦沒開機時 available=false（不叫醒）；短時間內重複要求回同一張。
func TestSnapshot(t *testing.T) {
	f := setup(t)
	var shots atomic.Int32
	f.svc.o.Snapshot = func(context.Context, hosts.DockerAPI, string) ([]byte, error) {
		shots.Add(1)
		return []byte{0xFF, 0xD8, 0xFF, 0xD9}, nil
	}
	f.svc.o.Hosts = fakeHosts{}
	var out SnapshotOut
	if err := f.call(t, "owner", "xbot.computers.snapshot", BotIn{BotID: f.bot.ID}, &out); err != nil || !out.Available || out.MediaType != "image/jpeg" || len(out.Image) != 4 {
		t.Fatalf("縮圖：%+v %v", out, err)
	}
	_ = f.call(t, "owner", "xbot.computers.snapshot", BotIn{BotID: f.bot.ID}, &out)
	if shots.Load() != 1 {
		t.Fatalf("短時間內不要重複擷取：%d", shots.Load())
	}
	if err := f.call(t, "reader", "xbot.computers.snapshot", BotIn{BotID: f.bot.ID}, nil); ops.AsError(err).Code != ops.CodeForbidden {
		t.Fatalf("唯讀代理不能看縮圖：%v", err)
	}
	f.boxes.mu.Lock()
	f.boxes.running = false
	f.boxes.mu.Unlock()
	f.svc.Forget(f.bot.ID)
	if err := f.call(t, "owner", "xbot.computers.snapshot", BotIn{BotID: f.bot.ID}, &out); err != nil || out.Available {
		t.Fatalf("沒開機：%+v %v", out, err)
	}
}

type fakeHosts struct{}

func (fakeHosts) Get(string) (hosts.Host, error) { return fakeHost{}, nil }

type fakeHost struct{ hosts.Host }

func (fakeHost) Docker() hosts.DockerAPI { return nil }
