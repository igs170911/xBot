package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// SessionCookie 是登入後的 cookie 名稱。內容是簽章過的 session，不是登入碼本身（SEC-7：裝置權杖與 cookie 分離）。
const SessionCookie = "xbot_session"

// 驗證失敗的原因。
var (
	ErrUnauthenticated = errors.New("請先登入")
	ErrBadToken        = errors.New("登入碼不正確")
)

// RateLimitedError 表示同一個來源失敗太多次，要等 RetryAfter 之後再試。
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("登入失敗太多次，請 %d 分鐘後再試", int(e.RetryAfter.Minutes()+0.999))
}

// HTTPConfig 是 HTTP 驗證的設定。
type HTTPConfig struct {
	// Tokens 驗證 Bearer 權杖（登入碼或代理權杖），和 MCP 入口共用同一個（C04）。
	// 空白時用 DB 與 AccessToken 建一個。
	Tokens      *Authenticator
	DB          *store.DB
	AccessToken string // 登入碼（資料目錄的 access-token）：session 簽章金鑰由它衍生，也用來登入
	// LocalNoAuth 為 true 時，從本機（localhost）來的請求直接當成使用者本人，不用登入（XBOT_LOCAL_NOAUTH=0 關掉）。
	LocalNoAuth bool
	Logger      *slog.Logger
	Now         func() time.Time // 測試用；空白用 time.Now
}

// HTTP 驗證 HTTP 請求是誰發的（SEC-7 最小版）：
//   - Authorization: Bearer <登入碼> → 使用者本人（給腳本用）
//   - Authorization: Bearer <代理權杖> → 那個代理（agent_token，MCP-6）
//   - 登入後的簽章 cookie → 使用者本人
//   - 從本機 localhost 來、沒有經過反向代理 → 使用者本人（LocalNoAuth 開啟時）
//
// Bearer 權杖的驗證交給 Authenticator（和 MCP 入口共用）；這裡另外加上 cookie、本機免登入與失敗限速。
type HTTP struct {
	cfg  HTTPConfig
	key  []byte
	fail *loginFailures
}

// NewHTTP 建立 HTTP 驗證器。
func NewHTTP(cfg HTTPConfig) *HTTP {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Tokens == nil {
		cfg.Tokens = &Authenticator{DB: cfg.DB, AccessToken: cfg.AccessToken}
	}
	// session 的簽章金鑰由登入碼衍生：換登入碼，舊的 session 全部失效。
	m := hmac.New(sha256.New, []byte(cfg.AccessToken))
	m.Write([]byte("xbot-session-v1"))
	return &HTTP{cfg: cfg, key: m.Sum(nil), fail: &loginFailures{now: cfg.Now, m: map[string]*loginFailure{}}}
}

// Identify 回傳請求的身分。失敗時回 ErrUnauthenticated、ErrBadToken 或 *RateLimitedError。
func (a *HTTP) Identify(r *http.Request) (ops.Identity, error) {
	if raw, ok := bearer(r); ok {
		return a.identifyBearer(r, raw)
	}
	if c, err := r.Cookie(SessionCookie); err == nil && a.validSession(c.Value) {
		return ops.Owner("ui"), nil
	}
	if a.cfg.LocalNoAuth && IsLocal(r) {
		return ops.Owner("ui"), nil
	}
	return ops.Identity{}, ErrUnauthenticated
}

func (a *HTTP) identifyBearer(r *http.Request, raw string) (ops.Identity, error) {
	client := ClientKey(r)
	if wait := a.fail.blocked(client); wait > 0 {
		return ops.Identity{}, &RateLimitedError{RetryAfter: wait}
	}
	if a.cfg.Tokens.DB == nil && (a.cfg.Tokens.AccessToken == "" || raw != a.cfg.Tokens.AccessToken) {
		a.fail.add(client) // 沒有資料庫（測試）：只認登入碼
		return ops.Identity{}, ErrBadToken
	}
	id, err := a.cfg.Tokens.Identify(r.Context(), raw, "http")
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrInvalidToken) {
		return ops.Identity{}, err
	}
	a.fail.add(client)
	return ops.Identity{}, ErrBadToken
}

// Login 用登入碼換一個 session cookie。失敗回 ErrBadToken 或 *RateLimitedError。
func (a *HTTP) Login(w http.ResponseWriter, r *http.Request, token string) error {
	client := ClientKey(r)
	if wait := a.fail.blocked(client); wait > 0 {
		return &RateLimitedError{RetryAfter: wait}
	}
	token = strings.TrimSpace(token)
	if a.cfg.AccessToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.AccessToken)) != 1 {
		a.fail.add(client)
		return ErrBadToken
	}
	a.fail.reset(client)
	exp := a.cfg.Now().Add(limits.SessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: a.newSession(exp), Path: "/", Expires: exp, MaxAge: int(limits.SessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: IsHTTPS(r),
	})
	return nil
}

