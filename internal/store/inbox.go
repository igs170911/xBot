package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// ── inbox_item ───────────────────────────────────────

// 待我處理的狀態。
const (
	InboxOpen      = "open"
	InboxResolved  = "resolved"
	InboxDismissed = "dismissed"
	InboxExpired   = "expired"
)

// InboxItem 是「待我處理」或通知中心的一張卡。
type InboxItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // approval / question / secret / signin / takeover / proposal / discussion / routine_failed / budget / done / error / mention
	BotID    string `json:"bot_id"`
	TaskID   string `json:"task_id"`
	ThreadID string `json:"thread_id"`
	Ref      string `json:"ref"` // 例：approval 的 id；Bot 在對話裡的提問是引擎的 request id
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Payload 是給介面顯示的內容；永遠不放密碼、驗證碼這類使用者輸入的值。
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`     // open / resolved / dismissed / expired
	Resolution string          `json:"resolution"` // once / always / deny / allow / answered / provided / declined / expired / gone / canceled / asked_again / undone
	ResolvedBy string          `json:"resolved_by"`
	ResolvedAt int64           `json:"resolved_at"`
	ReadAt     int64           `json:"read_at"`    // 0＝未讀
	ExpiresAt  int64           `json:"expires_at"` // 0＝不會過期
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

const inboxCols = `id, kind, bot_id, task_id, thread_id, ref, title, body, payload, status, resolution, resolved_by, resolved_at, read_at, expires_at, created_at, updated_at`

func scanInbox(s scanner) (InboxItem, error) {
	var i InboxItem
	var bot, task, thread sql.NullString
	var payload string
	err := s.Scan(&i.ID, &i.Kind, &bot, &task, &thread, &i.Ref, &i.Title, &i.Body, &payload, &i.Status, &i.Resolution, &i.ResolvedBy,
		&i.ResolvedAt, &i.ReadAt, &i.ExpiresAt, &i.CreatedAt, &i.UpdatedAt)
	i.BotID, i.TaskID, i.ThreadID, i.Payload = bot.String, task.String, thread.String, json.RawMessage(payload)
	return i, err
}

// CreateInboxItem 新增一張卡。Status 空白時是 open（通知類可以直接建成 resolved）。
func (q *Queries) CreateInboxItem(ctx context.Context, i InboxItem) (InboxItem, error) {
	if i.ID == "" {
		i.ID = NewID()
	}
	if i.Status == "" {
		i.Status = InboxOpen
	}
	i.CreatedAt = nowMS()
	i.UpdatedAt = i.CreatedAt
	i.Payload = json.RawMessage(jsonOr(i.Payload, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO inbox_item(`+inboxCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Kind, nullStr(i.BotID), nullStr(i.TaskID), nullStr(i.ThreadID), i.Ref, i.Title, i.Body, string(i.Payload), i.Status,
		i.Resolution, i.ResolvedBy, i.ResolvedAt, i.ReadAt, i.ExpiresAt, i.CreatedAt, i.UpdatedAt)
	return i, err
}

// GetInboxItem 讀一張卡。
func (q *Queries) GetInboxItem(ctx context.Context, id string) (InboxItem, error) {
	i, err := scanInbox(q.r.QueryRowContext(ctx, `SELECT `+inboxCols+` FROM inbox_item WHERE id = ?`, id))
	return i, notFound(err)
}

// ListInbox 列出卡片，新到舊；status 不是空白時只列那個狀態。
func (q *Queries) ListInbox(ctx context.Context, status string) ([]InboxItem, error) {
	return q.ListInboxFiltered(ctx, InboxFilter{Status: status})
}

// InboxFilter 是列卡片的條件；空白的欄位不限。
type InboxFilter struct {
	Status string
	Kinds  []string
	BotID  string
	TaskID string
	Unread bool
}

