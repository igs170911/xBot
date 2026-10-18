package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xbot/internal/auth"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Prefix 是操作 API 的路徑開頭：POST /api/v1/<領域>.<動作>（操作名稱去掉 xbot.）。
const Prefix = "/api/v1/"

// 請求與回應標頭。
const (
	HeaderRequestID = "X-Request-Id" // MCP-4：同一個 request_id 重送不重做；會改東西的操作沒帶時由服務產生並回傳
	HeaderDryRun    = "X-Dry-Run"    // 1：只回傳會發生什麼（MCP-4）
)

// Route 回傳操作的 HTTP 路徑，例：xbot.bots.create → /api/v1/bots.create。
func Route(opName string) string {
	return Prefix + strings.TrimPrefix(opName, "xbot.")
}

// StatusFor 是錯誤代碼對應的 HTTP 狀態碼。回應內容一律是 {"error": {code, message, retryable, needs_approval_id?}}（MCP-5），
// 呼叫端應該看 code，不要只看狀態碼。
func StatusFor(code ops.Code) int { return auth.HTTPStatus(code) }

type errorBody struct {
	Error *ops.Error `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, e *ops.Error) {
	if status == 0 {
		status = StatusFor(e.Code)
	}
	writeJSON(w, status, errorBody{Error: e})
}

type ctxKey struct{}

func identityFrom(ctx context.Context) ops.Identity {
	id, _ := ctx.Value(ctxKey{}).(ops.Identity)
	return id
}

// guard 擋掉跨站請求、驗證身分，把身分放進 context。
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.SameOrigin(r) {
			writeError(w, 0, ops.Forbidden("不接受從其他網站來的請求"))
			return
		}
		id, err := s.auth.Identify(r)
		if err != nil {
			s.authError(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	}
}

func (s *server) authError(w http.ResponseWriter, err error) {
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter/time.Second)+1))
		e := ops.Limit(rl.Error())
		e.Retryable = true
		writeError(w, http.StatusTooManyRequests, e)
	case errors.Is(err, auth.ErrBadToken):
		writeError(w, http.StatusUnauthorized, ops.Forbidden("登入碼或權杖不正確"))
	case errors.Is(err, auth.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, ops.Forbidden("請先登入"))
	default:
		writeError(w, 0, ops.AsError(err))
	}
}

// readBody 讀請求內容：有內容時必須是 application/json（擋掉不需要預檢的跨站表單）；空白當成 {}。
func readBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, *ops.Error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.APIBodyMax))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, ops.InvalidInput(ops.FieldError{Message: "內容太大"})
		}
		return nil, ops.InvalidInput(ops.FieldError{Message: "讀不到請求內容"})
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return json.RawMessage("{}"), nil
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		return nil, ops.InvalidInput(ops.FieldError{Message: "Content-Type 要是 application/json"})
	}
	return body, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// opHandler 是一個操作的 HTTP 入口。
func (s *server) opHandler(info ops.Info) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, perr := readBody(w, r)
		if perr != nil {
			writeError(w, 0, perr)
			return
		}
		opts := ops.Options{RequestID: strings.TrimSpace(r.Header.Get(HeaderRequestID)), DryRun: truthy(r.Header.Get(HeaderDryRun))}
		if opts.RequestID == "" && info.Mutates && !opts.DryRun {
			opts.RequestID = store.NewID() // 呼叫端可以用回傳的 X-Request-Id 安全重送
		}
		if opts.RequestID != "" {
			w.Header().Set(HeaderRequestID, opts.RequestID)
		}
		res, err := s.reg.Invoke(r.Context(), identityFrom(r.Context()), info.Name, raw, opts)
		if err != nil {
			writeError(w, 0, ops.AsError(err))
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// listOps：GET /api/v1/ops，列出所有操作和它們的 JSON Schema。
func (s *server) listOps(w http.ResponseWriter, _ *http.Request) {
	type route struct {
		ops.Info
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	list := s.reg.List()
	out := make([]route, 0, len(list))
	for _, info := range list {
		out = append(out, route{Info: info, Method: http.MethodPost, Path: Route(info.Name)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": ops.Version, "ops": out})
}

// ── 登入（不是操作：它是 HTTP 出口自己的事，用來換 cookie）─────────

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !auth.SameOrigin(r) {
		writeError(w, 0, ops.Forbidden("不接受從其他網站來的請求"))
		return
	}
	raw, perr := readBody(w, r)
	if perr != nil {
		writeError(w, 0, perr)
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		writeError(w, 0, ops.InvalidInput(ops.FieldError{Message: "格式不對"}))
		return
	}
	if err := s.auth.Login(w, r, in.Token); err != nil {
		s.authError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if !auth.SameOrigin(r) {
		writeError(w, 0, ops.Forbidden("不接受從其他網站來的請求"))
		return
	}
	s.auth.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// authStatus：GET /api/v1/auth/status，前端用它決定要不要顯示登入畫面。
func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	id, err := s.auth.Identify(r)
	body := map[string]any{"authenticated": err == nil, "local": auth.IsLocal(r)}
	if err == nil {
		body["kind"] = id.Kind
		body["name"] = id.DisplayName()
	}
	writeJSON(w, http.StatusOK, body)
}
