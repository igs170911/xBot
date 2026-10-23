package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"xbot/internal/backup"
	"xbot/internal/backup/dockervol"
	"xbot/internal/box"
	"xbot/internal/config"
	"xbot/internal/hosts"
	"xbot/internal/service"
)

// installedConfig 讀設定：已安裝的服務檔裡的 XBot 變數（資料目錄、埠號…）先補進環境（這次有明確設的優先），
// 讓 service／backup／restore／token 看到的和服務一樣。
func installedConfig() (config.Config, []service.EnvVar, error) {
	p, err := service.DefaultPaths("")
	if err != nil {
		return config.Config{}, nil, err
	}
	unit := service.ReadUnitEnv(p.UnitPath())
	service.ApplyUnitEnv(p.UnitPath())
	cfg, err := config.Load()
	return cfg, unit, err
}

func newManager() (*service.Manager, config.Config, error) {
	cfg, unit, err := installedConfig()
	if err != nil {
		return nil, cfg, err
	}
	p, err := service.DefaultPaths(cfg.DataDir)
	if err != nil {
		return nil, cfg, err
	}
	return &service.Manager{
		Paths:    p,
		Settings: service.Settings{Bind: cfg.Bind, Port: cfg.Port, Extra: service.MergeEnv(unit, os.LookupEnv)},
		Run:      service.SystemRunner,
		Out:      os.Stdout,
		In:       bufio.NewReader(os.Stdin),
		Yes:      os.Getenv("YES") == "1",
		Healthy:  service.HTTPHealthy,
		Wait:     30 * time.Second,
		// 更新前的自動備份：只有資料目錄（含資料庫快照），不含 Bot 電腦的資料（那個要 make backup）。
		Snapshot: func() (string, error) {
			out := filepath.Join(backup.DefaultDir(cfg.DataDir), "xbot-backup-"+time.Now().Format("20060102-150405")+"-before-update.tar.gz")
			_, err := backup.Create(backup.Options{DataDir: cfg.DataDir, Out: out})
			return out, err
		},
		RestoreDB: func(snap string) (string, error) { return backup.RestoreDB(snap, cfg.DataDir) },
	}, cfg, nil
}

// cmdService：xbot service install|update|update-computers|sleep-computers|purge-computers|uninstall|start|stop|restart|status|logs
func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：xbot service install|update|update-computers|sleep-computers|purge-computers|uninstall|start|stop|restart|status|logs")
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
	case "update-computers":
		return updateComputers(m, cfg)
	case "sleep-computers":
		return sleepComputers(m, cfg)
	case "purge-computers":
		return purgeComputers(m, cfg)
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
	fmt.Printf("\n🎉 XBot 已安裝並啟動\n   打開：%s\n", m.URL())
	if cfg.Bind == "0.0.0.0" || cfg.Bind == "::" || cfg.Bind == "" {
		for _, ip := range lanIPs() {
			fmt.Printf("   區網其他裝置：http://%s:%d\n", ip, cfg.Port)
		}
	}
	fmt.Printf("   其他裝置登入碼：%s（用 make token 顯示）\n   常用：make status｜make logs｜make stop｜make backup\n", tokenPath)
}

// lanIPs 列出這台機器的區網 IPv4，給區網其他裝置連線用。
// 排除 loopback、link-local，以及 Docker／虛擬網路介面（docker0、br-*、veth*、xbot-box0、virbr*、podman*）的位址。
func lanIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() || !n.IP.IsPrivate() {
				continue
			}
			out = append(out, n.IP.String())
		}
	}
	return out
}

func virtualIface(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "xbot-box", "virbr", "podman", "cni", "flannel", "tailscale"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// cmdBackup：xbot backup [--out 檔案] [--include-logins] [--no-computers]
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "", "備份檔路徑（預設放在資料目錄旁的 xbot-backups/）")
	logins := fs.Bool("include-logins", false, "也備份 Bot 瀏覽器的登入資料")
	noComputers := fs.Bool("no-computers", false, "只備份資料目錄，不含 Bot 電腦的資料（家目錄、工作資料夾、Hermes）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, err := installedConfig()
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(backup.DefaultDir(cfg.DataDir), backup.DefaultName(time.Now()))
	}
	opts := backup.Options{DataDir: cfg.DataDir, Out: *out, IncludeLogins: *logins,
		Report: func(f string, a ...any) { fmt.Printf(f+"\n", a...) }}
	if !*noComputers {
		comps := &dockervol.Computers{DataDir: cfg.DataDir, Image: boxImage()}
		defer comps.Close()
		opts.Computers = comps
		fmt.Println("▶ 備份資料目錄與每個 Bot 電腦的資料…")
	}
	m, err := backup.Create(opts)
	if err != nil {
		return err
	}
	inc, skipped := 0, 0
	for _, c := range m.Computers {
		if c.Included {
			inc++
		} else if c.Skipped != "" {
			skipped++
		}
	}
	note := "不含瀏覽器登入資料（要包含請加 INCLUDE_LOGINS=1）"
	if m.IncludeLogins {
		note = "含瀏覽器登入資料，請妥善保管"
	}
	fmt.Printf("✅ 已備份 %d 個檔案", m.Files)
	if !*noComputers {
		fmt.Printf("和 %d 台 Bot 電腦的資料", inc)
	}
	fmt.Printf("：%s\n   %s；訂閱登入憑證不備份（開機時會重新寫入）\n", *out, note)
	if skipped > 0 {
		fmt.Printf("⚠️  有 %d 台 Bot 電腦的資料沒有備到（原因見上面），主機恢復後再備份一次\n", skipped)
		return errSilent
	}
	return nil
}

