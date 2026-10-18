package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"xbot/internal/notify"
	"xbot/internal/store"
)

// GlobalCapsSetting 是全域花費上限在 setting 表的 key（PRD US-2：全域月上限）。
const GlobalCapsSetting = "budget.global"

// ProviderCapsPrefix 是每個供應商花費上限的 key 開頭：budget.provider.<id>（US-3：一把金鑰的額度省著用）。
const ProviderCapsPrefix = "budget.provider."

// Caps 是花費上限（百萬分之一美元）；0 表示不限。
// 每個 Bot 的上限存在 bot.budget，全域的存在 setting（只有月上限）。
type Caps struct {
	DailyMicros   int64 `json:"daily_micros,omitempty"`
	MonthlyMicros int64 `json:"monthly_micros,omitempty"`
}

// 超過哪一種上限。
const (
	ScopeBotDaily        = "bot_daily"
	ScopeBotMonthly      = "bot_monthly"
	ScopeGlobalMonthly   = "global_monthly"
	ScopeProviderDaily   = "provider_daily"
	ScopeProviderMonthly = "provider_monthly"
)

// BudgetError 表示已經超過花費上限，不能再呼叫模型（PRD §6.13）。
type BudgetError struct {
	Scope       string `json:"scope"` // bot_daily / bot_monthly / global_monthly / provider_daily / provider_monthly
	BotID       string `json:"bot_id,omitempty"`
	Provider    string `json:"provider,omitempty"` // provider_* 時是哪個供應商
	BotName     string `json:"bot_name,omitempty"`
	LimitMicros int64  `json:"limit_micros"`
	SpentMicros int64  `json:"spent_micros"`
	Period      string `json:"period"` // 2026-10-18（日）或 2026-10（月）
}

func (e *BudgetError) Error() string {
	return e.Title() + "：已花 " + USD(e.SpentMicros) + "，上限 " + USD(e.LimitMicros) + "。要繼續請提高上限。"
}

// Title 是給人看的一句話。
func (e *BudgetError) Title() string {
	who := e.BotName
	if who == "" {
		who = "這個 Bot"
	}
	switch e.Scope {
	case ScopeBotDaily:
		return who + " 超過今天的花費上限"
	case ScopeBotMonthly:
		return who + " 超過這個月的花費上限"
	case ScopeProviderDaily:
		return "模型供應商 " + e.Provider + " 超過今天的花費上限"
	case ScopeProviderMonthly:
		return "模型供應商 " + e.Provider + " 超過這個月的花費上限"
	}
	return "所有 Bot 合計超過這個月的花費上限"
}

// USD 把百萬分之一美元印成 $1.23。
func USD(micros int64) string {
	return fmt.Sprintf("$%.2f", float64(micros)/1e6)
}

// Spend 是某個範圍目前的花費與上限。
type Spend struct {
	DayMicros   int64
	MonthMicros int64
}

// Budgets 檢查花費上限、在超過時發「待我處理」卡片（同一個上限、同一期只發一次）。
type Budgets struct {
	DB *store.DB
	// Now、Location 決定「今天」「這個月」怎麼切；預設現在時間、本機時區。
	Now      func() time.Time
	Location *time.Location
	// Notify 在卡片寫進資料庫後呼叫（SSE 用）；可為 nil。
	Notify func(store.Event)
	// OnExceeded 在第一次發現超過上限時呼叫（停下 Bot 正在做的工作）；可為 nil。不能阻塞太久。
	OnExceeded func(e *BudgetError)

	mu sync.Mutex
}

func (b *Budgets) now() time.Time {
	t := time.Now()
	if b.Now != nil {
		t = b.Now()
	}
	if b.Location != nil {
		t = t.In(b.Location)
	}
	return t
}

// Periods 回傳今天與這個月的開始時間（毫秒）以及期別字串。
func (b *Budgets) Periods() (dayStart, monthStart int64, day, month string) {
	t := b.now()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	m := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	return d.UnixMilli(), m.UnixMilli(), d.Format("2006-01-02"), m.Format("2006-01")
}

