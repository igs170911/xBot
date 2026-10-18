package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"xbot/internal/limits"
	"xbot/internal/version"
)

// Prefix 是 Model Proxy 掛載的網址開頭：/model/<供應商>/v1/<路徑>。
const Prefix = "/model/"

// allowedPaths 是容器可以呼叫的模型 API（LazyBoy server/main.py provider_proxy 的 allowed；
// PRD EN-3：chat/responses/messages/models/embeddings/images/audio）。
var allowedPaths = map[string]bool{
	"chat/completions":     true,
	"responses":            true,
	"messages":             true,
	"models":               true,
	"embeddings":           true,
	"images/generations":   true,
	"audio/transcriptions": true,
}

// responses/<id>、responses/<id>/cancel 等（LazyBoy 允許 responses/ 開頭）。
var responsesSubpath = regexp.MustCompile(`^responses(/[A-Za-z0-9_-]+)+$`)

// meteredPaths 是會記用量的路徑（LazyBoy：chat/completions、responses、messages、embeddings）。
var meteredPaths = map[string]bool{
	"chat/completions": true,
	"responses":        true,
	"messages":         true,
	"embeddings":       true,
}

// 只轉送這些請求標頭給供應商；容器送來的 Authorization、x-api-key、Cookie 一律不轉。
var forwardRequestHeaders = []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta", "Openai-Beta"}

// 只把這些回應標頭還給容器。
var forwardResponseHeaders = []string{"Content-Type", "Cache-Control", "Retry-After", "X-Request-Id", "Request-Id"}
var forwardResponseHeaderPrefixes = []string{"Openai-", "X-Ratelimit-", "Anthropic-Ratelimit-"}

// AllowedPath 回答容器能不能呼叫這個路徑（/v1/ 之後的部分）。
func AllowedPath(p string) bool {
	return allowedPaths[p] || responsesSubpath.MatchString(p)
}

// ProviderSource 讀出供應商（含真金鑰）；*Providers 就是一個。
type ProviderSource interface {
	Get(ctx context.Context, id string) (Provider, error)
}

// Options 是建立 Handler 需要的東西。
type Options struct {
	Keys      *WorkerKeys
	Providers ProviderSource
	Usage     UsageLedger
	// Client 預設是專用的連線池；測試可換。
	Client *http.Client
	// MaxBodyBytes 預設 limits.ModelProxyBodyBytes。
	MaxBodyBytes int64
	Logger       *slog.Logger
	Now          func() time.Time
	// Bindings 決定每個 Bot 能用哪個供應商、備援模型是什麼；nil 時不限制（測試用）。
	Bindings Binder
	// Budget 檢查花費上限；nil 時不限制。
	Budget BudgetGuard
	// Pricing 查模型價格算花費；nil 時只記供應商自己回報的花費。
	Pricing Pricer
	// IdleTimeout 是串流兩段資料之間最久等多久；預設 limits.ModelStreamIdleTimeout。
	IdleTimeout time.Duration
	// Session 回傳 Bot 目前這段對話的穩定 id（runtime 的對話串 id），給要求對話識別標頭的供應商（OpenCode）；
	// 回空白時用「xbot-<Bot id>」。可為 nil。
	Session func(botID string) string
	// ProviderError 在供應商回錯誤（4xx、5xx）時呼叫，帶回應標頭與內容的前 limits.QuotaProbeBytes 位元組（已遮掉金鑰），
	// 讓服務認出額度用完、限流（EN-7）。回應照常轉給 Bot。可為 nil。
	ProviderError func(botID, provider string, status int, header http.Header, body []byte)
}

// Route 指定一個模型：哪個供應商的哪個模型、用哪種呼叫方式。
type Route struct {
	Provider  string
	Model     string
	Transport string
}

// Binding 是一個 Bot 的模型綁定：容器只能用 Provider；Fallback 是主要模型忙碌時改用的模型（可為 nil）。
type Binding struct {
	Provider string
	Fallback *Route
}

