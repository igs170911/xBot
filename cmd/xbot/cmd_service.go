package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"xbot/internal/backup"
	"xbot/internal/config"
	"xbot/internal/service"
)

func newManager() (*service.Manager, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, err
	}
	p, err := service.DefaultPaths(cfg.DataDir)
	if err != nil {
		return nil, cfg, err
	}
	return &service.Manager{
		Paths:    p,
		Settings: service.Settings{Bind: cfg.Bind, Port: cfg.Port},
		Run:      service.SystemRunner,
		Out:      os.Stdout,
		In:       bufio.NewReader(os.Stdin),
		Yes:      os.Getenv("YES") == "1",
		Healthy:  service.HTTPHealthy,
		Wait:     30 * time.Second,
	}, cfg, nil
}

// cmdService：xbot service install|update|uninstall|start|stop|restart|status|logs
func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：xbot service install|update|uninstall|start|stop|restart|status|logs")
	}
	m, cfg, err := newManager()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("service "+args[0], flag.ContinueOnError)
	bin := fs.String("bin", "", "要安裝的執行檔（預設是目前這個）")
	purge := fs.Bool("purge", false, "uninstall 時一併刪除資料（會再確認）")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	src := *bin
	if src == "" {
		if src, err = os.Executable(); err != nil {
			return err
		}
	}
	switch args[0] {
	case "install":
		if err := m.Install(src); err != nil {
			return err
		}
		printWelcome(m, cfg)
		return nil
	case "update":
		return m.Update(src)
	case "uninstall":
		return m.Uninstall(*purge)
	case "start", "stop", "restart":
		return m.Ctl(args[0])
	case "status":
		return m.Status()
	case "logs":
		cmd := exec.Command("journalctl", "--user", "-u", service.Unit, "-n", "200", "-f", "--no-hostname")
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		return cmd.Run()
	}
	return fmt.Errorf("不認識的 service 指令 %q", args[0])
}

func printWelcome(m *service.Manager, cfg config.Config) {
	tokenPath := filepath.Join(cfg.DataDir, "access-token")
	fmt.Printf("\n🎉 XBot 已安裝並啟動\n   打開：%s\n   其他裝置登入碼：%s（用 make token 顯示）\n   常用：make status｜make logs｜make stop｜make backup\n", m.URL(), tokenPath)
}

// cmdBackup：xbot backup [--out 檔案] [--include-logins]
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "備份檔路徑（預設放在資料目錄旁的 xbot-backups/）")
	logins := fs.Bool("include-logins", false, "也備份 Bot 瀏覽器的登入資料")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(backup.DefaultDir(cfg.DataDir), backup.DefaultName(time.Now()))
	}
	m, err := backup.Create(backup.Options{DataDir: cfg.DataDir, Out: *out, IncludeLogins: *logins})
	if err != nil {
		return err
	}
	note := "不含登入資料（要包含請加 INCLUDE_LOGINS=1）"
	if m.IncludeLogins {
		note = "含登入資料，請妥善保管"
	}
	fmt.Printf("✅ 已備份 %d 個檔案：%s\n   %s\n", m.Files, *out, note)
	return nil
}

// cmdRestore：xbot restore 備份檔。服務在執行的話會先停下、還原後再啟動。
func cmdRestore(args []string) error {
	if len(args) != 1 {
		return errors.New("用法：xbot restore <備份檔>（make restore FILE=…）")
	}
	m, cfg, err := newManager()
	if err != nil {
		return err
	}
	running := false
	if out, _ := m.Run("systemctl", "--user", "is-active", service.Unit); out == "active" {
		running = true
		fmt.Println("服務執行中，先停止…")
		if err := m.Ctl("stop"); err != nil {
			return err
		}
	} else if m.Healthy(m.URL() + "/healthz") {
		return errors.New("XBot 正在執行（不是由 make install 啟動的），請先停止它再還原")
	}
	man, kept, err := backup.Restore(args[0], cfg.DataDir)
	if err != nil {
		return err
	}
	fmt.Printf("✅ 已還原 %d 個檔案（備份時間 %s）到 %s\n", man.Files, man.CreatedAt.Local().Format("2006-01-02 15:04"), cfg.DataDir)
	if kept != "" {
		fmt.Printf("   原本的資料保留在：%s（確認沒問題後可以刪除）\n", kept)
	}
	if running {
		return m.Ctl("start")
	}
	return nil
}

// cmdToken 顯示其他裝置用的登入碼。
func cmdToken([]string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "access-token"))
	if err != nil {
		return fmt.Errorf("還沒有登入碼（服務第一次啟動時會產生）：%w", err)
	}
	fmt.Print(string(b))
	return nil
}
