package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// ── room ─────────────────────────────────────────────

// 群組的回應規則（沒點名時誰回，D01）。
const (
	RuleAuto      = "auto"      // 自動挑一位（快判，X05；還沒有快判時交給主責）
	RuleLead      = "lead"      // 主責 Bot
	RuleMentioned = "mentioned" // 只回應被點名的
	RuleEveryone  = "everyone"  // 所有成員都看，不關自己的事回 [silent]（LazyBoy 的做法）
)

// Room 是一個群組（PRD §10）。
type Room struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AutoName      bool   `json:"auto_name"`
	ResponderRule string `json:"responder_rule"`
	LeadID        string `json:"lead_id"`
	Bulletin      string `json:"bulletin"`
	Cwd           string `json:"cwd"`
	CreatedBy     string `json:"created_by"`
	LastMessageAt int64  `json:"last_message_at"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

const roomCols = `id, name, auto_name, responder_rule, lead_id, bulletin, cwd, created_by, last_message_at, created_at, updated_at`

func scanRoom(s scanner) (Room, error) {
	var r Room
	var lead sql.NullString
	err := s.Scan(&r.ID, &r.Name, &r.AutoName, &r.ResponderRule, &lead, &r.Bulletin, &r.Cwd, &r.CreatedBy, &r.LastMessageAt, &r.CreatedAt, &r.UpdatedAt)
	r.LeadID = lead.String
	return r, err
}

// CreateRoom 新增群組（成員另外用 SetRoomMembers）。
func (q *Queries) CreateRoom(ctx context.Context, r Room) (Room, error) {
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.ResponderRule == "" {
		r.ResponderRule = RuleAuto
	}
	r.CreatedAt = nowMS()
	r.UpdatedAt = r.CreatedAt
	_, err := q.w.ExecContext(ctx, `INSERT INTO room(`+roomCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Name, b2i(r.AutoName), r.ResponderRule, nullStr(r.LeadID), r.Bulletin, r.Cwd, r.CreatedBy, r.LastMessageAt, r.CreatedAt, r.UpdatedAt)
	return r, err
}

// GetRoom 讀一個群組。
func (q *Queries) GetRoom(ctx context.Context, id string) (Room, error) {
	r, err := scanRoom(q.r.QueryRowContext(ctx, `SELECT `+roomCols+` FROM room WHERE id = ?`, id))
	return r, notFound(err)
}

// ListRooms 列出群組，最近有訊息的在前；botID 不是空白時只列那個 Bot 在裡面的。
func (q *Queries) ListRooms(ctx context.Context, botID string) ([]Room, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+roomCols+` FROM room WHERE ? = '' OR id IN (SELECT room_id FROM room_member WHERE bot_id = ?)
		ORDER BY MAX(last_message_at, created_at) DESC, id DESC`, botID, botID)
	return collect(rows, err, scanRoom)
}

// UpdateRoom 覆寫群組的名稱、自動命名、回應規則、主責、公告、工作資料夾。
func (q *Queries) UpdateRoom(ctx context.Context, r Room) error {
	return affected(q.w.ExecContext(ctx, `UPDATE room SET name=?, auto_name=?, responder_rule=?, lead_id=?, bulletin=?, cwd=?, updated_at=? WHERE id=?`,
		r.Name, b2i(r.AutoName), r.ResponderRule, nullStr(r.LeadID), r.Bulletin, r.Cwd, nowMS(), r.ID))
}

// TouchRoom 記下群組最後一則訊息的時間。
func (q *Queries) TouchRoom(ctx context.Context, id string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE room SET last_message_at=?, updated_at=? WHERE id=?`, now, now, id))
}

