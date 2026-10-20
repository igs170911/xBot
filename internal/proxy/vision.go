package proxy

import (
	"regexp"
	"strings"
)

// 看得懂圖片的模型（使用者附的截圖、照片）。
//
// LazyBoy 是在模型清單上逐個標 vision（server/computers.py：有標才開 Hermes 的 vision 工具、image_input_mode native）；
// XBot 的模型清單是探測來的，沒有這個欄位，所以照模型名稱判斷（2026-10-20 起：原本一律當看不懂，Bot 收到截圖會去看
// 自己的桌面、回答別的東西）。認不出的當成看不懂：Hermes 會改請看得懂的模型先描述圖片（runtime 挑），不會把圖片送給
// 不收圖片的模型而被拒。
var visionRe = regexp.MustCompile(`^(gpt-4o|gpt-4\.1|gpt-4\.5|gpt-5|gpt-6|gpt-7|o3|o4|chatgpt-4o|` +
	`claude-|gemini-|gemma-3|grok-4|grok-5|grok-vision|grok-2-vision|` +
	`pixtral|mistral-medium|mistral-small-3|llama-4|llava|` +
	`qwen[0-9.]*-vl|qwen-vl|qvq|glm-[0-9.]+v|kimi-vl|kimi-k2\.5|kimi-k3|minicpm-v|internvl)`)

// 名稱看起來像但不收圖片的（純文字或寫程式的變體）。
var noVisionRe = regexp.MustCompile(`(^gpt-oss|codex-mini|-code-fast|^grok-code|-embedding|-tts|-transcribe|-audio|-realtime|-search-preview)`)

// SeesImages 回答這個模型看不看得懂圖片。model 可以帶供應商前綴（例：anthropic/claude-sonnet-5.5、x-ai/grok-4.7）。
func SeesImages(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if m == "" || noVisionRe.MatchString(m) {
		return false
	}
	return visionRe.MatchString(m)
}

// cheapHints 是名稱裡表示便宜、快的字：挑看圖的模型時先用這些（描述一張圖不需要大模型）。
var cheapHints = []string{"luna", "mini", "flash", "haiku", "lite", "nano", "small", "fast"}

// VisionRank 是挑看圖模型的順序：越小越先用。看不懂圖片的回 -1。
func VisionRank(model string) int {
	if !SeesImages(model) {
		return -1
	}
	m := strings.ToLower(model)
	for _, h := range cheapHints {
		if strings.Contains(m, h) {
			return 0
		}
	}
	return 1
}
