package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/auth"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

const testToken = "the-access-token"

type env struct {
	db      *store.DB
	reg     *ops.Registry
	bus     *events.Bus
	auth    *auth.HTTP
	h       http.Handler
	creates atomic.Int32
}

type makeIn struct {
	Name  string `json:"name" required:"true" max:"20"`
	BotID string `json:"bot_id,omitempty" resource:"bot"`
}

type makeOut struct {
	Name    string `json:"name"`
	N       int32  `json:"n"`
	Preview bool   `json:"preview,omitempty"`
}

type failIn struct {
	Code string `json:"code" enum:"forbidden,not_found,invalid_input,limit,budget,conflict,internal"`
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db}
	e.bus = events.New()
	e.reg = ops.New(ops.Config{DB: db, Notify: func(ev store.Event) { e.bus.Publish(events.FromStore(ev)) }})
	ops.RegisterBuiltin(e.reg)
	ops.Register(e.reg, ops.Op[makeIn, makeOut]{
		Name: "xbot.test.make", Summary: "建立測試資料", Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in makeIn) (makeOut, error) {
			if call.DryRun {
				return makeOut{Name: in.Name, Preview: true}, nil
			}
			return makeOut{Name: in.Name, N: e.creates.Add(1)}, nil
		},
	})
	ops.Register(e.reg, ops.Op[makeIn, makeOut]{
		Name: "xbot.test.destroy", Summary: "刪掉測試資料", Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in makeIn) (makeOut, error) {
			return makeOut{Name: in.Name}, nil
		},
	})
	ops.Register(e.reg, ops.Op[failIn, makeOut]{
		Name: "xbot.test.fail", Summary: "回指定的錯誤", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in failIn) (makeOut, error) {
			if in.Code == "internal" {
				return makeOut{}, fmt.Errorf("壞掉了")
			}
			return makeOut{}, ops.Errorf(ops.Code(in.Code), "測試錯誤")
		},
	})
	e.auth = auth.NewHTTP(auth.HTTPConfig{DB: db, AccessToken: testToken, LocalNoAuth: true})
	e.h = New(Options{Registry: e.reg, Bus: e.bus, Auth: e.auth, Heartbeat: 50 * time.Millisecond, DB: db})
	return e
}

