package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// 測試用假的供應商（不打真的 OpenCode）：快判經真的 Model Proxy（Handler.Call）打到這裡。

type upstreamReq struct {
	Path    string
	Session string
	Body    map[string]any
}

type fakeUpstream struct {
	mu    sync.Mutex
	reqs  []upstreamReq
	reply func(r upstreamReq) (int, string, time.Duration)
	srv   *httptest.Server
}

func (f *fakeUpstream) calls() []upstreamReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamReq(nil), f.reqs...)
}

func (f *fakeUpstream) set(fn func(r upstreamReq) (int, string, time.Duration)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = fn
}

// reply 是一個 chat/completions 回應：內容是 content（JSON 字串會再包一層）。
func reply(content string) string {
	b, _ := json.Marshal(map[string]any{
		"model":   "x",
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 200, "completion_tokens": 10},
	})
	return string(b)
}

func jsonReply(label string, p float64, why string) func(upstreamReq) (int, string, time.Duration) {
	return func(upstreamReq) (int, string, time.Duration) {
		b, _ := json.Marshal(map[string]any{"label": label, "p": p, "why": why})
		return 200, reply(string(b)), 0
	}
}

type env struct {
	db  *store.DB
	svc *Service
	up  *fakeUpstream
	now time.Time
}

