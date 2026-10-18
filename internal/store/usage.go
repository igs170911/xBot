package store

import (
	"context"
	"encoding/json"
	"errors"
)

// ── usage_entry ──────────────────────────────────────

// UsageEntry 是用量帳本的一筆（US-1）。花費以「百萬分之一美元」存整數，避免浮點誤差。
type UsageEntry struct {
	ID               int64  `json:"id"`
	BotID            string `json:"bot_id"`
	ThreadID         string `json:"thread_id"`
	TaskID           string `json:"task_id"`
	Engine           string `json:"engine"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CostMicros       int64  `json:"cost_micros"`
	CreatedAt        int64  `json:"created_at"`
}

const usageCols = `id, bot_id, thread_id, task_id, engine, provider, model, input_tokens, output_tokens,
	cache_read_tokens, cache_write_tokens, cost_micros, created_at`

func scanUsage(s scanner) (UsageEntry, error) {
	var u UsageEntry
	err := s.Scan(&u.ID, &u.BotID, &u.ThreadID, &u.TaskID, &u.Engine, &u.Provider, &u.Model, &u.InputTokens,
		&u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.CostMicros, &u.CreatedAt)
	return u, err
}

// AddUsage 記一筆用量。CreatedAt 為 0 時用現在時間。
func (q *Queries) AddUsage(ctx context.Context, u UsageEntry) (UsageEntry, error) {
	if u.CreatedAt == 0 {
		u.CreatedAt = nowMS()
	}
	res, err := q.w.ExecContext(ctx, `INSERT INTO usage_entry(bot_id, thread_id, task_id, engine, provider, model,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_micros, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, u.BotID, u.ThreadID, u.TaskID, u.Engine, u.Provider, u.Model,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CostMicros, u.CreatedAt)
	if err != nil {
		return u, err
	}
	u.ID, err = res.LastInsertId()
	return u, err
}

// UsageFilter 是用量查詢的條件；時間是 [Since, Until)，0 表示不限。
type UsageFilter struct {
	BotID string
	Since int64
	Until int64
}

const usageWhere = ` WHERE (? = '' OR bot_id = ?) AND (? = 0 OR created_at >= ?) AND (? = 0 OR created_at < ?)`

func (f UsageFilter) args() []any {
	return []any{f.BotID, f.BotID, f.Since, f.Since, f.Until, f.Until}
}

// ListUsage 列出用量明細，依時間由舊到新。
func (q *Queries) ListUsage(ctx context.Context, f UsageFilter) ([]UsageEntry, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+usageCols+` FROM usage_entry`+usageWhere+` ORDER BY created_at, id`, f.args()...)
	return collect(rows, err, scanUsage)
}

// UsageTotals 是一段時間的用量合計。
type UsageTotals struct {
	Calls            int64 `json:"calls"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CostMicros       int64 `json:"cost_micros"`
}

// SumUsage 合計符合條件的用量（花費上限檢查用）。
func (q *Queries) SumUsage(ctx context.Context, f UsageFilter) (UsageTotals, error) {
	var t UsageTotals
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(cost_micros),0)
		FROM usage_entry`+usageWhere, f.args()...).Scan(&t.Calls, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostMicros)
	return t, err
}

// ── setting ──────────────────────────────────────────

// GetSetting 讀設定值（JSON），沒有就回 ErrNotFound。
func (q *Queries) GetSetting(ctx context.Context, key string) (json.RawMessage, error) {
	var v string
	err := q.r.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return nil, notFound(err)
	}
	return json.RawMessage(v), nil
}

// GetSettingInto 讀設定並解到 dst；沒有這個設定時 dst 不變、回傳 false。
func (q *Queries) GetSettingInto(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := q.GetSetting(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, dst)
}

// SetSetting 把 v 轉成 JSON 存起來（覆寫舊值）。
func (q *Queries) SetSetting(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = q.w.ExecContext(ctx, `INSERT INTO setting(key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, string(b), nowMS())
	return err
}

// DeleteSetting 刪除設定；本來就沒有也不算錯。
func (q *Queries) DeleteSetting(ctx context.Context, key string) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM setting WHERE key = ?`, key)
	return err
}

// ListSettings 回傳所有設定。
func (q *Queries) ListSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT key, value FROM setting ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}
