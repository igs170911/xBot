package collab

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
)

// Silent 是 Bot 沒有要補充時的回答（LazyBoy SILENT）。
const Silent = "[silent]"

// 傳話的意圖（LazyBoy INTENTS）。
const (
	IntentRequest  = "request"  // 請求：要回覆（交辦，mode=work）
	IntentQuestion = "question" // 提問：要回覆（諮詢，mode=consult）
	IntentResult   = "result"   // 回覆：交回的結果，不算往來次數上限
	IntentFYI      = "fyi"      // 通知：不回覆（mode=notify）
)

var intentLabel = map[string]string{IntentRequest: "請求", IntentQuestion: "提問", IntentResult: "回覆", IntentFYI: "通知"}

// replyExpected 是要回覆的意圖（LazyBoy REPLY_EXPECTED）。
func replyExpected(intent string) bool { return intent == IntentRequest || intent == IntentQuestion }

// normalizeIntent 把 PRD AA-11 的委派模式（work／consult／notify）對到 LazyBoy 的意圖；不認得的當成 request。
// result 只有系統交回結果時用，Bot 不能自己送。
func normalizeIntent(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case IntentQuestion, "consult":
		return IntentQuestion
	case IntentFYI, "notify":
		return IntentFYI
	}
	return IntentRequest
}

// modeOf 是意圖對應的交辦模式（assignment.mode）。
func modeOf(intent string) string {
	switch intent {
	case IntentQuestion:
		return "consult"
	case IntentFYI:
		return "notify"
	}
	return "work"
}

// everyoneWords 是「所有人」的寫法（LazyBoy EVERYONE）。
var everyoneWords = map[string]bool{"everyone": true, "all": true, "所有人": true, "大家": true, "全部": true}

// plain 把空白壓成一個空格，超過 limit 個字時截斷並加「…」（LazyBoy plain）。
func plain(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit-1]) + "…"
}

// runes 回傳字數（同 Python len）。
func runes(s string) int { return utf8.RuneCountInString(s) }

// headRunes 回傳前 n 個字。
func headRunes(s string, n int) string {
	if runes(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// IsSilent 回答 Bot 是不是沒有要說的（回 [silent]，或什麼都沒寫；LazyBoy is_silent）。
func IsSilent(text string) bool {
	body := strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "`"))
	lower := strings.ToLower(body)
	return body == "" || lower == Silent || (strings.HasPrefix(lower, Silent) && runes(body) < runes(Silent)+limits.SilentTailMax)
}

