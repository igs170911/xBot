package api

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("狀態碼 %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("回應 %v", body)
	}
}

func TestNoWebShowsHint(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "XBot 服務正在執行") {
		t.Fatalf("沒有前端時應顯示提示：%s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    {Data: []byte("<div id=root></div>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := New(Options{Web: files})

	for _, p := range []string{"/", "/bots/abc"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if !strings.Contains(rec.Body.String(), "root") {
			t.Fatalf("%s 應回 index.html，實際 %q", p, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/app.js", nil))
	if !strings.Contains(rec.Body.String(), "console.log") || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("靜態檔回應錯誤：%q %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}

	// v1.0 效能：接受 gzip 的瀏覽器拿到壓縮版（打包檔夠大才壓）
	big := strings.Repeat("console.log('xbot');\n", 500)
	files["assets/big.js"] = &fstest.MapFile{Data: []byte(big)}
	req := httptest.NewRequest("GET", "/assets/big.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") ||
		!strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") || rec.Body.Len() >= len(big)/4 {
		t.Fatalf("應回 gzip：%v，%d bytes", rec.Header(), rec.Body.Len())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); string(plain) != big {
		t.Fatal("解壓後內容不對")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/big.js", nil))
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != big {
		t.Fatalf("不接受 gzip 時照原樣送：%v", rec.Header())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知的 API 應回 404，實際 %d", rec.Code)
	}
}

func TestModelProxyMount(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest("POST", "/model/openai/v1/chat/completions", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "proxy_disabled") {
		t.Fatalf("沒有代理時應回 503，實際 %d %s", rec.Code, rec.Body.String())
	}

	var got string
	h := New(Options{ModelProxy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(http.StatusTeapot)
	})})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/model/openai/v1/chat/completions", nil))
	if rec.Code != http.StatusTeapot || got != "/model/openai/v1/chat/completions" {
		t.Fatalf("代理沒有掛上：%d %q", rec.Code, got)
	}
}

func TestMountsMCPAndAgentPair(t *testing.T) {
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) })
	}
	h := New(Options{MCP: mark("mcp"), AgentPair: mark("pair"), Hooks: mark("hooks")})
	for path, want := range map[string]string{"/mcp": "mcp", "/api/v1/agents.pair": "pair", "/hooks/h123": "hooks"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Body.String() != want {
			t.Errorf("%s → %q", path, rec.Body.String())
		}
	}
}