// DeleteRoom 刪群組，連同訊息、成員與座位對話串（trigger）。
func (q *Queries) DeleteRoom(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM room WHERE id=?`, id))
}

// RoomMember 是群組的一位成員。
type RoomMember struct {
	RoomID       string `json:"room_id"`
	BotID        string `json:"bot_id"`
	Position     int    `json:"position"`
	SeatThreadID string `json:"seat_thread_id"`
	SeenSeq      int64  `json:"seen_seq"`
	Paused       bool   `json:"paused"`
	JoinedAt     int64  `json:"joined_at"`
}

const memberCols = `room_id, bot_id, position, seat_thread_id, seen_seq, paused, joined_at`

func scanMember(s scanner) (RoomMember, error) {
	var m RoomMember
	var seat sql.NullString
	err := s.Scan(&m.RoomID, &m.BotID, &m.Position, &seat, &m.SeenSeq, &m.Paused, &m.JoinedAt)
	m.SeatThreadID = seat.String
	return m, err
}

// ListRoomMembers 列出群組成員（依加入順序）。
func (q *Queries) ListRoomMembers(ctx context.Context, roomID string) ([]RoomMember, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+memberCols+` FROM room_member WHERE room_id = ? ORDER BY position, joined_at`, roomID)
	return collect(rows, err, scanMember)
}

// GetRoomMember 讀一位成員。
func (q *Queries) GetRoomMember(ctx context.Context, roomID, botID string) (RoomMember, error) {
	m, err := scanMember(q.r.QueryRowContext(ctx, `SELECT `+memberCols+` FROM room_member WHERE room_id = ? AND bot_id = ?`, roomID, botID))
	return m, notFound(err)
}

// SetRoomMembers 把成員換成 botIDs（依順序）；留下來的成員保留座位、讀到哪裡與暫停狀態。
func (q *Queries) SetRoomMembers(ctx context.Context, roomID string, botIDs []string) error {
	return q.atomic(ctx, func(q *Queries) error {
		if len(botIDs) == 0 {
			_, err := q.w.ExecContext(ctx, `DELETE FROM room_member WHERE room_id = ?`, roomID)
			return err
		}
		args := []any{roomID}
		for _, id := range botIDs {
			args = append(args, id)
		}
		if _, err := q.w.ExecContext(ctx, `DELETE FROM room_member WHERE room_id = ? AND bot_id NOT IN (?`+strings.Repeat(",?", len(botIDs)-1)+`)`, args...); err != nil {
			return err
		}
		now := nowMS()
		for i, id := range botIDs {
			if _, err := q.w.ExecContext(ctx, `INSERT INTO room_member(room_id, bot_id, position, joined_at) VALUES (?,?,?,?)
				ON CONFLICT(room_id, bot_id) DO UPDATE SET position = excluded.position`, roomID, id, i, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetSeat 記下成員的座位對話串。
func (q *Queries) SetSeat(ctx context.Context, roomID, botID, threadID string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE room_member SET seat_thread_id=? WHERE room_id=? AND bot_id=?`, nullStr(threadID), roomID, botID))
}

// SetSeen 記下成員讀到群組的哪一則（message.seq）。
func (q *Queries) SetSeen(ctx context.Context, roomID, botID string, seq int64) error {
	return affected(q.w.ExecContext(ctx, `UPDATE room_member SET seen_seq=? WHERE room_id=? AND bot_id=?`, seq, roomID, botID))
}

// RestoreSeen 只在成員還停在 at 時把讀到哪裡改回 to（被打斷的一輪沒有真的讀完）。
func (q *Queries) RestoreSeen(ctx context.Context, roomID, botID string, at, to int64) error {
	_, err := q.w.ExecContext(ctx, `UPDATE room_member SET seen_seq=? WHERE room_id=? AND bot_id=? AND seen_seq=?`, to, roomID, botID, at)
	return err
}

// SetMemberPaused 暫停或恢復一位成員（RM-10）。
func (q *Queries) SetMemberPaused(ctx context.Context, roomID, botID string, paused bool) error {
	return affected(q.w.ExecContext(ctx, `UPDATE room_member SET paused=? WHERE room_id=? AND bot_id=?`, b2i(paused), roomID, botID))
}

// FindThread 找一個 Bot 的座位（roomID）或收隊友工作的對話串（peerID）；沒有時回 ErrNotFound。
func (q *Queries) FindThread(ctx context.Context, botID, roomID, peerID string) (Thread, error) {
	t, err := scanThread(q.r.QueryRowContext(ctx, `SELECT `+threadCols+` FROM thread WHERE bot_id = ?
		AND COALESCE(room_id, '') = ? AND COALESCE(peer_bot_id, '') = ? AND (? <> '' OR ? <> '') ORDER BY created_at LIMIT 1`,
		botID, roomID, peerID, roomID, peerID))
	return t, notFound(err)
}

// ── bot_message ──────────────────────────────────────

