package proxy

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// ErrNotFound 表示存放層裡沒有這筆資料。
var ErrNotFound = errors.New("找不到")

// ProviderRecord 是存進資料庫的供應商設定；金鑰只以密文存放。
type ProviderRecord struct {
	ID        string
	BaseURL   string
	SealedKey []byte // Keyring.Seal 的結果，aad 是 providerAAD(ID)
	Label     string
	Transport string
	Preset    string
	Auth      string // 空白是 API 金鑰；AuthOAuthCodex 時 SealedKey 是整組 OAuth 權杖
	UpdatedAt time.Time
}

// UsageEntry 是一次模型請求的用量（對應 usage_entry 表）。
type UsageEntry struct {
	BotID    string
	Provider string
	Model    string
	Endpoint string // chat/completions、responses、messages、embeddings
	// InputTokens 一律包含快取命中的部分；CachedTokens 是其中命中快取的量（照 LazyBoy usage.normalize）。
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64 // Anthropic 的 cache_creation_input_tokens（也算在 InputTokens 裡）
	ReasoningTokens  int64
	// Cost 是供應商自己回報的花費（例：OpenRouter 的 usage.cost）；沒有回報時為 0。
	Cost float64
	// CostMicros 是記帳用的花費（百萬分之一美元）：供應商有回報就用回報的，否則照價目表算。
	CostMicros int64
	Unpriced   bool // 價目表沒有這個模型、供應商也沒回報花費
	Fallback   bool // 改用了備援模型
	Status     int  // 供應商回的 HTTP 狀態碼
	OK         bool
	At         time.Time
}

// ProviderStore 存放供應商設定。
type ProviderStore interface {
	GetProvider(ctx context.Context, id string) (ProviderRecord, error) // 沒有時回 ErrNotFound
	PutProvider(ctx context.Context, rec ProviderRecord) error
	DeleteProvider(ctx context.Context, id string) error // 沒有時回 ErrNotFound
	ListProviders(ctx context.Context) ([]ProviderRecord, error)
}

// NonceStore 存放每個 Bot 目前的 worker-key 亂數；換掉它就讓舊的 worker-key 失效。
type NonceStore interface {
	WorkerNonce(ctx context.Context, botID string) (string, error) // 沒有時回 ErrNotFound
	SetWorkerNonce(ctx context.Context, botID, nonce string) error
	DeleteWorkerNonce(ctx context.Context, botID string) error
}

// UsageLedger 記錄用量。
type UsageLedger interface {
	RecordUsage(ctx context.Context, e UsageEntry) error
}

// Store 是 Model Proxy 需要的全部存放功能；之後由 internal/store 的 SQLite 實作。
type Store interface {
	ProviderStore
	NonceStore
	UsageLedger
}

// Memory 是放在記憶體裡的 Store，給測試與尚未接上資料庫前使用。
type Memory struct {
	mu        sync.Mutex
	providers map[string]ProviderRecord
	nonces    map[string]string
	usage     []UsageEntry
}

// NewMemory 建立空的 Memory。
func NewMemory() *Memory {
	return &Memory{providers: map[string]ProviderRecord{}, nonces: map[string]string{}}
}

func (m *Memory) GetProvider(_ context.Context, id string) (ProviderRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.providers[id]
	if !ok {
		return ProviderRecord{}, ErrNotFound
	}
	rec.SealedKey = slices.Clone(rec.SealedKey)
	return rec, nil
}

func (m *Memory) PutProvider(_ context.Context, rec ProviderRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec.SealedKey = slices.Clone(rec.SealedKey)
	m.providers[rec.ID] = rec
	return nil
}

func (m *Memory) DeleteProvider(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.providers[id]; !ok {
		return ErrNotFound
	}
	delete(m.providers, id)
	return nil
}

func (m *Memory) ListProviders(_ context.Context) ([]ProviderRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProviderRecord, 0, len(m.providers))
	for _, rec := range m.providers {
		rec.SealedKey = slices.Clone(rec.SealedKey)
		out = append(out, rec)
	}
	return out, nil
}

func (m *Memory) WorkerNonce(_ context.Context, botID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nonces[botID]
	if !ok {
		return "", ErrNotFound
	}
	return n, nil
}

func (m *Memory) SetWorkerNonce(_ context.Context, botID, nonce string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nonces[botID] = nonce
	return nil
}

func (m *Memory) DeleteWorkerNonce(_ context.Context, botID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nonces, botID)
	return nil
}

func (m *Memory) RecordUsage(_ context.Context, e UsageEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, e)
	return nil
}

// Usage 回傳目前記下的用量（複本）。
func (m *Memory) Usage() []UsageEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.usage)
}
