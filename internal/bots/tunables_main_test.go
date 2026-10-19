package bots

import (
	"os"
	"testing"

	"xbot/internal/limits"
)

// 這個套件的測試照 LazyBoy 原本的數字驗證機制（例：往來 6 次就停、每天建 3 個）；正式預設比較寬（limits.DefaultTunables）。
func TestMain(m *testing.M) {
	limits.SetTunables(limits.LazyBoyTunables)
	os.Exit(m.Run())
}
