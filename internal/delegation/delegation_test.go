package delegation

import (
	"testing"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

// 輸入標籤寫的數字要和 internal/limits 一致。
func TestTagsMatchLimits(t *testing.T) {
	reg := ops.New(ops.Config{})
	Register(reg)
	info, ok := reg.Get("xbot.agents.set_delegation")
	if !ok || !info.NoDelegate {
		t.Fatalf("set_delegation 要 NoDelegate：%+v", info)
	}
	p := info.Input.Properties
	for name, want := range map[string]float64{
		"daily_count":         limits.DelegationDailyCountMax,
		"daily_amount_micros": limits.DelegationDailyAmountMaxMicros,
		"days":                limits.DelegationMaxDays,
	} {
		if p[name] == nil || p[name].Maximum == nil || *p[name].Maximum != want {
			t.Errorf("%s 的 max 要是 %v", name, want)
		}
	}
	if items := p["categories"].Items; items == nil || len(items.Enum) != len(ops.Categories) {
		t.Errorf("categories 要列出所有類別：%+v", items)
	}
}

func TestUSD(t *testing.T) {
	if got := USD(1_500_000); got != "US$1.50" {
		t.Fatal(got)
	}
}
