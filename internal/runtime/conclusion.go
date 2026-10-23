package runtime

import (
	"strings"
	"unicode/utf8"
)

// conclusionCheck 檢查文字是否像是一個工作的結論（2026-10-22：改善「沒有達成結論」問題）。
// 結論特徵：說明了做了什麼、結果如何、接下來怎麼辦（或不需要怎麼辦）；不是中途停下或過程描述。
func conclusionCheck(text string) (hasConclusion bool, confidence float64) {
	if strings.TrimSpace(text) == "" {
		return false, 0
	}

	runes := utf8.RuneCountInString(text)
	if runes < 20 { // 太短不太可能是完整結論
		return false, 0
	}

	lower := strings.ToLower(text)
	lastPart := text
	if len(text) > 200 {
		lastPart = string([]rune(text)[runes-100:]) // 檢查最後 100 字
	}
	lastLower := strings.ToLower(lastPart)

	// 正面指標：結論性用語
	conclusionPhrases := []string{
		"完成", "做好", "整理好", "建好", "處理好", "修好",
		"結果", "總共", "總結", "綜合",
		"已經", "現在", "目前",
	}
	positiveScore := 0.0
	for _, phrase := range conclusionPhrases {
		if strings.Contains(lastLower, phrase) {
			positiveScore += 0.2
		}
	}

	// 文件或連結提及（通常是交付成果）- 強指標
	if strings.Contains(lower, "/workspace") || strings.Contains(lower, "http") {
		positiveScore += 0.2
	}

	// 說明性語句（不用...、不需要...、只有...、就...）- 像是給建議或解釋
	if strings.Contains(lower, "不用") || strings.Contains(lower, "不需要") ||
		strings.Contains(lower, "只有") || strings.Contains(lower, "只要") ||
		strings.Contains(lower, "就好") || strings.Contains(lower, "就可以") {
		positiveScore += 0.15
	}

	// 負面指標：中途停下、計畫但未執行
	incompletePhrases := []string{
		"接下來", "然後", "接著",
		"正在", "正要", "準備",
		"等待", "等你", "需要你",
	}
	negativeScore := 0.0
	for _, phrase := range incompletePhrases {
		if strings.Contains(lastLower, phrase) {
			negativeScore += 0.25
		}
	}

	// 問句結尾不一定是壞事（「要調價嗎？」是結論+詢問下一步）
	// 但如果主要內容是問句就是未完成
	sentences := strings.Split(text, "\n\n")
	questionCount := 0
	for _, s := range sentences {
		if strings.HasSuffix(strings.TrimSpace(s), "？") || strings.HasSuffix(strings.TrimSpace(s), "?") {
			questionCount++
		}
	}
	if float64(questionCount)/float64(len(sentences)) > 0.5 {
		negativeScore += 0.2
	}

	confidence = positiveScore - negativeScore
	if confidence > 0.25 { // 略降門檻
		return true, confidence
	}
	return false, confidence
}

// RequestConclusion 生成一個請求結論的提示，讓引擎補上總結。
// 當檢測到回覆缺少結論時，runtime 可以用這個提示要求 engine 補充。
func RequestConclusion() string {
	return "（請簡短總結：你完成了什麼、還剩什麼、使用者接著要做什麼或不需要做什麼。）"
}
