package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

// C35：tailnet／外部入口來的請求永遠不算本機；外部入口的代理呼叫有另一個、比較嚴的速率計數。

func TestRemoteNeverLocal(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/api/v1/ops", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	if !IsLocal(r) || !IsLoopbackRequest(r) {
		t.Fatal("一般本機請求要算本機")
	}
	for name, ctx := range map[string]context.Context{
		"tailnet":  WithTailnet(r.Context(), "me@example.com"),
		"external": WithExternal(r.Context()),
	} {
		rr := r.WithContext(ctx)
		if IsLocal(rr) || IsLoopbackRequest(rr) {
			t.Fatalf("%s 來的請求不能算本機", name)
		}
	}
	tr := r.WithContext(WithTailnet(r.Context(), "me@example.com"))
	if !IsTailnet(tr.Context()) || TailnetUser(tr.Context()) != "me@example.com" || IsExternal(tr.Context()) {
		t.Fatal("tailnet 標記")
	}
	a := NewHTTP(HTTPConfig{AccessToken: "secret", LocalNoAuth: true})
	if _, err := a.Identify(tr); err != ErrUnauthenticated {
		t.Fatalf("tailnet 來的沒登入要被擋：%v", err)
	}
}

func TestLimiterExternalBucket(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	now := time.Now()
	l := &Limiter{DB: p.DB, Now: func() time.Time { return now }}
	reg.SetLimiter(l.Check)
	pr := pairWith(t, reg, p, map[string]any{})
	ext, _ := a.Identify(ctx, pr.Token, ViaExternal)
	in, _ := a.Identify(ctx, pr.Token, "mcp")
	for i := range limits.ExternalAgentRatePerMin {
		if _, err := reg.Invoke(ctx, ext, "xbot.system.ping", nil, ops.Options{}); err != nil {
			t.Fatalf("外部第 %d 次：%v", i+1, err)
		}
	}
	_, err := reg.Invoke(ctx, ext, "xbot.system.ping", nil, ops.Options{})
	oe := ops.AsError(err)
	if oe == nil || oe.Code != ops.CodeLimit || !strings.Contains(oe.Message, "經外部入口") {
		t.Fatalf("外部入口超過 %d 次要被擋：%v", limits.ExternalAgentRatePerMin, err)
	}
	// tailnet 內是另一個計數，不受影響。
	if _, err := reg.Invoke(ctx, in, "xbot.system.ping", nil, ops.Options{}); err != nil {
		t.Fatalf("tailnet 內不受外部計數影響：%v", err)
	}
	now = now.Add(61 * time.Second)
	if _, err := reg.Invoke(ctx, ext, "xbot.system.ping", nil, ops.Options{}); err != nil {
		t.Fatalf("一分鐘後恢復：%v", err)
	}
}
