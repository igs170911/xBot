package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 假的 ChatGPT 登入伺服器＋假的 Codex 端點。不連任何真實帳號。

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func fakeJWT(exp time.Time, account, residency string, n int) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	payload := map[string]any{
		"exp": exp.Unix(), "n": n,
		"https://api.openai.com/auth":    map[string]any{"chatgpt_account_id": account, "chatgpt_data_residency": residency, "chatgpt_plan_type": "plus"},
		"https://api.openai.com/profile": map[string]any{"email": "owner@example.com"},
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(payload) + ".sig"
}

type fakeAuth struct {
	t        *testing.T
	clk      *clock
	srv      *httptest.Server
	mu       sync.Mutex
	polls    int
	pending  int // 前幾次查詢回 403（使用者還沒完成）
	access   string
	refresh  string
	n        int
	ttl      time.Duration
	refreshN atomic.Int32
	reject   string // 非空時 refresh 回這個錯誤碼
}

func newFakeAuth(t *testing.T, clk *clock) *fakeAuth {
	a := &fakeAuth{t: t, clk: clk, pending: 1, ttl: time.Hour}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *fakeAuth) mint() map[string]any {
	a.n++
	a.access = fakeJWT(a.clk.Now().Add(a.ttl), "acct-1", "us", a.n)
	a.refresh = fmt.Sprintf("rt-%d", a.n)
	return map[string]any{"access_token": a.access, "refresh_token": a.refresh, "id_token": "id." + a.access}
}

func (a *fakeAuth) current() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.access
}

func (a *fakeAuth) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/api/accounts/deviceauth/usercode":
		var v map[string]string
		_ = json.Unmarshal(body, &v)
		if v["client_id"] != CodexClientID {
			w.WriteHeader(400)
			return
		}
		_, _ = io.WriteString(w, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":"5"}`)
	case "/api/accounts/deviceauth/token":
		a.polls++
		var v map[string]string
		_ = json.Unmarshal(body, &v)
		if v["device_auth_id"] != "dev-1" || v["user_code"] != "ABCD-1234" {
			w.WriteHeader(400)
			return
		}
		if a.polls <= a.pending {
			w.WriteHeader(403)
			return
		}
		_, _ = io.WriteString(w, `{"authorization_code":"code-1","code_verifier":"ver-1"}`)
	case "/oauth/token":
		f, _ := url.ParseQuery(string(body))
		if f.Get("client_id") != CodexClientID {
			w.WriteHeader(400)
			return
		}
		switch f.Get("grant_type") {
		case "authorization_code":
			if f.Get("code") != "code-1" || f.Get("code_verifier") != "ver-1" || f.Get("redirect_uri") != a.srv.URL+"/deviceauth/callback" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(a.mint())
		case "refresh_token":
			a.refreshN.Add(1)
			if a.reject != "" {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":"`+a.reject+`"}`)
				return
			}
			if f.Get("refresh_token") != a.refresh {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":"refresh_token_reused"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(a.mint())
		default:
			w.WriteHeader(400)
		}
	default:
		w.WriteHeader(404)
	}
}

// fakeCodex 只接受目前有效的存取權杖。
type fakeCodex struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []upstreamCall
}

func newFakeCodex(t *testing.T, auth *fakeAuth) *fakeCodex {
	c := &fakeCodex{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.calls = append(c.calls, upstreamCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Body: string(b), Header: r.Header.Clone()})
		c.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+auth.current() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"detail":"token expired"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":30,\"output_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":10}}}}\n\n")
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *fakeCodex) Calls() []upstreamCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]upstreamCall(nil), c.calls...)
}

