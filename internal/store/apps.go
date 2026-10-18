package store

import (
	"context"
	"encoding/json"
)

// ── app_connection ───────────────────────────────────

// AppConnection 是一個應用程式連接（C34）：目錄裡的應用程式的一個帳號，或一個自訂 MCP。
// Secret 是加密後的祕密（權杖、金鑰、標頭值、環境變數值），只有 apps 套件用 Model Proxy 的金鑰解開，永遠不回傳給畫面。
type AppConnection struct {
	ID            string    `json:"id"`
	AppID         string    `json:"app_id"` // 目錄 id；自訂的是 custom
	Name          string    `json:"name"`   // Bot 設定裡的名字（mcp_servers.<name>）
	Title         string    `json:"title"`
	Transport     string    `json:"transport"` // http / stdio
	URL           string    `json:"url"`
	Command       string    `json:"command"`
	Args          []string  `json:"args"`
	Auth          string    `json:"auth"` // none / api_key / oauth2
	Account       string    `json:"account"`
	Secret        []byte    `json:"-"`
	SecretHint    string    `json:"secret_hint"`
	HeaderNames   []string  `json:"header_names"`
	EnvNames      []string  `json:"env_names"`
	Status        string    `json:"status"` // ok / needs_signin / expired / error / disabled / checking
	Error         string    `json:"error"`
	Tools         []AppTool `json:"tools"`
	DisabledTools []string  `json:"disabled_tools"`
	AllBots       bool      `json:"all_bots"`
	Enabled       bool      `json:"enabled"`
	CheckedAt     int64     `json:"checked_at"`
	LastUsedAt    int64     `json:"last_used_at"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     int64     `json:"created_at"`
	UpdatedAt     int64     `json:"updated_at"`
}

// AppTool 是連接提供的一個工具（測試連線或 Bot 用過時記下）。
type AppTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

const appCols = `id, app_id, name, title, transport, url, command, args, auth, account, secret, secret_hint, header_names, env_names,
	status, error, tools, disabled_tools, all_bots, enabled, checked_at, last_used_at, created_by, created_at, updated_at`

func scanApp(s scanner) (AppConnection, error) {
	var a AppConnection
	var args, headers, envs, tools, disabled string
	var allBots, enabled int
	err := s.Scan(&a.ID, &a.AppID, &a.Name, &a.Title, &a.Transport, &a.URL, &a.Command, &args, &a.Auth, &a.Account, &a.Secret, &a.SecretHint,
		&headers, &envs, &a.Status, &a.Error, &tools, &disabled, &allBots, &enabled, &a.CheckedAt, &a.LastUsedAt, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.AllBots, a.Enabled = allBots != 0, enabled != 0
	for _, f := range []struct {
		raw string
		dst any
	}{{args, &a.Args}, {headers, &a.HeaderNames}, {envs, &a.EnvNames}, {tools, &a.Tools}, {disabled, &a.DisabledTools}} {
		_ = json.Unmarshal([]byte(f.raw), f.dst)
	}
	if a.Args == nil {
		a.Args = []string{}
	}
	if a.HeaderNames == nil {
		a.HeaderNames = []string{}
	}
	if a.EnvNames == nil {
		a.EnvNames = []string{}
	}
	if a.Tools == nil {
		a.Tools = []AppTool{}
	}
	if a.DisabledTools == nil {
		a.DisabledTools = []string{}
	}
	return a, nil
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}

// CreateAppConnection 新增一個連接。ID、時間沒給時自動產生。
func (q *Queries) CreateAppConnection(ctx context.Context, a AppConnection) (AppConnection, error) {
	if a.ID == "" {
		a.ID = NewID()
	}
	now := nowMS()
	a.CreatedAt, a.UpdatedAt = now, now
	if a.Status == "" {
		a.Status = "checking"
	}
	if a.Auth == "" {
		a.Auth = "none"
	}
	_, err := q.w.ExecContext(ctx, `INSERT INTO app_connection(`+appCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.AppID, a.Name, a.Title, a.Transport, a.URL, a.Command, jsonText(a.Args), a.Auth, a.Account, a.Secret, a.SecretHint,
		jsonText(a.HeaderNames), jsonText(a.EnvNames), a.Status, a.Error, jsonText(a.Tools), jsonText(a.DisabledTools), b2i(a.AllBots), b2i(a.Enabled),
		a.CheckedAt, a.LastUsedAt, a.CreatedBy, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return a, err
	}
	return q.GetAppConnection(ctx, a.ID)
}

// UpdateAppConnection 整筆覆寫一個連接的可改欄位（id、app_id、created_* 不變）。
func (q *Queries) UpdateAppConnection(ctx context.Context, a AppConnection) (AppConnection, error) {
	a.UpdatedAt = nowMS()
	err := affected(q.w.ExecContext(ctx, `UPDATE app_connection SET name = ?, title = ?, transport = ?, url = ?, command = ?, args = ?, auth = ?,
		account = ?, secret = ?, secret_hint = ?, header_names = ?, env_names = ?, status = ?, error = ?, tools = ?, disabled_tools = ?,
		all_bots = ?, enabled = ?, checked_at = ?, last_used_at = ?, updated_at = ? WHERE id = ?`,
		a.Name, a.Title, a.Transport, a.URL, a.Command, jsonText(a.Args), a.Auth, a.Account, a.Secret, a.SecretHint, jsonText(a.HeaderNames),
		jsonText(a.EnvNames), a.Status, a.Error, jsonText(a.Tools), jsonText(a.DisabledTools), b2i(a.AllBots), b2i(a.Enabled),
		a.CheckedAt, a.LastUsedAt, a.UpdatedAt, a.ID))
	if err != nil {
		return a, err
	}
	return q.GetAppConnection(ctx, a.ID)
}

// SetAppStatus 只改連接的狀態與錯誤說明（轉送時發現權杖失效等）。
func (q *Queries) SetAppStatus(ctx context.Context, id, status, errText string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE app_connection SET status = ?, error = ?, updated_at = ? WHERE id = ?`, status, errText, nowMS(), id))
}

