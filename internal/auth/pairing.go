package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// PairPath 是代理用配對碼換權杖的端點（不需要登入；配對碼本身就是一次性的憑證）。
const PairPath = "/api/v1/agents.pair"

// pairAlphabet 去掉 0/O、1/I/L 這類容易看錯的字。
const pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// PairGrant 是配對碼換到的權杖內容（由使用者在 pair_start 決定）。
type PairGrant struct {
	Name      string
	Scopes    []ops.Scope
	Resources *ops.Resources // nil 表示不限制
	TokenDays int            // 權杖有效天數；0 表示不過期
	// 上限（MCP-12）；0 表示不另外限制。
	DailyCalls       int
	DailySpendMicros int64
}

type pendingCode struct {
	grant   PairGrant
	expires time.Time
}

type failures struct {
	count       int
	lockedUntil time.Time
}

// failTracker 記錄配對碼換錯的次數：每個來源 PairMaxFailures 次、全部合計 PairGlobalMaxFailures 次就鎖 PairLockout。
// 代理配對（Pairing）與手機登入（DevicePairing）共用。呼叫端要自己上鎖。
type failTracker map[string]*failures // key：來源 IP；"*" 是全部合計

func (t failTracker) sweep(now time.Time) {
	for k, f := range t {
		if f.count == 0 && now.After(f.lockedUntil) {
			delete(t, k)
		}
	}
}

func (t failTracker) locked(now time.Time, source string) bool {
	for _, k := range []string{source, "*"} {
		if f := t[k]; f != nil && now.Before(f.lockedUntil) {
			return true
		}
	}
	return false
}

func (t failTracker) fail(now time.Time, source string) {
	for _, k := range []string{source, "*"} {
		limit := limits.PairMaxFailures
		if k == "*" {
			limit = limits.PairGlobalMaxFailures
		}
		f := t[k]
		if f == nil {
			f = &failures{}
			t[k] = f
		}
		f.count++
		if f.count >= limit {
			f.count = 0
			f.lockedUntil = now.Add(limits.PairLockout)
		}
	}
}

// Pairing 管理配對碼（MCP-6）：一次性、5 分鐘有效、換錯太多次會鎖住。
// 配對碼只放在記憶體：服務重開就失效，請使用者重新產生即可。
type Pairing struct {
	DB  *store.DB
	Now func() time.Time // 測試用；空白用 time.Now

	mu    sync.Mutex
	codes map[string]pendingCode
	fails failTracker
}

// NewPairing 建立配對管理。
func NewPairing(db *store.DB) *Pairing {
	return &Pairing{DB: db}
}

func (p *Pairing) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Start 產生一個配對碼（顯示成 XXXX-XXXX）。
func (p *Pairing) Start(g PairGrant) (code string, expires time.Time, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.sweep(now)
	if len(p.codes) >= limits.PairMaxPending {
		return "", time.Time{}, ops.Limit(fmt.Sprintf("同時最多 %d 個還沒用的配對碼，請等舊的過期後再試", limits.PairMaxPending))
	}
	if p.codes == nil {
		p.codes = map[string]pendingCode{}
	}
	for {
		code = randomCode()
		if _, dup := p.codes[code]; !dup {
			break
		}
	}
	expires = now.Add(limits.PairCodeTTL)
	g.Scopes = slices.Clone(g.Scopes)
	p.codes[code] = pendingCode{grant: g, expires: expires}
	return FormatPairCode(code), expires, nil
}

func randomCode() string {
	b := make([]byte, limits.PairCodeLen)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairAlphabet[int(b[i])%len(pairAlphabet)]
	}
	return string(b)
}

