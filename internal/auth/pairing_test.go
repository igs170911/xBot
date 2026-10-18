package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time           { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newPairing(t *testing.T) (*Pairing, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	p := NewPairing(openDB(t))
	p.Now = c.now
	return p, c
}

func TestHashAndNewToken(t *testing.T) {
	raw, hash := NewAgentToken()
	if !strings.HasPrefix(raw, AgentTokenPrefix) || len(raw) < 40 {
		t.Fatalf("權杖格式不對：%q", raw)
	}
	if HashToken(raw) != hash || len(hash) != 64 {
		t.Fatalf("雜湊不一致")
	}
	raw2, _ := NewAgentToken()
	if raw == raw2 {
		t.Fatal("兩把權杖一樣")
	}
}

func TestPairCodeSingleUse(t *testing.T) {
	p, _ := newPairing(t)
	ctx := context.Background()
	code, _, err := p.Start(PairGrant{Name: "Claude Code", Scopes: []ops.Scope{ops.ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	if len(NormalizePairCode(code)) != limits.PairCodeLen {
		t.Fatalf("配對碼長度不對：%q", code)
	}
	// 小寫、不帶分隔線也可以。
	res, e := p.Complete(ctx, strings.ToLower(NormalizePairCode(code)), "ignored", "1.2.3.4")
	if e != nil {
		t.Fatal(e)
	}
	if res.Agent.Name != "Claude Code" || len(res.Agent.Scopes) != 1 {
		t.Fatalf("結果不對：%+v", res.Agent)
	}
	// 資料庫只有雜湊。
	tok, err := p.DB.FindAgentToken(ctx, HashToken(res.Token))
	if err != nil {
		t.Fatal(err)
	}
	if tok.TokenHash == res.Token || tok.ID != res.Agent.ID {
		t.Fatal("資料庫存的不是雜湊")
	}
	if _, e := p.Complete(ctx, code, "", "1.2.3.4"); e == nil || e.Code != ops.CodeForbidden {
		t.Fatalf("第二次用同一個配對碼應該失敗：%v", e)
	}
}

func TestPairCodeExpires(t *testing.T) {
	p, c := newPairing(t)
	code, exp, _ := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}})
	if exp.Sub(c.now()) != limits.PairCodeTTL {
		t.Fatalf("有效時間不對：%v", exp.Sub(c.now()))
	}
	c.advance(limits.PairCodeTTL)
	if _, e := p.Complete(context.Background(), code, "", "1.2.3.4"); e == nil || e.Code != ops.CodeForbidden {
		t.Fatalf("過期的配對碼應該失敗：%v", e)
	}
}

func TestPairNameFallbackAndTokenExpiry(t *testing.T) {
	p, c := newPairing(t)
	code, _, _ := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}, TokenDays: 1})
	res, e := p.Complete(context.Background(), code, "Codex", "x")
	if e != nil {
		t.Fatal(e)
	}
	if res.Agent.Name != "Codex" {
		t.Fatalf("名字應該用代理報的：%q", res.Agent.Name)
	}
	if want := c.now().Add(24 * time.Hour).UnixMilli(); res.Agent.ExpiresAt != want {
		t.Fatalf("權杖到期時間不對：%d", res.Agent.ExpiresAt)
	}
}

func TestPairRateLimit(t *testing.T) {
	p, c := newPairing(t)
	ctx := context.Background()
	code, _, _ := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}})
	for i := 0; i < limits.PairMaxFailures; i++ {
		if _, e := p.Complete(ctx, "WRONG-CODE", "", "9.9.9.9"); e == nil || e.Code != ops.CodeForbidden {
			t.Fatalf("第 %d 次：%v", i, e)
		}
	}
	// 鎖住了：就算配對碼正確也不行。
	if _, e := p.Complete(ctx, code, "", "9.9.9.9"); e == nil || e.Code != ops.CodeLimit || !e.Retryable {
		t.Fatalf("應該被鎖住：%v", e)
	}
	// 其他來源不受影響。
	other, _, _ := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}})
	if _, e := p.Complete(ctx, other, "", "8.8.8.8"); e != nil {
		t.Fatalf("其他來源應該可以：%v", e)
	}
	c.advance(limits.PairLockout + time.Second)
	if _, e := p.Complete(ctx, code, "", "9.9.9.9"); e != nil {
		t.Fatalf("鎖定時間過了應該可以：%v", e)
	}
}

func TestPairGlobalRateLimit(t *testing.T) {
	p, _ := newPairing(t)
	ctx := context.Background()
	for i := 0; i < limits.PairGlobalMaxFailures; i++ {
		_, _ = p.Complete(ctx, "NOPE", "", "src-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	code, _, _ := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}})
	if _, e := p.Complete(ctx, code, "", "fresh"); e == nil || e.Code != ops.CodeLimit {
		t.Fatalf("分散猜碼也要鎖住：%v", e)
	}
}

func TestPairMaxPending(t *testing.T) {
	p, c := newPairing(t)
	for i := 0; i < limits.PairMaxPending; i++ {
		if _, _, err := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}})
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeLimit {
		t.Fatalf("超過上限應該回 limit：%v", err)
	}
	c.advance(limits.PairCodeTTL)
	if _, _, err := p.Start(PairGrant{Scopes: []ops.Scope{ops.ScopeRead}}); err != nil {
		t.Fatalf("舊的過期後應該可以再產生：%v", err)
	}
}

func TestPairHandler(t *testing.T) {
	p, _ := newPairing(t)
	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	code, _, _ := p.Start(PairGrant{Name: "Claude Code", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}})

	post := func(body string) (*http.Response, map[string]any) {
		resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	resp, m := post(`{"code":"` + code + `"}`)
	if resp.StatusCode != 200 || !strings.HasPrefix(m["token"].(string), AgentTokenPrefix) {
		t.Fatalf("換權杖失敗：%d %v", resp.StatusCode, m)
	}
	resp, m = post(`{"code":"` + code + `"}`)
	if resp.StatusCode != http.StatusForbidden || m["error"].(map[string]any)["code"] != "forbidden" {
		t.Fatalf("第二次應該 403：%d %v", resp.StatusCode, m)
	}
	resp, _ = post(`{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("沒有配對碼應該 400：%d", resp.StatusCode)
	}
	r, _ := http.Get(srv.URL)
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 應該 405：%d", r.StatusCode)
	}
	_ = r.Body.Close()
}

func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		remote, host string
		want         bool
	}{
		{"127.0.0.1:5000", "127.0.0.1:8780", true},
		{"[::1]:5000", "localhost:8780", true},
		{"127.0.0.1:5000", "xbot.tail.ts.net", false}, // Tailscale serve／反向代理
		{"100.64.0.2:5000", "127.0.0.1:8780", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if got := IsLoopbackRequest(r); got != c.want {
			t.Errorf("%s %s：%v", c.remote, c.host, got)
		}
	}
}