// BotMessage 是 Bot 之間的一則傳話（PRD §10）。
type BotMessage struct {
	ID           string   `json:"id"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	FromName     string   `json:"from_name"`
	ToName       string   `json:"to_name"`
	Intent       string   `json:"intent"`
	Text         string   `json:"text"`
	Hop          int      `json:"hop"`
	ChainID      string   `json:"chain_id"`
	AssignmentID string   `json:"assignment_id"`
	OriginTaskID string   `json:"origin_task_id"`
	TaskID       string   `json:"task_id"`
	RequestID    string   `json:"request_id"`
	Files        []string `json:"files"`
	Answered     bool     `json:"answered"`
	CreatedAt    int64    `json:"created_at"`
}

const botMessageCols = `id, from_bot, to_bot, from_name, to_name, intent, text, hop, chain_id, assignment_id, origin_task_id, task_id,
	request_id, files, answered, created_at`

func scanBotMessage(s scanner) (BotMessage, error) {
	var m BotMessage
	var assignment sql.NullString
	var files string
	err := s.Scan(&m.ID, &m.From, &m.To, &m.FromName, &m.ToName, &m.Intent, &m.Text, &m.Hop, &m.ChainID, &assignment, &m.OriginTaskID,
		&m.TaskID, &m.RequestID, &files, &m.Answered, &m.CreatedAt)
	m.AssignmentID = assignment.String
	if err == nil {
		m.Files, err = parseList(files)
	}
	return m, err
}

// AddBotMessage 寫一則傳話。
func (q *Queries) AddBotMessage(ctx context.Context, m BotMessage) (BotMessage, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMS()
	}
	_, err := q.w.ExecContext(ctx, `INSERT INTO bot_message(`+botMessageCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.From, m.To, m.FromName, m.ToName, m.Intent, m.Text, m.Hop, m.ChainID, nullStr(m.AssignmentID), m.OriginTaskID, m.TaskID,
		m.RequestID, strList(m.Files), b2i(m.Answered), m.CreatedAt)
	return m, err
}

// GetBotMessage 讀一則傳話。
func (q *Queries) GetBotMessage(ctx context.Context, id string) (BotMessage, error) {
	m, err := scanBotMessage(q.r.QueryRowContext(ctx, `SELECT `+botMessageCols+` FROM bot_message WHERE id = ?`, id))
	return m, notFound(err)
}

// FindBotMessageByRequest 找收件者已收過的同一個 request_id（寄件者:編號）；沒有時回 ErrNotFound。
func (q *Queries) FindBotMessageByRequest(ctx context.Context, to, requestID string) (BotMessage, error) {
	m, err := scanBotMessage(q.r.QueryRowContext(ctx, `SELECT `+botMessageCols+` FROM bot_message WHERE to_bot = ? AND request_id = ?`, to, requestID))
	return m, notFound(err)
}

// SetBotMessageTask 記下傳話在收件者那邊變成的工作。
func (q *Queries) SetBotMessageTask(ctx context.Context, id, taskID string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE bot_message SET task_id=? WHERE id=?`, taskID, id))
}

// MarkBotMessageAnswered 標記要回覆的傳話已經有結果（或不會有了）。
func (q *Queries) MarkBotMessageAnswered(ctx context.Context, id string) error {
	_, err := q.w.ExecContext(ctx, `UPDATE bot_message SET answered=1 WHERE id=?`, id)
	return err
}

// BotMessageFilter 是列出傳話的條件；空白的欄位不限制。
type BotMessageFilter struct {
	OriginTaskID string   // 寄件者在這件工作裡發出的
	To           string   // 寄給這個 Bot 的
	Pair         []string // 兩個 Bot 之間（兩個方向都算）
	ChainID      string
	Intents      []string
	Unanswered   bool
	Limit        int // 0 不限；有給時回最新的 Limit 則（仍由舊到新）
}

