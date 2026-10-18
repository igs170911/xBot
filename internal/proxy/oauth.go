package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"xbot/internal/limits"
)

// 訂閱登入（E13 原型）：供應商的「金鑰」換成 OAuth 權杖。權杖跟 API 金鑰一樣加密存在 setting 表，
// 只在 Model Proxy 裡使用與換新，永遠不進 Bot 的電腦（SEC-5）。容器照舊只拿 worker-key。

// AuthOAuthCodex 是 ChatGPT 方案（Hermes 的 openai-codex）：裝置碼登入 auth.openai.com，
// 請求送到 chatgpt.com/backend-api/codex/responses，帶 ChatGPT-Account-ID 等標頭。
const AuthOAuthCodex = "oauth_codex"

// ErrOAuthRelogin 表示換新權杖被拒（refresh token 失效、被別的程式用掉、帳號登出），要重新登入。
var ErrOAuthRelogin = errors.New("訂閱登入已失效，請重新登入")

// ErrLoginPending 表示使用者還沒在瀏覽器完成登入。
var ErrLoginPending = errors.New("還在等你在瀏覽器完成登入")

// ErrLoginNotFound 表示沒有這個登入（過期或已完成）。
var ErrLoginNotFound = errors.New("找不到這次登入，可能已經過期，請重新開始")

// OAuthTokens 是一組訂閱登入的權杖。只在服務端，存放時整組加密。
type OAuthTokens struct {
	AccessToken  Secret
	RefreshToken Secret
	IDToken      Secret
	// ExpiresAt 是存取權杖到期時間；零值表示不知道（只在被拒時換新）。
	ExpiresAt time.Time
	// AccountID 是 ChatGPT 的工作區帳號（JWT 的 chatgpt_account_id），每個請求都要帶。
	AccountID string
	// Residency 是資料駐留區域（chatgpt_data_residency）；有設定的工作區沒帶會被拒。
	Residency   string
	Email       string
	Plan        string
	LastRefresh time.Time
}

// expiring 回答權杖是不是快到期了。
func (t *OAuthTokens) expiring(now time.Time, skew time.Duration) bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	return !t.ExpiresAt.IsZero() && !now.Add(skew).Before(t.ExpiresAt)
}

// sealedTokens 是加密前的 JSON（Secret 轉 JSON 會被遮蔽，所以另外定義）。
type sealedTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Residency    string `json:"residency,omitempty"`
	Email        string `json:"email,omitempty"`
	Plan         string `json:"plan,omitempty"`
	LastRefresh  int64  `json:"last_refresh,omitempty"`
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

func marshalTokens(t *OAuthTokens) ([]byte, error) {
	return json.Marshal(sealedTokens{
		AccessToken: string(t.AccessToken), RefreshToken: string(t.RefreshToken), IDToken: string(t.IDToken),
		ExpiresAt: unixOrZero(t.ExpiresAt), AccountID: t.AccountID, Residency: t.Residency, Email: t.Email, Plan: t.Plan,
		LastRefresh: unixOrZero(t.LastRefresh),
	})
}

func unmarshalTokens(b []byte) (*OAuthTokens, error) {
	var s sealedTokens
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, errors.New("訂閱登入的權杖資料壞了，請重新登入")
	}
	return &OAuthTokens{
		AccessToken: Secret(s.AccessToken), RefreshToken: Secret(s.RefreshToken), IDToken: Secret(s.IDToken),
		ExpiresAt: timeOrZero(s.ExpiresAt), AccountID: s.AccountID, Residency: s.Residency, Email: s.Email, Plan: s.Plan,
		LastRefresh: timeOrZero(s.LastRefresh),
	}, nil
}

// DeviceLogin 是一次裝置碼登入：使用者到 VerificationURL 輸入 UserCode。
// Handle 是登入伺服器給的識別（例：Codex 的 device_auth_id），只留在服務端。
type DeviceLogin struct {
	UserCode        string
	VerificationURL string
	Handle          Secret
	Interval        time.Duration
	ExpiresAt       time.Time
}

