package judge

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── 快判的操作（PRD §22.3：judge.run、judge.stats；J10 設定；C15 先判斷）───────────

// UseView 是一個用途與它現在的開關。
type UseView struct {
	Use
	Mode Mode `json:"mode"`
}

// Today 是今天的快判用量。
type Today struct {
	Calls      int   `json:"calls" desc:"今天呼叫了幾次模型"`
	Tier2Calls int   `json:"tier2_calls" desc:"其中第二層幾次"`
	DailyCap   int   `json:"daily_cap"`
	Tier2Cap   int   `json:"tier2_cap"`
	Capped     bool  `json:"capped" desc:"今天已經到每日上限（全部照規則）"`
	CostMicros int64 `json:"cost_micros" desc:"今天的花費（百萬分之一美元，Model Proxy 用量帳本上標記 judge 的）"`
	Requests   int64 `json:"requests" desc:"用量帳本上今天標記 judge 的請求數"`
	InputTok   int64 `json:"input_tokens"`
	OutputTok  int64 `json:"output_tokens"`
}

// SettingsOut 是 xbot.judge.settings_get 的輸出。
type SettingsOut struct {
	Settings Settings  `json:"settings"`
	Uses     []UseView `json:"uses" desc:"所有用途（依編號），含現在的開關"`
	Today    Today     `json:"today"`
	MinP     float64   `json:"min_p" desc:"第一層採用的機率門檻"`
	// Problems 是今天遇到、要你處理的供應商問題（例：OpenCode Zen 餘額不足）；這些情況快判已經自動退回規則。
	Problems []Problem `json:"problems" desc:"今天遇到的供應商問題（已自動退回規則）"`
}

