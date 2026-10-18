// Package usage 是用量與花費的操作（PRD §6.13 US-1、US-2、US-3）：
// usage.summary、usage.export、usage.caps、usage.set_caps。
// 用量由 Model Proxy 逐筆記在 usage_entry；花費以百萬分之一美元記，對外同時給美元。
package usage

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// Deps 是用量操作需要的服務元件；Budgets 是 nil 時（指令列只產生說明）呼叫會回 internal。
type Deps struct {
	Budgets *proxy.Budgets
}

// ── 輸入輸出 ──────────────────────────────────────────

// RangeIn 是用量查詢的範圍。since／until 是毫秒時間；都沒給時看最近 days 天（預設 30）。
type RangeIn struct {
	Days     int    `json:"days,omitempty" min:"1" max:"365" desc:"看最近幾天（含今天）；預設 30"`
	Since    int64  `json:"since,omitempty" min:"0" desc:"從這個時間開始（毫秒）；給了就不看 days"`
	Until    int64  `json:"until,omitempty" min:"0" desc:"到這個時間之前（毫秒）；預設現在"`
	BotID    string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot"`
	Provider string `json:"provider,omitempty" max:"32" desc:"只看這個模型供應商"`
}

// Row 是一組用量合計。
type Row struct {
	Key              string  `json:"key" desc:"Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18）"`
	Label            string  `json:"label,omitempty" desc:"顯示名稱（Bot 名稱）"`
	Calls            int64   `json:"calls"`
	InputTokens      int64   `json:"input_tokens" desc:"含快取命中與寫入快取"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheHitRate     float64 `json:"cache_hit_rate" desc:"快取命中率：cache_read_tokens ÷ input_tokens（0～1）"`
	CostMicros       int64   `json:"cost_micros" desc:"花費（百萬分之一美元）"`
	CostUSD          float64 `json:"cost_usd"`
	UnpricedCalls    int64   `json:"unpriced_calls" desc:"沒有價格、花費記成 0 的次數"`
	FallbackCalls    int64   `json:"fallback_calls" desc:"改用備援模型的次數"`
}

// SummaryOut 是 xbot.usage.summary 的輸出。
type SummaryOut struct {
	Since      int64                 `json:"since"`
	Until      int64                 `json:"until"`
	Total      Row                   `json:"total"`
	ByBot      []Row                 `json:"by_bot" desc:"花費多到少"`
	ByProvider []Row                 `json:"by_provider" desc:"花費多到少"`
	ByModel    []Row                 `json:"by_model" desc:"花費多到少"`
	ByDay      []Row                 `json:"by_day" desc:"日期舊到新"`
	Unpriced   []store.UnpricedModel `json:"unpriced" desc:"這段期間用過、但沒有價格的模型"`
}

// ExportOut 是 xbot.usage.export 的輸出。
type ExportOut struct {
	Filename  string `json:"filename" desc:"建議的檔名"`
	CSV       string `json:"csv" desc:"CSV（UTF-8，第一列是欄位名稱）"`
	Rows      int    `json:"rows"`
	Truncated bool   `json:"truncated" desc:"超過 100000 筆，只匯出前面的"`
}

// CapsIn 是 xbot.usage.caps 的輸入。
type CapsIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot"`
}

// CapView 是一個範圍的花費上限與目前花費（美元；上限 0 表示不限）。
type CapView struct {
	Key            string  `json:"key" desc:"Bot id、供應商代號；全域是 all"`
	Label          string  `json:"label,omitempty"`
	DailyUSD       float64 `json:"daily_usd" desc:"每日上限；0 表示不限（全域沒有每日上限）"`
	MonthlyUSD     float64 `json:"monthly_usd" desc:"每月上限；0 表示不限"`
	SpentTodayUSD  float64 `json:"spent_today_usd"`
	SpentMonthUSD  float64 `json:"spent_month_usd"`
	Exceeded       bool    `json:"exceeded" desc:"已經到上限，模型請求會被擋下"`
	DailyMicros    int64   `json:"daily_micros"`
	MonthlyMicros  int64   `json:"monthly_micros"`
	SpentDayMicros int64   `json:"spent_day_micros"`
	SpentMonMicros int64   `json:"spent_month_micros"`
}

