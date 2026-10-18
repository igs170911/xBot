package store

import (
	"context"
	"encoding/json"
)

// 主機狀態（host.status）。
const (
	HostUnknown  = "unknown"
	HostOnline   = "online"
	HostOffline  = "offline"
	HostUpdating = "updating" // 加入中、正在載入電腦映像
	HostFailed   = "failed"   // 要人處理（主機金鑰不符、檢查沒過）
)

// Host 是一台 Docker 主機（PRD §16）。
type Host struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // local / ssh
	Address string `json:"address"`
	// Port、SSHUser、KeyRef、KnownHosts：SSH 主機的連線設定（KeyRef、KnownHosts 是資料目錄裡的相對路徑）。
	Port        int             `json:"port"`
	SSHUser     string          `json:"ssh_user"`
	KeyRef      string          `json:"key_ref"`
	KnownHosts  string          `json:"known_hosts"`
	Status      string          `json:"status"` // unknown / online / offline / updating / failed
	StatusError string          `json:"status_error"`
	Arch        string          `json:"arch"`
	MaxRunning  int             `json:"max_running"`
	Resources   json.RawMessage `json:"resources"`
	Health      json.RawMessage `json:"health"`
	LastSeenAt  int64           `json:"last_seen_at"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const hostCols = `id, name, kind, address, port, ssh_user, key_ref, known_hosts, status, status_error, arch, max_running,
	resources, health, last_seen_at, created_at, updated_at`

func scanHost(s scanner) (Host, error) {
	var h Host
	var res, health string
	err := s.Scan(&h.ID, &h.Name, &h.Kind, &h.Address, &h.Port, &h.SSHUser, &h.KeyRef, &h.KnownHosts, &h.Status, &h.StatusError,
		&h.Arch, &h.MaxRunning, &res, &health, &h.LastSeenAt, &h.CreatedAt, &h.UpdatedAt)
	h.Resources, h.Health = json.RawMessage(res), json.RawMessage(health)
	return h, err
}

// CreateHost 新增主機。Status 空白時為 unknown。
func (q *Queries) CreateHost(ctx context.Context, h Host) (Host, error) {
	if h.ID == "" {
		h.ID = NewID()
	}
	if h.Status == "" {
		h.Status = HostUnknown
	}
	h.CreatedAt = nowMS()
	h.UpdatedAt = h.CreatedAt
	h.Resources = json.RawMessage(jsonOr(h.Resources, "{}"))
	h.Health = json.RawMessage(jsonOr(h.Health, "{}"))
	_, err := q.w.ExecContext(ctx, `INSERT INTO host(`+hostCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Name, h.Kind, h.Address, h.Port, h.SSHUser, h.KeyRef, h.KnownHosts, h.Status, h.StatusError, h.Arch, h.MaxRunning,
		string(h.Resources), string(h.Health), h.LastSeenAt, h.CreatedAt, h.UpdatedAt)
	return h, err
}

// GetHost 讀一台主機。
func (q *Queries) GetHost(ctx context.Context, id string) (Host, error) {
	h, err := scanHost(q.r.QueryRowContext(ctx, `SELECT `+hostCols+` FROM host WHERE id = ?`, id))
	return h, notFound(err)
}

// ListHosts 列出所有主機（依加入順序）。
func (q *Queries) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+hostCols+` FROM host ORDER BY created_at, id`)
	return collect(rows, err, scanHost)
}

// UpdateHost 覆寫主機的名稱、位址、連線設定、上限與資源（狀態用 SetHostStatus）。
func (q *Queries) UpdateHost(ctx context.Context, h Host) error {
	return affected(q.w.ExecContext(ctx, `UPDATE host SET name=?, kind=?, address=?, port=?, ssh_user=?, key_ref=?, known_hosts=?,
		arch=?, max_running=?, resources=?, updated_at=? WHERE id=?`,
		h.Name, h.Kind, h.Address, h.Port, h.SSHUser, h.KeyRef, h.KnownHosts, h.Arch, h.MaxRunning, jsonOr(h.Resources, "{}"), nowMS(), h.ID))
}

// SetHostStatus 記錄健康檢查結果（錯誤訊息清空）；online 時同時更新 last_seen_at。
func (q *Queries) SetHostStatus(ctx context.Context, id, status string) error {
	return q.SetHostHealth(ctx, id, status, "", nil)
}

// SetHostHealth 記錄主機狀態、原因與連線狀態（health 是 nil 時不改）；online 時同時更新 last_seen_at。
func (q *Queries) SetHostHealth(ctx context.Context, id, status, errMsg string, health json.RawMessage) error {
	now := nowMS()
	return affected(q.w.ExecContext(ctx, `UPDATE host SET status=?, status_error=?, health = COALESCE(?, health),
		last_seen_at = CASE WHEN ? = 'online' THEN ? ELSE last_seen_at END, updated_at=? WHERE id=?`,
		status, errMsg, nullJSON(health), status, now, now, id))
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// DeleteHost 移除主機；上面還有 Bot 電腦時會失敗（外鍵 RESTRICT）。放在這台的 Bot 改回「還沒擺放」。
func (q *Queries) DeleteHost(ctx context.Context, id string) error {
	return affected(q.w.ExecContext(ctx, `DELETE FROM host WHERE id=?`, id))
}

// SetBotHost 改 Bot 放在哪台主機（空白＝還沒擺放）。
func (q *Queries) SetBotHost(ctx context.Context, botID, hostID string) error {
	return affected(q.w.ExecContext(ctx, `UPDATE bot SET host_id=?, updated_at=? WHERE id=?`, nullStr(hostID), nowMS(), botID))
}

// HostLoad 是一台主機上的電腦數。
type HostLoad struct {
	HostID string `json:"host_id"`
	// Running 是開機中的電腦；Boxes 是所有電腦（含休眠）；Bots 是放在這台的 Bot（含還沒有電腦的）。
	Running int `json:"running"`
	Boxes   int `json:"boxes"`
	Bots    int `json:"bots"`
}

// HostLoads 回傳每台主機的電腦數（沒有任何東西的主機不在結果裡）。
func (q *Queries) HostLoads(ctx context.Context) (map[string]HostLoad, error) {
	out := map[string]HostLoad{}
	rows, err := q.r.QueryContext(ctx, `SELECT host_id, SUM(state IN ('running', 'creating')), COUNT(*) FROM box WHERE host_id IS NOT NULL GROUP BY host_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l HostLoad
		if err := rows.Scan(&l.HostID, &l.Running, &l.Boxes); err != nil {
			rows.Close()
			return nil, err
		}
		out[l.HostID] = l
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Bot 的位置：有電腦時以電腦所在主機為準，否則是 bot.host_id。
	rows, err = q.r.QueryContext(ctx, `SELECT COALESCE(box.host_id, bot.host_id) AS h, COUNT(*) FROM bot LEFT JOIN box ON box.bot_id = bot.id
		WHERE bot.archived = 0 AND COALESCE(box.host_id, bot.host_id) IS NOT NULL GROUP BY h`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		l := out[id]
		l.HostID, l.Bots = id, n
		out[id] = l
	}
	return out, rows.Err()
}
