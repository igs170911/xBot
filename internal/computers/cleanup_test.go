package computers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"xbot/internal/box"
	"xbot/internal/cleanup"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type fakeActivity struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (a *fakeActivity) HasWork(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busy[id]
}

func (a *fakeActivity) Quiesce(id string) bool { return !a.HasWork(id) }

func (a *fakeActivity) set(id string, busy bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.busy[id] = busy
}

// cleanFixture：每個 Bot 的電腦是一個暫存資料夾（cleanup.Local），沒有資料夾的 Bot 當作還沒有電腦資料。
type cleanFixture struct {
	moveFixture
	act    *fakeActivity
	roots  map[string]string
	writes []bool
}

func newCleanFixture(t *testing.T) *cleanFixture {
	t.Helper()
	if err := cleanup.LocalSupported(); err != nil {
		t.Skip(err)
	}
	f := &cleanFixture{moveFixture: newMoveFixture(t), act: &fakeActivity{busy: map[string]bool{}}, roots: map[string]string{}}
	f.svc.Activity = f.act
	f.svc.cl.shell = func(ctx context.Context, botID string, write bool, fn func(context.Context, box.Shell) error) error {
		root, ok := f.roots[botID]
		if !ok {
			return box.ErrNoData
		}
		f.writes = append(f.writes, write)
		return fn(ctx, cleanup.Local(root))
	}
	return f
}

// file 在 Bot 的電腦裡寫一個檔案（rel 從 volume 開始，例：workspace/uploads/a.pdf），修改時間是 age 以前。
func (f *cleanFixture) file(t *testing.T, botID, rel string, size int, age time.Duration) string {
	t.Helper()
	if f.roots[botID] == "" {
		f.roots[botID] = t.TempDir()
		for _, d := range []string{"hermes", "home", "workspace"} {
			if err := os.MkdirAll(filepath.Join(f.roots[botID], d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := filepath.Join(f.roots[botID], rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(p, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return p
}

func present(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

const day = 24 * time.Hour

// 看用量、按「清理」：只清快取與暫存，附件與工作成果都留著；Bot 在工作時不清；還沒有電腦時全部是 0。
func TestStorageAndManualClean(t *testing.T) {
	ctx := context.Background()
	f := newCleanFixture(t)
	a := f.bot(t, "阿凱", f.local.ID, true)
	fresh := f.bot(t, "小編", f.local.ID, false)
	work := f.file(t, a.ID, "workspace/報告.md", 50, 400*day)
	upload := f.file(t, a.ID, "workspace/uploads/舊報價.pdf", 40, 90*day)
	f.file(t, a.ID, "hermes/cache/scratch/lane/a.bin", 300, 5*day)
	f.file(t, a.ID, "workspace/tmp/page.html", 80, 4*day)

	v, err := f.svc.Storage(ctx, a.ID)
	if err != nil || !v.HasComputer || v.Usage != (cleanup.Usage{Workspace: 50, Uploads: 40, Caches: 380, Total: 470}) {
		t.Fatalf("用量：%+v %v", v, err)
	}
	c, err := f.svc.Clean(ctx, a.ID)
	if err != nil || !c.HasComputer || c.Result != (cleanup.Result{Scratch: 380, Freed: 380}) || c.Usage.Caches != 0 || c.Usage.Uploads != 40 {
		t.Fatalf("清理：%+v %v", c, err)
	}
	if !present(work) || !present(upload) {
		t.Fatal("工作成果和附件不該被刪")
	}
	if len(f.writes) != 2 || f.writes[0] || !f.writes[1] {
		t.Fatalf("量用量不寫、清理才寫：%v", f.writes)
	}

	f.act.set(a.ID, true)
	if _, err := f.svc.Clean(ctx, a.ID); !errors.Is(err, ErrCleanBusy) {
		t.Fatalf("工作中：%v", err)
	}
	if e := ops.AsError(opErr(ErrCleanBusy)); e == nil || e.Code != ops.CodeConflict {
		t.Fatalf("%+v", e)
	}
	if v, err := f.svc.Storage(ctx, a.ID); err != nil || !v.HasComputer {
		t.Fatalf("工作中也可以看用量：%+v %v", v, err)
	}

	if v, err := f.svc.Storage(ctx, fresh.ID); err != nil || v.HasComputer || v.Usage != (cleanup.Usage{}) {
		t.Fatalf("還沒有電腦：%+v %v", v, err)
	}
	if c, err := f.svc.Clean(ctx, fresh.ID); err != nil || c.HasComputer || c.Result != (cleanup.Result{}) {
		t.Fatalf("還沒有電腦：%+v %v", c, err)
	}
	if _, err := f.svc.Storage(ctx, "no-such-bot"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("沒有這個 Bot：%v", err)
	}
}

// 每天的例行清理：工作中的 Bot 等下一輪；其他的連放超過 30 天的附件一起清。出自 server/cleanup.py daily()。
func TestCleanAllSkipsWorkingBotsAndExpiresUploads(t *testing.T) {
	ctx := context.Background()
	f := newCleanFixture(t)
	idle := f.bot(t, "阿凱", f.local.ID, true)
	busy := f.bot(t, "小編", f.local.ID, true)
	f.bot(t, "帳房", f.local.ID, true) // 有 box 列但還沒有電腦資料
	oldUpload := f.file(t, idle.ID, "workspace/uploads/10月訂單.csv", 40, 31*day)
	newUpload := f.file(t, idle.ID, "workspace/uploads/貼上的圖片.png", 60, 29*day)
	f.file(t, idle.ID, "hermes/cache/scratch/a.bin", 10, 4*day)
	busyUpload := f.file(t, busy.ID, "workspace/uploads/合約.pdf", 70, 60*day)
	f.act.set(busy.ID, true)

	n, freed, err := f.svc.CleanAll(ctx, time.Now())
	if err != nil || n != 1 || freed != 50 {
		t.Fatalf("清了 %d 個、%d 位元組：%v", n, freed, err)
	}
	if present(oldUpload) || !present(newUpload) || !present(busyUpload) {
		t.Fatalf("附件：舊的 %v、新的 %v、工作中的 %v", present(oldUpload), present(newUpload), present(busyUpload))
	}
}

func TestRunCleanupStopsWithTheService(t *testing.T) {
	f := newCleanFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.svc.RunCleanup(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("服務停了，例行清理還在等")
	}
}
