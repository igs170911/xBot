package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ── thread ───────────────────────────────────────────

// Thread 是和一個 Bot 的一條對話串。
type Thread struct {
	ID            string          `json:"id"`
	BotID         string          `json:"bot_id"`
	Title         string          `json:"title"`
	FolderID      string          `json:"folder_id"`
	Pinned        bool            `json:"pinned"`
	Archived      bool            `json:"archived"`
	Unread        int             `json:"unread"`
	ModelOverride string          `json:"model_override"`
	Usage         json.RawMessage `json:"usage"`
	LastMessageAt int64           `json:"last_message_at"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
}

const threadCols = `id, bot_id, title, folder_id, pinned, archived, unread, model_override, usage, last_message_at, created_at, updated_at`

func scanThread(s scanner) (Thread, error) {
	var t Thread
	var folder sql.NullString
	var usage string
	err := s.Scan(&t.ID, &t.BotID, &t.Title, &folder, &t.Pinned, &t.Archived, &t.Unread, &t.ModelOverride, &usage, &t.LastMessageAt, &t.CreatedAt, &t.UpdatedAt)
	t.FolderID, t.Usage = folder.String, json.RawMessage(usage)
	return t, err
}

// CreateThread 新增對話串。
func (q *Queries) CreateThread(ctx context.Context, t Thread) (Thread, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	t.CreatedAt = nowMS()
	t.UpdatedAt = t.CreatedAt
	t.Usage = json.RawMessage(jsonOr(t.Usage, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO thread(`+threadCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.BotID, t.Title, nullStr(t.FolderID), b2i(t.Pinned), b2i(t.Archived), t.Unread, t.ModelOverride,
		string(t.Usage), t.LastMessageAt, t.CreatedAt, t.UpdatedAt)
	return t, err
}

// GetThread 讀一條對話串。
func (q *Queries) GetThread(ctx context.Context, id string) (Thread, error) {
	t, err := scanThread(q.r.QueryRowContext(ctx, `SELECT `+threadCols+` FROM thread WHERE id = ?`, id))
	return t, notFound(err)
}

// ListThreads 列出一個 Bot 的對話串：釘選的在前，其餘依最後訊息時間新到舊。
func (q *Queries) ListThreads(ctx context.Context, botID string, includeArchived bool) ([]Thread, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+threadCols+` FROM thread WHERE bot_id = ? AND (archived = 0 OR ?)
		ORDER BY pinned DESC, last_message_at DESC, created_at DESC`, botID, includeArchived)
	return collect(rows, err, scanThread)
}

// UpdateThread 覆寫對話串的標題、資料夾、釘選、封存、模型覆寫。
func (q *Queries) UpdateThread(ctx context.Context, t Thread) error {
	return affected(q.w.ExecContext(ctx, `UPDATE thread SET title=?, folder_id=?, pinned=?, archived=?, model_override=?, updated_at=? WHERE id=?`,
		t.Title, nullStr(t.FolderID), b2i(t.Pinned), b2i(t.Archived), t.ModelOverride, nowMS(), t.ID))
}

// SetThreadUsage 寫入對話串的用量摘要。
func (q *Queries) SetThreadUsage(ctx context.Context, id string, usage json.RawMessage) error {
	return affected(q.w.ExecContext(ctx, `UPDATE thread SET usage=?, updated_at=? WHERE id=?`, jsonOr(usage, "{}"), nowMS(), id))
}

// MarkThreadRead 把未讀數歸零。
func (q *Queries) MarkThreadRead(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE thread SET unread=0, updated_at=? WHERE id=?`, nowMS(), id))
}

// DeleteThread 刪除對話串，連同裡面的訊息。
func (q *Queries) DeleteThread(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM thread WHERE id=?`, id))
}

// ── message ──────────────────────────────────────────

// 訊息的範圍。
const (
	ScopeThread = "thread"
	ScopeRoom   = "room"
)

