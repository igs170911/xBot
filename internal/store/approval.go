package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ErrAlreadyDecided 表示核准或待我處理項目已經處理過，不能再改。
var ErrAlreadyDecided = errors.New("這一項已經處理過了")

// ── approval ─────────────────────────────────────────

// Approval 是一個等待使用者核准的動作（MCP-9）。
type Approval struct {
	ID          string          `json:"id"`
	BotID       string          `json:"bot_id"`
	Action      string          `json:"action"`
	Payload     json.RawMessage `json:"payload"`
	Input       json.RawMessage `json:"-"` // 核准後要執行的原始輸入，可能含密鑰，永遠不對外回傳
	RequestedBy string          `json:"requested_by"`
	RequestID   string          `json:"request_id"`
	Status      string          `json:"status"` // pending / approved / denied / expired / canceled
	DecidedBy   string          `json:"decided_by"`
	DecidedAt   int64           `json:"decided_at"`
	// Error 是允許後執行失敗的錯誤（MCP-5 格式的 JSON）；沒失敗或之後成功時是空白。
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const approvalCols = `id, bot_id, action, payload, input, requested_by, request_id, status, decided_by, decided_at, error, created_at, updated_at`

func scanApproval(s scanner) (Approval, error) {
	var a Approval
	var bot sql.NullString
	var payload, input string
	err := s.Scan(&a.ID, &bot, &a.Action, &payload, &input, &a.RequestedBy, &a.RequestID, &a.Status, &a.DecidedBy, &a.DecidedAt, &a.Error, &a.CreatedAt, &a.UpdatedAt)
	a.BotID, a.Payload, a.Input = bot.String, json.RawMessage(payload), json.RawMessage(input)
	return a, err
}

// CreateApproval 新增一個待核准的動作（status 一律從 pending 開始）。
func (q *Queries) CreateApproval(ctx context.Context, a Approval) (Approval, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	a.Status = "pending"
	a.CreatedAt = nowMS()
	a.UpdatedAt = a.CreatedAt
	a.Payload = json.RawMessage(jsonOr(a.Payload, "{}"))
	a.Input = json.RawMessage(jsonOr(a.Input, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO approval(`+approvalCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, nullStr(a.BotID), a.Action, string(a.Payload), string(a.Input), a.RequestedBy, a.RequestID, a.Status, a.DecidedBy, a.DecidedAt, "", a.CreatedAt, a.UpdatedAt)
	return a, err
}

// GetApproval 讀一個核准。
func (q *Queries) GetApproval(ctx context.Context, id string) (Approval, error) {
	a, err := scanApproval(q.r.QueryRowContext(ctx, `SELECT `+approvalCols+` FROM approval WHERE id = ?`, id))
	return a, notFound(err)
}

// ListApprovals 列出核准，新到舊；status 不是空白時只列那個狀態。
func (q *Queries) ListApprovals(ctx context.Context, status string) ([]Approval, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+approvalCols+` FROM approval WHERE ? = '' OR status = ? ORDER BY created_at DESC, id DESC`, status, status)
	return collect(rows, err, scanApproval)
}

// DecideApproval 把 pending 的核准改成 status（approved／denied／expired／canceled）。
// 已經處理過的回 ErrAlreadyDecided，不存在的回 ErrNotFound。
func (q *Queries) DecideApproval(ctx context.Context, id, status, decidedBy string) error {
	now := nowMS()
	res, err := q.w.ExecContext(ctx, `UPDATE approval SET status=?, decided_by=?, decided_at=?, updated_at=? WHERE id=? AND status='pending'`,
		status, decidedBy, now, now, id)
	return q.decided(ctx, res, err, `SELECT 1 FROM approval WHERE id = ?`, id)
}

// SetApprovalError 記下允許後執行失敗的錯誤（errJSON 空白表示清掉，執行成功時）。
func (q *Queries) SetApprovalError(ctx context.Context, id, errJSON string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE approval SET error=?, updated_at=? WHERE id=?`, errJSON, nowMS(), id))
}

// ReopenApproval 把退回（denied）的核准改回 pending（在復原時限內復原）。不是 denied 的回 ErrAlreadyDecided。
func (q *Queries) ReopenApproval(ctx context.Context, id string) error {
	res, err := q.w.ExecContext(ctx, `UPDATE approval SET status='pending', decided_by='', decided_at=0, updated_at=? WHERE id=? AND status='denied'`, nowMS(), id)
	return q.decided(ctx, res, err, `SELECT 1 FROM approval WHERE id = ?`, id)
}

// decided 把「沒改到」分成不存在（ErrNotFound）和已處理過（ErrAlreadyDecided）。
func (q *Queries) decided(ctx context.Context, res sql.Result, err error, exists, id string) error {
	if err = affected(res, err); !errors.Is(err, ErrNotFound) {
		return err
	}
	var one int
	if e := q.w.QueryRowContext(ctx, exists, id).Scan(&one); e != nil {
		return notFound(e)
	}
	return ErrAlreadyDecided
}