// Problem 是一個供應商問題。
type Problem struct {
	Kind     string `json:"kind" desc:"no_funds 餘額不足、unsupported 模型不支援 chat/completions"`
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

// problemText 是介面上的白話。
func problemText(kind, provider string) string {
	if provider == "opencode-zen" {
		return "OpenCode Zen 餘額不足（Jev 需要 Zen 儲值）"
	}
	return provider + " 餘額不足"
}

// SettingsIn 是 xbot.judge.settings_get 的輸入（沒有欄位）。
type SettingsIn struct{}

// SetIn 是 xbot.judge.settings_set 的輸入：有給的欄位才改。
type SetIn struct {
	Tier1    *TierModel      `json:"tier1,omitempty" desc:"第一層 · 選擇題；provider 空白表示不用快判（只用規則）"`
	Tier2    *TierModel      `json:"tier2,omitempty" desc:"第二層 · 短文字；provider 空白表示不用"`
	Local    *TierModel      `json:"local,omitempty" desc:"本機小模型；provider 空白表示不用"`
	Modes    map[string]Mode `json:"modes,omitempty" desc:"要改的用途開關：{用途 id: off／shadow／live}"`
	DailyCap *int            `json:"daily_cap,omitempty" min:"1" max:"100000" desc:"每天最多呼叫幾次模型；提高要你核准"`
	Tier2Cap *int            `json:"tier2_cap,omitempty" min:"1" max:"100000" desc:"第二層每天最多幾次；提高要你核准"`
}

// StatsIn 是 xbot.judge.stats 的輸入。
type StatsIn struct {
	Days int `json:"days,omitempty" min:"1" max:"90" desc:"看最近幾天；預設 7"`
}

// UseStats 是一個用途的統計與建議。
type UseStats struct {
	store.JudgeUseStats
	N    int    `json:"n"`
	Name string `json:"name"`
	Mode Mode   `json:"mode"`
	// Agreement 是模型和規則一致的比例；CorrectedAgreement 是有你改正的那幾次，模型和你的改正一致的比例（-1 表示沒有資料）。
	Agreement          float64 `json:"agreement" desc:"模型和規則一致的比例（0–1）；-1 表示還沒有資料"`
	CorrectedAgreement float64 `json:"corrected_agreement" desc:"有你改正的那幾次，模型和你一致的比例；-1 表示沒有資料"`
	ReadyForLive       bool    `json:"ready_for_live" desc:"影子模式與你的改正（沒有改正時與規則）一致率 ≥ 95%，建議可以生效"`
}

// StatsOut 是 xbot.judge.stats 的輸出。
type StatsOut struct {
	Days   int              `json:"days"`
	Uses   []UseStats       `json:"uses" desc:"依編號；沒有紀錄的用途也列出（次數為 0）"`
	Today  Today            `json:"today"`
	Recent []store.JudgeRun `json:"recent" desc:"最近 20 次快判（新到舊）"`
}

// RunIn 是 xbot.judge.run 的輸入。
type RunIn struct {
	Use     string   `json:"use" required:"true" max:"40" desc:"用途 id（xbot.judge.settings_get 的 uses）"`
	Text    string   `json:"text" required:"true" max:"8000" desc:"要判斷的內容（會依用途截短）"`
	Context []string `json:"context,omitempty" max:"10" desc:"補充的幾行（最近的訊息等）"`
	// Mode 不填用設定的開關；填 shadow／live 只影響這次回傳的是誰的判斷，不會改設定。
	Mode Mode `json:"mode,omitempty" desc:"不填用設定的開關；填 live 可以試模型的判斷（只回傳，不會做任何事）"`
}

// PreCheckIn 是 xbot.judge.precheck 的輸入。
type PreCheckIn struct {
	BotID  string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"交給哪個 Bot"`
	Text   string `json:"text" required:"true" max:"8000" desc:"你交辦的話"`
	TaskID string `json:"task_id,omitempty" max:"64" desc:"送出後的工作 id（改正時對得上）"`
}

// PreCheckOut 是 xbot.judge.precheck 的輸出。
type PreCheckOut struct {
	Chips []Chip `json:"chips" desc:"生效、模型有把握（≥ 0.6）的項目；沒有時不顯示先判斷列"`
}

// FeedbackIn 是 xbot.judge.feedback 的輸入。
type FeedbackIn struct {
	Use       string `json:"use" required:"true" max:"40" desc:"哪個用途（例：precheck_engine）；任務完成判定請用 xbot.judge.correct"`
	TaskID    string `json:"task_id" required:"true" max:"64" desc:"被判斷的那件工作"`
	Judged    string `json:"judged" required:"true" max:"200" desc:"系統當時的判斷（標籤）"`
	JudgedBy  string `json:"judged_by,omitempty" enum:"rules,model" desc:"誰判斷的"`
	Corrected string `json:"corrected,omitempty" max:"200" desc:"應該是哪一個（選項標籤）；空白表示只是說不對"`
	Note      string `json:"note,omitempty" max:"500"`
}

// FeedbackOut 是 xbot.judge.feedback 的輸出。
type FeedbackOut struct {
	Correction store.JudgeCorrection `json:"correction"`
}

// SuggestIn 是 xbot.bots.suggest 的輸入。
type SuggestIn struct {
	Text string `json:"text" required:"true" max:"1000" desc:"一句話說你要什麼樣的 Bot，例：幫我每天整理科技新聞"`
}

// SuggestOut 是 xbot.bots.suggest 的輸出：建議（可改），不會建立任何東西。
type SuggestOut struct {
	Name        string  `json:"name"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Soul        string  `json:"soul"`
	By          string  `json:"by" desc:"rules（最接近的內建範本）或 model（快判模型）"`
	P           float64 `json:"p,omitempty"`
	Why         string  `json:"why,omitempty"`
	Template    string  `json:"template,omitempty" desc:"規則用的內建範本"`
}

// Register 登錄快判的操作。s 是 nil 時只登錄定義（指令列、gen ts），呼叫會回 internal。
func Register(reg *ops.Registry, s *Service) {
	ready := func() error {
		if s == nil {
			return ops.Internal(errors.New("快判服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[SettingsIn, SettingsOut]{
		Name: "xbot.judge.settings_get", Summary: "看快判模型的設定與今天的用量",
		Description: "第一層（選擇題）與第二層（短文字）預設都是 OpenCode Go 的 deepseek-v4.1-flash（可改用 Zen 的 jev-1.13）、本機小模型，" +
			"每個用途的開關（off 只用規則、shadow 只記錄、live 生效）、每日呼叫上限與今天的次數和花費。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ SettingsIn) (SettingsOut, error) {
			if err := ready(); err != nil {
				return SettingsOut{}, err
			}
			return s.view(ctx)
		},
	})
	ops.Register(reg, ops.Op[SetIn, SettingsOut]{
		Name: "xbot.judge.settings_set", Summary: "改快判模型的設定",
		Description: "選每一層的模型、逐項切換用途（off／shadow／live）、每日上限。快判永遠只做判斷與建議，不會自己刪除、花錢、對外發送或核准。" +
			"降低上限直接生效；提高上限要你核准，而且不能代為核准。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe, NoDelegate: true,
		RiskOf: func(in SetIn) ops.Risk {
			if s == nil || (in.DailyCap == nil && in.Tier2Cap == nil) {
				return ops.RiskSafe
			}
			cur, err := s.Settings(context.Background())
			if err != nil || (in.DailyCap != nil && *in.DailyCap > cur.DailyCap) || (in.Tier2Cap != nil && *in.Tier2Cap > cur.Tier2Cap) {
				return ops.RiskConfirm
			}
			return ops.RiskSafe
		},
		Handle: func(ctx context.Context, call *ops.Call, in SetIn) (SettingsOut, error) {
			if err := ready(); err != nil {
				return SettingsOut{}, err
			}
			set, err := s.Settings(ctx)
			if err != nil {
				return SettingsOut{}, err
			}
			for id, m := range in.Modes {
				if _, ok := UseByID(id); !ok {
					return SettingsOut{}, ops.InvalidInput(ops.FieldError{Field: "modes", Message: "沒有這個用途：" + id})
				}
				if !m.Valid() {
					return SettingsOut{}, ops.InvalidInput(ops.FieldError{Field: "modes", Message: "開關要是 off、shadow、live"})
				}
				set.Modes[id] = m
			}
			for _, t := range []struct {
				in    *TierModel
				dst   *TierModel
				field string
			}{{in.Tier1, &set.Tier1, "tier1"}, {in.Tier2, &set.Tier2, "tier2"}, {in.Local, &set.Local, "local"}} {
				if t.in == nil {
					continue
				}
				tm := TierModel{Provider: strings.TrimSpace(t.in.Provider), Model: strings.TrimSpace(t.in.Model)}
				if tm.Provider != "" && tm.Model == "" {
					return SettingsOut{}, ops.InvalidInput(ops.FieldError{Field: t.field + ".model", Message: "要選模型"})
				}
				if strings.HasSuffix(tm.Model, "-free") {
					// 2026-10-18 實測：OpenCode Zen 的免費模型只給 OpenCode 自己的程式用。
					return SettingsOut{}, ops.InvalidInput(ops.FieldError{Field: t.field + ".model", Message: "免費模型只給 OpenCode 自己的程式用，XBot 不能用，請選付費版"})
				}
				// 快判只打 chat/completions：只支援 Responses API 的模型（例：muse-spark）每次都會 400。沒改的層不擋（別的設定照樣能存）。
				if tm != *t.dst {
					if tr := s.unsupported(ctx, tm); tr != "" {
						return SettingsOut{}, ops.InvalidInput(ops.FieldError{Field: t.field + ".model", Message: unsupportedText(tm, tr)})
					}
				}
				*t.dst = tm
			}
			if in.DailyCap != nil {
				set.DailyCap = *in.DailyCap
			}
			if in.Tier2Cap != nil {
				set.Tier2Cap = *in.Tier2Cap
			}
			if !call.DryRun {
				if err := s.withDB(call.DB).SaveSettings(ctx, set); err != nil {
					return SettingsOut{}, err
				}
			}
			out, err := s.view(ctx)
			if call.DryRun {
				out.Settings = set
			}
			return out, err
		},
	})
	ops.Register(reg, ops.Op[StatsIn, StatsOut]{
		Name: "xbot.judge.stats", Summary: "看快判的影子模式統計（和規則、和你的改正一致多少）",
		Description: "每個用途：判斷幾次、呼叫幾次模型、模型和規則一致的比例、有你改正的那幾次模型對了多少、平均等多久；" +
			"一致率 ≥ 95% 時 ready_for_live 為 true（建議可以從影子切到生效）。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StatsIn) (StatsOut, error) {
			if err := ready(); err != nil {
				return StatsOut{}, err
			}
			return s.stats(ctx, in.Days)
		},
	})
	ops.Register(reg, ops.Op[RunIn, Decision]{
		Name: "xbot.judge.run", Summary: "請快判判斷一件事（只回傳判斷，不會做任何事）",
		Description: "照用途的開關：off 只用規則；shadow 呼叫模型、記錄，回傳規則的判斷（model 欄位看得到模型怎麼說）；live 模型合格就用模型。" +
			"逾時、出錯、格式不合、機率低於 0.6 都退回規則。每次呼叫模型都算進每日上限。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in RunIn) (Decision, error) {
			if err := ready(); err != nil {
				return Decision{}, err
			}
			if in.Mode != "" && !in.Mode.Valid() {
				return Decision{}, ops.InvalidInput(ops.FieldError{Field: "mode", Message: "要是 off、shadow、live"})
			}
			if _, ok := UseByID(in.Use); !ok {
				return Decision{}, ops.InvalidInput(ops.FieldError{Field: "use", Message: "沒有這個用途"})
			}
			a := Ask{Use: in.Use, Text: in.Text, Context: in.Context}
			if call.Identity.Kind == ops.KindBot {
				a.BotID = call.Identity.ID
			}
			if call.DryRun {
				u, _ := UseByID(in.Use)
				r := u.RuleFor(a)
				return Decision{Answer: r, Use: u.ID, Rule: r, Mode: ModeOff, Outcome: OutcomeOff, Shown: u.Shown(r.Label)}, nil
			}
			return s.DecideAs(ctx, a, in.Mode)
		},
	})
	ops.Register(reg, ops.Op[PreCheckIn, PreCheckOut]{
		Name: "xbot.judge.precheck", Summary: "先判斷：夠不夠清楚、要不要開電腦、交給哪個引擎、預估花費",
		Description: "你交辦工作時，快判先做四個選擇題（用途 11–14）。只回生效而且模型有把握（≥ 0.6）的項目；影子模式只記錄、不回。" +
			"只是建議：不會擋住工作，也不會自己換引擎或開關電腦。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in PreCheckIn) (PreCheckOut, error) {
			if err := ready(); err != nil {
				return PreCheckOut{}, err
			}
			b, err := call.DB.GetBot(ctx, in.BotID)
			if errors.Is(err, store.ErrNotFound) {
				return PreCheckOut{}, ops.NotFound("找不到這個 Bot")
			} else if err != nil {
				return PreCheckOut{}, err
			}
			if call.DryRun {
				return PreCheckOut{Chips: []Chip{}}, nil
			}
			engine, effort := b.Engine, b.Effort
			if engine == "" {
				engine = "hermes"
			}
			if effort == "" {
				effort = "medium"
			}
			return PreCheckOut{Chips: s.PreCheck(ctx, in.Text, b.ID, in.TaskID, engine+"_"+effort)}, nil
		},
	})
	ops.Register(reg, ops.Op[FeedbackIn, FeedbackOut]{
		Name: "xbot.judge.feedback", Summary: "說快判的某項判斷不對（不對？）",
		Description: "記下來用來校正門檻（FJ-7），不會改變工作本身。同一件工作、同一個用途再說一次會換成新的。只有你本人能改正。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in FeedbackIn) (FeedbackOut, error) {
			if call.Identity.Kind != ops.KindOwner {
				return FeedbackOut{}, ops.Forbidden("只有你本人能改正系統的判斷")
			}
			u, ok := UseByID(in.Use)
			if !ok || u.ID == UseTaskDone {
				return FeedbackOut{}, ops.InvalidInput(ops.FieldError{Field: "use", Message: "沒有這個用途（任務完成判定請用 xbot.judge.correct）"})
			}
			if in.Corrected != "" && len(u.Options) > 0 && !u.HasOption(in.Corrected) {
				return FeedbackOut{}, ops.InvalidInput(ops.FieldError{Field: "corrected", Message: "要是這個用途的選項之一"})
			}
			note := strings.TrimSpace(in.Note)
			if utf8.RuneCountInString(note) > limits.JudgeNoteMax {
				return FeedbackOut{}, ops.InvalidInput(ops.FieldError{Field: "note", Message: "太長了"})
			}
			task, err := call.DB.GetTask(ctx, in.TaskID)
			if errors.Is(err, store.ErrNotFound) {
				return FeedbackOut{}, ops.NotFound("找不到這件工作")
			} else if err != nil {
				return FeedbackOut{}, err
			}
			if !call.Identity.CanAccess(ops.ResourceBot, task.BotID) {
				return FeedbackOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這件工作的 Bot")
			}
			by := in.JudgedBy
			if by == "" {
				by = ByModel
			}
			c := store.JudgeCorrection{Use: u.ID, TaskID: task.ID, BotID: task.BotID, Judged: in.Judged, JudgedBy: by,
				Corrected: in.Corrected, Note: note, Actor: call.Identity.Actor()}
			if call.DryRun {
				return FeedbackOut{Correction: c}, nil
			}
			saved, err := call.DB.PutJudgeCorrection(ctx, c)
			return FeedbackOut{Correction: saved}, err
		},
	})
	ops.Register(reg, ops.Op[SuggestIn, SuggestOut]{
		Name: "xbot.bots.suggest", Summary: "一句話建議一個 Bot（名字、職稱、介紹、常駐指示）",
		Description: "快判用途 9：生效時用第二層模型寫建議，否則用最接近的內建範本。只回傳建議，不會建立 Bot（建立用 xbot.bots.create）。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SuggestIn) (SuggestOut, error) {
			if err := ready(); err != nil {
				return SuggestOut{}, err
			}
			text := strings.TrimSpace(in.Text)
			if text == "" {
				return SuggestOut{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: "說一句你要什麼樣的 Bot"})
			}
			var d Decision
			if call.DryRun {
				u, _ := UseByID(UseBotSuggest)
				d.Answer = u.RuleFor(Ask{Text: text})
			} else {
				d, _ = s.Decide(ctx, Ask{Use: UseBotSuggest, Text: text})
			}
			return suggestOf(d.Answer, text), nil
		},
	})
}

