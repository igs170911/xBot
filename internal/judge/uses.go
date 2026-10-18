package judge

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/limits"
)

// ── 快判的用途（PRD §20.2 的 1–10、§20 的「先判斷」11–15，以及 C32 一句話建定時任務）──

// Mode 是一個用途的開關（FJ-5）：off 只用規則、shadow 呼叫模型但只記錄（照規則）、live 採用模型（規則兜底）。
type Mode string

// 三種開關。
const (
	ModeOff    Mode = "off"
	ModeShadow Mode = "shadow"
	ModeLive   Mode = "live"
)

// EnumValues 讓操作的 schema 帶上三種開關。
func (Mode) EnumValues() []string {
	return []string{string(ModeOff), string(ModeShadow), string(ModeLive)}
}

// Valid 回答 m 是不是三種開關之一。
func (m Mode) Valid() bool { return m == ModeOff || m == ModeShadow || m == ModeLive }

// 層級：第一層選擇題（Jev）、第二層短文字（OpenCode Go flash）、本機小模型。
const (
	Tier1 = "tier1"
	Tier2 = "tier2"
	Local = "local"
)

// Option 是選擇題的一個選項：Key 是模型要回的標籤；Meaning 是給模型看的意思（英文，短）；Shown 是介面上的白話。
type Option struct {
	Key     string `json:"key"`
	Meaning string `json:"meaning"`
	Shown   string `json:"shown"`
}

// Answer 是一次快判的固定格式（FJ-1）：標籤＋機率＋一句原因。Fields 只有要寫字的用途才有（例：建 Bot 建議的職稱）。
type Answer struct {
	Label  string            `json:"label"`
	P      float64           `json:"p,omitempty" desc:"模型給的機率（0–1）；規則判斷時是 0"`
	Why    string            `json:"why,omitempty" desc:"一句話原因"`
	By     string            `json:"by" desc:"rules（規則）或 model（快判模型）"`
	Fields map[string]string `json:"fields,omitempty" desc:"要寫字的用途的其他欄位（例：建 Bot 建議的職稱、介紹）"`
}

// Ask 是請快判判斷一件事。
type Ask struct {
	Use string
	// Text 是主要的輸入（會依用途截短）。
	Text string
	// Context 是補充的幾行（最近的訊息、候選 Bot…），也會截短。
	Context []string
	// Options 是這次的選項（例：群組裡的候選 Bot）；空白用用途本身的選項。
	Options []Option
	// Rule 是已經算好的規則判斷；nil 時用用途的規則。
	Rule *Answer
	// Ref 是判斷的對象（工作 id…），記在 judge_run.ref，用來對人工改正；BotID 是相關的 Bot。
	Ref, BotID string
}