// CapsOut 是 xbot.usage.caps 的輸出。
type CapsOut struct {
	Day       string    `json:"day" desc:"今天（本機時區）"`
	Month     string    `json:"month"`
	Global    *CapView  `json:"global,omitempty" desc:"全部 Bot 合計（只有月上限）；指定 bot_id 時不出現"`
	Bots      []CapView `json:"bots"`
	Providers []CapView `json:"providers" desc:"有設上限的模型供應商"`
}

// SetCapsIn 是 xbot.usage.set_caps 的輸入。bot_id、provider 都沒給時設全域上限。
// 沒給的欄位不改；0 表示不限。
type SetCapsIn struct {
	BotID      string   `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"設這個 Bot 的上限"`
	Provider   string   `json:"provider,omitempty" max:"32" desc:"設這個模型供應商的上限（所有 Bot 合計；用來省著用某把金鑰的額度）"`
	DailyUSD   *float64 `json:"daily_usd,omitempty" min:"0" max:"1000000" desc:"每日上限（美元）；0 表示不限。全域不能設"`
	MonthlyUSD *float64 `json:"monthly_usd,omitempty" min:"0" max:"1000000" desc:"每月上限（美元）；0 表示不限"`
}

// Register 登錄用量與花費上限的操作。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[RangeIn, SummaryOut]{
		Name: "xbot.usage.summary", Summary: "看用量與花費",
		Description: "依 Bot、供應商、模型、日期合計 token、花費與快取命中率（US-1）。日期用本機時區切。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.summary,
	})
	ops.Register(reg, ops.Op[RangeIn, ExportOut]{
		Name: "xbot.usage.export", Summary: "匯出用量明細（CSV）",
		Description: "每一次模型請求一列。最多 100000 筆。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.export,
	})
	ops.Register(reg, ops.Op[CapsIn, CapsOut]{
		Name: "xbot.usage.caps", Summary: "看花費上限與目前花費",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.caps,
	})
	ops.Register(reg, ops.Op[SetCapsIn, CapView]{
		Name: "xbot.usage.set_caps", Summary: "設定花費上限",
		Description: "到上限時，Bot 的模型請求會被擋下、正在做的工作會停下，並在「待我處理」出現一張卡（US-2）。" +
			"降低上限直接生效；提高或取消上限要你核准，而且不能代為核准。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe, NoDelegate: true,
		RiskOf: d.capsRisk,
		Handle: d.setCaps,
	})
}

func (d Deps) ready() error {
	if d.Budgets == nil || d.Budgets.DB == nil {
		return ops.Internal(errors.New("花費上限沒有啟動"))
	}
	return nil
}

func usd(micros int64) float64 { return float64(micros) / 1e6 }

func micros(v float64) int64 { return int64(math.Round(v * 1e6)) }

func (d Deps) loc() *time.Location {
	if d.Budgets != nil && d.Budgets.Location != nil {
		return d.Budgets.Location
	}
	return time.Local
}

func (d Deps) now() time.Time {
	if d.Budgets != nil && d.Budgets.Now != nil {
		return d.Budgets.Now()
	}
	return time.Now()
}

// filter 把輸入的範圍換成查詢條件。
func (d Deps) filter(in RangeIn) store.UsageFilter {
	f := store.UsageFilter{BotID: in.BotID, Provider: in.Provider, Since: in.Since, Until: in.Until}
	if f.Since == 0 {
		days := in.Days
		if days == 0 {
			days = limits.UsageSummaryDefaultDays
		}
		days = min(days, limits.UsageSummaryMaxDays)
		t := d.now().In(d.loc())
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, -(days - 1))
		f.Since = start.UnixMilli()
	}
	return f
}

func (r *Row) add(u store.UsageEntry) {
	r.Calls++
	r.InputTokens += u.InputTokens
	r.OutputTokens += u.OutputTokens
	r.CacheReadTokens += u.CacheReadTokens
	r.CacheWriteTokens += u.CacheWriteTokens
	r.CostMicros += u.CostMicros
	if u.Unpriced {
		r.UnpricedCalls++
	}
	if u.Fallback {
		r.FallbackCalls++
	}
}