// IsAcknowledgement 檢測純粹的致謝/確認訊息（沒有實質內容）。
// 這些訊息不應該觸發對方回覆，會造成無限對話循環（bot loop bug）。
// B4 (2026-10-22): 增強中文短確認訊息的檢測。
func IsAcknowledgement(text string) bool {
	body := strings.TrimSpace(text)
	if body == "" {
		return true
	}

	lower := strings.ToLower(body)

	// 純粹的致謝/確認詞（完全匹配，避免誤判實質內容）
	acknowledgements := []string{
		"收到", "了解", "明白", "好的", "知道了", "謝謝", "感謝", "好", "行",
		"不客氣", "不用謝", // 2026-10-22c：常見回覆
		"同意", "我同意", // 2026-10-22c：純同意屬於 filler
		"ok", "okay", "got it", "received", "understood", "thanks", "thank you",
		"will do", "on it", "sure", "noted", "ack", "roger", "copy",
		"agree", "agreed", // 2026-10-22c：英文同意
		// B4: 更多中文確認詞
		"好的謝謝", "謝謝了", "辛苦了", "沒問題", "可以", "沒事", "知道", "懂了",
		"+1", "同上", "我也是", "贊同", "支持", "沒意見",
	}

	trimmed := strings.TrimRight(lower, "。！？!?.,;；，、")
	for _, ack := range acknowledgements {
		if trimmed == ack {
			return true
		}
	}

	// B4: 短組合確認訊息（≤12 字，包含多個確認詞）
	// 例：「好的謝謝」「收到辛苦了」「了解，沒問題」
	// 注意：避免重疊計數（如「好的」和「好」），按長度降序檢查並標記已匹配範圍
	if runes(body) <= 12 {
		ackParts := []string{
			// 長詞在前，避免重疊計數
			"沒問題", "got it", "了解", "明白", "知道", "收到", "好的", "謝謝", "感謝", "辛苦",
			"可以", "同意", "贊同", "沒事", "懂了",
			"+1", "同上",
			"ok", "thanks", "sure",
		}
		matchCount := 0
		matched := make([]bool, len([]rune(lower))) // 記錄已匹配的字符位置
		for _, part := range ackParts {
			if idx := strings.Index(lower, part); idx >= 0 {
				// 檢查是否與已匹配範圍重疊
				partRunes := []rune(part)
				overlaps := false
				for i := idx; i < idx+len(partRunes) && i < len(matched); i++ {
					if matched[i] {
						overlaps = true
						break
					}
				}
				if !overlaps {
					matchCount++
					// 標記已匹配範圍
					for i := idx; i < idx+len(partRunes) && i < len(matched); i++ {
						matched[i] = true
					}
				}
			}
		}
		// 短訊息包含 2 個或更多不重疊的確認詞 -> 是 acknowledgement
		if matchCount >= 2 {
			return true
		}
	}

	// 只是重述對方的話（"收到你的訊息"、"收到你說的"等），但要排除有後續實質內容的
	// 例："收到你的訊息" → true，"收到你的請求，需要一些時間處理" → false
	if strings.Contains(lower, "收到你") || strings.Contains(lower, "收到了") ||
		strings.Contains(lower, "got your") || strings.Contains(lower, "received your") {
		// 如果很短（<20字）且沒有逗號分隔的後續內容，才算純致謝
		if len([]rune(body)) < 20 {
			return true
		}
		// 如果有逗號，檢查逗號後面是否有實質內容（使用 rune 切片以正確處理 UTF-8）
		runes := []rune(body)
		for i, r := range runes {
			if r == '，' || r == ',' {
				after := strings.TrimSpace(string(runes[i+1:]))
				// 逗號後有實質內容（超過5字），不算純致謝
				if len([]rune(after)) > 5 {
					return false
				}
			}
		}
		return true
	}

	// 單純的同意/認同，沒有補充
	agreements := []string{
		"同意", "沒問題", "可以", "沒錯", "對", "是的",
		"agree", "agreed", "sounds good", "fine",
	}
	for _, agr := range agreements {
		if trimmed == agr {
			return true
		}
	}

	return false
}

// MessageClass 是訊息的分類（判斷是否包含新資訊）。
type MessageClass string

const (
	ClassNewInfo  MessageClass = "new_info" // 新資訊：數據、結果、具體內容
	ClassQuestion MessageClass = "question" // 問題：詢問對方
	ClassHandoff  MessageClass = "handoff"  // 交接：派工、請求協助
	ClassResult   MessageClass = "result"   // 結果：工作完成的回報
	ClassFiller   MessageClass = "filler"   // 填充：沒有實質內容的回覆
)

