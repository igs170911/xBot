package collab

import (
	"regexp"
	"strings"
	"unicode"
)

// IsAcknowledgement 判斷一段文字是否只是「收到」「好的」等不帶實質內容的回應。
// 規則：
//  1. 去除標點、空白、emoji 後，必須 ≤ 20 runes
//  2. 只能由確認/感謝/讚美詞組成（中英文）
//  3. 不能含 '?'、'？'、數字、URL、程式碼（` ）、@提及
func IsAcknowledgement(text string) bool {
	if text == "" {
		return false
	}

	// 去除前後空白
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	// 檢查排除項：問號、@提及、URL、程式碼標記
	if strings.ContainsAny(text, "?？") {
		return false
	}
	if strings.Contains(text, "@") {
		return false
	}
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
		return false
	}
	if strings.Contains(text, "`") {
		return false
	}

	// 檢查是否有數字
	for _, r := range text {
		if unicode.IsDigit(r) {
			return false
		}
	}

	// 去除標點和空白、emoji
	cleaned := stripPunctWhitespaceEmoji(text)
	if cleaned == "" {
		return false
	}

	// 檢查長度 ≤ 20 runes
	runes := []rune(cleaned)
	if len(runes) > 20 {
		return false
	}

	// 檢查是否只由確認/感謝/讚美詞彙組成
	return isAllAckTokens(cleaned)
}

// stripPunctWhitespaceEmoji 移除標點符號、空白和 emoji
func stripPunctWhitespaceEmoji(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// isAllAckTokens 檢查字串是否只由確認/感謝/讚美詞彙組成
func isAllAckTokens(cleaned string) bool {
	// 常見的確認/感謝/讚美詞彙（中英文）
	// 按長度降序排列，避免短詞先匹配導致長詞無法識別
	ackTokens := []string{
		// 長詞優先
		"acknowledged", "thankyou", "沒問題", "瞭解", "了解",
		"awesome", "perfect", "excellent", "alright",
		"感謝", "謝謝", "多謝", "謝了", "知道", "明白", "清楚",
		"太好", "很好", "不錯", "厲害", "優秀", "收到",
		"thanks", "thank", "great", "sweet", "roger",
		"okay", "gotit", "right", "cool", "good", "fine", "sure",
		"贊", "讚", "棒", "可以", "問題", "行", "懂",
		"copy", "nice", "yep", "yeah", "thx",
		// 單字
		"ok", "k", "y", "you", "got", "it", "ack", "ty",
		"好", "的", "嗯", "哦", "喔", "啊", "呀", "呢", "了", "沒",
		"謝", "太", "很", "yup",
	}

	// 將 cleaned 小寫（for 英文）
	lower := strings.ToLower(cleaned)

	// 使用貪婪匹配：反覆移除匹配的 token，最後應該是空字串
	remaining := lower
	changed := true
	maxIter := 50
	iter := 0
	for changed && iter < maxIter {
		changed = false
		iter++
		for _, tok := range ackTokens {
			if strings.Contains(remaining, tok) {
				remaining = strings.ReplaceAll(remaining, tok, "")
				changed = true
			}
		}
	}

	// 如果全部都能被 token 匹配掉，則是 filler
	return remaining == ""
}

// isFiller 是 IsAcknowledgement 的別名，供外部呼叫
func isFiller(text string) bool {
	return IsAcknowledgement(text)
}

// 用於提取關鍵字的輔助函數（保留原有用途）
var (
	urlPattern  = regexp.MustCompile(`https?://[^\s]+`)
	codePattern = regexp.MustCompile("`[^`]+`")
)

// RemoveURLsAndCode 移除 URL 和程式碼片段
func RemoveURLsAndCode(text string) string {
	text = urlPattern.ReplaceAllString(text, "")
	text = codePattern.ReplaceAllString(text, "")
	return text
}