// ListBotMessages 列出傳話，由舊到新。
func (q *Queries) ListBotMessages(ctx context.Context, f BotMessageFilter) ([]BotMessage, error) {
	where := []string{"1=1"}
	var args []any
	if f.OriginTaskID != "" {
		where, args = append(where, "origin_task_id = ?"), append(args, f.OriginTaskID)
	}
	if f.To != "" {
		where, args = append(where, "to_bot = ?"), append(args, f.To)
	}
	if len(f.Pair) == 2 {
		where = append(where, "((from_bot = ? AND to_bot = ?) OR (from_bot = ? AND to_bot = ?))")
		args = append(args, f.Pair[0], f.Pair[1], f.Pair[1], f.Pair[0])
	}
	if f.ChainID != "" {
		where, args = append(where, "chain_id = ?"), append(args, f.ChainID)
	}
	if len(f.Intents) > 0 {
		where = append(where, "intent IN (?"+strings.Repeat(",?", len(f.Intents)-1)+")")
		for _, i := range f.Intents {
			args = append(args, i)
		}
	}
	if f.Unanswered {
		where = append(where, "answered = 0")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT * FROM (SELECT `+botMessageCols+` FROM bot_message WHERE `+strings.Join(where, " AND ")+`
		ORDER BY created_at DESC, id DESC LIMIT ?) ORDER BY created_at, id`, append(args, limit)...)
	return collect(rows, err, scanBotMessage)
}

// ── assignment ───────────────────────────────────────

// 交辦的狀態。
const (
	AssignQueued    = "queued"
	AssignRunning   = "running"
	AssignDeferred  = "deferred"
	AssignDone      = "done"
	AssignFailed    = "failed"
	AssignCancelled = "cancelled"
)

// Assignment 是交辦樹的一個節點（PRD §10、AA-10）。
type Assignment struct {
	ID            string          `json:"id"`
	RootID        string          `json:"root_id"`
	ParentID      string          `json:"parent_id"`
	Requester     string          `json:"requester"`
	Assignee      string          `json:"assignee"`
	Mode          string          `json:"mode"`
	Status        string          `json:"status"`
	MessageID     string          `json:"message_id"`
	TaskID        string          `json:"task_id"`
	DeferredReply json.RawMessage `json:"deferred_reply"`
	BudgetTokens  int64           `json:"budget_tokens"`
	UsedTokens    int64           `json:"used_tokens"`
	CreatedAt     int64           `json:"created_at"`
	UpdatedAt     int64           `json:"updated_at"`
}

const assignmentCols = `id, root_id, parent_id, requester, assignee, mode, status, message_id, task_id, deferred_reply, budget_tokens, used_tokens,
	created_at, updated_at`

func scanAssignment(s scanner) (Assignment, error) {
	var a Assignment
	var parent sql.NullString
	var deferred string
	err := s.Scan(&a.ID, &a.RootID, &parent, &a.Requester, &a.Assignee, &a.Mode, &a.Status, &a.MessageID, &a.TaskID, &deferred,
		&a.BudgetTokens, &a.UsedTokens, &a.CreatedAt, &a.UpdatedAt)
	a.ParentID, a.DeferredReply = parent.String, json.RawMessage(deferred)
	return a, err
}

// CreateAssignment 新增交辦。
func (q *Queries) CreateAssignment(ctx context.Context, a Assignment) (Assignment, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.Status == "" {
		a.Status = AssignQueued
	}
	a.CreatedAt = nowMS()
	a.UpdatedAt = a.CreatedAt
	a.DeferredReply = json.RawMessage(jsonOr(a.DeferredReply, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO assignment(`+assignmentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.RootID, nullStr(a.ParentID), a.Requester, a.Assignee, a.Mode, a.Status, a.MessageID, a.TaskID, string(a.DeferredReply),
		a.BudgetTokens, a.UsedTokens, a.CreatedAt, a.UpdatedAt)
	return a, err
}

// GetAssignment 讀一筆交辦。
func (q *Queries) GetAssignment(ctx context.Context, id string) (Assignment, error) {
	a, err := scanAssignment(q.r.QueryRowContext(ctx, `SELECT `+assignmentCols+` FROM assignment WHERE id = ?`, id))
	return a, notFound(err)
}

// AssignmentForTask 找收件者這件工作對應的交辦；沒有時回 ErrNotFound。
func (q *Queries) AssignmentForTask(ctx context.Context, taskID string) (Assignment, error) {
	a, err := scanAssignment(q.r.QueryRowContext(ctx, `SELECT `+assignmentCols+` FROM assignment WHERE task_id = ? ORDER BY created_at LIMIT 1`, taskID))
	return a, notFound(err)
}

// ListAssignments 列出一條工作鏈的交辦（由舊到新）。
func (q *Queries) ListAssignments(ctx context.Context, rootID string) ([]Assignment, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+assignmentCols+` FROM assignment WHERE root_id = ? ORDER BY created_at, id`, rootID)
	return collect(rows, err, scanAssignment)
}