// ClassifyMessage 分類訊息內容（新資訊判斷的基礎）。
// 未來可接入 judge 模型；目前用結構化規則實作。
// 2026-10-22g：改進精確度：要求具體內容作為證據，減少誤判。
func ClassifyMessage(text string) MessageClass {
	body := strings.TrimSpace(text)
	if body == "" {
		return ClassFiller
	}

	lowerBody := strings.ToLower(body)

	// 1. 優先檢查問題
	if strings.Contains(body, "？") || strings.Contains(body, "?") ||
		strings.Contains(body, "請問") || strings.Contains(body, "能否") ||
		strings.Contains(body, "可以嗎") || strings.Contains(body, "怎麼") {
		return ClassQuestion
	}

	// 2. 優先檢查列表 → new_info (避免被其他規則誤判)
	hasList := strings.Contains(body, "\n- ") || strings.Contains(body, "\n1.")
	if hasList {
		return ClassNewInfo
	}

	// 3. 明確的派工/交接詞
	handoffWords := []string{"派工", "轉給", "assigned", "delegated"}
	for _, w := range handoffWords {
		if strings.Contains(lowerBody, w) {
			return ClassHandoff
		}
	}

	// 4. 檢查明確的完成/結果指標
	// 排除未來式（預計...完成）和名詞用法（完成的項目）
	completionIndicators := []string{
		"做好", "修好", "處理好", "準備好", "整理好", "搞定",
		"已經", "已", "成功", "完畢", "寄出", "上線", "上架", "下架", "發布", "部署",
		"改成",
		"done", "finished", "completed", "fixed", "resolved", "deployed",
	}
	hasCompletionWord := false
	for _, w := range completionIndicators {
		if strings.Contains(lowerBody, w) {
			hasCompletionWord = true
			break
		}
	}
	// "完成" needs special handling
	if strings.Contains(body, "完成") && !hasCompletionWord {
		// "已完成" / "已經完成" / "成功完成" / "完成，" are completion
		// "預計...完成" / "完成的" are NOT completion (future or noun)
		if strings.Contains(body, "預計") || strings.Contains(body, "完成的") {
			// Future or noun - not completion
		} else if strings.Contains(body, "已完成") || strings.Contains(body, "已經完成") ||
			strings.Contains(body, "成功完成") || strings.Contains(body, "完成，") || strings.Contains(body, "完成。") {
			hasCompletionWord = true
		}
	}
	if hasCompletionWord {
		return ClassResult
	}

	// 5. 短請求動詞（<50字）→ handoff
	// 但排除 "修好"/"改好" 的情況（已在上面作為 completion 處理）
	bodyRunes := []rune(body)
	if len(bodyRunes) < 50 && !strings.Contains(body, "修好") && !strings.Contains(body, "改好") {
		actionVerbs := []string{
			"請", "寫", "做", "查", "找", "確認", "檢查", "測試",
			"整理", "分析", "研究", "比較", "評估", "幫", "麻煩",
		}
		for _, verb := range actionVerbs {
			if strings.HasPrefix(body, verb) || strings.Contains(body, " "+verb) {
				return ClassHandoff
			}
		}
	}

	// 6. 移除@mentions和deference phrases以檢測具體內容
	stripped := body
	// Remove @mentions
	for {
		atIdx := strings.Index(stripped, "@")
		if atIdx < 0 {
			break
		}
		endIdx := atIdx + 1
		for endIdx < len(stripped) {
			if stripped[endIdx] == ' ' || stripped[endIdx] == ',' || stripped[endIdx] == '\n' {
				break
			}
			if endIdx+1 < len(stripped) {
				r, size := utf8.DecodeRuneInString(stripped[endIdx:])
				if r == '，' {
					break
				}
				endIdx += size
			} else {
				endIdx++
			}
		}
		// Skip trailing comma/space
		for endIdx < len(stripped) {
			if stripped[endIdx] == ' ' || stripped[endIdx] == ',' {
				endIdx++
				continue
			}
			if endIdx+1 < len(stripped) {
				r, size := utf8.DecodeRuneInString(stripped[endIdx:])
				if r == '，' {
					endIdx += size
					continue
				}
			}
			break
		}
		stripped = stripped[:atIdx] + stripped[endIdx:]
	}

	// Remove pure deference/agreement phrases (2026-10-22g: longest first)
	deferencePatterns := []string{
		// Longer patterns first to avoid partial matches
		"我完全同意你的看法", "完全同意你的看法", "我完全同意你的建議", "我完全同意你的",
		"照你說的", "你說的那個", "你說的", "我都可以", "都可以", "交給你",
		"輪到你", "輪到", "同意你", "贊成你", "很好", "不錯", "沒意見",
		"謝謝你的回覆", "謝謝你的看法", "謝謝你的建議", "謝謝你的", "感謝你的",
		"你的回覆", "你的看法", "你的建議", "你的",
		"謝謝你", "感謝你", "謝謝", "感謝", "收到", "了解", "明白", "好的", "知道了",
		"不客氣", "不用謝", "辛苦了",
		"我完全同意", "我同意", "完全同意",
		"ok", "okay",
	}
	for _, p := range deferencePatterns {
		stripped = strings.ReplaceAll(stripped, p, " ")
	}
	stripped = strings.TrimSpace(stripped)

	// 7. 檢查具體內容證據
	hasConcreteResidue := false

	// 7a. 數字或日期/星期
	if strings.ContainsAny(stripped, "0123456789") {
		hasConcreteResidue = true
	}
	weekdays := []string{"週一", "週二", "週三", "週四", "週五", "週六", "週日", "星期"}
	for _, w := range weekdays {
		if strings.Contains(stripped, w) {
			hasConcreteResidue = true
			break
		}
	}

	// 7b. 引號內容（「」""）
	if strings.Contains(stripped, "「") || strings.Contains(stripped, "\"") {
		hasConcreteResidue = true
	}

	// 7c. 專有名詞或拉丁詞
	words := strings.Fields(stripped)
	for _, w := range words {
		if len(w) > 0 {
			firstChar := w[0]
			// Capitalized English word or all-caps acronym
			// Exclude common acknowledgements like "OK"
			wLower := strings.ToLower(w)
			if wLower == "ok" || wLower == "okay" {
				continue
			}
			if firstChar >= 'A' && firstChar <= 'Z' {
				hasConcreteResidue = true
				break
			}
			// Common Latin/English words
			if wLower == "bug" || wLower == "issue" || wLower == "prod" || wLower == "pr" || wLower == "test" {
				hasConcreteResidue = true
				break
			}
		}
	}

	// 7d. 新句子≥6個中文字（但排除純致謝和通用長句）
	cjkCount := 0
	for _, r := range stripped {
		if r >= '\u4e00' && r <= '\u9fff' {
			cjkCount++
		}
	}
	// 2026-10-22g: After stripping, also check that it's not just punctuation fragments
	if cjkCount >= 6 {
		// Check if stripped text is mostly punctuation/spaces
		nonPunctCount := 0
		for _, r := range stripped {
			if r >= '\u4e00' && r <= '\u9fff' {
				nonPunctCount++
			} else if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				nonPunctCount++
			}
		}
		// If >50% is actual content (not punct/space), count as concrete
		if nonPunctCount >= len([]rune(stripped))/2 {
			hasConcreteResidue = true
		}
	}

	// 7e. 路徑、URL、代碼塊
	if strings.Contains(stripped, "/workspace") || strings.Contains(stripped, "http") ||
		strings.Contains(stripped, "```") {
		hasConcreteResidue = true
	}

	// 8. 決策/選擇/提議詞
	decisionWords := []string{"決定", "建議", "提議", "推薦"}
	hasDecisionWord := false
	for _, w := range decisionWords {
		if strings.Contains(body, w) {
			hasDecisionWord = true
			break
		}
	}

	// 9. 分類邏輯
	// 9a. 提議/建議 + 具體內容 → handoff
	if hasDecisionWord && hasConcreteResidue {
		if strings.Contains(body, "提議") || strings.Contains(body, "推薦") {
			return ClassHandoff
		}
		if strings.Contains(body, "建議") {
			return ClassHandoff
		}
	}

	// 9b. 純決定詞但無具體內容 → filler
	if hasDecisionWord && !hasConcreteResidue {
		return ClassFiller
	}

	// 9c. 冒號+具體內容 → new_info
	if (strings.Contains(body, "：") || strings.Contains(body, ":")) && hasConcreteResidue {
		return ClassNewInfo
	}

	// 9d. 有具體證據 → new_info
	if hasConcreteResidue {
		return ClassNewInfo
	}

	// 默認：filler
	return ClassFiller
}

