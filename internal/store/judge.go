package store

import (
	"context"
	"strings"
)

// ── judge_correction ─────────────────────────────────

// JudgeCorrection 是你對一次系統判斷的改正（X04，PRD §20 FJ-7）。X05 用來校正規則與快判的門檻。
type JudgeCorrection struct {
	ID        string `json:"id"`
	Use       string `json:"use"` // 判斷的用途，例：task_done
	TaskID    string `json:"task_id"`
	BotID     string `json:"bot_id"`
	Judged    string `json:"judged"`    // 系統的判斷
	JudgedBy  string `json:"judged_by"` // rules / model
	Reason    string `json:"reason"`    // 系統當時的原因
	Corrected string `json:"corrected"` // 你說應該是哪一種
	Note      string `json:"note"`
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const judgeCols = `id, use, task_id, bot_id, judged, judged_by, reason, corrected, note, actor, created_at, updated_at`

func scanJudge(s scanner) (JudgeCorrection, error) {
	var c JudgeCorrection
	err := s.Scan(&c.ID, &c.Use, &c.TaskID, &c.BotID, &c.Judged, &c.JudgedBy, &c.Reason, &c.Corrected, &c.Note, &c.Actor, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// PutJudgeCorrection 記下改正；同一件工作、同一個用途已經有了就換成新的（id、建立時間不變）。
func (q *Queries) PutJudgeCorrection(ctx context.Context, c JudgeCorrection) (JudgeCorrection, error) {
	if c.ID == "" {
		c.ID = NewID()
	}
	now := nowMS()
	c.CreatedAt, c.UpdatedAt = now, now
	if _, err := q.w.ExecContext(ctx, `INSERT INTO judge_correction(`+judgeCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id, use) DO UPDATE SET judged=excluded.judged, judged_by=excluded.judged_by, reason=excluded.reason,
		corrected=excluded.corrected, note=excluded.note, actor=excluded.actor, updated_at=excluded.updated_at`,
		c.ID, c.Use, c.TaskID, c.BotID, c.Judged, c.JudgedBy, c.Reason, c.Corrected, c.Note, c.Actor, c.CreatedAt, c.UpdatedAt); err != nil {
		return c, err
	}
	return q.GetJudgeCorrection(ctx, c.TaskID, c.Use)
}

// GetJudgeCorrection 讀一件工作某個用途的改正；沒有時回 ErrNotFound。
func (q *Queries) GetJudgeCorrection(ctx context.Context, taskID, use string) (JudgeCorrection, error) {
	c, err := scanJudge(q.r.QueryRowContext(ctx, `SELECT `+judgeCols+` FROM judge_correction WHERE task_id = ? AND use = ?`, taskID, use))
	return c, notFound(err)
}

// JudgeCorrectionFilter 是列出改正的條件；空白的欄位不限。
type JudgeCorrectionFilter struct {
	Use     string
	BotID   string
	TaskIDs []string
	Limit   int // 0 不限
}

// ListJudgeCorrections 列出改正，新到舊。
func (q *Queries) ListJudgeCorrections(ctx context.Context, f JudgeCorrectionFilter) ([]JudgeCorrection, error) {
	where := []string{"1=1"}
	var args []any
	if f.Use != "" {
		where, args = append(where, "use = ?"), append(args, f.Use)
	}
	if f.BotID != "" {
		where, args = append(where, "bot_id = ?"), append(args, f.BotID)
	}
	if len(f.TaskIDs) > 0 {
		where = append(where, "task_id IN (?"+strings.Repeat(",?", len(f.TaskIDs)-1)+")")
		for _, id := range f.TaskIDs {
			args = append(args, id)
		}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+judgeCols+` FROM judge_correction WHERE `+strings.Join(where, " AND ")+
		` ORDER BY updated_at DESC, id DESC LIMIT ?`, append(args, limit)...)
	return collect(rows, err, scanJudge)
}
