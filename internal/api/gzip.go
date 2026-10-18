package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// 前端靜態檔壓縮（v1.0 效能）：打包出來的 JS／CSS 原本照原樣送，手機在慢網路上首頁要下載約 880 KB；
// gzip 後約 290 KB。檔名含雜湊、內容不會變，所以每個檔第一次被要的時候壓一次、留在記憶體裡（全部約 1 MB）。

// compressible 是值得壓縮的副檔名（woff2、png 等本來就壓過了）。
var compressible = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".webmanifest": true, ".html": true, ".map": true, ".txt": true}

type gzipCache struct {
	files fs.FS
	mu    sync.Mutex
	done  map[string][]byte // nil 值表示壓了沒有比較小，照原樣送
}

func newGzipCache(files fs.FS) *gzipCache {
	return &gzipCache{files: files, done: map[string][]byte{}}
}

// get 回傳 name 壓縮後的內容；不值得壓或讀不到時回 nil。
func (c *gzipCache) get(name string) []byte {
	c.mu.Lock()
	b, ok := c.done[name]
	c.mu.Unlock()
	if ok {
		return b
	}
	raw, err := fs.ReadFile(c.files, name)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	if zw.Close() == nil && buf.Len() < len(raw)*9/10 {
		b = buf.Bytes()
	}
	c.mu.Lock()
	c.done[name] = b
	c.mu.Unlock()
	return b
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") && strings.ReplaceAll(q, " ", "") != "q=0" {
			return true
		}
	}
	return false
}

// serve 在用戶端接受 gzip、檔案值得壓縮時送壓縮版，回傳 true；否則回 false 交給一般的檔案伺服器。
func (c *gzipCache) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	ext := path.Ext(name)
	if !compressible[ext] || r.Header.Get("Range") != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		return false
	}
	b := c.get(name)
	if b == nil {
		return false
	}
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Content-Encoding", "gzip")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	return true
}
