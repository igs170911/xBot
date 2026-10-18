package acp

import "testing"

// SEC-2：任何權限等級都不能對應到「完全不檢查」的模式。
func TestNoModeBypassesReview(t *testing.T) {
	forbidden := map[string]bool{"bypassPermissions": true, "agent-full-access": true}
	for name, a := range Agents {
		for level, mode := range a.Modes {
			if forbidden[mode] {
				t.Fatalf("%s 的 %s 對應到 %s，違反 SEC-2", name, level, mode)
			}
		}
	}
}