func (r *Row) finish() {
	r.CostUSD = usd(r.CostMicros)
	if r.InputTokens > 0 {
		r.CacheHitRate = math.Round(float64(r.CacheReadTokens)/float64(r.InputTokens)*10000) / 10000
	}
}

func (d Deps) summary(ctx context.Context, call *ops.Call, in RangeIn) (SummaryOut, error) {
	f := d.filter(in)
	out := SummaryOut{Since: f.Since, Until: f.Until, Total: Row{Key: "all"}}
	if out.Until == 0 {
		out.Until = d.now().UnixMilli()
	}
	groups := [4]map[string]*Row{{}, {}, {}, {}}
	loc := d.loc()
	err := call.DB.EachUsage(ctx, f, func(u store.UsageEntry) error {
		if !call.Identity.CanAccess(ops.ResourceBot, u.BotID) {
			return nil
		}
		out.Total.add(u)
		keys := [4]string{u.BotID, u.Provider, u.Provider + "/" + u.Model, time.UnixMilli(u.CreatedAt).In(loc).Format("2006-01-02")}
		for i, k := range keys {
			r := groups[i][k]
			if r == nil {
				r = &Row{Key: k}
				groups[i][k] = r
			}
			r.add(u)
		}
		return nil
	})
	if err != nil {
		return SummaryOut{}, err
	}
	out.Total.finish()
	names, err := botNames(ctx, call.DB)
	if err != nil {
		return SummaryOut{}, err
	}
	list := func(m map[string]*Row, byKey bool) []Row {
		rows := make([]Row, 0, len(m))
		for _, k := range slices.Sorted(maps.Keys(m)) {
			r := m[k]
			r.finish()
			rows = append(rows, *r)
		}
		if !byKey {
			slices.SortStableFunc(rows, func(a, b Row) int { return cmp.Compare(b.CostMicros, a.CostMicros) })
		}
		return rows
	}
	out.ByBot = list(groups[0], false)
	for i := range out.ByBot {
		out.ByBot[i].Label = names[out.ByBot[i].Key]
	}
	out.ByProvider = list(groups[1], false)
	out.ByModel = list(groups[2], false)
	out.ByDay = list(groups[3], true)
	unpriced, err := call.DB.UnpricedModels(ctx, f)
	if err != nil {
		return SummaryOut{}, err
	}
	out.Unpriced = unpriced
	if out.Unpriced == nil {
		out.Unpriced = []store.UnpricedModel{}
	}
	return out, nil
}

func botNames(ctx context.Context, db *store.DB) (map[string]string, error) {
	bots, err := db.ListBots(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(bots))
	for _, b := range bots {
		out[b.ID] = b.Name
	}
	return out, nil
}

var errFull = errors.New("full")

