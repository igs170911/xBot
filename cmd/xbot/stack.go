package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"xbot/internal/box"
	"xbot/internal/collab"
	"xbot/internal/config"
	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/engines"
	"xbot/internal/events"
	"xbot/internal/files"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/memory"
	"xbot/internal/org"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/runtime"
	"xbot/internal/skills"
	"xbot/internal/store"
)

// stack 是服務的核心元件：資料庫、本機主機、Model Proxy、product.sock、Box Manager、引擎驅動（Hermes、ACP）、
// 訂閱憑證、runtime。
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
	driver    *engine.Router
	creds     *engines.Service
	hosts     *hosts.Service
	runtime   *runtime.Runtime
	routines  *routines.Service // 定時任務排程器（serve 才啟動；Notify 由 serve 接上 SSE）
	comp      *runtime.Computers
	skills    *skills.Service
	memory    *memory.Service
	collab    *collab.Service
	files     *files.Service
	org       *org.Service
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
	comp.XBotMCP = runtime.XBotMCPServer() // Claude Code、Codex 也拿到 XBot MCP（C11）
	registry := hosts.NewRegistry(s.local)
	comp.Hosts = registry
	image := os.Getenv("XBOT_BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	s.boxes = box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	bus := events.New()
	// 遠端主機（B12）：連線、健康狀態寫回 host.status、加入與檢查、擺放與排隊。
	s.hosts = hosts.NewService(hosts.Options{DB: db, Registry: registry, Local: s.local, DataDir: cfg.DataDir, Image: image,
		LocalSocket: s.sockets.Path, Bus: bus, Logger: logger})
	comp.Boxes = s.boxes
	// 訂閱 CLI 的憑證從執行 XBot 服務的使用者家目錄讀（EN-2b）。
	home, _ := os.UserHomeDir()
	s.creds = &engines.Service{DB: db, HostHome: home, Locate: comp.Locate, UID: box.BotUID, GID: box.BotGID, Logger: logger}
	comp.Creds = s.creds
	drivers := []engine.Driver{hermes.New(comp.Connect, logger)}
	for _, name := range []string{acp.EngineClaude, acp.EngineCodex} {
		drivers = append(drivers, acp.New(acp.Options{Agent: acp.Agents[name], Launch: comp.LaunchACP, Authorize: s.creds.Authorize,
			MCPServers: comp.MCPServers, Logger: logger}))
	}
	s.driver = engine.NewRouter(hermes.Name, comp.EngineOf, drivers...)
	s.creds.Close = s.driver.Close
	s.runtime = runtime.New(runtime.Options{DB: db, Driver: s.driver, Bus: bus, Models: comp, Logger: logger,
		Admit: func(ctx context.Context, botID string, wait func(any)) (func(), error) {
			return s.hosts.Admit(ctx, botID, func(w hosts.Wait) { wait(w) })
		},
		Budget: func(ctx context.Context, botID, providerID string) error {
			if err := s.budgets.Check(ctx, botID); err != nil {
				return err
			}
			return s.budgets.CheckProvider(ctx, botID, providerID)
		}})
	s.pstore.Attribute = s.runtime.Active
	s.routines = routines.New(routines.Options{DB: db, Runtime: s.runtime, Logger: logger})
	s.comp = comp
	s.skills = &skills.Service{Locate: comp.BotFS, Library: &skills.Library{Dir: filepath.Join(cfg.DataDir, "skills")}}
	s.memory = &memory.Service{DB: db, Locate: comp.BotFS}
	// Bot 之間的傳話與群組（X02）：接上 runtime 的 Hooks；帶檔經 Docker 讀寫兩邊電腦的 /workspace。
	carry := collab.DockerFiles{
		Locate: func(ctx context.Context, botID string) (collab.CopyAPI, string, error) {
			api, id, err := comp.Docker(ctx, botID)
			if api == nil {
				return nil, id, err
			}
			return api, id, err
		},
		UID: box.BotUID, GID: box.BotGID,
	}
	s.collab = collab.New(collab.Options{DB: db, Runtime: s.runtime, Logger: logger, Files: carry})
	// 檔案面板（C33）：經 Docker 讀寫 Bot 電腦的 /workspace；在電腦裡執行的指令用 Bot 自己的使用者。傳給別的 Bot 沿用 X02 帶檔。
	s.files = &files.Service{Locate: files.DockerLocator(comp.BotFS, fmt.Sprintf("%d:%d", box.BotUID, box.BotGID)), Carry: carry}
	// 團隊、匯報線、提案與討論（X03）。Bot 的系統提示：隊友名冊＋組織與回報。
	s.org = org.New(org.Options{DB: db, Runtime: s.runtime, Collab: s.collab, Logger: logger})
	comp.Roster = func(ctx context.Context, botID string) (string, error) {
		team, err := s.collab.RosterText(ctx, botID)
		if err != nil {
			return "", err
		}
		soul, err := s.org.SoulText(ctx, botID)
		return team + soul, err
	}
	if err := s.hosts.Start(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *stack) Close() error {
	var errs []error
	if s.routines != nil {
		s.routines.Close()
	}
	if s.hosts != nil {
		errs = append(errs, s.hosts.Close())
	}
	if s.sockets != nil {
		errs = append(errs, s.sockets.Shutdown())
	}
	if s.local != nil {
		errs = append(errs, s.local.Close())
	}
	errs = append(errs, s.db.Close())
	return errors.Join(errs...)
}