const testKey = "sk-oc-REAL-0000000000000000"

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	up := &fakeUpstream{}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		req := upstreamReq{Path: r.URL.Path, Session: r.Header.Get("X-Opencode-Session")}
		_ = json.Unmarshal(raw, &req.Body)
		up.mu.Lock()
		up.reqs = append(up.reqs, req)
		fn := up.reply
		up.mu.Unlock()
		status, body, delay := 500, `{}`, time.Duration(0)
		if fn != nil {
			status, body, delay = fn(req)
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(up.srv.Close)
	kr, err := proxy.NewKeyring(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ps := proxy.NewDBStore(db)
	providers := proxy.NewProviders(ps, kr)
	for _, p := range []proxy.Provider{
		{ID: "opencode-zen", BaseURL: up.srv.URL + "/zen/v1", Preset: "opencode-zen", APIKey: testKey},
		{ID: "opencode-go", BaseURL: up.srv.URL + "/zen/go/v1", Preset: "opencode-go", APIKey: testKey},
	} {
		if err := providers.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	h := proxy.NewHandler(proxy.Options{Keys: proxy.NewWorkerKeys(ps, kr), Providers: providers, Usage: ps, Pricing: &proxy.Pricing{DB: db}})
	e := &env{db: db, up: up, now: time.Now()}
	e.svc = &Service{DB: db, Now: func() time.Time { return e.now }, Call: func(ctx context.Context, provider string, body []byte, session string) (int, []byte, error) {
		resp, err := h.Call(ctx, proxy.CallRequest{Provider: provider, Path: "chat/completions", Body: body, Engine: UsageEngine, Session: session})
		if errors.Is(err, proxy.ErrNotFound) {
			return 0, nil, ErrNotConfigured
		}
		return resp.Status, resp.Body, err
	}}
	// 這些測試驗證「Jev 第一層 → Go 第二層」的分層機制，所以明確設定兩層（不依賴預設值）。
	set, err := e.svc.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Tier1, set.Tier2 = jevTier, goTier
	if err := e.svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	return e
}

var (
	jevTier = TierModel{Provider: "opencode-zen", Model: "jev-1.13"}
	goTier  = TierModel{Provider: "opencode-go", Model: "deepseek-v4-flash"}
)

func (e *env) modes(t *testing.T, m map[string]Mode) {
	t.Helper()
	set, err := e.svc.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		set.Modes[k] = v
	}
	if err := e.svc.SaveSettings(context.Background(), set); err != nil {
		t.Fatal(err)
	}
}

func (e *env) runs(t *testing.T) []store.JudgeRun {
	t.Helper()
	rs, err := e.db.ListJudgeRuns(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestParseReply(t *testing.T) {
	cases := []struct {
		name, body string
		label      string
		p          float64
		ok         bool
	}{
		{"固定格式", reply(`{"label":"done","p":0.92,"why":"做完了"}`), "done", 0.92, true},
		{"包在 json 區塊裡", reply("```json\n{\"label\":\"stuck\",\"p\":0.7,\"why\":\"x\"}\n```"), "stuck", 0.7, true},
		{"Jev 決策格式", reply(`{"choice":"chat","probabilities":{"chat":0.81,"work":0.19}}`), "chat", 0.81, true},
		{"answers 包一層", reply(`{"answers":{"q":{"choice":"work","probabilities":{"chat":0.1,"work":0.9}}}}`), "work", 0.9, true},
		{"百分比", reply(`{"label":"done","p":92,"why":"x"}`), "done", 0.92, true},
		{"Jev 原生回應", `{"answers":{"q":{"type":"choice","choice":"yes","probabilities":{"yes":0.66,"no":0.34}}}}`, "yes", 0.66, true},
		{"不是 JSON", reply("好的，我覺得做完了"), "", 0, false},
		{"沒有標籤", reply(`{"p":0.9}`), "", 0, false},
		{"機率超過", reply(`{"label":"done","p":180}`), "", 0, false},
		{"整個壞掉", `<html>`, "", 0, false},
	}
	for _, c := range cases {
		v, err := parseReply([]byte(c.body))
		if (err == nil) != c.ok || (c.ok && (v.Label != c.label || v.P != c.p)) {
			t.Errorf("%s：%+v %v", c.name, v, err)
		}
	}
	u, _ := UseByID(UseTaskDone)
	if _, oc := accept(u, u.Options, Answer{Label: "maybe", P: 0.99}); oc != OutcomeInvalid {
		t.Fatal("不在選項裡的標籤不合格")
	}
	if _, oc := accept(u, u.Options, Answer{Label: "done", P: 0.59}); oc != OutcomeLowConfidence {
		t.Fatal("機率低於 0.6 不採用")
	}
	if _, oc := accept(u, u.Options, Answer{Label: "done", P: -1}); oc != OutcomeInvalid {
		t.Fatal("選擇題沒有機率不合格")
	}
	tu, _ := UseByID(UseTitle)
	if v, oc := accept(tu, nil, Answer{Label: "「台北東京機票最便宜的日子比較一下下」", P: -1}); oc != OutcomeOK || len([]rune(v.Label)) != limits.JudgeTitleMax {
		t.Fatalf("標題截到 16 字：%q %s", v.Label, oc)
	}
}

func TestShadowRecordsWithoutActing(t *testing.T) {
	e := newEnv(t)
	e.up.set(jsonReply("stuck", 0.93, "其實沒做完"))
	ctx := context.Background()
	// 預設：選擇題用途是影子模式。
	if m := e.svc.Mode(ctx, UseTaskDone); m != ModeShadow {
		t.Fatalf("預設影子：%s", m)
	}
	d, err := e.svc.Decide(ctx, Ask{Use: UseTaskDone, Text: "查好了，最便宜的是週二。", Ref: "task-1", BotID: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Label != "done" || d.By != ByRules || d.Model == nil || d.Model.Label != "stuck" || d.Outcome != OutcomeOK || d.Live() {
		t.Fatalf("影子模式只記錄、照規則：%+v", d)
	}
	rs := e.runs(t)
	if len(rs) != 1 || rs[0].Mode != "shadow" || rs[0].RuleLabel != "done" || rs[0].ModelLabel != "stuck" || rs[0].P != 0.93 ||
		rs[0].Used != "rules" || rs[0].Calls != 1 || rs[0].Tier != Tier1 || rs[0].Model != "jev-1.13" || rs[0].Ref != "task-1" {
		t.Fatalf("紀錄：%+v", rs)
	}
	// 請求的樣子：Zen 的 jev-1.13、OpenCode 的對話標頭、選擇題的 JSON schema、輸入是資料。
	c := e.up.calls()
	if len(c) != 1 || c[0].Path != "/zen/v1/chat/completions" || c[0].Session != "xbot-judge-task_done" || c[0].Body["model"] != "jev-1.13" {
		t.Fatalf("請求：%+v", c)
	}
	raw, _ := json.Marshal(c[0].Body)
	if !strings.Contains(string(raw), `"enum":["done","needs_person","handed_back","only_promised","stuck"]`) ||
		!strings.Contains(string(raw), "untrusted data") || strings.Contains(string(raw), testKey) {
		t.Fatalf("請求內容：%s", raw)
	}
	// 用量帳本：標記 judge、照 Zen 的價格算（輸入 $0.042／百萬）。
	u, err := e.db.SumUsageEngine(ctx, UsageEngine, 0)
	if err != nil || u.Calls != 1 || u.InputTokens != 200 {
		t.Fatalf("用量：%+v %v", u, err)
	}
	// 關掉：不呼叫模型、不記錄。
	e.modes(t, map[string]Mode{UseTaskDone: ModeOff})
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseTaskDone, Text: "好了"}); d.Outcome != OutcomeOff || len(e.up.calls()) != 1 || len(e.runs(t)) != 1 {
		t.Fatalf("關：%+v", d)
	}
}

func TestLiveFallbacks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modes(t, map[string]Mode{UseRoute: ModeLive})
	ask := Ask{Use: UseRoute, Text: "幫我查明天台北天氣"}

	e.up.set(jsonReply("chat", 0.9, "閒聊"))
	if d, _ := e.svc.Decide(ctx, ask); d.Label != "chat" || d.By != ByModel || d.P != 0.9 || d.Shown != "閒聊" {
		t.Fatalf("生效：%+v", d)
	}
	for _, c := range []struct {
		name    string
		reply   func(upstreamReq) (int, string, time.Duration)
		outcome string
	}{
		{"機率不夠", jsonReply("chat", 0.55, ""), OutcomeLowConfidence},
		{"不在選項裡", jsonReply("maybe", 0.99, ""), OutcomeInvalid},
		{"不是 JSON", func(upstreamReq) (int, string, time.Duration) { return 200, reply("我覺得是 chat"), 0 }, OutcomeInvalid},
		{"限流", func(upstreamReq) (int, string, time.Duration) { return 429, `{"error":"slow down"}`, 0 }, OutcomeRateLimited},
		{"401", func(upstreamReq) (int, string, time.Duration) { return 401, `{"error":"bad key"}`, 0 }, OutcomeError},
		{"逾時", func(upstreamReq) (int, string, time.Duration) {
			return 200, reply(`{"label":"chat","p":0.99}`), 2 * time.Second
		}, OutcomeTimeout},
	} {
		e.up.set(c.reply)
		start := time.Now()
		d, _ := e.svc.Decide(ctx, ask)
		if d.Label != "work" || d.By != ByRules || d.Outcome != c.outcome {
			t.Errorf("%s：退回規則：%+v", c.name, d)
		}
		// 選擇題只問第一層與第二層（沒有設本機小模型）；用途 1 等 0.3 秒就放棄。
		if el := time.Since(start); el > limits.JudgeWaitRoute+700*time.Millisecond {
			t.Errorf("%s：等太久 %v", c.name, el)
		}
	}
}

func TestTierChainFallsToTier2(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modes(t, map[string]Mode{UseProgressQ: ModeLive})
	e.up.set(func(r upstreamReq) (int, string, time.Duration) {
		if strings.HasPrefix(r.Path, "/zen/go/") {
			b, _ := json.Marshal(map[string]any{"label": "yes", "p": 0.8, "why": "問進度"})
			return 200, reply(string(b)), 0
		}
		return 503, `{}`, 0
	})
	d, _ := e.svc.Decide(ctx, Ask{Use: UseProgressQ, Text: "那個報告弄得怎樣"})
	if d.Label != "yes" || d.By != ByModel || d.Tier != Tier2 || d.ModelID != "deepseek-v4-flash" {
		t.Fatalf("Jev 出錯往下退到第二層：%+v", d)
	}
	rs := e.runs(t)
	if len(rs) != 1 || rs[0].Calls != 2 || rs[0].Tier2Calls != 1 || rs[0].Used != "model" {
		t.Fatalf("%+v", rs)
	}
	// 第一層設成不用、第二層也不用：只用規則，不呼叫。
	set, _ := e.svc.Settings(ctx)
	set.Tier1, set.Tier2 = TierModel{}, TierModel{}
	if err := e.svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseProgressQ, Text: "進度如何"}); d.Outcome != OutcomeNotConfigured || d.Label != "yes" || len(e.up.calls()) != 2 {
		t.Fatalf("沒有模型：%+v", d)
	}
	// 供應商沒設定（本機小模型指到不存在的供應商）：當成沒設定，不算呼叫次數。
	set.Local = TierModel{Provider: "ollama", Model: "qwen"}
	_ = e.svc.SaveSettings(ctx, set)
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseProgressQ, Text: "進度如何"}); d.Outcome != OutcomeNotConfigured {
		t.Fatalf("%+v", d)
	}
	if calls, _, _ := e.db.JudgeCalls(ctx, 0); calls != 2 {
		t.Fatalf("呼叫次數：%d", calls)
	}
}