// OAuthClient 是一種訂閱登入：開始登入、查詢登入結果、換新權杖。
type OAuthClient interface {
	StartLogin(ctx context.Context) (DeviceLogin, error)
	// PollLogin 查一次登入結果；使用者還沒完成時回 ErrLoginPending。
	PollLogin(ctx context.Context, l DeviceLogin) (OAuthTokens, error)
	// Refresh 用 refresh token 換新權杖；被拒時回包著 ErrOAuthRelogin 的錯誤。
	Refresh(ctx context.Context, t OAuthTokens) (OAuthTokens, error)
	// Defaults 是登入成功後建立供應商用的設定（網址、呼叫方式、名稱）。
	Defaults() Provider
}

// SetOAuth 指定一種訂閱登入的實作（測試換成假的登入伺服器）。
func (p *Providers) SetOAuth(auth string, c OAuthClient) {
	p.oauthMu.Lock()
	defer p.oauthMu.Unlock()
	if p.oauth == nil {
		p.oauth = map[string]OAuthClient{}
	}
	p.oauth[auth] = c
}

func (p *Providers) oauthClient(auth string) (OAuthClient, error) {
	p.oauthMu.Lock()
	defer p.oauthMu.Unlock()
	c, ok := p.oauth[auth]
	if !ok {
		return nil, fmt.Errorf("不支援這種訂閱登入：%q", auth)
	}
	return c, nil
}

// refreshLock 回傳一個供應商的換新鎖：refresh token 只能用一次，同時換兩次會讓整組登入失效（Hermes #33538）。
func (p *Providers) refreshLock(id string) *sync.Mutex {
	p.oauthMu.Lock()
	defer p.oauthMu.Unlock()
	if p.refreshing == nil {
		p.refreshing = map[string]*sync.Mutex{}
	}
	mu, ok := p.refreshing[id]
	if !ok {
		mu = &sync.Mutex{}
		p.refreshing[id] = mu
	}
	return mu
}

// Fresh 確保訂閱登入的權杖可用：快到期或 force（供應商回 401）時換新並存回。API 金鑰的供應商原樣回傳。
// 同一個供應商同時只換一次；排隊的請求拿到別人剛換好的權杖就直接用。
func (p *Providers) Fresh(ctx context.Context, pr Provider, force bool) (Provider, error) {
	if pr.Auth == "" {
		return pr, nil
	}
	now := p.now()
	if !force && !pr.OAuth.expiring(now, limits.OAuthRefreshSkew) {
		return pr, nil
	}
	c, err := p.oauthClient(pr.Auth)
	if err != nil {
		return Provider{}, err
	}
	mu := p.refreshLock(pr.ID)
	mu.Lock()
	defer mu.Unlock()
	cur, err := p.Get(ctx, pr.ID)
	if err != nil {
		return Provider{}, err
	}
	if cur.Auth != pr.Auth || cur.OAuth == nil {
		return Provider{}, ErrOAuthRelogin
	}
	rotated := pr.OAuth == nil || cur.OAuth.AccessToken != pr.OAuth.AccessToken
	if !cur.OAuth.expiring(p.now(), limits.OAuthRefreshSkew) && (!force || rotated) {
		return cur, nil // 別人剛換好
	}
	if cur.OAuth.RefreshToken == "" {
		return Provider{}, ErrOAuthRelogin
	}
	next, err := c.Refresh(ctx, *cur.OAuth)
	if err != nil {
		return Provider{}, err
	}
	cur.OAuth = &next
	cur.APIKey = next.AccessToken
	if err := p.Put(ctx, cur); err != nil {
		return Provider{}, err
	}
	return cur, nil
}

// LoginStart 是開始登入的結果：給使用者看的網址和代碼。
type LoginStart struct {
	LoginID         string
	UserCode        string
	VerificationURL string
	Interval        time.Duration
	ExpiresAt       time.Time
}