// StripMentionsAndPleasantries 移除 @-mention 和客套話，返回剩餘內容
// 用於判斷一個 @-addressed 訊息是否真的有實質內容
func StripMentionsAndPleasantries(text string) string {
	// 移除 @mention (包括 @Name 和 @Name，等變體)
	body := text
	body = strings.TrimSpace(strings.Join(strings.Fields(body), " "))

	// 簡化版：移除常見的 @ 模式
	bodyRunes := []rune(body)
	for {
		found := false
		for i := 0; i < len(bodyRunes); i++ {
			if bodyRunes[i] == '@' {
				found = true
				// 找到 @ 後面的名字結束位置
				endIdx := i + 1
				for endIdx < len(bodyRunes) {
					r := bodyRunes[endIdx]
					if r == ' ' || r == ',' || r == '，' || r == '。' || r == '！' || r == '？' || r == '\n' {
						break
					}
					endIdx++
				}
				// 跳過後面的逗號和空格
				for endIdx < len(bodyRunes) {
					r := bodyRunes[endIdx]
					if r != ' ' && r != ',' && r != '，' {
						break
					}
					endIdx++
				}
				bodyRunes = append(bodyRunes[:i], bodyRunes[endIdx:]...)
				break
			}
		}
		if !found {
			break
		}
	}

	body = strings.TrimSpace(string(bodyRunes))

	// 移除常見客套話
	pleasantries := []string{
		"謝謝", "感謝", "辛苦了", "好的", "了解", "收到", "沒問題", "同意", "贊成",
		"我完全同意", "我同意你的看法", "你說得對", "你的回覆", "你的建議",
		"thank you", "thanks", "got it", "understood", "agree", "i agree",
	}

	lowerBody := strings.ToLower(body)
	for _, p := range pleasantries {
		lowerP := strings.ToLower(p)
		lowerBody = strings.ReplaceAll(lowerBody, lowerP, " ")
	}

	// 移除標點和多餘空格
	lowerBody = strings.Map(func(r rune) rune {
		if r == '。' || r == '，' || r == '、' || r == '！' || r == '？' ||
			r == ',' || r == '.' || r == '!' || r == '?' {
			return ' '
		}
		return r
	}, lowerBody)

	return strings.TrimSpace(strings.Join(strings.Fields(lowerBody), " "))
}

