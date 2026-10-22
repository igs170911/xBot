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
	}

	trimmed := strings.TrimRight(lower, "。！？!?.,;；，、")
	for _, ack := range acknowledgements {
		if trimmed == ack {
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
func ClassifyMessage(text string) MessageClass {
	body := strings.TrimSpace(text)
	if body == "" {
		return ClassFiller
	}

	lowerBody := strings.ToLower(body)

	// 0. 先檢查是否只包含致謝/同意詞彙（2026-10-22c：防止純同意誤判為 handoff）
	// 移除所有標點、空格、@mentions 後檢查剩餘詞彙
	runes := []rune(body)
	// 移除 @mentions
	for i := 0; i < len(runes); {
		if runes[i] == '@' {
			// Find end of mention (alphanumeric, _, -, or CJK)
			j := i + 1
			for j < len(runes) {
				r := runes[j]
				if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
					(r >= '0' && r <= '9') || r == '_' || r == '-' ||
					(r >= '\u4e00' && r <= '\u9fff') {
					j++
				} else {
					break
				}
			}
			// Remove from i to j
			runes = append(runes[:i], runes[j:]...)
		} else {
			i++
		}
	}
	cleaned := string(runes)

	// 移除標點和空格
	cleaned = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || (r >= '\u4e00' && r <= '\u9fff') {
			return r
		}
		return ' '
	}, cleaned)
	words := strings.Fields(cleaned)

	// 檢查是否所有詞彙都是致謝/同意詞
	if len(words) > 0 {
		allAckOrAgree := true
		ackWords := []string{
			"收到", "了解", "明白", "好的", "知道了", "謝謝", "感謝", "好", "行",
			"不客氣", "不用謝", "同意", "我同意", "沒問題", "可以", "沒錯", "對", "是的",
			"ok", "okay", "got", "it", "received", "understood", "thanks", "thank", "you",
			"will", "do", "on", "sure", "noted", "ack", "roger", "copy",
			"agree", "agreed", "sounds", "good", "fine",
		}
		for _, w := range words {
			wLower := strings.ToLower(w)
			found := false
			for _, ack := range ackWords {
				if wLower == ack {
					found = true
					break
				}
			}
			if !found {
				allAckOrAgree = false
				break
			}
		}
		if allAckOrAgree {
			return ClassFiller
		}
	}

	// 1. 優先檢查問題（避免被其他規則覆蓋）
	if strings.Contains(body, "？") || strings.Contains(body, "?") ||
		strings.Contains(body, "請問") || strings.Contains(body, "能否") ||
		strings.Contains(body, "可以嗎") || strings.Contains(body, "怎麼") {
		return ClassQuestion
	}

	// 2. 檢查結果回報（優先於數字檢查，因為"完成 5 個"應該是 result 不是 new_info）
	// 2026-10-22d：只包含明確的"已完成"意義的詞，避免誤判請求為結果
	completionIndicators := []string{
		"完成", "做好", "修好", "搞定", "處理好", "準備好", "整理好",
		"已經完成", "已經做好", "已經修好", "已經處理好",
		"成功", "完畢", "選", "決定", "建議", "已經核准", "已核准", "已批准",
		"上架", "下架", "發布", "部署",
		"done", "finished", "completed", "fixed", "resolved", "approved",
	}
	for _, ind := range completionIndicators {
		if strings.Contains(lowerBody, ind) {
			return ClassResult
		}
	}

	// 2.5. 檢查動作動詞（命令/請求）- 2026-10-22 加入以識別任務指派
	// 2026-10-22b：檢測整個訊息，不只是開頭（修復「幫我寫一句咖啡標語」等請求被漏掉）
	// 2026-10-22d：「換」用於交接（如「換你」、「輪到你」）
	// 這些是常見的任務動詞和請求詞，出現時通常是在指派工作
	// 但要避免誤判：只在較短訊息（<50 字）且開頭或緊跟空格時才判定為 handoff
	actionVerbs := []string{
		"寫", "做", "查", "找", "確認", "檢查", "測試", "修", "改", "建",
		"整理", "分析", "研究", "比較", "評估", "幫", "請", "麻煩", "換", "輪到",
	}
	// 檢查較短訊息（長訊息更可能是描述或說明）
	bodyRunes := len([]rune(body))
	if bodyRunes < 50 {
		for _, verb := range actionVerbs {
			// 只在詞的開頭或前面有空格時判定
			if strings.HasPrefix(body, verb) || strings.Contains(body, " "+verb) || strings.Contains(body, "　"+verb) {
				return ClassHandoff
			}
		}
	}

	// 3. 檢查數據/具體內容（數字、路徑、URL、代碼等）
	hasNumbers := strings.ContainsAny(body, "0123456789")
	hasPath := strings.Contains(body, "/") && (strings.Contains(body, "workspace") || strings.Contains(body, "."))
	hasURL := strings.Contains(body, "http") || strings.Contains(body, "://")
	hasCodeBlock := strings.Contains(body, "```") || strings.Contains(body, "~~~")

	// 2026-10-22d：冒號後面接內容（如「標語三條已寫好：…」）或列表應算新資訊
	hasColonContent := (strings.Contains(body, "：") || strings.Contains(body, ":")) &&
		(strings.Contains(body, "\n") || len([]rune(body)) > 20)
	hasList := strings.Contains(body, "\n- ") || strings.Contains(body, "\n• ") ||
		strings.Contains(body, "\n1.") || strings.Contains(body, "\n2.")

	if hasNumbers || hasPath || hasURL || hasCodeBlock || hasColonContent || hasList {
		return ClassNewInfo
	}

	// 4. 長內容優先判定為 new_info（在檢查 handoff 之前）
	// 2026-10-22：提高門檻從 30→80 字，避免客套話誤判
	if len([]rune(body)) > 80 {
		return ClassNewInfo
	}

	// 5. 檢查交接/派工（簡單模式，避免誤判）
	// 2026-10-22d: 移除 "@" - 單純的 @ 不算交接，要看內容
	handoffPatterns := []string{
		"已派工", "轉給",
		"assigned", "delegated", "forwarded",
	}
	for _, h := range handoffPatterns {
		if strings.Contains(lowerBody, h) {
			return ClassHandoff
		}
	}

	// 6. 明確的請求詞（最後檢查）
	// 2026-10-22：只保留明確的請求模式，移除泛用詞
	if strings.Contains(lowerBody, "請你") || strings.Contains(lowerBody, "請幫") ||
		strings.Contains(lowerBody, "could you please") {
		return ClassHandoff
	}

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
		"please", "could you", "can you", "would you",
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
	bodyWithoutAt := text
	for strings.Contains(bodyWithoutAt, "@") {
		atIdx := strings.Index(bodyWithoutAt, "@")
		if atIdx >= 0 {
			bodyRunes := []rune(bodyWithoutAt)
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
			bodyWithoutAt = string(bodyRunes[:atIdx]) + string(bodyRunes[endIdx:])
		}
	}
	bodyWithoutAt = strings.TrimSpace(bodyWithoutAt)

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

// ShouldSendToPeer 檢查 Bot 的訊息是否應該送給隊友（統一的發送閘門，2026-10-22）。
// 用於群組回覆、Bot 主動傳訊、DM 回覆。回傳 (應該送出, 阻擋原因)。
// 2026-10-22b：judgeVerdict 是 peer_reply 快判的結果（handle/fyi/skip），但它是為接收設計的。
// 對於發送（isOutgoing=true），只阻擋 filler，讓 results/answers/handoffs 通過。
func ShouldSendToPeer(content, verdict string, isAnswer bool, recentFrom []string, judgeVerdict string, isOutgoing bool) (bool, string) {
	// 0. 快判優先（2026-10-22：整合 peer_reply 判斷）
	// 2026-10-22b：對於發送操作，只在明確 skip 時阻擋；fyi 允許通過（因為對方可能需要知道）
	if isOutgoing {
		// 發送分類：只阻擋純 filler
		switch judgeVerdict {
		case "skip":
			// 明確無用的訊息
			return false, "judge:skip"
		case "fyi", "handle":
			// 對方可能需要知道，或需要處理，都通過
			// 但仍然要檢查 silent/ack 和重複
		}
	} else {
		// 接收分類：原邏輯
		switch judgeVerdict {
		case "skip":
			return false, "judge:skip"
		case "fyi":
			// FYI 訊息：只要知道，不用回覆
			return false, "judge:fyi"
		case "handle":
			// 明確要處理的，通過（但仍然要檢查 silent/ack 和重複）
		}
	}
	// judgeVerdict == "" 或其他值：照規則檢查

	// 1. 永遠阻擋：silent 或純致謝
	if verdict != "failed" && (IsSilent(content) || IsAcknowledgement(content)) {
		return false, "silent_or_ack"
	}

	// 2. 答案情境（回覆 request/question）允許短答案
	if isAnswer && verdict == "answer" {
		return true, ""
	}

	// 3. 非答案情境：必須有新資訊
	if verdict != "failed" && !HasNewInformation(content) {
		return false, "no_new_info"
	}

	// 4. 近似重複檢測（檢查最近發送的訊息）
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
