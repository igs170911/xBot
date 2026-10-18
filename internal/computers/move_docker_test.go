//go:build docker

package computers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/hosts/sshtesthost"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 搬家（CL-5，B31）的整合測試：一台「剛租來的主機」（sshtesthost fresh，跑安裡腳本後加入）。
// 這台電腦 → 遠端：資料、權限、瀏覽器登入（chrome-profile）都在，遠端開機；原主機的資料保留、留下標記。
// 遠端 → 這台中途取消：Bot 留在遠端、資料不動、電腦開回來，這台沒有留下半套資料。
// 遠端 → 這台：再搬回來，資料一樣；保留期過了以後遠端的舊資料被刪掉。
func TestDockerMoveLocalRemoteAndBack(t *testing.T) {
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
	th := sshtesthost.StartFresh(ctx, t)

	dir, err := os.MkdirTemp("", "xbmove")
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
	bus := events.New()
	registry := hosts.NewRegistry(local)
	hsvc := hosts.NewService(hosts.Options{DB: db, Registry: registry, Local: local, DataDir: dir, Image: image, Bus: bus,
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
	added, err := hsvc.Add(ctx, hosts.AddRequest{Target: hosts.Target{Address: th.IP(), Port: 22, User: "xbot"}, Name: "cloud-01", MaxRunning: 2})
	if err != nil || !added.Added {
		t.Fatalf("加入主機：%+v %v", added.Report, err)
	}
	remoteID, localID := added.Host.ID, local.ID()
	waitOnline(ctx, t, db, remoteID)

	boxes := box.New(db, registry, box.Options{Image: image})
	svc := &Service{DB: db, Boxes: boxes, Hosts: hsvc, Bus: bus}
	t.Cleanup(svc.Close)
	reg := ops.New(ops.Config{DB: db})
	Register(reg, svc)
	owner := ops.Owner("test")
	invoke := func(name string, in any) MoveStatus {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, owner, name, raw, ops.Options{})
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		var st MoveStatus
		_ = json.Unmarshal(res.Output, &st)
		return st
	}

	bot, err := db.CreateBot(ctx, store.Bot{Name: "阿凱", HostID: localID})
	if err != nil {
		t.Fatal(err)
	}
	name := box.ContainerName(bot.ID)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = boxes.DropMoved(ctx, localID, bot.ID)
		_ = boxes.DropMoved(ctx, remoteID, bot.ID)
		_, _ = boxes.Remove(ctx, bot.ID, true)
	})
	if _, err := boxes.Start(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	// 一些資料：工作檔、瀏覽器登入（0600）、符號連結、中文檔名、一個大檔（看得到進度）、hermes 的狀態。
	seed := `set -e
mkdir -p /home/box/chrome-profile /workspace/報告 /hermes/sessions
echo 'SID=logged-in' > /home/box/chrome-profile/xbot-test-login
chmod 600 /home/box/chrome-profile/xbot-test-login
echo 留著 > /workspace/keep.txt
echo 中文 > '/workspace/報告/第 1 版.txt'
ln -s /workspace/keep.txt /workspace/link
head -c 200000000 /dev/urandom > /workspace/big.bin
echo '{"id":1}' > /hermes/sessions/xbot-test.json`
	dockerOut(ctx, t, "exec", "-u", "1000:1000", name, "sh", "-c", seed)
	// 只看自己放的檔案（Chromium、Hermes 開機後會改它們自己的檔案）：擁有者、權限、大小、內容。
	files := `home/box/chrome-profile/xbot-test-login workspace/keep.txt workspace/big.bin 'workspace/報告/第 1 版.txt' hermes/sessions/xbot-test.json`
	fingerprint := `cd / && stat -c '%n %u:%g %a %s %F' ` + files + ` workspace/link && sha256sum ` + files
	want := dockerOut(ctx, t, "exec", name, "sh", "-c", fingerprint)

	// ① 這台 → 遠端。
	st := invoke("xbot.computers.move", MoveIn{BotID: bot.ID, HostID: remoteID})
	if !st.Running || len(st.Jobs) == 0 || st.Jobs[0].BotID != bot.ID {
		t.Fatalf("開始搬家：%+v", st)
	}
	job := waitMove(ctx, t, svc, bot.ID)
	if job.State != MoveDone || job.Files < 5 || job.Total < 200000000 || job.KeepUntil == 0 || job.Note != "" {
		t.Fatalf("搬到遠端：%+v", job)
	}
	if b, _ := db.GetBox(ctx, bot.ID); b.HostID != remoteID || b.State != box.StateRunning {
		t.Fatalf("電腦應該在遠端開著：%+v", b)
	}
	if b, _ := db.GetBot(ctx, bot.ID); b.HostID != remoteID {
		t.Fatalf("bot.host_id：%s", b.HostID)
	}
	if got := remoteExec(ctx, t, th, "exec", name, "sh", "-c", fingerprint); got != want {
		t.Fatalf("遠端的資料不一樣：\n%s\n---\n%s", got, want)
	}
	if _, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("原主機的容器要刪掉")
	}
	for _, v := range []string{"home", "workspace", "hermes"} {
		if _, err := cli.VolumeInspect(ctx, name+"-"+v, client.VolumeInspectOptions{}); err != nil {
			t.Fatalf("原主機的 %s 要保留 24 小時：%v", v, err)
		}
	}
	marker, err := cli.VolumeInspect(ctx, box.MovedMarker(bot.ID), client.VolumeInspectOptions{})
	if err != nil || marker.Volume.Labels[box.LabelMovedTo] != remoteID {
		t.Fatalf("原主機的標記：%+v %v", marker.Volume.Labels, err)
	}
	if out := dockerOut(ctx, t, "ps", "-aq", "--filter", "name=xbot-move-"+bot.ID); out != "" {
		t.Fatalf("輔助容器要清掉：%s", out)
	}

	// ② 遠端 → 這台，傳到一半取消：留在遠端，資料不動，電腦開回來；這台沒有留下半套資料。
	invoke("xbot.computers.move", MoveIn{BotID: bot.ID, HostID: localID})
	deadline := time.Now().Add(5 * time.Minute)
	for {
		s, _ := svc.MoveStatus(ctx, bot.ID)
		if len(s.Jobs) > 0 && s.Jobs[0].State == MoveRunning && s.Jobs[0].Phase == box.PhaseCopy {
			break
		}
		if len(s.Jobs) > 0 && !s.Jobs[0].Active() || time.Now().After(deadline) {
			t.Fatalf("等不到傳資料：%+v", s.Jobs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	invoke("xbot.computers.move_cancel", BotIn{BotID: bot.ID})
	job = waitMove(ctx, t, svc, bot.ID)
	if job.State != MoveCanceled {
		t.Fatalf("取消：%+v", job)
	}
	if b, _ := db.GetBox(ctx, bot.ID); b.HostID != remoteID {
		t.Fatalf("取消後留在遠端：%+v", b)
	}
	waitRunning(ctx, t, boxes, bot.ID)
	if got := remoteExec(ctx, t, th, "exec", name, "sh", "-c", fingerprint); got != want {
		t.Fatalf("取消後遠端的資料不一樣：\n%s", got)
	}
	for _, v := range []string{"home", "workspace", "hermes", "cred"} {
		if _, err := cli.VolumeInspect(ctx, name+"-"+v, client.VolumeInspectOptions{}); err == nil {
			t.Fatalf("取消後這台不能留下 %s", v)
		}
	}

	// ③ 遠端 → 這台（電腦關著搬）。
	if _, err := boxes.Stop(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	invoke("xbot.computers.move", MoveIn{BotID: bot.ID, HostID: localID})
	job = waitMove(ctx, t, svc, bot.ID)
	if job.State != MoveDone {
		t.Fatalf("搬回來：%+v", job)
	}
	b, _ := db.GetBox(ctx, bot.ID)
	if b.HostID != localID || b.State != box.StateSleeping || b.ContainerID == "" {
		t.Fatalf("搬回來（原本關著：建好容器但不開機）：%+v", b)
	}
	if _, err := boxes.Start(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	if got := dockerOut(ctx, t, "exec", name, "sh", "-c", fingerprint); got != want {
		t.Fatalf("搬回來的資料不一樣：\n%s", got)
	}
	if out := remoteExec(ctx, t, th, "volume", "ls", "-q", "--filter", "label="+box.LabelMovedTo); !strings.Contains(out, box.MovedMarker(bot.ID)) {
		t.Fatalf("遠端的標記：%q", out)
	}
	// 保留期過了：刪掉遠端的舊資料；這台現在的資料不能動。
	n, err := svc.CleanupMoves(ctx, time.Now().Add(25*time.Hour))
	if err != nil || n < 1 {
		t.Fatalf("刪舊資料：%d %v", n, err)
	}
	if out := remoteExec(ctx, t, th, "volume", "ls", "-q", "--filter", "name="+name); out != "" {
		t.Fatalf("遠端的舊資料要刪掉：%q", out)
	}
	if got := dockerOut(ctx, t, "exec", name, "cat", "/workspace/keep.txt"); got != "留著" {
		t.Fatalf("這台的資料不能動：%q", got)
	}
}

func dockerOut(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v：%v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// remoteExec 在遠端主機（測試主機容器裡的 Docker）執行 docker 指令。
func remoteExec(ctx context.Context, t *testing.T, th *sshtesthost.Host, args ...string) string {
	t.Helper()
	out, err := th.Exec(ctx, append([]string{"docker"}, args...)...)
	if err != nil {
		t.Fatalf("遠端 docker %v：%v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func waitOnline(ctx context.Context, t *testing.T, db *store.DB, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if row, err := db.GetHost(ctx, id); err == nil && row.Status == store.HostOnline {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("主機沒有上線")
}

func waitMove(ctx context.Context, t *testing.T, svc *Service, botID string) MoveJob {
	t.Helper()
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := svc.MoveStatus(ctx, botID)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Jobs) > 0 && !st.Jobs[0].Active() {
			return st.Jobs[0]
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("搬家沒有結束")
	return MoveJob{}
}

func waitRunning(ctx context.Context, t *testing.T, boxes *box.Manager, botID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if s, err := boxes.Status(ctx, botID); err == nil && s.Running {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("電腦沒有開回來")
}
