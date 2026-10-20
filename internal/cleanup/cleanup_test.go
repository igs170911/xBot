package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/limits"
)

// 測試照 LazyBoy tests/test_cleanup.py 與 tests/test_security_review.py 的清理測試翻寫。
// 在電腦裡執行的小程式直接在這台機器上跑（暫存資料夾當成三個 volume）；要 GNU find 的 -printf、-newermt 與 head -z。

const day = 24 * time.Hour

func needTools(t *testing.T) {
	t.Helper()
	if err := LocalSupported(); err != nil {
		t.Skip(err)
	}
}

// computer 是一台假的電腦：root 底下的 hermes、home、workspace 就是三個 volume。
func computer(t *testing.T) (box.Shell, string) {
	t.Helper()
	needTools(t)
	root := t.TempDir()
	s := Local(root)
	for _, d := range []string{s.Hermes, s.Home, s.Workspace} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s, root
}

// write 寫一個 size 位元組的檔案，修改時間是 age 以前。大檔用稀疏檔（只有大小，不佔空間）。
func write(t *testing.T, p string, size int64, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if size <= 4096 {
		_, err = f.Write(bytes.Repeat([]byte("x"), int(size)))
	} else {
		err = f.Truncate(size)
	}
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

const bigCache = limits.CleanupBrowserCacheBytes + 1<<20

func TestCleanupKeepsWorkAndLoginsAndDropsRegenerableFiles(t *testing.T) {
	ctx := context.Background()
	s, _ := computer(t)
	j := filepath.Join
	write(t, j(s.Workspace, "report.md"), 50, 400*day)
	write(t, j(s.Hermes, "cache/scratch/old/a.bin"), 300, 10*day)
	write(t, j(s.Hermes, "cache/scratch/new.bin"), 70, day)
	write(t, j(s.Home, "chrome-profile/Default/Cache/data_1"), bigCache, 0)
	write(t, j(s.Home, "chrome-profile/Default/Cookies"), 20, 0)
	write(t, j(s.Workspace, "uploads/old1/IMG.jpeg"), 500, 40*day)
	write(t, j(s.Workspace, "uploads/new1/doc.txt"), 30, 2*day)
	// XBot 的附件直接放在 uploads 底下；/workspace/tmp 是 Bot 的中間檔（XBot 新增）。
	write(t, j(s.Workspace, "uploads/10月訂單.csv"), 40, 31*day)
	write(t, j(s.Workspace, "uploads/貼上的圖片.png"), 60, 29*day)
	write(t, j(s.Workspace, "tmp/raw/page.html"), 80, 4*day)
	write(t, j(s.Workspace, "tmp/data.json"), 90, 2*day)
	if err := os.MkdirAll(j(s.Workspace, "tmp/just-made"), 0o755); err != nil {
		t.Fatal(err)
	}

	before, err := Measure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Workspace: 50, Uploads: 630, Caches: 300 + 70 + 80 + 90 + bigCache, Browser: 20}
	want.Total = want.Workspace + want.Uploads + want.Caches + want.Browser
	if before != want {
		t.Fatalf("用量：%+v，想要 %+v", before, want)
	}

	res, err := Clean(ctx, s, time.Now(), Options{Uploads: true})
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Scratch: 300 + 80, Uploads: 500 + 40, Browser: bigCache, Freed: 300 + 80 + 500 + 40 + bigCache}) {
		t.Fatalf("清理結果：%+v", res)
	}
	for _, p := range []string{"workspace/report.md", "home/chrome-profile/Default/Cookies", "hermes/cache/scratch/new.bin",
		"workspace/uploads/new1/doc.txt", "workspace/uploads/貼上的圖片.png", "workspace/tmp/data.json", "workspace/tmp/just-made"} {
		if !exists(j(filepath.Dir(s.Hermes), p)) {
			t.Errorf("%s 不該被刪", p)
		}
	}
	for _, p := range []string{"hermes/cache/scratch/old", "home/chrome-profile/Default/Cache", "workspace/uploads/old1",
		"workspace/uploads/10月訂單.csv", "workspace/tmp/raw"} {
		if exists(j(filepath.Dir(s.Hermes), p)) {
			t.Errorf("%s 應該被刪", p)
		}
	}
	after, err := Measure(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if after.Workspace != 50 || after.Uploads != 90 || after.Caches != 70+90 || after.Browser != 20 {
		t.Fatalf("清理後的用量：%+v", after)
	}
}

