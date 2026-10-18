package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ── routine ──────────────────────────────────────────

// 定時任務的狀態。
const (
	RoutineActive = "active"
	RoutinePaused = "paused"
	RoutineDone   = "done"
)

// Routine 是一個定時任務（PRD 6.8）。Schedule、Goal 的內容由 internal/routines 解讀。
type Routine struct {
	ID              string          `json:"id"`
	BotID           string          `json:"bot_id"`
	Title           string          `json:"title"`
	Prompt          string          `json:"prompt"`
	Schedule        json.RawMessage `json:"schedule"`
	JitterMinutes   int             `json:"jitter_minutes"`
	NotifyOnFailure bool            `json:"notify_on_failure"`
	Goal            json.RawMessage `json:"goal"`
	ThreadID        string          `json:"thread_id"`
	Status          string          `json:"status"`
	NextRunAt       int64           `json:"next_run_at"`
	LastRunAt       int64           `json:"last_run_at"`
	LastTaskID      string          `json:"last_task_id"`
	LastError       string          `json:"last_error"`
	Failures        int             `json:"failures"`
	RetryAt         int64           `json:"retry_at"`
	HookID          string          `json:"hook_id"`
	HookSecret      string          `json:"-"`
	LastHookAt      int64           `json:"last_hook_at"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`
}

const routineCols = `id, bot_id, title, prompt, schedule, jitter_minutes, notify_on_failure, goal, thread_id, status, next_run_at,
	last_run_at, last_task_id, last_error, failures, retry_at, hook_id, hook_secret, last_hook_at, created_by, created_at, updated_at`