// HasSubstanceAfterMention 檢查 @-mention 訊息去除客套話後是否有實質內容
// 實質內容 = 問題、請求、新資訊、或 Conclusion
func HasSubstanceAfterMention(text string) bool {
	stripped := StripMentionsAndPleasantries(text)

	// 如果去除客套話後太短（<5 個字），視為無實質內容
	if len([]rune(stripped)) < 5 {
		return false
	}

	// 檢查是否包含請求動詞
	requestVerbs := []string{
		"請", "幫", "能否", "可以", "麻煩",
		"查", "找", "寫", "做", "修", "改", "建", "整理", "分析",
		"提議", "建議", "推薦", // 2026-10-22f：提議也算請求/交接
		"please", "could you", "can you", "would you", "propose",
	}
	lowerStripped := strings.ToLower(stripped)
	for _, verb := range requestVerbs {
		if strings.Contains(lowerStripped, strings.ToLower(verb)) {
			return true
		}
	}

	// 檢查是否包含問號
	if strings.Contains(text, "？") || strings.Contains(text, "?") {
		return true
	}

	// 檢查是否包含 Conclusion
	if strings.Contains(text, "Conclusion:") || strings.Contains(text, "Conclusion：") {
		return true
	}

	// 2026-10-22d：檢查原始訊息（去除@後）的分類
	// 先移除 @ mentions，保留其他內容
	// 2026-10-22e：修正 byte/rune 混用造成的 panic（用 rune slice 找 @）
	bodyRunes := []rune(text)
	for {
		atIdx := -1
		for i, r := range bodyRunes {
			if r == '@' {
				atIdx = i
				break
			}
		}
		if atIdx < 0 {
			break
		}
		// 找到 @mention 的結束位置
		endIdx := atIdx + 1
		for endIdx < len(bodyRunes) {
			r := bodyRunes[endIdx]
			if r == ' ' || r == ',' || r == '，' || r == '。' || r == '！' || r == '？' || r == '\n' {
				break
			}
			endIdx++
		}
		// 跳過後面的逗號和空格
		for endIdx < len(bodyRunes) {
			r := bodyRunes[endIdx]
			if r != ' ' && r != ',' && r != '，' {
				break
			}
			endIdx++
		}
		bodyRunes = append(bodyRunes[:atIdx], bodyRunes[endIdx:]...)
	}
	bodyWithoutAt := strings.TrimSpace(string(bodyRunes))

	// 如果去除@後的內容包含實質資訊，返回true
	// 使用 ClassifyMessage 但允許 ClassResult 和 ClassQuestion 通過
	class := ClassifyMessage(bodyWithoutAt)
	return class != ClassFiller
}

