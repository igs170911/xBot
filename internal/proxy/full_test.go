package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/store"
	"xbot/internal/version"
)

// ── 價目表 ────────────────────────────────────────────

func TestNormalizeModel(t *testing.T) {
	cases := map[string]string{
		"anthropic/claude-sonnet-4.6":  "claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929":   "claude-sonnet-4-5",
		"Claude-Opus-4-20250514":       "claude-opus-4",
		"gpt-4.1-mini":                 "gpt-4-1-mini",
		"openai/gpt-5.4":               "gpt-5-4",
		" deepseek/deepseek-chat ":     "deepseek-chat",
		"claude-haiku-4-5-2025-10-01x": "claude-haiku-4-5-2025-10-01x",
	}
	for in, want := range cases {
		if got := NormalizeModel(in); got != want {
			t.Errorf("%q → %q，要 %q", in, got, want)
		}
	}
}

func TestLookupPriceAndCost(t *testing.T) {
	if _, _, ok := LookupPrice(nil, "openai", "gpt-5.4"); ok {
		t.Fatal("沒在表上的模型不能亂套相近模型的價格")
	}
	pr, src, ok := LookupPrice(nil, "openrouter", "anthropic/claude-sonnet-4.6")
	if !ok || src != PriceSourceDefault || pr.Input != 3 || pr.Output != 15 {
		t.Fatalf("內建：%+v %s %v", pr, src, ok)
	}
	custom := map[string]Price{"openrouter/anthropic/claude-sonnet-4.6": {Input: 1, Output: 2}, "my-model": {Input: 5, Output: 5}}
	if pr, src, _ := LookupPrice(custom, "openrouter", "anthropic/claude-sonnet-4.6"); src != PriceSourceCustom || pr.Input != 1 {
		t.Fatalf("自訂優先：%+v %s", pr, src)
	}
	if pr, _, ok := LookupPrice(custom, "x", "my-model"); !ok || pr.Output != 5 {
		t.Fatal("只寫模型名的自訂價格")
	}
	// 1000 輸入（其中 400 命中快取、100 寫入快取）＋ 200 輸出，Sonnet 4.6：
	// 500×3 + 400×0.3 + 100×3.75 + 200×15 = 1500+120+375+3000 = 4995 百萬分之一美元。
	sonnet := defaultPrices["claude-sonnet-4-6"]
	if got := sonnet.CostMicros(1000, 400, 100, 200); got != 4995 {
		t.Fatalf("花費：%d", got)
	}
	// 沒有快取價時照輸入價。
	if got := (Price{Input: 2, Output: 1}).CostMicros(100, 50, 0, 0); got != 200 {
		t.Fatalf("沒有快取價：%d", got)
	}
	if ValidPrice(Price{Input: -1}) {
		t.Fatal("負的價格")
	}
}

// ── 探測 ──────────────────────────────────────────────

func TestClassifyCheck(t *testing.T) {
	cases := []struct {
		status int
		text   string
		want   string
	}{
		{200, "", CheckOK},
		{400, "Unsupported parameter: max_tokens", CheckOK},
		{404, "Invalid URL (POST /v1/responses)", CheckProtocol},
		{404, "The model `x` does not exist", CheckUnavailable},
		{401, "Incorrect API key provided", CheckAuth},
		{429, "Rate limit reached", CheckBusy},
		{503, "", CheckBusy},
		{403, "", CheckAuth},
		{405, "", CheckProtocol},
		{400, "something odd", CheckUnknown},
	}
	for _, c := range cases {
		if got := ClassifyCheck(c.status, c.text); got != c.want {
			t.Errorf("%d %q → %s，要 %s", c.status, c.text, got, c.want)
		}
	}
}

func TestRuleTransport(t *testing.T) {
	cases := []struct{ provider, base, model, want string }{
		{"anthropic", "https://api.anthropic.com/v1", "claude-x", TransportMessages},
		{"minimax", "https://api.minimax.io/anthropic/v1", "m", TransportMessages},
		{"opencode-go", "https://opencode.ai/zen/go/v1", "gpt-5", TransportResponses},
		{"opencode-go", "https://opencode.ai/zen/go/v1", "minimax-m2", TransportMessages},
		{"opencode-go", "https://opencode.ai/zen/go/v1", "deepseek-v4-flash", TransportChat},
		{"opencode-zen", "https://opencode.ai/zen/v1", "claude-sonnet-5", TransportMessages},
		{"openai", "https://api.openai.com/v1", "gpt-5", ""},
	}
	for _, c := range cases {
		if got := RuleTransport(c.provider, c.base, c.model); got != c.want {
			t.Errorf("%+v → %q", c, got)
		}
	}
}