// cmdRestore：xbot restore [--computers-only] [--no-computers] [--bot id] 備份檔。服務在執行的話會先停下、還原後再啟動。
func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	computersOnly := fs.Bool("computers-only", false, "只還原 Bot 電腦的資料（資料目錄不動；例：當時主機不在線上，之後補還原）")
	noComputers := fs.Bool("no-computers", false, "只還原資料目錄，不動 Bot 電腦的資料")
	bot := fs.String("bot", "", "只還原這些 Bot 的電腦資料（Bot id，逗號分隔）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || (*computersOnly && *noComputers) {
		return errors.New("用法：xbot restore [--computers-only|--no-computers] [--bot id,…] <備份檔>（make restore FILE=…）")
	}
	file := fs.Arg(0)
	man0, err := backup.ReadManifest(file)
	if err != nil {
		return err
	}
	if man0.Format > backup.FormatVersion {
		return fmt.Errorf("這份備份是較新版的 XBot（%s）做的，請先更新 XBot 再還原", man0.Version)
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
	var failed error
	if !*computersOnly {
		man, kept, err := backup.Restore(file, cfg.DataDir)
		if err != nil {
			return err
		}
		fmt.Printf("✅ 已還原 %d 個檔案（備份時間 %s）到 %s\n", man.Files, man.CreatedAt.Local().Format("2006-01-02 15:04"), cfg.DataDir)
		if kept != "" {
			fmt.Printf("   原本的資料保留在：%s（確認沒問題後可以刪除）\n", kept)
			stopLocalComputers(cfg.DataDir)
		}
	}
	if !*noComputers {
		failed = restoreComputers(file, cfg, splitList(*bot))
	}
	if running {
		if err := m.Ctl("start"); err != nil {
			return err
		}
	}
	return failed
}

// stopLocalComputers 停掉這台上還開著的 Bot 電腦：它們掛的是改名保留的舊資料目錄裡的 product.sock，下次用到時會重新開機。
func stopLocalComputers(dataDir string) {
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		return
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stopped, err := box.StopUsingDir(ctx, cli, dataDir)
	if len(stopped) > 0 {
		fmt.Printf("   已讓這台上開著的 %d 台 Bot 電腦休眠（下次有工作時自動開機，接上還原後的資料）\n", len(stopped))
	}
	if err != nil {
		fmt.Printf("⚠️  讓 Bot 電腦休眠失敗：%v（請在網頁上讓它們休眠再叫醒）\n", err)
	}
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// restoreComputers 把備份檔裡每個 Bot 電腦的資料寫回它的主機；主機不在線上的說明怎麼之後補還原。
func restoreComputers(file string, cfg config.Config, only []string) error {
	man, err := backup.ReadManifest(file)
	if err != nil {
		return err
	}
	if man.Format < 2 {
		fmt.Println("   （這份備份是舊格式，沒有 Bot 電腦的資料）")
		return nil
	}
	fmt.Println("▶ 還原 Bot 電腦的資料…")
	comps := &dockervol.Computers{DataDir: cfg.DataDir, Image: boxImage()}
	defer comps.Close()
	res, err := backup.RestoreComputers(context.Background(), file, cfg.DataDir, comps, only)
	if err != nil {
		return err
	}
	var later []string
	for _, r := range res {
		if r.Restored {
			fmt.Printf("   💻 %s（%s）：已還原 %d 個檔案；下次開機時重建電腦\n", r.BotName, r.HostName, r.Files)
			if len(r.Kept) > 0 {
				fmt.Printf("      原本的資料保留在 volume：%s（確認沒問題後可以 docker volume rm）\n", strings.Join(r.Kept, " "))
			}
			continue
		}
		fmt.Printf("   ⚠️  %s（%s）沒有還原：%s\n", r.BotName, r.HostName, r.Error)
		later = append(later, r.BotID)
	}
	if len(res) == 0 {
		fmt.Println("   （備份裡沒有 Bot 電腦的資料）")
	}
	if len(later) > 0 {
		fmt.Printf("   資料還在備份檔裡。主機恢復後補還原：\n   xbot restore --computers-only --bot %s %s\n", strings.Join(later, ","), file)
		return errSilent
	}
	return nil
}

// cmdToken 顯示其他裝置用的登入碼。
func cmdToken([]string) error {
	cfg, _, err := installedConfig()
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
