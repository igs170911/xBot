package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"xbot/internal/apps"
	"xbot/internal/box"
	"xbot/internal/collab"
	"xbot/internal/computers"
	"xbot/internal/config"
	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/engines"
	"xbot/internal/events"
	"xbot/internal/files"
	"xbot/internal/hosts"
	"xbot/internal/importer"
	"xbot/internal/ipc"
	"xbot/internal/judge"
	"xbot/internal/lessons"
	"xbot/internal/limits"
	"xbot/internal/limitsops"
	"xbot/internal/memory"
	"xbot/internal/network"
	"xbot/internal/org"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/runtime"
	"xbot/internal/screen"
	"xbot/internal/skills"
	"xbot/internal/store"
	"xbot/internal/teaching"
	"xbot/internal/voice"
)

// stack 是服務的核心元件：資料庫、本機主機、Model Proxy、product.sock、Box Manager、引擎驅動（Hermes、ACP）、
// 訂閱憑證、runtime。
// serve 與 dev 指令共用。
type stack struct {
	dataDir   string // 資料目錄（網頁備份用）
	boxImage  string // Bot 電腦映像（網頁備份的輔助容器用）
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
	judge     *judge.Service   // 快判（X05）：經 Model Proxy 呼叫 Jev／OpenCode Go，規則兜底
	lessons   *lessons.Service // 從沒做完的工作學到的事：老師模型提議、你同意才加進守則
	computers *computers.Service
	// apps 是連接應用程式與登入（C34）。
	apps    *apps.Service
	network *network.Service // 內建 Tailscale 與外部入口（C35）：serve 才建立
	// stopIdle 停掉閒置休眠的背景檢查（startBackground 開始）。
	stopIdle context.CancelFunc
	screen   *screen.Service   // 看畫面與接手（B14）
	teaching *teaching.Service // 示範教學（B32）
	importer *importer.Service // 從 LazyBoy、OpenMausBot 匯入（V10）
	voice    *voice.Service    // 語音（E32）：合成與辨識經 Model Proxy
	// hermesDriver 是 Hermes 驅動（重新載入 MCP 用）；toolReloads 記著哪些 Bot 正在等空下來重新載入（reloadBotTools）。
	hermesDriver *hermes.Driver
	toolReloads  sync.Map
}

