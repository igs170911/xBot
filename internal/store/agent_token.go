package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// UpdateAgentTokenScopes 改代理權杖的權限範圍與資源範圍（xbot.agents.update_scopes）。
// resources 是 nil 時保留原本的資源範圍。
func (q *Queries) UpdateAgentTokenScopes(ctx context.Context, id string, scopes []string, resources json.RawMessage) error {
	if scopes == nil {
		scopes = []string{}
	}
	if resources == nil {
		return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET scopes=?, updated_at=? WHERE id=?`,
			strList(scopes), nowMS(), id))
	}
	return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET scopes=?, resources=?, updated_at=? WHERE id=?`,
		strList(scopes), jsonOr(resources, "{}"), nowMS(), id))
}

// SetAgentTokenLimits 改代理的上限（MCP-12）：每分鐘呼叫、每日呼叫、每日花費（美元百萬分之一）。
func (q *Queries) SetAgentTokenLimits(ctx context.Context, id string, ratePerMin, dailyCalls int, dailySpendMicros int64) error {
	return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET rate_per_min=?, daily_calls=?, daily_spend_micros=?, updated_at=? WHERE id=?`,
		ratePerMin, dailyCalls, dailySpendMicros, nowMS(), id))
}

// AgentSpendSince 算 actor（agent:<id>）交辦的工作從 since 起花了多少（美元百萬分之一）。
// 工作的 meta.actor 記下是誰交辦的（chat.send 寫入）。
func (q *Queries) AgentSpendSince(ctx context.Context, actor string, since int64) (int64, error) {
	var n int64
	err := q.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(u.cost_micros), 0) FROM usage_entry u JOIN task t ON t.id = u.task_id
		WHERE u.created_at >= ? AND json_extract(t.meta, '$.actor') = ?`, since, actor).Scan(&n)
	return n, err
}

// ── delegation ───────────────────────────────────────

// Delegation 是一個代理的代為核准授權（MCP-10）。
type Delegation struct {
	AgentID           string   `json:"agent_id"`
	Categories        []string `json:"categories"`
	DailyCount        int      `json:"daily_count"`
	DailyAmountMicros int64    `json:"daily_amount_micros"`
	ExpiresAt         int64    `json:"expires_at"`
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

const delegationCols = `agent_id, categories, daily_count, daily_amount_micros, expires_at, created_at, updated_at`

func scanDelegation(s scanner) (Delegation, error) {
	var d Delegation
	var cats string
	err := s.Scan(&d.AgentID, &cats, &d.DailyCount, &d.DailyAmountMicros, &d.ExpiresAt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return d, err
	}
	d.Categories, err = parseList(cats)
	return d, err
}

// PutDelegation 新增或整筆取代一個代理的授權。
func (q *Queries) PutDelegation(ctx context.Context, d Delegation) (Delegation, error) {
	now := nowMS()
	d.UpdatedAt = now
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	if d.Categories == nil {
		d.Categories = []string{}
	}
	_, err := q.w.ExecContext(ctx, `INSERT INTO delegation(`+delegationCols+`) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET categories=excluded.categories, daily_count=excluded.daily_count,
		daily_amount_micros=excluded.daily_amount_micros, expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		d.AgentID, strList(d.Categories), d.DailyCount, d.DailyAmountMicros, d.ExpiresAt, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return d, err
	}
	return q.GetDelegation(ctx, d.AgentID)
}

// GetDelegation 讀一個代理的授權；沒有授權回 ErrNotFound。
func (q *Queries) GetDelegation(ctx context.Context, agentID string) (Delegation, error) {
	d, err := scanDelegation(q.r.QueryRowContext(ctx, `SELECT `+delegationCols+` FROM delegation WHERE agent_id = ?`, agentID))
	return d, notFound(err)
}

// DeleteDelegation 收回一個代理的授權（沒有也不算錯）。
func (q *Queries) DeleteDelegation(ctx context.Context, agentID string) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM delegation WHERE agent_id = ?`, agentID)
	return err
}

// DelegationUse 是一次代為核准。
type DelegationUse struct {
	ApprovalID   string `json:"approval_id"`
	AgentID      string `json:"agent_id"`
	Op           string `json:"op"`
	Category     string `json:"category"`
	AmountMicros int64  `json:"amount_micros"`
	CreatedAt    int64  `json:"created_at"`
}

// AddDelegationUse 記下一次代為核准；同一個核准第二次回 ErrAlreadyDecided。
func (q *Queries) AddDelegationUse(ctx context.Context, u DelegationUse) (DelegationUse, error) {
	u.CreatedAt = nowMS()
	res, err := q.w.ExecContext(ctx, `INSERT INTO delegation_use(approval_id, agent_id, op, category, amount_micros, created_at)
		VALUES (?,?,?,?,?,?) ON CONFLICT(approval_id) DO NOTHING`, u.ApprovalID, u.AgentID, u.Op, u.Category, u.AmountMicros, u.CreatedAt)
	if err := affected(res, err); err != nil {
		if errors.Is(err, ErrNotFound) {
			return u, ErrAlreadyDecided
		}
		return u, err
	}
	return u, nil
}

// DelegationUsedSince 算一個代理從 since 起代為核准了幾次、核准了多少錢。
func (q *Queries) DelegationUsedSince(ctx context.Context, agentID string, since int64) (count int, amountMicros int64, err error) {
	err = q.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(amount_micros), 0) FROM delegation_use WHERE agent_id = ? AND created_at >= ?`,
		agentID, since).Scan(&count, &amountMicros)
	return count, amountMicros, err
}

// GetDelegationUse 讀一個核准的代為核准紀錄；不是代為核准的回 ErrNotFound。
func (q *Queries) GetDelegationUse(ctx context.Context, approvalID string) (DelegationUse, error) {
	var u DelegationUse
	err := q.r.QueryRowContext(ctx, `SELECT approval_id, agent_id, op, category, amount_micros, created_at FROM delegation_use WHERE approval_id = ?`,
		approvalID).Scan(&u.ApprovalID, &u.AgentID, &u.Op, &u.Category, &u.AmountMicros, &u.CreatedAt)
	return u, notFound(err)
}

// DayStart 是 t 那一天（本機時區）零點的 Unix 毫秒；「每日」上限從這個時間開始算。
func DayStart(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).UnixMilli()
}
