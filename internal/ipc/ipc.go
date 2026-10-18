// Package ipc 是每個 Bot 的 product.sock：容器裡的 hermes_host.py 把模型請求（/model/*）與 XBot 工具（/product）
// 轉送到這個 Unix socket，身分用該 Bot 的 worker-key（同 LazyBoy server/main.py 的 /internal/* 路由）。
//
// 本機主機時 socket 就在 <data>/bots/<id>/ipc/product.sock，該資料夾唯讀掛進容器的 /product-ipc。
// 遠端主機（B11）會把同一個 socket 經 SSH 反向轉發過去。
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SocketName 是 socket 的檔名（box/hermes_host.py 連 /product-ipc/product.sock）。
const SocketName = "product.sock"

// ProvidersPrefix 是 hermes_host.py 轉送模型請求用的路徑：/internal/providers/<供應商>/v1/<路徑>。
const ProvidersPrefix = "/internal/providers/"

// MCPPath 是 XBot MCP（C11）在 socket 上的路徑（同 mcpserver.BotPath）；box/xbot_mcp.py 連這裡。
// ProductPath 是 hermes_host.py 的 /product 轉送過來的路徑，接到同一個 MCP。
const (
	MCPPath     = "/internal/mcp"
	ProductPath = "/internal/product"
)

// AppsPrefix 是連接的應用程式（C34）在 socket 上的路徑：/internal/apps/<連接名字>。
// 容器裡的 XBot MCP 橋接（XBOT_MCP_PATH）把 MCP 轉到這裡，XBot 檢查這個 Bot 有沒有授權、帶上權杖再轉給遠端 MCP。
const AppsPrefix = "/internal/apps/"

// ModelPrefix 是 Model Proxy 的掛載點（internal/proxy.Prefix）；收到的請求改寫成這個開頭再交給它。
const ModelPrefix = "/model/"

// KeyVerifier 驗證 worker-key 並回傳它所屬的 Bot；*proxy.WorkerKeys 符合。
type KeyVerifier interface {
	Verify(ctx context.Context, key string) (string, error)
}

// Options 是 Server 的設定。
type Options struct {
	// Dir 回傳 Bot 的 ipc 資料夾（本機：<data>/bots/<id>/ipc）。
	Dir func(botID string) string
	// Model 是 Model Proxy 的處理器（掛在 /model/）。nil 時模型請求回 503。
	Model http.Handler
	// Keys 驗證 worker-key，確認請求來自這個 socket 所屬的 Bot。
	Keys   KeyVerifier
	Logger *slog.Logger
}

// Server 管理每個 Bot 的 product.sock。
type Server struct {
	o  Options
	mu sync.Mutex
	// bots 是正在聽的 socket。
	bots map[string]*listener
	// product 產生每個 Bot 的 XBot MCP 處理器（SetProduct）；還沒設定時回 503。
	product atomic.Pointer[func(botID string) http.Handler]
	// apps 是連接的應用程式的轉送（SetApps，C34）；還沒設定時回 503。
	apps atomic.Pointer[func(botID string) http.Handler]
}

// SetApps 接上連接的應用程式的轉送（apps.Service.Proxy）。worker-key 由這裡先驗證過，處理器拿到的 botID 就是呼叫的 Bot。
func (s *Server) SetApps(f func(botID string) http.Handler) { s.apps.Store(&f) }

// SetProduct 接上 XBot MCP（mcpserver.BotHandler）。操作登錄表在 socket 之後才建好，所以另外設定。
// 處理器自己驗證 worker-key，身分固定是這個 socket 的 Bot。
func (s *Server) SetProduct(f func(botID string) http.Handler) { s.product.Store(&f) }

type listener struct {
	path string
	info os.FileInfo
	srv  *http.Server
}

// New 建立 Server。
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Server{o: o, bots: map[string]*listener{}}
}

// Path 回傳 Bot 的 socket 路徑。
func (s *Server) Path(botID string) string { return filepath.Join(s.o.Dir(botID), SocketName) }

