package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// ── 快判服務：設定、每日上限、影子紀錄、退路（PRD §20.1）───────────

// 一次快判的結果（judge_run.outcome）。
const (
	OutcomeOK            = "ok"
	OutcomeLowConfidence = "low_confidence"
	OutcomeInvalid       = "invalid"
	OutcomeTimeout       = "timeout"
	OutcomeError         = "error"
	OutcomeRateLimited   = "rate_limited"
	OutcomeCapped        = "capped"
	OutcomeNotConfigured = "not_configured"
	OutcomeLocked        = "locked"
	OutcomeOff           = "off"
	// OutcomeNoFunds：供應商說帳戶餘額不足（例：OpenCode Zen 沒有儲值，Jev 要 Zen 的隨用隨付餘額）。
	OutcomeNoFunds = "no_funds"
	// OutcomeUnsupported：這一層的模型不支援 chat/completions（例：只支援 Responses API），沒有呼叫就往下一層退。
	OutcomeUnsupported = "unsupported"
)

// transportChat 是快判唯一會用的呼叫方式（proxy.TransportChat：POST chat/completions）。
const transportChat = "chat_completions"

// noFundsRe 認出「餘額不足」類的錯誤（OpenCode Zen：Upstream request failed: Insufficient account funds）。
var noFundsRe = regexp.MustCompile(`(?i)insufficient (account )?(funds|balance|credits?)|out of credits|payment required|餘額不足`)

// SettingKey 是快判設定在 setting 表的 key。
const SettingKey = "judge.settings"

// UsageEngine 是快判呼叫模型時，用量帳本上的標記（usage_entry.engine）。
const UsageEngine = "judge"

// 預設的模型（PRD §20：第一層 OpenCode Zen 的 Jev 付費版；第二層 OpenCode Go flash）。
// 免費版 jev-1.13-free 只給 OpenCode 自己的程式用（2026-10-18 實測），XBot 不能用。
var (
	// 使用者決定（2026-10-18）：用 OpenCode Go；Go 沒有 Jev 類的分類模型，兩層都用 deepseek-v4.1-flash
	// （實測分類正確、約 2 秒；比 mimo-v2.6-flash 的 5.4 秒快）。要用 Jev 可在 J10 改成 opencode-zen/jev-1.13（需 Zen 儲值）。
	DefaultTier1 = TierModel{Provider: "opencode-go", Model: "deepseek-v4.1-flash"}
	DefaultTier2 = TierModel{Provider: "opencode-go", Model: "deepseek-v4.1-flash"}
)

// TierModel 是一層用哪個供應商的哪個模型；Provider 空白表示這一層不用。
type TierModel struct {
	Provider string `json:"provider" max:"64" desc:"供應商 id（xbot.models.get 看）；空白表示這一層不用"`
	Model    string `json:"model" max:"200" desc:"模型"`
}

// On 回答這一層有沒有設定。
func (t TierModel) On() bool { return t.Provider != "" && t.Model != "" }

// Settings 是快判的設定（設定 → AI 模型 → 快判模型，J10）。
type Settings struct {
	Tier1 TierModel `json:"tier1" desc:"第一層 · 選擇題（預設 opencode-go / deepseek-v4.1-flash；Jev 需 Zen 儲值）"`
	Tier2 TierModel `json:"tier2" desc:"第二層 · 短文字（預設 opencode-go / deepseek-v4.1-flash）"`
	Local TierModel `json:"local" desc:"本機小模型（LM Studio／Ollama 等，設成一個供應商）；空白不用"`
	// Modes 是每個用途的開關；沒寫的用用途的預設（選擇題影子、要寫字的關）。
	Modes    map[string]Mode `json:"modes" desc:"每個用途的開關：off 只用規則、shadow 只記錄、live 生效"`
	DailyCap int             `json:"daily_cap" desc:"每天最多呼叫幾次模型；到了全部退回規則並通知一次（預設 300）"`
	Tier2Cap int             `json:"tier2_cap" desc:"第二層每天最多幾次（預設 100）；到了往下一層退"`
}

