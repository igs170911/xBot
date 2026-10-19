package apps

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// 遠端 MCP 的 OAuth 2 登入（I03，MCP 授權規格 2025-06-18／2025-11-25）。
//
// 和 LazyBoy 不同：LazyBoy 讓 Hermes 在 Bot 的電腦裡跑 OAuth、權杖寫進每個 Bot 都掛載的資料夾；
// XBot 由服務自己跑整個流程（找授權伺服器、動態註冊、PKCE、換權杖、換新），權杖加密存在服務端，
// Bot 用的時候由 XBot 轉送並帶上權杖（SEC-5：權杖永遠不進容器）。
//
// 回呼網址是 <XBot 網址>/api/v1/apps/oauth/callback：在你自己的瀏覽器登入時直接回到 XBot；
// 在 Bot 的電腦裡登入時那個網址打不開（正常），把網址列整串貼回來（xbot.apps.finish_signin）一樣能完成。

// CallbackPath 是 OAuth 回呼的路徑（不需要登入：state 本身就是一次性的憑證）。
const CallbackPath = "/api/v1/apps/oauth/callback"

var (
	// errRelogin 表示換新權杖被拒（refresh token 失效或被撤銷），要重新登入。
	errRelogin = errors.New("授權已過期，請重新登入")
	// errNeedsClient 表示授權伺服器不開放動態註冊，要你自己的 OAuth 用戶端 ID。
	errNeedsClient = errors.New("這個服務不開放自動註冊，請填入你自己的 OAuth 用戶端 ID（Client ID）")
)

// discover 找出遠端 MCP 的授權伺服器與 resource（RFC 9728 → RFC 8414；找不到時用舊規格的預設端點）。
func (s *Service) discover(ctx context.Context, mcpURL string) (*oauthState, error) {
	st := &oauthState{Resource: mcpURL}
	var metadataURL string
	var scopes []string
	// 不帶權杖打一次：401 的 WWW-Authenticate 會告訴我們 resource metadata 在哪、要哪些 scope。
	if req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(initializeBody)); err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if resp, err := s.http().Do(req); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if cs, err := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate")); err == nil {
				for _, c := range cs {
					if v := c.Params["resource_metadata"]; v != "" && metadataURL == "" {
						metadataURL = v
					}
					if v := c.Params["scope"]; v != "" && c.Scheme == "bearer" && scopes == nil {
						scopes = strings.Fields(v)
					}
				}
			}
		}
	}
	issuer := ""
	for _, u := range resourceMetadataURLs(metadataURL, mcpURL) {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, u[0], u[1], s.http())
		if err != nil || prm == nil {
			continue
		}
		if len(prm.AuthorizationServers) == 0 {
			return nil, errors.New("這個服務沒有提供授權伺服器")
		}
		issuer = prm.AuthorizationServers[0]
		if prm.Resource != "" {
			st.Resource = prm.Resource
		}
		if scopes == nil {
			scopes = prm.ScopesSupported
		}
		break
	}
	if issuer == "" {
		u, err := url.Parse(mcpURL)
		if err != nil {
			return nil, err
		}
		u.Path, u.RawQuery, u.Fragment = "", "", ""
		issuer = u.String()
	}
	st.Issuer = issuer
	asm, err := sdkauth.GetAuthServerMetadata(ctx, issuer, s.http())
	if err != nil {
		s.o.Logger.Warn("讀不到授權伺服器的設定，改用預設端點", "issuer", issuer, "err", err)
	}
	if asm != nil {
		st.AuthURL, st.TokenURL, st.RegistrationURL = asm.AuthorizationEndpoint, asm.TokenEndpoint, asm.RegistrationEndpoint
		if slices.Contains(asm.ScopesSupported, "offline_access") && len(scopes) > 0 && !slices.Contains(scopes, "offline_access") {
			scopes = append(scopes, "offline_access")
		}
		st.AuthMethod = pickAuthMethod(asm.TokenEndpointAuthMethodsSupported)
	} else {
		base := strings.TrimRight(issuer, "/")
		st.AuthURL, st.TokenURL, st.RegistrationURL = base+"/authorize", base+"/token", base+"/register"
	}
	for _, u := range []string{st.AuthURL, st.TokenURL} {
		if !webURL(u) {
			return nil, fmt.Errorf("授權伺服器的網址不安全：%q", u)
		}
	}
	if st.RegistrationURL != "" && !webURL(st.RegistrationURL) {
		st.RegistrationURL = ""
	}
	st.Scopes = scopes
	return st, nil
}

