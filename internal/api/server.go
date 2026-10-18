// Package api 是 XBot 的 HTTP 出口（C03）：健康檢查、前端檔案、模型代理，
// 以及由操作登錄表（internal/ops）產生的 /api/v1 路由、登入、SSE 事件流和前端 TypeScript 型別。
//
// 路由：
//
//	POST /api/v1/<領域>.<動作>   每個操作一條（xbot.bots.create → /api/v1/bots.create）
//	GET  /api/v1/ops             列出所有操作與 JSON Schema
//	GET  /api/v1/events          SSE 事件流
//	POST /api/v1/auth/login      用登入碼換 session cookie；auth/logout 登出；GET auth/status 看登入狀態
//	POST /api/v1/auth/pair       手機用 QR 裡的一次性配對碼換 session cookie（W31，不需要登入）
//	POST /api/v1/agents.pair     代理用配對碼換權杖（C04，不需要登入）
//	/mcp                         MCP Streamable HTTP（C04）
//	POST /hooks/<id>             定時任務的網址觸發（C32，不需要登入，帶金鑰）
//	POST /api/v1/files.upload    檔案上傳（multipart，C33）；GET /api/v1/files.download 檔案下載（Streams）
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"xbot/internal/auth"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
	"xbot/internal/version"
)

// Options 是建立 HTTP 處理器需要的東西。
type Options struct {
	// Web 是打包後的前端；nil 表示沒有嵌入（開發模式或 go test）。
	Web fs.FS
	// ModelProxy 掛在 /model/（internal/proxy.Handler）；nil 表示還沒啟用，回 503。
	ModelProxy http.Handler
	// MCP 掛在 /mcp（internal/mcpserver.Handler，Streamable HTTP）；nil 表示不提供。
	MCP http.Handler
	// AgentPair 掛在 /api/v1/agents.pair（internal/auth.Pairing.Handler）：代理用配對碼換權杖，不需要登入。
	AgentPair http.Handler
	// DevicePair 掛在 POST /api/v1/auth/pair（手機用 QR 裡的一次性配對碼登入，W31）；nil 表示不提供。
	DevicePair *auth.DevicePairing
	// Hooks 掛在 /hooks/（定時任務的網址觸發，C32）：不需要登入，網址 id＋金鑰就是憑證。nil 表示不提供。
	Hooks http.Handler
	// Registry 是操作登錄表；nil 表示不提供 /api/v1 的操作路由。要在呼叫 New 之前登錄好所有操作。
	Registry *ops.Registry
	// Bus 是事件匯流排（SSE 用）；Registry 不是 nil 時必填。
	Bus *events.Bus
	// DB 用來補送斷線期間的事件（event 表）；Registry 不是 nil 時必填。
	DB *store.DB
	// Auth 驗證請求的身分；Registry 不是 nil 時必填。
	Auth *auth.HTTP
	// Streams 是內容不走 JSON 的出口（檔案上傳下載，C33）：「方法 路徑」→ 處理器，例："GET /api/v1/files.download"。
	// 和操作同路徑時（POST /api/v1/files.upload），Content-Type 是 application/json 的請求仍交給操作。
	// 處理器在寫出任何內容前回傳錯誤時，由這裡寫成一般的錯誤回應。
	Streams map[string]StreamHandler
	// Heartbeat 是 SSE 心跳間隔；0 用 limits.SSEHeartbeat（測試可以調短）。
	Heartbeat time.Duration
}

type server struct {
	reg       *ops.Registry
	bus       *events.Bus
	db        *store.DB
	auth      *auth.HTTP
	pair      *auth.DevicePairing
	heartbeat time.Duration
}

// New 回傳整個服務的 HTTP 處理器。
func New(o Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if o.Registry != nil {
		s := &server{reg: o.Registry, bus: o.Bus, db: o.DB, auth: o.Auth, heartbeat: o.Heartbeat, pair: o.DevicePair}
		if s.heartbeat == 0 {
			s.heartbeat = limits.SSEHeartbeat
		}
		if s.bus == nil || s.db == nil || s.auth == nil {
			panic("api.New：有 Registry 時 Bus、DB 與 Auth 都要提供")
		}
		mux.HandleFunc("POST "+Prefix+"auth/login", s.login)
		mux.HandleFunc("POST "+Prefix+"auth/logout", s.logout)
		mux.HandleFunc("GET "+Prefix+"auth/status", s.authStatus)
		if s.pair != nil {
			mux.HandleFunc("POST "+Prefix+"auth/pair", s.pairLogin)
		}
		mux.HandleFunc("GET "+Prefix+"ops", s.guard(s.listOps))
		mux.HandleFunc("GET "+Prefix+"events", s.guard(s.events))
		streams := map[string]StreamHandler{}
		for k, v := range o.Streams {
			streams[k] = v
		}
		for _, info := range s.reg.List() {
			pattern := "POST " + Route(info.Name)
			h := s.opHandler(info)
			if sh, ok := streams[pattern]; ok {
				h = s.streamOrOp(sh, h)
				delete(streams, pattern)
			}
			mux.HandleFunc(pattern, s.guard(h))
		}
		for pattern, sh := range streams {
			mux.HandleFunc(pattern, s.guard(s.stream(sh)))
		}
		mux.HandleFunc(Prefix, s.guard(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, ops.NotFound("沒有這個 API："+r.Method+" "+r.URL.Path))
		}))
	}
	if o.ModelProxy != nil {
		mux.Handle("/model/", o.ModelProxy)
	} else {
		mux.HandleFunc("/model/", proxyDisabled)
	}
	if o.MCP != nil {
		mux.Handle("/mcp", o.MCP)
	}
	if o.AgentPair != nil {
		mux.Handle("/api/v1/agents.pair", o.AgentPair)
	}
	if o.Hooks != nil {
		mux.Handle("/hooks/", o.Hooks)
	}
	// Android 分享目標（W31）：平常由 service worker 攔下；service worker 還沒啟用時退回分享頁，
	// 請使用者再分享一次（內容沒辦法在這裡保存）。
	mux.HandleFunc("POST /share", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/share?nosw=1", http.StatusSeeOther)
	})
	mux.Handle("/", spa(o.Web))
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version.Version})
}

// proxyDisabled 讓容器拿到明確的錯誤，而不是前端的 index.html。
func proxyDisabled(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": "模型代理還沒啟用", "type": "xbot_model_proxy", "code": "proxy_disabled"},
	})
}

const notBuilt = `<!doctype html><meta charset="utf-8"><title>XBot</title>
<body style="font-family:sans-serif;background:#070707;color:#fcfcfc;padding:40px">
<h1>XBot 服務正在執行</h1>
<p>這個執行檔沒有內建前端。開發時請打開 Vite 的網址（make dev 會印出來）；要內建前端請用 make build。</p>`

// spa 提供前端檔案；找不到的路徑回 index.html，交給前端路由。
func spa(files fs.FS) http.Handler {
	if files == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuilt))
		})
	}
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(files, name); errors.Is(err, fs.ErrNotExist) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		switch {
		case strings.HasPrefix(name, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case name == "sw.js":
			// service worker 一定要每次問伺服器有沒有新版（PWA 更新）
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Service-Worker-Allowed", "/")
		case name == "manifest.webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
