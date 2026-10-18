package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"xbot/internal/hosts"
)

// deployDir 是遠端主機上放「等著換上去的新版」的地方（家目錄底下）。
const deployDir = ".local/share/xbot-deploy"

// cmdDeploy：make deploy HOST=… 的實際邏輯（PLAN §2，B12）。
//
//  1. 用你自己的 SSH 看那台的 CPU 架構，挑 bin/xbot-linux-<arch>（make build 建好的）；
//  2. 傳到遠端 ~/.local/share/xbot-deploy/xbot.new；
//  3. 遠端已經裝過：用「已安裝的」xbot 執行 service update（新版起不來會自動換回上一版，C06）；
//     還沒裝過：用新版執行 service install；
//  4. 最後用遠端的 xbot service status 做健康檢查；
//  5. Bot 電腦映像：那台沒有或和這台不同時，經 SSH 串流過去（docker save | docker load，V10）。
//
// 可以重複執行；同一版再部署一次只是重新啟動。
func cmdDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	host := fs.String("host", os.Getenv("HOST"), "SSH 目的地：使用者@位址（XBot 服務會裝成這個使用者的 systemd 使用者服務）")
	sshOpts := fs.String("ssh-opts", os.Getenv("SSH_OPTS"), "額外的 ssh 參數，例：\"-p 2222 -i ~/.ssh/id_vps\"")
	binDir := fs.String("bin-dir", "bin", "make build 建出執行檔的資料夾")
	bin := fs.String("bin", "", "直接指定要部署的執行檔（不依架構挑選；測試回滾用）")
	port := fs.String("port", os.Getenv("XBOT_DEPLOY_PORT"), "遠端服務的埠（預設 8780）")
	noImage := fs.Bool("no-image", os.Getenv("NO_IMAGE") == "1", "不傳 Bot 電腦映像")
	imageOnly := fs.Bool("image-only", false, "只傳 Bot 電腦映像（不部署 XBot）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	t, err := newSSHTarget(*host, *sshOpts)
	if err != nil {
		return err
	}

	fmt.Printf("▶ 連到 %s，看 CPU 架構…\n", t.dest)
	uname, err := t.output("uname -m")
	if err != nil {
		return fmt.Errorf("%w\n   確認你自己能 ssh %s，需要時用 SSH_OPTS 指定埠號或金鑰", err, t.dest)
	}
	arch := hosts.NormalizeArch(uname)
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("%s 的架構是 %s，XBot 只有 amd64 與 arm64 版", t.dest, uname)
	}
	if *imageOnly {
		return deployImage(context.Background(), t, arch)
	}
	src := *bin
	if src == "" {
		src = filepath.Join(*binDir, "xbot-linux-"+arch)
	}
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("找不到要部署的執行檔 %s：先執行 make build", src)
	}
	defer f.Close()
	fmt.Printf("   %s → 用 %s\n", arch, src)

	fmt.Println("▶ 傳執行檔…")
	newBin := deployDir + "/xbot.new"
	if err := t.upload(f, newBin, "0755"); err != nil {
		return err
	}

	env := []string{}
	if os.Getenv("YES") == "1" {
		env = append(env, "YES=1")
	}
	if *port != "" {
		env = append(env, "XBOT_PORT="+shellWord(*port))
	}
	// 經 SSH 執行時不一定有使用者服務需要的環境變數（systemctl --user 要 XDG_RUNTIME_DIR 與 session bus）。
	script := strings.Join([]string{
		`export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"`,
		`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"`,
		`export ` + strings.Join(append(env, "XBOT_DEPLOY=1"), " "),
		`new="$HOME/` + newBin + `"`,
		`if [ -x "$HOME/.local/bin/xbot" ]; then`,
		`  echo "▶ 更新已安裝的服務（新版起不來會自動換回上一版）…"`,
		`  "$HOME/.local/bin/xbot" service update --bin "$new"`,
		`else`,
		`  echo "▶ 第一次安裝成服務…"`,
		`  "$new" service install --bin "$new"`,
		`fi`,
	}, "\n")
	if err := t.run("sh -c " + shellWord(script)); err != nil {
		return fmt.Errorf("部署沒有成功（上面是原因；已安裝的舊版會自動換回）：%w", err)
	}

	fmt.Println("▶ 健康檢查…")
	status := `export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"; ` +
		`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"; `
	if *port != "" {
		status += "export XBOT_PORT=" + shellWord(*port) + "; "
	}
	if err := t.run(status + `"$HOME/.local/bin/xbot" service status && "$HOME/.local/bin/xbot" version`); err != nil {
		return errors.New("部署後服務沒有正常回應（make logs 在那台機器上看原因）")
	}
	if !*noImage {
		fmt.Println("▶ Bot 電腦映像…")
		if err := deployImage(context.Background(), t, arch); err != nil {
			fmt.Printf("⚠️  沒有傳 Bot 電腦映像：%v\n", err)
		}
	}
	fmt.Printf("\n✅ 已部署到 %s\n", t.dest)
	return nil
}

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