// SetAssignmentTask 記下交辦變成的工作。
func (q *Queries) SetAssignmentTask(ctx context.Context, id, taskID string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE assignment SET task_id=?, updated_at=? WHERE id=?`, taskID, nowMS(), id))
}

// SetAssignmentStatus 改交辦狀態；usedTokens >= 0 時一起記下用量。
func (q *Queries) SetAssignmentStatus(ctx context.Context, id, status string, usedTokens int64) error {
	return affected(q.w.ExecContext(ctx, `UPDATE assignment SET status=?, used_tokens = CASE WHEN ? >= 0 THEN ? ELSE used_tokens END, updated_at=? WHERE id=?`,
		status, usedTokens, usedTokens, nowMS(), id))
}

// ── task meta、工作鏈用量 ──────────────────────────────

// PatchTaskMeta 把 patch 合併進 task.meta（JSON merge patch：值是 null 的欄位會刪掉）。
func (q *Queries) PatchTaskMeta(ctx context.Context, id string, patch any) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return affected(q.w.ExecContext(ctx, `UPDATE task SET meta = json_patch(meta, ?), updated_at=? WHERE id=?`, string(raw), nowMS(), id))
}

// ListTasksByMeta 列出 meta 裡 key 等於 value 的任務（由舊到新）；openOnly 時只列還沒完成的。key 是 JSON 路徑，例：$.room_id。
func (q *Queries) ListTasksByMeta(ctx context.Context, key, value string, openOnly bool) ([]Task, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+taskCols+` FROM task WHERE json_extract(meta, ?) = ? AND (NOT ? OR state <> 'done')
		ORDER BY created_at, id`, key, value, openOnly)
	return collect(rows, err, scanTask)
}

// ChainTokens 回傳一條工作鏈（開頭那件工作與 meta.chain 是它的工作）用掉的 token（輸入＋輸出）。
func (q *Queries) ChainTokens(ctx context.Context, chainID string) (int64, error) {
	var n int64
	err := q.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(u.input_tokens + u.output_tokens), 0) FROM usage_entry u
		JOIN task t ON t.id = u.task_id WHERE t.id = ? OR json_extract(t.meta, '$.chain') = ?`, chainID, chainID).Scan(&n)
	return n, err
}

// SetTaskCancelledIfQueued 只在任務還在排隊時把它改成已取消（避免和開始執行搶）；回傳有沒有改到。
func (q *Queries) SetTaskCancelledIfQueued(ctx context.Context, id string) (bool, error) {
	return q.SetTaskDoneIfQueued(ctx, id, "cancelled")
}

// SetTaskDoneIfQueued 只在任務還在排隊時把它結束成 verdict（例：cancelled、merged＝併進別的一輪）；回傳有沒有改到。
func (q *Queries) SetTaskDoneIfQueued(ctx context.Context, id, verdict string) (bool, error) {
	now := nowMS()
	res, err := q.w.ExecContext(ctx, `UPDATE task SET state='done', verdict=?, finished_at=?, updated_at=? WHERE id=? AND state='queued'`,
		verdict, now, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RequeueTask 讓一件還沒結束（不是排隊中）的工作重新排隊：換成新的輸入、把 patch 併進 meta。
// 服務重開時做到一半的工作接著做用；已經結束或本來就在排隊的不動（回 ErrNotFound）。
func (q *Queries) RequeueTask(ctx context.Context, id, input string, patch any) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return affected(q.w.ExecContext(ctx, `UPDATE task SET state='queued', input=?, meta=json_patch(meta, ?), updated_at=?
		WHERE id=? AND state NOT IN ('done', 'queued')`, input, string(raw), nowMS(), id))
}

// ListMessagesAfter 讀一個範圍裡 seq 比 after 大的訊息（由舊到新，最多 limit 則；0 不限）。
func (q *Queries) ListMessagesAfter(ctx context.Context, scope, scopeID string, after int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+messageCols+` FROM message WHERE scope = ? AND scope_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		scope, scopeID, after, limit)
	return collect(rows, err, scanMessage)
}

// TaskTokens 回傳一件工作用掉的 token（輸入＋輸出）。
func (q *Queries) TaskTokens(ctx context.Context, taskID string) (int64, error) {
	var n int64
	err := q.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens + output_tokens), 0) FROM usage_entry WHERE task_id = ?`, taskID).Scan(&n)
	return n, err
}

// SetTaskPriority 改任務的優先順序（你在群組發言時，已經排隊的座位改成你的、排到前面）。
func (q *Queries) SetTaskPriority(ctx context.Context, id string, low bool) error {
	return affected(q.w.ExecContext(ctx, `UPDATE task SET low_priority=?, updated_at=? WHERE id=?`, b2i(low), nowMS(), id))
}
