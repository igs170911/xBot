// Package delegation 是代為核准（PRD MCP-10，design/J12、C16、F08）：
// 使用者可以授權某個代理代為按「允許」，逐項設定類別、每日次數與金額上限、有效期限。預設關閉。
//
// 永遠不能代為核准的（操作設 NoDelegate）：刪除 Bot 或資料、移除主機、提高花費上限、新增或修改代為核准授權本身。
// 每次代為核准都在「待我處理」留一張「已由 ○○ 代為核准」紀錄卡；動作可以復原的話，10 分鐘內可以按「復原」（internal/inbox）。
package delegation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Never 是永遠要你本人核准的動作（design/J12「永遠要你本人核准」）。
var Never = []string{"刪除 Bot 或資料", "移除主機", "提高花費上限", "改代為核准授權"}

// clock 是現在時間（測試可以換）。
var clock = time.Now

// ── 輸入輸出 ──────────────────────────────────────────

// SetIn 是 xbot.agents.set_delegation 的輸入。
type SetIn struct {
	ID      string `json:"id" required:"true" max:"64" desc:"代理 id（xbot.agents.list 的 id）"`
	Enabled bool   `json:"enabled" desc:"true：授權代為核准；false：收回（預設關閉）"`
	// 下面幾項 enabled=true 時才需要。
	Categories        []ops.Category `json:"categories,omitempty" max:"4" desc:"哪些類別：external_send（對外發送）、spend_small（小額花費）、publish（上架）、sign（簽約）"`
	DailyCount        int            `json:"daily_count,omitempty" min:"0" max:"100" desc:"每天最多代為核准幾次"`
	DailyAmountMicros int64          `json:"daily_amount_micros,omitempty" min:"0" max:"1000000000" desc:"每天最多核准多少錢（美元百萬分之一）；只算帶金額的動作，0 表示帶金額的一律要你本人"`
	Days              int            `json:"days,omitempty" min:"0" max:"30" desc:"有效幾天（1、7、30）"`
}

// AgentIn 指定一個代理。
type AgentIn struct {
	ID string `json:"id" required:"true" max:"64" desc:"代理 id；代理查自己的授權時填自己的 id"`
}

// View 是一個代理的代為核准授權與今天用了多少。
type View struct {
	AgentID           string         `json:"agent_id"`
	Enabled           bool           `json:"enabled" desc:"有授權、而且還沒過期"`
	Expired           bool           `json:"expired,omitempty" desc:"授權過，但已經過期"`
	Categories        []ops.Category `json:"categories"`
	DailyCount        int            `json:"daily_count"`
	DailyAmountMicros int64          `json:"daily_amount_micros"`
	ExpiresAt         int64          `json:"expires_at" desc:"到期時間（Unix 毫秒）；0 表示沒有授權"`
	UsedToday         int            `json:"used_today" desc:"今天代為核准了幾次"`
	AmountToday       int64          `json:"amount_today_micros" desc:"今天代為核准了多少錢（美元百萬分之一）"`
	Never             []string       `json:"never" desc:"永遠要你本人核准的動作"`
}

// Register 登錄代為核准的操作：xbot.agents.set_delegation、xbot.agents.get_delegation。
func Register(reg *ops.Registry) {
	ops.Register(reg, ops.Op[SetIn, View]{
		Name:    "xbot.agents.set_delegation",
		Summary: "設定一個代理的代為核准授權",
		Description: "授權後這個代理可以用 xbot.approvals.decide 代你允許指定類別的動作（每日次數、金額、有效期限內）。" +
			"刪除 Bot 或資料、移除主機、提高花費上限、改代為核准授權永遠要你本人。只有你本人能設定。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe, NoDelegate: true,
		Handle: set,
	})
	ops.Register(reg, ops.Op[AgentIn, View]{
		Name:        "xbot.agents.get_delegation",
		Summary:     "看一個代理的代為核准授權",
		Description: "代理可以查自己的授權（id 填自己的），看還能代為核准幾次。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: get,
	})
}