func scanRoutine(s scanner) (Routine, error) {
	var r Routine
	var schedule, goal string
	var thread, hook sql.NullString
	err := s.Scan(&r.ID, &r.BotID, &r.Title, &r.Prompt, &schedule, &r.JitterMinutes, &r.NotifyOnFailure, &goal, &thread, &r.Status,
		&r.NextRunAt, &r.LastRunAt, &r.LastTaskID, &r.LastError, &r.Failures, &r.RetryAt, &hook, &r.HookSecret, &r.LastHookAt,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	r.Schedule, r.Goal, r.ThreadID, r.HookID = json.RawMessage(schedule), json.RawMessage(goal), thread.String, hook.String
	return r, err
}

func (r *Routine) args() []any {
	return []any{r.ID, r.BotID, r.Title, r.Prompt, jsonOr(r.Schedule, "{}"), r.JitterMinutes, b2i(r.NotifyOnFailure), jsonOr(r.Goal, "{}"),
		nullStr(r.ThreadID), r.Status, r.NextRunAt, r.LastRunAt, r.LastTaskID, r.LastError, r.Failures, r.RetryAt, nullStr(r.HookID),
		r.HookSecret, r.LastHookAt, r.CreatedBy, r.CreatedAt, r.UpdatedAt}
}

// CreateRoutine 新增一個定時任務；Status 空白時是 active。
func (q *Queries) CreateRoutine(ctx context.Context, r Routine) (Routine, error) {
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.Status == "" {
		r.Status = RoutineActive
	}
	r.CreatedAt = nowMS()
	r.UpdatedAt = r.CreatedAt
	r.Schedule, r.Goal = json.RawMessage(jsonOr(r.Schedule, "{}")), json.RawMessage(jsonOr(r.Goal, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO routine(`+routineCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.args()...)
	return r, err
}

// UpdateRoutine 整筆寫回（id、bot_id、created_* 不變）。
func (q *Queries) UpdateRoutine(ctx context.Context, r Routine) (Routine, error) {
	r.UpdatedAt = nowMS()
	r.Schedule, r.Goal = json.RawMessage(jsonOr(r.Schedule, "{}")), json.RawMessage(jsonOr(r.Goal, "{}"))
	err := affected(q.w.ExecContext(ctx, `UPDATE routine SET title=?, prompt=?, schedule=?, jitter_minutes=?, notify_on_failure=?, goal=?,
		thread_id=?, status=?, next_run_at=?, last_run_at=?, last_task_id=?, last_error=?, failures=?, retry_at=?, hook_id=?, hook_secret=?,
		last_hook_at=?, updated_at=? WHERE id=?`,
		r.Title, r.Prompt, string(r.Schedule), r.JitterMinutes, b2i(r.NotifyOnFailure), string(r.Goal), nullStr(r.ThreadID), r.Status,
		r.NextRunAt, r.LastRunAt, r.LastTaskID, r.LastError, r.Failures, r.RetryAt, nullStr(r.HookID), r.HookSecret, r.LastHookAt,
		r.UpdatedAt, r.ID))
	return r, err
}

// GetRoutine 讀一個定時任務。
func (q *Queries) GetRoutine(ctx context.Context, id string) (Routine, error) {
	r, err := scanRoutine(q.r.QueryRowContext(ctx, `SELECT `+routineCols+` FROM routine WHERE id = ?`, id))
	return r, notFound(err)
}

// GetRoutineByHook 用網址觸發的 id 找定時任務。
func (q *Queries) GetRoutineByHook(ctx context.Context, hookID string) (Routine, error) {
	r, err := scanRoutine(q.r.QueryRowContext(ctx, `SELECT `+routineCols+` FROM routine WHERE hook_id = ?`, hookID))
	return r, notFound(err)
}

// ListRoutines 列出定時任務（依建立順序）；botID 不是空白時只列那個 Bot 的。
func (q *Queries) ListRoutines(ctx context.Context, botID string) ([]Routine, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+routineCols+` FROM routine WHERE (? = '' OR bot_id = ?) ORDER BY created_at, id`, botID, botID)
	return collect(rows, err, scanRoutine)
}

// CountRoutines 算一個 Bot 有幾個定時任務。
func (q *Queries) CountRoutines(ctx context.Context, botID string) (int, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM routine WHERE bot_id = ?`, botID).Scan(&n)
	return n, err
}

// DueRoutines 列出到期（next_run_at <= now、不在退避中）的啟用中定時任務。
func (q *Queries) DueRoutines(ctx context.Context, now int64) ([]Routine, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+routineCols+` FROM routine
		WHERE status = 'active' AND next_run_at > 0 AND next_run_at <= ? AND retry_at <= ? ORDER BY next_run_at, id`, now, now)
	return collect(rows, err, scanRoutine)
}

// DeleteRoutine 刪除定時任務（連同執行紀錄）。
func (q *Queries) DeleteRoutine(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM routine WHERE id = ?`, id))
}

// ── routine_run ──────────────────────────────────────

// 執行紀錄的觸發方式與狀態。
const (
	TriggerSchedule = "schedule"
	TriggerCatchup  = "catchup"
	TriggerManual   = "manual"
	TriggerWebhook  = "webhook"
	TriggerTest     = "test"

	RunStarting  = "starting"
	RunRunning   = "running"
	RunDone      = "done"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
	RunSkipped   = "skipped"
	RunRetrying  = "retrying"
	RunTest      = "test"
)

// RoutineRun 是定時任務的一次執行。
type RoutineRun struct {
	ID           string `json:"id"`
	RoutineID    string `json:"routine_id"`
	BotID        string `json:"bot_id"`
	Occurrence   string `json:"occurrence"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"`
	TaskID       string `json:"task_id"`
	Error        string `json:"error"`
	PayloadBytes int    `json:"payload_bytes"`
	ScheduledAt  int64  `json:"scheduled_at"`
	StartedAt    int64  `json:"started_at"`
	FinishedAt   int64  `json:"finished_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

const runCols = `id, routine_id, bot_id, occurrence, trigger, status, task_id, error, payload_bytes, scheduled_at, started_at, finished_at,
	created_at, updated_at`

func scanRun(s scanner) (RoutineRun, error) {
	var r RoutineRun
	err := s.Scan(&r.ID, &r.RoutineID, &r.BotID, &r.Occurrence, &r.Trigger, &r.Status, &r.TaskID, &r.Error, &r.PayloadBytes,
		&r.ScheduledAt, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ClaimRoutineRun 登記一次執行；同一個定時任務的同一個 occurrence 已經有紀錄時回傳那一筆（created=false）。
func (q *Queries) ClaimRoutineRun(ctx context.Context, r RoutineRun) (run RoutineRun, created bool, err error) {
	if r.ID == "" {
		r.ID = NewID()
	}
	r.CreatedAt = nowMS()
	r.UpdatedAt = r.CreatedAt
	res, err := q.w.ExecContext(ctx, `INSERT OR IGNORE INTO routine_run(`+runCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.RoutineID, r.BotID, r.Occurrence, r.Trigger, r.Status, r.TaskID, r.Error, r.PayloadBytes, r.ScheduledAt, r.StartedAt,
		r.FinishedAt, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return r, false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return r, true, nil
	}
	run, err = scanRun(q.w.QueryRowContext(ctx, `SELECT `+runCols+` FROM routine_run WHERE routine_id = ? AND occurrence = ?`,
		r.RoutineID, r.Occurrence))
	return run, false, notFound(err)
}

// UpdateRoutineRun 改一次執行的狀態、工作與錯誤。進入 running 時記 started_at；結束時記 finished_at。
func (q *Queries) UpdateRoutineRun(ctx context.Context, id, status, taskID, errText string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE routine_run SET status=?,
		task_id = CASE WHEN ? <> '' THEN ? ELSE task_id END,
		error=?,
		started_at = CASE WHEN ? = 'running' AND started_at = 0 THEN ? ELSE started_at END,
		finished_at = CASE WHEN ? IN ('done', 'failed', 'cancelled', 'skipped', 'test') THEN ? ELSE finished_at END,
		updated_at=? WHERE id=?`, status, taskID, taskID, errText, status, now, status, now, now, id))
}

// GetRoutineRun 讀一次執行。
func (q *Queries) GetRoutineRun(ctx context.Context, id string) (RoutineRun, error) {
	r, err := scanRun(q.r.QueryRowContext(ctx, `SELECT `+runCols+` FROM routine_run WHERE id = ?`, id))
	return r, notFound(err)
}

// ListRoutineRuns 列出一個定時任務的執行紀錄，新到舊。
func (q *Queries) ListRoutineRuns(ctx context.Context, routineID string, limit int) ([]RoutineRun, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+runCols+` FROM routine_run WHERE routine_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`,
		routineID, limit)
	return collect(rows, err, scanRun)
}

