package models

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

type fakeProber struct {
	mu     sync.Mutex
	checks []string
}

func (f *fakeProber) ListModels(context.Context, proxy.Provider) ([]string, string) {
	return []string{"deepseek-v4-flash", "kimi-k3"}, ""
}

func (f *fakeProber) CheckModel(_ context.Context, _ proxy.Provider, model, guess string) proxy.ModelCheck {
	f.mu.Lock()
	f.checks = append(f.checks, model+"@"+guess)
	f.mu.Unlock()
	return proxy.ModelCheck{Model: model, Status: proxy.CheckOK, Transport: proxy.TransportResponses, Message: "可以使用"}
}

func setupFull(t *testing.T) (*env, *fakeProber) {
	t.Helper()
	e := setup(t)
	fp := &fakeProber{}
	// 換一個有假探測器的登錄表。
	e.reg = ops.New(ops.Config{DB: e.db})
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, Deps{Providers: e.pv, Lister: proxy.NewDBStore(e.db), Prober: fp})
	return e, fp
}

func TestPresetsAndAddProvider(t *testing.T) {
	e, _ := setupFull(t)
	var ps PresetsOut
	raw, err := e.call(owner, "xbot.models.presets", map[string]any{})
	if err != nil || json.Unmarshal(raw, &ps) != nil {
		t.Fatal(err)
	}
	var found *PresetView
	for i := range ps.Presets {
		if ps.Presets[i].ID == "opencode-go" {
			found = &ps.Presets[i]
		}
	}
	if found == nil || found.DefaultModel != "deepseek-v4-flash" || found.SessionHeader == "" || found.Configured {
		t.Fatalf("OpenCode Go 範本：%+v", found)
	}
	// 用範本只要貼金鑰。
	if _, err := e.call(owner, "xbot.models.add_provider", map[string]any{"preset": "opencode-go"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("沒金鑰：%v", err)
	}
	raw, err = e.call(owner, "xbot.models.add_provider", map[string]any{"preset": "opencode-go", "api_key": secret, "default_model": "deepseek-v4-flash"})
	if err != nil || strings.Contains(string(raw), secret) {
		t.Fatalf("%s %v", raw, err)
	}
	p, err := e.pv.Get(bg, "opencode-go")
	if err != nil || p.BaseURL != "https://opencode.ai/zen/go/v1" || p.Preset != "opencode-go" || p.Label != "OpenCode Go" || string(p.APIKey) != secret {
		t.Fatalf("%+v %v", p, err)
	}
	var def runtime.ModelRef
	if _, _ = e.db.GetSettingInto(bg, runtime.DefaultModelSetting, &def); def.Provider != "opencode-go" || def.Model != "deepseek-v4-flash" || def.Transport != proxy.TransportChat {
		t.Fatalf("預設模型：%+v", def)
	}
	if _, err := e.call(owner, "xbot.models.add_provider", map[string]any{"preset": "opencode-go", "api_key": "x"}); code(err) != ops.CodeConflict {
		t.Fatalf("重複：%v", err)
	}
	// 自訂供應商；代理不能加（金鑰只能你本人設定）。
	if _, err := e.call(owner, "xbot.models.add_provider", map[string]any{"id": "local", "base_url": "http://127.0.0.1:1234/v1", "transport": "chat_completions"}); err != nil {
		t.Fatal(err)
	}
	admin := ops.Identity{Kind: ops.KindAgent, ID: "a", Scopes: []ops.Scope{ops.ScopeAdmin}}
	if _, err := e.call(admin, "xbot.models.add_provider", map[string]any{"id": "x", "base_url": "https://x"}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理：%v", err)
	}
	var g GetOut
	raw, _ = e.call(owner, "xbot.models.get", map[string]any{})
	_ = json.Unmarshal(raw, &g)
	if len(g.Providers) != 2 || g.Providers[1].ID != "opencode-go" || len(g.Providers[1].Models) == 0 || g.Providers[1].Label != "OpenCode Go" {
		t.Fatalf("models.get：%s", raw)
	}
}

func TestUpdateAndRemoveProvider(t *testing.T) {
	e, _ := setupFull(t)
	_, _ = e.call(owner, "xbot.models.add_provider", map[string]any{"id": "a", "base_url": "https://a.example/v1", "api_key": secret, "default_model": "m"})
	_, _ = e.call(owner, "xbot.models.add_provider", map[string]any{"id": "b", "base_url": "https://b.example/v1", "api_key": secret})
	raw, err := e.call(owner, "xbot.models.update_provider", map[string]any{"id": "b", "label": "B 牌", "transport": "codex_responses"})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.pv.Get(bg, "b"); p.Label != "B 牌" || p.Transport != proxy.TransportResponses || string(p.APIKey) != secret {
		t.Fatalf("只改有給的：%+v %s", p, raw)
	}
	if _, err := e.call(owner, "xbot.models.update_provider", map[string]any{"id": "zz"}); code(err) != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}
	// 預設模型用的不能移除。
	if _, err := e.call(owner, "xbot.models.remove_provider", map[string]any{"id": "a"}); code(err) != ops.CodeConflict {
		t.Fatalf("%v", err)
	}
	bot, _ := e.db.CreateBot(bg, store.Bot{Name: "x", Model: "b/m2"})
	_, _ = e.call(owner, "xbot.models.set_fallback", map[string]any{"provider": "b", "model": "m3"})
	// 代理要求移除：一定要核准。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a", Scopes: []ops.Scope{ops.ScopeAdmin}}
	if _, err := e.call(agent, "xbot.models.remove_provider", map[string]any{"id": "b"}); code(err) != ops.CodePendingApproval {
		t.Fatalf("要核准：%v", err)
	}
	if info, _ := e.reg.Get("xbot.models.remove_provider"); !info.NoDelegate || info.Risk != ops.RiskConfirm {
		t.Fatalf("%+v", info)
	}
	var out RemoveProviderOut
	raw, err = e.call(owner, "xbot.models.remove_provider", map[string]any{"id": "b"})
	if err != nil || json.Unmarshal(raw, &out) != nil {
		t.Fatal(err)
	}
	if !out.Removed || !out.ClearedFallback || len(out.AffectedBots) != 1 || out.AffectedBots[0] != bot.ID {
		t.Fatalf("%+v", out)
	}
	if _, err := e.db.GetSetting(bg, runtime.FallbackModelSetting); err == nil {
		t.Fatal("全域備援應該清掉")
	}
}

