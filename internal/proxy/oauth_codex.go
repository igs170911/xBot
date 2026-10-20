package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"xbot/internal/limits"
	"xbot/internal/version"
)

// ChatGPT 方案（openai-codex）的登入與請求格式，照 Hermes（box/HERMES_VERSION 釘選版）的
// hermes_cli/auth_codex.py、auth_constants.py、agent/codex_headers.py。細節見 docs/spike-hermes-oauth.md。
const (
	CodexIssuer       = "https://auth.openai.com"
	CodexTokenURL     = CodexIssuer + "/oauth/token"
	CodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann" // Codex CLI 的公開 OAuth client（Hermes 沿用）
	CodexBaseURL      = "https://chatgpt.com/backend-api/codex"
	CodexOriginator   = "hermes-agent" // 請求其實是 Hermes 產生的，照 Hermes 自報身分（待確認，見 spike 文件）
	codexJWTAuthClaim = "https://api.openai.com/auth"
	codexJWTProfile   = "https://api.openai.com/profile"
)

// CodexModels 是 ChatGPT 方案能用的模型（Hermes hermes_cli/codex_models.py DEFAULT_CODEX_MODELS）。
var CodexModels = []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-5.5", "gpt-5.4-mini", "gpt-5.4", "gpt-5.3-codex-spark"}

// CodexOAuth 是 ChatGPT 方案的裝置碼登入與換新。網址可換成假的登入伺服器（測試）。
type CodexOAuth struct {
	Issuer   string
	TokenURL string
	ClientID string
	BaseURL  string // 登入後供應商的服務網址
	Client   *http.Client
	Now      func() time.Time
}

var _ OAuthClient = (*CodexOAuth)(nil)

// NewCodexOAuth 回傳連到真實 auth.openai.com 的 CodexOAuth。
func NewCodexOAuth() *CodexOAuth {
	return &CodexOAuth{Issuer: CodexIssuer, TokenURL: CodexTokenURL, ClientID: CodexClientID, BaseURL: CodexBaseURL}
}

func (c *CodexOAuth) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: limits.OAuthHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *CodexOAuth) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Defaults 實作 OAuthClient。
func (c *CodexOAuth) Defaults() Provider {
	return Provider{BaseURL: c.BaseURL, Label: "ChatGPT 方案", Transport: TransportResponses}
}

func (c *CodexOAuth) post(ctx context.Context, u string, body io.Reader, contentType string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "XBot/"+version.Version)
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, errors.New("連不上 ChatGPT 登入伺服器")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limits.OAuthBodyBytes))
	return resp.StatusCode, b, err
}

func (c *CodexOAuth) postJSON(ctx context.Context, u string, v any) (int, []byte, error) {
	b, _ := json.Marshal(v)
	return c.post(ctx, u, bytes.NewReader(b), "application/json")
}

func (c *CodexOAuth) postForm(ctx context.Context, u string, v url.Values) (int, []byte, error) {
	return c.post(ctx, u, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}

// StartLogin 實作 OAuthClient：POST {issuer}/api/accounts/deviceauth/usercode。
func (c *CodexOAuth) StartLogin(ctx context.Context) (DeviceLogin, error) {
	status, b, err := c.postJSON(ctx, c.Issuer+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": c.ClientID})
	if err != nil {
		return DeviceLogin{}, err
	}
	if status == http.StatusTooManyRequests {
		return DeviceLogin{}, errors.New("ChatGPT 登入請求太頻繁，請稍後再試")
	}
	if status != http.StatusOK {
		return DeviceLogin{}, fmt.Errorf("ChatGPT 登入伺服器回 %d", status)
	}
	var v struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	if json.Unmarshal(b, &v) != nil || v.DeviceAuthID == "" || v.UserCode == "" {
		return DeviceLogin{}, errors.New("ChatGPT 登入伺服器的回應少了必要欄位")
	}
	interval := 5 * time.Second
	var n json.Number
	if json.Unmarshal(bytes.Trim(v.Interval, `"`), &n) == nil {
		if i, err := n.Int64(); err == nil && i > 0 {
			interval = time.Duration(i) * time.Second
		}
	}
	return DeviceLogin{UserCode: v.UserCode, VerificationURL: c.Issuer + "/codex/device", Handle: Secret(v.DeviceAuthID),
		Interval: interval, ExpiresAt: c.now().Add(limits.OAuthLoginTTL)}, nil
}

// PollLogin 實作 OAuthClient：查 deviceauth/token（403／404 表示還沒完成），完成後用 PKCE 換權杖。
func (c *CodexOAuth) PollLogin(ctx context.Context, l DeviceLogin) (OAuthTokens, error) {
	status, b, err := c.postJSON(ctx, c.Issuer+"/api/accounts/deviceauth/token",
		map[string]string{"device_auth_id": string(l.Handle), "user_code": l.UserCode})
	if err != nil {
		return OAuthTokens{}, err
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return OAuthTokens{}, ErrLoginPending
	}
	if status != http.StatusOK {
		return OAuthTokens{}, fmt.Errorf("ChatGPT 登入伺服器回 %d", status)
	}
	var code struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if json.Unmarshal(b, &code) != nil || code.AuthorizationCode == "" || code.CodeVerifier == "" {
		return OAuthTokens{}, errors.New("ChatGPT 登入伺服器的回應少了授權碼")
	}
	status, b, err = c.postForm(ctx, c.TokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code.AuthorizationCode},
		"redirect_uri": {c.Issuer + "/deviceauth/callback"}, "client_id": {c.ClientID}, "code_verifier": {code.CodeVerifier},
	})
	if err != nil {
		return OAuthTokens{}, err
	}
	if status != http.StatusOK {
		return OAuthTokens{}, fmt.Errorf("換權杖失敗（%d）", status)
	}
	return c.tokens(b, OAuthTokens{})
}

