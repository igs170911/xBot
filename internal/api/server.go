// Package api 是 XBot 的 HTTP 出口：健康檢查、前端檔案，
// 之後由操作登錄表（internal/ops）產生 /api/v1 路由與 SSE 事件流（C03）。
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

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
}

// New 回傳整個服務的 HTTP 處理器。
func New(o Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
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
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}
