package store

import "context"

// ── worker_nonce ─────────────────────────────────────

// WorkerNonce 讀 Bot 目前的 worker-key 亂數（Model Proxy 用），沒有就回 ErrNotFound。
func (q *Queries) WorkerNonce(ctx context.Context, botID string) (string, error) {
	var n string
	err := q.r.QueryRowContext(ctx, `SELECT nonce FROM worker_nonce WHERE bot_id = ?`, botID).Scan(&n)
	return n, notFound(err)
}

// SetWorkerNonce 寫入（或換掉）Bot 的 worker-key 亂數。Bot 必須存在。
func (q *Queries) SetWorkerNonce(ctx context.Context, botID, nonce string) error {
	_, err := q.w.ExecContext(ctx, `INSERT INTO worker_nonce(bot_id, nonce, created_at) VALUES (?,?,?)
		ON CONFLICT(bot_id) DO UPDATE SET nonce=excluded.nonce, created_at=excluded.created_at`, botID, nonce, nowMS())
	return err
}

// DeleteWorkerNonce 刪掉 Bot 的 worker-key 亂數（收回 key）；本來就沒有也不算錯。
func (q *Queries) DeleteWorkerNonce(ctx context.Context, botID string) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM worker_nonce WHERE bot_id = ?`, botID)
	return err
}