// HasNewInformation 檢查訊息是否包含新資訊、結果、問題或交接。
// Bot-to-bot 回覆必須符合其中之一，否則應該保持 [silent]（結構化規則）。
// 2026-10-22：加強規則，避免客套話誤判為新資訊。
func HasNewInformation(text string) bool {
	// 2026-10-22c：Conclusion: 訊息永遠不阻擋（移到最前面，避免被 ClassFiller 擋掉）
	if strings.Contains(text, "Conclusion:") || strings.Contains(text, "Conclusion：") {
		return true
	}

	class := ClassifyMessage(text)
	if class == ClassFiller {
		return false
	}

	// 2026-10-22b：任務交接／請求永遠算有新資訊
	if class == ClassHandoff {
		return true
	}

	// 2026-10-22：即使分類為 new_info，如果是純客套話（感謝、問候、鼓勵）也視為 filler
	// 2026-10-22b：只在移除客套話後幾乎沒內容時才阻擋
	trimmed := strings.TrimSpace(text)

	// 常見客套話模式
	pleasantries := []string{
		"不客氣", "不用謝", "沒問題", "沒關係", "不好意思",
		"很高興", "樂意", "隨時", "歡迎", "加油",
		"辛苦了", "謝謝", "感謝", "太好了", "真棒",
		"you're welcome", "no problem", "my pleasure", "glad to help",
	}

	// 嘗試移除所有客套話，看剩下什麼
	remaining := trimmed
	for _, p := range pleasantries {
		// 移除客套話及其常見搭配（標點、空格）
		remaining = strings.ReplaceAll(remaining, p+"。", "")
		remaining = strings.ReplaceAll(remaining, p+"，", "")
		remaining = strings.ReplaceAll(remaining, p+"！", "")
		remaining = strings.ReplaceAll(remaining, p, "")
	}
	remaining = strings.TrimSpace(remaining)

	// 檢查剩餘內容長度和實質性
	remainingRunes := len([]rune(remaining))
	if remainingRunes < 3 {
		// 移除客套話後幾乎沒有內容（< 3 字）
		return false
	}

	// 如果有數據、路徑、代碼等實質內容，或剩餘內容充足，通過
	hasSubstance := strings.ContainsAny(remaining, "0123456789") ||
		strings.Contains(remaining, "/workspace") ||
		strings.Contains(remaining, "http") ||
		strings.Contains(remaining, "```") ||
		remainingRunes >= 4 // 剩餘內容 >= 4 字（如 "done", "OK", "成功"）就通過

	if !hasSubstance {
		return false
	}

	return class != ClassFiller
}

