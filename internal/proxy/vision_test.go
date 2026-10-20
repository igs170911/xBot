package proxy

import "testing"

func TestSeesImages(t *testing.T) {
	for m, want := range map[string]bool{
		"gpt-5.6-luna": true, "gpt-6-sol": true, "gpt-4o-mini": true, "anthropic/claude-sonnet-5.5": true, "claude-haiku-5-5": true,
		"gemini-4-argon-contributor": true, "x-ai/grok-4.7": true, "qwen3-vl-plus": true, "glm-5v": true, "kimi-k3": true,
		"deepseek-v4.1-flash": false, "glm-5.3": false, "beam-free": false, "gpt-oss-120b": false, "grok-code-fast-1": false,
		"text-embedding-3-small": false, "gpt-4o-mini-tts": false, "": false,
	} {
		if got := SeesImages(m); got != want {
			t.Errorf("%s：%v，應該是 %v", m, got, want)
		}
	}
	if VisionRank("gpt-5.6-luna") != 0 || VisionRank("gpt-6-sol") != 1 || VisionRank("deepseek-v4.1-flash") != -1 {
		t.Error("便宜的看圖模型要先用")
	}
}
