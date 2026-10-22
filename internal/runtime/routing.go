package runtime

import (
	"context"
	"log/slog"
	"strings"

	"xbot/internal/judge"
	"xbot/internal/store"
)

// Decider 是快判服務的介面（讓測試可以 mock）。
type Decider interface {
	Decide(ctx context.Context, a judge.Ask) (judge.Decision, error)
}

// Router 處理對話分流（UseRoute）：閒聊和查進度用快速的聊天模型，實際工作用完整的工作模型並開電腦。
// 這是 Phase 1 的核心：即時回應。
type Router struct {
	Decider Decider
	// FastModel 是聊天模型（不開電腦、便宜快速的模型），例：OpenCode Go 的 flash。
	// nil 時所有訊息都走 Bot 自己的模型。
	FastModel *ModelRef
	Logger    *slog.Logger
}

// Route 決定這則訊息要怎麼處理，並在需要時先回應一句確認。
// 返回值：shouldOpenComputer（是否需要開電腦）、quickReply（即時回應的文字，空白表示不需要）
func (r *Router) Route(ctx context.Context, b store.Bot, threadID, text string) (shouldOpenComputer bool, quickReply string) {
	if r.Decider == nil {
		// 沒有快判服務：所有訊息都用完整流程
		return true, ""
	}

	// 對話分流快判（UseRoute）
	ask := judge.Ask{
		Use:    judge.UseRoute,
		Text:   text,
		BotID:  b.ID,
		Ref:    threadID,
	}
	
	dec, err := r.Decider.Decide(ctx, ask)
	if err != nil || dec.Outcome != judge.OutcomeOK {
		// 快判失敗或未設定：走完整流程
		return true, ""
	}
	
	if r.Logger != nil {
		r.Logger.Debug("對話分流", "text", routingClipText(text, 50), "label", dec.Label, "p", dec.P, "by", dec.By, "outcome", dec.Outcome)
	}

	// 根據分流結果決定處理方式
	switch dec.Label {
	case "chat":
		// 閒聊：用聊天模型快速回應，不開電腦
		return false, r.quickChatReply(ctx, b, text)
		
	case "progress":
		// 查進度：直接回報進度，不開電腦
		// 注意：進度資訊的組裝在 runtime.Progress 裡，這裡只做決策
		return false, ""
		
	case "work":
		// 實際工作：需要完整流程
		// 如果是複雜任務，先回一句確認讓使用者知道 Bot 收到了
		if r.shouldAck(text) {
			return true, r.workAckReply(text)
		}
		return true, ""
	}

	// 預設：走完整流程（寧可做多不要做少）
	return true, ""
}

// quickChatReply 用聊天模型回應簡單的閒聊或問候。
func (r *Router) quickChatReply(ctx context.Context, b store.Bot, text string) string {
	// 這裡應該呼叫快速聊天模型
	// 暫時返回空字串，在後續 PR 中實作實際的模型呼叫
	// TODO: 實作快速聊天模型呼叫
	return ""
}

// workAckReply 生成工作確認訊息。
func (r *Router) workAckReply(text string) string {
	// 分析文字判斷是什麼類型的工作，生成對應的確認訊息
	text = strings.TrimSpace(strings.ToLower(text))
	
	// 常見工作類型的確認語句
	patterns := []struct {
		keywords []string
		reply    string
	}{
		{[]string{"查", "搜", "找", "search", "find"}, "好的，我來查一下。"},
		{[]string{"比", "對比", "比較", "compare"}, "好，我來比較看看。"},
		{[]string{"寫", "製作", "建立", "create", "write"}, "收到，我開始製作。"},
		{[]string{"整理", "分析", "analyze"}, "好的，我來整理資料。"},
		{[]string{"下載", "抓取", "download"}, "收到，開始下載。"},
	}

	for _, p := range patterns {
		for _, kw := range p.keywords {
			if strings.Contains(text, kw) {
				return p.reply
			}
		}
	}

	// 預設確認訊息
	return "收到，我開始處理。"
}

// shouldAck 判斷是否需要先回應確認訊息。
// 判斷依據：訊息長度、關鍵字等
func (r *Router) shouldAck(text string) bool {
	// 簡短的訊息（< 20 字）通常不需要確認
	if len([]rune(text)) < 20 {
		return false
	}

	// 包含多個步驟的工作需要確認
	stepIndicators := []string{"然後", "接著", "再", "之後", "最後", "第一", "第二", "第三"}
	lower := strings.ToLower(text)
	matchCount := 0
	for _, ind := range stepIndicators {
		if strings.Contains(lower, ind) {
			matchCount++
			if matchCount >= 2 {
				return true
			}
		}
	}

	// 很長的訊息（> 100 字）需要確認
	if len([]rune(text)) > 100 {
		return true
	}

	return false
}

// routingClipText 截斷文字用於日誌（避免和 taskdone.go 的 clipText 重名）。
func routingClipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// IsProgressRoute 回答分流結果是不是查進度（外部會用到）。
func IsProgressRoute(label string) bool {
	return label == "progress"
}