// IsSimilarTo 檢查兩則訊息是否相似（近似重複檢測，防止循環）。
// 使用簡單的詞袋模型 + 編輯距離，避免重複發送相同內容。
func IsSimilarTo(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))

	if a == b {
		return true
	}

	// 移除標點後比較
	stripPunct := func(s string) string {
		// 移除所有常見標點符號
		punct1 := "。！？，、；：（）【】《》〈〉…—·" // 中文標點
		punct2 := ".,;:!?()[]<>-"      // 英文標點
		punct3 := `'"`                 // 引號和反引號
		for _, p := range punct1 + punct2 + punct3 {
			s = strings.ReplaceAll(s, string(p), "")
		}
		return s
	}

	aNoPunct := stripPunct(a)
	bNoPunct := stripPunct(b)

	if aNoPunct == bNoPunct {
		return true
	}

	// 檢查一個是否包含另一個（長度相近時）
	aLen := len([]rune(a))
	bLen := len([]rune(b))
	if aLen > 0 && bLen > 0 {
		ratio := float64(aLen) / float64(bLen)
		if ratio > 0.7 && ratio < 1.3 {
			if strings.Contains(aNoPunct, bNoPunct) || strings.Contains(bNoPunct, aNoPunct) {
				return true
			}
		}
	}

	return false
}

// StageADecision 是 Stage A 的確定性規則結果（2026-10-22h）。
type StageADecision int

const (
	StageAUnsure StageADecision = iota // 不確定，交給 Stage B
	StageABlock                        // 確定阻擋（空白、純致謝）
	StageAPass                         // 確定通過（Conclusion、問句、數字日期、URL、程式碼）
)

var (
	conclusionRE = regexp.MustCompile(`(?i)^Conclusion[：:]`)
	urlRE        = regexp.MustCompile(`https?://`)
	codeFenceRE  = regexp.MustCompile("```")
	numberDateRE = regexp.MustCompile(`\d{1,4}[/-]\d{1,2}[/-]\d{1,4}|\d+[.,]\d+|[¥$€£]\s*\d+`)
)

// StageAFiller 是 Stage A：確定性規則（2026-10-22h）。
// 空白或純致謝 → 阻擋；Conclusion/問號/數字日期/URL/程式碼 → 通過；其他 → 不確定。
func StageAFiller(content, verdict string) StageADecision {
	// [silent] 或純致謝：確定阻擋
	if verdict != "failed" && (IsSilent(content) || IsAcknowledgement(content)) {
		return StageABlock
	}

	stripped := strings.TrimSpace(content)
	if stripped == "" {
		return StageABlock
	}

	// Conclusion: → 確定通過
	if conclusionRE.MatchString(stripped) {
		return StageAPass
	}

	// 問號 → 確定通過
	if strings.ContainsAny(stripped, "?？") {
		return StageAPass
	}

	// 數字、日期 → 確定通過
	if numberDateRE.MatchString(stripped) {
		return StageAPass
	}

	// URL → 確定通過
	if urlRE.MatchString(stripped) {
		return StageAPass
	}

	// 程式碼圍欄 → 確定通過
	if codeFenceRE.MatchString(stripped) {
		return StageAPass
	}

	return StageAUnsure
}

// ShouldSendToPeer 檢查 Bot 的訊息是否應該送給隊友（統一的發送閘門，2026-10-22h 兩階段）。
// 用於群組回覆、Bot 主動傳訊、DM 回覆。回傳 (應該送出, 阻擋原因)。
// Stage A（確定性規則）→ Stage B（peer_reply 快判 ENFORCE）→ 退路（lenient pass）。
func ShouldSendToPeer(content, verdict string, isAnswer bool, recentFrom []string, judgeVerdict string, isOutgoing bool) (bool, string) {
	// Stage A：確定性規則
	switch StageAFiller(content, verdict) {
	case StageABlock:
		return false, "stageA:block"
	case StageAPass:
		// Stage A 確定通過，仍檢查重複
		goto checkDuplicate
	case StageAUnsure:
		// 繼續 Stage B
	}

	// Stage B：peer_reply 快判（judgeVerdict 由 peerReplyJudge 提供，ENFORCE 模式）
	// judgeVerdict 空字串表示快判失敗或逾時 → 退路（lenient pass）
	if judgeVerdict == "" {
		// 快判失敗或逾時 → 退路：lenient pass（只阻擋答案情境外的短文字）
		if !isAnswer && verdict == "answer" && !HasNewInformation(content) {
			return false, "stageB:fallback:no_new_info"
		}
		goto checkDuplicate
	}

	// 快判成功：handle → 通過；fyi/skip → 阻擋
	switch judgeVerdict {
	case "handle":
		goto checkDuplicate
	case "fyi":
		return false, "stageB:fyi"
	case "skip":
		return false, "stageB:skip"
	}

checkDuplicate:
	// 近似重複檢測（檢查最近發送的訊息）
	for _, recent := range recentFrom {
		if IsSimilarTo(content, recent) {
			return false, "duplicate"
		}
	}

	return true, ""
}

