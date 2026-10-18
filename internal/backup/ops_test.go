package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/store"
)

func opsEnv(t *testing.T, dataDir string) *ops.Registry {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	Register(reg, &Service{DataDir: dataDir})
	return reg
}

func invoke[T any](t *testing.T, reg *ops.Registry, id ops.Identity, name string, opts ops.Options) (T, error) {
	t.Helper()
	var out T
	res, err := reg.Invoke(context.Background(), id, name, json.RawMessage(`{}`), opts)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(res.Output, &out)
}

func TestBackupOps(t *testing.T) {
	data := makeData(t)
	reg := opsEnv(t, data)
	owner := ops.Owner("http")

	empty, err := invoke[ListOut](t, reg, owner, "xbot.backup.list", ops.Options{})
	if err != nil || len(empty.Backups) != 0 || empty.Dir != DefaultDir(data) || empty.DataDir != data {
		t.Fatalf("還沒有備份：%+v %v", empty, err)
	}

	dry, err := invoke[CreateOut](t, reg, owner, "xbot.backup.create", ops.Options{DryRun: true})
	if err != nil || !strings.HasPrefix(dry.File.Path, DefaultDir(data)) {
		t.Fatalf("預演：%+v %v", dry, err)
	}
	if _, err := os.Stat(DefaultDir(data)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("預演不能真的寫備份")
	}

	made, err := invoke[CreateOut](t, reg, owner, "xbot.backup.create", ops.Options{})
	if err != nil || made.Files == 0 || made.File.Size == 0 {
		t.Fatalf("立即備份：%+v %v", made, err)
	}
	var m Manifest
	if err := readManifest(made.File.Path, &m); err != nil || m.IncludeLogins {
		t.Fatalf("網頁備份不含登入：%+v %v", m, err)
	}

	// 舊的一份（修改時間較早）、不是備份的檔案不列
	old := filepath.Join(DefaultDir(data), "xbot-backup-20250101-000000.tar.gz")
	write(t, old, "x")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(DefaultDir(data), "notes.txt"), "x")
	list, err := invoke[ListOut](t, reg, owner, "xbot.backup.list", ops.Options{})
	if err != nil || len(list.Backups) != 2 || list.Backups[0].Name != made.File.Name || list.Backups[1].Path != old {
		t.Fatalf("新到舊：%+v %v", list, err)
	}

	// 代理要 admin 權限
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "代理", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}}
	if _, err := invoke[ListOut](t, reg, agent, "xbot.backup.list", ops.Options{}); err == nil {
		t.Fatal("沒有 admin 的代理不能看備份")
	}
}

func TestBackupCreateOneAtATime(t *testing.T) {
	s := &Service{DataDir: makeData(t)}
	s.mu.Lock()
	_, err := s.Create(context.Background(), filepath.Join(t.TempDir(), "b.tar.gz"))
	s.mu.Unlock()
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeConflict {
		t.Fatalf("同時兩份要回 conflict：%v", err)
	}
}

func TestBackupOpsWithoutDataDir(t *testing.T) {
	reg := ops.New(ops.Config{})
	Register(reg, nil)
	if _, ok := reg.Get("xbot.backup.create"); !ok {
		t.Fatal("沒有服務時也要登錄定義")
	}
}
