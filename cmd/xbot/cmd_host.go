package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"xbot/internal/config"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/opsclient"
	"xbot/internal/service"
)

// xbot host add|check|script：make host-add、make host-check 的實際邏輯（PLAN §2，B12）。
// 安裝那一步用「你自己」連得上那台主機的 SSH（例：root@vps 或有 sudo 的帳號）；之後 XBot 用自己的金鑰以 xbot 帳號連線。

func cmdHost(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(`用法：
  xbot host add   --host 使用者@主機 [--name 名稱] [--max-running N] [--address 位址] [--ssh-opts "…"] [--no-tailscale]
                  在主機上執行安裝腳本（會先問；YES=1 不問），再加入 XBot（等同網頁的 3 步）。可以重複執行。
  xbot host check --host 主機（名稱、位址或 使用者@位址） [--update-image]
                  檢查一台主機的狀態。
  xbot host script [--no-tailscale]
                  印出在主機上貼的一行安裝指令。
`)
		return nil
	}
	switch args[0] {
	case "add":
		return cmdHostAdd(args[1:])
	case "check":
		return cmdHostCheck(args[1:])
	case "script":
		return cmdHostScript(args[1:])
	}
	return fmt.Errorf("不認識的 host 指令 %q（xbot host --help）", args[0])
}

// hostClient 回傳執行中服務的用戶端；服務沒在執行時回傳 nil。
func hostClient() (*opsclient.Client, config.Config, error) {
	base, cfg, err := serviceURL()
	if err != nil {
		return nil, cfg, err
	}
	if !service.HTTPHealthy(base + "/healthz") {
		return nil, cfg, nil
	}
	return &opsclient.Client{BaseURL: base, Token: cliToken(cfg)}, cfg, nil
}

