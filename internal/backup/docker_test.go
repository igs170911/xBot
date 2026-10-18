//go:build docker

package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/hosts/sshtesthost"
	"xbot/internal/store"
)

// 還原整個資料目錄後，這個資料目錄的電腦要停下（掛著改名前的 product.sock 資料夾）；別的 XBot 的電腦不動。
func TestDockerStopUsingDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s：%v", image, err)
	}
	mine, other := t.TempDir(), t.TempDir()
	run := func(name, src string) {
		t.Helper()
		_ = os.MkdirAll(src, 0o755)
		out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--network", "none", "--label", "xbot.managed=true",
			"--label", "xbot.bot="+name, "-v", src+":/product-ipc:ro", "--entrypoint", "sleep", image, "300").CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	}
	suffix := time.Now().Format("150405")
	a, b := "xbot-stoptest-a-"+suffix, "xbot-stoptest-b-"+suffix
	run(a, filepath.Join(mine, "bots", "x", "ipc"))
	run(b, filepath.Join(other, "bots", "y", "ipc"))
	stopped, err := box.StopUsingDir(ctx, cli, mine)
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != a {
		t.Fatalf("只停這個資料目錄的電腦：%v", stopped)
	}
	state := func(n string) string {
		out, _ := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", n).Output()
		return strings.TrimSpace(string(out))
	}
	if state(a) != "false" || state(b) != "true" {
		t.Fatalf("a=%s b=%s", state(a), state(b))
	}
}

