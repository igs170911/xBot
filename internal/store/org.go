package store

import (
	"context"
	"database/sql"
)

// ── team ─────────────────────────────────────────────

// Team 是一個團隊（PRD §10 team、TM-1）：側欄的一個分區，一位主管帶成員（bot.team_id 指向它）。
type Team struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	About     string   `json:"about"`
	ChiefID   string   `json:"chief_id"`
	Memory    []string `json:"memory"`
	RoomID    string   `json:"room_id"`
	Template  string   `json:"template"`
	Position  int      `json:"position"`
	CreatedBy string   `json:"created_by"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

const teamCols = `id, name, about, chief_id, memory, room_id, template, position, created_by, created_at, updated_at`

func scanTeam(s scanner) (Team, error) {
	var t Team
	var chief, room sql.NullString
	var memory string
	err := s.Scan(&t.ID, &t.Name, &t.About, &chief, &memory, &room, &t.Template, &t.Position, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	t.ChiefID, t.RoomID = chief.String, room.String
	t.Memory, err = parseList(memory)
	if t.Memory == nil {
		t.Memory = []string{}
	}
	return t, err
}

// CreateTeam 新增團隊；Position 是 0 時排在最後。
func (q *Queries) CreateTeam(ctx context.Context, t Team) (Team, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.Position == 0 {
		var n int
		if err := q.w.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM team`).Scan(&n); err != nil {
			return t, err
		}
		t.Position = n + 1
	}
	t.CreatedAt = nowMS()
	t.UpdatedAt = t.CreatedAt
	_, err := q.w.ExecContext(ctx, `INSERT INTO team(`+teamCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.About, nullStr(t.ChiefID), strList(t.Memory), nullStr(t.RoomID), t.Template, t.Position, t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return t, err
	}
	return q.getTeam(ctx, q.w, t.ID)
}

// GetTeam 讀一個團隊。
func (q *Queries) GetTeam(ctx context.Context, id string) (Team, error) {
	return q.getTeam(ctx, q.r, id)
}

func (q *Queries) getTeam(ctx context.Context, db querier, id string) (Team, error) {
	t, err := scanTeam(db.QueryRowContext(ctx, `SELECT `+teamCols+` FROM team WHERE id = ?`, id))
	return t, notFound(err)
}

// FindTeamByName 依名稱找團隊。
func (q *Queries) FindTeamByName(ctx context.Context, name string) (Team, error) {
	t, err := scanTeam(q.r.QueryRowContext(ctx, `SELECT `+teamCols+` FROM team WHERE name = ?`, name))
	return t, notFound(err)
}

// ListTeams 列出團隊（依側欄順序）。
func (q *Queries) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+teamCols+` FROM team ORDER BY position, created_at, id`)
	return collect(rows, err, scanTeam)
}

// UpdateTeam 覆寫團隊的名稱、說明、主管、記憶、群組、順序。
func (q *Queries) UpdateTeam(ctx context.Context, t Team) (Team, error) {
	err := affected(q.w.ExecContext(ctx, `UPDATE team SET name=?, about=?, chief_id=?, memory=?, room_id=?, position=?, updated_at=? WHERE id=?`,
		t.Name, t.About, nullStr(t.ChiefID), strList(t.Memory), nullStr(t.RoomID), t.Position, nowMS(), t.ID))
	if err != nil {
		return t, err
	}
	return q.getTeam(ctx, q.w, t.ID)
}

// DeleteTeam 刪除團隊；成員回到「未分區」（trigger），Bot 本身不受影響。
func (q *Queries) DeleteTeam(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM team WHERE id=?`, id))
}

// SetBotOrg 只改 Bot 的團隊與報告對象（空白＝未分區／直接向你報告），記下誰改的。
func (q *Queries) SetBotOrg(ctx context.Context, botID, teamID, reportsTo, by string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE bot SET team_id=?, reports_to=?, updated_at=?, updated_by=? WHERE id=?`,
		nullStr(teamID), nullStr(reportsTo), nowMS(), by, botID))
}

