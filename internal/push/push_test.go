package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/events"
	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type hit struct {
	path, auth, ttl, urgency string
	n                        int
}

type fakePush struct {
	mu     sync.Mutex
	hits   []hit
	status map[string]int // path → 回應狀態
	srv    *httptest.Server
}

func newFakePush(t *testing.T) *fakePush {
	f := &fakePush{status: map[string]int{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		f.mu.Lock()
		f.hits = append(f.hits, hit{path: r.URL.Path, auth: r.Header.Get("Authorization"), ttl: r.Header.Get("TTL"), urgency: r.Header.Get("Urgency"), n: n})
		st := f.status[r.URL.Path]
		f.mu.Unlock()
		if st == 0 {
			st = http.StatusCreated
		}
		w.WriteHeader(st)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePush) got() []hit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hit(nil), f.hits...)
}

func clientKeys(t *testing.T) (p256dh, auth string) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(a)
}

func setup(t *testing.T) (*Service, *ops.Registry, *fakePush) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := newFakePush(t)
	s := New(db)
	s.Client = f.srv.Client()
	reg := ops.New(ops.Config{DB: db})
	Register(reg, s)
	return s, reg, f
}

func subscribe(t *testing.T, reg *ops.Registry, endpoint string, done bool) string {
	p, a := clientKeys(t)
	in, _ := json.Marshal(SubscribeIn{Endpoint: endpoint, P256dh: p, Auth: a, Label: "Pixel 7", NotifyDone: done})
	res, err := reg.Invoke(context.Background(), ops.Owner("ui"), "xbot.push.subscribe", in, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out SubscribeOut
	_ = json.Unmarshal(res.Output, &out)
	return out.ID
}

func created(t *testing.T, id, kind, title string) events.Event {
	raw, _ := json.Marshal(notify.Payload{ID: id, Kind: kind, Group: notify.Group(kind), Status: "open", Title: title})
	return events.Event{Kind: notify.KindCreated, BotID: "bot1", ThreadID: "th1", Payload: raw}
}

func waitHits(t *testing.T, f *fakePush, n int) []hit {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h := f.got(); len(h) >= n {
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("推播次數 %d，想要 %d", len(f.got()), n)
	return nil
}

func TestPushOnNewInboxCard(t *testing.T) {
	s, reg, f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribe(t, reg, f.srv.URL+"/a", false)
	subscribe(t, reg, f.srv.URL+"/b", true)
	// 同一個 endpoint 再訂一次是更新，不會多一筆。
	subscribe(t, reg, f.srv.URL+"/b", true)
	res, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.push.config", nil, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg ConfigOut
	_ = json.Unmarshal(res.Output, &cfg)
	if cfg.PublicKey == "" || len(cfg.Subscriptions) != 2 || cfg.Subscriptions[1].EndpointHost == "" || strings.Contains(string(res.Output), "p256dh") {
		t.Fatalf("config：%s", res.Output)
	}

	bus := events.New()
	done := make(chan struct{})
	go func() { s.Run(ctx, bus); close(done) }()
	time.Sleep(20 * time.Millisecond) // 等訂閱好

	bus.Publish(created(t, "card1", "approval", "想付款"))
	h := waitHits(t, f, 2)
	for _, x := range h {
		if !strings.HasPrefix(x.auth, "vapid t=") || x.ttl != "43200" || x.urgency != "high" || x.n == 0 {
			t.Fatalf("推播請求不對：%+v", x)
		}
	}
	// 工作完成：只推給勾了 notify_done 的 /b。
	bus.Publish(created(t, "card2", "done", "整理好了"))
	h = waitHits(t, f, 3)
	if h[2].path != "/b" {
		t.Fatalf("工作完成推到 %s", h[2].path)
	}
	// 不推的分類（提到你）。
	bus.Publish(created(t, "card3", "mention", "@你"))
	// 410：訂閱失效就移除。
	f.mu.Lock()
	f.status["/a"] = http.StatusGone
	f.mu.Unlock()
	bus.Publish(created(t, "card4", "question", "要不要比較日本？"))
	waitHits(t, f, 5)
	deadline := time.Now().Add(3 * time.Second)
	for {
		subs, _ := s.List(ctx)
		if len(subs) == 1 && subs[0].Endpoint == f.srv.URL+"/b" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("410 的訂閱沒被移除：%+v", subs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(f.got()); n != 5 {
		t.Fatalf("mention 不該推：共 %d 次", n)
	}
	cancel()
	<-done
}

func TestPushMessageFor(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	m, ok := s.MessageFor(ctx, created(t, "c1", "secret", "請給我 Shopee 的密碼 hunter2"))
	if !ok || strings.Contains(m.Title, "hunter2") || m.Body != "要你決定" || m.URL != "/" {
		t.Fatalf("密碼卡不能帶內容：%+v", m)
	}
	m, ok = s.MessageFor(ctx, created(t, "c2", "done", "做完了"))
	if !ok || m.URL != "/bots/bot1/threads/th1" || m.Kind != "done" {
		t.Fatalf("完成卡：%+v", m)
	}
	if _, ok := s.MessageFor(ctx, events.Event{Kind: notify.KindUpdated}); ok {
		t.Fatal("updated 不該推")
	}
}

func TestPushOpsValidation(t *testing.T) {
	_, reg, f := setup(t)
	ctx := context.Background()
	p, a := clientKeys(t)
	for _, ep := range []string{"http://127.0.0.1:9/x", "file:///etc/passwd", "https://"} {
		in, _ := json.Marshal(SubscribeIn{Endpoint: ep, P256dh: p, Auth: a})
		if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.push.subscribe", in, ops.Options{}); err == nil {
			t.Fatalf("%s 應被拒絕", ep)
		}
	}
	// 代理不能訂閱。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "Claude Code", Via: "mcp", Scopes: []ops.Scope{ops.ScopeAdmin}}
	in, _ := json.Marshal(SubscribeIn{Endpoint: f.srv.URL + "/z", P256dh: p, Auth: a})
	if _, err := reg.Invoke(ctx, agent, "xbot.push.subscribe", in, ops.Options{}); err == nil {
		t.Fatal("代理不能訂閱推播")
	}
	// 測試推播與取消。
	id := subscribe(t, reg, f.srv.URL+"/t", false)
	res, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.push.test", nil, ops.Options{})
	if err != nil || !strings.Contains(string(res.Output), `"sent":1`) {
		t.Fatalf("測試推播：%s %v", res.Output, err)
	}
	res, err = reg.Invoke(ctx, ops.Owner("ui"), "xbot.push.unsubscribe", []byte(`{"id":"`+id+`"}`), ops.Options{})
	if err != nil || !strings.Contains(string(res.Output), `"removed":true`) {
		t.Fatalf("取消：%s %v", res.Output, err)
	}
}
