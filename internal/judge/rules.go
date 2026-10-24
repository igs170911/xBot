package judge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/replystyle"
)

// Verdict 是「任務完成了沒」的判斷（PRD §20.2 用途 2）。
type Verdict string

// 五種判斷。一輪的結果（task.verdict：answer／failed／cancelled／interrupted）只代表模型結束了這一輪，
// 這裡才是「目標有沒有達成、要不要人」（§19.1）。
const (
	Done         Verdict = "done"          // 回完了，沒有發現沒做完或要你處理的地方
	NeedsPerson  Verdict = "needs_person"  // 要你本人：登入、驗證碼、付款、核准、對外發送前的同意
	HandedBack   Verdict = "handed_back"   // 把還沒做完的部分丟回給你（問要不要繼續、請你自己去點）
	OnlyPromised Verdict = "only_promised" // 只回一句「我來查一下」，沒有真的做
	Stuck        Verdict = "stuck"         // 出錯、看門狗停掉、計畫沒做完、背景子任務失敗沒人接手
)

// Verdicts 是所有判斷，依畫面上的順序。
var Verdicts = []Verdict{Done, NeedsPerson, HandedBack, OnlyPromised, Stuck}

// EnumValues 讓操作的輸入輸出 schema 帶上五種判斷。
func (Verdict) EnumValues() []string {
	out := make([]string, len(Verdicts))
	for i, v := range Verdicts {
		out[i] = string(v)
	}
	return out
}

// Valid 回答 v 是不是五種判斷之一。
func (v Verdict) Valid() bool {
	for _, x := range Verdicts {
		if v == x {
			return true
		}
	}
	return false
}

// Label 是介面上的白話（「系統判斷：已完成」）。
func (v Verdict) Label() string {
	switch v {
	case Done:
		return "已完成"
	case NeedsPerson:
		return "需要你"
	case HandedBack:
		return "交回給你"
	case OnlyPromised:
		return "只說要做"
	case Stuck:
		return "卡住了"
	}
	return string(v)
}

// 一輪的結果（和 runtime.Verdict* 同值；judge 不 import runtime）。
const (
	OutcomeAnswer      = "answer"
	OutcomeFailed      = "failed"
	OutcomeCancelled   = "cancelled"
	OutcomeInterrupted = "interrupted"
)

// 判斷是誰做的。
const (
	ByRules = "rules"
	ByModel = "model"
)

// Input 是一輪結束時判斷需要的事實。
type Input struct {
	// Outcome 是這一輪的結果：answer、failed、cancelled、interrupted。
	Outcome string
	// Text 是 Bot 最後的回覆。
	Text string
	// UsedTools：這一輪有沒有呼叫任何工具（LazyBoy task.steps）。
	UsedTools bool
	// Stalled：看門狗停掉的（太久沒有動靜）。
	Stalled bool
	// PlanLeft 是 Bot 自己的計畫裡還沒做完的項目（pending、in_progress）。
	PlanLeft []string
	// Stranded 是失敗後沒有重新派送、也沒有人接手的背景子任務（目標文字）。
	Stranded []string
	// Background：這一輪是背景子任務回報結果後自己開始的那一輪（LazyBoy kind=background）。
	Background bool
	// Looped、LoopCount：引擎因為一直重複同一個工具（Looped）LoopCount 次沒有進展而停下這一輪（Hermes 的 tool guardrail）。
	Looped    string
	LoopCount int
	// TaskID、BotID 是這一輪的工作與 Bot（快判記錄用，對得到你的改正）。
	TaskID, BotID string
	// Asked 是這件事原本怎麼交代的（你親手打的那則；自動續做時是最一開始那則）。只有你本人、定時任務、Webhook 交代的才有；
	// 隊友、群組的是空的。模型要看得到你問了什麼，才不會把「嗨」的回覆當成沒做完（10/19～21 影子紀錄 38 次誤判）。
	Asked string
	// Continued：這一輪是系統叫它接著做的（自動續做、自動再試）。新的規則和模型只看第一輪，免得一直叫回去。
	Continued bool
	// Research 是這一輪成功的搜尋、讀網頁、瀏覽器呼叫次數（IsResearchTool）。
	Research int
}

// Result 是一次判斷。
type Result struct {
	Verdict Verdict `json:"verdict"`
	// Reason 是一行白話原因。
	Reason string `json:"reason"`
	// By 是誰判斷的：rules（規則）或 model（快判模型，X05）。
	By string `json:"by"`
	// Confidence 是模型給的機率（0–1）；規則判斷時是 0（介面顯示「規則」）。
	Confidence float64 `json:"confidence,omitempty"`
	// Ask 是 Bot 要人處理的那一段（交回或要你的段落；沒有時是回覆的最後幾個字）。needs_person 卡片引用它。
	Ask string `json:"ask,omitempty"`
}

