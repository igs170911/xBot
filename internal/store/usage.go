package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
	// Unpriced：價目表沒有這個模型、供應商也沒回報花費，CostMicros 記成 0。
	Unpriced  bool   `json:"unpriced"`
	Endpoint  string `json:"endpoint"`
	Fallback  bool   `json:"fallback"` // 主要模型忙碌或出錯，改用備援模型
	CreatedAt int64  `json:"created_at"`
}

const usageCols = `id, bot_id, thread_id, task_id, engine, provider, model, input_tokens, output_tokens,
	cache_read_tokens, cache_write_tokens, cost_micros, unpriced, endpoint, fallback, created_at`

func scanUsage(s scanner) (UsageEntry, error) {
	var u UsageEntry
	err := s.Scan(&u.ID, &u.BotID, &u.ThreadID, &u.TaskID, &u.Engine, &u.Provider, &u.Model, &u.InputTokens,
		&u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens, &u.CostMicros, &u.Unpriced, &u.Endpoint, &u.Fallback, &u.CreatedAt)
	return u, err
}

// AddUsage 記一筆用量。CreatedAt 為 0 時用現在時間。
func (q *Queries) AddUsage(ctx context.Context, u UsageEntry) (UsageEntry, error) {
	if u.CreatedAt == 0 {
		u.CreatedAt = nowMS()
	}
	res, err := q.w.ExecContext(ctx, `INSERT INTO usage_entry(bot_id, thread_id, task_id, engine, provider, model,
		input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_micros, unpriced, endpoint, fallback, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, u.BotID, u.ThreadID, u.TaskID, u.Engine, u.Provider, u.Model,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CostMicros, b2i(u.Unpriced), u.Endpoint, b2i(u.Fallback), u.CreatedAt)
	if err != nil {
		return u, err
	}
	u.ID, err = res.LastInsertId()
	return u, err
}

// UsageFilter 是用量查詢的條件；時間是 [Since, Until)，0 表示不限。
type UsageFilter struct {
	BotID    string
	Provider string
	Since    int64
	Until    int64
}

// where 組出查詢條件（col 是欄位前綴，例："u."）。不限的條件不寫進 SQL：寫成 (? = ” OR bot_id = ?) 時
// SQLite 用不到索引，10 萬筆的統計要整張表掃過（v1.0 效能）。
func (f UsageFilter) where(col string) (string, []any) {
	where := []string{"1=1"}
	var args []any
	if f.BotID != "" {
		where, args = append(where, col+"bot_id = ?"), append(args, f.BotID)
	}
	if f.Provider != "" {
		where, args = append(where, col+"provider = ?"), append(args, f.Provider)
	}
	if f.Since != 0 {
		where, args = append(where, col+"created_at >= ?"), append(args, f.Since)
	}
	if f.Until != 0 {
		where, args = append(where, col+"created_at < ?"), append(args, f.Until)
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// EachUsage 依時間由舊到新逐筆讀用量明細（匯出用，不必整批放進記憶體）。fn 回錯誤就停止。
func (q *Queries) EachUsage(ctx context.Context, f UsageFilter, fn func(UsageEntry) error) error {
	where, args := f.where("")
	rows, err := q.r.QueryContext(ctx, `SELECT `+usageCols+` FROM usage_entry`+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return err
		}
		if err := fn(u); err != nil {
			return err
		}
	}
	return rows.Err()
}

// UsageSpan 回傳符合條件的最早、最晚一筆的時間；沒有資料時 ok 是 false。
func (q *Queries) UsageSpan(ctx context.Context, f UsageFilter) (first, last int64, ok bool, err error) {
	where, args := f.where("")
	var lo, hi sql.NullInt64
	err = q.r.QueryRowContext(ctx, `SELECT MIN(created_at), MAX(created_at) FROM usage_entry`+where, args...).Scan(&lo, &hi)
	return lo.Int64, hi.Int64, lo.Valid, err
}

// UsageGroup 是一段時間裡、一組（Bot、供應商、模型）的用量合計。
type UsageGroup struct {
	Span             int // 第幾段（bounds[Span] ≤ 時間 < bounds[Span+1]）
	BotID            string
	Provider         string
	Model            string
	Calls            int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostMicros       int64
	UnpricedCalls    int64
	FallbackCalls    int64
}

// GroupUsage 把用量依時間段（bounds 是遞增的分段點，例：每天 0 點）、Bot、供應商、模型合計；落在 bounds 外的不算。
// 只讀合計要用的欄位、不排序，字串欄位用 RawBytes 讀（同一組只配置一次），不逐筆建 UsageEntry
// （v1.0 效能；在 SQLite 裡 GROUP BY 要先排序，在純 Go 的 driver 上反而比較慢）。
func (q *Queries) GroupUsage(ctx context.Context, f UsageFilter, bounds []int64) ([]UsageGroup, error) {
	if len(bounds) < 2 {
		return nil, nil
	}
	where, args := f.where("")
	rows, err := q.r.QueryContext(ctx, `SELECT bot_id, provider, model, created_at, input_tokens, output_tokens, cache_read_tokens,
		cache_write_tokens, cost_micros, unpriced, fallback FROM usage_entry`+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		span                 int
		bot, provider, model string
	}
	index := map[key]int{}
	var out []UsageGroup
	var bot, provider, model sql.RawBytes
	var at, in, outTok, cr, cw, cost, unpriced, fallback int64
	for rows.Next() {
		if err := rows.Scan(&bot, &provider, &model, &at, &in, &outTok, &cr, &cw, &cost, &unpriced, &fallback); err != nil {
			return nil, err
		}
		span, found := slices.BinarySearch(bounds, at)
		if !found {
			span--
		}
		if span < 0 || span >= len(bounds)-1 {
			continue
		}
		k := key{span, string(bot), string(provider), string(model)}
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, UsageGroup{Span: span, BotID: k.bot, Provider: k.provider, Model: k.model})
		}
		g := &out[i]
		g.Calls++
		g.InputTokens += in
		g.OutputTokens += outTok
		g.CacheReadTokens += cr
		g.CacheWriteTokens += cw
		g.CostMicros += cost
		g.UnpricedCalls += unpriced
		g.FallbackCalls += fallback
	}
	return out, rows.Err()
}

// ListUsage 列出用量明細，依時間由舊到新。
func (q *Queries) ListUsage(ctx context.Context, f UsageFilter) ([]UsageEntry, error) {
	where, args := f.where("")
	rows, err := q.r.QueryContext(ctx, `SELECT `+usageCols+` FROM usage_entry`+where+` ORDER BY created_at, id`, args...)
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
	where, args := f.where("")
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(cost_micros),0)
		FROM usage_entry`+where, args...).Scan(&t.Calls, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostMicros)
	return t, err
}

// UnpricedModel 是用過、但沒有價格的模型。
type UnpricedModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Calls    int64  `json:"calls"`
	LastAt   int64  `json:"last_at"`
}

// UnpricedModels 列出符合條件、花費記成 0 的模型（價目表要補的）。
func (q *Queries) UnpricedModels(ctx context.Context, f UsageFilter) ([]UnpricedModel, error) {
	where, args := f.where("")
	rows, err := q.r.QueryContext(ctx, `SELECT provider, model, COUNT(*), MAX(created_at) FROM usage_entry`+where+
		` AND unpriced = 1 GROUP BY provider, model ORDER BY provider, model`, args...)
	return collect(rows, err, func(s scanner) (UnpricedModel, error) {
		var m UnpricedModel
		err := s.Scan(&m.Provider, &m.Model, &m.Calls, &m.LastAt)
		return m, err
	})
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