// fakeProvider 只認得 responses：chat/completions 回 404「Invalid URL」，messages 回 404。
func fakeProvider(t *testing.T, key string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided: `+r.Header.Get("Authorization")+`"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"m-b"},{"id":"m-a"},{"id":"m-a"}]}`)
		case "/v1/responses":
			_, _ = io.WriteString(w, `{"id":"r1","output":[]}`)
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid URL (POST `+r.URL.Path+`)"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestProberFindsTransport(t *testing.T) {
	ctx := context.Background()
	srv, paths := fakeProvider(t, realKey)
	p := &Prober{Timeout: 5 * time.Second}
	pr := Provider{ID: "x", BaseURL: srv.URL + "/v1", APIKey: realKey}
	models, listErr := p.ListModels(ctx, pr)
	if listErr != "" || strings.Join(models, ",") != "m-a,m-b" {
		t.Fatalf("模型清單：%v %q", models, listErr)
	}
	c := p.CheckModel(ctx, pr, "m-a", TransportChat)
	if c.Status != CheckOK || c.Transport != TransportResponses {
		t.Fatalf("探測：%+v", c)
	}
	if got := strings.Join((*paths)[1:], ","); got != "POST /v1/chat/completions,POST /v1/responses" {
		t.Fatalf("先試猜的，再試下一個：%s", got)
	}
	// 金鑰錯：停在第一個，不往下試；錯誤訊息不含金鑰。
	bad := Provider{ID: "x", BaseURL: srv.URL + "/v1", APIKey: "sk-wrong-secret-key-value"}
	before := len(*paths)
	c = p.CheckModel(ctx, bad, "m-a", TransportChat)
	if c.Status != CheckAuth || len(*paths)-before != 1 || strings.Contains(c.Detail, "sk-wrong-secret-key-value") {
		t.Fatalf("金鑰錯：%+v（試了 %d 次）", c, len(*paths)-before)
	}
	// 連不上：busy。
	down := Provider{ID: "x", BaseURL: "http://127.0.0.1:1/v1"}
	if c := p.CheckModel(ctx, down, "m", ""); c.Status != CheckBusy {
		t.Fatalf("連不上：%+v", c)
	}
}

