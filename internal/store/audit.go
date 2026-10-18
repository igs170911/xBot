package store

import (
	"context"
	"encoding/json"
)

// ── audit_log ────────────────────────────────────────

// AuditEntry 是一筆稽核紀錄（MCP-13）。Params 必須是已遮蔽密鑰的內容。
type AuditEntry struct {
	ID        int64           `json:"id"`
	Actor     string          `json:"actor"` // owner / agent:<token id> / bot:<id>
	Via       string          `json:"via"`   // http / mcp / cli / ui
	Op        string          `json:"op"`
	RequestID string          `json:"request_id"`
	Target    string          `json:"target"`
	Params    json.RawMessage `json:"params"`
	Status    string          `json:"status"` // ok / error / pending_approval / dry_run
	ErrorCode string          `json:"error_code"`
	CreatedAt int64           `json:"created_at"`
}

const auditCols = `id, actor, via, op, request_id, target, params, status, error_code, created_at`

func scanAudit(s scanner) (AuditEntry, error) {
	var a AuditEntry
	var params string
	err := s.Scan(&a.ID, &a.Actor, &a.Via, &a.Op, &a.RequestID, &a.Target, &params, &a.Status, &a.ErrorCode, &a.CreatedAt)
	a.Params = json.RawMessage(params)
	return a, err
}

// AddAudit 寫一筆稽核紀錄。
func (q *Queries) AddAudit(ctx context.Context, a AuditEntry) (AuditEntry, error) {
	a.CreatedAt = nowMS()
	a.Params = json.RawMessage(jsonOr(a.Params, "{}"))
	res, err := q.w.ExecContext(ctx, `INSERT INTO audit_log(actor, via, op, request_id, target, params, status, error_code, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, a.Actor, a.Via, a.Op, a.RequestID, a.Target, string(a.Params), a.Status, a.ErrorCode, a.CreatedAt)
	if err != nil {
		return a, err
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

// AuditFilter 是 ListAudit 的條件；空白欄位不篩選。
type AuditFilter struct {
	Actor  string
	Target string
	Before int64 // 只列 ID 比它小的（往前翻頁）
	Limit  int
}

// ListAudit 列出稽核紀錄，新到舊。
func (q *Queries) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	if f.Limit <= 0 {
		f.Limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+auditCols+` FROM audit_log
		WHERE (? = '' OR actor = ?) AND (? = '' OR target = ?) AND (? = 0 OR id < ?)
		ORDER BY id DESC LIMIT ?`, f.Actor, f.Actor, f.Target, f.Target, f.Before, f.Before, f.Limit)
	return collect(rows, err, scanAudit)
}

// ── agent_token ──────────────────────────────────────

// AgentToken 是外部代理的權杖（MCP-6）。只存雜湊；原文只在配對當下給代理一次。
type AgentToken struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	TokenHash  string          `json:"-"`
	Scopes     []string        `json:"scopes"` // read / operate / manage / admin
	Resources  json.RawMessage `json:"resources"`
	LastUsedAt int64           `json:"last_used_at"`
	ExpiresAt  int64           `json:"expires_at"` // 0 表示不過期
	RevokedAt  int64           `json:"revoked_at"` // 0 表示有效
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

const tokenCols = `id, name, token_hash, scopes, resources, last_used_at, expires_at, revoked_at, created_at, updated_at`

func scanToken(s scanner) (AgentToken, error) {
	var t AgentToken
	var scopes, res string
	err := s.Scan(&t.ID, &t.Name, &t.TokenHash, &scopes, &res, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	t.Resources = json.RawMessage(res)
	t.Scopes, err = parseList(scopes)
	return t, err
}

// CreateAgentToken 新增代理權杖。
func (q *Queries) CreateAgentToken(ctx context.Context, t AgentToken) (AgentToken, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.Scopes == nil {
		t.Scopes = []string{}
	}
	t.CreatedAt = nowMS()
	t.UpdatedAt = t.CreatedAt
	t.Resources = json.RawMessage(jsonOr(t.Resources, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO agent_token(`+tokenCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.TokenHash, strList(t.Scopes), string(t.Resources), t.LastUsedAt, t.ExpiresAt, t.RevokedAt, t.CreatedAt, t.UpdatedAt)
	return t, err
}

// GetAgentToken 用 id 讀權杖。
func (q *Queries) GetAgentToken(ctx context.Context, id string) (AgentToken, error) {
	t, err := scanToken(q.r.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM agent_token WHERE id = ?`, id))
	return t, notFound(err)
}

// FindAgentToken 用雜湊找「目前有效」的權杖（沒撤銷、沒過期）；找不到回 ErrNotFound。
func (q *Queries) FindAgentToken(ctx context.Context, hash string) (AgentToken, error) {
	t, err := scanToken(q.r.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM agent_token
		WHERE token_hash = ? AND revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)`, hash, nowMS()))
	return t, notFound(err)
}

// ListAgentTokens 列出所有權杖（含已撤銷的），依建立順序。
func (q *Queries) ListAgentTokens(ctx context.Context) ([]AgentToken, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+tokenCols+` FROM agent_token ORDER BY created_at, id`)
	return collect(rows, err, scanToken)
}

// TouchAgentToken 記錄最後使用時間。
func (q *Queries) TouchAgentToken(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET last_used_at=? WHERE id=?`, nowMS(), id))
}

// RevokeAgentToken 撤銷權杖；已撤銷的再撤銷不會改時間。
func (q *Queries) RevokeAgentToken(ctx context.Context, id string) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET revoked_at = CASE WHEN revoked_at = 0 THEN ? ELSE revoked_at END,
		updated_at=? WHERE id=?`, now, now, id))
}