// appendEvent 寫一筆事件再發布（同 runtime 的做法）。
func (e *env) appendEvent(t *testing.T, ev store.Event) {
	t.Helper()
	row, err := e.db.AppendEvent(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	e.bus.Publish(events.FromStore(row))
}

type req struct {
	method, path, body string
	local              bool
	header             map[string]string
	cookie             *http.Cookie
	remote             string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodPost
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	if r.body != "" {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.local {
		hr.RemoteAddr = "127.0.0.1:40000"
		hr.Host = "127.0.0.1:8780"
	} else {
		hr.RemoteAddr = "192.0.2.10:40000"
		if r.remote != "" {
			hr.RemoteAddr = r.remote
		}
		hr.Host = "xbot.example.ts.net"
	}
	for k, v := range r.header {
		hr.Header.Set(k, v)
	}
	if r.cookie != nil {
		hr.AddCookie(r.cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, hr)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("回應不是 JSON（%d）：%s", rec.Code, rec.Body.String())
	}
	return v
}

type result struct {
	Op       string          `json:"op"`
	Output   json.RawMessage `json:"output"`
	DryRun   bool            `json:"dry_run"`
	Replayed bool            `json:"replayed"`
	Error    *ops.Error      `json:"error"`
}

func TestRoutesFromRegistry(t *testing.T) {
	e := newEnv(t)
	for _, info := range e.reg.List() {
		body := `{}`
		if info.Name == "xbot.test.make" || info.Name == "xbot.test.destroy" || info.Name == "xbot.settings.get" {
			body = `{"name":"x"}`
		}
		rec := e.do(req{path: Route(info.Name), body: body, local: true})
		if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "沒有這個 API") {
			t.Fatalf("%s 沒有路由", info.Name)
		}
	}
	if Route("xbot.bots.create") != "/api/v1/bots.create" {
		t.Fatal(Route("xbot.bots.create"))
	}
	rec := e.do(req{path: "/api/v1/nope.nothing", body: `{}`, local: true})
	if rec.Code != 404 || decode[result](t, rec).Error.Code != ops.CodeNotFound {
		t.Fatalf("不存在的操作：%d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(req{method: "GET", path: "/api/v1/ops", local: true})
	var list struct {
		Version string `json:"version"`
		Ops     []struct {
			Name  string          `json:"name"`
			Path  string          `json:"path"`
			Input json.RawMessage `json:"input_schema"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 {
		t.Fatalf("ops 清單：%d %s", rec.Code, rec.Body.String())
	}
	if list.Version != ops.Version || len(list.Ops) != len(e.reg.List()) {
		t.Fatalf("ops 清單內容不對：%+v", list)
	}
	for _, o := range list.Ops {
		if o.Name == "xbot.test.make" && (o.Path != "/api/v1/test.make" || !strings.Contains(string(o.Input), `"required":["name"]`)) {
			t.Fatalf("test.make 的描述不對：%+v %s", o, o.Input)
		}
	}
}

func TestPingAsOwner(t *testing.T) {
	e := newEnv(t)
	rec := e.do(req{path: "/api/v1/system.ping", body: `{"echo":"hi"}`, local: true})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out ops.PingOut
	if err := json.Unmarshal(decode[result](t, rec).Output, &out); err != nil || !out.Pong || out.Echo != "hi" || out.Caller != "owner" {
		t.Fatalf("ping 結果不對：%s", rec.Body.String())
	}
	// 空白內容當成 {}，不需要 Content-Type。
	if rec := e.do(req{path: "/api/v1/system.ping", local: true}); rec.Code != 200 {
		t.Fatalf("空白內容：%d %s", rec.Code, rec.Body.String())
	}
}

func TestStatusMapping(t *testing.T) {
	e := newEnv(t)
	want := map[string]int{"forbidden": 403, "not_found": 404, "invalid_input": 400, "limit": 429, "budget": 402, "conflict": 409, "internal": 500}
	for code, status := range want {
		rec := e.do(req{path: "/api/v1/test.fail", body: `{"code":"` + code + `"}`, local: true})
		res := decode[result](t, rec)
		if rec.Code != status || res.Error == nil || string(res.Error.Code) != code {
			t.Fatalf("%s：%d %s", code, rec.Code, rec.Body.String())
		}
	}
	if StatusFor(ops.CodePendingApproval) != 202 {
		t.Fatal("pending_approval 應該是 202")
	}
	// 輸入驗證失敗：400，列出欄位。
	rec := e.do(req{path: "/api/v1/test.make", body: `{"name":""}`, local: true})
	if res := decode[result](t, rec); rec.Code != 400 || len(res.Error.Fields) == 0 {
		t.Fatalf("輸入錯誤：%d %s", rec.Code, rec.Body.String())
	}
	// 內部錯誤不外洩原因。
	rec = e.do(req{path: "/api/v1/test.fail", body: `{"code":"internal"}`, local: true})
	if strings.Contains(rec.Body.String(), "壞掉了") || !decode[result](t, rec).Error.Retryable {
		t.Fatalf("內部錯誤：%s", rec.Body.String())
	}
	// 不是 JSON 的內容。
	rec = e.do(req{path: "/api/v1/test.make", body: `{"name":`, local: true})
	if rec.Code != 400 {
		t.Fatalf("壞掉的 JSON：%d", rec.Code)
	}
}

func TestDryRunHeader(t *testing.T) {
	e := newEnv(t)
	rec := e.do(req{path: "/api/v1/test.make", body: `{"name":"a"}`, local: true, header: map[string]string{HeaderDryRun: "1"}})
	res := decode[result](t, rec)
	if rec.Code != 200 || !res.DryRun || !strings.Contains(string(res.Output), `"preview":true`) || e.creates.Load() != 0 {
		t.Fatalf("預演：%d %s（建立了 %d 次）", rec.Code, rec.Body.String(), e.creates.Load())
	}
	if rec.Header().Get(HeaderRequestID) != "" {
		t.Fatal("預演不需要 request id")
	}
}

func TestRequestIDReplay(t *testing.T) {
	e := newEnv(t)
	h := map[string]string{HeaderRequestID: "req-1"}
	first := e.do(req{path: "/api/v1/test.make", body: `{"name":"a"}`, local: true, header: h})
	second := e.do(req{path: "/api/v1/test.make", body: `{"name": "a"}`, local: true, header: h})
	if first.Code != 200 || second.Code != 200 || e.creates.Load() != 1 {
		t.Fatalf("重送應該只做一次：%d %d 建立 %d 次", first.Code, second.Code, e.creates.Load())
	}
	if r := decode[result](t, second); !r.Replayed || string(r.Output) != string(decode[result](t, first).Output) {
		t.Fatalf("重送應該回第一次的結果：%s", second.Body.String())
	}
	if second.Header().Get(HeaderRequestID) != "req-1" {
		t.Fatal("要回傳 X-Request-Id")
	}
	// 同一個 id 用在不同內容：409。
	if rec := e.do(req{path: "/api/v1/test.make", body: `{"name":"b"}`, local: true, header: h}); rec.Code != 409 {
		t.Fatalf("不同內容：%d %s", rec.Code, rec.Body.String())
	}
	// 沒帶 id：服務產生一個，用它重送也不會重做。
	rec := e.do(req{path: "/api/v1/test.make", body: `{"name":"c"}`, local: true})
	gen := rec.Header().Get(HeaderRequestID)
	if gen == "" {
		t.Fatal("會改東西的操作沒帶 id 時要產生一個")
	}
	e.do(req{path: "/api/v1/test.make", body: `{"name":"c"}`, local: true, header: map[string]string{HeaderRequestID: gen}})
	if e.creates.Load() != 2 {
		t.Fatalf("用產生的 id 重送不該重做，建立了 %d 次", e.creates.Load())
	}
	// 查看類操作不產生 id。
	if rec := e.do(req{path: "/api/v1/system.ping", local: true}); rec.Header().Get(HeaderRequestID) != "" {
		t.Fatal("查看類操作不需要 request id")
	}
}

func newAgentToken(t *testing.T, e *env, scopes []string, resources string) string {
	t.Helper()
	raw, hash := auth.NewAgentToken()
	if _, err := e.db.CreateAgentToken(context.Background(), store.AgentToken{Name: "Claude Code", TokenHash: hash, Scopes: scopes, Resources: json.RawMessage(resources)}); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPendingApprovalOverHTTP(t *testing.T) {
	e := newEnv(t)
	tok := newAgentToken(t, e, []string{"read", "manage"}, "")
	rec := e.do(req{path: "/api/v1/test.destroy", body: `{"name":"x"}`, header: map[string]string{"Authorization": "Bearer " + tok}})
	res := decode[result](t, rec)
	if rec.Code != 202 || res.Error == nil || res.Error.Code != ops.CodePendingApproval || res.Error.NeedsApprovalID == "" {
		t.Fatalf("confirm 動作要回 202 pending_approval：%d %s", rec.Code, rec.Body.String())
	}
	// 權限不足：403。
	readOnly := newAgentToken(t, e, []string{"read"}, "")
	rec = e.do(req{path: "/api/v1/test.make", body: `{"name":"x"}`, header: map[string]string{"Authorization": "Bearer " + readOnly}})
	if rec.Code != 403 {
		t.Fatalf("權限不足：%d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(req{path: "/api/v1/system.ping", header: map[string]string{"Authorization": "Bearer " + readOnly}})
	var out ops.PingOut
	_ = json.Unmarshal(decode[result](t, rec).Output, &out)
	if rec.Code != 200 || !strings.HasPrefix(out.Caller, "agent:") {
		t.Fatalf("代理 ping：%d %s", rec.Code, rec.Body.String())
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	ping := "/api/v1/system.ping"

	// 本機免登入；從遠端來要登入。
	if rec := e.do(req{path: ping, local: true}); rec.Code != 200 {
		t.Fatalf("本機：%d", rec.Code)
	}
	if rec := e.do(req{path: ping}); rec.Code != 401 || decode[result](t, rec).Error.Code != ops.CodeForbidden {
		t.Fatalf("遠端沒登入：%d %s", rec.Code, rec.Body.String())
	}
	// 經過本機反向代理（例：tailscale serve）的不算本機。
	if rec := e.do(req{path: ping, local: true, header: map[string]string{"X-Forwarded-For": "100.64.0.9"}}); rec.Code != 401 {
		t.Fatalf("反向代理：%d", rec.Code)
	}
	// DNS rebinding：對方是 127.0.0.1 但 Host 不是 localhost。
	hr := httptest.NewRequest("POST", ping, nil)
	hr.RemoteAddr, hr.Host = "127.0.0.1:1", "evil.example.com"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, hr)
	if rec.Code != 401 {
		t.Fatalf("DNS rebinding：%d", rec.Code)
	}
	// 跨站請求一律擋。
	if rec := e.do(req{path: ping, local: true, header: map[string]string{"Origin": "https://evil.example.com"}}); rec.Code != 403 {
		t.Fatalf("跨站：%d", rec.Code)
	}
	if rec := e.do(req{path: ping, local: true, header: map[string]string{"Origin": "http://127.0.0.1:8780"}}); rec.Code != 200 {
		t.Fatalf("同源：%d", rec.Code)
	}
	// 有內容但不是 JSON（表單）：400。
	hr = httptest.NewRequest("POST", ping, strings.NewReader("echo=1"))
	hr.RemoteAddr, hr.Host = "127.0.0.1:1", "localhost:8780"
	hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, hr)
	if rec.Code != 400 {
		t.Fatalf("表單內容：%d", rec.Code)
	}

	// Bearer 登入碼 → 使用者本人。
	if rec := e.do(req{path: ping, header: map[string]string{"Authorization": "Bearer " + testToken}}); rec.Code != 200 {
		t.Fatalf("Bearer 登入碼：%d %s", rec.Code, rec.Body.String())
	}
	// 錯的 Bearer：401。
	if rec := e.do(req{path: ping, header: map[string]string{"Authorization": "Bearer nope"}}); rec.Code != 401 {
		t.Fatalf("錯的權杖：%d", rec.Code)
	}

	// cookie 登入。
	if rec := e.do(req{path: "/api/v1/auth/login", body: `{"token":"wrong"}`}); rec.Code != 401 {
		t.Fatalf("錯的登入碼：%d", rec.Code)
	}
	rec = e.do(req{path: "/api/v1/auth/login", body: `{"token":"` + testToken + `"}`})
	if rec.Code != 200 {
		t.Fatalf("登入：%d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure || strings.Contains(cookie.Value, testToken) {
		t.Fatalf("cookie 不對：%+v", cookie)
	}
	if rec := e.do(req{path: ping, cookie: cookie}); rec.Code != 200 {
		t.Fatalf("用 cookie：%d", rec.Code)
	}
	st := decode[map[string]any](t, e.do(req{method: "GET", path: "/api/v1/auth/status", cookie: cookie}))
	if st["authenticated"] != true || st["kind"] != "owner" || st["local"] != false {
		t.Fatalf("登入狀態：%v", st)
	}
	bad := *cookie
	bad.Value = cookie.Value[:len(cookie.Value)-2] + "xx"
	if rec := e.do(req{path: ping, cookie: &bad}); rec.Code != 401 {
		t.Fatalf("竄改的 cookie：%d", rec.Code)
	}
	// HTTPS 時 cookie 要 Secure。
	rec = e.do(req{path: "/api/v1/auth/login", body: `{"token":"` + testToken + `"}`, local: true, header: map[string]string{"X-Forwarded-Proto": "https"}})
	if c := rec.Result().Cookies(); len(c) == 0 || !c[0].Secure {
		t.Fatalf("HTTPS 的 cookie 要 Secure：%+v", c)
	}
	// 登出清掉 cookie。
	rec = e.do(req{path: "/api/v1/auth/logout", cookie: cookie})
	if c := rec.Result().Cookies(); rec.Code != 200 || len(c) == 0 || c[0].MaxAge >= 0 {
		t.Fatalf("登出：%d %+v", rec.Code, c)
	}
}

func TestLocalNoAuthOff(t *testing.T) {
	e := newEnv(t)
	a := auth.NewHTTP(auth.HTTPConfig{DB: e.db, AccessToken: testToken, LocalNoAuth: false})
	e.h = New(Options{Registry: e.reg, Bus: e.bus, Auth: a, DB: e.db})
	if rec := e.do(req{path: "/api/v1/system.ping", local: true}); rec.Code != 401 {
		t.Fatalf("關掉本機免登入後要登入：%d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < limits.LoginFailures; i++ {
		if rec := e.do(req{path: "/api/v1/auth/login", body: `{"token":"wrong"}`}); rec.Code != 401 {
			t.Fatalf("第 %d 次：%d", i+1, rec.Code)
		}
	}
	rec := e.do(req{path: "/api/v1/auth/login", body: `{"token":"` + testToken + `"}`})
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || decode[result](t, rec).Error.Code != ops.CodeLimit {
		t.Fatalf("失敗太多次後連對的也要先擋：%d %s", rec.Code, rec.Body.String())
	}
	// 錯的 Bearer 也算，同樣被擋。
	if rec := e.do(req{path: "/api/v1/system.ping", header: map[string]string{"Authorization": "Bearer " + testToken}}); rec.Code != 429 {
		t.Fatalf("Bearer 也要被擋：%d", rec.Code)
	}
	// 別的來源不受影響。
	if rec := e.do(req{path: "/api/v1/auth/login", body: `{"token":"` + testToken + `"}`, remote: "198.51.100.7:1"}); rec.Code != 200 {
		t.Fatalf("別的來源：%d", rec.Code)
	}
}

func TestAgentTokenRevoked(t *testing.T) {
	e := newEnv(t)
	tok := newAgentToken(t, e, []string{"read"}, "")
	list, _ := e.db.ListAgentTokens(context.Background())
	if err := e.db.RevokeAgentToken(context.Background(), list[0].ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(req{path: "/api/v1/system.ping", header: map[string]string{"Authorization": "Bearer " + tok}}); rec.Code != 401 {
		t.Fatalf("撤銷的權杖：%d", rec.Code)
	}
}

// ── SSE ──────────────────────────────────────────────

type sseClient struct {
	resp *http.Response
	all  chan events.Event
}

func openSSE(t *testing.T, ctx context.Context, url string, header map[string]string) *sseClient {
	t.Helper()
	r, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE：%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	c := &sseClient{resp: resp, all: make(chan events.Event, 100)}
	go func() {
		defer close(c.all)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var ev events.Event
				if json.Unmarshal([]byte(data), &ev) == nil {
					c.all <- ev
				}
			} else if line == ": ping" {
				c.all <- events.Event{Kind: "_heartbeat"}
			}
		}
	}()
	return c
}

func (c *sseClient) next(t *testing.T, skipBeats bool) events.Event {
	t.Helper()
	for {
		select {
		case e, ok := <-c.all:
			if !ok {
				t.Fatal("事件流關掉了")
			}
			if skipBeats && e.Kind == "_heartbeat" {
				continue
			}
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("等不到事件")
		}
	}
}

func TestSSEResumeMissesNothing(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.h) // 對方是 127.0.0.1、Host 是 127.0.0.1:port → 本機免登入
	defer srv.Close()
	ctx := context.Background()
	appendN := func(n int, kind string) {
		for range n {
			e.appendEvent(t, store.Event{Kind: kind, Payload: json.RawMessage(`{}`)})
		}
	}
	appendN(3, "old") // 連線之前的事件：沒帶 Last-Event-ID 時不補送

	c1ctx, cancel1 := context.WithCancel(ctx)
	c1 := openSSE(t, c1ctx, srv.URL+"/api/v1/events", nil)
	appendN(5, "live")
	var last int64
	for range 5 {
		ev := c1.next(t, true)
		if ev.Kind != "live" || ev.ID <= last {
			t.Fatalf("事件順序不對：%+v（上一筆 %d）", ev, last)
		}
		last = ev.ID
	}
	cancel1() // 斷線
	c1.resp.Body.Close()

	appendN(7, "missed") // 斷線期間的事件
	c2ctx, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	c2 := openSSE(t, c2ctx, srv.URL+"/api/v1/events", map[string]string{"Last-Event-ID": fmt.Sprint(last)})
	appendN(2, "after")
	for i := range 9 {
		ev := c2.next(t, true)
		if ev.ID != last+1 {
			t.Fatalf("第 %d 筆漏了事件：拿到 %d，應該是 %d", i, ev.ID, last+1)
		}
		last = ev.ID
	}
	// 心跳。
	if ev := c2.next(t, false); ev.Kind != "_heartbeat" {
		t.Fatalf("沒有事件時要送心跳：%+v", ev)
	}
	// 用 query 指定也可以。
	c3ctx, cancel3 := context.WithCancel(ctx)
	defer cancel3()
	c3 := openSSE(t, c3ctx, srv.URL+"/api/v1/events?last_event_id=0", nil)
	if ev := c3.next(t, true); ev.ID != 1 || ev.Kind != "old" {
		t.Fatalf("last_event_id=0 要從頭補送：%+v", ev)
	}
}

func TestSSEFilteredByIdentity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mine, err := e.db.CreateBot(ctx, store.Bot{Name: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.db.CreateBot(ctx, store.Bot{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	tok := newAgentToken(t, e, []string{"read", "manage"}, `{"bots":["`+mine.ID+`"]}`)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := openSSE(t, sctx, srv.URL+"/api/v1/events", map[string]string{"Authorization": "Bearer " + tok})
	for _, ev := range []store.Event{
		{BotID: other.ID, Kind: "other"},
		{Kind: "global", Payload: json.RawMessage(`{"actor":"owner"}`)},
		{BotID: mine.ID, Kind: "mine"},
	} {
		e.appendEvent(t, ev)
	}
	// 沒存的即時事件也照範圍過濾後送出。
	e.bus.Publish(events.Event{BotID: other.ID, Kind: "other.delta"})
	e.bus.Publish(events.Event{BotID: mine.ID, Kind: "mine.delta"})
	// 代理自己的操作產生的全域事件看得到。
	rec := e.do(req{path: "/api/v1/test.make", body: `{"name":"x"}`, header: map[string]string{"Authorization": "Bearer " + tok}})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if ev := c.next(t, true); ev.Kind != "mine" {
		t.Fatalf("只該看到自己範圍內的 Bot：%+v", ev)
	}
	if ev := c.next(t, true); ev.Kind != "mine.delta" || ev.ID != 0 {
		t.Fatalf("即時事件：%+v", ev)
	}
	if ev := c.next(t, true); ev.Kind != ops.EventOp {
		t.Fatalf("應該看到自己的操作事件：%+v", ev)
	}
	// 沒有 read 權限的代理不能訂閱。
	noRead := newAgentToken(t, e, []string{"manage"}, "")
	if rec := e.do(req{method: "GET", path: "/api/v1/events", header: map[string]string{"Authorization": "Bearer " + noRead}}); rec.Code != 403 {
		t.Fatalf("沒有 read：%d", rec.Code)
	}
	if rec := e.do(req{method: "GET", path: "/api/v1/events?last_event_id=abc", local: true}); rec.Code != 400 {
		t.Fatalf("壞掉的 last_event_id：%d", rec.Code)
	}
}
