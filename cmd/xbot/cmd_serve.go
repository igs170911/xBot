package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/config"
	"xbot/internal/hosts"
	"xbot/internal/mcpserver"
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
	db, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, store.FileName))
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("關閉資料庫失敗", "err", err)
		}
	}()
	// 本機一定是一台 Bot 電腦主機（host 表沒有就新增）；Docker 沒開時記成離線，服務照常啟動。
	hostCtx, hostCancel := context.WithTimeout(context.Background(), 10*time.Second)
	local, err := hosts.EnsureLocal(hostCtx, db, cfg.DataDir)
	hostCancel()
	if err != nil {
		return err
	}
	defer local.Close()

	pairing := auth.NewPairing(db)
	reg := buildRegistry(db, pairing)
	localOwner := os.Getenv("XBOT_MCP_LOCAL_OWNER") == "1"
	if localOwner {
		slog.Warn("XBOT_MCP_LOCAL_OWNER=1：這台機器上沒帶權杖的 MCP 連線都會被當成你本人")
	}
	mcpHandler := mcpserver.Handler(reg, mcpserver.HTTPOptions{
		Auth:       &auth.Authenticator{DB: db, AccessToken: accessToken},
		LocalOwner: localOwner,
		Logger:     slog.Default(),
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           api.New(api.Options{Web: web.Dist(), MCP: mcpHandler, AgentPair: pairing.Handler()}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
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
