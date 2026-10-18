package store

import "context"

// 搬家紀錄的狀態（box_move.state）。
const (
	MoveQueued   = "queued"
	MoveRunning  = "running"
	MoveDone     = "done"
	MoveFailed   = "failed"
	MoveCanceled = "canceled"
)

// BoxMove 是一次電腦搬家（CL-5，B31）。
type BoxMove struct {
	ID         string `json:"id"`
	BotID      string `json:"bot_id"`
	FromHost   string `json:"from_host"`
	ToHost     string `json:"to_host"`
	State      string `json:"state"`
	Error      string `json:"error"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	KeepUntil  int64  `json:"keep_until"`
	CleanedAt  int64  `json:"cleaned_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	FinishedAt int64  `json:"finished_at"`
}

const moveCols = `id, bot_id, from_host, to_host, state, error, files, bytes, keep_until, cleaned_at, created_at, updated_at, finished_at`

func scanMove(s scanner) (BoxMove, error) {
	var m BoxMove
	err := s.Scan(&m.ID, &m.BotID, &m.FromHost, &m.ToHost, &m.State, &m.Error, &m.Files, &m.Bytes, &m.KeepUntil, &m.CleanedAt,
		&m.CreatedAt, &m.UpdatedAt, &m.FinishedAt)
	return m, err
}

// CreateMove 新增一筆搬家紀錄（ID 空白時自動產生，State 空白時是 queued）。
func (q *Queries) CreateMove(ctx context.Context, m BoxMove) (BoxMove, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.State == "" {
		m.State = MoveQueued
	}
	m.CreatedAt = nowMS()
	m.UpdatedAt = m.CreatedAt
	_, err := q.w.ExecContext(ctx, `INSERT INTO box_move(`+moveCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.BotID, m.FromHost, m.ToHost, m.State, m.Error, m.Files, m.Bytes, m.KeepUntil, m.CleanedAt, m.CreatedAt, m.UpdatedAt, m.FinishedAt)
	return m, err
}

// UpdateMove 覆寫搬家紀錄的狀態、結果與保留期。
func (q *Queries) UpdateMove(ctx context.Context, m BoxMove) error {
	return affected(q.w.ExecContext(ctx, `UPDATE box_move SET state=?, error=?, files=?, bytes=?, keep_until=?, cleaned_at=?, finished_at=?,
		updated_at=? WHERE id=?`, m.State, m.Error, m.Files, m.Bytes, m.KeepUntil, m.CleanedAt, m.FinishedAt, nowMS(), m.ID))
}

// GetMove 讀一筆搬家紀錄。
func (q *Queries) GetMove(ctx context.Context, id string) (BoxMove, error) {
	m, err := scanMove(q.r.QueryRowContext(ctx, `SELECT `+moveCols+` FROM box_move WHERE id = ?`, id))
	return m, notFound(err)
}

// ListMoves 列出最近的搬家紀錄（新的在前）；botID 不是空白時只列那個 Bot 的。
func (q *Queries) ListMoves(ctx context.Context, botID string, limit int) ([]BoxMove, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+moveCols+` FROM box_move WHERE ? = '' OR bot_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		botID, botID, limit)
	return collect(rows, err, scanMove)
}

// UnfinishedMoves 列出排隊中或進行中的搬家（舊的在前）。
func (q *Queries) UnfinishedMoves(ctx context.Context) ([]BoxMove, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+moveCols+` FROM box_move WHERE state IN ('queued', 'running') ORDER BY created_at, id`)
	return collect(rows, err, scanMove)
}

// ExpiredMoves 列出保留期已過、原主機上的舊資料還沒刪的搬家（now 是毫秒）。
func (q *Queries) ExpiredMoves(ctx context.Context, now int64) ([]BoxMove, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+moveCols+` FROM box_move WHERE state = 'done' AND cleaned_at = 0 AND keep_until > 0
		AND keep_until <= ? ORDER BY keep_until, id`, now)
	return collect(rows, err, scanMove)
}

// KeptMoves 列出原主機上還留著舊資料的搬家（botID、hostID 是篩選條件；空白＝不篩）。
func (q *Queries) KeptMoves(ctx context.Context, botID, hostID string) ([]BoxMove, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+moveCols+` FROM box_move WHERE state = 'done' AND cleaned_at = 0 AND keep_until > 0
		AND (? = '' OR bot_id = ?) AND (? = '' OR from_host = ?) ORDER BY keep_until, id`, botID, botID, hostID, hostID)
	return collect(rows, err, scanMove)
}