// SetAppSecret 只換掉加密的祕密（換新權杖時用）。
func (q *Queries) SetAppSecret(ctx context.Context, id string, secret []byte) error {
	return affected(q.w.ExecContext(ctx, `UPDATE app_connection SET secret = ?, updated_at = ? WHERE id = ?`, secret, nowMS(), id))
}

// GetAppConnection 讀一個連接。
func (q *Queries) GetAppConnection(ctx context.Context, id string) (AppConnection, error) {
	a, err := scanApp(q.r.QueryRowContext(ctx, `SELECT `+appCols+` FROM app_connection WHERE id = ?`, id))
	return a, notFound(err)
}

// GetAppConnectionByName 依 Bot 設定裡的名字讀一個連接。
func (q *Queries) GetAppConnectionByName(ctx context.Context, name string) (AppConnection, error) {
	a, err := scanApp(q.r.QueryRowContext(ctx, `SELECT `+appCols+` FROM app_connection WHERE name = ?`, name))
	return a, notFound(err)
}

// ListAppConnections 列出所有連接，依建立順序。
func (q *Queries) ListAppConnections(ctx context.Context) ([]AppConnection, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+appCols+` FROM app_connection ORDER BY created_at, id`)
	out, err := collect(rows, err, scanApp)
	if out == nil {
		out = []AppConnection{}
	}
	return out, err
}

// DeleteAppConnection 刪除一個連接（授權與最近使用一起刪）。
func (q *Queries) DeleteAppConnection(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM app_connection WHERE id = ?`, id))
}

// ── app_grant ────────────────────────────────────────

// AppGrants 回傳一個連接授權給哪些 Bot（只在 all_bots＝0 時有意義）。
func (q *Queries) AppGrants(ctx context.Context, connectionID string) ([]string, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT bot_id FROM app_grant WHERE connection_id = ? ORDER BY created_at, bot_id`, connectionID)
	out, err := collect(rows, err, func(s scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	})
	if out == nil {
		out = []string{}
	}
	return out, err
}

