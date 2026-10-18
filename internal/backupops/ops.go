// Package backupops 是網頁的備份操作（xbot.backup.*）。和 backup 分開，backup 才不用相依 ops（store 的測試會用 backup）。
package backupops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"xbot/internal/backup"
	"xbot/internal/limits"
	"xbot/internal/ops"
)

// 設定 › 進階 › 備份與更新（J07）：網頁列出最近的備份、按「立即備份」。
// 還原一定要先停服務，所以留在指令列（make restore）；網頁只顯示做法。

// Service 是網頁備份用的設定：資料目錄（備份放在旁邊的 <資料目錄>-backups）。
type Service struct {
	DataDir string
	mu      sync.Mutex // 同一時間只做一份備份
}

// ListIn 是 xbot.backup.list 的輸入。
type ListIn struct{}

// File 是一份備份檔。
type File struct {
	Name      string `json:"name" desc:"檔名，例：xbot-backup-20261019-030000.tar.gz"`
	Path      string `json:"path" desc:"完整路徑（make restore FILE=… 用）"`
	Size      int64  `json:"size" desc:"位元組"`
	CreatedAt int64  `json:"created_at" desc:"建立時間（Unix 毫秒，取檔案修改時間）"`
}

// ListOut 是 xbot.backup.list 的輸出。
type ListOut struct {
	Dir     string `json:"dir" desc:"備份放在哪個資料夾"`
	DataDir string `json:"data_dir" desc:"XBot 的資料目錄"`
	Backups []File `json:"backups" desc:"新到舊，最多 BackupListMax 份"`
}

// CreateIn 是 xbot.backup.create 的輸入。
type CreateIn struct{}

// CreateOut 是 xbot.backup.create 的輸出。
type CreateOut struct {
	File  File `json:"file"`
	Files int  `json:"files" desc:"包進去幾個檔案"`
}

// Register 登錄 xbot.backup.list、xbot.backup.create。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil || svc.DataDir == "" {
			return ops.Internal(errors.New("備份沒有設定資料目錄"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.backup.list", Summary: "列出最近的備份檔（資料目錄旁的 xbot-backups）",
		Description: "還原要先停服務，請用指令列：make restore FILE=<備份檔>。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, _ *ops.Call, _ ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			return svc.List()
		},
	})
	ops.Register(reg, ops.Op[CreateIn, CreateOut]{
		Name: "xbot.backup.create", Summary: "立即備份 XBot 的資料（同 make backup，不含瀏覽器登入）",
		Description: "資料庫用一致的快照，服務執行中也能備份。不含 Bot 電腦裡的資料（Docker volume）與瀏覽器登入；" +
			"要包含登入請用指令列 make backup INCLUDE_LOGINS=1。同一時間只能有一份在做。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ CreateIn) (CreateOut, error) {
			if err := need(); err != nil {
				return CreateOut{}, err
			}
			out := filepath.Join(backup.DefaultDir(svc.DataDir), backup.DefaultName(time.Now()))
			if call.DryRun {
				return CreateOut{File: File{Name: filepath.Base(out), Path: out}}, nil
			}
			return svc.Create(ctx, out)
		},
	})
}

// List 列出備份資料夾裡的 .tar.gz，新到舊。資料夾不存在時回空的清單。
func (s *Service) List() (ListOut, error) {
	dir := backup.DefaultDir(s.DataDir)
	out := ListOut{Dir: dir, DataDir: s.DataDir, Backups: []File{}}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out.Backups = append(out.Backups, File{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Size: info.Size(), CreatedAt: info.ModTime().UnixMilli()})
	}
	slices.SortFunc(out.Backups, func(a, b File) int {
		if a.CreatedAt != b.CreatedAt {
			return int(b.CreatedAt - a.CreatedAt)
		}
		return strings.Compare(b.Name, a.Name)
	})
	if len(out.Backups) > limits.BackupListMax {
		out.Backups = out.Backups[:limits.BackupListMax]
	}
	return out, nil
}

// Create 建一份備份到 out（不含登入）。已經有一份在做時回 conflict。
func (s *Service) Create(_ context.Context, out string) (CreateOut, error) {
	if !s.mu.TryLock() {
		return CreateOut{}, ops.Conflict("已經在備份了，等它做完")
	}
	defer s.mu.Unlock()
	m, err := backup.Create(backup.Options{DataDir: s.DataDir, Out: out})
	if err != nil {
		return CreateOut{}, err
	}
	info, err := os.Stat(out)
	if err != nil {
		return CreateOut{}, err
	}
	return CreateOut{File: File{Name: filepath.Base(out), Path: out, Size: info.Size(), CreatedAt: info.ModTime().UnixMilli()}, Files: m.Files}, nil
}
