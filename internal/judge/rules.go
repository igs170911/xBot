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
var needsPersonRe = regexp.MustCompile(`(?i)登入|驗證碼|密碼|付款|刷卡|核定|核准|同意|授權|二階段|2FA|captcha|` +
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

// NeedsPersonIn 回答這段話是不是在要你本人做事（LazyBoy NEEDS_PERSON.search）。
func NeedsPersonIn(text string) bool {
	return needsPersonRe.MatchString(alreadyDoneRe.ReplaceAllString(text, ""))
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
	promised := Promised(in)
	r := Result{By: ByRules, Ask: clipRunes(strings.TrimSpace(ask), limits.JudgeAskMax)}
	unfinished := handed || promised || len(in.PlanLeft) > 0 || len(in.Stranded) > 0
	switch {
	// 要你本人的事（登入、驗證碼、付款、核准、對外發送前的同意）：永遠停下等你，不自動續做。
	// LazyBoy 只在「有沒做完的事」時才看 NEEDS_PERSON（擋自動續做）；XBot 另外把「在要你回答」的結尾也算進來。
	case NeedsPersonIn(ask) && (unfinished || asksRe.MatchString(ask)):
		r.Verdict, r.Reason = NeedsPerson, "要你本人處理："+firstMatch(needsPersonRe, ask)
	case promised && !handed && in.UsedTools:
		r.Verdict, r.Reason = OnlyPromised, "最後說要去做下一步，卻沒有做就結束了"
	case promised && !handed:
		r.Verdict, r.Reason = OnlyPromised, "只回了一句要去做的話，整輪沒有用任何工具"
	case handed:
		r.Verdict, r.Reason = HandedBack, "把還沒做完的部分交回給你："+firstMatch(handsBackRe, ask)
	case len(in.Stranded) > 0:
		r.Verdict, r.Reason = Stuck, fmt.Sprintf("%d 個背景子任務失敗，沒有重新派送", len(in.Stranded))
	case len(in.PlanLeft) > 0:
		r.Verdict, r.Reason = Stuck, fmt.Sprintf("計畫還有 %d 項沒完成", len(in.PlanLeft))
	default:
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

// NudgeFor 組出自動續做的提示，文字照 LazyBoy auto_continue。
func NudgeFor(in Input) Nudge {
	in.Text = Prose(in.Text)
	promised := Promised(in)
	_, handed := AskOf(in.Text)
	var unfinished string
	switch {
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
		"只有需要使用者登入、驗證碼、付款或核定時，才停下來說明需要什麼。做完後給一份完整的最終回報，包含先前已完成的部分。" +
		"如果使用者其實還沒交代具體的事（只是打招呼、問你能做什麼、在聊天），就簡短回一句就好，不要自己找事做。"
	var shown string
	switch {
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
