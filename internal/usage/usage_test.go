package usage

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

var bg = context.Background()

type env struct {
	db  *store.DB
	reg *ops.Registry
	now time.Time
	a   store.Bot
	b   store.Bot
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	e.reg = ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, Deps{Budgets: &proxy.Budgets{DB: db, Now: func() time.Time { return e.now }, Location: time.UTC}})
	e.a, _ = db.CreateBot(bg, store.Bot{Name: "=小秘"})
	e.b, _ = db.CreateBot(bg, store.Bot{Name: "小二"})
	day := func(d int) int64 { return e.now.AddDate(0, 0, d).UnixMilli() }
	for _, u := range []store.UsageEntry{
		{BotID: e.a.ID, Provider: "openai", Model: "gpt", InputTokens: 1000, OutputTokens: 10, CacheReadTokens: 250, CostMicros: 300_000, CreatedAt: day(0)},
		{BotID: e.a.ID, Provider: "openai", Model: "gpt", InputTokens: 1000, OutputTokens: 10, CacheReadTokens: 750, CostMicros: 100_000, CreatedAt: day(-1), Fallback: true},
		{BotID: e.b.ID, Provider: "oc", Model: "flash", InputTokens: 500, OutputTokens: 5, Unpriced: true, CreatedAt: day(0)},
		{BotID: e.b.ID, Provider: "oc", Model: "flash", InputTokens: 9, CostMicros: 9, CreatedAt: day(-40)}, // 30 天外
	} {
		if _, err := db.AddUsage(bg, u); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

var owner = ops.Owner("test")

func (e *env) call(id ops.Identity, op string, in any, out any) error {
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(bg, id, op, raw, ops.Options{})
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(res.Output, out)
	}
	return nil
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func TestSummary(t *testing.T) {
	e := setup(t)
	var s SummaryOut
	if err := e.call(owner, "xbot.usage.summary", map[string]any{}, &s); err != nil {
		t.Fatal(err)
	}
	if s.Total.Calls != 3 || s.Total.CostMicros != 400_000 || s.Total.CostUSD != 0.4 || s.Total.UnpricedCalls != 1 || s.Total.FallbackCalls != 1 {
		t.Fatalf("合計：%+v", s.Total)
	}
	// 快取命中率 = (250+750) / (1000+1000+500)
	if s.Total.CacheHitRate != 0.4 {
		t.Fatalf("快取命中率：%v", s.Total.CacheHitRate)
	}
	if len(s.ByBot) != 2 || s.ByBot[0].Key != e.a.ID || s.ByBot[0].Label != "=小秘" || s.ByBot[0].CacheHitRate != 0.5 {
		t.Fatalf("依 Bot：%+v", s.ByBot)
	}
	if len(s.ByProvider) != 2 || len(s.ByModel) != 2 || s.ByModel[0].Key != "openai/gpt" {
		t.Fatalf("依供應商／模型：%+v %+v", s.ByProvider, s.ByModel)
	}
	if len(s.ByDay) != 2 || s.ByDay[0].Key != "2026-10-17" || s.ByDay[1].Key != "2026-10-18" {
		t.Fatalf("依日期：%+v", s.ByDay)
	}
	if len(s.Unpriced) != 1 || s.Unpriced[0].Model != "flash" {
		t.Fatalf("%+v", s.Unpriced)
	}
	// 只看一個 Bot、只看一天。
	_ = e.call(owner, "xbot.usage.summary", map[string]any{"bot_id": e.a.ID, "days": 1}, &s)
	if s.Total.Calls != 1 || s.Total.CostMicros != 300_000 {
		t.Fatalf("%+v", s.Total)
	}
	_ = e.call(owner, "xbot.usage.summary", map[string]any{"provider": "oc", "days": 365}, &s)
	if s.Total.Calls != 2 {
		t.Fatalf("依供應商篩選：%+v", s.Total)
	}
	// 只能碰小二的代理看不到小秘的用量。
	limited := ops.Identity{Kind: ops.KindAgent, ID: "x", Scopes: []ops.Scope{ops.ScopeRead}, Resources: ops.Resources{Bots: []string{e.b.ID}}}
	_ = e.call(limited, "xbot.usage.summary", map[string]any{}, &s)
	if s.Total.Calls != 1 || len(s.ByBot) != 1 || s.ByBot[0].Key != e.b.ID {
		t.Fatalf("資源範圍：%+v", s.ByBot)
	}
	if err := e.call(limited, "xbot.usage.summary", map[string]any{"bot_id": e.a.ID}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

func TestExport(t *testing.T) {
	e := setup(t)
	var out ExportOut
	if err := e.call(owner, "xbot.usage.export", map[string]any{}, &out); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.CSV)).ReadAll()
	if err != nil || out.Rows != 3 || len(rows) != 4 || out.Truncated || !strings.HasSuffix(out.Filename, ".csv") {
		t.Fatalf("%+v %v", out, err)
	}
	if rows[0][0] != "time" || rows[1][2] != "'=小秘" || rows[2][13] != "0.300000" {
		t.Fatalf("CSV：%v", rows)
	}
}

func TestCapsAndSetCaps(t *testing.T) {
	e := setup(t)
	var v CapView
	if err := e.call(owner, "xbot.usage.set_caps", map[string]any{"bot_id": e.a.ID, "daily_usd": 0.25, "monthly_usd": 10}, &v); err != nil {
		t.Fatal(err)
	}
	if v.DailyMicros != 250_000 || v.SpentDayMicros != 300_000 || !v.Exceeded || v.Label != "=小秘" {
		t.Fatalf("%+v", v)
	}
	bot, _ := e.db.GetBot(bg, e.a.ID)
	if c := proxy.ParseCaps(bot.Budget); c.DailyMicros != 250_000 || c.MonthlyMicros != 10_000_000 {
		t.Fatalf("存在 bot.budget：%s", bot.Budget)
	}
	// 只改每月：每日保留。
	_ = e.call(owner, "xbot.usage.set_caps", map[string]any{"bot_id": e.a.ID, "monthly_usd": 0}, &v)
	if v.DailyMicros != 250_000 || v.MonthlyMicros != 0 {
		t.Fatalf("%+v", v)
	}
	if err := e.call(owner, "xbot.usage.set_caps", map[string]any{"monthly_usd": 50}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.call(owner, "xbot.usage.set_caps", map[string]any{"provider": "oc", "daily_usd": 1}, nil); err != nil {
		t.Fatal(err)
	}
	for _, in := range []map[string]any{
		{"daily_usd": 1},   // 全域沒有每日上限
		{"bot_id": e.a.ID}, // 沒給上限
		{"bot_id": e.a.ID, "provider": "oc", "daily_usd": 1}, // 只能一個
		{"provider": "BAD", "daily_usd": 1},
		{"bot_id": e.a.ID, "daily_usd": -1},
	} {
		if err := e.call(owner, "xbot.usage.set_caps", in, nil); code(err) != ops.CodeInvalidInput {
			t.Errorf("%v：%v", in, err)
		}
	}
	var caps CapsOut
	if err := e.call(owner, "xbot.usage.caps", map[string]any{}, &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Global == nil || caps.Global.MonthlyMicros != 50_000_000 || len(caps.Bots) != 2 || len(caps.Providers) != 1 ||
		caps.Providers[0].Key != "oc" || caps.Day != "2026-10-18" || caps.Global.SpentMonMicros != 400_000 {
		t.Fatalf("%+v", caps)
	}
	caps = CapsOut{}
	_ = e.call(owner, "xbot.usage.caps", map[string]any{"bot_id": e.b.ID}, &caps)
	if caps.Global != nil || len(caps.Bots) != 1 {
		t.Fatalf("只看一個 Bot：%+v", caps)
	}
}

func TestSetCapsRisk(t *testing.T) {
	e := setup(t)
	agent := ops.Identity{Kind: ops.KindAgent, ID: "x", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeManage}}
	// 原本不限：設上限是收緊，直接做。
	if err := e.call(agent, "xbot.usage.set_caps", map[string]any{"bot_id": e.a.ID, "daily_usd": 2}, nil); err != nil {
		t.Fatalf("從不限到有上限：%v", err)
	}
	if err := e.call(agent, "xbot.usage.set_caps", map[string]any{"bot_id": e.a.ID, "daily_usd": 1}, nil); err != nil {
		t.Fatalf("降低：%v", err)
	}
	for _, in := range []map[string]any{
		{"bot_id": e.a.ID, "daily_usd": 3}, // 提高
		{"bot_id": e.a.ID, "daily_usd": 0}, // 取消上限
	} {
		if err := e.call(agent, "xbot.usage.set_caps", in, nil); code(err) != ops.CodePendingApproval {
			t.Errorf("%v 要核准：%v", in, err)
		}
	}
	if info, _ := e.reg.Get("xbot.usage.set_caps"); !info.NoDelegate || !info.DynamicRisk {
		t.Fatalf("提高上限永遠不能代為核准：%+v", info)
	}
	// 只能碰部分 Bot 的代理不能改全域。
	limited := agent
	limited.Resources = ops.Resources{Bots: []string{e.a.ID}}
	if err := e.call(limited, "xbot.usage.set_caps", map[string]any{"monthly_usd": 1}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	// 沒有啟動花費上限：一律當成要核准，呼叫回 internal。
	d := Deps{}
	if d.capsRisk(SetCapsIn{}) != ops.RiskConfirm {
		t.Fatal("沒啟動時要保守")
	}
}
