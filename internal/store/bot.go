package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"xbot/internal/limits"
)

// jsonOr 把 JSON 欄位轉成要存的文字；空的就用 def（"{}" 或 "[]"）。
func jsonOr(raw json.RawMessage, def string) string {
	if len(raw) == 0 {
		return def
	}
	return string(raw)
}

func strList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseList(s string) ([]string, error) {
	var v []string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// ── bot ──────────────────────────────────────────────

// Bot 是一個 Bot 的設定（PRD §10）。JSON 欄位原樣存放，由上層決定格式。
type Bot struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Soul           string          `json:"soul"`
	Color          string          `json:"color"`
	Avatar         json.RawMessage `json:"avatar"`
	Engine         string          `json:"engine"`
	Model          string          `json:"model"`
	Effort         string          `json:"effort"`
	Fallback       []string        `json:"fallback"`
	ApprovalMode   string          `json:"approval_mode"`
	ToolScope      json.RawMessage `json:"tool_scope"`
	ContactsPolicy json.RawMessage `json:"contacts_policy"`
	TeamID         string          `json:"team_id"`
	ReportsTo      string          `json:"reports_to"`
	IsChief        bool            `json:"is_chief"`
	BoxLimits      json.RawMessage `json:"box_limits"`
	IdleMinutes    int             `json:"idle_minutes"`
	Budget         json.RawMessage `json:"budget"`
	MemoryEnabled  bool            `json:"memory_enabled"`
	Voice          string          `json:"voice"`
	Archived       bool            `json:"archived"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

const botCols = `id, name, title, description, soul, color, avatar, engine, model, effort, fallback,
	approval_mode, tool_scope, contacts_policy, team_id, reports_to, is_chief, box_limits, idle_minutes,
	budget, memory_enabled, voice, archived, created_at, updated_at`

func scanBot(s scanner) (Bot, error) {
	var b Bot
	var avatar, fallback, toolScope, contacts, boxLimits, budget string
	var team, reports sql.NullString
	err := s.Scan(&b.ID, &b.Name, &b.Title, &b.Description, &b.Soul, &b.Color, &avatar, &b.Engine, &b.Model,
		&b.Effort, &fallback, &b.ApprovalMode, &toolScope, &contacts, &team, &reports, &b.IsChief, &boxLimits,
		&b.IdleMinutes, &budget, &b.MemoryEnabled, &b.Voice, &b.Archived, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return b, err
	}
	b.Avatar, b.ToolScope, b.ContactsPolicy = json.RawMessage(avatar), json.RawMessage(toolScope), json.RawMessage(contacts)
	b.BoxLimits, b.Budget = json.RawMessage(boxLimits), json.RawMessage(budget)
	b.TeamID, b.ReportsTo = team.String, reports.String
	b.Fallback, err = parseList(fallback)
	return b, err
}

func (b *Bot) args() []any {
	return []any{b.Name, b.Title, b.Description, b.Soul, b.Color, jsonOr(b.Avatar, "{}"), b.Engine, b.Model,
		b.Effort, strList(b.Fallback), b.ApprovalMode, jsonOr(b.ToolScope, "{}"), jsonOr(b.ContactsPolicy, "{}"),
		nullStr(b.TeamID), nullStr(b.ReportsTo), b2i(b.IsChief), jsonOr(b.BoxLimits, "{}"), b.IdleMinutes,
		jsonOr(b.Budget, "{}"), b2i(b.MemoryEnabled), b.Voice, b2i(b.Archived)}
}

// CreateBot 新增一個 Bot。ID 空白會自動產生；IdleMinutes 為 0 時用預設值。回傳存好的 Bot。
func (q *Queries) CreateBot(ctx context.Context, b Bot) (Bot, error) {
	if b.ID == "" {
		b.ID = NewID()
	}
	if b.IdleMinutes == 0 {
		b.IdleMinutes = limits.BoxIdleMinutes
	}
	b.CreatedAt = nowMS()
	b.UpdatedAt = b.CreatedAt
	args := append([]any{b.ID}, b.args()...)
	args = append(args, b.CreatedAt, b.UpdatedAt)
	_, err := q.w.ExecContext(ctx, `INSERT INTO bot(`+botCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	if err != nil {
		return b, err
	}
	return q.getBot(ctx, q.w, b.ID)
}

// GetBot 讀一個 Bot。
func (q *Queries) GetBot(ctx context.Context, id string) (Bot, error) {
	return q.getBot(ctx, q.r, id)
}

func (q *Queries) getBot(ctx context.Context, db querier, id string) (Bot, error) {
	b, err := scanBot(db.QueryRowContext(ctx, `SELECT `+botCols+` FROM bot WHERE id = ?`, id))
	return b, notFound(err)
}

// ListBots 列出 Bot（依建立順序）；includeArchived 為 false 時不含已封存的。
func (q *Queries) ListBots(ctx context.Context, includeArchived bool) ([]Bot, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+botCols+` FROM bot WHERE archived = 0 OR ? ORDER BY created_at, id`, includeArchived)
	return collect(rows, err, scanBot)
}

// UpdateBot 整筆覆寫一個 Bot 的可改欄位（id、created_at 不變）。
func (q *Queries) UpdateBot(ctx context.Context, b Bot) (Bot, error) {
	args := append(b.args(), nowMS(), b.ID)
	err := affected(q.w.ExecContext(ctx, `UPDATE bot SET name=?, title=?, description=?, soul=?, color=?, avatar=?,
		engine=?, model=?, effort=?, fallback=?, approval_mode=?, tool_scope=?, contacts_policy=?, team_id=?,
		reports_to=?, is_chief=?, box_limits=?, idle_minutes=?, budget=?, memory_enabled=?, voice=?, archived=?,
		updated_at=? WHERE id=?`, args...))
	if err != nil {
		return b, err
	}
	return q.getBot(ctx, q.w, b.ID)
}

// SetBotArchived 封存或取消封存。
func (q *Queries) SetBotArchived(ctx context.Context, id string, archived bool) error {
	return affected(q.w.ExecContext(ctx, `UPDATE bot SET archived=?, updated_at=? WHERE id=?`, b2i(archived), nowMS(), id))
}

// DeleteBot 刪除 Bot；它的電腦、對話串、任務、事件、核准、待我處理會一起刪掉（外鍵 CASCADE）。
func (q *Queries) DeleteBot(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM bot WHERE id=?`, id))
}