// stored 是存進資料庫的樣子：沒存過的層用預設，存成空白表示「不用這一層」。
type stored struct {
	Tier1    *TierModel      `json:"tier1,omitempty"`
	Tier2    *TierModel      `json:"tier2,omitempty"`
	Local    *TierModel      `json:"local,omitempty"`
	Modes    map[string]Mode `json:"modes,omitempty"`
	DailyCap int             `json:"daily_cap,omitempty"`
	Tier2Cap int             `json:"tier2_cap,omitempty"`
}

// Caller 經 Model Proxy 呼叫一個供應商的 chat/completions（金鑰只在 Model Proxy）。session 給 OpenCode 的對話標頭。
// 供應商沒設定時回 ErrNotConfigured。
type Caller func(ctx context.Context, provider string, body []byte, session string) (status int, resp []byte, err error)

// ErrNotConfigured 表示這一層的供應商還沒設定。
var ErrNotConfigured = errors.New("快判模型的供應商還沒設定")

// Service 是快判服務。Call 是 nil 時所有用途只用規則（但影子／生效設定照樣保存）。
type Service struct {
	DB   *store.DB
	Call Caller
	// Transport 回傳一個模型的呼叫方式（chat_completions、codex_responses、anthropic_messages；proxy.DBStore.TransportFor）。
	// 快判只會打 chat/completions：別的方式的模型存設定時擋下、執行時略過這一層。nil 或回空白時不檢查。
	Transport func(ctx context.Context, provider, model string) (string, error)
	// Now 是現在時間（測試用假時鐘）；nil 用 time.Now。
	Now func() time.Time
	// Notify 在事件寫進 event 表之後呼叫（SSE 用）；可為 nil。
	Notify func(store.Event)
	Logger *slog.Logger

	// mu 讓「檢查每日上限＋預留一次」不會同時發生，並記住還沒寫進 judge_run 的呼叫（inflight）。
	mu        sync.Mutex
	inflight  int
	inflight2 int
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// DayStart 回傳今天 0 點（本地時間，毫秒）與日期字串。
func (s *Service) DayStart() (int64, string) {
	t := s.now()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return d.UnixMilli(), d.Format("2006-01-02")
}

// Settings 讀快判設定（補上預設值）。
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var st stored
	if s.DB != nil {
		if _, err := s.DB.GetSettingInto(ctx, SettingKey, &st); err != nil {
			return Settings{}, err
		}
	}
	return withDefaults(st), nil
}

func withDefaults(st stored) Settings {
	out := Settings{Tier1: DefaultTier1, Tier2: DefaultTier2, Modes: map[string]Mode{}, DailyCap: limits.JudgeDailyCalls, Tier2Cap: limits.JudgeTier2DailyCalls}
	if st.Tier1 != nil {
		out.Tier1 = *st.Tier1
	}
	if st.Tier2 != nil {
		out.Tier2 = *st.Tier2
	}
	if st.Local != nil {
		out.Local = *st.Local
	}
	if st.DailyCap > 0 {
		out.DailyCap = st.DailyCap
	}
	if st.Tier2Cap > 0 {
		out.Tier2Cap = st.Tier2Cap
	}
	for _, u := range Uses {
		m, ok := st.Modes[u.ID]
		if !ok || !m.Valid() {
			m = u.Default
		}
		out.Modes[u.ID] = m
	}
	return out
}

// SaveSettings 存快判設定。
func (s *Service) SaveSettings(ctx context.Context, set Settings) error {
	st := stored{Tier1: &set.Tier1, Tier2: &set.Tier2, Local: &set.Local, Modes: set.Modes, DailyCap: set.DailyCap, Tier2Cap: set.Tier2Cap}
	return s.DB.SetSetting(ctx, SettingKey, st)
}

