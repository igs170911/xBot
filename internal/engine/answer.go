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

// Signal 回答這個工具是不是只是告訴 XBot 一件事（xbot.chat.wait_for_user：在等你）：它前後寫的字是同一段回答，不在這裡切開。
// 2026-10-24 deepseek 把三個名字和推薦寫在呼叫它的同一步，工具回來後只補「三個都列在上面了，你點一個就好」；
// 照工具切開，前面那段（不到 InterimNarrationMax 字）被當成旁白，你只看到最後那句。各引擎的名稱寫法不同
// （mcp__xbot__xbot_chat_wait_for_user、xbot.chat.wait_for_user），看結尾。
func Signal(tool string) bool { return strings.Contains(tool, "wait_for_user") }