// ListInboxFiltered 依條件列出卡片，新到舊。
func (q *Queries) ListInboxFiltered(ctx context.Context, f InboxFilter) ([]InboxItem, error) {
	where := []string{"1=1"}
	var args []any
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, f.Status)
	}
	if len(f.Kinds) > 0 {
		where = append(where, "kind IN (?"+strings.Repeat(",?", len(f.Kinds)-1)+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.BotID != "" {
		where, args = append(where, "bot_id = ?"), append(args, f.BotID)
	}
	if f.TaskID != "" {
		where, args = append(where, "task_id = ?"), append(args, f.TaskID)
	}
	if f.Unread {
		where = append(where, "read_at = 0")
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+inboxCols+` FROM inbox_item WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id DESC`, args...)
	return collect(rows, err, scanInbox)
}

// ResolveInboxItem 把 open 的卡改成 resolved 或 dismissed。已處理過的回 ErrAlreadyDecided。
func (q *Queries) ResolveInboxItem(ctx context.Context, id, status, resolvedBy string) error {
	return q.SettleInboxItem(ctx, id, status, "", resolvedBy, nil)
}

// SettleInboxItem 把 open 的卡改成 status（resolved／dismissed／expired），記下怎麼處理的；
// payload 不是 nil 時一起換掉（例：記下回答的文字，給「已回答」那一行顯示）。處理過的卡也算已讀。
// 已處理過的回 ErrAlreadyDecided，不存在的回 ErrNotFound。
func (q *Queries) SettleInboxItem(ctx context.Context, id, status, resolution, resolvedBy string, payload json.RawMessage) error {
	now := nowMS()
	res, err := q.w.ExecContext(ctx, `UPDATE inbox_item SET status=?, resolution=?, resolved_by=?, resolved_at=?,
		read_at = CASE WHEN read_at = 0 THEN ? ELSE read_at END,
		payload = CASE WHEN ? <> '' THEN ? ELSE payload END, updated_at=? WHERE id=? AND status='open'`,
		status, resolution, resolvedBy, now, now, string(payload), string(payload), now, id)
	return q.decided(ctx, res, err, `SELECT 1 FROM inbox_item WHERE id = ?`, id)
}

// ResolveInboxByRef 把指向 ref（例：某個核准）的所有 open 卡一起結掉，回傳結掉的卡。
func (q *Queries) ResolveInboxByRef(ctx context.Context, ref, resolution, resolvedBy string) ([]InboxItem, error) {
	items, err := q.ListInboxByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []InboxItem
	for _, it := range items {
		if it.Status != InboxOpen {
			continue
		}
		if err := q.SettleInboxItem(ctx, it.ID, InboxResolved, resolution, resolvedBy, nil); err != nil {
			return out, err
		}
		got, err := q.GetInboxItem(ctx, it.ID)
		if err != nil {
			return out, err
		}
		out = append(out, got)
	}
	return out, nil
}

// ListInboxByRef 列出指向 ref 的卡（新到舊）。
func (q *Queries) ListInboxByRef(ctx context.Context, ref string) ([]InboxItem, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+inboxCols+` FROM inbox_item WHERE ref = ? ORDER BY created_at DESC, id DESC`, ref)
	return collect(rows, err, scanInbox)
}

// ReopenInboxItem 把處理過的卡改回 open（復原）。已經是 open 的回 ErrAlreadyDecided。
func (q *Queries) ReopenInboxItem(ctx context.Context, id string) error {
	res, err := q.w.ExecContext(ctx, `UPDATE inbox_item SET status='open', resolution='', resolved_by='', resolved_at=0, updated_at=?
		WHERE id=? AND status <> 'open'`, nowMS(), id)
	return q.decided(ctx, res, err, `SELECT 1 FROM inbox_item WHERE id = ?`, id)
}

// SetInboxResolution 只改處理方式（例：過期的提問按了「請它再問一次」）。
func (q *Queries) SetInboxResolution(ctx context.Context, id, resolution string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE inbox_item SET resolution=?, updated_at=? WHERE id=?`, resolution, nowMS(), id))
}

// MarkInboxRead 把卡標成已讀，回傳這次真的從未讀變已讀的卡的 id。
// ids 不是空的時只標那些；threadID 不是空白時只標那條對話串的；兩個都空白時全部標。
func (q *Queries) MarkInboxRead(ctx context.Context, ids []string, threadID string) ([]string, error) {
	where := "read_at = 0"
	var args []any
	switch {
	case len(ids) > 0:
		where += " AND id IN (?" + strings.Repeat(",?", len(ids)-1) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	case threadID != "":
		where += " AND thread_id = ?"
		args = append(args, threadID)
	}
	var marked []string
	err := q.atomic(ctx, func(q *Queries) error {
		rows, err := q.w.QueryContext(ctx, `SELECT id FROM inbox_item WHERE `+where, args...)
		marked, err = collect(rows, err, func(s scanner) (string, error) {
			var id string
			return id, s.Scan(&id)
		})
		if err != nil || len(marked) == 0 {
			return err
		}
		now := nowMS()
		upd := []any{now, now}
		for _, id := range marked {
			upd = append(upd, id)
		}
		_, err = q.w.ExecContext(ctx, `UPDATE inbox_item SET read_at=?, updated_at=? WHERE id IN (?`+strings.Repeat(",?", len(marked)-1)+`)`, upd...)
		return err
	})
	return marked, err
}
