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
		"ok", "okay", "got it", "received", "understood", "thanks", "thank you",
		"will do", "on it", "sure", "noted", "ack", "roger", "copy",
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
		"同意", "沒問題", "可以", "沒錯", "對", "是的", "agree", "agreed", "sounds good", "fine",
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

	// 1. 優先檢查問題（避免被其他規則覆蓋）
	if strings.Contains(body, "？") || strings.Contains(body, "?") ||
		strings.Contains(body, "請問") || strings.Contains(body, "能否") ||
		strings.Contains(body, "可以嗎") || strings.Contains(body, "怎麼") {
		return ClassQuestion
	}

	// 2. 檢查結果回報（優先於數字檢查，因為"完成 5 個"應該是 result 不是 new_info）
	completionIndicators := []string{
		"完成", "做好", "修好", "搞定", "處理好", "準備好",
		"已經完成", "已經做好", "已經修好", "已經處理好",
		"成功", "完畢",
		"done", "finished", "completed", "fixed", "resolved",
	}
	for _, ind := range completionIndicators {
		if strings.Contains(lowerBody, ind) {
			return ClassResult
		}
	}

	// 3. 檢查數據/具體內容（數字、路徑、URL、代碼等）
	hasNumbers := strings.ContainsAny(body, "0123456789")
	hasPath := strings.Contains(body, "/") && (strings.Contains(body, "workspace") || strings.Contains(body, "."))
	hasURL := strings.Contains(body, "http") || strings.Contains(body, "://")
	hasCodeBlock := strings.Contains(body, "```") || strings.Contains(body, "~~~")
	if hasNumbers || hasPath || hasURL || hasCodeBlock {
		return ClassNewInfo
	}

	// 4. 長內容優先判定為 new_info（在檢查 handoff 之前）
	if len([]rune(body)) > 30 {
		return ClassNewInfo
	}

	// 5. 檢查交接/派工（簡單模式，避免誤判）
	handoffPatterns := []string{
		"已派工", "轉給", "@",
		"assigned", "delegated", "forwarded",
	}
	for _, h := range handoffPatterns {
		if strings.Contains(lowerBody, h) {
			return ClassHandoff
		}
	}

	// 6. "請"、"幫" 等詞（最後檢查，避免覆蓋前面的判定）
	if strings.Contains(lowerBody, "請") || strings.Contains(lowerBody, "幫") ||
		strings.Contains(lowerBody, "please") || strings.Contains(lowerBody, "could you") {
		return ClassHandoff
	}

	return ClassFiller
}

// HasNewInformation 檢查訊息是否包含新資訊、結果、問題或交接。
// Bot-to-bot 回覆必須符合其中之一，否則應該保持 [silent]（結構化規則）。
func HasNewInformation(text string) bool {
	class := ClassifyMessage(text)
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