func set(ctx context.Context, call *ops.Call, in SetIn) (View, error) {
	if call.Identity.Kind != ops.KindOwner {
		return View{}, ops.Forbidden("只有你本人能設定代為核准")
	}
	if _, err := call.DB.GetAgentToken(ctx, in.ID); err != nil {
		return View{}, notFoundAgent(err)
	}
	if !in.Enabled {
		if !call.DryRun {
			if err := call.DB.DeleteDelegation(ctx, in.ID); err != nil {
				return View{}, err
			}
		}
		return view(ctx, call.DB, in.ID, nil)
	}
	var fields []ops.FieldError
	cats := []string{}
	for _, c := range ops.Categories { // 依畫面順序、去掉重複
		if slices.Contains(in.Categories, ops.Category(c)) {
			cats = append(cats, c)
		}
	}
	if len(cats) == 0 {
		fields = append(fields, ops.FieldError{Field: "categories", Message: "至少勾一個類別"})
	}
	if in.DailyCount < 1 {
		fields = append(fields, ops.FieldError{Field: "daily_count", Message: "每天至少 1 次"})
	}
	if in.Days < 1 {
		fields = append(fields, ops.FieldError{Field: "days", Message: fmt.Sprintf("有效期限要 1～%d 天", limits.DelegationMaxDays)})
	}
	if len(fields) > 0 {
		return View{}, ops.InvalidInput(fields...)
	}
	d := store.Delegation{AgentID: in.ID, Categories: cats, DailyCount: in.DailyCount, DailyAmountMicros: in.DailyAmountMicros,
		ExpiresAt: clock().Add(time.Duration(in.Days) * 24 * time.Hour).UnixMilli()}
	if call.DryRun {
		return view(ctx, call.DB, in.ID, &d)
	}
	if _, err := call.DB.PutDelegation(ctx, d); err != nil {
		return View{}, err
	}
	return view(ctx, call.DB, in.ID, nil)
}

func get(ctx context.Context, call *ops.Call, in AgentIn) (View, error) {
	switch call.Identity.Kind {
	case ops.KindOwner:
	case ops.KindAgent:
		if in.ID != call.Identity.ID {
			return View{}, ops.Forbidden("代理只能看自己的代為核准授權")
		}
	default:
		return View{}, ops.Forbidden("Bot 沒有代為核准授權")
	}
	if _, err := call.DB.GetAgentToken(ctx, in.ID); err != nil {
		return View{}, notFoundAgent(err)
	}
	return view(ctx, call.DB, in.ID, nil)
}

// Get 讀一個代理的授權與今天的用量（agents.list 顯示「代為核准」標籤用）。
func Get(ctx context.Context, db *store.DB, agentID string) (View, error) {
	return view(ctx, db, agentID, nil)
}

func view(ctx context.Context, db *store.DB, agentID string, draft *store.Delegation) (View, error) {
	v := View{AgentID: agentID, Categories: []ops.Category{}, Never: Never}
	var d store.Delegation
	if draft != nil {
		d = *draft
	} else {
		var err error
		d, err = db.GetDelegation(ctx, agentID)
		if errors.Is(err, store.ErrNotFound) {
			return v, nil
		}
		if err != nil {
			return v, err
		}
	}
	now := clock()
	for _, c := range d.Categories {
		v.Categories = append(v.Categories, ops.Category(c))
	}
	v.DailyCount, v.DailyAmountMicros, v.ExpiresAt = d.DailyCount, d.DailyAmountMicros, d.ExpiresAt
	v.Expired = d.ExpiresAt <= now.UnixMilli()
	v.Enabled = !v.Expired
	n, amt, err := db.DelegationUsedSince(ctx, agentID, store.DayStart(now))
	v.UsedToday, v.AmountToday = n, amt
	return v, err
}

func notFoundAgent(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ops.NotFound("找不到這個代理")
	}
	return err
}

// ── 代為核准 ─────────────────────────────────────────

// Use 是一次成功的代為核准（給紀錄卡顯示「今天第 n／N 次」）。
type Use struct {
	Category     string
	AmountMicros int64
	Count        int // 今天第幾次（含這次）
	Limit        int // 每天上限
}