// BotCaps 讀一個 Bot 的花費上限；Bot 不存在時回空的上限。
func (b *Budgets) BotCaps(ctx context.Context, botID string) (Caps, string, error) {
	bot, err := b.DB.GetBot(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return Caps{}, "", nil
	}
	if err != nil {
		return Caps{}, "", err
	}
	return ParseCaps(bot.Budget), bot.Name, nil
}

// ParseCaps 解開 bot.budget；格式不對當成沒有上限。
func ParseCaps(raw json.RawMessage) Caps {
	var c Caps
	_ = json.Unmarshal(raw, &c)
	c.DailyMicros, c.MonthlyMicros = max(c.DailyMicros, 0), max(c.MonthlyMicros, 0)
	return c
}

// GlobalCaps 讀全域花費上限。
func (b *Budgets) GlobalCaps(ctx context.Context) (Caps, error) {
	var c Caps
	_, err := b.DB.GetSettingInto(ctx, GlobalCapsSetting, &c)
	return c, err
}

// ProviderCaps 讀一個供應商的花費上限。
func (b *Budgets) ProviderCaps(ctx context.Context, providerID string) (Caps, error) {
	var c Caps
	_, err := b.DB.GetSettingInto(ctx, ProviderCapsPrefix+providerID, &c)
	return c, err
}

// Spent 回傳符合條件（Bot、供應商，空白表示不限）今天與這個月的花費。
func (b *Budgets) Spent(ctx context.Context, botID, providerID string) (Spend, error) {
	dayStart, monthStart, _, _ := b.Periods()
	month, err := b.DB.SumUsage(ctx, store.UsageFilter{BotID: botID, Provider: providerID, Since: monthStart})
	if err != nil {
		return Spend{}, err
	}
	day, err := b.DB.SumUsage(ctx, store.UsageFilter{BotID: botID, Provider: providerID, Since: dayStart})
	if err != nil {
		return Spend{}, err
	}
	return Spend{DayMicros: day.CostMicros, MonthMicros: month.CostMicros}, nil
}

// CheckProvider 回答這個供應商今天、這個月還能不能用；超過上限時回 *BudgetError（BotID 是觸發的 Bot）。
func (b *Budgets) CheckProvider(ctx context.Context, botID, providerID string) error {
	caps, err := b.ProviderCaps(ctx, providerID)
	if err != nil || (caps.DailyMicros == 0 && caps.MonthlyMicros == 0) {
		return err
	}
	_, _, day, month := b.Periods()
	s, err := b.Spent(ctx, "", providerID)
	if err != nil {
		return err
	}
	_, name, err := b.BotCaps(ctx, botID)
	if err != nil {
		return err
	}
	if caps.DailyMicros > 0 && s.DayMicros >= caps.DailyMicros {
		return &BudgetError{Scope: ScopeProviderDaily, BotID: botID, BotName: name, Provider: providerID, LimitMicros: caps.DailyMicros, SpentMicros: s.DayMicros, Period: day}
	}
	if caps.MonthlyMicros > 0 && s.MonthMicros >= caps.MonthlyMicros {
		return &BudgetError{Scope: ScopeProviderMonthly, BotID: botID, BotName: name, Provider: providerID, LimitMicros: caps.MonthlyMicros, SpentMicros: s.MonthMicros, Period: month}
	}
	return nil
}

// BotSpend 回傳一個 Bot（botID 空白表示全部）今天與這個月的花費。
func (b *Budgets) BotSpend(ctx context.Context, botID string) (Spend, error) {
	return b.Spent(ctx, botID, "")
}