// ── host ─────────────────────────────────────────────

// Host 是一台 Docker 主機（PRD §16）。
type Host struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"` // local / ssh
	Address    string          `json:"address"`
	Status     string          `json:"status"` // unknown / online / offline / updating
	Arch       string          `json:"arch"`
	MaxRunning int             `json:"max_running"`
	Resources  json.RawMessage `json:"resources"`
	LastSeenAt int64           `json:"last_seen_at"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

const hostCols = `id, name, kind, address, status, arch, max_running, resources, last_seen_at, created_at, updated_at`

func scanHost(s scanner) (Host, error) {
	var h Host
	var res string
	err := s.Scan(&h.ID, &h.Name, &h.Kind, &h.Address, &h.Status, &h.Arch, &h.MaxRunning, &res, &h.LastSeenAt, &h.CreatedAt, &h.UpdatedAt)
	h.Resources = json.RawMessage(res)
	return h, err
}

// CreateHost 新增主機。Status 空白時為 unknown。
func (q *Queries) CreateHost(ctx context.Context, h Host) (Host, error) {
	if h.ID == "" {
		h.ID = NewID()
	}
	if h.Status == "" {
		h.Status = "unknown"
	}
	h.CreatedAt = nowMS()
	h.UpdatedAt = h.CreatedAt
	_, err := q.w.ExecContext(ctx, `INSERT INTO host(`+hostCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Name, h.Kind, h.Address, h.Status, h.Arch, h.MaxRunning, jsonOr(h.Resources, "{}"), h.LastSeenAt, h.CreatedAt, h.UpdatedAt)
	if err != nil {
		return h, err
	}
	h.Resources = json.RawMessage(jsonOr(h.Resources, "{}"))
	return h, nil
}

// GetHost 讀一台主機。
func (q *Queries) GetHost(ctx context.Context, id string) (Host, error) {
	h, err := scanHost(q.r.QueryRowContext(ctx, `SELECT `+hostCols+` FROM host WHERE id = ?`, id))
	return h, notFound(err)
}

// ListHosts 列出所有主機。
func (q *Queries) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+hostCols+` FROM host ORDER BY created_at, id`)
	return collect(rows, err, scanHost)
}

// UpdateHost 覆寫主機的名稱、位址、上限等設定。
func (q *Queries) UpdateHost(ctx context.Context, h Host) error {
	return affected(q.w.ExecContext(ctx, `UPDATE host SET name=?, kind=?, address=?, arch=?, max_running=?, resources=?, updated_at=? WHERE id=?`,
		h.Name, h.Kind, h.Address, h.Arch, h.MaxRunning, jsonOr(h.Resources, "{}"), nowMS(), h.ID))
}

// SetHostStatus 記錄健康檢查結果；online 時同時更新 last_seen_at。
func (q *Queries) SetHostStatus(ctx context.Context, id, status string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE host SET status=?,
		last_seen_at = CASE WHEN ? = 'online' THEN ? ELSE last_seen_at END, updated_at=? WHERE id=?`, status, status, now, now, id))
}