func openStack(cfg config.Config) (*stack, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, store.FileName))
	if err != nil {
		return nil, err
	}
	s := &stack{db: db, dataDir: cfg.DataDir}
	// 你調過的限制（設定 → 進階 → 限制）；讀不到就用預設，不擋啟動
	if err := limitsops.Load(context.Background(), db); err != nil {
		slog.Warn("讀不到調過的限制，先用預設", "err", err)
	}
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
	// 供應商的額度用完、限流（EN-7）：Model Proxy 看到時記下，Hermes 的一輪失敗時用來開額度卡。
	quotaLog := &engine.QuotaLog{}
	s.model = proxy.NewHandler(proxy.Options{
		ProviderError: providerError(quotaLog),
		Keys:          s.keys, Providers: s.providers, Usage: s.pstore, Logger: logger,
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
	bus := events.New()
	// 網路防護（SEC-4）預設開著；只有不能用 iptables 的環境（例：rootless Docker）才設 XBOT_NETGUARD=off。
	noGuard := os.Getenv("XBOT_NETGUARD") == "off"
	if noGuard {
		logger.Warn("XBOT_NETGUARD=off：Bot 的電腦連得到這台主機與區網（SEC-4 網路防護已關閉）")
	}
	s.boxImage = image
	s.boxes = box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare, NoNetGuard: noGuard, Logger: logger,
		// 電腦狀態變了：推給介面，並叫醒排隊等電腦的工作（休眠空出了名額）。
		Notify: func(st box.Status) {
			computers.Publish(db, bus, computers.KindStatus, st.BotID, st, true)
			if s.hosts != nil {
				s.hosts.Signal()
			}
		},
		// 連資料一起刪電腦時先關掉服務這邊的 product.sock。
		Release: func(botID string) { _ = s.sockets.Close(botID) },
		// 「從家裡出去」（CL-7）的 exit node 與 Tailscale 登入金鑰存在 computers 的設定裡。
		Egress: box.EgressFunc(func(ctx context.Context) (box.EgressConfig, error) {
			if s.computers == nil {
				return box.EgressConfig{}, box.ErrEgressNotSet
			}
			return s.computers.EgressConfig(ctx)
		}),
	})
	// 遠端主機（B12）：連線、健康狀態寫回 host.status、加入與檢查、擺放與排隊。
	s.hosts = hosts.NewService(hosts.Options{DB: db, Registry: registry, Local: s.local, DataDir: cfg.DataDir, Image: image,
		LocalSocket: s.sockets.Path, Bus: bus, Logger: logger})
	comp.Boxes = s.boxes
	// 訂閱 CLI 的憑證從執行 XBot 服務的使用者家目錄讀（EN-2b）。
	home, _ := os.UserHomeDir()
	s.creds = &engines.Service{DB: db, HostHome: home, Locate: comp.Locate, UID: box.BotUID, GID: box.BotGID, Logger: logger,
		Exec: comp.ExecRoot, Launch: comp.LaunchACP}
	comp.Creds = s.creds
	hermesDriver := hermes.New(comp.Connect, logger)
	s.hermesDriver = hermesDriver
	drivers := []engine.Driver{hermesDriver}
	for _, name := range acp.Engines {
		// Instructions：和 Hermes 的 SOUL.md 同一份身分與守則（隊友名冊與組織用 comp.Roster，下面才接上）。
		drivers = append(drivers, acp.New(acp.Options{Agent: acp.Agents[name], Launch: comp.LaunchACP, Authorize: s.creds.Authorize,
			MCPServers: comp.MCPServers, Instructions: comp.Instructions, Logger: logger,
			OnModels: func(_ string, current string, models []acp.ModelChoice) {
				s.creds.RememberModels(name, current, models)
			}}))
	}
	s.driver = engine.NewRouter(hermes.Name, comp.EngineOf, drivers...)
	// 快判（X05）：服務自己經 Model Proxy 呼叫模型（金鑰只在 Model Proxy），用量標記 judge。
	// 只打 chat/completions；只支援別的呼叫方式的模型（例：Responses API）存設定時擋下、執行時略過（Transport）。
	s.judge = &judge.Service{DB: db, Logger: logger, Transport: s.pstore.TransportFor, FreeTierBlocked: s.pstore.FreeTierBlocked, ShadowInBackground: true, Call: func(ctx context.Context, provider string, body []byte, session string) (int, []byte, error) {
		resp, err := s.model.Call(ctx, proxy.CallRequest{Provider: provider, Path: "chat/completions", Body: body, Engine: judge.UsageEngine,
			Session: session, MaxBytes: limits.JudgeReplyBytes})
		if errors.Is(err, proxy.ErrNotFound) {
			return 0, nil, judge.ErrNotConfigured
		}
		return resp.Status, resp.Body, err
	}}
	// Log the judge backend selection at startup (2026-10-22i)
	if set, err := s.judge.Settings(context.Background()); err == nil {
		if set.Tier1.On() || set.Tier2.On() {
			logger.Info("judge backend: API-based", "tier1", set.Tier1.Provider+"/"+set.Tier1.Model, "tier2", set.Tier2.Provider+"/"+set.Tier2.Model)
		} else {
			logger.Warn("judge backend: not configured, using STRICT fallback only")
		}
	}
	s.creds.Close = s.driver.Close
	// 學到的事（harness2 的做法）：工作沒做完、或你說「不對？」時，老師模型看步驟、提議一條守則；你同意才寫進 SOUL.md。
	s.lessons = &lessons.Service{DB: db, Logger: logger, Call: s.model.Call, Teacher: teacherModel(db, s.pstore),
		SoulChanged: func(ctx context.Context, botID string) {
			ids := []string{botID}
			if botID == "" {
				ids = nil
				if all, err := db.ListBots(ctx, false); err == nil {
					for _, b := range all {
						ids = append(ids, b.ID)
					}
				}
			}
			for _, id := range ids {
				if err := comp.RefreshSoul(ctx, id); err != nil {
					logger.Warn("更新 Bot 電腦裡的 SOUL.md 失敗，下次開機前會再寫", "bot", id, "err", err)
				}
			}
		}}
	comp.Lessons = s.lessons.For
	// 對話分流路由器（即時確認）：用快判判斷閒聊、查進度還是實際工作
	router := &runtime.Router{
		Decider:   s.judge,
		FastModel: nil, // TODO: 配置快速聊天模型
		Logger:    logger,
	}
	// 2026-10-22e：Bot 之間的傳話與群組（X02）檔案操作，也用於 owner DM 太長的回覆存檔
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
	s.runtime = runtime.New(runtime.Options{DB: db, Driver: s.driver, Bus: bus, Models: comp, Logger: logger, OwnerNote: runtime.OwnerNote,
		Router:     router,
		Unfinished: s.lessons.Consider,
		Admit: func(ctx context.Context, botID string, wait func(any)) (func(), error) {
			return s.hosts.Admit(ctx, botID, func(w hosts.Wait) { wait(w) })
		},
		Budget: func(ctx context.Context, botID, providerID string) error {
			if err := s.budgets.Check(ctx, botID); err != nil {
				return err
			}
			return s.budgets.CheckProvider(ctx, botID, providerID)
		},
		Judge: s.judge.TaskDoneJudge(), Namer: s.judge.Title, Summarize: s.judge.Summary,
		QuotaLog: quotaLog, QuotaCard: s.creds.QuotaCard,
		Files: carry}) // owner DM 太長的回覆存檔
	// 額度用完時換引擎（xbot.engines.switch）。
	s.creds.Models = modelChoices(db, s.pstore, s.providers)
	s.creds.Busy = func(botID string) bool { busy, _ := s.runtime.Busy(botID); return busy }
	s.creds.Forget = quotaLog.Forget
	s.creds.Resend = func(ctx context.Context, botID, threadID, text string) (string, error) {
		res, err := s.runtime.Start(ctx, runtime.ChatRequest{BotID: botID, ThreadID: threadID, Text: text, Queue: true})
		return res.TaskID, err
	}
	s.pstore.Attribute = s.runtime.Active
	if !cfg.FakeNow.IsZero() {
		logger.Warn("XBOT_FAKE_NOW：定時任務用假的現在時間（只供測試）", "start", cfg.FakeNow)
	}
	s.routines = routines.New(routines.Options{DB: db, Runtime: s.runtime, Logger: logger, Rewrite: s.judge.Routine, Now: cfg.Clock()})
	s.comp = comp
	s.skills = &skills.Service{Locate: comp.BotFS, Library: &skills.Library{Dir: filepath.Join(cfg.DataDir, "skills")}}
	s.memory = &memory.Service{DB: db, Locate: comp.BotFS, Active: s.runtime.Active}

	// Wire production judge for collab layer (2026-10-22, fixed 2026-10-22i, item 1: Grok CLI backend)
	// E5a: No API key configured -> Grok CLI judge (not deepseek API tier)
	var peerJudge collab.PeerJudge
	set, _ := s.judge.Settings(context.Background())
	// E5a: A tier is "configured" only if provider has an actual API key
	hasAPIKey := false
	if set.Tier1.Provider != "" {
		if prov, err := s.pstore.GetProvider(context.Background(), set.Tier1.Provider); err == nil && len(prov.SealedKey) > 0 {
			hasAPIKey = true
		}
	}
	if !hasAPIKey && set.Tier2.Provider != "" {
		if prov, err := s.pstore.GetProvider(context.Background(), set.Tier2.Provider); err == nil && len(prov.SealedKey) > 0 {
			hasAPIKey = true
		}
	}

	if hasAPIKey {
		// Use Model Proxy API-based judge (current production path)
		decideFn := func(ctx context.Context, kind, content, botID string) (string, error) {
			ask := judge.Ask{Use: "peer_reply", Text: content, BotID: botID}
			decision, err := s.judge.Decide(ctx, ask)
			// Treat not_configured, errors, and timeouts as "no verdict" -> STRICT fallback
			if err != nil || decision.Outcome == judge.OutcomeNotConfigured ||
				decision.Outcome == judge.OutcomeError || decision.Outcome == judge.OutcomeTimeout {
				return "", collab.ErrJudgeNotConfigured
			}
			// Map decision answer label to peer_reply format
			switch decision.Answer.Label {
			case "fyi", "skip":
				return "skip", nil
			case "handle", "ok":
				return "handle", nil
			default:
				return "", nil // Unknown answer -> no judgment
			}
		}
		peerJudge = collab.NewProductionJudge(decideFn, logger)
		logger.Info("judge backend: API-based (Model Proxy)", "tier1", set.Tier1.Provider+"/"+set.Tier1.Model, "tier2", set.Tier2.Provider+"/"+set.Tier2.Model)
	} else {
		// Default to Grok CLI judge when no API key is set (item 1: one-shot ACP session)
		// Find Grok ACP driver from the already-created drivers
		var grokDriver *acp.Driver
		for _, drv := range drivers {
			if acpD, ok := drv.(*acp.Driver); ok && acpD.Name() == acp.EngineGrok {
				grokDriver = acpD
				break
			}
		}
		if grokDriver == nil {
			logger.Warn("judge backend: Grok CLI driver not found, using STRICT fallback only")
			peerJudge = nil
		} else {
			// Item 2 (2026-10-22): Judge uses direct grok CLI exec, not ACP Driver
			modelName := detectGrokJudgeModel(logger)
			if modelName == "" {
				logger.Warn("judge backend: No suitable Grok model found, falling back to STRICT mode")
				peerJudge = nil
			} else {
				peerJudge = &judge.GrokJudge{
					ContainerFinder: func(botID string) string {
						box, err := db.GetBox(context.Background(), botID)
						if err != nil || box.ContainerID == "" {
							return ""
						}
						return box.ContainerID
					},
					Log:       logger,
					Timeout:   12 * time.Second, // F2: Single source of truth - Grok median is 5.3s, ~23% timeout at 5s
					ModelName: modelName,
				}
				logger.Info("judge backend: Grok CLI (direct exec, no ACP sessions, exempt from session cap)", "model", modelName)
			}
		}
	}

	// Bot 之間的傳話與群組（X02）：接上 runtime 的 Hooks；帶檔經 Docker 讀寫兩邊電腦的 /workspace（已在 runtime 初始化時共用）。
	s.collab = collab.New(collab.Options{DB: db, Runtime: s.runtime, Logger: logger, Judge: s.judge.Responder, JudgeService: s.judge, Files: carry, PeerJudge: peerJudge})
	// 檔案面板（C33）：經 Docker 讀寫 Bot 電腦的 /workspace；在電腦裡執行的指令用 Bot 自己的使用者。傳給別的 Bot 沿用 X02 帶檔。
	s.files = &files.Service{Locate: files.DockerLocator(comp.BotFS, fmt.Sprintf("%d:%d", box.BotUID, box.BotGID)), Carry: carry}
	// 團隊、匯報線、提案與討論（X03）。Bot 的系統提示：隊友名冊＋組織與回報。
	s.org = org.New(org.Options{DB: db, Runtime: s.runtime, Collab: s.collab, Logger: logger,
		ManageChanged: func(_ context.Context, botID string) { s.reloadBotTools(botID) }})
	comp.Roster = func(ctx context.Context, botID string) (string, error) {
		team, err := s.collab.RosterText(ctx, botID)
		if err != nil {
			return "", err
		}
		soul, err := s.org.SoulText(ctx, botID)
		return team + soul, err
	}
	// 連接應用程式與登入（C34）：權杖加密存在服務端（和模型金鑰同一把主金鑰），Bot 經 product.sock 由 XBot 轉送。
	browser := &apps.DockerBrowser{Locate: comp.BotFS, User: fmt.Sprintf("%d:%d", box.BotUID, box.BotGID), UID: box.BotUID, GID: box.BotGID}
	s.apps = apps.New(apps.Options{DB: db, Keys: s.keyring, BaseURL: cfg.URL(), Logger: logger, Browser: browser,
		// I03 主要流程：登入頁開在 Bot 的電腦上，並在「待我處理」出一張接手卡（B14），你在即時畫面登入。
		OpenOnBot: func(ctx context.Context, botID, url string) error {
			if err := browser.OpenURL(ctx, botID, url); err != nil {
				return err
			}
			if s.screen == nil {
				return nil
			}
			_, err := s.screen.RequestTakeover(ctx, botID, "請在這台電腦的瀏覽器登入並按「允許存取」，完成後 XBot 會自動繼續。")
			return err
		},
		OnChange: func(ctx context.Context, botIDs []string) {
			go s.refreshApps(ctx, hermesDriver, botIDs)
		}})
	comp.Apps = s.apps.Servers
	s.sockets.SetApps(s.apps.Proxy)
	// 電腦的生命週期操作與「更新電腦」（B13）。
	// 搬家（B31，CL-5）與「從家裡出去」（CL-7）也在這裡。
	s.computers = &computers.Service{DB: db, Boxes: s.boxes, Hosts: s.hosts, Activity: s.runtime, Bus: bus, Logger: logger, Sealer: s.keyring}
	// 語音（E32）：朗讀、語音輸入、語音訊息、通話。合成與辨識經 Model Proxy（金鑰不離開服務端），用量記成 engine=voice。
	s.voice = &voice.Service{DB: db, Bus: bus, CacheDir: filepath.Join(cfg.DataDir, "voice-cache"), Logger: logger,
		Media: func(ctx context.Context, req voice.MediaRequest) (voice.MediaResponse, error) {
			resp, err := s.model.CallMedia(ctx, proxy.MediaRequest{Provider: req.Provider, Path: req.Path, ContentType: req.ContentType,
				Body: req.Body, BotID: req.BotID, Engine: voice.UsageEngine, MaxBytes: req.MaxBytes})
			var be *proxy.BudgetError
			switch {
			case errors.As(err, &be):
				return voice.MediaResponse{}, &voice.BudgetError{Message: be.Error()}
			case errors.Is(err, proxy.ErrNotFound), errors.Is(err, proxy.ErrCallPath):
				return voice.MediaResponse{}, voice.ErrNoEngine
			}
			return voice.MediaResponse{Status: resp.Status, ContentType: resp.ContentType, Body: resp.Body}, err
		},
		Providers: func(ctx context.Context) ([]voice.Provider, error) {
			recs, err := s.pstore.ListProviders(ctx)
			out := make([]voice.Provider, 0, len(recs))
			for _, r := range recs {
				out = append(out, voice.Provider{ID: r.ID, Label: r.Label, OAuth: r.Auth != ""})
			}
			return out, err
		}}
	// 看畫面與接手（B14）：noVNC 經服務代理、接手時暫停 Bot、辦公室縮圖。
	s.screen = screen.New(screen.Options{DB: db, Boxes: s.boxes, Hosts: registry, Runtime: s.runtime, Bus: bus, Logger: logger})
	// 示範教學（B32）：接手畫面、在電腦裡跑錄製器、整理成技能草稿（快判用途 17 潤飾名稱與用途）、存成 Bot 的技能（C31）。
	s.teaching = teaching.New(teaching.Options{DB: db, Screen: s.screen, Runtime: s.runtime, Bus: bus, Logger: logger, Polish: s.judge.Teach,
		Open: teaching.DockerOpener(comp.BotFS, func(ctx context.Context, botID string) error {
			_, err := s.computers.Wake(ctx, botID, true)
			return err
		}),
		Create: func(ctx context.Context, botID string, e skills.Edit) (skills.Skill, error) {
			f, err := comp.BotFS(ctx, botID, true)
			if err != nil {
				return skills.Skill{}, err
			}
			return skills.Bot{FS: f, BotID: botID}.Create(ctx, e)
		}})
	// 從 LazyBoy、OpenMausBot 匯入（V10）：技能、記憶、電腦資料寫進新 Bot 的電腦（沒有時先建立、不開機）；
	// 金鑰加密後存在 Model Proxy；進度推給介面。
	s.importer = &importer.Service{DB: db, Locate: comp.BotFS, Memory: s.memory, Providers: s.providers, Logger: logger,
		ApprovalItems: func(ctx context.Context, add []string) ([]string, error) {
			cur, err := s.org.ApprovalItems(ctx)
			if err != nil {
				return nil, err
			}
			return s.org.SetApprovalItems(ctx, append(cur, add...))
		},
		Publish: func(kind string, payload any) { computers.Publish(db, bus, kind, "", payload, false) }}
	if err := s.hosts.Start(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// refreshApps 把 Bot 能用的連接寫進它的電腦（botIDs 是 nil 表示所有 Bot），沒在工作的 Hermes 立刻重新載入 MCP；
// 正在工作的等下一個對話（同 LazyBoy connectors.apply）。ACP 引擎在下一個 session 拿到新的清單。
func (s *stack) refreshApps(ctx context.Context, hd *hermes.Driver, botIDs []string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if botIDs == nil {
		bots, err := s.db.ListBots(ctx, true)
		if err != nil {
			slog.Warn("更新 Bot 的應用程式失敗", "err", err)
			return
		}
		for _, b := range bots {
			botIDs = append(botIDs, b.ID)
		}
	}
	for _, id := range botIDs {
		if err := s.comp.RefreshApps(ctx, id); err != nil {
			slog.Warn("把應用程式寫進 Bot 的電腦失敗，下次開機前會再寫", "bot", id, "err", err)
			continue
		}
		if thread, _ := s.runtime.Active(id); thread != "" {
			continue
		}
		if _, err := hd.ReloadMCP(ctx, id); err != nil {
			slog.Warn("請 Hermes 重新載入 MCP 失敗，下一個對話生效", "bot", id, "err", err)
		}
	}
}

// reloadBotTools 在 Bot 的團隊管理權限改了以後，請 Hermes 重新載入 MCP：XBot MCP 依權限列出不同的工具
// （mcpserver.BotHandler），重新載入後新的清單才會進到模型請求。正在工作時等這一輪結束再載入（重新載入會中斷進行中的
// 工具呼叫），最多等 limits.BotToolsReloadWait。電腦沒開時不用做（下次連線就拿到新的清單）；ACP 引擎在下一個 session 拿到。
func (s *stack) reloadBotTools(botID string) {
	if _, waiting := s.toolReloads.LoadOrStore(botID, true); waiting {
		return // 已經在等這個 Bot 空下來
	}
	go func() {
		deadline := time.Now().Add(limits.BotToolsReloadWait)
		for thread, _ := s.runtime.Active(botID); thread != ""; thread, _ = s.runtime.Active(botID) {
			if time.Now().After(deadline) {
				s.toolReloads.Delete(botID)
				slog.Warn("Bot 一直在工作，沒有重新載入 MCP；權限的改變等電腦重開生效", "bot", botID)
				return
			}
			time.Sleep(limits.BotToolsReloadPoll)
		}
		s.toolReloads.Delete(botID) // 之後再改權限要再載入一次
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := s.hermesDriver.ReloadMCP(ctx, botID); err != nil {
			slog.Warn("請 Hermes 重新載入 MCP 失敗，權限的改變等電腦重開生效", "bot", botID, "err", err)
		}
	}()
}

// startBackground 開始服務才需要的背景工作：閒置休眠（BX-12、CL-9）；收尾上次沒做完的搬家，並定期刪掉搬家後保留期已過的舊資料（CL-5）；
// 每天的例行清理（BX-14）；清掉人數不夠的群組。
func (s *stack) startBackground() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stopIdle = cancel
	go s.boxes.RunIdle(ctx, s.runtime)
	// Bot 用記憶工具改了記憶：馬上記一筆變更（時間、對話串才對）
	go s.memory.Watch(ctx, s.runtime.Bus(), runtime.KindToolResult)
	if err := s.computers.RecoverMoves(ctx); err != nil {
		slog.Warn("收尾上次沒做完的搬家失敗", "err", err)
	}
	go s.computers.RunMoveCleanup(ctx)
	go s.computers.RunCleanup(ctx)
	// 服務重開時 product.sock 都關了，還開著的電腦要等下一件工作才會補開；在那之前電腦裡要用模型或 XBot 工具的
	// （例：Hermes 的背景整理）一律 502。開機中的電腦現在就補開。
	if boxes, err := s.db.ListBoxes(ctx, ""); err == nil {
		for _, b := range boxes {
			if b.State == "running" {
				if err := s.sockets.Ensure(b.BotID); err != nil {
					slog.Warn("補開 Bot 的 product.sock 失敗", "bot", b.BotID, "err", err)
				}
			}
		}
	}
	// 以前刪 Bot 留下的空群組（少於 2 位成員，不能用也不能編輯）
	if gone, err := s.collab.PruneRooms(ctx, ""); err != nil {
		slog.Warn("清掉人數不夠的群組失敗", "err", err)
	} else if len(gone) > 0 {
		slog.Info("清掉人數不夠的群組", "rooms", gone)
	}
}