// Check 回答這個 Bot 現在能不能再呼叫模型；超過上限時回 *BudgetError。
// 上限是「到了就停」：花費大於或等於上限就擋。
func (b *Budgets) Check(ctx context.Context, botID string) error {
	_, _, day, month := b.Periods()
	caps, name, err := b.BotCaps(ctx, botID)
	if err != nil {
		return err
	}
	global, err := b.GlobalCaps(ctx)
	if err != nil {
		return err
	}
	if caps.DailyMicros > 0 || caps.MonthlyMicros > 0 {
		s, err := b.BotSpend(ctx, botID)
		if err != nil {
			return err
		}
		if caps.DailyMicros > 0 && s.DayMicros >= caps.DailyMicros {
			return &BudgetError{Scope: ScopeBotDaily, BotID: botID, BotName: name, LimitMicros: caps.DailyMicros, SpentMicros: s.DayMicros, Period: day}
		}
		if caps.MonthlyMicros > 0 && s.MonthMicros >= caps.MonthlyMicros {
			return &BudgetError{Scope: ScopeBotMonthly, BotID: botID, BotName: name, LimitMicros: caps.MonthlyMicros, SpentMicros: s.MonthMicros, Period: month}
		}
	}
	if global.MonthlyMicros > 0 {
		s, err := b.BotSpend(ctx, "")
		if err != nil {
			return err
		}
		if s.MonthMicros >= global.MonthlyMicros {
			return &BudgetError{Scope: ScopeGlobalMonthly, BotID: botID, BotName: name, LimitMicros: global.MonthlyMicros, SpentMicros: s.MonthMicros, Period: month}
		}
	}
	return nil
}

// cardRef 是同一張花費上限卡的識別：同一個範圍、同一期、同一個上限只發一次（上限改了再超過會再發）。
func (e *BudgetError) cardRef() string {
	who := e.BotID
	switch e.Scope {
	case ScopeGlobalMonthly:
		who = "all"
	case ScopeProviderDaily, ScopeProviderMonthly:
		who = "provider/" + e.Provider
	}
	return fmt.Sprintf("budget:%s:%s:%s:%d", e.Scope, who, e.Period, e.LimitMicros)
}

// Exceeded 處理「超過上限」：第一次時發待我處理卡片並呼叫 OnExceeded；之後再遇到同一件事什麼都不做。
// 回傳這次有沒有發卡。
func (b *Budgets) Exceeded(ctx context.Context, e *BudgetError) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ref := e.cardRef()
	exists, err := b.DB.InboxRefExists(ctx, ref)
	if err != nil || exists {
		return false, err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	cardBot := e.BotID
	if cardBot != "" {
		// Bot 剛被刪掉：卡片記成全域的（inbox_item.bot_id 有外鍵）。
		if _, err := b.DB.GetBot(ctx, cardBot); errors.Is(err, store.ErrNotFound) {
			cardBot = ""
		} else if err != nil {
			return false, err
		}
	}
	var ev, card store.Event
	err = b.DB.Tx(ctx, func(q *store.Queries) error {
		item, err := q.CreateInboxItem(ctx, store.InboxItem{Kind: "budget", BotID: cardBot, Ref: ref, Title: e.Title(),
			Body:    "已花 " + USD(e.SpentMicros) + "，上限 " + USD(e.LimitMicros) + "。工作已經停下。要繼續請提高上限，或等下一期。",
			Payload: payload})
		if err != nil {
			return err
		}
		// 待我處理卡片的通用事件（C12，徽章與通知中心用）。
		if card, err = q.AppendEvent(ctx, notify.Event(notify.KindCreated, item)); err != nil {
			return err
		}
		ep, err := json.Marshal(map[string]any{"inbox_id": item.ID, "budget": e})
		if err != nil {
			return err
		}
		ev, err = q.AppendEvent(ctx, store.Event{BotID: cardBot, Kind: EventBudgetExceeded, Payload: ep})
		return err
	})
	if err != nil {
		return false, err
	}
	if b.Notify != nil {
		b.Notify(card)
		b.Notify(ev)
	}
	if b.OnExceeded != nil {
		b.OnExceeded(e)
	}
	return true, nil
}

// EventBudgetExceeded 是超過花費上限時寫進 event 表的事件。
const EventBudgetExceeded = "budget.exceeded"
