//go:build unix

// Package guardlock 讓同一台機器上的 Docker 整合測試（go test -tags docker）安全地共用網路防護（SEC-4）。
//
// 一台 Docker 主機只有一個 xbot-netguard 容器與一個 xbot-box 網路，規則檔也只有一份：誰最後套用，就是誰的規則。
// 正式環境一台主機只跑一個 XBot，沒有問題；但 make test-docker 會同時跑好幾個測試套件（各自一個資料庫、一個 Box Manager），
// 其他 Agent 或開發者也可能同時在跑。一般的電腦測試只需要「基本規則」（每個 XBot 算出來都一樣），可以同時跑；
// 會改白名單、並且驗證規則效果的測試（TestDockerNetguard）跑的時候，別的套件開電腦會用基本規則蓋掉它的白名單。
//
// 所以：開本機電腦的測試拿共用鎖（Shared），驗證網路防護規則的測試拿獨占鎖（Exclusive）。
// 鎖是使用者快取目錄裡的一個檔案（flock），跨套件、跨 worktree 都有效；行程結束（含當掉）時系統自動放開。
// 測試從來不刪 xbot-netguard 與 xbot-box（其他 XBot 與其他測試也在用；要整個移除時見 box/xbot-netguard 的 remove）。
package guardlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Wait 是最多等鎖多久（另一個套件的網路防護測試大約 1–2 分鐘）。
const Wait = 20 * time.Minute

// Path 回傳鎖檔的位置（XBOT_NETGUARD_TEST_LOCK 可以改，給這個套件自己的測試用）。
func Path() string {
	if p := os.Getenv("XBOT_NETGUARD_TEST_LOCK"); p != "" {
		return p
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "xbot", "netguard-test.lock")
}

// Shared 拿共用鎖：會在本機 Docker 開 Bot 電腦（套用基本的網路防護規則）的測試呼叫。測試結束時放開。
func Shared(t testing.TB) { lock(t, syscall.LOCK_SH) }

// Exclusive 拿獨占鎖：會改網路防護規則並驗證效果的測試呼叫。測試結束時放開。
func Exclusive(t testing.TB) { lock(t, syscall.LOCK_EX) }

func lock(t testing.TB, how int) {
	t.Helper()
	release, err := acquire(Path(), how, Wait, func() { t.Logf("等其他測試用完網路防護（%s）…", Path()) })
	if err != nil {
		t.Fatalf("網路防護測試鎖：%v", err)
	}
	// t.Cleanup 後進先出：要在開電腦（登記清理）之前拿鎖，電腦刪掉之後才放開。
	t.Cleanup(release)
}

// acquire 拿鎖（how 是 LOCK_SH 或 LOCK_EX），最多等 wait；要等時先呼叫一次 waiting。
func acquire(path string, how int, wait time.Duration, waiting func()) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for first := true; ; first = false {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("等了 %v 還拿不到（%s）", wait, path)
		}
		if first && waiting != nil {
			waiting()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
