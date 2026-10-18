package files

import (
	"context"
	"errors"
	"io"
	"path"
	"time"

	"xbot/internal/boxfs"
)

// 錯誤。
var (
	ErrNotFound   = errors.New("找不到這個檔案或資料夾")
	ErrLink       = errors.New("這個路徑經過連結（symlink），為了安全不開啟")
	ErrIsDir      = errors.New("這是資料夾")
	ErrNotDir     = errors.New("這不是資料夾")
	ErrExists     = errors.New("已經有同名的檔案或資料夾")
	ErrNotRunning = boxfs.ErrNotRunning
	ErrNoComputer = boxfs.ErrNoComputer
)

// Info 是工作區裡一項的基本資訊。Rel 是 /workspace 底下的相對路徑。
type Info struct {
	Rel      string
	Dir      bool
	Size     int64
	Modified time.Time
}

// Workspace 是一個 Bot 電腦裡的 /workspace。所有路徑都是 Rel 整理過的相對路徑。
//
// 連結（symlink）一律不跟：Stat、Open、Write 經過連結時回 ErrLink；List、Walk 不列出連結。
type Workspace interface {
	// List 列出一個資料夾（不遞迴、含隱藏項目），最多 limits.FilesFolderScanMax 項。要開機。
	List(ctx context.Context, rel string) ([]Info, error)
	// Walk 列出所有一般檔案，略過隱藏項目與套件資料夾（LazyBoy files.walk），最多 limits.FilesWalkScanMax 個。要開機。
	Walk(ctx context.Context) ([]Info, error)
	// Stat 看一項；不存在回 ErrNotFound。
	Stat(ctx context.Context, rel string) (Info, error)
	// Open 串流讀一個一般檔案。
	Open(ctx context.Context, rel string) (io.ReadCloser, Info, error)
	// Write 寫入（或換掉）一個檔案，缺的上層資料夾一起建；r 要正好 size 個位元組。
	Write(ctx context.Context, rel string, size int64, r io.Reader) error
	// Mkdir 建資料夾（可以多層）；已經有同名的資料夾不算錯。
	Mkdir(ctx context.Context, rel string) error
	// Move 搬移或改名；目的地已經存在時回 ErrExists。要開機。
	Move(ctx context.Context, from, to string) error
	// Remove 刪掉一個檔案或整個資料夾。要開機。
	Remove(ctx context.Context, rel string) error
}

// Locator 找 Bot 的 /workspace。wake 為 true 時，需要開機的動作（列資料夾、搬移、刪除）會先開機；
// 為 false 時那些動作回 ErrNotRunning。Bot 還沒有電腦時回 ErrNoComputer。
type Locator func(ctx context.Context, botID string, wake bool) (Workspace, error)

func base(rel string) string {
	if rel == "" {
		return path.Base(Root)
	}
	return path.Base(rel)
}