// make backup／make restore 連 Bot 電腦的資料一起（V10）：一台在本機、一台在遠端主機（sshtesthost fresh，跑安裝腳本後加入；
// 備份與還原時用資料目錄裡的 SSH 金鑰與 known_hosts 自己連過去）。
// 備份檔裡有 home／workspace／hermes（擁有者與權限照原樣），沒有訂閱憑證、沒有瀏覽器登入；
// 改了資料以後還原：資料回到備份時的樣子，原本的資料保留在 -before-restore- volume。
func TestDockerBackupRestoreComputers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	th := sshtesthost.StartFreshAs(ctx, t, "backup")

	dir, err := os.MkdirTemp("", "xbbackup")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	hsvc := hosts.NewService(hosts.Options{DB: db, Registry: hosts.NewRegistry(local), Local: local, DataDir: dir, Image: image,
		LocalSocket: func(id string) string { return filepath.Join(dir, "bots", id, "ipc", "product.sock") }})
	t.Cleanup(func() { hsvc.Close() })
	if err := hsvc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := hsvc.Identity()
	if err != nil {
		t.Fatal(err)
	}
	install := exec.CommandContext(ctx, "docker", "exec", "-i", th.Container, "sh", "-s", "--", "--no-tailscale")
	install.Stdin = strings.NewReader(hosts.InstallScript(id.AuthorizedKey))
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("安裝腳本：%v\n%s", err, out)
	}
	added, err := hsvc.Add(ctx, hosts.AddRequest{Target: hosts.Target{Address: th.IP(), Port: 22, User: "xbot"}, Name: "cloud-01"})
	if err != nil || !added.Added {
		t.Fatalf("加入主機：%+v %v", added.Report, err)
	}
	remoteID := added.Host.ID
	deadline := time.Now().Add(20 * time.Minute)
	for {
		if row, err := db.GetHost(ctx, remoteID); err == nil && row.Status == store.HostOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("主機沒有上線（映像沒傳完？）")
		}
		time.Sleep(time.Second)
	}
	hsvc.Close() // 之後由 backup 自己連過去

	botL, _ := db.CreateBot(ctx, store.Bot{Name: "本機的", HostID: local.ID()})
	botR, _ := db.CreateBot(ctx, store.Bot{Name: "遠端的", HostID: remoteID})
	for _, b := range []struct{ bot, host string }{{botL.ID, local.ID()}, {botR.ID, remoteID}} {
		if err := db.UpsertBox(ctx, store.Box{BotID: b.bot, HostID: b.host, State: "sleeping"}); err != nil {
			t.Fatal(err)
		}
	}
	// 每台主機上的 docker 指令：本機直接執行，遠端在測試主機裡執行。
	dockerOn := func(remote bool, args ...string) string {
		t.Helper()
		var out []byte
		var err error
		if remote {
			out, err = exec.CommandContext(ctx, "docker", append([]string{"exec", th.Container, "docker"}, args...)...).CombinedOutput()
		} else {
			out, err = exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		}
		if err != nil {
			t.Fatalf("docker %v：%v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	vols := func(botID string) []string {
		v := box.VolumeNames(botID)
		return []string{v.Home, v.Workspace, v.Hermes, v.Cred}
	}
	sh := func(remote bool, botID, script string) string {
		v := box.VolumeNames(botID)
		return dockerOn(remote, "run", "--rm", "--user", "0:0", "--network", "none", "--entrypoint", "sh",
			"-v", v.Home+":/v/home", "-v", v.Workspace+":/v/workspace", "-v", v.Hermes+":/v/hermes", "-v", v.Cred+":/v/cred",
			image, "-c", script)
	}
	seed := `set -e
echo 原本 > /v/home/note.txt && chmod 640 /v/home/note.txt
mkdir -p /v/home/chrome-profile/Default && echo cookie > /v/home/chrome-profile/Default/Cookies
mkdir -p /v/workspace/成果 && echo '# 報告' > /v/workspace/成果/報告.md
echo 'model: x' > /v/hermes/config.yaml
echo 祕密 > /v/cred/token
chown -R 1000:1000 /v/home /v/workspace /v/hermes`
	for _, b := range []struct {
		id     string
		remote bool
	}{{botL.ID, false}, {botR.ID, true}} {
		for _, v := range vols(b.id) {
			dockerOn(b.remote, "volume", "create", "--label", "xbot.managed=true", "--label", "xbot.bot="+b.id, v)
		}
		sh(b.remote, b.id, seed)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "label=xbot.bot="+botL.ID).Output()
		for _, v := range strings.Fields(string(out)) {
			_ = exec.CommandContext(ctx, "docker", "volume", "rm", "-f", v).Run()
		}
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", "xbot-backup-"+botL.ID, "xbot-backup-"+botL.ID+"-keep").Run()
	})

	file := filepath.Join(t.TempDir(), "b.tar.gz")
	comps := &DockerComputers{DataDir: dir, Image: image}
	m, err := CreateContext(ctx, Options{DataDir: dir, Out: file, Computers: comps})
	comps.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Computers {
		if !c.Included || c.Files != 3 {
			t.Fatalf("兩台都要完整備到（home、workspace、hermes 各一個檔案，不含登入）：%+v", m.Computers)
		}
	}
	names := archiveNames(t, file)
	for _, b := range []string{botL.ID, botR.ID} {
		p := ComputersDir + "/" + b + "/"
		h := names[p+"home/note.txt"]
		if h == nil || names[p+"workspace/成果/報告.md"] == nil || names[p+"hermes/config.yaml"] == nil {
			t.Fatalf("%s 的資料不在備份裡：%v", b, keys(names))
		}
		if h.Uid != 1000 || h.Mode&0o777 != 0o640 {
			t.Fatalf("擁有者與權限要照原樣：uid %d mode %o", h.Uid, h.Mode)
		}
	}
	for n := range names {
		if strings.Contains(n, "chrome-profile") || strings.Contains(n, "cred") || strings.Contains(n, "token") {
			t.Fatalf("不該備份登入資料或訂閱憑證：%s", n)
		}
	}

	// 備份之後資料改了；還原回備份時的樣子。
	for _, remote := range []bool{false, true} {
		b := botL.ID
		if remote {
			b = botR.ID
		}
		sh(remote, b, `echo 改過 > /v/home/note.txt && echo 新檔 > /v/workspace/new.txt`)
	}
	comps = &DockerComputers{DataDir: dir, Image: image}
	res, err := RestoreComputers(ctx, file, dir, comps, nil)
	comps.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, r := range res {
		if !r.Restored || len(r.Kept) != 3 {
			t.Fatalf("要還原並保留原本的三個資料區：%+v", r)
		}
		remote := r.BotID == botR.ID
		got := sh(remote, r.BotID, `cat /v/home/note.txt; stat -c '%u %a' /v/home/note.txt; test ! -e /v/workspace/new.txt && echo 沒有新檔; `+
			`cat /v/workspace/成果/報告.md; test -e /v/cred/token && echo 憑證還在; test ! -e /v/home/chrome-profile && echo 沒有登入資料`)
		for _, want := range []string{"原本", "1000 640", "沒有新檔", "# 報告", "憑證還在", "沒有登入資料"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s 還原後的內容不對，缺 %q：\n%s", r.BotName, want, got)
			}
		}
		kept := dockerOn(remote, "run", "--rm", "--user", "0:0", "--network", "none", "--entrypoint", "sh", "-v", r.Kept[0]+":/k", image,
			"-c", "cat /k/note.txt; ls /k/chrome-profile/Default")
		if !strings.Contains(kept, "改過") || !strings.Contains(kept, "Cookies") {
			t.Fatalf("原本的資料要完整保留：%s", kept)
		}
	}
}