func TestDBStoreTransportFor(t *testing.T) {
	ctx := context.Background()
	s := NewDBStore(openDB(t))
	p := NewProviders(s, testKeyring(t))
	_ = p.Put(ctx, Provider{ID: "oc", BaseURL: "https://opencode.ai/zen/v1", Preset: "opencode-zen", APIKey: "k"})
	_ = p.Put(ctx, Provider{ID: "plain", BaseURL: "https://example.com/v1", Transport: TransportResponses, APIKey: "k"})
	check := func(provider, model, want string) {
		t.Helper()
		if got, err := s.TransportFor(ctx, provider, model); err != nil || got != want {
			t.Fatalf("%s/%s → %q %v，要 %q", provider, model, got, err, want)
		}
	}
	check("oc", "claude-sonnet-5", TransportMessages)
	check("plain", "m", TransportResponses)
	check("nope", "m", "")
	// 探測結果優先。
	_ = s.PutProbe(ctx, ProbeResult{Provider: "plain", Checks: map[string]ModelCheck{"m": {Model: "m", Status: CheckOK, Transport: TransportMessages}}})
	check("plain", "m", TransportMessages)
	// 移除供應商時探測結果一起刪。
	if err := p.Delete(ctx, "plain"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Probe(ctx, "plain"); ok {
		t.Fatal("探測結果應該一起刪")
	}
	if err := p.Delete(ctx, "plain"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("刪兩次：%v", err)
	}
	list, err := p.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "oc" || list[0].APIKey == "" || string(list[0].APIKey) == "k" {
		t.Fatalf("列出（金鑰只標示有沒有）：%+v %v", list, err)
	}
}

func TestPresets(t *testing.T) {
	go1, ok := PresetByID("opencode-go")
	if !ok || go1.BaseURL != "https://opencode.ai/zen/go/v1" || go1.SessionHeader == "" || go1.SuggestedModel() != "deepseek-v4-flash" || go1.Transport != TransportChat {
		t.Fatalf("OpenCode Go：%+v", go1)
	}
	zen, ok := PresetByID("opencode-zen")
	if !ok || zen.BaseURL != "https://opencode.ai/zen/v1" || zen.SessionHeader == "" {
		t.Fatalf("OpenCode Zen：%+v", zen)
	}
	if a, ok := PresetByID("anthropic"); !ok || a.Transport != TransportMessages {
		t.Fatalf("Anthropic：%+v", a)
	}
	for _, p := range Presets() {
		if !ValidProviderID(p.ID) || !ValidTransport(p.Transport) {
			t.Errorf("範本 %s 不合格：%+v", p.ID, p)
		}
		if _, err := NormalizeBaseURL(p.BaseURL); err != nil {
			t.Errorf("範本 %s 網址：%v", p.ID, err)
		}
	}
	if SessionHeaderFor(Provider{BaseURL: "https://opencode.ai/zen/go/v1"}) == "" || SessionHeaderFor(Provider{BaseURL: "https://api.openai.com/v1"}) != "" {
		t.Fatal("自訂網址指到 opencode.ai 也要帶 session")
	}
}

// ── Handler：綁定、備援、閒置逾時、花費上限、session 標頭 ─────────────

type staticBinder Binding

func (b staticBinder) Binding(context.Context, string) (Binding, error) { return Binding(b), nil }

func addProvider(t *testing.T, f *fixture, p Provider) {
	t.Helper()
	if err := NewProviders(f.mem, testKeyring(t)).Put(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestBotCanOnlyUseItsProvider(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{}`), func(o *Options) { o.Bindings = staticBinder{Provider: "other"} })
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	if body := readAll(t, resp); resp.StatusCode != 403 || !strings.Contains(body, "provider_not_allowed") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(f.upstreamCalls()) != 0 {
		t.Fatal("不能送到供應商")
	}
}

func TestFallbackBeforeStreamStarts(t *testing.T) {
	var n atomic.Int32
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			n.Add(1)
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"message":"overloaded"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
	}, func(o *Options) {
		o.Bindings = staticBinder{Provider: "openai", Fallback: &Route{Provider: "backup", Model: "cheap-model", Transport: TransportChat}}
	})
	addProvider(t, f, Provider{ID: "backup", BaseURL: f.upstream.URL + "/backup/v1", APIKey: "sk-backup-key-0000000000"})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if resp.StatusCode != 200 || resp.Header.Get("X-Xbot-Fallback") != "backup/cheap-model" {
		t.Fatalf("%d %v %s", resp.StatusCode, resp.Header, body)
	}
	calls := f.upstreamCalls()
	if len(calls) != 2 || calls[1].Path != "/backup/v1/chat/completions" || calls[1].Auth != "Bearer sk-backup-key-0000000000" {
		t.Fatalf("呼叫：%+v", calls)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(calls[1].Body), &sent)
	if sent["model"] != "cheap-model" || sent["messages"] == nil {
		t.Fatalf("備援請求要換模型、其他不變：%s", calls[1].Body)
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	u := f.mem.Usage()
	if len(u) != 1 || u[0].Provider != "backup" || u[0].Model != "cheap-model" || !u[0].Fallback || u[0].InputTokens != 7 {
		t.Fatalf("用量記在備援模型：%+v", u)
	}
}

func TestNoFallbackOnClientErrorOrWithoutConfig(t *testing.T) {
	f := newFixture(t, jsonUpstream(400, `{"error":{"message":"bad"}}`), func(o *Options) {
		o.Bindings = staticBinder{Provider: "openai", Fallback: &Route{Provider: "openai", Model: "other", Transport: TransportChat}}
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	if resp.StatusCode != 400 || len(f.upstreamCalls()) != 1 {
		t.Fatalf("400 不備援：%d %d", resp.StatusCode, len(f.upstreamCalls()))
	}
	// 備援的呼叫方式不同（messages）時不備援。
	g := newFixture(t, jsonUpstream(503, `{}`), func(o *Options) {
		o.Bindings = staticBinder{Provider: "openai", Fallback: &Route{Provider: "openai", Model: "claude", Transport: TransportMessages}}
	})
	resp = g.do("POST", "/model/openai/v1/chat/completions", "Bearer "+g.botKey, chatBody)
	if resp.StatusCode != 503 || len(g.upstreamCalls()) != 1 {
		t.Fatalf("呼叫方式不同不備援：%d %d", resp.StatusCode, len(g.upstreamCalls()))
	}
}

func TestFallbackOnConnectionError(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{"choices":[]}`), func(o *Options) {
		o.Bindings = staticBinder{Provider: "dead", Fallback: &Route{Provider: "openai", Model: "gpt-b", Transport: TransportChat}}
	})
	addProvider(t, f, Provider{ID: "dead", BaseURL: "http://127.0.0.1:1/v1", APIKey: "k"})
	resp := f.do("POST", "/model/dead/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	if resp.StatusCode != 200 || resp.Header.Get("X-Xbot-Fallback") != "openai/gpt-b" {
		t.Fatalf("%d", resp.StatusCode)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select { // 之後就不送了
		case <-release:
		case <-r.Context().Done():
		}
	}, func(o *Options) { o.IdleTimeout = 150 * time.Millisecond })
	start := time.Now()
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if !strings.Contains(body, "hi") || time.Since(start) > 5*time.Second {
		t.Fatalf("已收到的要轉出去，閒置後結束：%q %v", body, time.Since(start))
	}
	if !strings.Contains(f.logs.String(), "串流閒置逾時") {
		t.Fatalf("紀錄：%s", f.logs.String())
	}
}

