package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"xbot/internal/store"
)

// ProviderSettingPrefix 是供應商設定在 setting 表裡的 key 開頭：model.provider.<id>。
const ProviderSettingPrefix = "model.provider."

// providerSetting 是存進 setting 表的 JSON。金鑰只存密文（[]byte 轉成 base64）。
type providerSetting struct {
	BaseURL   string `json:"base_url"`
	SealedKey []byte `json:"sealed_key"`
	Label     string `json:"label,omitempty"`
	Transport string `json:"transport,omitempty"`
	Preset    string `json:"preset,omitempty"`
	Auth      string `json:"auth,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// Attribution 回答「這個 Bot 現在在做哪個對話串的哪個任務」，讓用量能記到任務上；沒有時回空字串。
type Attribution func(botID string) (threadID, taskID string)

// DBStore 是存在 SQLite 的 Store：供應商放 setting 表、worker-key 亂數放 worker_nonce 表、用量寫 usage_entry。
type DBStore struct {
	db *store.DB
	// Attribute 可為 nil。
	Attribute Attribution
	// Engine 寫進 usage_entry.engine（Model Proxy 目前只服務 Hermes）。
	Engine string
}

var _ Store = (*DBStore)(nil)

// NewDBStore 建立 DBStore。
func NewDBStore(db *store.DB) *DBStore { return &DBStore{db: db, Engine: "hermes"} }

// GetProvider 實作 ProviderStore。
func (s *DBStore) GetProvider(ctx context.Context, id string) (ProviderRecord, error) {
	var v providerSetting
	ok, err := s.db.GetSettingInto(ctx, ProviderSettingPrefix+id, &v)
	if err != nil {
		return ProviderRecord{}, err
	}
	if !ok {
		return ProviderRecord{}, ErrNotFound
	}
	return v.record(id), nil
}

func (v providerSetting) record(id string) ProviderRecord {
	return ProviderRecord{ID: id, BaseURL: v.BaseURL, SealedKey: v.SealedKey, Label: v.Label, Transport: v.Transport,
		Preset: v.Preset, Auth: v.Auth, UpdatedAt: time.UnixMilli(v.UpdatedAt)}
}

// ProviderAuth 回傳供應商的登入方式（runtime.AuthSource）；沒有這個供應商時是空白。
func (s *DBStore) ProviderAuth(ctx context.Context, id string) (string, error) {
	rec, err := s.GetProvider(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return rec.Auth, err
}

// DeleteProvider 實作 ProviderStore；探測結果一起刪。
func (s *DBStore) DeleteProvider(ctx context.Context, id string) error {
	if _, err := s.GetProvider(ctx, id); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.DeleteSetting(ctx, ProviderSettingPrefix+id); err != nil {
			return err
		}
		return q.DeleteSetting(ctx, ProbeSettingPrefix+id)
	})
}

// ListProviders 實作 ProviderStore。
func (s *DBStore) ListProviders(ctx context.Context) ([]ProviderRecord, error) {
	all, err := s.db.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	var out []ProviderRecord
	for k, raw := range all {
		id, ok := strings.CutPrefix(k, ProviderSettingPrefix)
		if !ok {
			continue
		}
		var v providerSetting
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v.record(id))
	}
	return out, nil
}

// PutProvider 實作 ProviderStore。
func (s *DBStore) PutProvider(ctx context.Context, rec ProviderRecord) error {
	return s.db.SetSetting(ctx, ProviderSettingPrefix+rec.ID, providerSetting{
		BaseURL: rec.BaseURL, SealedKey: rec.SealedKey, Label: rec.Label, Transport: rec.Transport, Preset: rec.Preset,
		Auth: rec.Auth, UpdatedAt: rec.UpdatedAt.UnixMilli(),
	})
}

// ProviderIDs 列出已設定的供應商代號。
func (s *DBStore) ProviderIDs(ctx context.Context) ([]string, error) {
	all, err := s.db.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for k := range all {
		if id, ok := strings.CutPrefix(k, ProviderSettingPrefix); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// WorkerNonce 實作 NonceStore。
func (s *DBStore) WorkerNonce(ctx context.Context, botID string) (string, error) {
	n, err := s.db.WorkerNonce(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotFound
	}
	return n, err
}

// SetWorkerNonce 實作 NonceStore。
func (s *DBStore) SetWorkerNonce(ctx context.Context, botID, nonce string) error {
	return s.db.SetWorkerNonce(ctx, botID, nonce)
}

// DeleteWorkerNonce 實作 NonceStore。
func (s *DBStore) DeleteWorkerNonce(ctx context.Context, botID string) error {
	return s.db.DeleteWorkerNonce(ctx, botID)
}

// RecordUsage 實作 UsageLedger。花費以百萬分之一美元記；快取命中記在 cache_read_tokens、寫入快取記在 cache_write_tokens。
func (s *DBStore) RecordUsage(ctx context.Context, e UsageEntry) error {
	var thread, task string
	if s.Attribute != nil {
		thread, task = s.Attribute(e.BotID)
	}
	cost := e.CostMicros
	if cost == 0 && e.Cost > 0 {
		cost = int64(math.Round(e.Cost * 1e6))
	}
	engine := s.Engine
	if e.Engine != "" {
		engine = e.Engine
	}
	at := e.At.UnixMilli()
	if e.At.IsZero() {
		at = 0
	}
	_, err := s.db.AddUsage(ctx, store.UsageEntry{
		BotID: e.BotID, ThreadID: thread, TaskID: task, Engine: engine, Provider: e.Provider, Model: e.Model,
		InputTokens: e.InputTokens, OutputTokens: e.OutputTokens, CacheReadTokens: e.CachedTokens,
		CacheWriteTokens: e.CacheWriteTokens, CostMicros: cost, Unpriced: e.Unpriced, Endpoint: e.Endpoint,
		Fallback: e.Fallback, CreatedAt: at,
	})
	return err
}

// Probe 讀一個供應商的探測結果；沒有時 ok 為 false。
func (s *DBStore) Probe(ctx context.Context, providerID string) (ProbeResult, bool, error) {
	var r ProbeResult
	ok, err := s.db.GetSettingInto(ctx, ProbeSettingPrefix+providerID, &r)
	if r.Checks == nil {
		r.Checks = map[string]ModelCheck{}
	}
	return r, ok, err
}

// PutProbe 存探測結果。
func (s *DBStore) PutProbe(ctx context.Context, r ProbeResult) error {
	return s.db.SetSetting(ctx, ProbeSettingPrefix+r.Provider, r)
}

// TransportFor 決定一個模型的呼叫方式：探測確認過的 → 範本對這個模型的記載 → Hermes 規則 → 供應商預設。
// 供應商不存在時回空白。
func (s *DBStore) TransportFor(ctx context.Context, providerID, model string) (string, error) {
	if t, err := s.ProbedTransport(ctx, providerID, model); err != nil || t != "" {
		return t, err
	}
	rec, err := s.GetProvider(ctx, providerID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if p, ok := PresetByID(rec.Preset); ok {
		if t, ok := p.ModelTransports[model]; ok {
			return t, nil
		}
	}
	if t := RuleTransport(providerID, rec.BaseURL, model); t != "" {
		return t, nil
	}
	return rec.Transport, nil
}

// ProbedTransport 回傳探測確認過的呼叫方式；沒探測過或不能用時回空白。
func (s *DBStore) ProbedTransport(ctx context.Context, providerID, model string) (string, error) {
	r, ok, err := s.Probe(ctx, providerID)
	if err != nil || !ok {
		return "", err
	}
	if c, ok := r.Checks[model]; ok && c.Status == CheckOK {
		return c.Transport, nil
	}
	return "", nil
}