// resourceMetadataURLs 是要試的 protected resource metadata 網址（[網址, 對應的 resource]），照 MCP 規格的順序。
func resourceMetadataURLs(metadataURL, resourceURL string) [][2]string {
	var out [][2]string
	if metadataURL != "" {
		out = append(out, [2]string{metadataURL, resourceURL})
	}
	ru, err := url.Parse(resourceURL)
	if err != nil {
		return out
	}
	mu := *ru
	mu.RawQuery, mu.Fragment = "", ""
	if p := strings.Trim(ru.Path, "/"); p != "" {
		mu.Path = "/.well-known/oauth-protected-resource/" + p
		out = append(out, [2]string{mu.String(), resourceURL})
	}
	mu.Path = "/.well-known/oauth-protected-resource"
	root := *ru
	root.Path, root.RawQuery, root.Fragment = "", "", ""
	out = append(out, [2]string{mu.String(), root.String()})
	return out
}

func pickAuthMethod(supported []string) string {
	for _, m := range []string{"none", "client_secret_post", "client_secret_basic"} {
		if slices.Contains(supported, m) {
			return m
		}
	}
	return ""
}

// webURL 只接受 http(s) 而且有主機的網址（遠端可能回傳 javascript: 之類的「登入網址」，LazyBoy 同樣擋掉）。
func webURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.Hostname() != ""
}

// register 準備 OAuth 用戶端：你給了用戶端 ID 就用它；否則動態註冊（RFC 7591）。
func (s *Service) register(ctx context.Context, st *oauthState, redirectURI, clientID, clientSecret string) error {
	st.RedirectURI = redirectURI
	if clientID != "" {
		st.ClientID, st.ClientSecret, st.Registered = clientID, clientSecret, false
		if clientSecret != "" && (st.AuthMethod == "" || st.AuthMethod == "none") {
			st.AuthMethod = "client_secret_post"
		}
		if clientSecret == "" {
			st.AuthMethod = "none"
		}
		return nil
	}
	if st.RegistrationURL == "" {
		return errNeedsClient
	}
	meta := &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{redirectURI},
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              "XBot",
	}
	if len(st.Scopes) > 0 {
		meta.Scope = strings.Join(st.Scopes, " ")
	}
	res, err := oauthex.RegisterClient(ctx, st.RegistrationURL, meta, s.http())
	if err != nil {
		return fmt.Errorf("向授權伺服器註冊 XBot 失敗：%w", err)
	}
	st.ClientID, st.ClientSecret, st.Registered = res.ClientID, res.ClientSecret, true
	st.AuthMethod = res.TokenEndpointAuthMethod
	if st.AuthMethod == "" {
		st.AuthMethod = "client_secret_basic" // RFC 7591 的預設
		if st.ClientSecret == "" {
			st.AuthMethod = "none"
		}
	}
	return nil
}