// Binder 回答一個 Bot 的模型綁定（runtime.Computers 實作）。
type Binder interface {
	Binding(ctx context.Context, botID string) (Binding, error)
}

// BudgetGuard 檢查花費上限（*Budgets 實作）。
type BudgetGuard interface {
	Check(ctx context.Context, botID string) error // 超過時回 *BudgetError
	CheckProvider(ctx context.Context, botID, providerID string) error
	Exceeded(ctx context.Context, e *BudgetError) (bool, error)
}

// Pricer 查模型價格（*Pricing 實作）。
type Pricer interface {
	Lookup(ctx context.Context, provider, model string) (Price, string, bool, error)
}

// Handler 是掛在 /model/ 的 HTTP 處理器：驗 worker-key → 檢查路徑 → 換上真金鑰轉送 → 串流回傳 → 記用量。
type Handler struct {
	o Options
}

// NewHandler 建立 Handler。
func NewHandler(o Options) *Handler {
	if o.Client == nil {
		o.Client = NewUpstreamClient()
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = limits.ModelProxyBodyBytes
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = limits.ModelStreamIdleTimeout
	}
	return &Handler{o: o}
}

// NewUpstreamClient 是到供應商的共用連線池：每次重新連線要多花一次 TLS 交握，長任務會打上百次。
// 不設整體時限，串流回應可以很長；只限制連線與等第一個位元組的時間。
func NewUpstreamClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: limits.ModelConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   limits.ModelConnectTimeout,
			ResponseHeaderTimeout: limits.ModelResponseHeaderTimeout,
			MaxConnsPerHost:       limits.ModelMaxConns,
			MaxIdleConns:          limits.ModelMaxIdleConns,
			MaxIdleConnsPerHost:   limits.ModelMaxIdleConns,
			IdleConnTimeout:       limits.ModelIdleConnTimeout,
			ForceAttemptHTTP2:     true,
		},
		// 供應商的轉址不跟：跟過去會把真金鑰送到別的地方。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "模型代理只接受 GET 和 POST")
		return
	}
	providerID, apiPath, ok := splitPath(r.URL.Path)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", "網址格式應為 /model/<供應商>/v1/<路徑>")
		return
	}

	// 先驗身分，沒有 key 的人看不到任何其他資訊。
	botID, err := h.o.Keys.Verify(r.Context(), workerKeyFrom(r))
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) {
			h.o.Logger.Error("模型代理：驗證 worker-key 失敗", "err", err)
			fail(w, http.StatusInternalServerError, "internal_error", "模型代理暫時無法驗證身分")
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="xbot-model-proxy"`)
		fail(w, http.StatusUnauthorized, "invalid_worker_key", "需要有效的 worker-key")
		return
	}

	if !AllowedPath(apiPath) {
		fail(w, http.StatusForbidden, "endpoint_not_allowed", "模型代理不開放這個路徑："+apiPath)
		return
	}

	// 每個 Bot 只能用它設定的模型所屬的供應商（金鑰不能被別的 Bot 借用）。
	var binding Binding
	if h.o.Bindings != nil {
		binding, err = h.o.Bindings.Binding(r.Context(), botID)
		if err != nil {
			h.o.Logger.Warn("模型代理：讀不到 Bot 的模型設定", "bot", botID, "err", err)
			fail(w, http.StatusForbidden, "provider_not_allowed", "這個 Bot 還沒設定可以用的模型")
			return
		}
		if binding.Provider != providerID {
			fail(w, http.StatusForbidden, "provider_not_allowed", "這個 Bot 只能用它設定的模型供應商（"+binding.Provider+"）")
			return
		}
	}

	provider, err := h.load(r.Context(), providerID, false)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			fail(w, http.StatusNotFound, "provider_not_found", "還沒有設定這個模型供應商："+providerID)
			return
		}
		if errors.Is(err, ErrOAuthRelogin) {
			fail(w, http.StatusUnauthorized, "oauth_relogin_required", "訂閱登入已失效，請到模型設定重新登入（"+providerID+"）")
			return
		}
		h.o.Logger.Error("模型代理：讀不到供應商設定", "provider", providerID, "err", err)
		fail(w, http.StatusInternalServerError, "internal_error", "讀不到模型供應商設定")
		return
	}
	if provider.Auth != "" && !codexPathAllowed(r.Method, apiPath) {
		fail(w, http.StatusForbidden, "endpoint_not_allowed", "訂閱登入的供應商只開放 responses："+apiPath)
		return
	}

	if r.ContentLength > h.o.MaxBodyBytes {
		fail(w, http.StatusRequestEntityTooLarge, "request_too_large", "請求內容太大")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.o.MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, "request_too_large", "請求內容太大")
			return
		}
		fail(w, http.StatusBadRequest, "bad_request", "讀不到請求內容")
		return
	}

	// 花費上限（US-2）：會花錢的請求在送出前檢查。
	metered := meteredPaths[apiPath]
	if metered && h.o.Budget != nil {
		err := h.o.Budget.Check(r.Context(), botID)
		if err == nil {
			err = h.o.Budget.CheckProvider(r.Context(), botID, providerID)
		}
		if err != nil {
			var be *BudgetError
			if errors.As(err, &be) {
				h.exceeded(r.Context(), be)
				fail(w, http.StatusPaymentRequired, "budget_exceeded", be.Error())
				return
			}
			h.o.Logger.Error("模型代理：檢查花費上限失敗", "bot", botID, "err", err)
			fail(w, http.StatusInternalServerError, "internal_error", "暫時無法確認花費上限")
			return
		}
	}

	model := requestModel(body)
	start := h.o.Now()
	resp, cancel, err := h.forward(r, h.session(botID), provider, apiPath, body)
	// 訂閱登入：供應商回 401 表示存取權杖被撤銷或過期，換新一次再送（還沒送出任何位元組）。
	if err == nil && resp.StatusCode == http.StatusUnauthorized && provider.Auth != "" {
		if fresh, ferr := h.refresh(r.Context(), provider, true); ferr == nil {
			_ = resp.Body.Close()
			cancel()
			provider = fresh
			resp, cancel, err = h.forward(r, h.session(botID), provider, apiPath, body)
		} else {
			h.o.Logger.Warn("模型代理：訂閱登入換新權杖失敗", "provider", provider.ID, "err", ferr)
		}
	}
	fellBack := false
	// 備援（EN-5）：供應商忙碌或出錯、而且還沒送出任何位元組時，改用設定好的備援模型重試一次。
	if fb := fallbackRoute(binding, r.Method, apiPath, body, providerID, model); fb != nil &&
		((err != nil && r.Context().Err() == nil) || (err == nil && retryableStatus(resp.StatusCode))) {
		fp, ferr := h.load(r.Context(), fb.Provider, false)
		if ferr == nil && h.o.Budget != nil && fb.Provider != providerID {
			// 備援的供應商也有自己的上限；超過了就不備援。
			ferr = h.o.Budget.CheckProvider(r.Context(), botID, fb.Provider)
		}
		if ferr == nil {
			reason := "連不上"
			if err == nil {
				reason = resp.Status
				_ = resp.Body.Close()
			}
			cancel()
			h.o.Logger.Warn("模型代理：改用備援模型", "bot", botID, "from", providerID+"/"+model, "to", fb.Provider+"/"+fb.Model, "reason", reason)
			provider, model, fellBack = fp, fb.Model, true
			resp, cancel, err = h.forward(r, h.session(botID), provider, apiPath, withModel(body, fb.Model))
		} else {
			h.o.Logger.Warn("模型代理：不能改用備援模型", "provider", fb.Provider, "err", ferr)
		}
	}
	realKey := string(provider.APIKey)
	if err != nil {
		if r.Context().Err() == nil {
			h.o.Logger.Warn("模型代理：連不上供應商", "bot", botID, "provider", provider.ID, "path", apiPath,
				"err", redactString(err.Error(), realKey))
		}
		fail(w, http.StatusBadGateway, "upstream_unavailable", "連不上模型供應商")
		return
	}
	defer cancel()
	defer resp.Body.Close()

	for name, values := range resp.Header {
		if forwardResponseHeader(name) {
			for _, v := range values {
				w.Header().Add(name, redactString(v, realKey))
			}
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if fellBack {
		w.Header().Set("X-Xbot-Fallback", provider.ID+"/"+model)
	}
	w.WriteHeader(resp.StatusCode)

	streaming := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	var m *meter
	if metered {
		m = newMeter(streaming)
	}
	out := newRedactWriter(w, realKey)
	var src io.Reader = resp.Body
	var probe *capped
	if resp.StatusCode >= 400 && h.o.ProviderError != nil {
		probe = &capped{max: limits.QuotaProbeBytes}
		src = io.TeeReader(resp.Body, probe)
	}
	copyErr := pipe(w, out, src, m)
	if probe != nil {
		h.o.ProviderError(botID, provider.ID, resp.StatusCode, resp.Header.Clone(), []byte(redactString(string(probe.b), realKey)))
	}
	_ = out.flush()
	flush(w)
	if copyErr != nil && r.Context().Err() == nil {
		h.o.Logger.Warn("模型代理：轉送回應中斷", "bot", botID, "provider", provider.ID, "path", apiPath,
			"err", redactString(copyErr.Error(), realKey))
	}

	if m != nil && h.o.Usage != nil {
		c, _ := m.result()
		ctx := context.WithoutCancel(r.Context()) // 容器斷線也要記下已經用掉的量。
		entry := UsageEntry{
			BotID: botID, Provider: provider.ID, Model: model, Endpoint: apiPath,
			InputTokens: c.Input, OutputTokens: c.Output, CachedTokens: c.Cached, CacheWriteTokens: c.CacheWrite,
			ReasoningTokens: c.Reasoning, Cost: c.Cost, Fallback: fellBack,
			Status: resp.StatusCode, OK: resp.StatusCode < 400 && copyErr == nil, At: start,
		}
		if provider.Auth == "" { // 訂閱登入是月費方案，不另外算錢（只記 token）。
			entry.CostMicros, entry.Unpriced = h.cost(ctx, provider.ID, model, c)
		}
		if err := h.o.Usage.RecordUsage(ctx, entry); err != nil {
			h.o.Logger.Error("模型代理：記錄用量失敗", "bot", botID, "provider", provider.ID, "err", err)
		}
		// 這一筆讓花費到了上限：馬上停下工作並發卡，不必等下一個請求。
		if h.o.Budget != nil && entry.CostMicros > 0 {
			var be *BudgetError
			err := h.o.Budget.Check(ctx, botID)
			if err == nil {
				err = h.o.Budget.CheckProvider(ctx, botID, provider.ID)
			}
			if errors.As(err, &be) {
				h.exceeded(ctx, be)
			}
		}
	}
}

// cost 算出一次請求的花費：供應商有回報就用回報的，否則照價目表；都沒有時回 unpriced。
func (h *Handler) cost(ctx context.Context, provider, model string, c counts) (int64, bool) {
	if c.Cost > 0 {
		return int64(math.Round(c.Cost * 1e6)), false
	}
	if h.o.Pricing == nil {
		return 0, false
	}
	pr, _, ok, err := h.o.Pricing.Lookup(ctx, provider, model)
	if err != nil {
		h.o.Logger.Warn("模型代理：讀不到價目表", "err", err)
	}
	if !ok {
		return 0, c.Input+c.Output > 0
	}
	return pr.CostMicros(c.Input, c.Cached, c.CacheWrite, c.Output), false
}

func (h *Handler) exceeded(ctx context.Context, be *BudgetError) {
	if _, err := h.o.Budget.Exceeded(context.WithoutCancel(ctx), be); err != nil {
		h.o.Logger.Error("模型代理：發花費上限卡片失敗", "bot", be.BotID, "err", err)
	}
}

// OAuthRefresher 換新訂閱登入的權杖；*Providers 實作。ProviderSource 有實作它時才支援訂閱登入。
type OAuthRefresher interface {
	Fresh(ctx context.Context, p Provider, force bool) (Provider, error)
}

// load 讀出供應商；訂閱登入的權杖快到期時先換新。
func (h *Handler) load(ctx context.Context, id string, force bool) (Provider, error) {
	p, err := h.o.Providers.Get(ctx, id)
	if err != nil {
		return Provider{}, err
	}
	return h.refresh(ctx, p, force)
}

func (h *Handler) refresh(ctx context.Context, p Provider, force bool) (Provider, error) {
	if p.Auth == "" {
		return p, nil
	}
	rf, ok := h.o.Providers.(OAuthRefresher)
	if !ok {
		return Provider{}, errors.New("模型代理不支援訂閱登入")
	}
	return rf.Fresh(ctx, p, force)
}

// session 回傳這個 Bot 目前對話的穩定 id；沒有進行中的對話時用「xbot-<Bot id>」。
func (h *Handler) session(botID string) string {
	if h.o.Session != nil {
		if s := h.o.Session(botID); s != "" {
			return "xbot-" + s
		}
	}
	return "xbot-" + botID
}

// errStreamIdle 是串流閒置太久時取消請求的原因。
var errStreamIdle = errors.New("模型供應商太久沒有送資料（串流閒置逾時）")

// forward 換上真金鑰把請求送給供應商。回傳的 cancel 要在讀完回應後呼叫。
// 回應內容兩次讀取之間超過 IdleTimeout 就取消請求（LazyBoy httpx read timeout 300 秒）。
func (h *Handler) forward(r *http.Request, session string, provider Provider, apiPath string, body []byte) (*http.Response, context.CancelFunc, error) {
	realKey := string(provider.APIKey)
	target := provider.BaseURL + "/" + apiPath
	query := r.URL.RawQuery
	var cacheKey string
	if provider.Auth == AuthOAuthCodex {
		if r.Method == http.MethodPost {
			body, cacheKey = codexBody(body)
		} else if apiPath == "models" && !r.URL.Query().Has("client_version") {
			// Codex 的模型清單要 client_version（Hermes model_metadata：任何版本都會列出全部）。
			query = "client_version=1.0.0"
		}
	}
	if query != "" {
		target += "?" + query
	}
	ctx, cancelCause := context.WithCancelCause(r.Context())
	cancel := func() { cancelCause(context.Canceled) }
	var reqBody io.Reader
	if r.Method == http.MethodPost {
		reqBody = bytes.NewReader(body)
	}
	up, err := http.NewRequestWithContext(ctx, r.Method, target, reqBody)
	if err != nil {
		cancel()
		return nil, func() {}, errors.New("模型供應商網址有問題")
	}
	for _, name := range forwardRequestHeaders {
		if v := r.Header.Get(name); v != "" {
			up.Header.Set(name, v)
		}
	}
	if r.Method == http.MethodPost && up.Header.Get("Content-Type") == "" {
		up.Header.Set("Content-Type", "application/json")
	}
	// 一律用 XBot 自己的身分（OpenCode 等要求用戶端自報名稱）；容器送來的 User-Agent 不轉送。
	up.Header.Set("User-Agent", "XBot/"+version.Version)
	if name := SessionHeaderFor(provider); name != "" {
		up.Header.Set(name, session)
	}
	if provider.Auth == AuthOAuthCodex {
		setCodexHeaders(up.Header, provider.OAuth, session, cacheKey)
		if up.Header.Get("Accept") == "" && r.Method == http.MethodPost {
			up.Header.Set("Accept", "text/event-stream")
		}
	}
	if realKey != "" {
		up.Header.Set("Authorization", "Bearer "+realKey)
		if apiPath == "messages" {
			// Anthropic 相容端點用 x-api-key。
			up.Header.Set("X-Api-Key", realKey)
			if up.Header.Get("Anthropic-Version") == "" {
				up.Header.Set("Anthropic-Version", "2023-06-01")
			}
		}
	}
	resp, err := h.o.Client.Do(up)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	idle := h.o.IdleTimeout
	resp.Body = &idleBody{ReadCloser: resp.Body, d: idle, t: time.AfterFunc(idle, func() { cancelCause(errStreamIdle) }), ctx: ctx}
	return resp, cancel, nil
}

// idleBody 每讀到一段資料就把閒置計時器重設；計時器到了就取消請求，讀取會回 errStreamIdle。
type idleBody struct {
	io.ReadCloser
	d   time.Duration
	t   *time.Timer
	ctx context.Context
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && errors.Is(context.Cause(b.ctx), errStreamIdle) {
		err = errStreamIdle
	}
	b.t.Reset(b.d)
	return n, err
}

func (b *idleBody) Close() error {
	b.t.Stop()
	return b.ReadCloser.Close()
}

// retryableStatus 是可以改用備援模型的狀態碼（LazyBoy server/main.py：{429, 500, 502, 503, 504}）。
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// fallbackRoute 回傳這個請求可以改用的備援模型；不適用時回 nil。
// 只有模型請求（chat/completions、responses、messages）、備援的呼叫方式和這個請求相同、
// 不是接續先前回應（previous_response_id 只在原供應商有效）時才備援。
func fallbackRoute(b Binding, method, apiPath string, body []byte, providerID, model string) *Route {
	fb := b.Fallback
	if fb == nil || method != http.MethodPost || fb.Model == "" || fb.Provider == "" {
		return nil
	}
	if apiPath != "chat/completions" && apiPath != "responses" && apiPath != "messages" {
		return nil
	}
	if TransportPath(fb.Transport) != apiPath || (fb.Provider == providerID && fb.Model == model) {
		return nil
	}
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil || req == nil {
		return nil
	}
	if _, ok := req["previous_response_id"]; ok {
		return nil
	}
	return fb
}

// withModel 把請求內容的 model 換掉（其他欄位原樣保留）。
func withModel(body []byte, model string) []byte {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	req["model"], _ = json.Marshal(model)
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// pipe 把供應商的回應一段一段轉給容器，每段都 flush，SSE 才能即時送達。
func pipe(w http.ResponseWriter, out io.Writer, src io.Reader, m *meter) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if m != nil {
				_, _ = m.Write(buf[:n])
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			flush(w)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func flush(w http.ResponseWriter) {
	_ = http.NewResponseController(w).Flush()
}

func forwardResponseHeader(name string) bool {
	name = http.CanonicalHeaderKey(name)
	for _, n := range forwardResponseHeaders {
		if name == n {
			return true
		}
	}
	for _, p := range forwardResponseHeaderPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// splitPath 把 /model/<供應商>/v1/<路徑> 拆開。
func splitPath(p string) (provider, apiPath string, ok bool) {
	rest, ok := strings.CutPrefix(p, Prefix)
	if !ok {
		return "", "", false
	}
	provider, rest, ok = strings.Cut(rest, "/")
	if !ok || !ValidProviderID(provider) {
		return "", "", false
	}
	apiPath, ok = strings.CutPrefix(rest, "v1/")
	if !ok || apiPath == "" {
		return "", "", false
	}
	return provider, apiPath, true
}

// workerKeyFrom 讀 Authorization: Bearer；Anthropic 的 SDK 改用 x-api-key（LazyBoy hermes_host.relay 也接受）。
func workerKeyFrom(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if key, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(key)
		}
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

// fail 回傳 OpenAI 相容的錯誤格式，Hermes 等用戶端才看得懂。
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": "xbot_model_proxy", "code": code},
	})
}

// capped 留下寫進來的前 max 個位元組，其餘丟掉。
type capped struct {
	b   []byte
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
