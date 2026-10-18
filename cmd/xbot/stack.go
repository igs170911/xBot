package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"xbot/internal/box"
	"xbot/internal/config"
	"xbot/internal/engine/hermes"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// stack 是服務的核心元件：資料庫、本機主機、Model Proxy、product.sock、Box Manager、Hermes 驅動、runtime。
// serve 與 dev 指令共用。
type stack struct {
	db        *store.DB
	local     *hosts.Local
	keyring   *proxy.Keyring
	pstore    *proxy.DBStore
	providers *proxy.Providers
	keys      *proxy.WorkerKeys
	model     *proxy.Handler
	budgets   *proxy.Budgets
	sockets   *ipc.Server
	boxes     *box.Manager
	driver    *hermes.Driver
	runtime   *runtime.Runtime
}

func openStack(cfg config.Config) (*stack, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, store.FileName))
	if err != nil {
		return nil, err
	}
	s := &stack{db: db}
	// 本機一定是一台 Bot 電腦主機（host 表沒有就新增）；Docker 沒開時記成離線，服務照常啟動。
	hostCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	s.local, err = hosts.EnsureLocal(hostCtx, db, cfg.DataDir)
	cancel()
	if err != nil {
		db.Close()
		return nil, err
	}
	if s.keyring, err = proxy.LoadKeyring(cfg.DataDir); err != nil {
		s.Close()
		return nil, err
	}
	logger := slog.Default()
	s.pstore = proxy.NewDBStore(db)
	s.providers = proxy.NewProviders(s.pstore, s.keyring)
	s.keys = proxy.NewWorkerKeys(s.pstore, s.keyring)
	// 超過花費上限時停下 Bot 正在做的工作（卡片由 Budgets 自己發）；Notify 由 serve 接上 SSE。
	s.budgets = &proxy.Budgets{DB: db, OnExceeded: func(e *proxy.BudgetError) {
		if e.BotID == "" || s.runtime == nil {
			return
		}
		go func() {
			if _, err := s.runtime.Stop(context.Background(), e.BotID, ""); err != nil {
				logger.Warn("超過花費上限，停止工作失敗", "bot", e.BotID, "err", err)
			}
		}()
	}}
	comp := &runtime.Computers{DB: db, Providers: s.pstore}
	s.model = proxy.NewHandler(proxy.Options{
		Keys: s.keys, Providers: s.providers, Usage: s.pstore, Logger: logger,
		Bindings: comp, Budget: s.budgets, Pricing: &proxy.Pricing{DB: db},
		Session: func(botID string) string {
			if s.runtime == nil {
				return ""
			}
			thread, _ := s.runtime.Active(botID)
			return thread
		},
	})
	s.sockets = ipc.New(ipc.Options{
		Dir:   func(id string) string { return filepath.Join(hosts.BotDir(cfg.DataDir, id), "ipc") },
		Model: s.model, Keys: s.keys, Logger: logger,
	})
	comp.Keys, comp.Sockets = s.keys, s.sockets
	s.boxes = box.New(db, hosts.NewRegistry(s.local), box.Options{Image: os.Getenv("XBOT_BOX_IMAGE"), Prepare: comp.Prepare})
	comp.Boxes = s.boxes
	s.driver = hermes.New(comp.Connect, logger)
	s.runtime = runtime.New(runtime.Options{DB: db, Driver: s.driver, Bus: events.New(), Models: comp, Logger: logger,
		Budget: func(ctx context.Context, botID, providerID string) error {
			if err := s.budgets.Check(ctx, botID); err != nil {
				return err
			}
			return s.budgets.CheckProvider(ctx, botID, providerID)
		}})
	s.pstore.Attribute = s.runtime.Active
	return s, nil
}

func (s *stack) Close() error {
	var errs []error
	if s.sockets != nil {
		errs = append(errs, s.sockets.Shutdown())
	}
	if s.local != nil {
		errs = append(errs, s.local.Close())
	}
	errs = append(errs, s.db.Close())
	return errors.Join(errs...)
}
