package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/config"
	"xbot/internal/events"
	"xbot/internal/mcpserver"
	"xbot/internal/push"
	"xbot/internal/store"
	"xbot/internal/version"
	"xbot/web"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("建立資料目錄 %s 失敗：%w", cfg.DataDir, err)
	}
	accessToken, err := auth.EnsureAccessToken(cfg.DataDir)
	if err != nil {
		return err
	}
	st, err := openStack(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("關閉服務元件失敗", "err", err)
		}
	}()

	// 上次留下的 Bot 提問卡：引擎連線換過了，Bot 不會再等那些回覆，收掉。
	if err := st.runtime.Recover(context.Background()); err != nil {
		slog.Warn("收掉舊的提問卡失敗", "err", err)
	}
	// 上次沒做完的工作收掉（等它的 Bot 會收到「做到一半被中斷」），排隊中的照順序開始（AA-10）。
	if err := st.runtime.RecoverTasks(context.Background()); err != nil {
		slog.Warn("接續排隊中的工作失敗", "err", err)
	}
	// 閒置的 Bot 電腦自動休眠（有工作時會自動開機）。
	st.startBackground()
	// 只有一條事件匯流排：runtime（對話串流）與操作登錄表都發布到它，SSE 從它訂閱。
	bus := st.runtime.Bus()
	pairing := auth.NewPairing(st.db)
	notify := func(e store.Event) { bus.Publish(events.FromStore(e)) }
	st.budgets.Notify = notify // 花費上限卡片也即時推給介面
	st.routines.SetNotify(notify)
	st.judge.Notify = notify // 快判到每日上限的通知
	if err := st.judge.Prune(context.Background()); err != nil {
		slog.Warn("清掉太舊的快判紀錄失敗", "err", err)
	}
	// 手機掃 QR 登入（W31、§17）與 Web Push（新的待我處理卡推到手機）。
	devices := auth.NewDevicePairing(st.db, cfg.PublicURL)
	pusher := push.New(st.db)
	reg := buildRegistry(st.db, pairing, notify, st, pwaDeps{devices: devices, push: pusher})
	// Bot 經自己的 product.sock 用 XBot MCP（C11）：身分就是那個 Bot。
	st.sockets.SetProduct(mcpserver.BotHandler(reg, mcpserver.BotOptions{Keys: st.keys, DB: st.db, Logger: slog.Default()}))
	// 權杖驗證 HTTP 與 MCP 共用（登入碼＝你本人，代理權杖＝那個代理）。
	tokens := &auth.Authenticator{DB: st.db, AccessToken: accessToken}
	localOwner := os.Getenv("XBOT_MCP_LOCAL_OWNER") == "1"
	if localOwner {
		slog.Warn("XBOT_MCP_LOCAL_OWNER=1：這台機器上沒帶權杖的 MCP 連線都會被當成你本人")
	}
	mcpHandler := mcpserver.Handler(reg, mcpserver.HTTPOptions{
		Auth:       tokens,
		LocalOwner: localOwner,
		Logger:     slog.Default(),
	})
	handler := api.New(api.Options{
		Web:        web.Dist(),
		ModelProxy: st.model,
		MCP:        mcpHandler,
		AgentPair:  pairing.Handler(),
		DevicePair: devices,
		Hooks:      st.routines.HookHandler(),
		Streams: map[string]api.StreamHandler{
			"POST " + api.Route("xbot.files.upload"): st.files.UploadHandler(reg),
			"GET /api/v1/files.download":             st.files.DownloadHandler(reg),
		},
		Registry: reg,
		Bus:      bus,
		DB:       st.db,
		Auth:     auth.NewHTTP(auth.HTTPConfig{Tokens: tokens, AccessToken: accessToken, LocalNoAuth: cfg.LocalNoAuth}),
	})
	// 關機時先結束 SSE 這類長連線，Shutdown 才不會一直等。
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	srv.RegisterOnShutdown(cancelBase)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 提案到期視同同意、討論時間到叫醒 Bot（X03，LazyBoy agenda tick）。
	go st.org.Run(ctx)
	// 新的待我處理卡推播到訂閱的裝置。
	go pusher.Run(ctx, bus)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	// 定時任務排程器（C32）：關機時先停，避免在關機途中交辦新工作。
	schedCtx, stopSched := context.WithCancel(context.Background())
	schedDone := make(chan struct{})
	go func() { defer close(schedDone); st.routines.Run(schedCtx) }()
	defer func() { stopSched(); <-schedDone }()
	slog.Info("XBot 服務已啟動", "url", cfg.URL(), "version", version.Version, "data", cfg.DataDir)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("服務無法啟動（%s）：%w", cfg.Addr(), err)
	case <-ctx.Done():
	}
	slog.Info("正在關閉服務…")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func cmdVersion([]string) error {
	fmt.Println(version.Version)
	return nil
}