func callOp(ctx context.Context, c *opsclient.Client, name string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	res, err := c.Call(ctx, name, raw, ops.Options{})
	var oe *ops.Error
	if errors.As(err, &oe) {
		return errors.New(oe.Message)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(res.Output, out)
}

func cmdHostScript(args []string) error {
	fs := flag.NewFlagSet("host script", flag.ContinueOnError)
	noTS := fs.Bool("no-tailscale", false, "不要裝 Tailscale")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd, _, err := installScript(context.Background(), !*noTS)
	if err != nil {
		return err
	}
	fmt.Println(cmd)
	return nil
}

// installScript 取得安裝指令與腳本：服務在執行時用服務的金鑰（xbot.hosts.install_script），否則用這台資料目錄裡的金鑰。
func installScript(ctx context.Context, tailscale bool) (cmd, script string, err error) {
	c, cfg, err := hostClient()
	if err != nil {
		return "", "", err
	}
	if c != nil {
		var out hosts.InstallOut
		if err := callOp(ctx, c, "xbot.hosts.install_script", hosts.InstallIn{NoTailscale: !tailscale}, &out); err != nil {
			return "", "", err
		}
		return out.Command, out.Script, nil
	}
	id, err := hosts.EnsureIdentity(cfg.DataDir)
	if err != nil {
		return "", "", err
	}
	return hosts.InstallCommand(id.AuthorizedKey, tailscale), hosts.InstallScript(id.AuthorizedKey), nil
}

// sshTarget 是用你自己的 SSH 連線到主機。
type sshTarget struct {
	dest string   // 使用者@主機
	opts []string // 額外的 ssh 參數（SSH_OPTS）
}

func newSSHTarget(host, opts string) (sshTarget, error) {
	host = strings.TrimSpace(strings.TrimPrefix(host, "ssh://"))
	if host == "" {
		return sshTarget{}, errors.New("要指定主機：HOST=使用者@位址（例：make host-add HOST=root@100.101.7.23）")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return sshTarget{}, errors.New("找不到 ssh 指令（make doctor 會告訴你怎麼裝）")
	}
	return sshTarget{dest: host, opts: strings.Fields(opts)}, nil
}

// addr 是 dest 去掉使用者的部分。
func (t sshTarget) addr() string {
	_, h, ok := strings.Cut(t.dest, "@")
	if !ok {
		return t.dest
	}
	return h
}

func (t sshTarget) command(tty bool, remote string) *exec.Cmd {
	args := append([]string{}, t.opts...)
	args = append(args, "-o", "ConnectTimeout=15")
	if tty {
		args = append(args, "-t")
	}
	args = append(args, t.dest, remote)
	return exec.Command("ssh", args...)
}

// output 在主機上執行一個指令並回傳輸出。
func (t sshTarget) output(remote string) (string, error) {
	cmd := t.command(false, remote)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh %s 失敗：%s", t.dest, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// upload 把資料寫到主機上的檔案。
func (t sshTarget) upload(r io.Reader, remotePath, mode string) error {
	cmd := t.command(false, "mkdir -p \"$(dirname "+remotePath+")\" && cat > "+remotePath+".tmp && chmod "+mode+" "+remotePath+".tmp && mv -f "+remotePath+".tmp "+remotePath)
	cmd.Stdin = r
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("傳檔案到 %s 失敗：%s", t.dest, strings.TrimSpace(string(out)))
	}
	return nil
}

// run 在主機上執行指令，輸出直接顯示；有終端機時配一個 tty（sudo 要問密碼）。
func (t sshTarget) run(remote string) error {
	tty := isTerminal(os.Stdin)
	cmd := t.command(tty, remote)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func confirm(question string) bool {
	if os.Getenv("YES") == "1" {
		return true
	}
	fmt.Print(question + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

func cmdHostAdd(args []string) error {
	fs := flag.NewFlagSet("host add", flag.ContinueOnError)
	host := fs.String("host", os.Getenv("HOST"), "用來安裝的 SSH 目的地：使用者@位址（要有 root 或 sudo）")
	name := fs.String("name", "", "主機名稱（不填用位址）")
	maxRunning := fs.Int("max-running", 0, "同時開機上限（不填是 3）")
	address := fs.String("address", "", "XBot 連這台主機用的位址（不填用 --host 的位址，可帶 :埠號）")
	sshOpts := fs.String("ssh-opts", os.Getenv("SSH_OPTS"), "額外的 ssh 參數，例：\"-p 2222 -i ~/.ssh/id_vps\"")
	noTS := fs.Bool("no-tailscale", false, "不要裝 Tailscale")
	skipInstall := fs.Bool("skip-install", false, "不執行安裝腳本（主機已經準備好）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := newSSHTarget(*host, *sshOpts)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !*skipInstall {
		_, script, err := installScript(ctx, !*noTS)
		if err != nil {
			return err
		}
		fmt.Printf("▶ 檢查能不能用 SSH 連到 %s…\n", t.dest)
		uid, err := t.output("id -u")
		if err != nil {
			return fmt.Errorf("%w\n   確認你自己能 ssh %s（金鑰或密碼），需要時用 SSH_OPTS 指定埠號或金鑰", err, t.dest)
		}
		sudo := "sudo "
		if uid == "0" {
			sudo = ""
		}
		argv := strings.Join(hosts.InstallArgs(!*noTS), " ")
		fmt.Printf(`
要在 %s 上用 root 權限執行 XBot 的主機安裝腳本（可以重複執行，已完成的步驟會略過）：
  1. 安裝 Docker（已經有 %d 版以上就略過）
  2. 安裝 Tailscale 並加入你的 tailnet（--no-tailscale 不裝）
  3. 建 %s 帳號（docker 群組，等於這台的最高權限），只放 XBot 的 SSH 公鑰
  4. 設定 sshd 允許 XBot 的轉發（/etc/ssh/sshd_config.d/10-xbot.conf）
  5. 建 %s
`, t.dest, limits.HostMinDockerMajor, limits.HostSSHUser, limits.SSHRemoteDataDir)
		if !confirm("要繼續嗎？") {
			return errors.New("已取消，主機沒有任何改變")
		}
		const remote = ".xbot-host-install.sh"
		if err := t.upload(strings.NewReader(script), remote, "0600"); err != nil {
			return err
		}
		if err := t.run(sudo + "sh " + remote + " " + argv + "; rc=$?; rm -f " + remote + "; exit $rc"); err != nil {
			return fmt.Errorf("安裝腳本沒有完成（上面是原因），修好後再執行一次 make host-add：%w", err)
		}
	}

	addr := *address
	if addr == "" {
		addr = t.addr()
	}
	target, err := hosts.ParseTarget(addr)
	if err != nil {
		return err
	}
	c, cfg, err := hostClient()
	if err != nil {
		return err
	}
	if c == nil {
		fmt.Printf("\nXBot 服務沒有在執行，先只檢查（不加入清單）。啟動服務（make start）後再執行一次 make host-add 就會加入。\n\n")
		return directCheck(ctx, cfg, target)
	}
	fmt.Printf("\n▶ 加入 XBot 並自動檢查 %s…\n", target.Address)
	var res hosts.AddResult
	if err := callOp(ctx, c, "xbot.hosts.add", hosts.AddIn{Address: addr, Name: *name, MaxRunning: *maxRunning}, &res); err != nil {
		return err
	}
	printReport(res.Report)
	if !res.Report.OK {
		return errors.New("檢查沒過，主機沒有加入；照上面的方法修好後再執行一次")
	}
	if res.Loading {
		if err := waitImage(ctx, c, res.Host.ID); err != nil {
			return err
		}
	}
	var v hosts.View
	if err := callOp(ctx, c, "xbot.hosts.get", hosts.IDIn{ID: res.Host.ID}, &v); err != nil {
		return err
	}
	fmt.Printf("\n✅ 主機「%s」已加入（%s），同時開機上限 %d。在「設定 → 電腦與裝置」可以改名字與上限。\n", v.Name, zhStatus(v.Status), v.MaxRunning)
	return nil
}

// waitImage 等電腦映像傳完，期間印進度。
func waitImage(ctx context.Context, c *opsclient.Client, id string) error {
	fmt.Println("\n▶ 把電腦映像傳到主機（第一次要幾分鐘）…")
	last := -1
	for {
		var v hosts.View
		if err := callOp(ctx, c, "xbot.hosts.get", hosts.IDIn{ID: id}, &v); err != nil {
			return err
		}
		if v.Image != nil && v.Image.Percent != last {
			last = v.Image.Percent
			fmt.Printf("   %d%%（%s / %s）\n", v.Image.Percent, hosts.GB(v.Image.Done), hosts.GB(v.Image.Total))
		}
		switch {
		case v.Image == nil && v.Status == "online":
			fmt.Println("   ✅ 電腦映像已就緒")
			return nil
		case v.Image == nil && v.Status == "failed":
			return fmt.Errorf("電腦映像沒有傳完：%s", v.StatusError)
		}
		select {
		case <-ctx.Done():
			return errors.New("已中斷；映像在服務裡繼續傳，之後用 make host-check 看狀態")
		case <-time.After(time.Second):
		}
	}
}

func zhStatus(s string) string {
	return map[string]string{"online": "在線", "offline": "離線", "updating": "更新中", "failed": "要處理", "unknown": "未知"}[s]
}

func printReport(r hosts.Report) {
	icon := map[string]string{hosts.CheckOK: "✅", hosts.CheckWarn: "⚠️ ", hosts.CheckFail: "❌", hosts.CheckPending: "⏳", hosts.CheckRunning: "⏳"}
	for _, it := range r.Items {
		if it.Status == hosts.CheckPending && it.ID != hosts.CheckImage {
			fmt.Printf("   ・ %-14s （沒有檢查：前面的項目沒過）\n", it.Label)
			continue
		}
		fmt.Printf("   %s %-14s %s\n", icon[it.Status], it.Label, it.Detail)
		if it.Fix != "" && it.Status != hosts.CheckOK {
			fmt.Printf("      → %s\n", it.Fix)
		}
	}
}

// directCheck 服務沒在執行時，直接用這台的金鑰連過去檢查（不記下主機金鑰、不加入清單）。
func directCheck(ctx context.Context, cfg config.Config, t hosts.Target) error {
	id, err := hosts.EnsureIdentity(cfg.DataDir)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "xbot-hostcheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	h, err := hosts.NewSSH(hosts.SSHOptions{ID: "check", Addr: fmt.Sprintf("%s:%d", t.Address, t.Port), User: t.User, Signer: id.Signer,
		KnownHosts:  filepath.Join(tmp, "known_hosts"),
		LocalSocket: func(string) string { return filepath.Join(tmp, ipc.SocketName) }})
	if err != nil {
		return err
	}
	defer h.Close()
	cctx, cancel := context.WithTimeout(ctx, limits.HostCheckTimeout)
	defer cancel()
	img := hosts.ImageRef{Name: boxImage()}
	if local, err := hosts.NewLocalDocker(); err == nil {
		img, _ = hosts.LocalImage(cctx, local, boxImage())
		local.Close()
	}
	r := h.Inspect(cctx, img)
	printReport(r)
	if !r.OK {
		return errors.New("檢查沒過")
	}
	return nil
}

func boxImage() string {
	if v := os.Getenv("XBOT_BOX_IMAGE"); v != "" {
		return v
	}
	return "xbot-box:dev"
}

func cmdHostCheck(args []string) error {
	fs := flag.NewFlagSet("host check", flag.ContinueOnError)
	host := fs.String("host", os.Getenv("HOST"), "主機：名稱、位址、id，或 使用者@位址")
	update := fs.Bool("update-image", false, "主機上的電腦映像和這台不同時傳過去")
	if err := fs.Parse(args); err != nil {
		return err
	}
	want := strings.TrimSpace(*host)
	if want == "" {
		return errors.New("要指定主機：make host-check HOST=名稱或位址")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, cfg, err := hostClient()
	if err != nil {
		return err
	}
	t, err := hosts.ParseTarget(want)
	if err != nil {
		return err
	}
	if c == nil {
		fmt.Printf("XBot 服務沒有在執行，直接連過去檢查 %s@%s…\n", t.User, t.Address)
		return directCheck(ctx, cfg, t)
	}
	var list hosts.ListOut
	if err := callOp(ctx, c, "xbot.hosts.list", hosts.ListIn{}, &list); err != nil {
		return err
	}
	var found *hosts.View
	for i, v := range list.Hosts {
		if v.ID == want || v.Name == want || (strings.EqualFold(v.Address, t.Address) && (v.Kind == "local" || v.Port == t.Port)) {
			found = &list.Hosts[i]
			break
		}
	}
	if found == nil {
		names := []string{}
		for _, v := range list.Hosts {
			names = append(names, v.Name)
		}
		return fmt.Errorf("XBot 的主機清單裡沒有 %q（目前有：%s）。要加入請用 make host-add", want, strings.Join(names, "、"))
	}
	var res hosts.AddResult
	if err := callOp(ctx, c, "xbot.hosts.check", hosts.CheckIn{ID: found.ID, UpdateImage: *update}, &res); err != nil {
		return err
	}
	v := res.Host
	fmt.Printf("主機「%s」：%s", v.Name, zhStatus(v.Status))
	if v.StatusError != "" {
		fmt.Printf("（%s）", v.StatusError)
	}
	limit := "不限"
	if v.MaxRunning > 0 {
		limit = fmt.Sprint(v.MaxRunning)
	}
	fmt.Printf("\n開機 %d／%s · %d 個 Bot\n", v.Running, limit, len(v.Bots))
	printReport(res.Report)
	if res.Loading {
		if err := waitImage(ctx, c, v.ID); err != nil {
			return err
		}
	}
	if !res.Report.OK {
		return errSilent
	}
	return nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