// ListRunsBetween 列出排定時間在 [from, to) 之間的執行紀錄（行事曆用）；botID 不是空白時只列那個 Bot 的。
func (q *Queries) ListRunsBetween(ctx context.Context, botID string, from, to int64) ([]RoutineRun, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+runCols+` FROM routine_run
		WHERE (? = '' OR bot_id = ?) AND scheduled_at >= ? AND scheduled_at < ? ORDER BY scheduled_at, rowid`, botID, botID, from, to)
	return collect(rows, err, scanRun)
}

// ListOpenRoutineRuns 列出還在 starting／running 的執行（服務重開時對帳）。
func (q *Queries) ListOpenRoutineRuns(ctx context.Context) ([]RoutineRun, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+runCols+` FROM routine_run WHERE status IN ('starting', 'running') ORDER BY created_at, id`)
	return collect(rows, err, scanRun)
}

// PruneRoutineRuns 只保留每個定時任務最新的 keep 筆紀錄（還在跑的不刪）。
func (q *Queries) PruneRoutineRuns(ctx context.Context, routineID string, keep int) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM routine_run WHERE routine_id = ? AND status NOT IN ('starting', 'running', 'retrying')
		AND id NOT IN (SELECT id FROM routine_run WHERE routine_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?)`, routineID, routineID, keep)
	return err
}