// Use 是一個快判用途。
type Use struct {
	ID   string `json:"id"`
	N    int    `json:"n" desc:"PRD §20 的編號；16 是一句話建定時任務（C32）"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Tier 是預設用哪一層：tier1 選擇題、tier2 短文字。
	Tier string        `json:"tier"`
	Wait time.Duration `json:"-"`
	// WaitMS 是等待上限（毫秒）。
	WaitMS  int64    `json:"wait_ms"`
	Options []Option `json:"options,omitempty"`
	// Locked 是規則判成這些時，模型不能改（FJ-4：安全方向只能更嚴，例：需要你、要你核定）。
	Locked []string `json:"locked,omitempty"`
	// MaxOut 是要寫字的用途，標籤最多幾個字。
	MaxOut int `json:"max_out,omitempty"`
	// Wired：這個用途已經接到產品的流程裡；沒接上的只能從 xbot.judge.run 試。
	Wired   bool `json:"wired"`
	Default Mode `json:"default_mode"`
	// Background：背景用途（不擋使用者）。
	Background bool `json:"background,omitempty"`
	// instr 是給模型的一句話任務（英文，短）。
	instr string
	rule  func(Ask) Answer
	// inputMax 是 Text 最多幾個字；0 用 limits.JudgeInputMax。
	inputMax int
}

// 用途 id。
const (
	UseRoute            = "route"
	UseTaskDone         = "task_done" // 和 runtime.UseTaskDone 同值（改正紀錄用）
	UseApproval         = "approval"
	UseProgressQ        = "progress_q"
	UseErrorKind        = "error_kind"
	UseSkillDup         = "skill_dup"
	UseResponder        = "responder"
	UseSummary          = "summary"
	UseBotSuggest       = "bot_suggest"
	UseTitle            = "title"
	UsePreEngine        = "precheck_engine"
	UsePreComputer      = "precheck_computer"
	UsePreClarity       = "precheck_clarity"
	UsePreCost          = "precheck_cost"
	UsePeerReply        = "peer_reply"
	UseRoutine          = "routine"
	SeveralKey          = "several"
	responderSeveralTxt = "Several members: the message explicitly needs answers or work from more than one member."
)

// PreCheckUses 是「先判斷」列的四項（C15：夠清楚、要不要開電腦、交給誰、預估花費），依畫面順序。
var PreCheckUses = []string{UsePreClarity, UsePreComputer, UsePreEngine, UsePreCost}

func opts(kv ...string) []Option {
	out := make([]Option, 0, len(kv)/3)
	for i := 0; i+2 < len(kv); i += 3 {
		out = append(out, Option{Key: kv[i], Meaning: kv[i+1], Shown: kv[i+2]})
	}
	return out
}

var engineOptions = func() []Option {
	var out []Option
	engines := [][2]string{{"hermes", "Hermes"}, {"claude-code", "Claude Code"}, {"codex", "Codex"}}
	efforts := [][3]string{{"low", "low thinking effort", "低"}, {"medium", "medium thinking effort", "中"}, {"high", "high thinking effort", "高"}}
	for _, e := range engines {
		for _, f := range efforts {
			out = append(out, Option{Key: e[0] + "_" + f[0], Meaning: e[1] + " engine with " + f[1], Shown: "交給 " + e[1] + " · " + f[2]})
		}
	}
	return out
}()

// Uses 是所有用途，依編號。
var Uses = []*Use{
	{ID: UseRoute, N: 1, Name: "對話分流", Desc: "閒聊／查進度／要做事，決定用聊天模型或工作模型", Tier: Tier1, Wait: limits.JudgeWaitRoute,
		Options: opts("chat", "small talk or greeting, no work needed", "閒聊", "progress", "asks about the progress or status of ongoing work", "查進度",
			"work", "asks the bot to do or find something", "要做事"),
		instr: "Classify the owner's latest message.", rule: ruleRoute, inputMax: limits.JudgeRouteInputMax, Default: ModeShadow},
	{ID: UseTaskDone, N: 2, Name: "任務完成了沒", Desc: "Bot 回完一輪後，判斷做完了、要你、交回、只說要做、卡住", Tier: Tier1, Wait: limits.JudgeTaskDoneWait,
		Options: opts("done", "the work is finished and reported", "已完成", "needs_person", "the owner must act: login, verification code, payment, approval, consent before sending", "需要你",
			"handed_back", "the bot hands unfinished work back to the owner or asks whether to continue", "交回給你",
			"only_promised", "the bot only said it would do it but did not do anything", "只說要做", "stuck", "an error or unfinished plan stopped the work", "卡住了"),
		Locked: []string{string(NeedsPerson)}, instr: "Did the bot finish the owner's task in its last reply?", rule: ruleTaskDone,
		inputMax: limits.JudgeTaskDoneInputMax, Wired: true, Default: ModeShadow},
	{ID: UseApproval, N: 3, Name: "要不要找你核定", Desc: "花錢、簽約、上架、對外發送、刪除前先問你；只能多攔，不能放行", Tier: Tier1, Wait: limits.JudgeWaitApproval,
		Options: opts("yes", "needs the owner's approval: spends money, signs, publishes or lists, sends outside, deletes, or is irreversible", "要你核定",
			"no", "safe to do without asking", "不用問"),
		Locked: []string{"yes"}, instr: "Does this action need the owner's approval before it is done?", rule: ruleApproval,
		inputMax: limits.JudgeApprovalInputMax, Default: ModeShadow},
	{ID: UseProgressQ, N: 4, Name: "是不是在問進度", Desc: "是的話附上目前進度的事實", Tier: Tier1, Wait: limits.JudgeWaitProgress,
		Options: opts("yes", "the owner is asking about progress or status", "在問進度", "no", "not a progress question", "不是"),
		instr:   "Is the owner asking about the progress of ongoing work?", rule: ruleProgressQ, inputMax: limits.JudgeRouteInputMax, Default: ModeShadow},
	{ID: UseErrorKind, N: 5, Name: "錯誤分類", Desc: "暫時性的重試、永久的停下通知；不確定當永久", Tier: Tier1, Wait: limits.JudgeWaitError,
		Options: opts("transient", "temporary: rate limit, timeout, overloaded, connection reset; retry later", "暫時性", "permanent", "will fail again; stop and tell the owner", "永久",
			"auth", "login, key or permission problem", "登入或權限", "quota", "quota, credits or budget used up", "額度用完"),
		instr: "Classify this error message.", rule: ruleErrorKind, inputMax: limits.JudgeErrorInputMax, Default: ModeShadow},
	{ID: UseSkillDup, N: 6, Name: "重複技能偵測", Desc: "兩個技能是不是同一個、重疊或不同（背景）", Tier: Tier1, Wait: limits.JudgeWaitSkillDup, Background: true,
		Options: opts("same", "the two skills do the same thing", "相同", "overlap", "they partly overlap", "重疊", "different", "they are different", "不同"),
		instr:   "Compare the two skills (separated by ---).", rule: ruleSkillDup, Default: ModeShadow},
	{ID: UseResponder, N: 7, Name: "交給哪個 Bot", Desc: "群組沒點名時誰回答（低於 0.6 交給主責）", Tier: Tier1, Wait: limits.JudgeWaitResponder,
		instr: "Which bot in this room should answer the new message? Choose several only when the message needs several members.",
		rule:  func(a Ask) Answer { return Answer{} }, Wired: true, Default: ModeShadow},
	{ID: UseSummary, N: 8, Name: "一行摘要", Desc: "完成通知、進度面板上的一行（≤ 30 字，背景）", Tier: Tier2, Wait: limits.JudgeWaitSummary, Background: true,
		MaxOut: limits.JudgeSummaryMax, instr: "Summarize what was done in one line of Traditional Chinese, at most 30 characters, as the label.",
		rule: ruleSummary, Wired: true, Default: ModeOff},
	{ID: UseBotSuggest, N: 9, Name: "一句話建立 Bot", Desc: "建議名字、職稱、介紹、常駐指示（可改）", Tier: Tier2, Wait: limits.JudgeWaitBotSuggest,
		MaxOut: 20, instr: "Suggest a new bot for this request. label = a short Traditional Chinese name (<= 6 characters); " +
			"fields: title (job title, <= 10 characters), description (one sentence), soul (2-3 short standing instructions).",
		rule: ruleBotSuggest, Wired: true, Default: ModeOff},
	{ID: UseTitle, N: 10, Name: "對話串命名", Desc: "新對話串的標題（≤ 16 字）", Tier: Tier2, Wait: limits.JudgeWaitTitle, Background: true,
		MaxOut: limits.JudgeTitleMax, instr: "Name this conversation thread in Traditional Chinese, at most 16 characters, as the label.",
		rule: ruleTitle, inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeOff},
	{ID: UsePreEngine, N: 11, Name: "選引擎與思考強度", Desc: "簡單的事不用最貴的模型（先判斷）", Tier: Tier1, Wait: limits.JudgeWaitPreCheck,
		Options: engineOptions, instr: "Which engine and thinking effort fit this task best? Simple tasks need less.",
		rule: func(Ask) Answer { return Answer{Label: "hermes_medium", Why: "照 Bot 的預設引擎"} }, inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeShadow},
	{ID: UsePreComputer, N: 12, Name: "要不要開電腦", Desc: "不用電腦的事不必等開機（先判斷）", Tier: Tier1, Wait: limits.JudgeWaitPreCheck,
		Options: opts("answer", "can be answered directly without any tool", "不用開電腦 · 直接回答", "web", "needs a web search only", "不用開電腦 · 先查網路",
			"browser", "needs a real browser (forms, logins, clicking)", "要開電腦 · 用瀏覽器", "desktop", "needs desktop apps or files", "要開電腦 · 用桌面 App"),
		instr: "What does this task need?", rule: func(Ask) Answer { return Answer{Label: "desktop", Why: "照舊開電腦"} },
		inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeShadow},
	{ID: UsePreClarity, N: 13, Name: "要不要先問清楚", Desc: "缺關鍵資訊時先問，不做白工（先判斷）", Tier: Tier1, Wait: limits.JudgeWaitPreCheck,
		Options: opts("clear", "clear enough to start", "夠清楚", "missing", "key information is missing; ask first (say what is missing in why)", "缺資訊 · 先問清楚"),
		instr:   "Is this request clear enough to start working?", rule: func(Ask) Answer { return Answer{Label: "clear", Why: "照常開始做"} },
		inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeShadow},
	{ID: UsePreCost, N: 14, Name: "花費預估", Desc: "小（< 2 萬 token）／中／大（先判斷）", Tier: Tier1, Wait: limits.JudgeWaitPreCheck,
		Options: opts("small", "under 20k tokens: a quick answer or one lookup", "預估 小（< 2 萬 token）", "medium", "20k-200k tokens: several steps", "預估 中",
			"large", "over 200k tokens: long research, many files or pages", "預估 大"),
		instr: "Estimate how much model usage this task will take.", rule: rulePreCost, inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeShadow},
	{ID: UsePeerReply, N: 15, Name: "隊友訊息要不要回", Desc: "要處理／只要知道／不用回，減少 Bot 之間的寒暄", Tier: Tier1, Wait: limits.JudgeWaitPeerReply,
		Options: opts("handle", "needs work or an answer", "要處理", "fyi", "just information to note", "只要知道", "skip", "small talk; no reply needed", "不用回"),
		instr:   "Does this teammate message need a reply?", rule: func(Ask) Answer { return Answer{Label: "handle", Why: "照常處理"} }, Default: ModeShadow},
	{ID: UseRoutine, N: 16, Name: "一句話建定時任務", Desc: "把你的話整理成排程看得懂的說法，再交給規則解析", Tier: Tier2, Wait: limits.JudgeWaitRoutine,
		MaxOut: 200, instr: "Rewrite the request as one Traditional Chinese sentence: when (e.g. 每天早上9點, 每週一下午3點, 每30分鐘, 10月20日下午2點) " +
			"followed by what to do. Keep bot names. Put it in label.",
		rule: func(a Ask) Answer { return Answer{Label: strings.TrimSpace(a.Text)} }, inputMax: limits.JudgeRouteInputMax, Wired: true, Default: ModeOff},
}

func init() {
	for _, u := range Uses {
		u.WaitMS = u.Wait.Milliseconds()
	}
}

// UseByID 找一個用途。
func UseByID(id string) (*Use, bool) {
	for _, u := range Uses {
		if u.ID == id {
			return u, true
		}
	}
	return nil, false
}

// Shown 回傳一個選項在介面上的白話（找不到時原樣）。
func (u *Use) Shown(label string) string {
	for _, o := range u.Options {
		if o.Key == label {
			return o.Shown
		}
	}
	return label
}

// HasOption 回答 key 是不是這個用途的選項之一。
func (u *Use) HasOption(key string) bool {
	for _, o := range u.Options {
		if o.Key == key {
			return true
		}
	}
	return false
}

// Choice 回答這個用途是不是選擇題（有固定選項，或是交給哪個 Bot）。
func (u *Use) Choice() bool { return u.MaxOut == 0 }

// RuleFor 算出規則的判斷。
func (u *Use) RuleFor(a Ask) Answer {
	if a.Rule != nil {
		v := *a.Rule
		v.By = ByRules
		return v
	}
	v := u.rule(a)
	v.By = ByRules
	return v
}

// ── 各用途的規則（退路）───────────────────────────────

var chatRe = regexp.MustCompile(`(?i)^(嗨|哈囉|hi|hello|hey|你好|您好|早安|午安|晚安|謝謝|感謝|thanks|thank you|ok|okay|好的?|收到|了解|讚|辛苦了)[!！。.~～ ]*$`)

func ruleRoute(a Ask) Answer {
	t := strings.TrimSpace(a.Text)
	switch {
	case IsProgressQuestion(t):
		return Answer{Label: "progress", Why: "有問進度的字"}
	case chatRe.MatchString(t):
		return Answer{Label: "chat", Why: "只是打招呼"}
	}
	return Answer{Label: "work", Why: "不確定時用工作模型"}
}

func ruleTaskDone(a Ask) Answer {
	r, err := (Rules{}).Judge(context.Background(), Input{Outcome: OutcomeAnswer, Text: a.Text})
	if err != nil {
		return Answer{Label: string(Done)}
	}
	return Answer{Label: string(r.Verdict), Why: r.Reason}
}

// approvalRe：只有你能決定的事（花錢、簽約、上架、對外發送、刪除、不可逆）。PRD §20.3「一定要人」。
var approvalRe = regexp.MustCompile(`(?i)付款|付錢|刷卡|轉帳|匯款|購買|買下|下單|訂購|花費|簽約|合約|簽署|上架|定價|發佈|發布|公開|寄出|寄信|發信|送出|發送|貼文|刪除|刪掉|移除|清空|取消訂閱|退款|pay|purchase|buy|checkout|sign|publish|send|email|post|delete|remove|drop`)

func ruleApproval(a Ask) Answer {
	if m := approvalRe.FindString(a.Text); m != "" {
		return Answer{Label: "yes", Why: "有「" + m + "」，只有你能決定"}
	}
	return Answer{Label: "no", Why: "沒看到要你決定的字"}
}

func ruleProgressQ(a Ask) Answer {
	if IsProgressQuestion(strings.TrimSpace(a.Text)) {
		return Answer{Label: "yes", Why: "有問進度的字"}
	}
	return Answer{Label: "no"}
}

var (
	authRe      = regexp.MustCompile(`(?i)\b(401|403)\b|unauthori[sz]ed|forbidden|invalid.?api.?key|permission|登入|權限|金鑰`)
	quotaRe     = regexp.MustCompile(`(?i)quota|insufficient|credit|billing|budget|額度|餘額|花費上限|\b402\b`)
	transientRe = regexp.MustCompile(`(?i)\b(429|500|502|503|504|529)\b|rate.?limit|too many requests|timeout|timed out|overload|temporar|connection (reset|refused)|EOF|逾時|忙碌|暫時|連不上|限流`)
)

// ruleErrorKind 照 LazyBoy _TRANSIENT 的想法；不確定當永久（不亂重試）。
func ruleErrorKind(a Ask) Answer {
	switch t := a.Text; {
	case quotaRe.MatchString(t):
		return Answer{Label: "quota", Why: "額度或花費用完"}
	case authRe.MatchString(t):
		return Answer{Label: "auth", Why: "登入或權限問題"}
	case transientRe.MatchString(t):
		return Answer{Label: "transient", Why: "限流、逾時或暫時忙碌"}
	}
	return Answer{Label: "permanent", Why: "不確定時當成永久，不亂重試"}
}

func ruleSkillDup(a Ask) Answer {
	left, right, ok := strings.Cut(a.Text, "---")
	if !ok {
		return Answer{Label: "different"}
	}
	s := overlap(left, right)
	switch {
	case s >= 0.8:
		return Answer{Label: "same", Why: "字詞幾乎一樣"}
	case s >= 0.3:
		return Answer{Label: "overlap", Why: "字詞有一部分重疊"}
	}
	return Answer{Label: "different", Why: "字詞不太重疊"}
}

// overlap 是兩段文字的二字詞（英文是單字）重疊比例（Jaccard）。
func overlap(a, b string) float64 {
	grams := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.Fields(strings.ToLower(s)) {
			r := []rune(w)
			if len(r) == 1 || r[0] < 0x2E80 {
				out[w] = true
				continue
			}
			for i := 0; i+1 < len(r); i++ {
				out[string(r[i:i+2])] = true
			}
		}
		return out
	}
	ga, gb := grams(a), grams(b)
	if len(ga) == 0 || len(gb) == 0 {
		return 0
	}
	inter := 0
	for g := range ga {
		if gb[g] {
			inter++
		}
	}
	return float64(inter) / float64(len(ga)+len(gb)-inter)
}

var sentenceEnd = regexp.MustCompile(`[。！？!?\n]`)

// FirstSentence 取第一句、截到 n 個字（用途 8 的規則：第一句截斷）。
func FirstSentence(text string, n int) string {
	t := strings.TrimSpace(text)
	for strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") {
		t = strings.TrimSpace(strings.TrimLeft(t, "#-* "))
	}
	if loc := sentenceEnd.FindStringIndex(t); loc != nil {
		t = t[:loc[0]]
	}
	return clipRunes(strings.Join(strings.Fields(t), " "), n)
}

func ruleSummary(a Ask) Answer {
	return Answer{Label: FirstSentence(a.Text, limits.JudgeSummaryMax), Why: "第一句截斷"}
}

// TitleRule 是用途 10 的規則：前 20 字。
func TitleRule(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(t) <= limits.JudgeTitleRuleChars {
		return t
	}
	return string([]rune(t)[:limits.JudgeTitleRuleChars]) + "…"
}

func ruleTitle(a Ask) Answer { return Answer{Label: TitleRule(a.Text), Why: "前 20 字"} }

func rulePreCost(a Ask) Answer {
	n := utf8.RuneCountInString(a.Text)
	if n > 300 || strings.Contains(a.Text, "每一") || strings.Contains(a.Text, "全部") || strings.Contains(a.Text, "所有") {
		return Answer{Label: "medium", Why: "內容多或範圍大"}
	}
	return Answer{Label: "small", Why: "一般的短交辦"}
}

// ── 用途 9 的規則：最接近的內建範本 ─────────────────────

// Template 是建立 Bot 的內建範本。
type Template struct {
	Key, Name, Title, Description, Soul string
	words                               []string
}

// Templates 是內建範本（用途 9 的退路：最接近的範本）。
var Templates = []Template{
	{Key: "research", Name: "小研", Title: "研究員", Description: "上網查資料、比較、整理成重點報告。", Soul: "先給結論再給細節。\n每個事實附來源網址。\n區分已確認、估計和查不到的。",
		words: []string{"查", "研究", "比較", "資料", "調查", "分析", "新聞", "報告", "research"}},
	{Key: "writer", Name: "小寫", Title: "文案", Description: "寫文案、貼文、信件與摘要。", Soul: "白話、短句。\n先給一版可以直接用的，再給兩個替代說法。\n對外發送前一定先問我。",
		words: []string{"寫", "文案", "貼文", "文章", "信", "翻譯", "摘要", "社群", "write"}},
	{Key: "secretary", Name: "小秘", Title: "秘書", Description: "幫你排行程、提醒、整理待辦與會議紀錄。", Soul: "記得我說過的偏好。\n時間一律寫清楚日期與星期。\n不確定的事先問我。",
		words: []string{"行程", "提醒", "待辦", "會議", "秘書", "日曆", "排程", "安排"}},
	{Key: "support", Name: "小服", Title: "客服", Description: "回覆客人的問題、整理常見問答。", Soul: "語氣親切、有禮貌。\n不確定的不要亂承諾，先問我。\n退款、賠償一定要我核准。",
		words: []string{"客服", "客人", "顧客", "回覆", "問答", "售後", "support"}},
	{Key: "engineer", Name: "小工", Title: "工程師", Description: "寫程式、修錯誤、看日誌與自動化。", Soul: "改之前先讀懂現在的程式。\n做完要測試。\n刪除或部署前先問我。",
		words: []string{"程式", "code", "開發", "網站", "bug", "錯誤", "自動化", "腳本", "工程"}},
	{Key: "finance", Name: "小帳", Title: "會計", Description: "整理帳目、發票、報表與預算。", Soul: "數字要對得起來，附算式。\n付款、轉帳一定先問我。\n用表格呈現。",
		words: []string{"帳", "會計", "發票", "報表", "預算", "財務", "記帳", "訂單"}},
	{Key: "marketing", Name: "小行", Title: "行銷", Description: "規劃活動、看數據、提出行銷點子。", Soul: "每個點子附預估效果與成本。\n花錢的事先問我。\n用數據說話。",
		words: []string{"行銷", "廣告", "活動", "推廣", "電商", "sales", "業務", "銷售", "marketing"}},
}

// TemplateFor 找最接近的內建範本（字詞命中最多的；都沒有時用研究員）。
func TemplateFor(text string) Template {
	best, score := Templates[0], 0
	low := strings.ToLower(text)
	for _, t := range Templates {
		n := 0
		for _, w := range t.words {
			if strings.Contains(low, w) {
				n++
			}
		}
		if n > score {
			best, score = t, n
		}
	}
	return best
}

func ruleBotSuggest(a Ask) Answer {
	t := TemplateFor(a.Text)
	desc := strings.TrimSpace(a.Text)
	if desc == "" {
		desc = t.Description
	}
	return Answer{Label: t.Name, Why: "最接近的範本：" + t.Title,
		Fields: map[string]string{"title": t.Title, "description": clipRunes(desc, 200), "soul": t.Soul, "template": t.Key}}
}
