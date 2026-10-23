package engine

import (
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
)

// FinalAnswer 從一輪的文字挑出最後的回答。segs 是依工具呼叫切開的文字段，最後一段是最後一次呼叫工具之後寫的。
// 用工具前寫的短旁白（「我先看一下…」「接著把表格改窄」，每段不超過 limits.InterimNarrationMax 字）不算回答；
// 比這長的段落留下（可能是先寫完答案再存檔）。最後一段是空的（最後是工具呼叫，沒寫結論）回傳空字串，照原本整輪的文字。
// （2026-10-24 Grok 做儀表板用了 39 步，十幾句旁白加起來超過上限，整段接在「做好了」前面交出去。）
func FinalAnswer(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	last := strings.TrimSpace(segs[len(segs)-1])
	if last == "" {
		return ""
	}
	var keep []string
	for _, s := range segs[:len(segs)-1] {
		if s = strings.TrimSpace(s); utf8.RuneCountInString(s) > limits.InterimNarrationMax {
			keep = append(keep, s)
		}
	}
	return strings.Join(append(keep, last), "\n\n")
}
