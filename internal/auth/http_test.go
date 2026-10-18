package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
)

func TestSessionExpiresAndRotates(t *testing.T) {
	now := time.Now()
	a := NewHTTP(HTTPConfig{AccessToken: "tok", Now: func() time.Time { return now }})
	rec := httptest.NewRecorder()
	if err := a.Login(rec, httptest.NewRequest("POST", "/", nil), "tok"); err != nil {
		t.Fatal(err)
	}
	v := rec.Result().Cookies()[0].Value
	if !a.validSession(v) {
		t.Fatal("剛登入的 session 要有效")
	}
	now = now.Add(limits.SessionTTL + time.Second)
	if a.validSession(v) {
		t.Fatal("過期的 session 要無效")
	}
	now = now.Add(-limits.SessionTTL)
	if NewHTTP(HTTPConfig{AccessToken: "new-token"}).validSession(v) {
		t.Fatal("換登入碼後舊的 session 要失效")
	}
	if a.validSession("123.abc") || a.validSession("") {
		t.Fatal("格式不對的 session 要無效")
	}
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		remote, host string
		hdr          string
		want         bool
	}{
		{"127.0.0.1:1", "localhost:8780", "", true},
		{"[::1]:1", "[::1]:8780", "", true},
		{"127.0.0.1:1", "127.0.0.1", "", true},
		{"127.0.0.1:1", "evil.com", "", false},
		{"10.0.0.2:1", "localhost", "", false},
		{"127.0.0.1:1", "localhost", "Tailscale-User-Login", false},
		{"127.0.0.1:1", "localhost", "Forwarded", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if c.hdr != "" {
			r.Header.Set(c.hdr, "x")
		}
		if IsLocal(r) != c.want {
			t.Errorf("%+v：應該是 %v", c, c.want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Host = "localhost:8780"
	for origin, want := range map[string]bool{"": true, "http://localhost:8780": true, "http://localhost:5181": false, "null": false} {
		r.Header.Set("Origin", origin)
		if SameOrigin(r) != want {
			t.Errorf("Origin %q：應該是 %v", origin, want)
		}
	}
}

func TestFailuresWindow(t *testing.T) {
	now := time.Now()
	a := NewHTTP(HTTPConfig{AccessToken: "tok", Now: func() time.Time { return now }})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	for range limits.LoginFailures {
		if _, err := a.Identify(r); err != ErrBadToken {
			t.Fatalf("應該是 ErrBadToken：%v", err)
		}
	}
	if _, err := a.Identify(r); err == nil || !strings.Contains(err.Error(), "分鐘") {
		t.Fatalf("應該被限速：%v", err)
	}
	now = now.Add(limits.LoginWindow)
	r.Header.Set("Authorization", "Bearer tok")
	if id, err := a.Identify(r); err != nil || id.Via != "http" {
		t.Fatalf("時間窗過了要恢復：%v", err)
	}
	// 透過本機反向代理時，用代理標的來源計算。
	p := httptest.NewRequest("GET", "/", nil)
	p.RemoteAddr = "127.0.0.1:9"
	p.Header.Set("X-Forwarded-For", "1.2.3.4, 100.64.0.5")
	if ClientKey(p) != "100.64.0.5" {
		t.Fatal(ClientKey(p))
	}
}