// csvSafe 避免試算表把 =、+、-、@ 開頭的文字當公式執行。
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (d Deps) export(ctx context.Context, call *ops.Call, in RangeIn) (ExportOut, error) {
	f := d.filter(in)
	names, err := botNames(ctx, call.DB)
	if err != nil {
		return ExportOut{}, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"time", "bot_id", "bot_name", "thread_id", "task_id", "engine", "provider", "model", "endpoint",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cost_usd", "unpriced", "fallback"})
	out := ExportOut{}
	loc := d.loc()
	i64 := func(v int64) string { return strconv.FormatInt(v, 10) }
	err = call.DB.EachUsage(ctx, f, func(u store.UsageEntry) error {
		if !call.Identity.CanAccess(ops.ResourceBot, u.BotID) {
			return nil
		}
		if out.Rows >= limits.UsageExportMaxRows {
			out.Truncated = true
			return errFull
		}
		out.Rows++
		return w.Write([]string{time.UnixMilli(u.CreatedAt).In(loc).Format(time.RFC3339), u.BotID, csvSafe(names[u.BotID]),
			u.ThreadID, u.TaskID, u.Engine, csvSafe(u.Provider), csvSafe(u.Model), u.Endpoint,
			i64(u.InputTokens), i64(u.OutputTokens), i64(u.CacheReadTokens), i64(u.CacheWriteTokens),
			strconv.FormatFloat(usd(u.CostMicros), 'f', 6, 64), strconv.FormatBool(u.Unpriced), strconv.FormatBool(u.Fallback)})
	})
	if err != nil && !errors.Is(err, errFull) {
		return ExportOut{}, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return ExportOut{}, err
	}
	out.CSV = buf.String()
	out.Filename = "xbot-usage-" + time.UnixMilli(f.Since).In(loc).Format("20060102") + ".csv"
	return out, nil
}

func view(key, label string, c proxy.Caps, s proxy.Spend) CapView {
	v := CapView{Key: key, Label: label, DailyMicros: c.DailyMicros, MonthlyMicros: c.MonthlyMicros,
		SpentDayMicros: s.DayMicros, SpentMonMicros: s.MonthMicros}
	v.DailyUSD, v.MonthlyUSD = usd(c.DailyMicros), usd(c.MonthlyMicros)
	v.SpentTodayUSD, v.SpentMonthUSD = usd(s.DayMicros), usd(s.MonthMicros)
	v.Exceeded = (c.DailyMicros > 0 && s.DayMicros >= c.DailyMicros) || (c.MonthlyMicros > 0 && s.MonthMicros >= c.MonthlyMicros)
	return v
}

func (d Deps) caps(ctx context.Context, call *ops.Call, in CapsIn) (CapsOut, error) {
	if err := d.ready(); err != nil {
		return CapsOut{}, err
	}
	b := d.Budgets
	_, _, day, month := b.Periods()
	out := CapsOut{Day: day, Month: month, Bots: []CapView{}, Providers: []CapView{}}
	bots, err := call.DB.ListBots(ctx, false)
	if err != nil {
		return CapsOut{}, err
	}
	for _, bot := range bots {
		if (in.BotID != "" && bot.ID != in.BotID) || !call.Identity.CanAccess(ops.ResourceBot, bot.ID) {
			continue
		}
		s, err := b.BotSpend(ctx, bot.ID)
		if err != nil {
			return CapsOut{}, err
		}
		out.Bots = append(out.Bots, view(bot.ID, bot.Name, proxy.ParseCaps(bot.Budget), s))
	}
	if in.BotID != "" {
		return out, nil
	}
	g, err := b.GlobalCaps(ctx)
	if err != nil {
		return CapsOut{}, err
	}
	s, err := b.BotSpend(ctx, "")
	if err != nil {
		return CapsOut{}, err
	}
	gv := view("all", "全部 Bot", g, s)
	out.Global = &gv
	settings, err := call.DB.ListSettings(ctx)
	if err != nil {
		return CapsOut{}, err
	}
	for _, k := range slices.Sorted(maps.Keys(settings)) {
		id, ok := strings.CutPrefix(k, proxy.ProviderCapsPrefix)
		if !ok {
			continue
		}
		c, err := b.ProviderCaps(ctx, id)
		if err != nil {
			return CapsOut{}, err
		}
		s, err := b.Spent(ctx, "", id)
		if err != nil {
			return CapsOut{}, err
		}
		out.Providers = append(out.Providers, view(id, "", c, s))
	}
	return out, nil
}

// current 讀出要改的那個範圍目前的上限。
func (d Deps) current(ctx context.Context, in SetCapsIn) (proxy.Caps, error) {
	switch {
	case in.BotID != "":
		bot, err := d.Budgets.DB.GetBot(ctx, in.BotID)
		if err != nil {
			return proxy.Caps{}, err
		}
		return proxy.ParseCaps(bot.Budget), nil
	case in.Provider != "":
		return d.Budgets.ProviderCaps(ctx, in.Provider)
	}
	return d.Budgets.GlobalCaps(ctx)
}

// raises 回答新的上限是不是比舊的寬（提高或取消上限）。舊的是 0（不限）時，設任何上限都是收緊。
func raises(old int64, v *float64) bool {
	if v == nil || old == 0 {
		return false
	}
	n := micros(*v)
	return n == 0 || n > old
}

// capsRisk：降低上限是 safe；提高或取消上限要核准（AGENTS.md：提高花費上限永遠不能代為核准）。讀不到現況就當成要核准。
func (d Deps) capsRisk(in SetCapsIn) ops.Risk {
	if d.ready() != nil {
		return ops.RiskConfirm
	}
	cur, err := d.current(context.Background(), in)
	if err != nil {
		return ops.RiskConfirm
	}
	if raises(cur.DailyMicros, in.DailyUSD) || raises(cur.MonthlyMicros, in.MonthlyUSD) {
		return ops.RiskConfirm
	}
	return ops.RiskSafe
}

func (d Deps) setCaps(ctx context.Context, call *ops.Call, in SetCapsIn) (CapView, error) {
	if err := d.ready(); err != nil {
		return CapView{}, err
	}
	if in.BotID != "" && in.Provider != "" {
		return CapView{}, ops.InvalidInput(ops.FieldError{Field: "provider", Message: "bot_id 和 provider 只能給一個"})
	}
	if in.BotID == "" && in.Provider == "" {
		if in.DailyUSD != nil {
			return CapView{}, ops.InvalidInput(ops.FieldError{Field: "daily_usd", Message: "全域只有每月上限"})
		}
		if call.Identity.Resources.Bots != nil && call.Identity.Kind != ops.KindOwner {
			return CapView{}, ops.Forbidden("只能碰部分 Bot 的代理不能改全域上限")
		}
	}
	if in.Provider != "" && !proxy.ValidProviderID(in.Provider) {
		return CapView{}, ops.InvalidInput(ops.FieldError{Field: "provider", Message: "供應商代號不對"})
	}
	if in.DailyUSD == nil && in.MonthlyUSD == nil {
		return CapView{}, ops.InvalidInput(ops.FieldError{Field: "monthly_usd", Message: "至少給一個上限"})
	}
	var c proxy.Caps
	var label string
	var save func(q *store.DB) error
	key := "all"
	switch {
	case in.BotID != "":
		bot, err := call.DB.GetBot(ctx, in.BotID)
		if err != nil {
			return CapView{}, err
		}
		c, label, key = proxy.ParseCaps(bot.Budget), bot.Name, bot.ID
		apply(&c, in)
		save = func(db *store.DB) error {
			return db.Tx(ctx, func(q *store.Queries) error {
				b, err := q.GetBot(ctx, in.BotID)
				if err != nil {
					return err
				}
				b.Budget = mergeBudget(b.Budget, c)
				_, err = q.UpdateBot(ctx, b)
				return err
			})
		}
	case in.Provider != "":
		cur, err := (&proxy.Budgets{DB: call.DB}).ProviderCaps(ctx, in.Provider)
		if err != nil {
			return CapView{}, err
		}
		c, key = cur, in.Provider
		apply(&c, in)
		save = func(db *store.DB) error {
			if c == (proxy.Caps{}) {
				return db.DeleteSetting(ctx, proxy.ProviderCapsPrefix+in.Provider)
			}
			return db.SetSetting(ctx, proxy.ProviderCapsPrefix+in.Provider, c)
		}
	default:
		cur, err := (&proxy.Budgets{DB: call.DB}).GlobalCaps(ctx)
		if err != nil {
			return CapView{}, err
		}
		c, label = cur, "全部 Bot"
		apply(&c, in)
		save = func(db *store.DB) error { return db.SetSetting(ctx, proxy.GlobalCapsSetting, c) }
	}
	if !call.DryRun {
		if err := save(call.DB); err != nil {
			return CapView{}, err
		}
	}
	s, err := d.Budgets.Spent(ctx, in.BotID, in.Provider)
	if err != nil {
		return CapView{}, err
	}
	return view(key, label, c, s), nil
}

func apply(c *proxy.Caps, in SetCapsIn) {
	if in.DailyUSD != nil {
		c.DailyMicros = micros(*in.DailyUSD)
	}
	if in.MonthlyUSD != nil {
		c.MonthlyMicros = micros(*in.MonthlyUSD)
	}
}

// mergeBudget 只改 bot.budget 裡的上限欄位，其他欄位（之後的卡可能加）保留。
func mergeBudget(raw []byte, c proxy.Caps) []byte {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{}
	}
	delete(m, "daily_micros")
	delete(m, "monthly_micros")
	if c.DailyMicros > 0 {
		m["daily_micros"] = c.DailyMicros
	}
	if c.MonthlyMicros > 0 {
		m["monthly_micros"] = c.MonthlyMicros
	}
	b, _ := json.Marshal(m)
	return b
}
