package proxy

import (
	"context"
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
	return ProviderRecord{ID: id, BaseURL: v.BaseURL, SealedKey: v.SealedKey, UpdatedAt: time.UnixMilli(v.UpdatedAt)}, nil
}

// PutProvider 實作 ProviderStore。
func (s *DBStore) PutProvider(ctx context.Context, rec ProviderRecord) error {
	return s.db.SetSetting(ctx, ProviderSettingPrefix+rec.ID, providerSetting{
		BaseURL: rec.BaseURL, SealedKey: rec.SealedKey, UpdatedAt: rec.UpdatedAt.UnixMilli(),
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

// RecordUsage 實作 UsageLedger。花費換算成百萬分之一美元；快取命中記在 cache_read_tokens。
func (s *DBStore) RecordUsage(ctx context.Context, e UsageEntry) error {
	var thread, task string
	if s.Attribute != nil {
		thread, task = s.Attribute(e.BotID)
	}
	at := e.At.UnixMilli()
	if e.At.IsZero() {
		at = 0
	}
	_, err := s.db.AddUsage(ctx, store.UsageEntry{
		BotID: e.BotID, ThreadID: thread, TaskID: task, Engine: s.Engine, Provider: e.Provider, Model: e.Model,
		InputTokens: e.InputTokens, OutputTokens: e.OutputTokens, CacheReadTokens: e.CachedTokens,
		CostMicros: int64(math.Round(e.Cost * 1e6)), CreatedAt: at,
	})
	return err
}
