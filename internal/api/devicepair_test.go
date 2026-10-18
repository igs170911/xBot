package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"xbot/internal/auth"
	"xbot/internal/store"
)

// W31：手機掃 QR → 打開 /pair#<碼> → POST /api/v1/auth/pair 換 session cookie。
func TestDevicePairLogin(t *testing.T) {
	e := newEnv(t)
	dp := auth.NewDevicePairing(e.db, "")
	auth.RegisterDeviceOps(e.reg, dp)
	e.h = New(Options{Registry: e.reg, Bus: e.bus, Auth: e.auth, Heartbeat: 50 * time.Millisecond, DB: e.db, DevicePair: dp})

	// 沒登入的手機不能產生配對碼。
	if rec := e.do(req{path: "/api/v1/devices.pair_start", body: `{"origin":"https://xbot.example.ts.net"}`}); rec.Code != 401 {
		t.Fatalf("沒登入產生配對碼：%d", rec.Code)
	}
	// 電腦（本機）產生配對碼。
	out := outOf[auth.DevicePairStartOut](t, e.do(req{path: "/api/v1/devices.pair_start", body: `{"origin":"https://xbot.example.ts.net"}`, local: true}))
	if out.Code == "" || !strings.HasPrefix(out.URL, "https://xbot.example.ts.net/pair#") || out.LocalOnly || out.QRSize < 21 || out.QRPath == "" {
		t.Fatalf("pair_start：%+v", out)
	}
	status := func() string {
		return outOf[auth.DevicePairStatusOut](t, e.do(req{path: "/api/v1/devices.pair_status", body: `{"code":"` + out.Code + `"}`, local: true})).Status
	}
	if s := status(); s != auth.DevicePending {
		t.Fatalf("狀態 %s", s)
	}

	// 跨站送來的請求擋掉。
	if rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"` + out.Code + `"}`, header: map[string]string{"Origin": "https://evil.example"}}); rec.Code != 403 {
		t.Fatalf("跨站：%d", rec.Code)
	}
	// 錯的碼：403。
	if rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"AAAA-AAAA"}`}); rec.Code != 403 || !strings.Contains(rec.Body.String(), "配對碼不對") {
		t.Fatalf("錯的碼：%d %s", rec.Code, rec.Body.String())
	}
	// 對的碼（小寫、沒有分隔線也可以）：發 cookie。
	code := strings.ToLower(strings.ReplaceAll(out.Code, "-", ""))
	rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"` + code + `"}`})
	if rec.Code != 200 {
		t.Fatalf("換登入：%d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("沒有登入 cookie：%+v", rec.Result().Cookies())
	}
	st := decode[map[string]any](t, e.do(req{method: "GET", path: "/api/v1/auth/status", cookie: cookie}))
	if st["authenticated"] != true || st["kind"] != "owner" {
		t.Fatalf("登入狀態：%v", st)
	}
	if s := status(); s != auth.DevicePaired {
		t.Fatalf("用過之後狀態 %s", s)
	}
	// 只能用一次。
	if rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"` + out.Code + `"}`, remote: "192.0.2.99:1"}); rec.Code != 403 {
		t.Fatalf("第二次：%d", rec.Code)
	}
	// 稽核紀錄。
	entries, err := e.db.ListAudit(context.Background(), store.AuditFilter{})
	n := 0
	for _, a := range entries {
		if a.Op == "xbot.devices.pair_complete" {
			n++
		}
	}
	if err != nil || n != 1 {
		t.Fatalf("稽核 %d %v", n, err)
	}
	// 格式不對的 origin。
	if rec := e.do(req{path: "/api/v1/devices.pair_start", body: `{"origin":"javascript:alert(1)"}`, local: true}); rec.Code != 400 {
		t.Fatalf("壞的 origin：%d", rec.Code)
	}
	// localhost：標成手機連不到。
	loc := outOf[auth.DevicePairStartOut](t, e.do(req{path: "/api/v1/devices.pair_start", body: `{"origin":"http://localhost:8780"}`, local: true}))
	if !loc.LocalOnly {
		t.Fatalf("localhost 應標 local_only：%+v", loc)
	}
}

func TestDevicePairLockout(t *testing.T) {
	e := newEnv(t)
	dp := auth.NewDevicePairing(e.db, "")
	e.h = New(Options{Registry: e.reg, Bus: e.bus, Auth: e.auth, Heartbeat: 50 * time.Millisecond, DB: e.db, DevicePair: dp})
	code, _, err := dp.Start()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		e.do(req{path: "/api/v1/auth/pair", body: `{"code":"ZZZZ-ZZZZ"}`})
	}
	rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"` + code + `"}`})
	if rec.Code != 429 {
		t.Fatalf("鎖住時連對的碼也不行：%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Retryable bool `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Error.Retryable {
		t.Fatalf("應可重試：%s", rec.Body.String())
	}
	// 別的來源不受影響。
	if rec := e.do(req{path: "/api/v1/auth/pair", body: `{"code":"` + code + `"}`, remote: "198.51.100.7:1"}); rec.Code != 200 {
		t.Fatalf("別的來源：%d %s", rec.Code, rec.Body.String())
	}
}

func TestPWAFilesAndShare(t *testing.T) {
	files := fstest.MapFS{
		"index.html":           {Data: []byte("<div id=root></div>")},
		"sw.js":                {Data: []byte("self.addEventListener('fetch',()=>{})")},
		"manifest.webmanifest": {Data: []byte(`{"name":"XBot"}`)},
	}
	h := New(Options{Web: files})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest：%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sw.js", nil))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("Service-Worker-Allowed") != "/" ||
		!strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("sw.js：%d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/share", strings.NewReader("text=hi")))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/share?nosw=1" {
		t.Fatalf("POST /share：%d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/share?nosw=1", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("GET /share 應回 index.html：%d", rec.Code)
	}
}

func outOf[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	r := decode[result](t, rec)
	var v T
	if err := json.Unmarshal(r.Output, &v); err != nil {
		t.Fatalf("輸出不是 JSON（%d）：%s", rec.Code, rec.Body.String())
	}
	return v
}
