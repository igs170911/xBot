package runtime

import (
	"bytes"
	"context"
	"testing"

	"xbot/internal/proxy"
)

// 看圖模型：便宜的先，同樣便宜時預設模型先；沒有看得懂圖片的模型時是 nil。
func TestVisionAux(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	ps := proxy.NewDBStore(f.db)
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{5}, 32))
	pv := proxy.NewProviders(ps, kr)
	c := &Computers{DB: f.db, Providers: ps}
	_ = pv.Put(ctx, proxy.Provider{ID: "opencode", BaseURL: "https://opencode.ai/zen/go/v1", APIKey: "k"})
	_ = ps.PutProbe(ctx, proxy.ProbeResult{Provider: "opencode", Models: []string{"deepseek-v4.1-flash", "glm-5.3"}})
	_ = f.db.SetSetting(ctx, DefaultModelSetting, ModelRef{Provider: "opencode", Model: "deepseek-v4.1-flash"})
	if a := c.visionAux(ctx); a != nil {
		t.Fatalf("都看不懂圖片：%+v", a)
	}

	_ = ps.PutProbe(ctx, proxy.ProbeResult{Provider: "opencode", Models: []string{"deepseek-v4.1-flash", "grok-4.7", "gpt-6-sol"}})
	if a := c.visionAux(ctx); a == nil || a.ProviderID != "opencode" || a.Model != "grok-4.7" {
		t.Fatalf("探測到的看圖模型：%+v", a)
	}
	// 探測過可以用的，比只在清單上的先（只在清單上的可能叫不動）。
	_ = ps.PutProbe(ctx, proxy.ProbeResult{Provider: "opencode", Models: []string{"deepseek-v4.1-flash", "claude-haiku-5-5", "gpt-6-sol"},
		Checks: map[string]proxy.ModelCheck{"gpt-6-sol": {Status: proxy.CheckOK, Transport: proxy.TransportResponses}}})
	if a := c.visionAux(ctx); a == nil || a.Model != "gpt-6-sol" {
		t.Fatalf("探測過的先：%+v", a)
	}
	// ChatGPT 方案（訂閱）最先，裡面便宜的先。
	_ = ps.PutProvider(ctx, proxy.ProviderRecord{ID: "chatgpt", BaseURL: "https://chatgpt.com/backend-api/codex", Transport: proxy.TransportResponses, Auth: proxy.AuthOAuthCodex})
	_ = ps.PutProbe(ctx, proxy.ProbeResult{Provider: "chatgpt", Models: []string{"gpt-6-sol", "gpt-5.6-luna"}})
	if a := c.visionAux(ctx); a == nil || a.ProviderID != "chatgpt" || a.Model != "gpt-5.6-luna" || a.Transport != proxy.TransportResponses {
		t.Fatalf("ChatGPT 方案、便宜的先：%+v", a)
	}
}