func TestSafetyOnlyStricter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modes(t, map[string]Mode{UseTaskDone: ModeLive, UseApproval: ModeLive})
	e.up.set(jsonReply("done", 0.99, "做完了"))
	// 規則判成需要你（要你登入）：不問模型、不降級（FJ-4）。
	text := "我已經打開網站了，請你先登入帳號，登入後我再繼續下單嗎？"
	d, _ := e.svc.Decide(ctx, Ask{Use: UseTaskDone, Text: text})
	if d.Label != string(NeedsPerson) || d.By != ByRules || d.Outcome != OutcomeLocked || len(e.up.calls()) != 0 {
		t.Fatalf("needs_person 不降級：%+v", d)
	}
	// 經 Fallback（runtime 用的）：一樣。
	res, err := e.svc.TaskDoneJudge().Judge(ctx, Input{Outcome: OutcomeAnswer, Text: text})
	if err != nil || res.Verdict != NeedsPerson || res.By != ByRules {
		t.Fatalf("Fallback：%+v %v", res, err)
	}
	// 要你核定：規則命中就鎖住；規則沒命中、模型說要問 → 可以多攔。
	e.up.set(jsonReply("no", 0.99, "不用問"))
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseApproval, Text: "用信用卡付款 320 元買網域"}); d.Label != "yes" || d.Outcome != OutcomeLocked {
		t.Fatalf("核定不放行：%+v", d)
	}
	e.up.set(jsonReply("yes", 0.8, "會對外公開"))
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseApproval, Text: "把草稿放上部落格"}); d.Label != "yes" || d.By != ByModel {
		t.Fatalf("快判可以多攔：%+v", d)
	}
	// 生效時，模型可以把「已完成」改成更嚴的判斷；Fallback 帶上模型的機率。
	e.up.set(jsonReply("only_promised", 0.88, "只說要查"))
	res, err = e.svc.TaskDoneJudge().Judge(ctx, Input{Outcome: OutcomeAnswer, Text: "好的，這就幫你整理三家的價格。", UsedTools: true, TaskID: "t1",
		Asked: "幫我整理三家的價格"})
	if err != nil || res.Verdict != OnlyPromised || res.By != ByModel || res.Confidence != 0.88 || res.Reason != "只說要查" {
		t.Fatalf("模型生效：%+v %v", res, err)
	}
	// 出錯的一輪（不是 answer）不問模型。
	n := len(e.up.calls())
	if res, _ := e.svc.TaskDoneJudge().Judge(ctx, Input{Outcome: OutcomeFailed}); res.Verdict != Stuck || len(e.up.calls()) != n {
		t.Fatalf("出錯的一輪：%+v", res)
	}
}

func TestDailyCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	set, _ := e.svc.Settings(ctx)
	set.DailyCap = 2
	set.Modes[UseRoute] = ModeLive
	if err := e.svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	var notified int
	e.svc.Notify = func(ev store.Event) { notified++ }
	e.up.set(jsonReply("chat", 0.9, ""))
	for i := 0; i < 2; i++ {
		if d, _ := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"}); d.By != ByModel {
			t.Fatalf("上限內：%+v", d)
		}
	}
	for i := 0; i < 2; i++ {
		d, _ := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"})
		if d.By != ByRules || d.Outcome != OutcomeCapped || d.Label != "chat" {
			t.Fatalf("到上限退回規則：%+v", d)
		}
	}
	if len(e.up.calls()) != 2 {
		t.Fatalf("到上限後不再呼叫：%d", len(e.up.calls()))
	}
	items, _ := e.db.ListInboxFiltered(ctx, store.InboxFilter{})
	if len(items) != 1 || !strings.Contains(items[0].Title, "上限") || notified != 1 {
		t.Fatalf("只通知一次：%+v %d", items, notified)
	}
	// 隔天自動恢復。
	e.now = e.now.Add(24 * time.Hour)
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"}); d.By != ByModel {
		t.Fatalf("隔天：%+v", d)
	}
}