// Refresh 實作 OAuthClient：POST /oauth/token grant_type=refresh_token。refresh token 會輪換，回應有新的就換掉。
func (c *CodexOAuth) Refresh(ctx context.Context, t OAuthTokens) (OAuthTokens, error) {
	status, b, err := c.postForm(ctx, c.TokenURL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {string(t.RefreshToken)}, "client_id": {c.ClientID},
	})
	if err != nil {
		return OAuthTokens{}, err
	}
	if status == http.StatusTooManyRequests {
		// 額度或頻率限制：refresh token 還有效，不要求重新登入（Hermes _codex_quota_exhausted_error）。
		return OAuthTokens{}, errors.New("ChatGPT 暫時限制換新權杖，請稍後再試")
	}
	if status != http.StatusOK {
		if status == http.StatusUnauthorized || status == http.StatusForbidden || reloginCode(b) {
			return OAuthTokens{}, fmt.Errorf("%w（換新權杖被拒：%d）", ErrOAuthRelogin, status)
		}
		return OAuthTokens{}, fmt.Errorf("換新權杖失敗（%d）", status)
	}
	return c.tokens(b, t)
}

// reloginCode 照 Hermes _codex_refresh_failure_error：這些錯誤碼表示 refresh token 不能再用。
func reloginCode(b []byte) bool {
	var v struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	var code string
	if json.Unmarshal(v.Error, &code) != nil {
		var obj struct{ Code, Type string }
		_ = json.Unmarshal(v.Error, &obj)
		code = cmpOrStr(obj.Code, obj.Type)
	}
	switch code {
	case "invalid_grant", "invalid_token", "invalid_request", "refresh_token_reused":
		return true
	}
	return false
}

func cmpOrStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// tokens 解析 token 端點的回應；prev 是換新前的權杖（回應沒給 refresh token 時沿用）。
func (c *CodexOAuth) tokens(b []byte, prev OAuthTokens) (OAuthTokens, error) {
	var v struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(b, &v) != nil || v.AccessToken == "" {
		return OAuthTokens{}, errors.New("ChatGPT 登入伺服器沒有給存取權杖")
	}
	now := c.now()
	t := prev
	t.AccessToken = Secret(v.AccessToken)
	if v.RefreshToken != "" {
		t.RefreshToken = Secret(v.RefreshToken)
	}
	if v.IDToken != "" {
		t.IDToken = Secret(v.IDToken)
	}
	t.LastRefresh = now
	cl := codexClaims(v.AccessToken)
	t.ExpiresAt = time.Time{}
	if cl.Exp > 0 {
		t.ExpiresAt = time.Unix(cl.Exp, 0)
	} else if v.ExpiresIn > 0 {
		t.ExpiresAt = now.Add(time.Duration(v.ExpiresIn) * time.Second)
	}
	if cl.AccountID != "" {
		t.AccountID = cl.AccountID
	}
	t.Residency = cl.Residency
	if cl.Plan != "" {
		t.Plan = cl.Plan
	}
	if cl.Email != "" {
		t.Email = cl.Email
	}
	if t.AccountID == "" {
		// id_token 也有同樣的 claim（Codex CLI 從 id_token 讀）。
		if id := codexClaims(string(t.IDToken)); id.AccountID != "" {
			t.AccountID, t.Email = id.AccountID, cmpOrStr(t.Email, id.Email)
		}
	}
	return t, nil
}