type pendingLogin struct {
	auth, providerID, label string
	dl                      DeviceLogin
	lastPoll                time.Time
}

// StartLogin 開始一次訂閱登入（由 XBot 服務去跟登入伺服器要代碼，不經過 Bot 的電腦）。
func (p *Providers) StartLogin(ctx context.Context, auth, providerID, label string) (LoginStart, error) {
	if !ValidProviderID(providerID) {
		return LoginStart{}, fmt.Errorf("供應商代號只能用小寫英文、數字和 -：%q", providerID)
	}
	c, err := p.oauthClient(auth)
	if err != nil {
		return LoginStart{}, err
	}
	p.oauthMu.Lock()
	p.gcLoginsLocked()
	full := len(p.logins) >= limits.OAuthMaxPendingLogins
	p.oauthMu.Unlock()
	if full {
		return LoginStart{}, errors.New("同時進行中的登入太多了，請先完成或等幾分鐘")
	}
	dl, err := c.StartLogin(ctx)
	if err != nil {
		return LoginStart{}, err
	}
	if dl.Interval < limits.OAuthMinPollInterval {
		dl.Interval = limits.OAuthMinPollInterval
	}
	if ttl := p.now().Add(limits.OAuthLoginTTL); dl.ExpiresAt.IsZero() || dl.ExpiresAt.After(ttl) {
		dl.ExpiresAt = ttl
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return LoginStart{}, err
	}
	id := hex.EncodeToString(b[:])
	p.oauthMu.Lock()
	if p.logins == nil {
		p.logins = map[string]*pendingLogin{}
	}
	p.logins[id] = &pendingLogin{auth: auth, providerID: providerID, label: label, dl: dl}
	p.oauthMu.Unlock()
	return LoginStart{LoginID: id, UserCode: dl.UserCode, VerificationURL: dl.VerificationURL, Interval: dl.Interval, ExpiresAt: dl.ExpiresAt}, nil
}

func (p *Providers) gcLoginsLocked() {
	now := p.now()
	for id, l := range p.logins {
		if !now.Before(l.dl.ExpiresAt) {
			delete(p.logins, id)
		}
	}
}

// CompleteLogin 查一次登入結果。還沒完成回 ErrLoginPending（查太快時不會打擾登入伺服器）；
// 完成時把權杖加密存成供應商（同代號的訂閱登入會被換成新的帳號）並回傳。
func (p *Providers) CompleteLogin(ctx context.Context, loginID string) (Provider, error) {
	p.oauthMu.Lock()
	p.gcLoginsLocked()
	l, ok := p.logins[loginID]
	var wait bool
	if ok {
		now := p.now()
		wait = !l.lastPoll.IsZero() && now.Sub(l.lastPoll) < l.dl.Interval
		if !wait {
			l.lastPoll = now
		}
	}
	p.oauthMu.Unlock()
	if !ok {
		return Provider{}, ErrLoginNotFound
	}
	if wait {
		return Provider{}, ErrLoginPending
	}
	c, err := p.oauthClient(l.auth)
	if err != nil {
		return Provider{}, err
	}
	tok, err := c.PollLogin(ctx, l.dl)
	if err != nil {
		if !errors.Is(err, ErrLoginPending) {
			p.dropLogin(loginID)
		}
		return Provider{}, err
	}
	p.dropLogin(loginID)
	pr := c.Defaults()
	pr.ID, pr.Auth, pr.OAuth, pr.APIKey = l.providerID, l.auth, &tok, tok.AccessToken
	if l.label != "" {
		pr.Label = l.label
	}
	if err := p.Put(ctx, pr); err != nil {
		return Provider{}, err
	}
	return pr, nil
}

func (p *Providers) dropLogin(id string) {
	p.oauthMu.Lock()
	defer p.oauthMu.Unlock()
	delete(p.logins, id)
}
