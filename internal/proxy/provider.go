package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
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

// 模型的呼叫方式（Hermes 的 api_mode；LazyBoy 叫 protocol）。
const (
	TransportChat      = "chat_completions"   // POST chat/completions
	TransportResponses = "codex_responses"    // POST responses
	TransportMessages  = "anthropic_messages" // POST messages
)

// Transports 是所有呼叫方式，探測時依序嘗試。
var Transports = []string{TransportChat, TransportResponses, TransportMessages}

// TransportPath 回傳呼叫方式對應的 API 路徑（/v1/ 之後）；不認得的當成 chat/completions。
func TransportPath(t string) string {
	switch t {
	case TransportResponses:
		return "responses"
	case TransportMessages:
		return "messages"
	}
	return "chat/completions"
}

// ValidTransport 回答 t 是不是認得的呼叫方式（空白也可以，表示預設）。
func ValidTransport(t string) bool { return t == "" || slices.Contains(Transports, t) }

// Provider 是一個模型供應商。
type Provider struct {
	ID      string // 出現在網址 /model/<ID>/v1/… 裡
	BaseURL string // 例：https://api.openai.com/v1（結尾的 / 可有可無）
	APIKey  Secret // 真正的金鑰，只在服務端
	Label   string // 顯示名稱，例：OpenAI API
	// Transport 是這個供應商預設的呼叫方式；空白表示 chat_completions。
	Transport string
	Preset    string // 從哪個內建範本建立（空白表示自訂）
	// Auth 是登入方式：空白是 API 金鑰；AuthOAuthCodex 是訂閱登入（權杖在 OAuth，APIKey 是目前的存取權杖）。
	Auth      string
	OAuth     *OAuthTokens
	UpdatedAt time.Time
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

	// 訂閱登入（oauth.go）。
	oauthMu    sync.Mutex
	oauth      map[string]OAuthClient
	refreshing map[string]*sync.Mutex
	logins     map[string]*pendingLogin
}

// NewProviders 建立 Providers。訂閱登入預設接 ChatGPT（openai-codex）的真實登入伺服器。
func NewProviders(store ProviderStore, keys *Keyring) *Providers {
	p := &Providers{store: store, keys: keys, now: time.Now}
	p.SetOAuth(AuthOAuthCodex, NewCodexOAuth())
	return p
}

// Put 新增或更新供應商；金鑰加密後才交給存放層。
func (p *Providers) Put(ctx context.Context, pr Provider) error {
	if !ValidProviderID(pr.ID) {
		return fmt.Errorf("供應商代號只能用小寫英文、數字和 -：%q", pr.ID)
	}
	if !ValidTransport(pr.Transport) {
		return fmt.Errorf("不認得的呼叫方式：%q", pr.Transport)
	}
	base, err := NormalizeBaseURL(pr.BaseURL)
	if err != nil {
		return err
	}
	secret := []byte(pr.APIKey)
	if pr.Auth != "" {
		if pr.Auth != AuthOAuthCodex {
			return fmt.Errorf("不認得的登入方式：%q", pr.Auth)
		}
		if pr.OAuth == nil || pr.OAuth.AccessToken == "" {
			return errors.New("訂閱登入沒有權杖")
		}
		// 權杖整組加密，和 API 金鑰放在同一個欄位。
		if secret, err = marshalTokens(pr.OAuth); err != nil {
			return err
		}
	}
	sealed, err := p.keys.Seal(secret, providerAAD(pr.ID))
	if err != nil {
		return fmt.Errorf("加密金鑰失敗：%w", err)
	}
	return p.store.PutProvider(ctx, ProviderRecord{
		ID:        pr.ID,
		BaseURL:   base,
		SealedKey: sealed,
		Label:     pr.Label,
		Transport: pr.Transport,
		Preset:    pr.Preset,
		Auth:      pr.Auth,
		UpdatedAt: p.now(),
	})
}

// NormalizeBaseURL 檢查供應商的服務網址（http／https、沒有帳密與查詢參數），回傳去掉結尾 / 的網址。
func NormalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("服務網址要以 http:// 或 https:// 開頭")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("服務網址不能含帳號密碼或查詢參數，金鑰請另外填")
	}
	return strings.TrimRight(u.String(), "/"), nil
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
	if rec.Auth != "" {
		tok, err := unmarshalTokens(key)
		if err != nil {
			return Provider{}, err
		}
		pr := providerOf(rec, tok.AccessToken)
		pr.OAuth = tok
		return pr, nil
	}
	return providerOf(rec, Secret(key)), nil
}

func providerOf(rec ProviderRecord, key Secret) Provider {
	return Provider{ID: rec.ID, BaseURL: rec.BaseURL, APIKey: key, Label: rec.Label, Transport: rec.Transport,
		Preset: rec.Preset, Auth: rec.Auth, UpdatedAt: rec.UpdatedAt}
}

// Delete 移除供應商（連同加密的金鑰）。沒有時回 ErrNotFound。
func (p *Providers) Delete(ctx context.Context, id string) error {
	return p.store.DeleteProvider(ctx, id)
}

// List 列出所有供應商（依代號排序）。金鑰不解開：APIKey 只用來表示「有沒有設定」（非空時是遮蔽字樣）。
func (p *Providers) List(ctx context.Context) ([]Provider, error) {
	recs, err := p.store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Provider, 0, len(recs))
	for _, rec := range recs {
		var key Secret
		if pt, err := p.keys.Open(rec.SealedKey, providerAAD(rec.ID)); err == nil && len(pt) > 0 {
			key = Masked
		}
		out = append(out, providerOf(rec, key))
	}
	slices.SortFunc(out, func(a, b Provider) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