// Decision 是一次快判的結果：Answer 是要採用的（影子模式、退路時是規則的）。
type Decision struct {
	Answer
	Use      string  `json:"use"`
	Mode     Mode    `json:"mode"`
	Rule     Answer  `json:"rule" desc:"規則的判斷"`
	Model    *Answer `json:"model,omitempty" desc:"模型的判斷（有回答時；影子模式時不生效）"`
	Outcome  string  `json:"outcome" desc:"ok、low_confidence、invalid、timeout、error、rate_limited、capped、not_configured、locked、off、no_funds、unsupported"`
	Tier     string  `json:"tier,omitempty" desc:"回答的是哪一層：tier1、tier2、local"`
	Provider string  `json:"provider,omitempty"`
	ModelID  string  `json:"model_id,omitempty"`
	Shown    string  `json:"shown,omitempty" desc:"介面上的白話（選擇題的選項說明）"`
}

// Live 回答這次採用了模型的判斷。
func (d Decision) Live() bool { return d.By == ByModel }

// ErrUnknownUse 表示沒有這個用途。
var ErrUnknownUse = errors.New("沒有這個快判用途")

type tierTry struct {
	name string
	tm   TierModel
}

// chain 是這個用途往下退的順序（P05）：選擇題 Jev → Go flash → 本機小模型；要寫字的 Go flash → 本機小模型。
func chain(u *Use, set Settings) []tierTry {
	var out []tierTry
	if u.Tier == Tier1 && set.Tier1.On() {
		out = append(out, tierTry{Tier1, set.Tier1})
	}
	if set.Tier2.On() && (len(out) == 0 || set.Tier2 != out[0].tm) {
		out = append(out, tierTry{Tier2, set.Tier2})
	}
	if set.Local.On() {
		out = append(out, tierTry{Local, set.Local})
	}
	return out
}

// Mode 回傳一個用途現在的開關（讀不到設定時用預設）。
func (s *Service) Mode(ctx context.Context, use string) Mode {
	u, ok := UseByID(use)
	if !ok {
		return ModeOff
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return u.Default
	}
	return set.Modes[use]
}

// Decide 照用途的開關判斷一件事：關 → 規則；影子 → 呼叫模型、記錄、照規則；生效 → 模型合格就用模型，否則規則。
// 不會回錯誤讓呼叫的人卡住：模型出任何問題都退回規則（FJ-2）。只有用途不存在時回 ErrUnknownUse。
func (s *Service) Decide(ctx context.Context, a Ask) (Decision, error) {
	u, ok := UseByID(a.Use)
	if !ok {
		return Decision{}, fmt.Errorf("%w：%s", ErrUnknownUse, a.Use)
	}
	return s.decide(ctx, u, a, ""), nil
}

// DecideAs 和 Decide 一樣，但用指定的開關（xbot.judge.run 試用時用）。
func (s *Service) DecideAs(ctx context.Context, a Ask, mode Mode) (Decision, error) {
	u, ok := UseByID(a.Use)
	if !ok {
		return Decision{}, fmt.Errorf("%w：%s", ErrUnknownUse, a.Use)
	}
	return s.decide(ctx, u, a, mode), nil
}

