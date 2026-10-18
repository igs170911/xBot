package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// ── task ─────────────────────────────────────────────

// 任務狀態（PRD §19）：queued → running → waiting_input / waiting_approval / waiting_box → stopping → done。
const (
	TaskQueued          = "queued"
	TaskRunning         = "running"
	TaskWaitingInput    = "waiting_input"
	TaskWaitingApproval = "waiting_approval"
	TaskWaitingBox      = "waiting_box"
	TaskStopping        = "stopping"
	TaskDone            = "done"
)

// Task 是交給 Bot 的一件工作。
type Task struct {
	ID          string          `json:"id"`
	BotID       string          `json:"bot_id"`
	ThreadID    string          `json:"thread_id"`
	Source      string          `json:"source"` // owner / bot / routine / webhook
	State       string          `json:"state"`
	Verdict     string          `json:"verdict"`
	LowPriority bool            `json:"low_priority"`
	Input       string          `json:"input"`
	Meta        json.RawMessage `json:"meta"`
	StartedAt   int64           `json:"started_at"`
	FinishedAt  int64           `json:"finished_at"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const taskCols = `id, bot_id, thread_id, source, state, verdict, low_priority, input, meta, started_at, finished_at, created_at, updated_at`

func scanTask(s scanner) (Task, error) {
	var t Task
	var thread sql.NullString
	var meta string
	err := s.Scan(&t.ID, &t.BotID, &thread, &t.Source, &t.State, &t.Verdict, &t.LowPriority, &t.Input, &meta, &t.StartedAt, &t.FinishedAt, &t.CreatedAt, &t.UpdatedAt)
	t.ThreadID, t.Meta = thread.String, json.RawMessage(meta)
	return t, err
}

// CreateTask 新增任務；State 空白時為 queued。
func (q *Queries) CreateTask(ctx context.Context, t Task) (Task, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.State == "" {
		t.State = TaskQueued
	}
	t.CreatedAt = nowMS()
	t.UpdatedAt = t.CreatedAt
	t.Meta = json.RawMessage(jsonOr(t.Meta, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO task(`+taskCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.BotID, nullStr(t.ThreadID), t.Source, t.State, t.Verdict, b2i(t.LowPriority), t.Input, string(t.Meta),
		t.StartedAt, t.FinishedAt, t.CreatedAt, t.UpdatedAt)
	return t, err
}

// GetTask 讀一個任務。
func (q *Queries) GetTask(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(q.r.QueryRowContext(ctx, `SELECT `+taskCols+` FROM task WHERE id = ?`, id))
	return t, notFound(err)
}

// ListTasks 列出一個 Bot 的任務（依建立順序）；openOnly 時只列還沒完成的。
func (q *Queries) ListTasks(ctx context.Context, botID string, openOnly bool) ([]Task, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+taskCols+` FROM task WHERE bot_id = ? AND (NOT ? OR state <> 'done')
		ORDER BY created_at, id`, botID, openOnly)
	return collect(rows, err, scanTask)
}

// ListTasksWithMeta 列出一個 Bot 的任務裡 meta 有 key 這個欄位（不是 null）的，由舊到新。key 是 JSON 路徑，例：$.deferred_reply。
func (q *Queries) ListTasksWithMeta(ctx context.Context, botID, key string) ([]Task, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+taskCols+` FROM task WHERE bot_id = ? AND json_extract(meta, ?) IS NOT NULL
		ORDER BY created_at, id`, botID, key)
	return collect(rows, err, scanTask)
}

// LatestDoneTask 回傳一個 Bot 最近結束的一件工作（不含 skipVerdict 這種結果，例：cancelled）；沒有時回 ErrNotFound。
func (q *Queries) LatestDoneTask(ctx context.Context, botID, skipVerdict string) (Task, error) {
	t, err := scanTask(q.r.QueryRowContext(ctx, `SELECT `+taskCols+` FROM task WHERE bot_id = ? AND state = 'done' AND verdict <> ?
		ORDER BY finished_at DESC, id DESC LIMIT 1`, botID, skipVerdict))
	return t, notFound(err)
}

// ListOpenTasks 列出所有 Bot 還沒完成的任務（服務重開時接續用）。
func (q *Queries) ListOpenTasks(ctx context.Context) ([]Task, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+taskCols+` FROM task WHERE state <> 'done' ORDER BY created_at, id`)
	return collect(rows, err, scanTask)
}

