package hermes

import "testing"

// SEC-1／SEC-2：權限等級對到 Hermes 的 approvals.mode；任何等級都不會是 off（危險指令永不自動核准）。
func TestApprovalsModeSafetyFloor(t *testing.T) {
	for mode, want := range map[string]string{"": "manual", "ask": "manual", "edits": "manual", "auto": "smart", "full": "smart", "weird": "manual"} {
		if got := ApprovalsMode(mode); got != want {
			t.Errorf("%q → %q，想要 %q", mode, got, want)
		}
		cfg := Config(map[string]any{"approvals": map[string]any{"mode": "off", "timeout": 300}}, Profile{ApprovalMode: mode, ProviderID: "p", Model: "m"})
		ap := cfg["approvals"].(map[string]any)
		if ap["mode"] == "off" || ap["mode"] != want || ap["timeout"] != 300 {
			t.Errorf("%q：approvals = %v", mode, ap)
		}
	}
}