// Judge 判斷一輪做完了沒。ok=false 表示這一輪不用判斷（你停掉的、服務重開中斷的）。
// X05 的模型判斷實作同一個介面，用 Fallback 包上 Rules。
type Judge interface {
	Judge(ctx context.Context, in Input) (Result, error)
}

// ErrSkip 表示這一輪不用判斷（停止、中斷）。
var ErrSkip = errors.New("這一輪不用判斷")

// Fallback 先問 primary，等不到（Wait 內）、出錯或回了不認得的判斷就用 rules（FJ-2）。
// 安全方向只能更嚴（FJ-4）：規則判成 needs_person 時，一律不降級。
type Fallback struct {
	Primary Judge
	Rules   Judge
	// Wait 是最多等 primary 多久；0 用 limits.JudgeTaskDoneWait。
	Wait time.Duration
}

// Judge 實作 Judge。
func (f Fallback) Judge(ctx context.Context, in Input) (Result, error) {
	rules := f.Rules
	if rules == nil {
		rules = Rules{}
	}
	base, err := rules.Judge(ctx, in)
	if err != nil || f.Primary == nil {
		return base, err
	}
	wait := f.Wait
	if wait <= 0 {
		wait = limits.JudgeTaskDoneWait
	}
	pctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	got, perr := f.Primary.Judge(pctx, in)
	if perr != nil || !got.Verdict.Valid() || base.Verdict == NeedsPerson || in.Looped != "" {
		return base, nil
	}
	if got.Ask == "" {
		got.Ask = base.Ask
	}
	return got, nil
}

// ── 規則（LazyBoy server/runtime.py）────────────────────

// outward 是會離開電腦的動作（發、寄、貼、刪、買、上架…）。LazyBoy Runtime.OUTWARD
const outward = `(發|寄|送|貼|回覆|留言|刪|買|下單|付|上架|上線|公開|發佈|發布|發送|送出|正式)`

// needsPersonRe：Bot 需要你本人（登入、驗證碼、錢、只有你能做的決定，或對外發送前的同意）。LazyBoy Runtime.NEEDS_PERSON
// XBot 加上「註冊」「你本人」「你親自」（2026-10-24：「有一件事需要你本人處理：momo 分潤要註冊新後台，要用你的資料」）。
var needsPersonRe = regexp.MustCompile(`(?i)登入|驗證碼|密碼|付款|刷卡|核定|核准|同意|授權|註冊|你本人|你親自|二階段|2FA|captcha|` +
	`(確認|看過|看一下|看看|檢查|過目|審)[^。\n]{0,12}(再|才|就)` + outward + `|要不要` + outward + `|可以` + outward + `[^。\n]{0,6}嗎|` + outward + `出去嗎`)

// handsBackRe：把工作丟回來，而不是做完（問要不要繼續、請你去做某一步）。LazyBoy Runtime.HANDS_BACK
var handsBackRe = regexp.MustCompile(`要我(先|再)?(繼續|接著|去)|要我(現在)?(就)?(開始|動手|執行|來做|幫你做)[^。\n？?]{0,20}(嗎|？|\?)|還是你(要)?(直接|自己|手動|幫)|需要你(幫|做|處理|接手)|你方便的話|(建議|請)你(先)?接手|` +
	`手動[^。\n]{0,40}(貼|傳|給)(給)?我|` +
	`請你(在|到|先|幫|手動|自己|直接)(後台|網頁|手機)?[^。\n]{0,12}(點|貼|複製|提供|找|挑|傳)[^。\n]{0,20}(給我|過來|一下)?|` +
	`你(手動|自己)(點|貼|複製|找|挑)|` +
	// 「請確認要開始嗎？」「可以開始嗎？」（2026-10-20：開子代理前也這樣問，問完就停）
	`(確認|要|可以|是否)[^。\n？?]{0,15}(開始|進行|執行|動手)(了)?[^。\n？?]{0,6}(嗎|？|\?)|` +
	`請(你)?確認[^。\n？?]{0,12}(開始|進行|執行|動手|繼續)`)

// progressRe：你在問進度。LazyBoy PROGRESS_WORDS
var progressRe = regexp.MustCompile(`進度|做到哪|做到那|還在做|在做嗎|有在做|做完了嗎|做完沒|完成了嗎|怎麼停了|停了嗎|卡住|現在狀況|目前狀況|在忙嗎|有在動|有回應|到哪了|還好嗎`)

// promiseRest 是 PROMISE 在動詞之後的部分。
var promiseRest = regexp.MustCompile(`^(?:一下|看看|看|查)?[^。\n？?：:]{0,30}[。.！!]?$`)