// DeleteHost 移除主機；上面還有 Bot 電腦時會失敗（外鍵 RESTRICT）。
func (q *Queries) DeleteHost(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM host WHERE id=?`, id))
}

// ── box ──────────────────────────────────────────────

// Box 是 Bot 的電腦（容器）狀態。
type Box struct {
	BotID        string          `json:"bot_id"`
	HostID       string          `json:"host_id"`
	ContainerID  string          `json:"container_id"`
	ImageRev     string          `json:"image_rev"`
	State        string          `json:"state"` // none / creating / running / sleeping / updating / error
	Error        string          `json:"error"`
	Limits       json.RawMessage `json:"limits"`
	LastActiveAt int64           `json:"last_active_at"`
	NoVNCPort    int             `json:"novnc_port"`
	CreatedAt    int64           `json:"created_at"`
	UpdatedAt    int64           `json:"updated_at"`
}

const boxCols = `bot_id, host_id, container_id, image_rev, state, error, limits, last_active_at, novnc_port, created_at, updated_at`

func scanBox(s scanner) (Box, error) {
	var b Box
	var host sql.NullString
	var lim string
	err := s.Scan(&b.BotID, &host, &b.ContainerID, &b.ImageRev, &b.State, &b.Error, &lim, &b.LastActiveAt, &b.NoVNCPort, &b.CreatedAt, &b.UpdatedAt)
	b.HostID, b.Limits = host.String, json.RawMessage(lim)
	return b, err
}

// UpsertBox 寫入 Bot 電腦的完整狀態（沒有就新增）。State 空白時為 none。
func (q *Queries) UpsertBox(ctx context.Context, b Box) error {
	if b.State == "" {
		b.State = "none"
	}
	now := nowMS()
	_, err := q.w.ExecContext(ctx, `INSERT INTO box(`+boxCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(bot_id) DO UPDATE SET host_id=excluded.host_id, container_id=excluded.container_id,
		image_rev=excluded.image_rev, state=excluded.state, error=excluded.error, limits=excluded.limits,
		last_active_at=excluded.last_active_at, novnc_port=excluded.novnc_port, updated_at=excluded.updated_at`,
		b.BotID, nullStr(b.HostID), b.ContainerID, b.ImageRev, b.State, b.Error, jsonOr(b.Limits, "{}"), b.LastActiveAt, b.NoVNCPort, now, now)
	return err
}

// GetBox 讀 Bot 的電腦。
func (q *Queries) GetBox(ctx context.Context, botID string) (Box, error) {
	b, err := scanBox(q.r.QueryRowContext(ctx, `SELECT `+boxCols+` FROM box WHERE bot_id = ?`, botID))
	return b, notFound(err)
}

// ListBoxes 列出電腦；hostID 不是空白時只列那台主機上的。
func (q *Queries) ListBoxes(ctx context.Context, hostID string) ([]Box, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+boxCols+` FROM box WHERE ? = '' OR host_id = ? ORDER BY created_at, bot_id`, hostID, hostID)
	return collect(rows, err, scanBox)
}

// SetBoxState 改電腦狀態與錯誤訊息。
func (q *Queries) SetBoxState(ctx context.Context, botID, state, errMsg string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE box SET state=?, error=?, updated_at=? WHERE bot_id=?`, state, errMsg, nowMS(), botID))
}

// TouchBox 記錄電腦最後一次被使用的時間（閒置休眠用）。
func (q *Queries) TouchBox(ctx context.Context, botID string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE box SET last_active_at=?, updated_at=? WHERE bot_id=?`, now, now, botID))
}
