package hermes

import (
	"slices"
	"strings"
	"testing"
)

// 看圖：主要模型看得懂就直接給它；看不懂但有別的模型看得懂就請它先描述；都沒有就照實說看不到。
func TestConfigVision(t *testing.T) {
	tools := func(cfg map[string]any) []any { return cfg["platform_toolsets"].(map[string]any)["cli"].([]any) }
	hint := func(cfg map[string]any) string {
		return cfg["platform_hints"].(map[string]any)["cli"].(map[string]any)["replace"].(string)
	}
	aux := func(cfg map[string]any) map[string]any { return cfg["auxiliary"].(map[string]any) }

	cfg := Config(nil, Profile{ProviderID: "openai-codex", Model: "gpt-5.6-luna", WorkerKey: "k", Vision: true})
	if cfg["model"].(map[string]any)["supports_vision"] != true || cfg["agent"].(map[string]any)["image_input_mode"] != "native" ||
		!slices.Contains(tools(cfg), any("vision")) || strings.Contains(hint(cfg), "看不到圖片") {
		t.Fatalf("看得懂圖片的模型：%v %v", cfg["model"], cfg["agent"])
	}

	p := Profile{ProviderID: "openai", Model: "deepseek-v4.1-flash", WorkerKey: "k",
		VisionAux: &AuxModel{ProviderID: "openai-codex", Model: "gpt-5.6-luna", Transport: "codex_responses"}}
	cfg = Config(cfg, p)
	v, _ := aux(cfg)["vision"].(map[string]any)
	if cfg["model"].(map[string]any)["supports_vision"] != false || cfg["agent"].(map[string]any)["image_input_mode"] != "text" ||
		!slices.Contains(tools(cfg), any("vision")) || v["base_url"] != ModelBaseURL("openai-codex") || v["model"] != "gpt-5.6-luna" ||
		v["key_env"] != TokenEnv || v["api_mode"] != "codex_responses" || strings.Contains(hint(cfg), "看不到圖片") {
		t.Fatalf("請別的模型描述圖片：%v %v", cfg["agent"], v)
	}
	if _, ok := v["api_key"]; ok {
		t.Fatal("金鑰不能寫進設定（用 key_env 讀 worker-key）")
	}

	p.VisionAux = nil
	cfg = Config(cfg, p)
	if _, ok := aux(cfg)["vision"]; ok || slices.Contains(tools(cfg), any("vision")) || !strings.Contains(hint(cfg), "看不到圖片") {
		t.Fatalf("沒有看得懂圖片的模型：%v %v", aux(cfg), hint(cfg))
	}
}

// 學到的事寫在基本守則後面、回話風格前面；沒有就不寫這一段。
func TestSoulLessons(t *testing.T) {
	soul := Soul(Profile{Name: "小幫手", Lessons: []string{"網站被擋時先換比價網站找"}})
	i, j := strings.Index(soul, "## 學到的事"), strings.Index(soul, replyStyleHead)
	if i < 0 || j < i || !strings.Contains(soul, "網站被擋時先換比價網站找") {
		t.Fatalf("%s", soul)
	}
	if strings.Contains(Soul(Profile{Name: "小幫手"}), "學到的事") {
		t.Fatal("沒有學到的事就不寫")
	}
}