// Message 是一則訊息。Seq 是寫入順序，用來排序與分頁。
type Message struct {
	Seq         int64           `json:"seq"`
	ID          string          `json:"id"`
	Scope       string          `json:"scope"`
	ScopeID     string          `json:"scope_id"`
	ParentID    string          `json:"parent_id"`
	Sender      string          `json:"sender"` // owner / bot:<id> / system
	Kind        string          `json:"kind"`   // text / card / activity / digest / widget / voice / peer
	Content     string          `json:"content"`
	Attachments json.RawMessage `json:"attachments"`
	Meta        json.RawMessage `json:"meta"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const messageCols = `seq, id, scope, scope_id, parent_id, sender, kind, content, attachments, meta, created_at, updated_at`

func scanMessage(s scanner) (Message, error) {
	var m Message
	var parent sql.NullString
	var att, meta string
	err := s.Scan(&m.Seq, &m.ID, &m.Scope, &m.ScopeID, &parent, &m.Sender, &m.Kind, &m.Content, &att, &meta, &m.CreatedAt, &m.UpdatedAt)
	m.ParentID, m.Attachments, m.Meta = parent.String, json.RawMessage(att), json.RawMessage(meta)
	return m, err
}

// AddMessage 新增一則訊息；在對話串裡時，同時更新對話串的最後訊息時間，非使用者送出的訊息會增加未讀數。
func (q *Queries) AddMessage(ctx context.Context, m Message) (Message, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.Kind == "" {
		m.Kind = "text"
	}
	m.CreatedAt = nowMS()
	m.UpdatedAt = m.CreatedAt
	m.Attachments = json.RawMessage(jsonOr(m.Attachments, "[]"))
	m.Meta = json.RawMessage(jsonOr(m.Meta, "{}"))
	err := q.atomic(ctx, func(q *Queries) error {
		return q.addMessage(ctx, &m)
	})
	return m, err
}

func (q *Queries) addMessage(ctx context.Context, m *Message) error {
	res, err := q.w.ExecContext(ctx, `INSERT INTO message(id, scope, scope_id, parent_id, sender, kind, content, attachments, meta, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Scope, m.ScopeID, nullStr(m.ParentID), m.Sender, m.Kind, m.Content,
		string(m.Attachments), string(m.Meta), m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return err
	}
	if m.Seq, err = res.LastInsertId(); err != nil {
		return err
	}
	if m.Scope == ScopeThread {
		_, err = q.w.ExecContext(ctx, `UPDATE thread SET last_message_at=?, updated_at=?,
			unread = unread + CASE WHEN ? = 'owner' THEN 0 ELSE 1 END WHERE id=?`, m.CreatedAt, m.CreatedAt, m.Sender, m.ScopeID)
	}
	return err
}

// GetMessage 讀一則訊息。
func (q *Queries) GetMessage(ctx context.Context, id string) (Message, error) {
	m, err := scanMessage(q.r.QueryRowContext(ctx, `SELECT `+messageCols+` FROM message WHERE id = ?`, id))
	return m, notFound(err)
}

// ListMessages 讀一個範圍裡的訊息，依時間由舊到新。before > 0 時只讀 seq 比它小的（往前翻頁），
// 結果是最接近 before 的 limit 則。
func (q *Queries) ListMessages(ctx context.Context, scope, scopeID string, before int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = -1 // 不限
	}
	rows, err := q.r.QueryContext(ctx, `SELECT * FROM (SELECT `+messageCols+` FROM message
		WHERE scope = ? AND scope_id = ? AND (? = 0 OR seq < ?) ORDER BY seq DESC LIMIT ?) ORDER BY seq`,
		scope, scopeID, before, before, limit)
	return collect(rows, err, scanMessage)
}

// UpdateMessage 改訊息的內容、附件與 meta（例：串流結束時補上完整內容）。
func (q *Queries) UpdateMessage(ctx context.Context, id, content string, attachments, meta json.RawMessage) error {
	return affected(q.w.ExecContext(ctx, `UPDATE message SET content=?, attachments=?, meta=?, updated_at=? WHERE id=?`,
		content, jsonOr(attachments, "[]"), jsonOr(meta, "{}"), nowMS(), id))
}

// CountMessages 回傳一個範圍裡的訊息數。
func (q *Queries) CountMessages(ctx context.Context, scope, scopeID string) (int, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE scope = ? AND scope_id = ?`, scope, scopeID).Scan(&n)
	return n, err
}