// PROMISE 的前半段（LazyBoy 是一條含 (?!…) 的正規表示式；Go 的 regexp 沒有 lookahead，逐一試每種前綴，結果相同）：
//
//	^(?:好的?|收到|了解)?[，,、 ]*(?:我(?:先|來|去|會)?|讓我|先|馬上)?(?:查|看|檢查|確認|整理|找|處理|開始|追)(?![了過到完好])
var (
	promiseGreet   = []string{"", "好", "好的", "收到", "了解"}
	promiseSubject = []string{"", "我", "我先", "我來", "我去", "我會", "讓我", "先", "馬上"}
	promiseVerb    = []string{"查", "看", "檢查", "確認", "整理", "找", "處理", "開始", "追"}
	promiseNotNext = "了過到完好"
	promiseSep     = "，,、 "
)

// IsPromise 回答一段回覆是不是只說「我來查一下」這類要去做的話（LazyBoy PROMISE.match）。
func IsPromise(text string) bool {
	for _, g := range promiseGreet {
		if !strings.HasPrefix(text, g) {
			continue
		}
		rest := strings.TrimLeft(text[len(g):], promiseSep)
		for _, s := range promiseSubject {
			if !strings.HasPrefix(rest, s) {
				continue
			}
			after := rest[len(s):]
			for _, v := range promiseVerb {
				if !strings.HasPrefix(after, v) {
					continue
				}
				tail := after[len(v):]
				if r, _ := utf8.DecodeRuneInString(tail); tail != "" && strings.ContainsRune(promiseNotNext, r) {
					continue
				}
				if promiseRest.MatchString(tail) {
					return true
				}
			}
		}
	}
	return false
}

// NeedsPersonIn 回答這段話是不是在要你本人做事（LazyBoy NEEDS_PERSON.search）。引號裡的字是在講那個詞，不算（見 mentions）。
func NeedsPersonIn(text string) bool {
	return needsPersonRe.MatchString(mentions(text))
}

// mentions 拿掉不是在要你做事的寫法：已經完成的（alreadyDoneRe）、引號裡的（「需要你同意」的關卡）。
func mentions(text string) string {
	return quotedRe.ReplaceAllString(alreadyDoneRe.ReplaceAllString(text, ""), "")
}

// quotedRe 是引號裡的一小段（講到那個詞，不是在用它）。
var quotedRe = regexp.MustCompile(`「[^」\n]{0,40}」|“[^”\n]{0,40}”|"[^"\n]{0,40}"`)

// sentenceRe 切句子（句號、問號、驚嘆號、分號、換行）。
var sentenceRe = regexp.MustCompile(`[^。！？!?；;\n]+[。！？!?；;]?`)

// policyRe：講自己遇到哪類事會怎麼做（「定價、簽約這類要你拍板的，我會先送核准再做」），不是現在在等你；有「請你」就還是在要你做。
var (
	policyRe     = regexp.MustCompile(`這類|這種|之類|一類|遇到|的時候`)
	policyDoesRe = regexp.MustCompile(`我(都|就)?會`)
	pleaseRe     = regexp.MustCompile(`請你|麻煩你|需要你本人`)
)

// needsPersonAsk 回答這段話是不是在要你本人做事：登入、核准、付款這類字要出現在「在要你做事或回答的那一句」裡
// （2026-10-19～23 的「需要你處理」卡 8 張有 3 張是誤判：介紹功能時說「需要登入的網站我也會自己開」、
// 描述做過的事時說跳出「需要你同意」的關卡）。回傳那個字。
func needsPersonAsk(text string) (string, bool) {
	for _, s := range sentenceRe.FindAllString(mentions(text), -1) {
		if !asksRe.MatchString(s) {
			continue
		}
		if policyRe.MatchString(s) && policyDoesRe.MatchString(s) && !pleaseRe.MatchString(s) {
			continue
		}
		if w := needsPersonRe.FindString(s); w != "" {
			return w, true
		}
	}
	return "", false
}

// alreadyDoneRe：已經完成的登入、核准（「已登入，帳號確認是…」）不是在等你。XBot 新增（2026-10-20：水哥哥說「已登入」就被當成要你登入，停下來不做）。
var alreadyDoneRe = regexp.MustCompile(`(已經?|有|都)(登入|核准|核定|同意|授權|付款)|(登入|核准|核定|授權|付款)(成功|好了|完成|過了|了)`)

// HandsBackIn 回答這段話有沒有把工作丟回來（LazyBoy HANDS_BACK.search）。
func HandsBackIn(text string) bool { return handsBackRe.MatchString(text) }