type fakeGuard struct {
	mu       sync.Mutex
	over     *BudgetError
	provider *BudgetError
	exceeded []*BudgetError
}

func (g *fakeGuard) Check(context.Context, string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.over != nil {
		return g.over
	}
	return nil
}

func (g *fakeGuard) CheckProvider(context.Context, string, string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider
	}
	return nil
}

func (g *fakeGuard) Exceeded(_ context.Context, e *BudgetError) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exceeded = append(g.exceeded, e)
	return true, nil
}

func TestBudgetBlocksBeforeForwarding(t *testing.T) {
	g := &fakeGuard{over: &BudgetError{Scope: ScopeBotDaily, BotID: "bot-1", LimitMicros: 1e6, SpentMicros: 1_200_000, Period: "2026-10-18"}}
	f := newFixture(t, jsonUpstream(200, `{}`), func(o *Options) { o.Budget = g })
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusPaymentRequired || !strings.Contains(body, "budget_exceeded") || !strings.Contains(body, "$1.00") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(f.upstreamCalls()) != 0 || len(g.exceeded) != 1 {
		t.Fatalf("不能送出；要通知一次：%d %d", len(f.upstreamCalls()), len(g.exceeded))
	}
	// 不花錢的 models 不擋。
	if resp := f.do("GET", "/model/openai/v1/models", "Bearer "+f.botKey, ""); resp.StatusCode != 200 {
		t.Fatalf("models：%d", resp.StatusCode)
	}
	// 供應商上限也擋。
	g.mu.Lock()
	g.over, g.provider = nil, &BudgetError{Scope: ScopeProviderDaily, Provider: "openai", LimitMicros: 1, SpentMicros: 1}
	g.mu.Unlock()
	if resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody); resp.StatusCode != 402 {
		t.Fatalf("供應商上限：%d", resp.StatusCode)
	}
}

type fakePricer map[string]Price

func (p fakePricer) Lookup(_ context.Context, _, model string) (Price, string, bool, error) {
	pr, ok := p[model]
	return pr, PriceSourceCustom, ok, nil
}

func TestCostFromPricing(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{"usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":10,"output_tokens":20}}`),
		func(o *Options) {
			o.Pricing = fakePricer{"claude-x": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}}
		})
	f.do("POST", "/model/openai/v1/messages", "Bearer "+f.botKey, `{"model":"claude-x","messages":[]}`)
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	f.do("POST", "/model/openai/v1/messages", "Bearer "+f.botKey, `{"model":"mystery","messages":[]}`)
	waitFor(t, func() bool { return len(f.mem.Usage()) == 2 })
	u := f.mem.Usage()
	if len(u) != 2 {
		t.Fatalf("%+v", u)
	}
	// input 160（100＋50 命中＋10 寫入）：100×3 + 50×0.3 + 10×3.75 + 20×15 = 300+15+37.5+300 = 652.5 → 653
	if u[0].InputTokens != 160 || u[0].CacheWriteTokens != 10 || u[0].CostMicros != 653 || u[0].Unpriced {
		t.Fatalf("有價格：%+v", u[0])
	}
	if u[1].CostMicros != 0 || !u[1].Unpriced {
		t.Fatalf("沒有價格要標示：%+v", u[1])
	}
}

func TestSessionHeaderAndUserAgent(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{}`), func(o *Options) {
		o.Session = func(botID string) string {
			if botID == "bot-1" {
				return "thread-9"
			}
			return ""
		}
	})
	addProvider(t, f, Provider{ID: "ocgo", BaseURL: f.upstream.URL + "/zen/go/v1", Preset: "opencode-go", APIKey: "sk-oc-key-000000000000"})
	req, _ := http.NewRequest("POST", f.srv.URL+"/model/ocgo/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("Authorization", "Bearer "+f.botKey)
	req.Header.Set("User-Agent", "hermes-in-the-box/1.0")
	req.Header.Set("X-Opencode-Session", "forged-by-box")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	calls := f.upstreamCalls()
	if len(calls) != 2 {
		t.Fatalf("%+v", calls)
	}
	oc, plain := calls[0].Header, calls[1].Header
	if oc.Get("X-Opencode-Session") != "xbot-thread-9" || oc.Get("User-Agent") != "XBot/"+version.Version {
		t.Fatalf("OpenCode 標頭：%v", oc)
	}
	if plain.Get("X-Opencode-Session") != "" || plain.Get("User-Agent") != "XBot/"+version.Version {
		t.Fatalf("其他供應商不帶 session：%v", plain)
	}
	// 沒有進行中的對話：用 Bot 的固定 id。
	h := NewHandler(Options{Keys: f.keys})
	if got := h.session("bot-2"); got != "xbot-bot-2" {
		t.Fatalf("%q", got)
	}
}

