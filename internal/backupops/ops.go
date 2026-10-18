// Package backupops 是網頁的備份操作（xbot.backup.*）。和 backup 分開，backup 才不用相依 ops（store 的測試會用 backup）。
package backupops

import (
	"context"
	"errors"
	"log/slog"
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

// 設定 › 進階 › 備份與更新（J07）：網頁列出最近的備份、按「立即備份」（同 make backup：資料目錄＋每個 Bot 電腦的資料，
// 不含瀏覽器登入）。備份 Bot 電腦的資料可能要幾分鐘，所以在背景做，進度看 xbot.backup.list 的 job。
// 還原一定要先停服務，所以留在指令列（make restore）；網頁只顯示做法。

// Service 是網頁備份用的設定。
type Service struct {
	// DataDir 是資料目錄；備份放在旁邊的 <資料目錄>-backups。
	DataDir string
	// NewComputers 開一個讀 Bot 電腦資料的連線（dockervol）；nil 時只備份資料目錄。
	NewComputers func() backup.Computers
	Logger       *slog.Logger

	mu  sync.Mutex
	job *Job
	wg  sync.WaitGroup
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

// Skipped 是一台沒有備到的 Bot 電腦。
type Skipped struct {
	BotName string `json:"bot_name"`
	Reason  string `json:"reason"`
}

// Job 是網頁按的「立即備份」：進行中或最近一次的結果（服務重啟後就沒了）。
type Job struct {
	Running    bool      `json:"running"`
	StartedAt  int64     `json:"started_at"`
	FinishedAt int64     `json:"finished_at,omitempty"`
	File       File      `json:"file" desc:"備份檔（做完才有大小）"`
	Files      int       `json:"files,omitempty" desc:"資料目錄裡備了幾個檔案"`
	Computers  int       `json:"computers,omitempty" desc:"備到幾台 Bot 電腦的資料"`
	Skipped    []Skipped `json:"skipped,omitempty" desc:"沒有備到的 Bot 電腦（主機連不上等），主機恢復後再備份一次"`
	Error      string    `json:"error,omitempty" desc:"備份失敗的原因"`
}

// ListOut 是 xbot.backup.list 的輸出。
type ListOut struct {
	Dir     string `json:"dir" desc:"備份放在哪個資料夾"`
	DataDir string `json:"data_dir" desc:"XBot 的資料目錄"`
	Backups []File `json:"backups" desc:"新到舊，最多 BackupListMax 份"`
	Job     *Job   `json:"job,omitempty" desc:"網頁按的「立即備份」：進行中或最近一次"`
}

// CreateIn 是 xbot.backup.create 的輸入。
type CreateIn struct{}

// Register 登錄 xbot.backup.list、xbot.backup.create。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil || svc.DataDir == "" {
			return ops.Internal(errors.New("備份沒有設定資料目錄"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.backup.list", Summary: "列出最近的備份檔（資料目錄旁的 xbot-backups）與網頁備份的進度",
		Description: "還原要先停服務，請用指令列：make restore FILE=<備份檔>。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, _ *ops.Call, _ ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			return svc.List()
		},
	})
	ops.Register(reg, ops.Op[CreateIn, Job]{
		Name: "xbot.backup.create", Summary: "立即備份 XBot 的資料與每個 Bot 電腦的資料（同 make backup，不含瀏覽器登入）",
		Description: "在背景進行，進度看 xbot.backup.list 的 job。資料庫用一致的快照、Bot 電腦開著也能備份（不會停下它）。" +
			"訂閱登入憑證不備份；要包含瀏覽器登入請用指令列 make backup INCLUDE_LOGINS=1。同一時間只能有一份在做（再呼叫回目前的進度）。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ CreateIn) (Job, error) {
			if err := need(); err != nil {
				return Job{}, err
			}
			out := filepath.Join(backup.DefaultDir(svc.DataDir), backup.DefaultName(time.Now()))
			if call.DryRun {
				return Job{File: File{Name: filepath.Base(out), Path: out}}, nil
			}
			return svc.Start(ctx, out), nil
		},
	})
}

// List 列出備份資料夾裡的 .tar.gz，新到舊。資料夾不存在時回空的清單。
func (s *Service) List() (ListOut, error) {
	dir := backup.DefaultDir(s.DataDir)
	out := ListOut{Dir: dir, DataDir: s.DataDir, Backups: []File{}}
	s.mu.Lock()
	if s.job != nil {
		j := *s.job
		out.Job = &j
	}
	s.mu.Unlock()
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

// Start 在背景開始一份備份到 out；已經有一份在做時回目前的進度。
func (s *Service) Start(ctx context.Context, out string) Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.Running {
		return *s.job
	}
	s.job = &Job{Running: true, StartedAt: time.Now().UnixMilli(), File: File{Name: filepath.Base(out), Path: out}}
	j := *s.job
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(context.WithoutCancel(ctx), out)
	}()
	return j
}

// Wait 等背景的備份做完（測試、關服務時用）。
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) run(ctx context.Context, out string) {
	opts := backup.Options{DataDir: s.DataDir, Out: out}
	if s.NewComputers != nil {
		c := s.NewComputers()
		defer c.Close()
		opts.Computers = c
	}
	m, err := backup.CreateContext(ctx, opts)
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.job
	j.Running, j.FinishedAt = false, time.Now().UnixMilli()
	if err != nil {
		j.Error = err.Error()
		s.log().Warn("網頁備份失敗", "err", err)
		return
	}
	j.Files = m.Files
	for _, c := range m.Computers {
		switch {
		case c.Included:
			j.Computers++
		case c.Skipped != "":
			j.Skipped = append(j.Skipped, Skipped{BotName: c.BotName, Reason: c.Skipped})
		}
	}
	if info, err := os.Stat(out); err == nil {
		j.File.Size, j.File.CreatedAt = info.Size(), info.ModTime().UnixMilli()
	}
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