// IsProgressQuestion 回答你這則訊息是不是在問進度：60 字以內、有進度相關的字（LazyBoy _prompt）。
func IsProgressQuestion(text string) bool {
	return utf8.RuneCountInString(text) <= limits.ProgressQuestionMax && progressRe.MatchString(text)
}

// Promised 回答這一輪是不是只說要做卻沒做：不是背景回報、70 字以內、像承諾、整輪沒用工具（LazyBoy auto_continue）。
func Promised(in Input) bool {
	text := strings.TrimSpace(in.Text)
	if in.Background {
		return false
	}
	return (utf8.RuneCountInString(text) <= limits.PromiseMaxChars && IsPromise(text) && !in.UsedTools) || EndsWithPromise(text)
}

// 最後一句「現在去做某某」：XBot 新增（2026-10-20，使用者：「要我持續確認，他才會開始動作」）。
// 用過工具之後，最後一句還是「現在去個人頁抓基本盤」「讓我開瀏覽器看帳號」就結束這一輪（較弱的模型常這樣），也算只說要做。
// 要有「現在、接下來、讓我、我先…」這類開頭，緊接著一個動作；是問句、或動作後面接了「了、完、好」（做完了）都不算。
var (
	nextLeadRe = regexp.MustCompile(`^(?:好[的]?[，,、 ]*)?(?:現在|接下來|接著|馬上|下一步|然後|讓我|我先|我來|我去|我現在|我馬上|我就|先)[，,、：: ]*(?:我)?(?:就|先|再|來|會|要|馬上|現在)*`)
	nextVerbs  = []string{"去", "開始", "繼續", "打開", "開", "抓", "查", "搜尋", "看", "讀", "檢查", "確認", "找", "整理", "處理", "寫", "跑", "進", "點", "下載"}
)

// offerRe：同一句前面是在等使用者（「直接講要做什麼，我就開始」「需要的話我去查」）：是提議，不是說了要做卻沒做。
// 2026-10-20 基準測試：回「嗨」時最後一句是這樣，被當成只說要做、自動繼續，Bot 就自己編了一份銷售分析出來。
var offerRe = regexp.MustCompile(`(講|說|告訴我|跟我說|給我|丟給我|貼給我|傳給我|交代|決定|確認|同意|選|指定|想要|需要|要的話|的話|只要|如果|若|等你|你)[^。！!？?\n]{0,20}$`)

// EndsWithPromise 回答回覆的最後一句是不是「接下來去做某某」（還沒做）。
func EndsWithPromise(text string) bool {
	last := lastSentence(text)
	if last == "" || utf8.RuneCountInString(last) > limits.PromiseMaxChars || strings.ContainsAny(last, "？?") || strings.HasSuffix(last, "嗎") {
		return false
	}
	if offerRe.MatchString(clauseBefore(text)) {
		return false
	}
	loc := nextLeadRe.FindStringIndex(last)
	if loc == nil {
		return false
	}
	rest := last[loc[1]:]
	for _, v := range nextVerbs {
		if !strings.HasPrefix(rest, v) {
			continue
		}
		tail := rest[len(v):]
		if r, _ := utf8.DecodeRuneInString(tail); tail != "" && strings.ContainsRune(promiseNotNext, r) {
			return false
		}
		return true
	}
	return false
}

// fenceRe 是 ``` 包起來的程式碼區塊（含 mermaid 流程圖）。
var fenceRe = regexp.MustCompile("(?s)(^|\n)[ \t]*(```|~~~).*?(\n[ \t]*(```|~~~)[^\n]*|$)")

// Prose 去掉回覆裡的程式碼區塊，只留下說給使用者聽的話：流程圖的方塊「{需要動手做嗎}」、程式裡的字串不是 Bot 在問你
// （2026-10-20 基準測試：畫流程圖那題被當成交回、自動繼續）。
func Prose(text string) string { return fenceRe.ReplaceAllString(text, "\n") }

// clauseBefore 是最後一個子句前面、同一句裡的那一段（「直接講要做什麼，我就開始」取「直接講要做什麼」）；沒有時是空字串。
func clauseBefore(text string) string {
	t := strings.TrimRight(strings.TrimSpace(text), "。.！!…~～ ")
	i := strings.LastIndexAny(t, "，,；;")
	if i < 0 || strings.LastIndexAny(t, "。！!\n") > i {
		return ""
	}
	head := t[:i]
	if j := strings.LastIndexAny(head, "。！!？?\n"); j >= 0 {
		_, size := utf8.DecodeRuneInString(head[j:])
		head = head[j+size:]
	}
	return head
}

// lastSentence 是最後一個子句（去掉結尾的標點；「資料夠了，接下來我去整理」取「接下來我去整理」）。
func lastSentence(text string) string {
	t := strings.TrimRight(strings.TrimSpace(text), "。.！!…~～ ")
	if i := strings.LastIndexAny(t, "。！!\n，,；;"); i >= 0 {
		_, size := utf8.DecodeRuneInString(t[i:])
		t = t[i+size:]
	}
	return strings.TrimSpace(t)
}

