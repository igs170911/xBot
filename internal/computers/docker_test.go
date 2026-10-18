//go:build docker

package computers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 真的開一台 Bot 電腦，經操作登錄表走一遍：叫醒、休眠、重開、重建（資料保留）、白名單、「更新電腦」（映像換了以後重建，
// 資料保留）、連資料一起刪。映像用 BOX_IMAGE 的一個暫時標籤，測試中把它換成一個衍生的新映像，模擬 make box。
func TestDockerComputerOps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	base := os.Getenv("BOX_IMAGE")
	if base == "" {
		base = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, base); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", base, err)
	}
	docker := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v：%v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	image := "xbot-test-update:" + strconv.Itoa(os.Getpid())
	docker("", "tag", base, image)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", image).Run() })

	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	bot, err := db.CreateBot(ctx, store.Bot{Name: "電腦操作"})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	sub := bus.Subscribe(nil, 0)
	defer sub.Close()
	// 和其他套件共用本機的網路防護（xbot-netguard）；網路防護測試在跑時先等它做完（見 guardlock）。
	guardlock.Shared(t)
	boxes := box.New(db, hosts.NewRegistry(host), box.Options{Image: image,
		Notify: func(s box.Status) { Publish(db, bus, KindStatus, s.BotID, s, true) }})
	svc := &Service{DB: db, Boxes: boxes, Bus: bus}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = boxes.Remove(ctx, bot.ID, true)
	})
	reg := ops.New(ops.Config{DB: db})
	Register(reg, svc)
	owner := ops.Owner("test")
	call := func(name string, in any, dry bool) View {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, owner, name, raw, ops.Options{DryRun: dry})
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		var v View
		_ = json.Unmarshal(res.Output, &v)
		return v
	}
	name := box.ContainerName(bot.ID)
	write := func() {
		docker("", "exec", "-u", "1000:1000", name, "sh", "-c", "echo 留著 > /workspace/keep.txt")
	}
	kept := func(when string) {
		t.Helper()
		if got := docker("", "exec", name, "cat", "/workspace/keep.txt"); got != "留著" {
			t.Fatalf("%s：資料不見了：%q", when, got)
		}
	}

	v := call("xbot.computers.wake", WakeIn{BotID: bot.ID}, false)
	if !v.Computer.Running || v.Outdated || v.SleepsAt <= v.LastActiveAt || v.IdleMinutes != 30 || v.NetIP == "" || v.HostName == "" {
		t.Fatalf("叫醒：%+v", v)
	}
	write()
	if v = call("xbot.computers.sleep", BotIn{BotID: bot.ID}, false); v.Computer.Running || v.Computer.State != box.StateSleeping {
		t.Fatalf("休眠：%+v", v)
	}
	if v = call("xbot.computers.restart", BotIn{BotID: bot.ID}, false); !v.Computer.Running {
		t.Fatalf("重開：%+v", v)
	}
	kept("重開後")
	old := v.Computer.ContainerID
	if v = call("xbot.computers.rebuild", BotIn{BotID: bot.ID}, false); !v.Computer.Running || v.Computer.ContainerID == old {
		t.Fatalf("重建：%+v", v)
	}
	kept("重建後")
	if v = call("xbot.computers.set_network", NetworkIn{BotID: bot.ID, AllowPrivate: []string{"http://192.168.1.10:8080"}}, false); len(v.AllowPrivate) != 1 || v.AllowPrivate[0] != "192.168.1.10:8080" {
		t.Fatalf("白名單：%+v", v)
	}
	if v = call("xbot.computers.set_network", NetworkIn{BotID: bot.ID, AllowPrivate: []string{}}, false); len(v.AllowPrivate) != 0 {
		t.Fatalf("關掉白名單：%+v", v)
	}

	// make box 換了映像：電腦標成要更新，「更新電腦」重建它，資料保留。
	var plan Progress
	raw, _ := json.Marshal(UpdateIn{})
	res, err := reg.Invoke(ctx, owner, "xbot.computers.update", raw, ops.Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(res.Output, &plan)
	if plan.Total != 0 {
		t.Fatalf("還不用更新：%+v", plan)
	}
	docker("FROM "+base+"\nLABEL xbot.test=update\n", "build", "-q", "-t", image, "-")
	newID, err := cli.ImageInspect(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	if v = call("xbot.computers.status", BotIn{BotID: bot.ID}, false); !v.Outdated {
		t.Fatalf("映像換了要標成要更新：%+v", v)
	}
	res, err = reg.Invoke(ctx, owner, "xbot.computers.update", raw, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(res.Output, &plan)
	if !plan.Running || plan.Total != 1 || len(plan.Hosts) != 1 || plan.Hosts[0].Bots[0].BotID != bot.ID {
		t.Fatalf("開始更新：%+v", plan)
	}
	svc.Wait()
	final, ok := svc.Progress()
	if !ok || final.Running || final.Done != 1 || final.Hosts[0].State != StateDone || final.Hosts[0].Bots[0].State != StateRebuilt {
		t.Fatalf("更新完成：%+v", final)
	}
	got, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil || got.Container.Image != newID.ID || !got.Container.State.Running {
		t.Fatalf("更新後的容器：%v %v（要 %s）", got.Container.Image, err, newID.ID)
	}
	kept("更新電腦後")
	if v = call("xbot.computers.status", BotIn{BotID: bot.ID}, false); v.Outdated {
		t.Fatalf("更新後不用再更新：%+v", v)
	}
	// 進度事件：開始、每一步、最後一則寫進資料庫。
	var updates, statuses int
	for len(sub.C) > 0 {
		e := <-sub.C
		switch e.Kind {
		case KindUpdate:
			updates++
		case KindStatus:
			statuses++
		}
	}
	if updates < 3 || statuses < 3 {
		t.Fatalf("事件：更新進度 %d 則、電腦狀態 %d 則", updates, statuses)
	}

	// 連資料一起刪（你本人直接執行；代理要核准）。
	if v = call("xbot.computers.purge", BotIn{BotID: bot.ID}, false); v.Computer.State != box.StateNone || v.Computer.ContainerID != "" {
		t.Fatalf("purge：%+v", v)
	}
	for _, vol := range []string{"home", "workspace", "hermes", "cred"} {
		if _, err := cli.VolumeInspect(ctx, name+"-"+vol, client.VolumeInspectOptions{}); err == nil {
			t.Fatalf("volume %s 應該刪掉", vol)
		}
	}
}