// SetTaskState 改任務狀態。第一次進 running 時記 started_at；進 done 時記 finished_at 與 verdict。
func (q *Queries) SetTaskState(ctx context.Context, id, state, verdict string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE task SET state=?,
		verdict = CASE WHEN ? <> '' THEN ? ELSE verdict END,
		started_at = CASE WHEN ? = 'running' AND started_at = 0 THEN ? ELSE started_at END,
		finished_at = CASE WHEN ? = 'done' THEN ? ELSE finished_at END,
		updated_at=? WHERE id=?`, state, verdict, verdict, state, now, state, now, now, id))
}

// ── event ────────────────────────────────────────────

// Event 是事件流水帳的一筆；ID 遞增，SSE 斷線後用它續傳。
type Event struct {
	ID        int64           `json:"id"`
	BotID     string          `json:"bot_id"`
	ThreadID  string          `json:"thread_id"`
	TaskID    string          `json:"task_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt int64           `json:"created_at"`
}

const eventCols = `id, bot_id, thread_id, task_id, kind, payload, created_at`

func scanEvent(s scanner) (Event, error) {
	var e Event
	var bot, thread, task sql.NullString
	var payload string
	err := s.Scan(&e.ID, &bot, &thread, &task, &e.Kind, &payload, &e.CreatedAt)
	e.BotID, e.ThreadID, e.TaskID, e.Payload = bot.String, thread.String, task.String, json.RawMessage(payload)
	return e, err
}

// AppendEvent 寫一筆事件，回傳含 ID 的事件。BotID 空白表示全域事件。
func (q *Queries) AppendEvent(ctx context.Context, e Event) (Event, error) {
	e.CreatedAt = nowMS()
	e.Payload = json.RawMessage(jsonOr(e.Payload, "{}"))
	res, err := q.w.ExecContext(ctx, `INSERT INTO event(bot_id, thread_id, task_id, kind, payload, created_at) VALUES (?,?,?,?,?,?)`,
		nullStr(e.BotID), nullStr(e.ThreadID), nullStr(e.TaskID), e.Kind, string(e.Payload), e.CreatedAt)
	if err != nil {
		return e, err
	}
	e.ID, err = res.LastInsertId()
	return e, err
}

// ListEvents 讀 ID 大於 since 的事件（由舊到新，最多 limit 筆）；botID 不是空白時只讀那個 Bot 的。
func (q *Queries) ListEvents(ctx context.Context, botID string, since int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+eventCols+` FROM event WHERE id > ? AND (? = '' OR bot_id = ?) ORDER BY id LIMIT ?`,
		since, botID, botID, limit)
	return collect(rows, err, scanEvent)
}

// LastEventID 回傳最新一筆事件的 ID（沒有就是 0）；botID 不是空白時只看那個 Bot。
func (q *Queries) LastEventID(ctx context.Context, botID string) (int64, error) {
	var id int64
	err := q.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM event WHERE ? = '' OR bot_id = ?`, botID, botID).Scan(&id)
	return id, err
}

// PruneEvents 只保留一個 Bot 最新的 keep 筆事件（同 LazyBoy store.py prune_events）。
func (q *Queries) PruneEvents(ctx context.Context, botID string, keep int) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM event WHERE bot_id = ? AND id <
		(SELECT id FROM event WHERE bot_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`, botID, botID, keep-1)
	return err
}

// LatestTaskEvent 回傳某個任務最新一筆指定種類的事件（例：chat.needs_approval）；沒有時回 ErrNotFound。
func (q *Queries) LatestTaskEvent(ctx context.Context, botID, taskID, kind string) (Event, error) {
	e, err := scanEvent(q.r.QueryRowContext(ctx, `SELECT `+eventCols+` FROM event
		WHERE bot_id = ? AND task_id = ? AND kind = ? ORDER BY id DESC LIMIT 1`, botID, taskID, kind))
	return e, notFound(err)
}

// ListThreadEvents 讀一條對話串的事件（由舊到新，最多 limit 筆，id 大於 after）；kinds 不是空的時只讀那些種類，
// taskID 不是空白時只讀那件工作的。
func (q *Queries) ListThreadEvents(ctx context.Context, threadID, taskID string, kinds []string, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = -1
	}
	query := `SELECT ` + eventCols + ` FROM event WHERE thread_id = ? AND id > ? AND (? = '' OR task_id = ?)`
	args := []any{threadID, after, taskID, taskID}
	if len(kinds) > 0 {
		query += ` AND kind IN (?` + strings.Repeat(",?", len(kinds)-1) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	rows, err := q.r.QueryContext(ctx, query+` ORDER BY id LIMIT ?`, append(args, limit)...)
	return collect(rows, err, scanEvent)
}

// LatestTaskInThread 回傳對話串裡最新的任務；沒有時回 ErrNotFound。
func (q *Queries) LatestTaskInThread(ctx context.Context, threadID string) (Task, error) {
	t, err := scanTask(q.r.QueryRowContext(ctx, `SELECT `+taskCols+` FROM task WHERE thread_id = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, threadID))
	return t, notFound(err)
}