// AskOf 找出 Bot 要人處理的那一段：丟回工作的那個段落；沒有時是回覆最後 300 字。
// 回覆別處提到的登入（別件事的）不代表這件事在等登入（LazyBoy auto_continue）。handed 表示有丟回工作。
func AskOf(text string) (ask string, handed bool) {
	loc := handsBackRe.FindStringIndex(text)
	if loc == nil {
		return tailRunes(text, limits.HandBackTailChars), false
	}
	start := strings.LastIndex(text[:loc[0]], "\n\n") + 1
	end := strings.Index(text[loc[1]:], "\n\n")
	if end < 0 {
		return text[start:], true
	}
	return text[start : loc[1]+end], true
}

// ── 你交代了，它卻沒做（2026-10-23）────────────────────
// 10/19～21：「你研究一下職能」沒查任何資料就回 250 字；交代建團隊，結尾連續四次「要我建團隊嗎？」「要繼續建其他成員嗎？」，
// 每一步都要你推。原本的規則一句都沒抓到（handsBackRe 對 8 個真實結尾 0 命中）。只看第一輪、只看你本人交代的。

// 判斷的原因（NudgeFor 照它給不同的提示）。
const (
	reasonResearch = "要你研究，卻沒有真的查資料就回了"
	reasonOffer    = "你已經交代了，它卻在最後問要不要做"
)

var (
	// researchAskRe：要它去研究、查、比較（「研究員」「研究總監」這類名字先拿掉）。
	researchAskRe = regexp.MustCompile(`(幫我|請你?|麻煩|你|去|先|再|要|仔細)[^。？?\n]{0,4}(上網)?(研究|調查|查一下|查查|查資料|搜尋|比較|比價|找資料)|使用技能「deep-research」`)
	researchName  = regexp.MustCompile(`研究(員|總監|助理|部|團隊|結果|報告|檔案?|資料|的)`)
	// claimsResearchRe：回覆說研究過、查過。
	claimsResearchRe = regexp.MustCompile(`研究(完|過|好了)|查(過|完|好了)|搜尋(過|完)|(我|已經|都)(查|研究|搜尋)了`)
	// researchToolRe：算「真的查了」的工具（Hermes、各 ACP 引擎的名稱）。
	// OpenCode 的結果名稱是「Exa Web Search: …」；Hermes 也會在 execute_code 裡呼叫搜尋（程式化呼叫工具），算有做事。
	researchToolRe = regexp.MustCompile(`(?i)web[ _]?search|web[ _]?extract|web[ _]?fetch|^fetch|url_?fetch|read_?url|browser|x_(keyword|semantic)_search|crawl|scrape|firecrawl|tavily|delegate_task|subagent|execute_code`)
	// notNowRe：你說先不要做、先討論、憑印象就好。
	notNowRe = regexp.MustCompile(`先不要|先別|不要(做|動|查|研究|建|開始)|不用(查|研究|上網)|先(跟我)?討論|先聊|憑(你的)?(經驗|印象)|你覺得|你認為`)
	// taskAskRe：在交代事情（不是只問問題）。
	taskAskRe = regexp.MustCompile(`幫我|請你|請幫|麻煩|你去|你先|給我|(建|開|整理|寫|列|設|安排|處理|弄|改|做|規劃|分配|建立|做成|存成|研究|調查|查|比較|搜尋|找)`)
	// questionOnlyRe：只是問問題（結尾是問號、嗎、呢，而且沒有「幫我、請、給我」）。
	questionOnlyRe = regexp.MustCompile(`[？?嗎呢吗]\s*$`)
	askingRe       = regexp.MustCompile(`幫我|請|給我|麻煩`)
	// echoRe：測試、只要它回一個字這類。
	echoRe = regexp.MustCompile(`只回|回我一個字|就好[。！!]?\s*$|測試|不要真的`)
	// offerEndRe：交代的事還沒做，最後問要不要做（「要我建團隊嗎？」「確認後我就動手」「你先回答這三個，我再建」）。
	offerEndRe = regexp.MustCompile(`(要|需要)我(現在|直接|先|再|就|來|幫你)*(幫你)?(建|做|開|改|設|寫|跑|安排|整理|處理|執行|動手|開始|繼續|建立|新增|產生|生成|規劃|排|拉|加)[^。\n？?]{0,24}(嗎|？|\?)|` +
		`確認(後|了|一下)?[^。\n]{0,6}我(就|再|馬上)(動手|開始|建|做|執行)|(回答|回覆|告訴我|確認|決定)[^。\n]{0,24}(後|了|之後)?[，,]?我(再|就)(建|做|開始|動手|執行|安排)|` +
		// 沒寫「我」的：「要繼續建其他成員（研究員、驗證員…）嗎？」「要設報告線或開群組對話嗎？」（10/21 連續四次）。
		// 「要改成 B 嗎」「要照 A 店的價格調嗎」可能是真的要你選，不算。
		`要(繼續|接著)?(建|做|開|設|寫|跑|安排|整理|處理|執行|動手|開始|建立|新增|拉|加|排)[^。\n？?]{0,40}(嗎|？|\?)\s*$`)
	// outwardRe：會離開電腦或刪東西的動作，問一下是對的。
	outwardRe = regexp.MustCompile(outward)
	// buildRe：建 Bot、團隊、群組這類；designAskRe：你在問要哪些角色、怎麼分工（是要建議，不是要它建）。
	// 2026-10-23 重播：「我想要建立一個蝦皮海巡團隊…每個 agent 要有啥職能」不能被推去建 Bot。
	buildRe     = regexp.MustCompile(`(?i)建|新增|開[^。\n]{0,6}群組|團隊|成員|bot|隊友`)
	designAskRe = regexp.MustCompile(`要哪些|要有啥|要有什麼|要什麼(角色|職能|人)|哪些角色|什麼角色|啥職能|什麼職能|職能是|怎麼分工|人設會是|需要哪些`)
)

