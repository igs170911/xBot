package network

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRealTailscale 真的用 tsnet 連上 tailnet（手動測試）。沒設 XBOT_TEST_TS_AUTHKEY 就跳過；
// 金鑰建議用 ephemeral、tag 過的 auth key，測完節點會自己消失。
//
//	XBOT_TEST_TS_AUTHKEY=tskey-auth-… go test ./internal/network -run TestRealTailscale -v
func TestRealTailscale(t *testing.T) {
	key := os.Getenv("XBOT_TEST_TS_AUTHKEY")
	if key == "" {
		t.Skip("沒設 XBOT_TEST_TS_AUTHKEY，跳過真的 Tailscale 測試")
	}
	n := NewTSNode(NodeConfig{Dir: t.TempDir(), Hostname: "xbot-test", AuthKey: key})
	defer n.Close()
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var st NodeStatus
	for {
		var err error
		st, err = n.Status(ctx)
		if err == nil && st.BackendState == "Running" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("連不上 tailnet：%+v %v", st, err)
		case <-time.After(time.Second):
		}
	}
	t.Logf("已連上：%s（%v），HTTPS=%v，Funnel=%v，裝置 %d", st.DNSName, st.IPs, len(st.CertDomains) > 0, st.Funnel, st.Devices)
	if st.DNSName == "" || len(st.IPs) == 0 {
		t.Fatalf("狀態不完整：%+v", st)
	}
	ln, err := n.ListenTLS(":443")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
}
