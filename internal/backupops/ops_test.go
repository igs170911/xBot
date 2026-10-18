package backupops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/backup"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeData(t *testing.T) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "xbot")
	write(t, filepath.Join(data, "access-token"), "secret")
	write(t, filepath.Join(data, "bots", "b1", "workspace", "report.md"), "# 報告")
	return data
}

func opsEnv(t *testing.T, dataDir string) (*ops.Registry, *Service) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	svc := &Service{DataDir: dataDir}
	Register(reg, svc)
	return reg, svc
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
	reg, svc := opsEnv(t, data)
	owner := ops.Owner("http")

	empty, err := invoke[ListOut](t, reg, owner, "xbot.backup.list", ops.Options{})
	if err != nil || len(empty.Backups) != 0 || empty.Dir != backup.DefaultDir(data) || empty.DataDir != data {
		t.Fatalf("還沒有備份：%+v %v", empty, err)
	}

	dry, err := invoke[Job](t, reg, owner, "xbot.backup.create", ops.Options{DryRun: true})
	if err != nil || !strings.HasPrefix(dry.File.Path, backup.DefaultDir(data)) {
		t.Fatalf("預演：%+v %v", dry, err)
	}
	if _, err := os.Stat(backup.DefaultDir(data)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("預演不能真的寫備份")
	}

	started, err := invoke[Job](t, reg, owner, "xbot.backup.create", ops.Options{})
	if err != nil || !started.Running {
		t.Fatalf("立即備份在背景開始：%+v %v", started, err)
	}
	svc.Wait()
	done, err := invoke[ListOut](t, reg, owner, "xbot.backup.list", ops.Options{})
	if err != nil || done.Job == nil || done.Job.Running || done.Job.Error != "" || done.Job.Files == 0 || done.Job.File.Size == 0 {
		t.Fatalf("做完的進度：%+v %v", done.Job, err)
	}
	made := done.Job
	if m, err := backup.ReadManifest(made.File.Path); err != nil || m.IncludeLogins {
		t.Fatalf("網頁備份不含登入：%+v %v", m, err)
	}

	// 舊的一份（修改時間較早）、不是備份的檔案不列
	old := filepath.Join(backup.DefaultDir(data), "xbot-backup-20250101-000000.tar.gz")
	write(t, old, "x")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(backup.DefaultDir(data), "notes.txt"), "x")
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
	s.job = &Job{Running: true, File: File{Name: "a.tar.gz"}}
	if j := s.Start(context.Background(), filepath.Join(t.TempDir(), "b.tar.gz")); j.File.Name != "a.tar.gz" || !j.Running {
		t.Fatalf("已經在做時回目前的進度：%+v", j)
	}
	s.job.Running = false
	s.Start(context.Background(), filepath.Join(t.TempDir(), "missing", "..", "c.tar.gz"))
	s.Wait()
	if s.job.Running || s.job.File.Name != "c.tar.gz" {
		t.Fatalf("新的一份：%+v", s.job)
	}
}

func TestBackupFailureIsReported(t *testing.T) {
	s := &Service{DataDir: filepath.Join(t.TempDir(), "nope")}
	s.Start(context.Background(), filepath.Join(t.TempDir(), "b.tar.gz"))
	s.Wait()
	if s.job.Running || s.job.Error == "" {
		t.Fatalf("找不到資料目錄要記下原因：%+v", s.job)
	}
}

func TestBackupOpsWithoutDataDir(t *testing.T) {
	reg := ops.New(ops.Config{})
	Register(reg, nil)
	if _, ok := reg.Get("xbot.backup.create"); !ok {
		t.Fatal("沒有服務時也要登錄定義")
	}
}
