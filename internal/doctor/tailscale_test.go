package doctor

import (
	"strings"
	"testing"
)

func TestEmbeddedTailscale(t *testing.T) {
	env := fakeEnv(nil, nil, nil)
	o := Options{Mode: ModeRun, DataDir: "/d", Addr: "127.0.0.1:8780"}
	body := `{"op":"xbot.network.status","output":{"tailscale":{"enabled":false}}}`
	env.Healthy = func(string) bool { return true }
	env.NetworkStatus = func(string) ([]byte, error) { return []byte(body), nil }
	if r := find(Check(env, o), "內建 Tailscale"); r.Name != "" {
		t.Fatalf("沒開不顯示：%+v", r)
	}
	body = `{"output":{"tailscale":{"enabled":true,"state":"running","url":"https://xbot.t.ts.net","cert":"ready","devices":2},"funnel":{"enabled":true,"state":"on"}}}`
	if r := find(Check(env, o), "內建 Tailscale"); r.Status != OK || !strings.Contains(r.Detail, "https://xbot.t.ts.net") || !strings.Contains(r.Detail, "Funnel") {
		t.Fatalf("連上：%+v", r)
	}
	body = `{"output":{"tailscale":{"enabled":true,"state":"needs_login"}}}`
	if r := find(Check(env, o), "內建 Tailscale"); r.Status != Warn || !strings.Contains(r.Fix, "登入") {
		t.Fatalf("要登入：%+v", r)
	}
	body = `{"output":{"tailscale":{"enabled":true,"state":"running","url":"https://x","cert":"missing","cert_message":"開 MagicDNS"}}}`
	if r := find(Check(env, o), "內建 Tailscale"); r.Status != Warn || r.Fix != "開 MagicDNS" {
		t.Fatalf("憑證沒好：%+v", r)
	}
	// 服務沒在跑、但有 tsnet 狀態檔。
	env.Healthy = func(string) bool { return false }
	if r := find(Check(env, o), "內建 Tailscale"); r.Status != Warn {
		t.Fatalf("服務沒在跑：%+v", r)
	}
}
