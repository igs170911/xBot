package store

import (
	"context"
	"encoding/json"
)

// ── lesson（B32 示範教學）──────────────────────────────

// 示範教學的狀態。
const (
	LessonRecording = "recording"
	LessonPaused    = "paused"
	LessonStopped   = "stopped"
	LessonSaved     = "saved"
	LessonDiscarded = "discarded"
)

// Lesson 是一次示範教學。Notes、Dropped、Steps、Draft 的內容由 internal/teaching 解讀。
type Lesson struct {
	ID          string          `json:"id"`
	BotID       string          `json:"bot_id"`
	Goal        string          `json:"goal"`
	State       string          `json:"state"`
	Notes       json.RawMessage `json:"notes"`
	Dropped     json.RawMessage `json:"dropped"`
	Steps       json.RawMessage `json:"steps"`
	Draft       json.RawMessage `json:"draft"`
	SkillID     string          `json:"skill_id"`
	ThreadID    string          `json:"thread_id"`
	TrialTaskID string          `json:"trial_task_id"`
	CreatedBy   string          `json:"created_by"`
	StartedAt   int64           `json:"started_at"`
	EndedAt     int64           `json:"ended_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

// Open 回答這次教學是不是還沒結束（錄製中、暫停、等你確認草稿）。
func (l Lesson) Open() bool {
	return l.State == LessonRecording || l.State == LessonPaused || l.State == LessonStopped
}

// Recording 回答錄製器是不是還在跑（錄製中或暫停）。
func (l Lesson) Recording() bool { return l.State == LessonRecording || l.State == LessonPaused }

const lessonCols = `id, bot_id, goal, state, notes, dropped, steps, draft, skill_id, thread_id, trial_task_id, created_by,
	started_at, ended_at, updated_at`

func scanLesson(s scanner) (Lesson, error) {
	var l Lesson
	var notes, dropped, steps, draft string
	err := s.Scan(&l.ID, &l.BotID, &l.Goal, &l.State, &notes, &dropped, &steps, &draft, &l.SkillID, &l.ThreadID, &l.TrialTaskID,
		&l.CreatedBy, &l.StartedAt, &l.EndedAt, &l.UpdatedAt)
	l.Notes, l.Dropped, l.Steps, l.Draft = json.RawMessage(notes), json.RawMessage(dropped), json.RawMessage(steps), json.RawMessage(draft)
	return l, err
}

// CreateLesson 新增一次示範教學；State 空白時是 recording。
func (q *Queries) CreateLesson(ctx context.Context, l Lesson) (Lesson, error) {
	if l.ID == "" {
		l.ID = NewID()
	}
	if l.State == "" {
		l.State = LessonRecording
	}
	l.StartedAt = nowMS()
	l.UpdatedAt = l.StartedAt
	l.normalize()
	_, err := q.w.ExecContext(ctx, `INSERT INTO lesson(`+lessonCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.ID, l.BotID, l.Goal, l.State, string(l.Notes), string(l.Dropped), string(l.Steps), string(l.Draft), l.SkillID, l.ThreadID,
		l.TrialTaskID, l.CreatedBy, l.StartedAt, l.EndedAt, l.UpdatedAt)
	return l, err
}

func (l *Lesson) normalize() {
	l.Notes = json.RawMessage(jsonOr(l.Notes, "[]"))
	l.Dropped = json.RawMessage(jsonOr(l.Dropped, "[]"))
	l.Steps = json.RawMessage(jsonOr(l.Steps, "[]"))
	l.Draft = json.RawMessage(jsonOr(l.Draft, "{}"))
}

// UpdateLesson 整筆寫回（id、bot_id、started_at、created_by 不變）。
func (q *Queries) UpdateLesson(ctx context.Context, l Lesson) (Lesson, error) {
	l.UpdatedAt = nowMS()
	l.normalize()
	err := affected(q.w.ExecContext(ctx, `UPDATE lesson SET goal=?, state=?, notes=?, dropped=?, steps=?, draft=?, skill_id=?, thread_id=?,
		trial_task_id=?, ended_at=?, updated_at=? WHERE id=?`,
		l.Goal, l.State, string(l.Notes), string(l.Dropped), string(l.Steps), string(l.Draft), l.SkillID, l.ThreadID, l.TrialTaskID,
		l.EndedAt, l.UpdatedAt, l.ID))
	return l, err
}

// GetLesson 讀一次示範教學。
func (q *Queries) GetLesson(ctx context.Context, id string) (Lesson, error) {
	l, err := scanLesson(q.r.QueryRowContext(ctx, `SELECT `+lessonCols+` FROM lesson WHERE id = ?`, id))
	return l, notFound(err)
}

// OpenLesson 讀一個 Bot 還沒結束的示範教學（錄製中、暫停、等你確認草稿；最新的一次）。沒有時回 ErrNotFound。
func (q *Queries) OpenLesson(ctx context.Context, botID string) (Lesson, error) {
	l, err := scanLesson(q.r.QueryRowContext(ctx, `SELECT `+lessonCols+` FROM lesson
		WHERE bot_id = ? AND state IN ('recording', 'paused', 'stopped') ORDER BY started_at DESC, id DESC LIMIT 1`, botID))
	return l, notFound(err)
}

// ListLessons 列出示範教學（新的在前）；botID 不是空白時只列那個 Bot 的，states 不是空的時只列這些狀態。
func (q *Queries) ListLessons(ctx context.Context, botID string, states ...string) ([]Lesson, error) {
	query := `SELECT ` + lessonCols + ` FROM lesson WHERE (? = '' OR bot_id = ?)`
	args := []any{botID, botID}
	if len(states) > 0 {
		query += ` AND state IN (?` + repeatComma(len(states)-1) + `)`
		for _, s := range states {
			args = append(args, s)
		}
	}
	rows, err := q.r.QueryContext(ctx, query+` ORDER BY started_at DESC, id DESC`, args...)
	return collect(rows, err, scanLesson)
}

func repeatComma(n int) string {
	out := make([]byte, 0, n*2)
	for range n {
		out = append(out, ",?"...)
	}
	return string(out)
}
