package api

import (
	"encoding/json"
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