// ── Budgets（真資料庫）─────────────────────────────────

func TestBudgetsCapsAndCardOnce(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	bot, err := db.CreateBot(ctx, store.Bot{Name: "小秘", Budget: json.RawMessage(`{"daily_micros":1000000}`)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 15, 0, 0, 0, time.UTC)
	var stopped atomic.Int32
	var notified atomic.Int32
	b := &Budgets{DB: db, Now: func() time.Time { return now }, Location: time.UTC,
		OnExceeded: func(*BudgetError) { stopped.Add(1) }, Notify: func(store.Event) { notified.Add(1) }}
	if err := b.Check(ctx, bot.ID); err != nil {
		t.Fatalf("還沒花錢：%v", err)
	}
	// 昨天花的不算今天。
	_, _ = db.AddUsage(ctx, store.UsageEntry{BotID: bot.ID, Provider: "p", CostMicros: 5_000_000, CreatedAt: now.AddDate(0, 0, -1).UnixMilli()})
	_, _ = db.AddUsage(ctx, store.UsageEntry{BotID: bot.ID, Provider: "p", CostMicros: 600_000, CreatedAt: now.UnixMilli()})
	if err := b.Check(ctx, bot.ID); err != nil {
		t.Fatalf("今天才 $0.60：%v", err)
	}
	_, _ = db.AddUsage(ctx, store.UsageEntry{BotID: bot.ID, Provider: "p", CostMicros: 400_000, CreatedAt: now.UnixMilli()})
	var be *BudgetError
	if err := b.Check(ctx, bot.ID); !errors.As(err, &be) || be.Scope != ScopeBotDaily || be.SpentMicros != 1_000_000 || be.Period != "2026-10-18" || be.BotName != "小秘" {
		t.Fatalf("到上限就擋：%v", err)
	}
	for range 3 {
		if _, err := b.Exceeded(ctx, be); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := db.ListInbox(ctx, "open")
	if len(items) != 1 || items[0].Kind != "budget" || items[0].BotID != bot.ID || !strings.Contains(items[0].Title, "小秘") {
		t.Fatalf("卡片只發一次：%+v", items)
	}
	if stopped.Load() != 1 || notified.Load() != 1 {
		t.Fatalf("停止 %d 次、通知 %d 次", stopped.Load(), notified.Load())
	}
	// 月上限、全域上限、供應商上限。
	if err := db.SetSetting(ctx, GlobalCapsSetting, Caps{MonthlyMicros: 6_000_000}); err != nil {
		t.Fatal(err)
	}
	other, _ := db.CreateBot(ctx, store.Bot{Name: "小二"})
	if err := b.Check(ctx, other.ID); !errors.As(err, &be) || be.Scope != ScopeGlobalMonthly {
		t.Fatalf("全域月上限（本月 $6.00）：%v", err)
	}
	_ = db.DeleteSetting(ctx, GlobalCapsSetting)
	if err := b.Check(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	_ = db.SetSetting(ctx, ProviderCapsPrefix+"p", Caps{DailyMicros: 500_000})
	if err := b.CheckProvider(ctx, other.ID, "p"); !errors.As(err, &be) || be.Scope != ScopeProviderDaily || be.Provider != "p" {
		t.Fatalf("供應商每日上限：%v", err)
	}
	if err := b.CheckProvider(ctx, other.ID, "q"); err != nil {
		t.Fatalf("別的供應商不受影響：%v", err)
	}
	// 被刪掉的 Bot：卡片記成全域。
	gone := &BudgetError{Scope: ScopeBotMonthly, BotID: "deleted-bot", LimitMicros: 1, SpentMicros: 2, Period: "2026-10"}
	if ok, err := b.Exceeded(ctx, gone); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
}
