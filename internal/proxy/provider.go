package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Secret 是不能外流的字串。印出、寫進紀錄或轉成 JSON 時都只會出現遮蔽字樣；
// 真的要用時明確寫 string(s)。
type Secret string

// Masked 是遮蔽後顯示的字樣。
const Masked = "[已遮蔽]"

func (Secret) String() string               { return Masked }
func (Secret) GoString() string             { return Masked }
func (Secret) LogValue() slog.Value         { return slog.StringValue(Masked) }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Masked + `"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte(Masked), nil }

// Format 讓 %s、%v、%q、%#v 都只印出遮蔽字樣。
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Masked)) }

// Provider 是一個 OpenAI 相容的模型供應商。
type Provider struct {
	ID      string // 出現在網址 /model/<ID>/v1/… 裡
	BaseURL string // 例：https://api.openai.com/v1（結尾的 / 可有可無）
	APIKey  Secret // 真正的金鑰，只在服務端
}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidProviderID 檢查供應商代號：小寫英數與 -，最多 32 字。
func ValidProviderID(id string) bool { return providerIDPattern.MatchString(id) }

func providerAAD(id string) string { return "provider:" + id }

// Providers 管理供應商設定，存放前把金鑰加密。
type Providers struct {
	store ProviderStore
	keys  *Keyring
	now   func() time.Time
}

// NewProviders 建立 Providers。
func NewProviders(store ProviderStore, keys *Keyring) *Providers {
	return &Providers{store: store, keys: keys, now: time.Now}
}

// Put 新增或更新供應商；金鑰加密後才交給存放層。
func (p *Providers) Put(ctx context.Context, pr Provider) error {
	if !ValidProviderID(pr.ID) {
		return fmt.Errorf("供應商代號只能用小寫英文、數字和 -：%q", pr.ID)
	}
	u, err := url.Parse(strings.TrimSpace(pr.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("服務網址要以 http:// 或 https:// 開頭")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("服務網址不能含帳號密碼或查詢參數，金鑰請另外填")
	}
	sealed, err := p.keys.Seal([]byte(pr.APIKey), providerAAD(pr.ID))
	if err != nil {
		return fmt.Errorf("加密金鑰失敗：%w", err)
	}
	return p.store.PutProvider(ctx, ProviderRecord{
		ID:        pr.ID,
		BaseURL:   strings.TrimRight(u.String(), "/"),
		SealedKey: sealed,
		UpdatedAt: p.now(),
	})
}

// Get 讀出供應商並解開金鑰。沒有時回 ErrNotFound。
func (p *Providers) Get(ctx context.Context, id string) (Provider, error) {
	rec, err := p.store.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, err
	}
	key, err := p.keys.Open(rec.SealedKey, providerAAD(rec.ID))
	if err != nil {
		return Provider{}, err
	}
	return Provider{ID: rec.ID, BaseURL: rec.BaseURL, APIKey: Secret(key)}, nil
}
