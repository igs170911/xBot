package hermes

import "testing"

// ChatGPT 方案（oauth_codex）的 Bot 關掉 tool_search（E13）；換回一般供應商時拿掉，回到 Hermes 預設。
func TestConfigToolSearchOffForChatGPTPlan(t *testing.T) {
	p := Profile{ProviderID: "chatgpt", Model: "gpt-5", WorkerKey: "k", NoToolSearch: true}
	cfg := Config(map[string]any{"tools": map[string]any{"tool_search": map[string]any{"threshold_pct": 7}}}, p)
	ts := cfg["tools"].(map[string]any)["tool_search"].(map[string]any)
	if ts["enabled"] != "off" || ts["threshold_pct"] != 7 {
		t.Fatalf("tool_search = %v", ts)
	}
	p.NoToolSearch = false
	cfg = Config(cfg, p)
	ts = cfg["tools"].(map[string]any)["tool_search"].(map[string]any)
	if _, ok := ts["enabled"]; ok || ts["threshold_pct"] != 7 {
		t.Fatalf("換回一般供應商後 tool_search = %v", ts)
	}
	// 一般供應商、沒有 tools 設定：不多寫。
	if cfg = Config(nil, p); cfg["tools"] != nil {
		t.Fatalf("不該寫 tools：%v", cfg["tools"])
	}
	// 沒有 tools 設定的 ChatGPT 方案：補上。
	p.NoToolSearch = true
	cfg = Config(nil, p)
	if cfg["tools"].(map[string]any)["tool_search"].(map[string]any)["enabled"] != "off" {
		t.Fatalf("tools = %v", cfg["tools"])
	}
}
