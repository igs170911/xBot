package proxy

import (
	"strings"
	"testing"
)

// OpenCode（使用者實測過的格式）。

func TestDeepSeekUsageAndOpenCodePrice(t *testing.T) {
	// deepseek-v4-flash 經 OpenCode Go：快取命中寫在 prompt_cache_hit_tokens；推理 token 可能吃光 max_tokens，內容是空的。
	body := `{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":1000,"completion_tokens":16,` +
		`"prompt_cache_hit_tokens":800,"prompt_cache_miss_tokens":200,"completion_tokens_details":{"reasoning_tokens":16}}}`
	f := newFixture(t, jsonUpstream(200, body), func(o *Options) { o.Pricing = &Pricing{} })
	addProvider(t, f, Provider{ID: "opencode-go", BaseURL: f.upstream.URL + "/zen/go/v1", Preset: "opencode-go", APIKey: "sk-oc-0000000000000"})
	resp := f.do("POST", "/model/opencode-go/v1/chat/completions", "Bearer "+f.botKey, `{"model":"deepseek-v4-flash","messages":[],"max_tokens":16}`)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	u := f.mem.Usage()[0]
	// 200×0.15 + 800×0.003 + 16×0.6 = 30 + 2.4 + 9.6 = 42
	if u.InputTokens != 1000 || u.CachedTokens != 800 || u.ReasoningTokens != 16 || u.CostMicros != 42 || u.Unpriced {
		t.Fatalf("%+v", u)
	}
	if pr, _, ok := LookupPrice(nil, "opencode-zen", "jev-1.13"); !ok || pr.Input != 0.042 {
		t.Fatalf("Zen：%+v", pr)
	}
	if _, _, ok := LookupPrice(nil, "other", "deepseek-v4-flash"); ok {
		t.Fatal("供應商專屬價格不能套到別的供應商")
	}
}

func TestFreeTierErrorIsSurfaced(t *testing.T) {
	const msg = `{"error":{"type":"FreeTierError","message":"Free models are only available in OpenCode"}}`
	f := newFixture(t, jsonUpstream(403, msg), func(o *Options) {
		o.Bindings = staticBinder{Provider: "openai", Fallback: &Route{Provider: "openai", Model: "paid", Transport: TransportChat}}
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, `{"model":"x-free","messages":[]}`)
	body := readAll(t, resp)
	if resp.StatusCode != 403 || !strings.Contains(body, "FreeTierError") || len(f.upstreamCalls()) != 1 {
		t.Fatalf("原樣轉回、不當成 502、不備援：%d %s", resp.StatusCode, body)
	}
	if got := ClassifyCheck(403, errorText(403, []byte(msg))); got != CheckFreeTier {
		t.Fatalf("探測要分出免費模型限制：%s", got)
	}
	c := (&Prober{}).CheckModel(t.Context(), Provider{ID: "z", BaseURL: f.upstream.URL + "/v1", APIKey: "k"}, "x-free", TransportChat)
	if c.Status != CheckFreeTier || !strings.Contains(c.Message, "免費模型") || len(f.upstreamCalls()) != 2 {
		t.Fatalf("探測只試一次就停：%+v", c)
	}
}
