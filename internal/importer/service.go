package importer

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"xbot/internal/boxfs"
	"xbot/internal/memory"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// KindProgress 是匯入進度的事件（payload：Run）。
const KindProgress = "import.progress"

// Providers 存模型金鑰（*proxy.Providers 符合）：金鑰加密後只放在服務端。
type Providers interface {
	Put(ctx context.Context, p proxy.Provider) error
	Get(ctx context.Context, id string) (proxy.Provider, error)
}

// Service 是匯入需要的元件。
type Service struct {
	DB *store.DB
	// Locate 找（或建立）Bot 的電腦（runtime.Computers.BotFS）：技能、記憶、電腦資料寫進去。
	Locate boxfs.Locator
	// Memory 寫 Bot 的記憶（和記憶頁同一套，留版本紀錄）。
	Memory *memory.Service
	// Providers 存模型金鑰；nil 時不帶金鑰。
	Providers Providers
	// ApprovalItems 把「只有你能決定的事」加進組織設定（org.Service）；nil 時不帶。回傳加完的清單。
	ApprovalItems func(ctx context.Context, add []string) ([]string, error)
	// Publish 發布事件（進度）；可為 nil。
	Publish func(kind string, payload any)
	// Roots 是要找的資料夾；nil 用 DefaultRoots()。
	Roots []string
	// TempDir 是複製舊資料庫用的暫存資料夾的上層；空白用系統的。
	TempDir string
	Logger  *slog.Logger
	// Now 是現在時間（測試用）；nil 用 time.Now。
	Now func() time.Time

	cacheMu sync.Mutex
	cache   map[string]cached

	mu     sync.Mutex
	runs   []*Run // 新的在前
	active bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) base() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	}
	return s.ctx
}

// Close 停下進行中的匯入並等它結束（已經建立的會留著，沒做完的項目標成失敗）。
func (s *Service) Close() {
	s.base()
	s.cancel()
	s.wg.Wait()
}