// IsResearchTool 回答這個工具算不算真的去查（搜尋、讀網頁、瀏覽器）。
func IsResearchTool(name string) bool { return researchToolRe.MatchString(name) }

// IsChitchat 回答你這則是不是閒聊、打招呼、測試或只是問問題（不是交代工作）：這種不用模型判斷，也不套「沒做完」的新規則。
func IsChitchat(asked string) bool {
	a := strings.TrimSpace(researchName.ReplaceAllString(asked, ""))
	if a == "" || chatRe.MatchString(a) || echoRe.MatchString(a) {
		return true
	}
	return !taskAskRe.MatchString(a) || (questionOnlyRe.MatchString(a) && !askingRe.MatchString(a))
}

// isGreeting 回答你這則是不是只打招呼、道謝或測試（比 IsChitchat 窄：「衝分潤」這種沒寫動詞的交代不算）。
func isGreeting(asked string) bool {
	a := strings.TrimSpace(asked)
	return a != "" && (chatRe.MatchString(a) || echoRe.MatchString(a))
}

// researchSkipped：你要它研究，它整輪沒有查，或說研究過卻只查了一兩下。
func researchSkipped(in Input, text string) bool {
	if in.Asked == "" || in.Continued || in.Background || notNowRe.MatchString(in.Asked) || progressRe.MatchString(in.Asked) {
		return false // 問進度（「研究結果回來了嗎」）不是要它現在去查
	}
	if !researchAskRe.MatchString(researchName.ReplaceAllString(in.Asked, "")) {
		return false
	}
	return in.Research == 0 || (claimsResearchRe.MatchString(text) && in.Research < limits.ResearchCallsMin)
}

// offeredInstead：你交代了一件事（不是只問問題），它最後一段卻問要不要做。花錢、對外發送、刪除這類問一下是對的，不算。
func offeredInstead(in Input, text string) (string, bool) {
	if in.Asked == "" || in.Continued || IsChitchat(in.Asked) || notNowRe.MatchString(in.Asked) {
		return "", false
	}
	paras := strings.Split(strings.TrimSpace(text), "\n\n")
	last := paras[len(paras)-1]
	m := offerEndRe.FindString(last)
	if m == "" || outwardRe.MatchString(m) || (buildRe.MatchString(m) && designAskRe.MatchString(in.Asked)) {
		return "", false
	}
	return m, true
}

// asksSomething：這段話是在要人做事或回答（不是只提到某個詞）。
// 「才能、之後再、後再」要是對你說的（「你登入後再…」）；「取得結果後再判斷」這種描述流程的不算（2026-10-20 重播快判找到的誤判）。
var asksRe = regexp.MustCompile(`[？?]|請你|需要你|要你|你[^。\n]{0,20}(才能|之後再|後再)|嗎`)

// Rules 是只用規則的判斷（LazyBoy 的正規表示式與 auto_continue 的條件）。
type Rules struct{}