func TestTier2Cap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	set, _ := e.svc.Settings(ctx)
	set.Tier2Cap = 1
	set.Modes[UseTitle] = ModeLive
	_ = e.svc.SaveSettings(ctx, set)
	e.up.set(jsonReply("台北東京機票比價", 0.9, ""))
	if name, ok := e.svc.Title(ctx, "幫我查這週台北飛東京最便宜的是哪一天，要含稅", "b"); !ok || name != "台北東京機票比價" {
		t.Fatalf("第二層命名：%q %v", name, ok)
	}
	// 第二層到上限、沒有本機小模型：用規則（前 20 字）。
	name, ok := e.svc.Title(ctx, "幫我查這週台北飛東京最便宜的是哪一天，要含稅", "b")
	if ok || name != "幫我查這週台北飛東京最便宜的是哪一天，要…" {
		t.Fatalf("第二層到上限：%q %v", name, ok)
	}
	if len(e.up.calls()) != 1 || !strings.HasPrefix(e.up.calls()[0].Path, "/zen/go/") {
		t.Fatalf("%+v", e.up.calls())
	}
}

func TestResponderAndPreCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	members := []store.Bot{{ID: "b1", Name: "小研", Title: "研究員"}, {ID: "b2", Name: "小寫", Title: "文案"}}
	room := store.Room{ID: "r1", Name: "行銷", LeadID: "b1"}
	e.up.set(jsonReply("b2", 0.8, "要寫文案"))
	if _, _, ok := e.svc.Responder(ctx, room, members, "幫我寫一段貼文"); ok {
		t.Fatal("影子模式不挑人")
	}
	e.modes(t, map[string]Mode{UseResponder: ModeLive})
	if id, p, ok := e.svc.Responder(ctx, room, members, "幫我寫一段貼文"); !ok || id != "b2" || p != 0.8 {
		t.Fatalf("生效：%s %v %v", id, p, ok)
	}
	raw, _ := json.Marshal(e.up.calls()[1].Body)
	if !strings.Contains(string(raw), `"enum":["b1","b2","several"]`) || !strings.Contains(string(raw), "小寫, 文案 bot.") {
		t.Fatalf("候選 Bot：%s", raw)
	}
	e.up.set(jsonReply(SeveralKey, 0.9, ""))
	if _, _, ok := e.svc.Responder(ctx, room, members, "大家各自說一下進度"); ok {
		t.Fatal("several 交給主責")
	}

	// 先判斷：影子模式不回任何一項；生效且有把握的才回。
	e.up.set(func(r upstreamReq) (int, string, time.Duration) {
		raw, _ := json.Marshal(r.Body)
		s := string(raw)
		switch {
		case strings.Contains(s, "clear enough"):
			return 200, reply(`{"label":"clear","p":0.96,"why":"夠清楚"}`), 0
		case strings.Contains(s, "What does this task need"):
			return 200, reply(`{"label":"web","p":0.93}`), 0
		case strings.Contains(s, "engine and thinking"):
			return 200, reply(`{"label":"hermes_medium","p":0.5}`), 0
		}
		return 200, reply(`{"label":"small","p":0.93}`), 0
	})
	if chips := e.svc.PreCheck(ctx, "幫我查這週台北飛東京最便宜的是哪一天", "b1", "", "hermes_medium"); len(chips) != 0 {
		t.Fatalf("影子模式：%+v", chips)
	}
	e.modes(t, map[string]Mode{UsePreClarity: ModeLive, UsePreComputer: ModeLive, UsePreEngine: ModeLive, UsePreCost: ModeLive})
	chips := e.svc.PreCheck(ctx, "幫我查這週台北飛東京最便宜的是哪一天", "b1", "", "hermes_medium")
	var shown []string
	for _, c := range chips {
		shown = append(shown, c.Shown)
	}
	if strings.Join(shown, "|") != "夠清楚|不用開電腦 · 先查網路|預估 小（< 2 萬 token）" {
		t.Fatalf("先判斷（引擎機率不夠不顯示）：%v", shown)
	}
}

func TestStatsAndSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b, err := e.db.CreateBot(ctx, store.Bot{Name: "小研"})
	if err != nil {
		t.Fatal(err)
	}
	th, err := e.db.CreateThread(ctx, store.Thread{BotID: b.ID, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.db.CreateTask(ctx, store.Task{BotID: b.ID, ThreadID: th.ID, Source: "owner", State: "done", Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	e.up.set(jsonReply("stuck", 0.9, ""))
	_, _ = e.svc.Decide(ctx, Ask{Use: UseTaskDone, Text: "查好了。", Ref: task.ID, BotID: b.ID})
	e.up.set(jsonReply("done", 0.9, ""))
	_, _ = e.svc.Decide(ctx, Ask{Use: UseTaskDone, Text: "查好了。"})
	if _, err := e.db.PutJudgeCorrection(ctx, store.JudgeCorrection{Use: UseTaskDone, TaskID: task.ID, BotID: b.ID, Judged: "done", JudgedBy: ByRules, Corrected: "stuck"}); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.stats(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var td UseStats
	for _, u := range st.Uses {
		if u.Use == UseTaskDone {
			td = u
		}
	}
	if td.Runs != 2 || td.Answered != 2 || td.Agreed != 1 || td.Agreement != 0.5 || td.Corrected != 1 || td.CorrectedModel != 1 ||
		td.CorrectedRule != 0 || td.CorrectedAgreement != 1 || td.ReadyForLive || td.Mode != ModeShadow {
		t.Fatalf("統計：%+v", td)
	}
	if st.Today.Calls != 2 || st.Today.Requests != 2 || st.Today.DailyCap != limits.JudgeDailyCalls || len(st.Recent) != 2 || len(st.Uses) != len(Uses) {
		t.Fatalf("今天：%+v", st.Today)
	}
	// 設定：沒存過的層用預設；存成空白表示不用；沒寫的用途用預設開關。
	set, _ := e.svc.Settings(ctx)
	if set.Tier1 != jevTier || set.Tier2 != goTier || set.Local.On() || set.Modes[UseSummary] != ModeOff || set.Modes[UsePreClarity] != ModeShadow {
		t.Fatalf("預設：%+v", set)
	}
	set.Tier1 = TierModel{}
	_ = e.svc.SaveSettings(ctx, set)
	if set, _ = e.svc.Settings(ctx); set.Tier1.On() {
		t.Fatal("存成空白要保留")
	}
}

func TestRuleFallbacks(t *testing.T) {
	cases := []struct{ use, text, want string }{
		{UseRoute, "嗨", "chat"}, {UseRoute, "進度如何", "progress"}, {UseRoute, "幫我訂位", "work"},
		{UseApproval, "刪除舊的備份", "yes"}, {UseApproval, "整理一下筆記", "no"},
		{UseErrorKind, "429 Too Many Requests", "transient"}, {UseErrorKind, "401 unauthorized", "auth"},
		{UseErrorKind, "insufficient quota", "quota"}, {UseErrorKind, "找不到檔案", "permanent"},
		{UseSkillDup, "整理 新聞 摘要\n---\n整理 新聞 摘要", "same"}, {UseSkillDup, "訂機票\n---\n寫程式", "different"},
		{UseSummary, "我查好了。最便宜是週二。", "我查好了"}, {UseBotSuggest, "幫我寫社群貼文", "小寫"},
	}
	for _, c := range cases {
		u, _ := UseByID(c.use)
		if got := u.RuleFor(Ask{Text: c.text}); got.Label != c.want || got.By != ByRules {
			t.Errorf("%s %q：%+v", c.use, c.text, got)
		}
	}
	for _, u := range Uses {
		if u.ID == UseTaskDone {
			continue // 只在你交代工作的第一輪才問，模型回答中位數 2.8 秒（limits.JudgeTaskDoneWait）
		}
		if u.Wait < 300*time.Millisecond || u.Wait > 2*time.Second {
			t.Errorf("%s 等待上限要在 0.3–2 秒：%v", u.ID, u.Wait)
		}
	}
}

// 使用者決定（2026-10-18）：快判預設用 OpenCode Go 的 deepseek-v4.1-flash（Go 沒有 Jev 類模型）。
func TestDefaultTiersUseOpenCodeGo(t *testing.T) {
	if DefaultTier1.Provider != "opencode-go" || DefaultTier2.Provider != "opencode-go" || DefaultTier1.Model != "deepseek-v4.1-flash" {
		t.Fatalf("預設應為 OpenCode Go：%+v %+v", DefaultTier1, DefaultTier2)
	}
}

// 服務的影子模式在背景判：呼叫的人不等模型；模型比用途的時限慢也記得到（2026-10-19：模型 1.2 秒、時限 1 秒，
// 每次都逾時什麼都沒記到，還讓每則訊息多等 0.3 秒）。
func TestShadowInBackgroundDoesNotWait(t *testing.T) {
	e := newEnv(t)
	e.svc.ShadowInBackground = true
	slow := 1500 * time.Millisecond // 比「任務完成了沒」的 1 秒時限慢
	e.up.set(func(upstreamReq) (int, string, time.Duration) {
		b, _ := json.Marshal(map[string]any{"label": "stuck", "p": 0.9, "why": "其實沒做完"})
		return 200, reply(string(b)), slow
	})
	start := time.Now()
	d, err := e.svc.Decide(context.Background(), Ask{Use: UseTaskDone, Text: "查好了。", Ref: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited > 300*time.Millisecond {
		t.Fatalf("影子模式不該等模型：等了 %s", waited)
	}
	if d.Label != "done" || d.By != ByRules || d.Outcome != OutcomeShadowPending {
		t.Fatalf("先回規則：%+v", d)
	}
	e.svc.WaitShadow()
	rs := e.runs(t)
	if len(rs) != 1 || rs[0].Outcome != OutcomeOK || rs[0].ModelLabel != "stuck" || rs[0].Mode != "shadow" || rs[0].Used != "rules" {
		t.Fatalf("背景判完要記下模型怎麼說（就算比時限慢）：%+v", rs)
	}
	// 直接試（judge.run 的 mode）照舊同步，看得到模型的判斷
	e.up.set(jsonReply("stuck", 0.9, "其實沒做完"))
	if d, _ := e.svc.DecideAs(context.Background(), Ask{Use: UseTaskDone, Text: "查好了。"}, ModeShadow); d.Model == nil {
		t.Fatalf("指定模式時同步：%+v", d)
	}
}

// 2026-10-23：模型只在你交代工作的第一輪、規則判「做完了」時才問；只能往嚴的方向改、要有 0.8 的把握，「卡住」不採用。
func TestTaskDoneModelOnlyUpgradesFirstTurnsOfRealWork(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modes(t, map[string]Mode{UseTaskDone: ModeLive})
	work := Input{Outcome: OutcomeAnswer, Text: "團隊建好了。", UsedTools: true, Asked: "幫我建一個三人的影片團隊"}
	judge := e.svc.TaskDoneJudge()
	for name, in := range map[string]Input{
		"閒聊":      {Outcome: OutcomeAnswer, Text: "嗨！", Asked: "嗨"},
		"接著做的那一輪": {Outcome: work.Outcome, Text: work.Text, UsedTools: true, Asked: work.Asked, Continued: true},
		"隊友交辦的":   {Outcome: work.Outcome, Text: work.Text, UsedTools: true},
	} {
		e.up.set(jsonReply("handed_back", 0.95, "沒做完"))
		n := len(e.up.calls())
		if res, _ := judge.Judge(ctx, in); res.By != ByRules || len(e.up.calls()) != n {
			t.Errorf("%s 不問模型：%+v", name, res)
		}
	}
	for _, c := range []struct {
		label string
		p     float64
		want  Verdict
		by    string
	}{
		{"handed_back", 0.9, HandedBack, ByModel},
		{"only_promised", 0.85, OnlyPromised, ByModel},
		{"handed_back", 0.7, Done, ByRules},
		{"stuck", 0.99, Done, ByRules},
		{"done", 0.99, Done, ByRules},
	} {
		e.up.set(jsonReply(c.label, c.p, "模型說"))
		if res, _ := judge.Judge(ctx, work); res.Verdict != c.want || res.By != c.by {
			t.Errorf("%s %.2f：%+v", c.label, c.p, res)
		}
	}
	// 模型看得到你原本交代的話。
	e.up.set(jsonReply("done", 0.99, "做完了"))
	_, _ = judge.Judge(ctx, work)
	calls := e.up.calls()
	if body, _ := json.Marshal(calls[len(calls)-1].Body); !strings.Contains(string(body), "幫我建一個三人的影片團隊") {
		t.Fatal("要帶上你交代的話")
	}
}