func (s *Service) decide(ctx context.Context, u *Use, a Ask, force Mode) Decision {
	rule := u.RuleFor(a)
	d := Decision{Answer: rule, Use: u.ID, Rule: rule, Mode: ModeOff, Outcome: OutcomeOff, Shown: u.Shown(rule.Label)}
	set, err := s.Settings(ctx)
	if err != nil {
		s.log().Warn("快判：讀不到設定，只用規則", "err", err)
		return d
	}
	d.Mode = set.Modes[u.ID]
	if force != "" {
		d.Mode = force
	}
	if d.Mode == ModeOff {
		return d
	}
	options := a.Options
	if len(options) == 0 {
		options = u.Options
	}
	run := store.JudgeRun{Use: u.ID, Mode: string(d.Mode), Ref: a.Ref, BotID: a.BotID, RuleLabel: rule.Label}
	tries := chain(u, set)
	switch {
	case s.Call == nil || len(tries) == 0 || (u.Choice() && len(options) < 2):
		d.Outcome = OutcomeNotConfigured
		return d
	case slicesContains(u.Locked, rule.Label):
		// 安全方向只能更嚴（FJ-4）：規則判成要人時不問模型，也不會降級。
		d.Outcome = OutcomeLocked
		run.Outcome = OutcomeLocked
		s.record(ctx, run)
		return d
	}
	wait := u.Wait
	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	start := s.now()
	var got *Answer
	outcome := OutcomeNotConfigured
	for _, t := range tries {
		if cctx.Err() != nil {
			break
		}
		if s.unsupported(cctx, t.tm) != "" {
			outcome = OutcomeUnsupported // 不呼叫（一定是 400），往下一層退；設定頁會列出這個問題
			continue
		}
		if ok, capped := s.reserve(ctx, set, t.name == Tier2); !ok {
			if capped {
				outcome = OutcomeCapped
				s.capped(ctx, set)
				break
			}
			continue // 第二層到了每日上限：往下一層退
		}
		run.Calls++
		if t.name == Tier2 {
			run.Tier2Calls++
		}
		v, oc := s.ask(cctx, u, a, options, t)
		s.release(t.name == Tier2)
		if oc == OutcomeNotConfigured {
			run.Calls--
			if t.name == Tier2 {
				run.Tier2Calls--
			}
		}
		outcome = oc
		if v == nil && run.Provider == "" && oc != OutcomeNotConfigured {
			run.Tier, run.Provider, run.Model = t.name, t.tm.Provider, t.tm.Model // 記下是哪一層出問題（J10 顯示）
		}
		if v != nil {
			run.Tier, run.Provider, run.Model = t.name, t.tm.Provider, t.tm.Model
			run.ModelLabel, run.P, run.Why = v.Label, v.P, v.Why
			d.Tier, d.Provider, d.ModelID = t.name, t.tm.Provider, t.tm.Model
		}
		if oc == OutcomeOK {
			got = v
			break
		}
	}
	run.LatencyMS = s.now().Sub(start).Milliseconds()
	run.Outcome = outcome
	d.Outcome = outcome
	if got != nil {
		mv := *got
		mv.By = ByModel
		d.Model = &mv
		if d.Mode == ModeLive {
			d.Answer = mv
			d.Shown = u.Shown(mv.Label)
			run.Used = ByModel
		}
	}
	if run.Calls > 0 || outcome == OutcomeCapped {
		s.record(ctx, run)
	}
	return d
}

// ask 問一層模型。回傳合格（或機率不夠）的判斷與結果。
func (s *Service) ask(ctx context.Context, u *Use, a Ask, options []Option, t tierTry) (*Answer, string) {
	body := buildRequest(u, a, options, t.tm.Model)
	status, resp, err := s.Call(ctx, t.tm.Provider, body, "xbot-judge-"+u.ID)
	switch {
	case errors.Is(err, ErrNotConfigured):
		return nil, OutcomeNotConfigured
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil):
		return nil, OutcomeTimeout
	case err != nil:
		s.log().Info("快判：呼叫模型失敗，退回規則", "use", u.ID, "tier", t.name, "err", err)
		return nil, OutcomeError
	case noFundsRe.Match(resp) && (status >= 400 || parseOK(resp) != nil):
		s.log().Info("快判：供應商餘額不足，退回規則", "use", u.ID, "tier", t.name, "provider", t.tm.Provider)
		return nil, OutcomeNoFunds
	case status == http.StatusTooManyRequests:
		return nil, OutcomeRateLimited
	case status >= 400:
		s.log().Info("快判：模型回錯誤，退回規則", "use", u.ID, "tier", t.name, "status", status)
		return nil, OutcomeError
	}
	v, err := parseReply(resp)
	if err != nil {
		return nil, OutcomeInvalid
	}
	v, oc := accept(u, options, v)
	if oc == OutcomeInvalid {
		return nil, oc
	}
	return &v, oc
}