// Ensure 確保 Bot 的 product.sock 正在聽；已經在聽而且檔案還在時什麼都不做。開機前呼叫。
func (s *Server) Ensure(botID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.Path(botID)
	if l, ok := s.bots[botID]; ok {
		if info, err := os.Lstat(path); err == nil && os.SameFile(info, l.info) {
			return nil
		}
		// 檔案被刪掉或換掉了（例：資料夾被清掉），重新開一個。
		_ = l.srv.Close()
		delete(s.bots, botID)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return fmt.Errorf("無法建立 Bot 的通訊資料夾：%w", err)
	}
	// 上次服務留下的 socket 檔：沒人在聽了，直接換掉。
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("無法清掉舊的 %s：%w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("無法建立 Bot 的 product.sock：%w", err)
	}
	// 容器裡的 Bot 使用者和服務不一定是同一個 UID；資料夾是 0711（看不到內容，只能用知道名字的檔案），
	// 而且只掛進這個 Bot 的容器，所以 socket 本身開放連線，身分靠 worker-key。
	if err := os.Chmod(path, 0o666); err != nil {
		ln.Close()
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: s.handler(botID), ReadHeaderTimeout: 10 * time.Second}
	s.bots[botID] = &listener{path: path, info: info, srv: srv}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.o.Logger.Warn("product.sock 停止服務", "bot", botID, "err", err)
		}
	}()
	return nil
}

// Close 關掉一個 Bot 的 socket（刪除 Bot 時用）。
func (s *Server) Close(botID string) error {
	s.mu.Lock()
	l, ok := s.bots[botID]
	delete(s.bots, botID)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	err := l.srv.Close()
	if info, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(info, l.info) {
		_ = os.Remove(l.path)
	}
	return err
}

// Shutdown 關掉所有 socket。
func (s *Server) Shutdown() error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.bots))
	for id := range s.bots {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var errs []error
	for _, id := range ids {
		errs = append(errs, s.Close(id))
	}
	return errors.Join(errs...)
}

func (s *Server) handler(botID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ProvidersPrefix, func(w http.ResponseWriter, r *http.Request) { s.model(botID, w, r) })
	var once sync.Once
	var product http.Handler
	mcp := func(w http.ResponseWriter, r *http.Request) {
		f := s.product.Load()
		if f == nil {
			fail(w, http.StatusServiceUnavailable, "product_disabled", "XBot 工具還沒啟用")
			return
		}
		once.Do(func() { product = (*f)(botID) })
		product.ServeHTTP(w, r)
	}
	mux.HandleFunc(MCPPath, mcp)
	mux.HandleFunc(ProductPath, mcp)
	var appsOnce sync.Once
	var apps http.Handler
	mux.HandleFunc(AppsPrefix, func(w http.ResponseWriter, r *http.Request) {
		f := s.apps.Load()
		if f == nil {
			fail(w, http.StatusServiceUnavailable, "apps_disabled", "連接應用程式還沒啟用")
			return
		}
		if !s.ownKey(botID, r) {
			fail(w, http.StatusUnauthorized, "invalid_worker_key", "需要這個 Bot 的 worker-key")
			return
		}
		appsOnce.Do(func() { apps = (*f)(botID) })
		apps.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fail(w, http.StatusNotFound, "not_found", "product.sock 沒有這個路徑")
	})
	return mux
}

// model 把 /internal/providers/<p>/v1/<路徑> 改寫成 /model/<p>/v1/<路徑> 交給 Model Proxy，
// 並確認 worker-key 屬於這個 socket 的 Bot：一個 Bot 不能拿別人的 key 從自己的 socket 進來。
func (s *Server) model(botID string, w http.ResponseWriter, r *http.Request) {
	if s.o.Model == nil {
		fail(w, http.StatusServiceUnavailable, "proxy_disabled", "模型代理還沒啟用")
		return
	}
	if !s.ownKey(botID, r) {
		fail(w, http.StatusUnauthorized, "invalid_worker_key", "需要這個 Bot 的 worker-key")
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = ModelPrefix + strings.TrimPrefix(r.URL.Path, ProvidersPrefix)
	r2.URL.RawPath = ""
	r2.RequestURI = ""
	s.o.Model.ServeHTTP(w, r2)
}

// ownKey 確認請求帶的是這個 socket 所屬 Bot 的 worker-key（Authorization: Bearer 或 X-Api-Key）。
func (s *Server) ownKey(botID string, r *http.Request) bool {
	key := ""
	if auth := r.Header.Get("Authorization"); auth != "" {
		key, _ = strings.CutPrefix(auth, "Bearer ")
	} else {
		key = r.Header.Get("X-Api-Key")
	}
	owner, err := s.o.Keys.Verify(r.Context(), strings.TrimSpace(key))
	return err == nil && owner == botID
}

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": "xbot_product", "code": code},
	})
}