// Logout 清掉 session cookie。
func (a *HTTP) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: IsHTTPS(r)})
}

// session 格式：<到期 unix 秒>.<亂數>.<HMAC>。
func (a *HTTP) newSession(exp time.Time) string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	body := strconv.FormatInt(exp.Unix(), 10) + "." + hex.EncodeToString(nonce)
	return body + "." + a.sign(body)
}

func (a *HTTP) sign(body string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *HTTP) validSession(v string) bool {
	if a.cfg.AccessToken == "" {
		return false
	}
	i := strings.LastIndexByte(v, '.')
	if i < 0 {
		return false
	}
	body, sig := v[:i], v[i+1:]
	if !hmac.Equal([]byte(sig), []byte(a.sign(body))) {
		return false
	}
	expStr, _, ok := strings.Cut(body, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	return err == nil && a.cfg.Now().Unix() < exp
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, raw, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(raw), true
}

// forwardedHeaders 出現任何一個，就表示請求經過反向代理（例：tailscale serve、nginx），不算本機。
var forwardedHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Tailscale-User-Login"}

// IsLocal 回答請求是不是從這台機器直接來的：對方是 loopback、沒有經過反向代理、Host 是 localhost。
// 檢查 Host 是為了擋 DNS rebinding（惡意網頁把自己的網域指到 127.0.0.1）。同 LazyBoy server/main.py local_client。
func IsLocal(r *http.Request) bool {
	if remote(r) { // 內建 Tailscale 或外部入口來的，永遠不算本機（C35）
		return false
	}
	for _, h := range forwardedHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	if !loopback(r.RemoteAddr) {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SameOrigin 回答請求的 Origin（有的話）是不是和 Host 相同，用來擋跨站請求（CSRF）。
// 沒有 Origin 的請求（curl、腳本、同站 GET）視為同源。同 LazyBoy server/main.py valid_origin。
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// IsHTTPS 回答請求是不是走 HTTPS（直接 TLS，或本機反向代理標了 X-Forwarded-Proto: https）。
func IsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ClientKey 是限速用的來源代號：對方 IP；從本機反向代理來時，用代理標的最後一個 X-Forwarded-For。
func ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if loopback(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

// loginFailures 記錄每個來源的登入失敗次數（SEC-7 登入失敗限速）。
type loginFailures struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]*loginFailure
}

type loginFailure struct {
	n     int
	start time.Time
}

func (f *loginFailures) blocked(client string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.m[client]
	if e == nil {
		return 0
	}
	left := limits.LoginWindow - f.now().Sub(e.start)
	if left <= 0 {
		delete(f.m, client)
		return 0
	}
	if e.n >= limits.LoginFailures {
		return left
	}
	return 0
}

func (f *loginFailures) add(client string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	e := f.m[client]
	if e == nil || now.Sub(e.start) >= limits.LoginWindow {
		e = &loginFailure{start: now}
		f.m[client] = e
	}
	e.n++
	if len(f.m) > limits.LoginTrackMax { // 避免被大量來源灌爆記憶體：清掉過期的
		for k, v := range f.m {
			if now.Sub(v.start) >= limits.LoginWindow {
				delete(f.m, k)
			}
		}
	}
}

func (f *loginFailures) reset(client string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, client)
}

// HTTPStatus 是錯誤代碼對應的 HTTP 狀態碼（/api/v1 的操作路由、配對入口共用）。
// 回應內容一律是 {"error": {code, message, retryable, needs_approval_id?}}（MCP-5），呼叫端應該看 code。
func HTTPStatus(code ops.Code) int {
	switch code {
	case ops.CodeInvalidInput:
		return http.StatusBadRequest // 400
	case ops.CodeForbidden:
		return http.StatusForbidden // 403（沒登入是 401，由驗證那一層回）
	case ops.CodeNotFound:
		return http.StatusNotFound // 404
	case ops.CodeConflict:
		return http.StatusConflict // 409
	case ops.CodePendingApproval:
		return http.StatusAccepted // 202：收下了，等你在「待我處理」按允許；needs_approval_id 是核准 id
	case ops.CodeLimit:
		return http.StatusTooManyRequests // 429
	case ops.CodeBudget:
		return http.StatusPaymentRequired // 402：超過花費上限
	}
	return http.StatusInternalServerError // 500
}
