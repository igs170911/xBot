package judge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// OpenCode Zen 沒有儲值時（2026-10-18 實測）：Jev 回 server_error「Insufficient account funds」。
// 要分成 no_funds（不是一般錯誤）、照規則、J10 顯示「OpenCode Zen 餘額不足」；改用 Go flash 做選擇題時不帶 json_schema。
func TestZenNoFunds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.modes(t, map[string]Mode{UseRoute: ModeLive})
	set, _ := e.svc.Settings(ctx)
	set.Tier2 = TierModel{} // 只看第一層
	_ = e.svc.SaveSettings(ctx, set)
	e.up.set(func(upstreamReq) (int, string, time.Duration) {
		return 500, `{"type":"error","error":{"type":"server_error","message":"Upstream request failed: Insufficient account funds"}}`, 0
	})
	d, _ := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"})
	if d.Outcome != OutcomeNoFunds || d.By != ByRules || d.Label != "chat" {
		t.Fatalf("餘額不足退回規則：%+v", d)
	}
	out, err := e.svc.view(ctx)
	if err != nil || len(out.Problems) != 1 || out.Problems[0].Provider != "opencode-zen" || out.Problems[0].Message != "OpenCode Zen 餘額不足（Jev 需要 Zen 儲值）" {
		t.Fatalf("J10 要顯示：%+v %v", out.Problems, err)
	}

	// 一鍵改用 Go flash 做選擇題：第一層＝第二層的模型，不重複呼叫，也不帶 json_schema。
	set.Tier1 = goTier
	set.Tier2 = goTier
	_ = e.svc.SaveSettings(ctx, set)
	e.up.set(jsonReply("chat", 0.9, ""))
	n := len(e.up.calls())
	if d, _ := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"}); d.By != ByModel || d.ModelID != "deepseek-v4-flash" {
		t.Fatalf("Go flash：%+v", d)
	}
	c := e.up.calls()[n:]
	raw, _ := json.Marshal(c[0].Body)
	if len(c) != 1 || !strings.Contains(string(raw), `"json_object"`) || strings.Contains(string(raw), "json_schema") {
		t.Fatalf("請求：%d %s", len(c), raw)
	}
	e.up.set(func(upstreamReq) (int, string, time.Duration) { return 503, `{}`, 0 })
	n = len(e.up.calls())
	_, _ = e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨"})
	if len(e.up.calls())-n != 1 {
		t.Fatal("第一層和第二層是同一個模型時只問一次")
	}
}