type jwtClaims struct {
	Exp                        int64
	AccountID, Residency, Plan string
	Email                      string
}

// codexClaims 讀 JWT 的 payload（不驗簽章：權杖是我們自己經 TLS 向 auth.openai.com 拿的，只用來讀帳號資訊）。
// 讀不懂時回零值（Hermes codex_account_headers 同樣：壞掉的權杖只會在供應商那邊得到 401）。
func codexClaims(token string) jwtClaims {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return jwtClaims{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return jwtClaims{}
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(raw, &p) != nil {
		return jwtClaims{}
	}
	var out jwtClaims
	_ = json.Unmarshal(p["exp"], &out.Exp)
	var auth struct {
		AccountID        string `json:"chatgpt_account_id"`
		DataResidency    string `json:"chatgpt_data_residency"`
		ComputeResidency string `json:"chatgpt_compute_residency"`
		Plan             string `json:"chatgpt_plan_type"`
	}
	if json.Unmarshal(p[codexJWTAuthClaim], &auth) == nil {
		out.AccountID = auth.AccountID
		out.Residency = strings.TrimSpace(cmpOrStr(auth.DataResidency, auth.ComputeResidency))
		out.Plan = auth.Plan
	}
	var prof struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(p[codexJWTProfile], &prof) == nil {
		out.Email = prof.Email
	}
	if out.Email == "" {
		_ = json.Unmarshal(p["email"], &out.Email)
	}
	return out
}

// codexDropFields 是 ChatGPT 的 Codex 端點不收的欄位。Hermes 走 openai-codex 時自己不送
// max_output_tokens、prompt_cache_retention（agent/transports/codex.py、codex_runtime.py）；
// 經 XBot 時 Hermes 把代理當成一般 Responses 端點，可能帶上，所以代理拿掉。
// temperature、top_p 照 Docker Agent 的 ChatGPT 方案文件也拿掉（待實測確認）。
var codexDropFields = []string{"max_output_tokens", "prompt_cache_retention", "temperature", "top_p"}

// codexCacheKey 是一個 Bot 送給 ChatGPT 方案的 prompt_cache_key：同一個 Bot 每段對話都一樣。
// Hermes 每段對話給不同的 key，Codex 照 key 分配快取，新對話的第一個請求就讀不到同一個 Bot 已經快取的守則和工具清單
// （2026-10-20 基準測試：ChatGPT 方案每段新對話第一個請求 22K token 全部沒命中；Haiku、deepseek 照前綴快取就有命中）。
func codexCacheKey(botID string) string { return "xbot-bot-" + botID }

// withCacheKey 把請求的 prompt_cache_key 換成 key；看不懂的內容原樣送出。
func withCacheKey(body []byte, key string) []byte {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil || req == nil {
		return body
	}
	req["prompt_cache_key"], _ = json.Marshal(key)
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// codexBody 把 Hermes 送來的 Responses 請求改成 Codex 端點要的樣子：一律串流、不存檔，拿掉不收的欄位。
// 也回傳 prompt_cache_key（Codex 要同時放在 x-client-request-id 標頭）。看不懂的內容原樣送出。
func codexBody(body []byte) ([]byte, string) {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil || req == nil {
		return body, ""
	}
	for _, k := range codexDropFields {
		delete(req, k)
	}
	req["stream"] = json.RawMessage("true")
	req["store"] = json.RawMessage("false")
	var cacheKey string
	_ = json.Unmarshal(req["prompt_cache_key"], &cacheKey)
	out, err := json.Marshal(req)
	if err != nil {
		return body, cacheKey
	}
	return out, cacheKey
}

// setCodexHeaders 加上 Codex 端點要的身分標頭（Hermes codex_cloudflare_headers + 請求時的 session_id、x-client-request-id）。
func setCodexHeaders(h http.Header, t *OAuthTokens, session, cacheKey string) {
	h.Set("Originator", CodexOriginator)
	h.Set("User-Agent", "HermesAgent (XBot/"+version.Version+")")
	if t != nil && t.AccountID != "" {
		h.Set("ChatGPT-Account-ID", t.AccountID)
	}
	if t != nil && t.Residency != "" {
		h.Set("X-Openai-Internal-Codex-Residency", t.Residency)
	}
	if session != "" {
		h.Set("Session_id", session)
	}
	if cacheKey != "" {
		h.Set("X-Client-Request-Id", cacheKey)
	}
}

// codexPathAllowed 是訂閱登入的供應商開放的路徑：Codex 端點只有 responses（和模型清單）。
func codexPathAllowed(method, apiPath string) bool {
	return (method == http.MethodPost && apiPath == "responses") || (method == http.MethodGet && apiPath == "models")
}
