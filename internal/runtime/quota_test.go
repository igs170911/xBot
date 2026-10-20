package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
	"xbot/internal/engine/hermes"
	"xbot/internal/events"
	"xbot/internal/store"
)

// EN-7：ACP 引擎額度用完 → 這一輪以白話說明結束、開一張額度卡（列出可以換的引擎），對話裡的回覆帶同一張卡。
func TestQuotaCard(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	agent := acptest.NewFor(acp.EngineGemini)
	comp := &Computers{DB: db}
	gemini := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGemini],
		Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }})
	router := engine.NewRouter(hermes.Name, comp.EngineOf, gemini)
	var asked *engine.QuotaError
	rt := New(Options{DB: db, Driver: router, Bus: events.New(), Models: comp,
		QuotaCard: func(_ context.Context, botID string, q *engine.QuotaError) (string, []engine.SwitchOption) {
			asked = q
			return "Gemini CLI", []engine.SwitchOption{{Engine: acp.EngineCodex, Label: "Codex CLI（ChatGPT 方案）", Note: "已登入"}}
		}})
	bot, err := db.CreateBot(ctx, store.Bot{Name: "雙子", Engine: acp.EngineGemini, ApprovalMode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(thread string) ChatResult {
		t.Helper()
		res, err := rt.Chat(ctx, bot.ID, thread, acptest.PromptQuota)
		if err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		task, err := rt.Wait(wctx, res.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Verdict != VerdictFailed {
			t.Fatalf("額度用完算沒完成：%+v", task)
		}
		return res
	}
	res := run("")
	if asked == nil || asked.Engine != acp.EngineGemini || asked.Kind != engine.QuotaExhausted {
		t.Fatalf("問選項時要帶額度錯誤：%+v", asked)
	}
	open, _ := db.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{engine.QuotaCardKind}})
	if len(open) != 1 {
		t.Fatalf("要有一張額度卡：%+v", open)
	}
	card := open[0]
	if !strings.HasPrefix(card.Title, "Gemini CLI 的額度用完了（約 ") || card.ThreadID != res.ThreadID || card.TaskID != res.TaskID || card.BotID != bot.ID {
		t.Fatalf("額度卡：%+v", card)
	}
	var p QuotaView
	if err := json.Unmarshal(card.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.RetryText != acptest.PromptQuota || len(p.Options) != 1 || p.Options[0].Engine != acp.EngineCodex || p.ResetAt == 0 || p.Message == "" {
		t.Fatalf("額度卡內容：%+v", p)
	}
	// 回覆：白話說明＋meta.quota 指向同一張卡；不另外發「沒有完成」的通知。
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "Gemini CLI 的額度用完了") || !strings.Contains(last.Content, "換成你有的其他引擎") {
		t.Fatalf("回覆要說清楚：%q", last.Content)
	}
	var meta struct {
		Quota *QuotaView `json:"quota"`
	}
	_ = json.Unmarshal(last.Meta, &meta)
	if meta.Quota == nil || meta.Quota.InboxID != card.ID {
		t.Fatalf("回覆的 meta.quota：%s", last.Meta)
	}
	if errs, _ := db.ListInboxFiltered(ctx, store.InboxFilter{Kinds: []string{"error"}}); len(errs) != 0 {
		t.Fatalf("額度卡已經是通知，不要再發出錯卡：%+v", errs)
	}
	// 同一個 Bot 又用完：舊卡收掉，只留新的一張。
	run(res.ThreadID)
	open, _ = db.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{engine.QuotaCardKind}})
	if len(open) != 1 || open[0].ID == card.ID {
		t.Fatalf("只留最新的一張：%+v", open)
	}
	if old, _ := db.GetInboxItem(ctx, card.ID); old.Resolution != ResolutionReplaced {
		t.Fatalf("舊卡：%+v", old)
	}
}

// Hermes 的一輪失敗時，用 Model Proxy 這一輪看到的額度錯誤（供應商、恢復時間）；錯誤文字本身不夠時也能認出來。
func TestQuotaOfHermes(t *testing.T) {
	log := &engine.QuotaLog{}
	r := &Runtime{o: Options{QuotaLog: log}}
	started := time.Now().Add(-time.Minute)
	tr := &turn{botID: "b1", engine: hermes.Name, provider: "openai", started: started}
	if q := r.quotaOf(tr, "Hermes 回報錯誤：連線失敗"); q != nil {
		t.Fatalf("一般錯誤不是額度：%+v", q)
	}
	log.Record("b1", &engine.QuotaError{Engine: hermes.Name, Provider: "opencode-go", Kind: engine.QuotaExhausted, ResetAt: 123})
	if q := r.quotaOf(tr, "Hermes 回報錯誤"); q == nil || q.Provider != "opencode-go" || q.ResetAt != 123 {
		t.Fatalf("要用 Model Proxy 記下的：%+v", q)
	}
	tr2 := &turn{botID: "b2", engine: hermes.Name, provider: "openai", started: started}
	if q := r.quotaOf(tr2, "Error code: 429 - Too Many Requests"); q == nil || q.Provider != "openai" || q.Kind != engine.QuotaRateLimit {
		t.Fatalf("錯誤文字也要認得，供應商是這一輪的：%+v", q)
	}
	if q := r.quotaOf(&turn{botID: "b1"}, "429"); q != nil {
		t.Fatal("還沒開始就失敗的不算")
	}
}

func TestQuotaTitle(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, loc)
	at := time.Date(2026, 10, 18, 14, 20, 0, 0, loc).UnixMilli()
	cases := []struct {
		q    engine.QuotaError
		want string
	}{
		{engine.QuotaError{Kind: engine.QuotaExhausted, ResetAt: at}, "Claude Code 的額度用完了（約 14:20 恢復）"},
		{engine.QuotaError{Kind: engine.QuotaExhausted}, "Claude Code 的額度用完了"},
		{engine.QuotaError{Kind: engine.QuotaExhausted, ResetAt: at + 24*3600*1000}, "Claude Code 的額度用完了（約 10/19 14:20 恢復）"},
		{engine.QuotaError{Kind: engine.QuotaFunds}, "Claude Code 的餘額不足"},
		{engine.QuotaError{Kind: engine.QuotaAuth, ResetAt: at}, "Claude Code 的登入過期了"},
		{engine.QuotaError{Kind: engine.QuotaRateLimit, ResetAt: at}, "Claude Code 暫時用量太多，被限流了（約 14:20 恢復）"},
	}
	for _, c := range cases {
		if got := quotaTitle("Claude Code", c.q, now); got != c.want {
			t.Fatalf("得到 %q，要 %q", got, c.want)
		}
	}
}

// 登入過期（2026-10-21 使用者截圖）：說去哪裡重新登入，不寫「等額度恢復」，也不把同一件事的「原本的訊息」再貼一次。
func TestQuotaBodyForExpiredLogin(t *testing.T) {
	q := engine.QuotaError{Kind: engine.QuotaAuth, Engine: "grok", Message: "無法開始對話：Grok 還沒登入"}
	b := quotaBody(q, 2)
	if !strings.Contains(b, "「AI 帳號與模型」重新登入") || strings.Contains(b, "額度恢復") || !strings.Contains(b, "其他引擎") {
		t.Fatalf("%q", b)
	}
	if b := quotaBody(q, 0); strings.Contains(b, "額度恢復") {
		t.Fatalf("%q", b)
	}
}