// Member 是 @ 比對用的成員（id、名字）。
type Member struct {
	ID   string
	Name string
}

var (
	mentionRE = regexp.MustCompile(`@([^\s@，。、,.!?！？:：;；()（）\[\]「」]+)`)
	wordChar  = regexp.MustCompile(`^[a-z0-9_]$`)
)

// Mentions 找出 text 裡用 @名字 點到的成員（依出現順序），以及有沒有 @所有人（LazyBoy mentions）。
// 名字長的先比，「@小編A」不會被當成「@小編」；英文名字要整個字相同（@bobby 不是 @bob）。
func Mentions(text string, members []Member) ([]Member, bool) {
	sorted := append([]Member(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool { return runes(sorted[i].Name) > runes(sorted[j].Name) })
	var found []Member
	everyone := false
	seen := map[string]bool{}
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		word := strings.ToLower(m[1])
		if everyoneWords[word] {
			everyone = true
			continue
		}
		w := []rune(word)
		for _, member := range sorted {
			name := []rune(strings.ToLower(member.Name))
			if len(name) == 0 || seen[member.ID] || !strings.HasPrefix(word, string(name)) {
				continue
			}
			if len(w) == len(name) || !wordChar.MatchString(string(w[len(name)])) || !wordChar.MatchString(string(name[len(name)-1])) {
				found = append(found, member)
				seen[member.ID] = true
				break
			}
		}
	}
	return found, everyone
}

// fileLink 是對話裡附件的連結（LazyBoy FILE_LINK）：群組附件放在每位成員電腦的同一個位置。
var fileLink = regexp.MustCompile(`\[([^\]]+)\]\(/api/agents/[0-9a-f-]+/files/content\?path=([^)\s]+)\)`)

// AttachmentsIn 把群組訊息裡的附件連結改寫成「在你的電腦：/workspace/…」（LazyBoy attachments_in）。
func AttachmentsIn(text string) string {
	return fileLink.ReplaceAllStringFunc(text, func(s string) string {
		m := fileLink.FindStringSubmatch(s)
		p, err := url.PathUnescape(m[2])
		if err != nil {
			p = m[2]
		}
		return "［附件：" + m[1] + "，在你的電腦：/workspace/" + p + "］"
	})
}

// fileRef 是訊息裡提到的 /workspace 檔案路徑（LazyBoy FILE_REF）：到空白、引號、括號或中文標點為止。
var fileRef = regexp.MustCompile("/workspace/[^\\s`'\"<>()（）\\[\\]「」『』，。、；：！？]+")

var badName = regexp.MustCompile(`[\x00-\x1f/\\]`)

// Filename 把名稱整理成可以當檔名的一段（LazyBoy files.filename）：控制字元與斜線換成 _、最多 FileNameMax 字、去掉頭尾的點與空白。
func Filename(name string) string {
	name = strings.Trim(headRunes(badName.ReplaceAllString(name, "_"), limits.FileNameMax), ". ")
	if name == "" {
		return "attachment"
	}
	return name
}

// Plain 把空白壓成一個空格，超過 limit 個字時截斷並加「…」（LazyBoy plain；給 org 用）。
func Plain(text string, limit int) string { return plain(text, limit) }
