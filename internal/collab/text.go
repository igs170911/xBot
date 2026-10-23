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