type oauthFixture struct {
	t         *testing.T
	clk       *clock
	auth      *fakeAuth
	codex     *fakeCodex
	mem       *Memory
	providers *Providers
	srv       *httptest.Server
	botKey    string
	logs      *syncBuffer
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{t: t, clk: &clock{t: time.Now()}, mem: NewMemory(), logs: &syncBuffer{}}
	f.auth = newFakeAuth(t, f.clk)
	f.codex = newFakeCodex(t, f.auth)
	kr := testKeyring(t)
	f.providers = NewProviders(f.mem, kr)
	f.providers.now = f.clk.Now
	f.providers.SetOAuth(AuthOAuthCodex, &CodexOAuth{Issuer: f.auth.srv.URL, TokenURL: f.auth.srv.URL + "/oauth/token",
		ClientID: CodexClientID, BaseURL: f.codex.srv.URL + "/backend-api/codex", Now: f.clk.Now})
	keys := NewWorkerKeys(f.mem, kr)
	k, err := keys.Issue(context.Background(), "bot-1")
	if err != nil {
		t.Fatal(err)
	}
	f.botKey = string(k)
	mux := http.NewServeMux()
	mux.Handle(Prefix, NewHandler(Options{Keys: keys, Providers: f.providers, Usage: f.mem,
		Logger: slog.New(slog.NewTextHandler(f.logs, nil)), Pricing: freePricer{}}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// freePricer 對任何模型都報價；訂閱登入不應該拿它算錢。
type freePricer struct{}

func (freePricer) Lookup(context.Context, string, string) (Price, string, bool, error) {
	return Price{Input: 1, Output: 1}, "test", true, nil
}

func (f *oauthFixture) login() Provider {
	f.t.Helper()
	ctx := context.Background()
	s, err := f.providers.StartLogin(ctx, AuthOAuthCodex, "openai-codex", "")
	if err != nil {
		f.t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		p, err := f.providers.CompleteLogin(ctx, s.LoginID)
		if err == nil {
			return p
		}
		if !errors.Is(err, ErrLoginPending) {
			f.t.Fatal(err)
		}
		f.clk.Add(s.Interval)
	}
	f.t.Fatal("登入一直沒完成")
	return Provider{}
}

func (f *oauthFixture) post(path, body string) *http.Response {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.botKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const hermesResponsesBody = `{"model":"gpt-5.5","instructions":"你是助理","input":[{"role":"user","content":"hi"}],` +
	`"store":true,"stream":false,"max_output_tokens":2048,"temperature":0.2,"prompt_cache_key":"pck_abc",` +
	`"reasoning":{"effort":"medium","summary":"auto"},"include":["reasoning.encrypted_content"]}`

func TestCodexDeviceLoginStoresOnlyCiphertext(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	s, err := f.providers.StartLogin(ctx, AuthOAuthCodex, "openai-codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.UserCode != "ABCD-1234" || s.VerificationURL != f.auth.srv.URL+"/codex/device" || s.Interval != 5*time.Second {
		t.Fatalf("%+v", s)
	}
	if _, err := f.providers.CompleteLogin(ctx, s.LoginID); !errors.Is(err, ErrLoginPending) {
		t.Fatalf("第一次應該還沒完成：%v", err)
	}
	// 查太快：不打擾登入伺服器。
	if _, err := f.providers.CompleteLogin(ctx, s.LoginID); !errors.Is(err, ErrLoginPending) {
		t.Fatal(err)
	}
	if f.auth.polls != 1 {
		t.Fatalf("間隔內不該再查：%d", f.auth.polls)
	}
	f.clk.Add(s.Interval)
	p, err := f.providers.CompleteLogin(ctx, s.LoginID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Auth != AuthOAuthCodex || p.Transport != TransportResponses || p.BaseURL != f.codex.srv.URL+"/backend-api/codex" {
		t.Fatalf("%+v", p)
	}
	got, err := f.providers.Get(ctx, "openai-codex")
	if err != nil {
		t.Fatal(err)
	}
	if got.OAuth.AccountID != "acct-1" || got.OAuth.Residency != "us" || got.OAuth.Plan != "plus" || got.OAuth.Email != "owner@example.com" ||
		string(got.OAuth.RefreshToken) != "rt-1" || string(got.APIKey) != f.auth.current() {
		t.Fatalf("%+v", got.OAuth)
	}
	rec, _ := f.mem.GetProvider(ctx, "openai-codex")
	for _, secret := range []string{f.auth.current(), "rt-1", "acct-1"} {
		if bytes.Contains(rec.SealedKey, []byte(secret)) {
			t.Fatalf("存放層看得到明文：%s", secret)
		}
	}
	// 登入完成後 login id 就不能再用。
	if _, err := f.providers.CompleteLogin(ctx, s.LoginID); !errors.Is(err, ErrLoginNotFound) {
		t.Fatal(err)
	}
	// 印出供應商不會洩漏權杖。
	if out := fmt.Sprintf("%v %+v", got, got.OAuth); strings.Contains(out, "rt-1") || strings.Contains(out, f.auth.current()) {
		t.Fatal(out)
	}
}

func TestCodexLoginExpires(t *testing.T) {
	f := newOAuthFixture(t)
	s, err := f.providers.StartLogin(context.Background(), AuthOAuthCodex, "openai-codex", "")
	if err != nil {
		t.Fatal(err)
	}
	f.clk.Add(16 * time.Minute)
	if _, err := f.providers.CompleteLogin(context.Background(), s.LoginID); !errors.Is(err, ErrLoginNotFound) {
		t.Fatal(err)
	}
}

func TestCodexProxyInjectsTokenAndHeaders(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	resp := f.post("/model/openai-codex/v1/responses", hermesResponsesBody)
	body := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "response.completed") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	calls := f.codex.Calls()
	if len(calls) != 1 {
		t.Fatalf("%d", len(calls))
	}
	c := calls[0]
	if c.Path != "/backend-api/codex/responses" || c.Auth != "Bearer "+f.auth.current() {
		t.Fatalf("%s %s", c.Path, c.Auth)
	}
	want := map[string]string{"Chatgpt-Account-Id": "acct-1", "X-Openai-Internal-Codex-Residency": "us", "Originator": CodexOriginator,
		"Session_id": "xbot-bot-1", "X-Client-Request-Id": "xbot-bot-bot-1"} // 快取 key 換成這個 Bot 固定的（每段對話一樣）
	for k, v := range want {
		if got := c.Header.Get(k); got != v {
			t.Errorf("%s = %q，應為 %q", k, got, v)
		}
	}
	if strings.Contains(c.Header.Get("Authorization"), f.botKey) {
		t.Fatal("worker-key 不能送到供應商")
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(c.Body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["stream"] != true || sent["store"] != false {
		t.Fatalf("Codex 一定要串流、不存檔：%v", sent)
	}
	for _, k := range []string{"max_output_tokens", "temperature"} {
		if _, ok := sent[k]; ok {
			t.Fatalf("%s 應該拿掉", k)
		}
	}
	if sent["prompt_cache_key"] != "xbot-bot-bot-1" || sent["instructions"] != "你是助理" {
		t.Fatalf("其他欄位要原樣保留：%v", sent)
	}
	// 容器拿到的回應裡沒有權杖。
	if strings.Contains(body, f.auth.current()) {
		t.Fatal("回應裡有權杖")
	}
	u := f.mem.Usage()
	if len(u) != 1 || u[0].InputTokens != 30 || u[0].OutputTokens != 7 || u[0].CostMicros != 0 || u[0].Unpriced {
		t.Fatalf("訂閱登入只記 token、不算錢：%+v", u)
	}
}

func TestCodexOnlyResponsesAllowed(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	resp := f.post("/model/openai-codex/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`)
	if body := readAll(t, resp); resp.StatusCode != 403 || !strings.Contains(body, "endpoint_not_allowed") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(f.codex.Calls()) != 0 {
		t.Fatal("不能送出")
	}
}

func TestCodexModelsAddsClientVersion(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	req, _ := http.NewRequest("GET", f.srv.URL+"/model/openai-codex/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+f.botKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	calls := f.codex.Calls()
	if len(calls) != 1 || calls[0].Path != "/backend-api/codex/models" || calls[0].Query != "client_version=1.0.0" {
		t.Fatalf("%+v", calls)
	}
}

func TestCodexRefreshesBeforeExpiry(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	old := f.auth.current()
	f.clk.Add(time.Hour - time.Minute) // 還剩 1 分鐘，小於提前換新的 2 分鐘
	resp := f.post("/model/openai-codex/v1/responses", hermesResponsesBody)
	if body := readAll(t, resp); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if f.auth.refreshN.Load() != 1 {
		t.Fatalf("應該換新一次：%d", f.auth.refreshN.Load())
	}
	got, _ := f.providers.Get(context.Background(), "openai-codex")
	if string(got.APIKey) == old || string(got.OAuth.RefreshToken) != "rt-2" {
		t.Fatal("新的權杖要存回去（refresh token 會輪換）")
	}
	if c := f.codex.Calls(); len(c) != 1 || c[0].Auth != "Bearer "+f.auth.current() {
		t.Fatalf("%+v", c)
	}
}

func TestCodexRefreshesOn401AndRetries(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	// 伺服器那邊撤銷了存取權杖（例：使用者在別處登出又登入），但 refresh token 還有效。
	f.auth.mu.Lock()
	f.auth.access = "revoked"
	f.auth.mu.Unlock()
	resp := f.post("/model/openai-codex/v1/responses", hermesResponsesBody)
	if body := readAll(t, resp); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	calls := f.codex.Calls()
	if len(calls) != 2 || calls[1].Auth != "Bearer "+f.auth.current() {
		t.Fatalf("應該 401 後換新再送一次：%d", len(calls))
	}
}

func TestCodexConcurrentRequestsRefreshOnce(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	f.clk.Add(time.Hour)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", f.srv.URL+"/model/openai-codex/v1/responses", strings.NewReader(hermesResponsesBody))
			req.Header.Set("Authorization", "Bearer "+f.botKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				codes <- 0
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatalf("status %d", c)
		}
	}
	if n := f.auth.refreshN.Load(); n != 1 {
		t.Fatalf("refresh token 只能用一次，同時的請求只能換新一次：%d", n)
	}
}

func TestCodexReloginRequired(t *testing.T) {
	f := newOAuthFixture(t)
	f.login()
	f.auth.mu.Lock()
	f.auth.reject = "invalid_grant"
	f.auth.mu.Unlock()
	f.clk.Add(2 * time.Hour)
	resp := f.post("/model/openai-codex/v1/responses", hermesResponsesBody)
	body := readAll(t, resp)
	if resp.StatusCode != 401 || !strings.Contains(body, "oauth_relogin_required") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(f.codex.Calls()) != 0 {
		t.Fatal("權杖過期又換不了新時不該送出")
	}
	if strings.Contains(f.logs.String(), "rt-1") {
		t.Fatal("紀錄裡有 refresh token")
	}
}

func TestCodexBodyKeepsUnknownAndHandlesBadJSON(t *testing.T) {
	out, key := codexBody([]byte(`not json`))
	if string(out) != "not json" || key != "" {
		t.Fatal(string(out))
	}
	out, key = codexBody([]byte(`{"model":"m","tools":[{"type":"function","name":"x"}],"prompt_cache_retention":"24h"}`))
	if strings.Contains(string(out), "prompt_cache_retention") || !strings.Contains(string(out), `"tools"`) || key != "" {
		t.Fatal(string(out))
	}
}

// 同一個 Bot 每段對話送一樣的 prompt_cache_key（新對話才讀得到已經快取的守則和工具清單），也放進 x-client-request-id。
func TestCodexCacheKeyPerBot(t *testing.T) {
	a := withCacheKey([]byte(`{"model":"m","prompt_cache_key":"session-1","input":[]}`), codexCacheKey("b1"))
	b := withCacheKey([]byte(`{"model":"m","prompt_cache_key":"session-2","input":[]}`), codexCacheKey("b1"))
	_, ka := codexBody(a)
	_, kb := codexBody(b)
	if ka != "xbot-bot-b1" || ka != kb {
		t.Fatalf("%q %q", ka, kb)
	}
	if string(withCacheKey([]byte("not json"), "k")) != "not json" {
		t.Fatal("看不懂的原樣送出")
	}
}

func TestCodexClaimsMalformed(t *testing.T) {
	for _, tok := range []string{"", "abc", "a.b.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`[]`)) + ".c"} {
		if c := codexClaims(tok); c != (jwtClaims{}) {
			t.Fatalf("%q → %+v", tok, c)
		}
	}
}
