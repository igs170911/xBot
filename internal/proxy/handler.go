package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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

	provider, err := h.o.Providers.Get(r.Context(), providerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			fail(w, http.StatusNotFound, "provider_not_found", "還沒有設定這個模型供應商："+providerID)
			return
		}
		h.o.Logger.Error("模型代理：讀不到供應商設定", "provider", providerID, "err", err)
		fail(w, http.StatusInternalServerError, "internal_error", "讀不到模型供應商設定")
		return
	}
	realKey := string(provider.APIKey)

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

	target := provider.BaseURL + "/" + apiPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var reqBody io.Reader
	if r.Method == http.MethodPost {
		reqBody = bytes.NewReader(body)
	}
	up, err := http.NewRequestWithContext(r.Context(), r.Method, target, reqBody)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal_error", "模型供應商網址有問題")
		return
	}
	for _, name := range forwardRequestHeaders {
		if v := r.Header.Get(name); v != "" {
			up.Header.Set(name, v)
		}
	}
	if r.Method == http.MethodPost && up.Header.Get("Content-Type") == "" {
		up.Header.Set("Content-Type", "application/json")
	}
	up.Header.Set("User-Agent", "XBot/"+version.Version)
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

	start := h.o.Now()
	resp, err := h.o.Client.Do(up)
	if err != nil {
		if r.Context().Err() == nil {
			h.o.Logger.Warn("模型代理：連不上供應商", "bot", botID, "provider", providerID, "path", apiPath,
				"err", redactString(err.Error(), realKey))
		}
		fail(w, http.StatusBadGateway, "upstream_unavailable", "連不上模型供應商")
		return
	}
	defer resp.Body.Close()

	for name, values := range resp.Header {
		if forwardResponseHeader(name) {
			for _, v := range values {
				w.Header().Add(name, redactString(v, realKey))
			}
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)

	streaming := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	var m *meter
	if meteredPaths[apiPath] {
		m = newMeter(streaming)
	}
	out := newRedactWriter(w, realKey)
	copyErr := pipe(w, out, resp.Body, m)
	_ = out.flush()
	flush(w)
	if copyErr != nil && r.Context().Err() == nil {
		h.o.Logger.Warn("模型代理：轉送回應中斷", "bot", botID, "provider", providerID, "path", apiPath,
			"err", redactString(copyErr.Error(), realKey))
	}

	if m != nil && h.o.Usage != nil {
		c, _ := m.result()
		entry := UsageEntry{
			BotID: botID, Provider: providerID, Model: requestModel(body), Endpoint: apiPath,
			InputTokens: c.Input, OutputTokens: c.Output, CachedTokens: c.Cached, ReasoningTokens: c.Reasoning,
			Cost: c.Cost, Status: resp.StatusCode, OK: resp.StatusCode < 400 && copyErr == nil, At: start,
		}
		// 容器斷線也要記下已經用掉的量。
		if err := h.o.Usage.RecordUsage(context.WithoutCancel(r.Context()), entry); err != nil {
			h.o.Logger.Error("模型代理：記錄用量失敗", "bot", botID, "provider", providerID, "err", err)
		}
	}
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