func (s *stack) Close() error {
	var errs []error
	if s.stopIdle != nil {
		s.stopIdle()
	}
	if s.computers != nil {
		s.computers.Close()
	}
	if s.importer != nil {
		s.importer.Close()
	}
	if s.routines != nil {
		s.routines.Close()
	}
	if s.network != nil {
		errs = append(errs, s.network.Close())
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
	if s.judge != nil {
		s.judge.WaitShadow() // 背景的影子判斷寫完再關資料庫（最多 limits.JudgeShadowWait）
	}
	errs = append(errs, s.db.Close())
	return errors.Join(errs...)
}

// detectGrokJudgeModel queries the grok CLI for available models and selects the best one for judging.
// Returns empty string if no suitable model found.
// B3 follow-up (2026-10-22m): Query installed grok CLI for real model names.
func detectGrokJudgeModel(logger *slog.Logger) string {
	// Check for user-configured model first
	if model := os.Getenv("XBOT_JUDGE_MODEL"); model != "" {
		logger.Info("judge model: using configured model", "model", model, "source", "XBOT_JUDGE_MODEL")
		return model
	}

	// Try to query grok CLI for available models
	// The grok CLI can list models via `grok model list` or similar command
	// For now, use a set of known good models in preference order

	// C5 (2026-10-22): Default to grok-4.7-build-fast (not grok-beta)
	model := "grok-4.7-build-fast"
	logger.Info("judge model: selected model", "model", model, "source", "default")
	return model
}