// unsupported 回傳模型不能用 chat/completions 時它的呼叫方式（例：codex_responses）；能用或不知道時回空白。
func (s *Service) unsupported(ctx context.Context, tm TierModel) string {
	if s.Transport == nil || !tm.On() {
		return ""
	}
	t, err := s.Transport(ctx, tm.Provider, tm.Model)
	if err != nil || t == "" || t == transportChat {
		return ""
	}
	return t
}

// unsupportedText 是介面上的白話：哪個模型、只支援什麼、改用什麼。
func unsupportedText(tm TierModel, transport string) string {
	name := transport
	switch transport {
	case "codex_responses":
		name = "Responses API"
	case "anthropic_messages":
		name = "Anthropic Messages API"
	}
	return tm.Model + " 只支援 " + name + "，快判只能用支援 chat/completions 的模型（例：" + DefaultTier1.Model + "）"
}

// reserve 檢查每日上限並預留一次呼叫。capped=true 表示全部的每日上限到了；只有第二層到了時 ok=false、capped=false。
func (s *Service) reserve(ctx context.Context, set Settings, tier2 bool) (ok, capped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	day, _ := s.DayStart()
	calls, t2, err := s.DB.JudgeCalls(ctx, day)
	if err != nil {
		s.log().Warn("快判：讀不到今天的呼叫次數，先不呼叫", "err", err)
		return false, true
	}
	if calls+s.inflight >= set.DailyCap {
		return false, true
	}
	if tier2 && t2+s.inflight2 >= set.Tier2Cap {
		return false, false
	}
	s.inflight++
	if tier2 {
		s.inflight2++
	}
	return true, false
}

func (s *Service) release(tier2 bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight--
	if tier2 {
		s.inflight2--
	}
}

func (s *Service) record(ctx context.Context, r store.JudgeRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.CreatedAt == 0 {
		r.CreatedAt = s.now().UnixMilli()
	}
	if _, err := s.DB.AddJudgeRun(context.WithoutCancel(ctx), r); err != nil {
		s.log().Warn("快判：寫入紀錄失敗", "use", r.Use, "err", err)
	}
}

// capped 在今天第一次到每日上限時發一張通知卡（FJ-6：達上限自動退回規則並通知一次）。
func (s *Service) capped(ctx context.Context, set Settings) {
	ctx = context.WithoutCancel(ctx)
	_, day := s.DayStart()
	ref := "judge.cap." + day
	if exists, err := s.DB.InboxRefExists(ctx, ref); err != nil || exists {
		return
	}
	raw, _ := json.Marshal(map[string]any{"daily_cap": set.DailyCap})
	it, err := s.DB.CreateInboxItem(ctx, store.InboxItem{Kind: "error", Ref: ref, Title: "快判今天已達上限，先改用規則",
		Body:   fmt.Sprintf("今天已經呼叫 %d 次快判模型，到了每日上限。明天自動恢復；要調整到 設定 → AI 模型 → 快判模型。", set.DailyCap),
		Status: store.InboxResolved, Resolution: "info", Payload: raw})
	if err != nil {
		s.log().Warn("快判：發每日上限通知失敗", "err", err)
		return
	}
	e, err := s.DB.AppendEvent(ctx, notify.Event(notify.KindCreated, it))
	if err == nil && s.Notify != nil {
		s.Notify(e)
	}
}

// Prune 刪掉太舊的快判紀錄（limits.JudgeRunsKeepDays）。
func (s *Service) Prune(ctx context.Context) error {
	return s.DB.PruneJudgeRuns(ctx, s.now().AddDate(0, 0, -limits.JudgeRunsKeepDays).UnixMilli())
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// parseOK 回答回應是不是合格的模型回答（nil 表示是）。
func parseOK(resp []byte) error {
	_, err := parseReply(resp)
	return err
}