// suggestOf 把建議整理好；模型漏掉的欄位用範本補。
func suggestOf(a Answer, text string) SuggestOut {
	t := TemplateFor(text)
	f := a.Fields
	get := func(k, def string, max int) string {
		if v := strings.TrimSpace(f[k]); v != "" {
			return clipRunes(v, max)
		}
		return def
	}
	desc := t.Description
	if a.By == ByRules {
		desc = clipRunes(strings.TrimSpace(text), 200)
	}
	return SuggestOut{Name: clipRunes(a.Label, 20), Title: get("title", t.Title, 30), Description: get("description", desc, 300),
		Soul: get("soul", t.Soul, 1000), By: a.By, P: a.P, Why: a.Why, Template: f["template"]}
}

// withDB 讓設定寫進這次操作的資料庫（dry_run 時是唯讀的）。
func (s *Service) withDB(db *store.DB) *Service {
	if db == nil || db == s.DB {
		return s
	}
	return &Service{DB: db, Call: s.Call, Transport: s.Transport, Now: s.Now, Notify: s.Notify, Logger: s.Logger}
}

func (s *Service) today(ctx context.Context, set Settings) (Today, error) {
	day, _ := s.DayStart()
	calls, t2, err := s.DB.JudgeCalls(ctx, day)
	if err != nil {
		return Today{}, err
	}
	u, err := s.DB.SumUsageEngine(ctx, UsageEngine, day)
	if err != nil {
		return Today{}, err
	}
	return Today{Calls: calls, Tier2Calls: t2, DailyCap: set.DailyCap, Tier2Cap: set.Tier2Cap, Capped: calls >= set.DailyCap,
		CostMicros: u.CostMicros, Requests: u.Calls, InputTok: u.InputTokens, OutputTok: u.OutputTokens}, nil
}