func TestSetFallback(t *testing.T) {
	e, _ := setupFull(t)
	if _, err := e.call(owner, "xbot.models.set_fallback", map[string]any{"provider": "nope", "model": "m"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("%v", err)
	}
	_, _ = e.call(owner, "xbot.models.add_provider", map[string]any{"id": "a", "base_url": "https://a.example/v1", "api_key": secret})
	if _, err := e.call(owner, "xbot.models.set_fallback", map[string]any{"provider": "a", "model": "cheap"}); err != nil {
		t.Fatal(err)
	}
	var g GetOut
	raw, _ := e.call(owner, "xbot.models.get", map[string]any{})
	_ = json.Unmarshal(raw, &g)
	if g.Fallback == nil || g.Fallback.Model != "cheap" {
		t.Fatalf("%s", raw)
	}
	if _, err := e.call(owner, "xbot.models.set_fallback", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	raw, _ = e.call(owner, "xbot.models.get", map[string]any{})
	if strings.Contains(string(raw), "cheap") {
		t.Fatalf("清掉：%s", raw)
	}
}

func TestProbeStoresResultAndUpdatesDefault(t *testing.T) {
	e, fp := setupFull(t)
	_, _ = e.call(owner, "xbot.models.add_provider", map[string]any{"preset": "opencode-go", "api_key": secret, "default_model": "deepseek-v4-flash"})
	// 預演不連線。
	if _, err := e.call(owner, "xbot.models.probe", map[string]any{"provider": "opencode-go"}, ops.Options{DryRun: true}); err != nil || len(fp.checks) != 0 {
		t.Fatalf("%v %v", err, fp.checks)
	}
	var out ProbeOut
	raw, err := e.call(owner, "xbot.models.probe", map[string]any{"provider": "opencode-go"})
	if err != nil || json.Unmarshal(raw, &out) != nil {
		t.Fatal(err)
	}
	if out.Check == nil || out.Check.Model != "deepseek-v4-flash" || !out.DefaultUpdated || len(out.Models) != 2 {
		t.Fatalf("%s", raw)
	}
	if fp.checks[0] != "deepseek-v4-flash@chat_completions" {
		t.Fatalf("先猜範本的呼叫方式：%v", fp.checks)
	}
	var def runtime.ModelRef
	_, _ = e.db.GetSettingInto(bg, runtime.DefaultModelSetting, &def)
	if def.Transport != proxy.TransportResponses {
		t.Fatalf("預設模型的呼叫方式要更新：%+v", def)
	}
	ps := proxy.NewDBStore(e.db)
	if tr, _ := ps.TransportFor(bg, "opencode-go", "deepseek-v4-flash"); tr != proxy.TransportResponses {
		t.Fatalf("探測結果要存起來：%q", tr)
	}
	var g GetOut
	raw, _ = e.call(owner, "xbot.models.get", map[string]any{})
	_ = json.Unmarshal(raw, &g)
	if g.Providers[0].ProbedAt == 0 || strings.Join(g.Providers[0].Models, ",") != "deepseek-v4-flash,kimi-k3" {
		t.Fatalf("%s", raw)
	}
	if _, err := e.call(owner, "xbot.models.probe", map[string]any{"provider": "nope"}); code(err) != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}
}

func TestPricingOps(t *testing.T) {
	e, _ := setupFull(t)
	_, _ = e.db.AddUsage(bg, store.UsageEntry{Provider: "p", Model: "mystery", Unpriced: true})
	var out PricingOut
	raw, err := e.call(owner, "xbot.models.pricing", map[string]any{})
	if err != nil || json.Unmarshal(raw, &out) != nil {
		t.Fatal(err)
	}
	if len(out.Prices) == 0 || out.Prices[0].Source != "default" || len(out.Unpriced) != 1 || out.Unpriced[0].Model != "mystery" {
		t.Fatalf("%s", raw)
	}
	// 調價格要核准（會影響花費上限），而且不能代為核准。
	if info, _ := e.reg.Get("xbot.models.set_price"); info.Risk != ops.RiskConfirm || !info.NoDelegate {
		t.Fatalf("%+v", info)
	}
	if _, err := e.call(owner, "xbot.models.set_price", map[string]any{"model": "mystery", "input": 1, "output": 2}); err != nil {
		t.Fatal(err)
	}
	raw, _ = e.call(owner, "xbot.models.pricing", map[string]any{})
	_ = json.Unmarshal(raw, &out)
	if out.Prices[0].Model != "mystery" || out.Prices[0].Source != "custom" || len(out.Unpriced) != 0 {
		t.Fatalf("補了價格就不再列為沒有價格：%s", raw)
	}
	if _, err := e.call(owner, "xbot.models.set_price", map[string]any{"model": "mystery", "remove": true}); err != nil {
		t.Fatal(err)
	}
	if pr, _, ok, _ := (&proxy.Pricing{DB: e.db}).Lookup(bg, "p", "mystery"); ok {
		t.Fatalf("移除：%+v", pr)
	}
	if _, err := e.call(owner, "xbot.models.set_price", map[string]any{"model": "x", "input": -1}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("負數：%v", err)
	}
}
