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
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const approvalCols = `id, bot_id, action, payload, input, requested_by, request_id, status, decided_by, decided_at, created_at, updated_at`

func scanApproval(s scanner) (Approval, error) {
	var a Approval
	var bot sql.NullString
	var payload, input string
	err := s.Scan(&a.ID, &bot, &a.Action, &payload, &input, &a.RequestedBy, &a.RequestID, &a.Status, &a.DecidedBy, &a.DecidedAt, &a.CreatedAt, &a.UpdatedAt)
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
	_, err := q.w.ExecContext(ctx, `INSERT INTO approval(`+approvalCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, nullStr(a.BotID), a.Action, string(a.Payload), string(a.Input), a.RequestedBy, a.RequestID, a.Status, a.DecidedBy, a.DecidedAt, a.CreatedAt, a.UpdatedAt)
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

// ── inbox_item ───────────────────────────────────────

// InboxItem 是「待我處理」的一張卡。
type InboxItem struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"` // approval / question / secret / signin / takeover / proposal / discussion / routine_failed / budget
	BotID      string          `json:"bot_id"`
	Ref        string          `json:"ref"` // 例：approval 的 id
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"` // open / resolved / dismissed
	ResolvedBy string          `json:"resolved_by"`
	ResolvedAt int64           `json:"resolved_at"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

const inboxCols = `id, kind, bot_id, ref, title, body, payload, status, resolved_by, resolved_at, created_at, updated_at`

func scanInbox(s scanner) (InboxItem, error) {
	var i InboxItem
	var bot sql.NullString
	var payload string
	err := s.Scan(&i.ID, &i.Kind, &bot, &i.Ref, &i.Title, &i.Body, &payload, &i.Status, &i.ResolvedBy, &i.ResolvedAt, &i.CreatedAt, &i.UpdatedAt)
	i.BotID, i.Payload = bot.String, json.RawMessage(payload)
	return i, err
}

// CreateInboxItem 新增一張待我處理（status 一律從 open 開始）。
func (q *Queries) CreateInboxItem(ctx context.Context, i InboxItem) (InboxItem, error) {
	if i.ID == "" {
		i.ID = NewID()
	}
	i.Status = "open"
	i.CreatedAt = nowMS()
	i.UpdatedAt = i.CreatedAt
	i.Payload = json.RawMessage(jsonOr(i.Payload, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO inbox_item(`+inboxCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Kind, nullStr(i.BotID), i.Ref, i.Title, i.Body, string(i.Payload), i.Status, i.ResolvedBy, i.ResolvedAt, i.CreatedAt, i.UpdatedAt)
	return i, err
}

// GetInboxItem 讀一張待我處理。
func (q *Queries) GetInboxItem(ctx context.Context, id string) (InboxItem, error) {
	i, err := scanInbox(q.r.QueryRowContext(ctx, `SELECT `+inboxCols+` FROM inbox_item WHERE id = ?`, id))
	return i, notFound(err)
}

// ListInbox 列出待我處理，新到舊；status 不是空白時只列那個狀態。
func (q *Queries) ListInbox(ctx context.Context, status string) ([]InboxItem, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+inboxCols+` FROM inbox_item WHERE ? = '' OR status = ? ORDER BY created_at DESC, id DESC`, status, status)
	return collect(rows, err, scanInbox)
}

// ResolveInboxItem 把 open 的卡改成 resolved 或 dismissed。已處理過的回 ErrAlreadyDecided。
func (q *Queries) ResolveInboxItem(ctx context.Context, id, status, resolvedBy string) error {
	now := nowMS()
	res, err := q.w.ExecContext(ctx, `UPDATE inbox_item SET status=?, resolved_by=?, resolved_at=?, updated_at=? WHERE id=? AND status='open'`,
		status, resolvedBy, now, now, id)
	return q.decided(ctx, res, err, `SELECT 1 FROM inbox_item WHERE id = ?`, id)
}

// ResolveInboxByRef 把指向 ref（例：某個核准）的所有 open 卡一起結掉，回傳結掉幾張。
func (q *Queries) ResolveInboxByRef(ctx context.Context, ref, resolvedBy string) (int64, error) {
	now := nowMS()
	res, err := q.w.ExecContext(ctx, `UPDATE inbox_item SET status='resolved', resolved_by=?, resolved_at=?, updated_at=? WHERE ref=? AND status='open'`,
		resolvedBy, now, now, ref)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InboxRefExists 回答有沒有指向 ref 的卡（不管狀態）；同一件事只發一次卡時用（例：花費上限）。
func (q *Queries) InboxRefExists(ctx context.Context, ref string) (bool, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_item WHERE ref = ?`, ref).Scan(&n)
	return n > 0, err
}