// Judge 實作 Judge。
func (Rules) Judge(_ context.Context, in Input) (Result, error) {
	in.Text = Prose(in.Text)
	switch in.Outcome {
	case OutcomeCancelled, OutcomeInterrupted:
		return Result{}, ErrSkip
	case OutcomeFailed:
		r := Result{Verdict: Stuck, By: ByRules, Reason: "這一輪出錯，沒有做完"}
		if in.Stalled {
			r.Reason = "太久沒有動靜，看門狗停掉了這一輪"
		}
		return r, nil
	}
	if in.Looped != "" {
		// XBot 新增（LazyBoy 沒有）：引擎停下的重複不是做完，也不是要你處理；換個做法自己接著做。
		return Result{Verdict: Stuck, By: ByRules,
			Reason: clipRunes(fmt.Sprintf("一直重複用 %s（%d 次）沒有進展，被停下來", in.Looped, in.LoopCount), limits.JudgeReasonMax)}, nil
	}
	text := in.Text
	ask, handed := AskOf(text)
	if handed && isGreeting(in.Asked) && !NeedsPersonIn(ask) {
		handed = false // 你只是打招呼、在測試：「要我幫你做什麼嗎？」是在問你要什麼，不是把工作丟回來
	}
	promised := Promised(in)
	r := Result{By: ByRules, Ask: clipRunes(strings.TrimSpace(ask), limits.JudgeAskMax)}
	unfinished := handed || promised || len(in.PlanLeft) > 0 || len(in.Stranded) > 0
	personAsked, _ := needsPersonAsk(ask)
	switch {
	// 要你本人的事（登入、驗證碼、付款、核准、對外發送前的同意）：永遠停下等你，不自動續做。
	// LazyBoy 只在「有沒做完的事」時才看 NEEDS_PERSON（擋自動續做）；XBot 另外把「在要你回答」的結尾也算進來。
	case unfinished && NeedsPersonIn(ask):
		r.Verdict, r.Reason = NeedsPerson, "要你本人處理："+firstMatch(needsPersonRe, mentions(ask))
	case !unfinished && personAsked != "":
		r.Verdict, r.Reason = NeedsPerson, "要你本人處理："+personAsked
	case promised && !handed && in.UsedTools:
		r.Verdict, r.Reason = OnlyPromised, "最後說要去做下一步，卻沒有做就結束了"
	case promised && !handed:
		r.Verdict, r.Reason = OnlyPromised, "只回了一句要去做的話，整輪沒有用任何工具"
	case researchSkipped(in, text):
		r.Verdict, r.Reason = OnlyPromised, reasonResearch
	case handed:
		r.Verdict, r.Reason = HandedBack, "把還沒做完的部分交回給你："+firstMatch(handsBackRe, ask)
	case len(in.Stranded) > 0:
		r.Verdict, r.Reason = Stuck, fmt.Sprintf("%d 個背景子任務失敗，沒有重新派送", len(in.Stranded))
	case len(in.PlanLeft) > 0:
		r.Verdict, r.Reason = Stuck, fmt.Sprintf("計畫還有 %d 項沒完成", len(in.PlanLeft))
	default:
		if m, ok := offeredInstead(in, text); ok {
			r.Verdict, r.Reason, r.Ask = HandedBack, reasonOffer+"：「"+m+"」", clipRunes(m, limits.JudgeAskMax)
			break
		}
		r.Verdict, r.Reason = Done, "這一輪正常結束，沒有發現沒做完或要你處理的地方"
	}
	r.Reason = clipRunes(r.Reason, limits.JudgeReasonMax)
	return r, nil
}

// ── 自動續做的提示（LazyBoy auto_continue）──────────────

// Nudge 是自動續做時送給 Bot 的話（prompt）和對話裡顯示的那一行（shown）。
type Nudge struct {
	Prompt string
	Shown  string
}

// Continues 回答這個判斷是不是「自己接著做」的那幾種：只說要做、丟回來、計畫沒做完、背景子任務失敗。
// 出錯、被看門狗停掉的（Outcome 不是 answer）不算：那些由重試處理。
func Continues(in Input, r Result) bool {
	if in.Outcome != OutcomeAnswer {
		return false
	}
	// 背景子任務回報後的那一輪：只有失敗沒人接手的子任務才叫回去（LazyBoy auto_continue）。
	if in.Background {
		return len(in.Stranded) > 0 && r.Verdict != NeedsPerson
	}
	switch r.Verdict {
	case OnlyPromised, HandedBack:
		return true
	case Stuck:
		return len(in.PlanLeft) > 0 || len(in.Stranded) > 0 || in.Looped != ""
	}
	return false
}