// pkce 產生 code_verifier 與 S256 的 code_challenge（RFC 7636）。
func pkce() (verifier, challenge string) {
	verifier = randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// authorizeURL 是要你打開的登入頁。
func authorizeURL(st *oauthState, state, challenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", st.ClientID)
	q.Set("redirect_uri", st.RedirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if st.Resource != "" {
		q.Set("resource", st.Resource)
	}
	if len(st.Scopes) > 0 {
		q.Set("scope", strings.Join(st.Scopes, " "))
	}
	// Google 要 access_type=offline 才會給 refresh token（只要 offline_access 不夠），不然大約每小時要重新登入
	if u, err := url.Parse(st.AuthURL); err == nil && u.Hostname() == "accounts.google.com" {
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
	sep := "?"
	if strings.Contains(st.AuthURL, "?") {
		sep = "&"
	}
	return st.AuthURL + sep + q.Encode()
}

// tokenResponse 是權杖端點的回覆（RFC 6749 §5.1；有的服務另外帶帳號資訊，拿來顯示）。
type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    json.Number     `json:"expires_in"`
	RefreshToken string          `json:"refresh_token"`
	Scope        string          `json:"scope"`
	Error        string          `json:"error"`
	Description  string          `json:"error_description"`
	Workspace    string          `json:"workspace_name"`
	Email        string          `json:"email"`
	Team         json.RawMessage `json:"team"`
}

// account 從權杖回覆裡找可以顯示的帳號名稱（Notion 的 workspace_name、Slack 的 team.name…）。
func (t tokenResponse) account() string {
	if t.Workspace != "" {
		return t.Workspace
	}
	if t.Email != "" {
		return t.Email
	}
	var team struct {
		Name string `json:"name"`
	}
	if len(t.Team) > 0 && json.Unmarshal(t.Team, &team) == nil {
		return team.Name
	}
	return ""
}

// exchange 用授權碼換權杖。
func (s *Service) exchange(ctx context.Context, st *oauthState, code, verifier string) (tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", st.RedirectURI)
	form.Set("code_verifier", verifier)
	if st.Resource != "" {
		form.Set("resource", st.Resource)
	}
	return s.tokenRequest(ctx, st, form)
}

// refresh 用 refresh token 換新的存取權杖（失效時回 errRelogin）。
func (s *Service) refresh(ctx context.Context, st *oauthState) (tokenResponse, error) {
	if st.RefreshToken == "" {
		return tokenResponse{}, errRelogin
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", st.RefreshToken)
	if st.Resource != "" {
		form.Set("resource", st.Resource)
	}
	return s.tokenRequest(ctx, st, form)
}

func (s *Service) tokenRequest(ctx context.Context, st *oauthState, form url.Values) (tokenResponse, error) {
	var tr tokenResponse
	basic := st.AuthMethod == "client_secret_basic" && st.ClientSecret != ""
	if !basic {
		form.Set("client_id", st.ClientID)
		if st.ClientSecret != "" && st.AuthMethod != "none" {
			form.Set("client_secret", st.ClientSecret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, st.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tr, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(st.ClientID), url.QueryEscape(st.ClientSecret))
	}
	resp, err := s.http().Do(req)
	if err != nil {
		return tr, fmt.Errorf("連不上授權伺服器：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tr, err
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "application/x-www-form-urlencoded") || strings.Contains(ct, "text/plain") {
		// 少數舊服務（例：GitHub 的舊端點）回表單格式。
		if v, err := url.ParseQuery(string(body)); err == nil && (v.Get("access_token") != "" || v.Get("error") != "") {
			tr = tokenResponse{AccessToken: v.Get("access_token"), TokenType: v.Get("token_type"), RefreshToken: v.Get("refresh_token"),
				Scope: v.Get("scope"), Error: v.Get("error"), Description: v.Get("error_description"), ExpiresIn: json.Number(v.Get("expires_in"))}
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		_ = dec.Decode(&tr)
	}
	if tr.Error != "" || resp.StatusCode >= 300 || tr.AccessToken == "" {
		if tr.Error == "invalid_grant" || tr.Error == "invalid_token" || (resp.StatusCode == http.StatusUnauthorized && form.Get("grant_type") == "refresh_token") {
			return tr, errRelogin
		}
		msg := tr.Description
		if msg == "" {
			msg = tr.Error
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return tr, fmt.Errorf("授權伺服器拒絕了：%s", truncate(msg, 300))
	}
	return tr, nil
}

// apply 把權杖回覆存進 st（沒有新的 refresh token 時保留舊的，RFC 6749 §6）。
func (st *oauthState) apply(tr tokenResponse, now time.Time) {
	st.AccessToken = tr.AccessToken
	st.TokenType = tr.TokenType
	if tr.RefreshToken != "" {
		st.RefreshToken = tr.RefreshToken
	}
	st.Expiry = 0
	if n, err := strconv.ParseInt(tr.ExpiresIn.String(), 10, 64); err == nil && n > 0 {
		st.Expiry = now.Unix() + n
	}
}

// expiring 回答存取權杖是不是快過期了（不知道到期時間的不算）。
func (st *oauthState) expiring(now time.Time, skew time.Duration) bool {
	return st.AccessToken == "" || (st.Expiry > 0 && now.Add(skew).Unix() >= st.Expiry)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