// 你按的「清理」只清快取與暫存，附件等每天的例行清理。
func TestManualCleanKeepsUploads(t *testing.T) {
	ctx := context.Background()
	s, _ := computer(t)
	old := filepath.Join(s.Workspace, "uploads/舊報價.pdf")
	write(t, old, 40, 90*day)
	write(t, filepath.Join(s.Hermes, "cache/scratch/a.bin"), 10, 5*day)
	res, err := Clean(ctx, s, time.Now(), Options{})
	if err != nil || res != (Result{Scratch: 10, Freed: 10}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !exists(old) {
		t.Fatal("附件不該被刪")
	}
}

func TestBrowserCacheUnderTheLimitIsLeftAlone(t *testing.T) {
	s, _ := computer(t)
	cache := filepath.Join(s.Home, "chrome-profile/Default/Cache/data_1")
	write(t, cache, 400, 0)
	n, err := ClearBrowserCache(context.Background(), s)
	if err != nil || n != 0 || !exists(cache) {
		t.Fatalf("%d %v", n, err)
	}
}

func TestRunningBrowserIsAskedToClearItsOwnCache(t *testing.T) {
	old := livePoll
	livePoll = 10 * time.Millisecond
	t.Cleanup(func() { livePoll = old })
	s, _ := computer(t)
	cache := filepath.Join(s.Home, "chrome-profile/Default/Cache/data_1")
	write(t, cache, bigCache, 0)
	var mu sync.Mutex
	var calls [][]string
	s.Live = true
	s.Run = func(ctx context.Context, max int64, cmd ...string) ([]byte, error) {
		if cmd[0] == "node" {
			mu.Lock()
			calls = append(calls, cmd)
			mu.Unlock()
			go func() { // 瀏覽器回覆以後才在背景清
				time.Sleep(30 * time.Millisecond)
				os.Remove(cache)
			}()
			return nil, nil
		}
		return runLocal(ctx, max, cmd...)
	}
	n, err := ClearBrowserCache(context.Background(), s)
	if err != nil || n != bigCache {
		t.Fatalf("%d %v", n, err)
	}
	if len(calls) != 1 || !slices.Equal(calls[0][:3], []string{"node", "--input-type=module", "-e"}) ||
		!strings.Contains(calls[0][3], "Network.clearBrowserCache") || !strings.Contains(calls[0][3], "/opt/xbot/playwright/") {
		t.Fatalf("%q", calls)
	}

	// 開著的瀏覽器沒回應：和 LazyBoy 一樣不硬刪它正在用的檔案，等下一次。
	write(t, cache, bigCache, 0)
	s.Run = func(ctx context.Context, max int64, cmd ...string) ([]byte, error) {
		if cmd[0] == "node" {
			return nil, &boxfs.ExitError{Cmd: "node", Code: 1}
		}
		return runLocal(ctx, max, cmd...)
	}
	if n, err := ClearBrowserCache(context.Background(), s); err != nil || n != 0 || !exists(cache) {
		t.Fatalf("%d %v", n, err)
	}
}

// 舊版的設定檔在 .config/chromium，chrome-profile 是連到它的連結。
func TestOldBrowserProfileLocation(t *testing.T) {
	s, _ := computer(t)
	write(t, filepath.Join(s.Home, ".config/chromium/Default/GPUCache/data_0"), bigCache, 0)
	write(t, filepath.Join(s.Home, ".config/chromium/Default/Cookies"), 20, 0)
	if err := os.Symlink("/home/box/.config/chromium", filepath.Join(s.Home, "chrome-profile")); err != nil {
		t.Fatal(err)
	}
	u, err := Measure(context.Background(), s)
	if err != nil || u.Caches != bigCache || u.Browser != 20 {
		t.Fatalf("%+v %v", u, err)
	}
	if n, err := ClearBrowserCache(context.Background(), s); err != nil || n != bigCache {
		t.Fatalf("%d %v", n, err)
	}
	if !exists(filepath.Join(s.Home, ".config/chromium/Default/Cookies")) {
		t.Fatal("登入不該被刪")
	}
}

// 出自 test_security_review.py：剛建好的空資料夾（例如正在用的）留著，這次刪空的資料夾刪掉，空了很久的也刪。
// 和 LazyBoy 一樣，只有直接裝著舊檔案的那一層算「這次刪空的」；更上層的空資料夾等它也放舊了再刪。
func TestScratchCleanupLeavesAFolderJustMade(t *testing.T) {
	s, _ := computer(t)
	scratch := filepath.Join(s.Hermes, "cache/scratch")
	write(t, filepath.Join(scratch, "old/a.pdf"), 10, 400*day)
	write(t, filepath.Join(scratch, "lane/deep/b.pdf"), 5, 400*day)
	for _, d := range []string{"inflight", "stale"} {
		if err := os.MkdirAll(filepath.Join(scratch, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-10 * day)
	if err := os.Chtimes(filepath.Join(scratch, "stale"), long, long); err != nil {
		t.Fatal(err)
	}
	freed, err := removeOld(context.Background(), s, s.Hermes, scratch, time.Now().Add(-time.Hour))
	if err != nil || freed != 15 {
		t.Fatalf("%d %v", freed, err)
	}
	for name, want := range map[string]bool{"old": false, "stale": false, "lane/deep": false, "lane": true, "inflight": true, ".": true} {
		if got := exists(filepath.Join(scratch, name)); got != want {
			t.Errorf("%s 在不在：%v，想要 %v", name, got, want)
		}
	}
}

// Bot 在暫存、附件、瀏覽器設定檔的路徑上放連結：不跟著走，連到的工作成果不會被刪。
func TestLinksAreNotFollowed(t *testing.T) {
	ctx := context.Background()
	s, _ := computer(t)
	precious := filepath.Join(s.Workspace, "成果")
	write(t, filepath.Join(precious, "cache/scratch/報告.docx"), 100, 400*day)
	write(t, filepath.Join(precious, "Cache/data_1"), bigCache, 0)
	write(t, filepath.Join(precious, "舊合約.pdf"), 100, 400*day)
	// /hermes/cache 是連到成果的連結
	if err := os.Symlink(precious, filepath.Join(s.Hermes, "cache")); err != nil {
		t.Fatal(err)
	}
	// 瀏覽器設定檔的 Default 是連結
	if err := os.MkdirAll(filepath.Join(s.Home, "chrome-profile"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, filepath.Join(s.Home, "chrome-profile/Default")); err != nil {
		t.Fatal(err)
	}
	// /workspace/tmp 是連結；附件裡有一個連到成果的舊連結
	if err := os.Symlink(precious, filepath.Join(s.Workspace, "tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Workspace, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Workspace, "uploads/捷徑")
	if err := os.Symlink(precious, link); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-400 * day)
	if err := exec.Command("touch", "-h", "-d", "@"+fmt.Sprint(long.Unix()), link).Run(); err != nil {
		t.Skipf("改不了連結的時間：%v", err)
	}
	u, err := Measure(ctx, s)
	if err != nil || u.Caches != 0 {
		t.Fatalf("連結不算在快取：%+v %v", u, err)
	}
	res, err := Clean(ctx, s, time.Now(), Options{Uploads: true})
	if err != nil || res.Scratch != 0 || res.Browser != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if exists(link) {
		t.Error("附件裡的舊連結本身應該被刪")
	}
	for _, p := range []string{"cache/scratch/報告.docx", "Cache/data_1", "舊合約.pdf"} {
		if !exists(filepath.Join(precious, p)) {
			t.Errorf("連到的 %s 不該被刪", p)
		}
	}
}

func TestExpiredUploadsSkipAnEntryCutOffByTheListLimit(t *testing.T) {
	now := time.Now()
	ts := func(age time.Duration) string { return fmt.Sprintf("%.3f", float64(now.Add(-age).UnixMilli())/1000) }
	rec := func(kind string, size int, age time.Duration, p string) string {
		return fmt.Sprintf("%s\t%d\t%s\t%s\x00", kind, size, ts(age), p)
	}
	out := rec("f", 10, 40*day, "a.pdf") +
		rec("d", 4096, time.Hour, "folder") + rec("f", 5, 40*day, "folder/x") + rec("f", 6, 35*day, "folder/y") +
		rec("d", 4096, 40*day, "empty") +
		rec("d", 4096, 40*day, "mixed") + rec("f", 1, 40*day, "mixed/old") + rec("f", 1, day, "mixed/new") +
		rec("d", 4096, 40*day, "cut") + rec("f", 7, 40*day, "cut/1")
	cutoff := now.Add(-limits.CleanupUploadAge)
	names := func(es []entry) (n []string, size int64) {
		for _, e := range es {
			n = append(n, e.name)
			size += e.size
		}
		return n, size
	}
	got, size := names(expired([]byte(out), 100, cutoff))
	if !slices.Equal(got, []string{"a.pdf", "folder", "empty", "cut"}) || size != 10+11+7 {
		t.Fatalf("%q %d", got, size)
	}
	// 清單在第 10 項截斷：最後一項（cut）也許沒列完，不算。
	got, _ = names(expired([]byte(out), 10, cutoff))
	if !slices.Equal(got, []string{"a.pdf", "folder", "empty"}) {
		t.Fatalf("%q", got)
	}
}

func TestNoComputerDataIsEmpty(t *testing.T) {
	s, root := computer(t)
	os.RemoveAll(root)
	u, err := Measure(context.Background(), s)
	if err != nil || u != (Usage{}) {
		t.Fatalf("%+v %v", u, err)
	}
	res, err := Clean(context.Background(), s, time.Now(), Options{Uploads: true})
	if err != nil || res != (Result{}) {
		t.Fatalf("%+v %v", res, err)
	}
}
