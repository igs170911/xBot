package runtime

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"xbot/internal/proxy"
	"xbot/internal/store"
)

func TestBindingAndTransports(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	ps := proxy.NewDBStore(f.db)
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{5}, 32))
	pv := proxy.NewProviders(ps, kr)
	_ = pv.Put(ctx, proxy.Provider{ID: "openai", BaseURL: "https://api.openai.com/v1", Transport: proxy.TransportResponses, APIKey: "k"})
	_ = pv.Put(ctx, proxy.Provider{ID: "claude", BaseURL: "https://api.anthropic.com/v1", APIKey: "k"})
	_ = pv.Put(ctx, proxy.Provider{ID: "cheap", BaseURL: "https://cheap.example/v1", APIKey: "k"})
	_ = f.db.SetSetting(ctx, DefaultModelSetting, ModelRef{Provider: "openai", Model: "gpt-x", Transport: proxy.TransportChat})
	c := &Computers{DB: f.db, Providers: ps}

	check := func(model string, want ModelRef) {
		t.Helper()
		got, err := c.ResolveModel(ctx, store.Bot{Model: model})
		if err != nil || got != want {
			t.Fatalf("%q → %+v %v，要 %+v", model, got, err, want)
		}
	}
	check("", ModelRef{Provider: "openai", Model: "gpt-x", Transport: proxy.TransportChat})                   // 預設模型照設定
	check("gpt-y", ModelRef{Provider: "openai", Model: "gpt-y", Transport: proxy.TransportResponses})         // 供應商預設
	check("claude/sonnet", ModelRef{Provider: "claude", Model: "sonnet", Transport: proxy.TransportMessages}) // Hermes 規則
	_ = ps.PutProbe(ctx, proxy.ProbeResult{Provider: "cheap", Checks: map[string]proxy.ModelCheck{"m": {Status: proxy.CheckOK, Transport: proxy.TransportResponses}}})
	check("cheap/m", ModelRef{Provider: "cheap", Model: "m", Transport: proxy.TransportResponses}) // 探測結果

	// 綁定：只能用自己模型的供應商；備援先看 Bot 的，再看全域的。
	bot, _ := f.db.CreateBot(ctx, store.Bot{Name: "x", Model: "claude/sonnet", Fallback: []string{"cheap/m"}})
	b, err := c.Binding(ctx, bot.ID)
	if err != nil || b.Provider != "claude" || b.Fallback == nil || *b.Fallback != (proxy.Route{Provider: "cheap", Model: "m", Transport: proxy.TransportResponses}) {
		t.Fatalf("%+v %v", b, err)
	}
	plain, _ := f.db.CreateBot(ctx, store.Bot{Name: "y"})
	if b, _ := c.Binding(ctx, plain.ID); b.Provider != "openai" || b.Fallback != nil {
		t.Fatalf("沒有備援：%+v", b)
	}
	_ = f.db.SetSetting(ctx, FallbackModelSetting, ModelRef{Provider: "cheap", Model: "m2"})
	if b, _ := c.Binding(ctx, plain.ID); b.Fallback == nil || b.Fallback.Model != "m2" || b.Fallback.Transport != "" {
		t.Fatalf("全域備援：%+v", b.Fallback)
	}
	// 全域備援的供應商被移除了：不用。
	_ = f.db.SetSetting(ctx, FallbackModelSetting, ModelRef{Provider: "gone", Model: "m2"})
	if b, _ := c.Binding(ctx, plain.ID); b.Fallback != nil {
		t.Fatalf("%+v", b.Fallback)
	}
	if _, err := c.Binding(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestChatChecksBudget(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	over := &proxy.BudgetError{Scope: proxy.ScopeBotDaily}
	var gotProvider string
	f.rt.o.Budget = func(_ context.Context, _, provider string) error { gotProvider = provider; return over }
	if _, err := f.rt.Chat(ctx, f.bot.ID, "", "嗨"); !errors.Is(err, over) || gotProvider != "openai" {
		t.Fatalf("%v %q", err, gotProvider)
	}
	if threads, _ := f.db.ListThreads(ctx, f.bot.ID, false); len(threads) != 0 {
		t.Fatal("被擋時不能開對話串")
	}
}