// Covers 回答這個身分有沒有有效的授權可以代為核准這種動作（approvals.list／wait 讓代理看得到要代按的核准）。
func Covers(ctx context.Context, db *store.DB, id ops.Identity, info ops.Info, botID string) bool {
	if id.Kind != ops.KindAgent || !info.Delegable() || (botID != "" && !id.CanAccess(ops.ResourceBot, botID)) {
		return false
	}
	d, err := db.GetDelegation(ctx, id.ID)
	return err == nil && d.ExpiresAt > clock().UnixMilli() && slices.Contains(d.Categories, info.Category)
}

// Authorize 在交易 q 裡檢查 id 能不能代為核准 a（操作是 info、金額是 amount），可以的話記下這次（計入每日上限）。
// 檢查和記錄在同一個交易裡（寫入只有一條連線、交易一開始就拿寫入鎖），同時兩個請求也不會超過上限。
// record 是 false 時只檢查、不記錄（預演用）。
func Authorize(ctx context.Context, q *store.Queries, id ops.Identity, info ops.Info, a store.Approval, amount int64, record bool) (Use, error) {
	if id.Kind != ops.KindAgent {
		return Use{}, ops.Forbidden("只有你本人能核准；代理請用 xbot.approvals.wait 等結果")
	}
	if info.NoDelegate {
		return Use{}, ops.Forbidden("這個動作永遠要你本人核准（刪除 Bot 或資料、移除主機、提高花費上限、改代為核准授權）；請用 xbot.approvals.wait 等結果")
	}
	if info.Category == "" {
		return Use{}, ops.Forbidden("這個動作不能代為核准，要你本人決定；請用 xbot.approvals.wait 等結果")
	}
	d, err := q.GetDelegation(ctx, id.ID)
	if errors.Is(err, store.ErrNotFound) {
		return Use{}, ops.Forbidden(id.DisplayName() + " 沒有代為核准授權；請用 xbot.approvals.wait 等你本人決定")
	}
	if err != nil {
		return Use{}, err
	}
	now := clock()
	if d.ExpiresAt <= now.UnixMilli() {
		return Use{}, ops.Forbidden("代為核准授權已經過期；請用 xbot.approvals.wait 等你本人決定")
	}
	name := ops.CategoryNames[info.Category]
	if !slices.Contains(d.Categories, info.Category) {
		return Use{}, ops.Forbidden(fmt.Sprintf("代為核准授權不包含「%s」；請用 xbot.approvals.wait 等你本人決定", name))
	}
	if a.BotID != "" && !id.CanAccess(ops.ResourceBot, a.BotID) {
		return Use{}, ops.Forbidden(id.DisplayName() + " 不能碰這個 Bot，不能代為核准")
	}
	n, sum, err := q.DelegationUsedSince(ctx, id.ID, store.DayStart(now))
	if err != nil {
		return Use{}, err
	}
	if n >= d.DailyCount {
		return Use{}, ops.Limit(fmt.Sprintf("今天已經代為核准 %d 次（上限 %d 次），其他的請等你本人決定", n, d.DailyCount))
	}
	if info.HasAmount && amount > 0 && sum+amount > d.DailyAmountMicros {
		return Use{}, ops.Limit(fmt.Sprintf("超過代為核准的每日金額上限（今天已核准 %s，上限 %s），請等你本人決定",
			USD(sum), USD(d.DailyAmountMicros)))
	}
	if !record {
		return Use{Category: info.Category, AmountMicros: amount, Count: n + 1, Limit: d.DailyCount}, nil
	}
	if _, err := q.AddDelegationUse(ctx, store.DelegationUse{ApprovalID: a.ID, AgentID: id.ID, Op: info.Name,
		Category: info.Category, AmountMicros: amount}); err != nil {
		return Use{}, err
	}
	return Use{Category: info.Category, AmountMicros: amount, Count: n + 1, Limit: d.DailyCount}, nil
}

// USD 把美元百萬分之一排成 US$1.50。
func USD(micros int64) string {
	return fmt.Sprintf("US$%.2f", float64(micros)/1e6)
}