// SetBotCanManage 開關 Bot 的團隊管理權限。
func (q *Queries) SetBotCanManage(ctx context.Context, botID string, on bool, by string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE bot SET can_manage=?, updated_at=?, updated_by=? WHERE id=?`, b2i(on), nowMS(), by, botID))
}

// CountBotsCreatedSince 數 actor（例：bot:<id>）從 since（unix 毫秒）之後建立了幾個 Bot（Bot 建 Bot 的每日上限，LP-9）。
func (q *Queries) CountBotsCreatedSince(ctx context.Context, actor string, since int64) (int, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM bot WHERE created_by = ? AND created_at > ?`, actor, since).Scan(&n)
	return n, err
}

// CountBots 數全部 Bot（含封存的；全部最多 50 個）。
func (q *Queries) CountBots(ctx context.Context) (int, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM bot`).Scan(&n)
	return n, err
}

// ── agenda ───────────────────────────────────────────

// 提案與討論的狀態（TM-5）。
const (
	AgendaOpen      = "open"
	AgendaHeld      = "held"
	AgendaAccepted  = "accepted"
	AgendaVetoed    = "vetoed"
	AgendaAuto      = "auto"
	AgendaAnswered  = "answered"
	AgendaDeclined  = "declined"
	AgendaCancelled = "cancelled"
)

// Agenda 是 Bot 送來等你的一件事：提案（propose）或討論（discuss）。
type Agenda struct {
	ID        string `json:"id"`
	BotID     string `json:"bot_id"`
	Kind      string `json:"kind"`
	Subject   string `json:"subject"`
	Details   string `json:"details"`
	DueAt     int64  `json:"due_at"`
	State     string `json:"state"`
	Grace     bool   `json:"grace"`
	Note      string `json:"note"`
	ThreadID  string `json:"thread_id"`
	TaskID    string `json:"task_id"`
	FromBot   string `json:"from_bot"`
	FromName  string `json:"from_name"`
	DecidedAt int64  `json:"decided_at"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const agendaCols = `id, bot_id, kind, subject, details, due_at, state, grace, note, thread_id, task_id, from_bot, from_name, decided_at, created_at, updated_at`

func scanAgenda(s scanner) (Agenda, error) {
	var a Agenda
	var thread, task sql.NullString
	err := s.Scan(&a.ID, &a.BotID, &a.Kind, &a.Subject, &a.Details, &a.DueAt, &a.State, &a.Grace, &a.Note, &thread, &task, &a.FromBot, &a.FromName,
		&a.DecidedAt, &a.CreatedAt, &a.UpdatedAt)
	a.ThreadID, a.TaskID = thread.String, task.String
	return a, err
}

// CreateAgenda 新增一件提案或討論（state 空白時是 open）。
func (q *Queries) CreateAgenda(ctx context.Context, a Agenda) (Agenda, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.State == "" {
		a.State = AgendaOpen
	}
	a.CreatedAt = nowMS()
	a.UpdatedAt = a.CreatedAt
	_, err := q.w.ExecContext(ctx, `INSERT INTO agenda(`+agendaCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.BotID, a.Kind, a.Subject, a.Details, a.DueAt, a.State, b2i(a.Grace), a.Note, nullStr(a.ThreadID), nullStr(a.TaskID), a.FromBot, a.FromName,
		a.DecidedAt, a.CreatedAt, a.UpdatedAt)
	return a, err
}

// GetAgenda 讀一件。
func (q *Queries) GetAgenda(ctx context.Context, id string) (Agenda, error) {
	a, err := scanAgenda(q.w.QueryRowContext(ctx, `SELECT `+agendaCols+` FROM agenda WHERE id = ?`, id))
	return a, notFound(err)
}

// AgendaFilter 是列提案與討論的條件；空白的欄位不限。
type AgendaFilter struct {
	States []string
	BotID  string
	Kind   string
}

// ListAgenda 依條件列出，依時間（due_at）先後。
func (q *Queries) ListAgenda(ctx context.Context, f AgendaFilter) ([]Agenda, error) {
	where, args := "1=1", []any{}
	if len(f.States) > 0 {
		where += " AND state IN (?"
		for range f.States[1:] {
			where += ",?"
		}
		where += ")"
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	if f.BotID != "" {
		where, args = where+" AND bot_id = ?", append(args, f.BotID)
	}
	if f.Kind != "" {
		where, args = where+" AND kind = ?", append(args, f.Kind)
	}
	rows, err := q.w.QueryContext(ctx, `SELECT `+agendaCols+` FROM agenda WHERE `+where+` ORDER BY due_at, created_at, id`, args...)
	return collect(rows, err, scanAgenda)
}

// SettleAgenda 把狀態是 from 之一的那件改成 state（記下備註與決定時間）；狀態已經變了回 ErrAlreadyDecided。
// 用條件更新，避免你回覆的同時到期處理也在改同一件（LazyBoy tick 的重讀）。
func (q *Queries) SettleAgenda(ctx context.Context, id string, from []string, state, note string, decidedAt int64) error {
	args := []any{state, note, decidedAt, nowMS(), id}
	cond := "state IN (?"
	for range from[1:] {
		cond += ",?"
	}
	cond += ")"
	for _, s := range from {
		args = append(args, s)
	}
	res, err := q.w.ExecContext(ctx, `UPDATE agenda SET state=?, note=?, decided_at=?, updated_at=? WHERE id=? AND `+cond, args...)
	return q.decided(ctx, res, err, `SELECT 1 FROM agenda WHERE id = ?`, id)
}

// MoveAgenda 改時間（open 的才能改）；grace 是「已經因為錯過而延過一次」。
func (q *Queries) MoveAgenda(ctx context.Context, id string, dueAt int64, grace bool) error {
	res, err := q.w.ExecContext(ctx, `UPDATE agenda SET due_at=?, grace=?, updated_at=? WHERE id=? AND state='open'`, dueAt, b2i(grace), nowMS(), id)
	return q.decided(ctx, res, err, `SELECT 1 FROM agenda WHERE id = ?`, id)
}

// RestoreAgenda 把一件改回原本的狀態（送不到 Bot 時，讓你的回覆可以再給一次）。
func (q *Queries) RestoreAgenda(ctx context.Context, a Agenda) error {
	return affected(q.w.ExecContext(ctx, `UPDATE agenda SET state=?, note=?, decided_at=?, updated_at=? WHERE id=?`, a.State, a.Note, a.DecidedAt, nowMS(), a.ID))
}

// LastVetoed 回傳 Bot 最近一次被否決的同主題提案的否決時間（沒有是 0）。
func (q *Queries) LastVetoed(ctx context.Context, botID, subject string) (int64, error) {
	var at sql.NullInt64
	err := q.r.QueryRowContext(ctx, `SELECT MAX(decided_at) FROM agenda WHERE bot_id=? AND subject=? AND state='vetoed'`, botID, subject).Scan(&at)
	return at.Int64, err
}

// TaskReply 是 Bot 在一件工作的最後一則回覆（主管看最近完成的結果用）。
func (q *Queries) TaskReply(ctx context.Context, threadID, taskID string) (Message, error) {
	m, err := scanMessage(q.r.QueryRowContext(ctx, `SELECT `+messageCols+` FROM message WHERE scope = 'thread' AND scope_id = ?
		AND sender LIKE 'bot:%' AND kind = 'text' AND json_extract(meta, '$.task_id') = ? ORDER BY seq DESC LIMIT 1`, threadID, taskID))
	return m, notFound(err)
}