func (s *Service) view(ctx context.Context) (SettingsOut, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return SettingsOut{}, err
	}
	out := SettingsOut{Settings: set, MinP: limits.JudgeMinP}
	for _, u := range Uses {
		out.Uses = append(out.Uses, UseView{Use: *u, Mode: set.Modes[u.ID]})
	}
	if out.Today, err = s.today(ctx, set); err != nil {
		return out, err
	}
	out.Problems = []Problem{}
	// 設定的模型不支援 chat/completions：這一層一直被略過（每個供應商列一次）。
	seen := map[string]bool{}
	for _, tm := range []TierModel{set.Tier1, set.Tier2, set.Local} {
		if tr := s.unsupported(ctx, tm); tr != "" && !seen[tm.Provider] {
			seen[tm.Provider] = true
			out.Problems = append(out.Problems, Problem{Kind: OutcomeUnsupported, Provider: tm.Provider, Message: unsupportedText(tm, tr)})
		}
	}
	day, _ := s.DayStart()
	provs, err := s.DB.JudgeProblemProviders(ctx, OutcomeNoFunds, day)
	for _, p := range provs {
		out.Problems = append(out.Problems, Problem{Kind: OutcomeNoFunds, Provider: p, Message: problemText(OutcomeNoFunds, p)})
	}
	return out, err
}

