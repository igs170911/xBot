//go:build unix

package guardlock

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// 共用鎖可以同時拿；有人拿著共用鎖時，獨占鎖要等到放開；拿著獨占鎖時，共用鎖也要等。
func TestSharedAndExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "lock")
	a, err := acquire(p, syscall.LOCK_SH, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := acquire(p, syscall.LOCK_SH, time.Second, nil)
	if err != nil {
		t.Fatalf("兩個共用鎖要能同時拿到：%v", err)
	}
	waited := false
	if _, err := acquire(p, syscall.LOCK_EX, 300*time.Millisecond, func() { waited = true }); err == nil || !waited {
		t.Fatalf("共用鎖還在，獨占鎖不能拿到：%v %v", err, waited)
	}
	a()
	go func() { time.Sleep(200 * time.Millisecond); b() }()
	ex, err := acquire(p, syscall.LOCK_EX, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("共用鎖都放開後要拿到獨占鎖：%v", err)
	}
	if _, err := acquire(p, syscall.LOCK_SH, 300*time.Millisecond, nil); err == nil {
		t.Fatal("獨占鎖拿著時，共用鎖要等")
	}
	ex()
	sh, err := acquire(p, syscall.LOCK_SH, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	sh()
}

func TestPathOverride(t *testing.T) {
	t.Setenv("XBOT_NETGUARD_TEST_LOCK", "/x/y.lock")
	if Path() != "/x/y.lock" {
		t.Fatal(Path())
	}
	t.Setenv("XBOT_NETGUARD_TEST_LOCK", "")
	if filepath.Base(Path()) != "netguard-test.lock" {
		t.Fatal(Path())
	}
}
