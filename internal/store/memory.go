package store

import "context"

// ── memory_version ───────────────────────────────────

// MemoryVersion 是一次記憶變更：改之前、改之後的全文（C31，MM-3）。
type MemoryVersion struct {
	ID     string `json:"id"`
	BotID  string `json:"bot_id"`
	Target string `json:"target"` // memory / user
	Before string `json:"before"`
	After  string `json:"after"`
	// Actor 是誰改的：owner、agent:<id>、bot:<id>；Bot 在電腦裡自己改的也記成 bot:<id>。
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"created_at"`
}

const memoryCols = `id, bot_id, target, before, after, actor, created_at`

// AddMemoryVersion 記一筆變更，並刪掉超過 keep 筆的舊紀錄（同一個 Bot、同一份記憶）。
func (q *Queries) AddMemoryVersion(ctx context.Context, v MemoryVersion, keep int) (MemoryVersion, error) {
	if v.ID == "" {
		v.ID = NewID()
	}
	if v.CreatedAt == 0 {
		v.CreatedAt = nowMS()
	}
	if _, err := q.w.ExecContext(ctx, `INSERT INTO memory_version(`+memoryCols+`) VALUES (?,?,?,?,?,?,?)`,
		v.ID, v.BotID, v.Target, v.Before, v.After, v.Actor, v.CreatedAt); err != nil {
		return v, err
	}
	_, err := q.w.ExecContext(ctx, `DELETE FROM memory_version WHERE bot_id = ? AND target = ? AND id NOT IN (
		SELECT id FROM memory_version WHERE bot_id = ? AND target = ? ORDER BY created_at DESC, id DESC LIMIT ?)`,
		v.BotID, v.Target, v.BotID, v.Target, keep)
	return v, err
}

// ListMemoryVersions 列出一個 Bot 的記憶變更，新的在前；target 空白時兩份都列。
func (q *Queries) ListMemoryVersions(ctx context.Context, botID, target string) ([]MemoryVersion, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+memoryCols+` FROM memory_version WHERE bot_id = ? AND (? = '' OR target = ?)
		ORDER BY created_at DESC, id DESC`, botID, target, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryVersion{}
	for rows.Next() {
		var v MemoryVersion
		if err := rows.Scan(&v.ID, &v.BotID, &v.Target, &v.Before, &v.After, &v.Actor, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetMemoryVersion 讀一筆變更。
func (q *Queries) GetMemoryVersion(ctx context.Context, id string) (MemoryVersion, error) {
	var v MemoryVersion
	err := q.r.QueryRowContext(ctx, `SELECT `+memoryCols+` FROM memory_version WHERE id = ?`, id).
		Scan(&v.ID, &v.BotID, &v.Target, &v.Before, &v.After, &v.Actor, &v.CreatedAt)
	return v, notFound(err)
}