// SetAppGrants 把一個連接的授權換成 botIDs。
func (q *Queries) SetAppGrants(ctx context.Context, connectionID string, botIDs []string) error {
	return q.atomic(ctx, func(q *Queries) error {
		if _, err := q.w.ExecContext(ctx, `DELETE FROM app_grant WHERE connection_id = ?`, connectionID); err != nil {
			return err
		}
		now := nowMS()
		for _, id := range botIDs {
			if _, err := q.w.ExecContext(ctx, `INSERT OR IGNORE INTO app_grant(connection_id, bot_id, created_at) VALUES (?,?,?)`, connectionID, id, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// AppGranted 回答一個 Bot 能不能用一個連接：連接開著，而且給所有 Bot 或授權給它。
func (q *Queries) AppGranted(ctx context.Context, connectionID, botID string) (bool, error) {
	var n int
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_connection c WHERE c.id = ? AND c.enabled = 1 AND (c.all_bots = 1
		OR EXISTS (SELECT 1 FROM app_grant g WHERE g.connection_id = c.id AND g.bot_id = ?))`, connectionID, botID).Scan(&n)
	return n > 0, err
}

// AppConnectionsForBot 列出一個 Bot 能用的連接（開著、給所有 Bot 或授權給它）。
func (q *Queries) AppConnectionsForBot(ctx context.Context, botID string) ([]AppConnection, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+appCols+` FROM app_connection c WHERE c.enabled = 1 AND (c.all_bots = 1
		OR EXISTS (SELECT 1 FROM app_grant g WHERE g.connection_id = c.id AND g.bot_id = ?)) ORDER BY created_at, id`, botID)
	out, err := collect(rows, err, scanApp)
	if out == nil {
		out = []AppConnection{}
	}
	return out, err
}

// ── app_use ──────────────────────────────────────────

// AppUse 是 Bot 用了一次連接的工具（I02 最近使用）。
type AppUse struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connection_id"`
	BotID        string `json:"bot_id"`
	Tool         string `json:"tool"`
	OK           bool   `json:"ok"`
	CreatedAt    int64  `json:"created_at"`
}

// AddAppUse 記一筆使用，更新連接的上次使用時間，並只留最新 keep 筆。
func (q *Queries) AddAppUse(ctx context.Context, u AppUse, keep int) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	if u.CreatedAt == 0 {
		u.CreatedAt = nowMS()
	}
	return q.atomic(ctx, func(q *Queries) error {
		if _, err := q.w.ExecContext(ctx, `INSERT INTO app_use(id, connection_id, bot_id, tool, ok, created_at) VALUES (?,?,?,?,?,?)`,
			u.ID, u.ConnectionID, u.BotID, u.Tool, b2i(u.OK), u.CreatedAt); err != nil {
			return err
		}
		if _, err := q.w.ExecContext(ctx, `UPDATE app_connection SET last_used_at = ? WHERE id = ?`, u.CreatedAt, u.ConnectionID); err != nil {
			return err
		}
		_, err := q.w.ExecContext(ctx, `DELETE FROM app_use WHERE connection_id = ? AND id NOT IN (
			SELECT id FROM app_use WHERE connection_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?)`, u.ConnectionID, u.ConnectionID, keep)
		return err
	})
}

// ListAppUses 列出一個連接最近的使用，新的在前。
func (q *Queries) ListAppUses(ctx context.Context, connectionID string, limit int) ([]AppUse, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT id, connection_id, bot_id, tool, ok, created_at FROM app_use WHERE connection_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT ?`, connectionID, limit)
	out, err := collect(rows, err, func(s scanner) (AppUse, error) {
		var u AppUse
		var ok int
		err := s.Scan(&u.ID, &u.ConnectionID, &u.BotID, &u.Tool, &ok, &u.CreatedAt)
		u.OK = ok != 0
		return u, err
	})
	if out == nil {
		out = []AppUse{}
	}
	return out, err
}

// ── login_share ──────────────────────────────────────

// LoginShare 是一次分享登入（E09）。Snapshot 是分享前對方原本的登入（加密），復原時寫回去。
type LoginShare struct {
	ID        string   `json:"id"`
	SourceBot string   `json:"source_bot"`
	Targets   []string `json:"targets"`
	Sites     []string `json:"sites"`
	Snapshot  []byte   `json:"-"`
	Actor     string   `json:"actor"`
	CreatedAt int64    `json:"created_at"`
	UndoneAt  int64    `json:"undone_at"`
}

// CreateLoginShare 記一次分享。
func (q *Queries) CreateLoginShare(ctx context.Context, s LoginShare) (LoginShare, error) {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.CreatedAt == 0 {
		s.CreatedAt = nowMS()
	}
	_, err := q.w.ExecContext(ctx, `INSERT INTO login_share(id, source_bot, targets, sites, snapshot, actor, created_at) VALUES (?,?,?,?,?,?,?)`,
		s.ID, s.SourceBot, jsonText(s.Targets), jsonText(s.Sites), s.Snapshot, s.Actor, s.CreatedAt)
	return s, err
}

// GetLoginShare 讀一次分享。
func (q *Queries) GetLoginShare(ctx context.Context, id string) (LoginShare, error) {
	var s LoginShare
	var targets, sites string
	err := q.r.QueryRowContext(ctx, `SELECT id, source_bot, targets, sites, snapshot, actor, created_at, undone_at FROM login_share WHERE id = ?`, id).
		Scan(&s.ID, &s.SourceBot, &targets, &sites, &s.Snapshot, &s.Actor, &s.CreatedAt, &s.UndoneAt)
	if err != nil {
		return s, notFound(err)
	}
	_ = json.Unmarshal([]byte(targets), &s.Targets)
	_ = json.Unmarshal([]byte(sites), &s.Sites)
	return s, nil
}

// MarkLoginShareUndone 記下分享已復原（只成功一次），並清掉快照。
func (q *Queries) MarkLoginShareUndone(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE login_share SET undone_at = ?, snapshot = NULL WHERE id = ? AND undone_at = 0`, nowMS(), id))
}

// ForgetLoginShareSnapshots 清掉超過 before 的分享快照（過了可以復原的時間就不留舊登入）。
func (q *Queries) ForgetLoginShareSnapshots(ctx context.Context, before int64) error {
	_, err := q.w.ExecContext(ctx, `UPDATE login_share SET snapshot = NULL WHERE created_at < ? AND snapshot IS NOT NULL`, before)
	return err
}