// NudgeFor 組出自動續做的提示，文字照 LazyBoy auto_continue。r 是這一輪的判斷：沒查就說研究、交代了卻問要不要做、
// 模型判斷的，照它說清楚哪裡沒做到（2026-10-23；原本一律重算規則，模型判的「只說要做」會拿到「交回」的提示）。
func NudgeFor(in Input, r Result) Nudge {
	in.Text = Prose(in.Text)
	promised := Promised(in)
	_, handed := AskOf(in.Text)
	var unfinished, shown string
	switch {
	case r.Reason == reasonResearch:
		unfinished = "使用者要你研究，你還沒有真的查資料就回了。現在從幾個角度用 web_search 查、用 web_extract 讀幾篇原文，" +
			"再照守則的「回話長度」給結論和骨架，附上來源。"
		shown = "自動繼續：要研究，先真的去查"
	case strings.HasPrefix(r.Reason, reasonOffer):
		unfinished = "使用者已經交代了這件事，你卻在最後問「" + r.Ask + "」。照他交代的直接做完，做完再說一句做了什麼；" +
			"只有花錢、對外發送、刪除，或有幾條路而選錯很難回頭時才先問。"
		shown = "自動繼續：照交代的直接做完"
	case r.By == ByModel && r.Verdict == OnlyPromised:
		unfinished = "系統判斷你這一輪只說了要做、還沒真的做（" + r.Reason + "）。現在就去做（需要查就呼叫工具），做完直接給結果。"
		shown = "自動繼續：剛才只說要做，現在真的去做"
	case r.By == ByModel && r.Verdict == HandedBack:
		unfinished = "系統判斷你把還沒做完的部分交回給使用者（" + r.Reason + "）。使用者已經交代了，照交代的直接做完。"
		shown = "自動繼續：自己把剩下的做完"
	case in.Looped != "":
		unfinished = fmt.Sprintf("你剛才一直重複呼叫 %s（%d 次），結果都一樣、沒有新進展，系統先把這一輪停下來。", in.Looped, in.LoopCount) +
			"不要再送同樣的呼叫或同樣的查詢。先用已經拿到的資料把成品做出來（該存的檔照樣存），缺的寫「未取得」；真的還缺，才換一個完全不同的來源或做法。"
	case len(in.PlanLeft) > 0:
		unfinished = "你的計畫還有這些沒完成：\n" + bullets(in.PlanLeft)
	case len(in.Stranded) > 0:
		unfinished = "這些背景子任務失敗了，也沒有重新派送，工作因此卡在這裡：\n" + bullets(in.Stranded) +
			"\n請重新派送（題目切小一點，或換個查法），或自己直接做完；整合好之後，如果這件事是隊友交辦的，用 message_bot 把完整結果傳給它，不要只留在這個對話裡。"
	case promised && !handed && in.UsedTools:
		unfinished = "你剛才最後說要去做下一步（「" + lastSentence(in.Text) + "」），卻沒有做就結束了。現在就接著做，做完直接給結果；不要再寫「接下來我去做某某」就停下來。"
	case promised && !handed:
		unfinished = "你剛才只回了一句要去做的話，卻沒有真的做。現在就去做（需要查就呼叫工具），做完直接給結果；不要再只說「我來查一下」。"
	default:
		unfinished = "你剛才停下來，把還沒做完的部分交回給使用者（問要不要繼續，或請使用者自己去點、貼、確認）。"
	}
	prompt := "（系統自動繼續）" + unfinished +
		"\n請自己接著做完：失敗的步驟換做法再試（改讀頁面 HTML、改點別的按鈕或入口、重新整理、用桌面工具截圖後真的用滑鼠點、換關鍵字或來源），" +
		"不要問使用者要不要繼續，也不要請使用者代勞整件事。真的需要人做一小步（驗證、點不動的按鈕）時，用 clarify 請使用者在電腦面板做完後按「交還給 Bot」，再接著做。" +
		"只有需要使用者登入、驗證碼、付款或核定時，才停下來說明需要什麼。" + replystyle.Final +
		"如果使用者其實還沒交代具體的事（只是打招呼、問你能做什麼、在聊天），就簡短回一句就好，不要自己找事做。"
	switch {
	case shown != "":
	case in.Looped != "":
		shown = "自動繼續：一直重複同一個動作，換個做法"
	case len(in.PlanLeft) > 0:
		shown = fmt.Sprintf("自動繼續：計畫還有 %d 項沒完成", len(in.PlanLeft))
	case promised && len(in.Stranded) == 0:
		shown = "自動繼續：剛才只說要做，現在真的去做"
	case len(in.Stranded) > 0:
		shown = fmt.Sprintf("自動繼續：%d 個背景子任務失敗，重新處理", len(in.Stranded))
	default:
		shown = "自動繼續：自己把剩下的做完"
	}
	return Nudge{Prompt: prompt, Shown: shown}
}

func bullets(items []string) string {
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = "- " + it
	}
	return strings.Join(lines, "\n")
}

func firstMatch(re *regexp.Regexp, s string) string {
	return "「" + clipRunes(re.FindString(s), 40) + "」"
}

func tailRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[len(r)-n:])
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