func (s *Service) stats(ctx context.Context, days int) (StatsOut, error) {
	if days <= 0 {
		days = limits.JudgeStatsDays
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return StatsOut{}, err
	}
	day, _ := s.DayStart()
	since := day - int64(days-1)*24*3600*1000
	rows, err := s.DB.JudgeStats(ctx, since)
	if err != nil {
		return StatsOut{}, err
	}
	by := map[string]store.JudgeUseStats{}
	for _, r := range rows {
		by[r.Use] = r
	}
	out := StatsOut{Days: days, Uses: []UseStats{}}
	for _, u := range Uses {
		r := by[u.ID]
		r.Use = u.ID
		st := UseStats{JudgeUseStats: r, N: u.N, Name: u.Name, Mode: set.Modes[u.ID], Agreement: -1, CorrectedAgreement: -1}
		if u.Choice() && r.Answered > 0 {
			st.Agreement = float64(r.Agreed) / float64(r.Answered)
		}
		if r.Corrected > 0 {
			st.CorrectedAgreement = float64(r.CorrectedModel) / float64(r.Corrected)
		}
		// 有你改正時以改正為準（FJ-5）；沒有時看和規則的一致率。至少要有 limits.JudgeLiveMinAnswers 次合格的回答才建議。
		switch {
		case r.Corrected > 0:
			st.ReadyForLive = r.Answered >= limits.JudgeLiveMinAnswers && st.CorrectedAgreement >= limits.JudgeLiveAgreement
		case st.Agreement >= 0:
			st.ReadyForLive = r.Answered >= limits.JudgeLiveMinAnswers && st.Agreement >= limits.JudgeLiveAgreement
		}
		out.Uses = append(out.Uses, st)
	}
	if out.Today, err = s.today(ctx, set); err != nil {
		return out, err
	}
	out.Recent, err = s.DB.ListJudgeRuns(ctx, "", 20)
	if out.Recent == nil {
		out.Recent = []store.JudgeRun{}
	}
	return out, err
}
