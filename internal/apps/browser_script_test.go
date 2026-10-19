package apps

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 輸出是 pipe：process.stdout.write 之後馬上 process.exit，超過 64 KB 的部分會被丟掉，
// cookies 多的 Bot 分享登入一直失敗（2026-10-19，76 KB 只收到 65536 位元組）。每個 write 都要等寫完才結束。
func TestBrowserScriptsExitAfterWrite(t *testing.T) {
	src, err := os.ReadFile("browser.go")
	if err != nil {
		t.Fatal(err)
	}
	bare := regexp.MustCompile(`process\.stdout\.write\([^;]*\);\s*process\.exit`)
	if m := bare.FindString(string(src)); m != "" {
		t.Fatalf("write 之後直接 exit 會截斷輸出：%s", m)
	}
	if n := strings.Count(string(src), "process.stdout.write("); n < 2 || strings.Count(string(src), "() => process.exit(0))") < n {
		t.Fatalf("每個 process.stdout.write 都要在 callback 裡才 exit（%d 個 write）", n)
	}
	if !strings.Contains(exportJS, "() => process.exit(0))") {
		t.Fatal("exportJS 要寫完才結束")
	}
}