// FormatPairCode 把配對碼排成 XXXX-XXXX，方便唸和抄。
func FormatPairCode(code string) string {
	if len(code) <= 4 {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// NormalizePairCode 去掉分隔線與空白、轉大寫。
func NormalizePairCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

func (p *Pairing) sweep(now time.Time) {
	for c, pc := range p.codes {
		if !now.Before(pc.expires) {
			delete(p.codes, c)
		}
	}
	p.fails.sweep(now)
}

// PairResult 是換權杖成功的結果。Token 只會出現這一次。
type PairResult struct {
	Token string      `json:"token"`
	Agent PairedAgent `json:"agent"`
}

// PairedAgent 是配對完成的代理（不含權杖）。
type PairedAgent struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Scopes    []ops.Scope   `json:"scopes"`
	Resources ops.Resources `json:"resources"`
	ExpiresAt int64         `json:"expires_at"`
}

// Complete 用配對碼換權杖。source 是來源（IP），用來計算換錯的次數。name 可以覆寫 pair_start 時的名字。
func (p *Pairing) Complete(ctx context.Context, code, name, source string) (PairResult, *ops.Error) {
	code = NormalizePairCode(code)
	p.mu.Lock()
	now := p.now()
	p.sweep(now)
	if p.fails.locked(now, source) {
		p.mu.Unlock()
		e := ops.Limit("換錯太多次，請一分鐘後再試")
		e.Retryable = true
		return PairResult{}, e
	}
	pc, ok := p.codes[code]
	if !ok {
		if p.fails == nil {
			p.fails = failTracker{}
		}
		p.fails.fail(now, source)
		p.mu.Unlock()
		return PairResult{}, ops.Forbidden("配對碼不對或已過期，請在 XBot 重新產生一個")
	}
	delete(p.codes, code) // 一次性：先拿掉，之後就算建立失敗也不能再用
	if p.fails != nil {
		delete(p.fails, source)
	}
	p.mu.Unlock()

	g := pc.grant
	if n := strings.TrimSpace(name); n != "" && g.Name == "" {
		g.Name = n
	}
	if g.Name == "" {
		g.Name = "外部代理"
	}
	raw, hash := NewAgentToken()
	t := store.AgentToken{Name: g.Name, TokenHash: hash, DailyCalls: g.DailyCalls, DailySpendMicros: g.DailySpendMicros}
	for _, s := range g.Scopes {
		t.Scopes = append(t.Scopes, string(s))
	}
	if g.Resources != nil {
		b, err := json.Marshal(g.Resources)
		if err != nil {
			return PairResult{}, ops.Internal(err)
		}
		t.Resources = b
	}
	if g.TokenDays > 0 {
		t.ExpiresAt = now.Add(time.Duration(g.TokenDays) * 24 * time.Hour).UnixMilli()
	}
	var created store.AgentToken
	err := p.DB.Tx(ctx, func(q *store.Queries) error {
		var err error
		if created, err = q.CreateAgentToken(ctx, t); err != nil {
			return err
		}
		params, _ := json.Marshal(map[string]any{"name": g.Name, "scopes": g.Scopes})
		_, err = q.AddAudit(ctx, store.AuditEntry{Actor: "agent:" + created.ID, Via: "http", Op: "xbot.agents.pair_complete",
			Target: created.ID, Params: params, Status: "ok"})
		return err
	})
	if err != nil {
		return PairResult{}, ops.Internal(err)
	}
	res := PairResult{Token: raw, Agent: PairedAgent{ID: created.ID, Name: created.Name, Scopes: slices.Clone(g.Scopes), ExpiresAt: created.ExpiresAt}}
	if g.Resources != nil {
		res.Agent.Resources = *g.Resources
	}
	return res, nil
}

// pairRequest 是 POST /api/v1/agents.pair 的內容。
type pairRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Handler 是 POST /api/v1/agents.pair：不需要登入，用配對碼換權杖。
// 成功回 200 {token, agent}；失敗回 {"error": {code, message, retryable}}（MCP-5）。
func (p *Pairing) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			WriteError(w, http.StatusMethodNotAllowed, ops.Errorf(ops.CodeInvalidInput, "請用 POST"))
			return
		}
		var req pairRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, ops.InvalidInput(ops.FieldError{Message: "內容要是 JSON：{\"code\": \"配對碼\"}"}))
			return
		}
		if strings.TrimSpace(req.Code) == "" {
			WriteError(w, http.StatusBadRequest, ops.InvalidInput(ops.FieldError{Field: "code", Message: "必填"}))
			return
		}
		if len([]rune(req.Name)) > 100 {
			WriteError(w, http.StatusBadRequest, ops.InvalidInput(ops.FieldError{Field: "name", Message: "最多 100 個字"}))
			return
		}
		res, e := p.Complete(r.Context(), req.Code, req.Name, remoteIP(r))
		if e != nil {
			WriteError(w, StatusFor(e), e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(res)
	})
}

// WriteError 寫出 MCP-5 格式的錯誤：{"error": {...}}。
func WriteError(w http.ResponseWriter, status int, e *ops.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": e})
}

// StatusFor 是錯誤對應的 HTTP 狀態（見 HTTPStatus）。
func StatusFor(e *ops.Error) int { return HTTPStatus(e.Code) }

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsLoopbackRequest 回答請求是不是從這台機器直接連進來（來源與 Host 都是 localhost；
// 經過反向代理或 Tailscale serve 的請求 Host 不是 localhost，不算）。
func IsLoopbackRequest(r *http.Request) bool {
	ip := net.ParseIP(remoteIP(r))
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	hip := net.ParseIP(host)
	return hip != nil && hip.IsLoopback()
}
