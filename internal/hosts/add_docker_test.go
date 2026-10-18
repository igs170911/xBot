//go:build docker

// B12 主機管理的整合測試：一台「剛租來的主機」容器（Debian 13＋sshd 預設設定＋Docker 26，沒有 xbot 帳號），
// 以 root 跑兩次安裝腳本（等同使用者貼那一行指令；第二次要不出錯）→ xbot.hosts.add 的邏輯（連線、記下主機金鑰、
// 自動檢查、把電腦映像經 SSH 傳過去）→ 在那台開 Bot 的電腦並對話 → 映像換了（make box）：主機標成過期，「更新電腦」把新映像
// 傳過去並重建上面的電腦（B13）→ 主機離線（工作排隊）→ 回來（自動重連、排隊的工作開始）→ 移除。
// 需要 Docker 與 make box 的映像；沒有時略過。
package hosts_test

import (
	"bytes"
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
	"xbot/internal/computers"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/hosts/sshtesthost"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

func TestHostInstallAddAndRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, boxImage()); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", boxImage(), err)
	}
	th := sshtesthost.StartFresh(ctx, t)
	// 這個測試自己的映像標籤：中途要把它換成新的映像（模擬 make box），不能動到其他測試用的那個。
	image := "xbot-test-hostupd:" + strconv.Itoa(os.Getpid())
	if out, err := exec.CommandContext(ctx, "docker", "tag", boxImage(), image).CombinedOutput(); err != nil {
		t.Fatalf("標籤：%v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", image).Run() })

	dir, err := os.MkdirTemp("", "xbadd")
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

	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") },
		Model: proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore}), Keys: keys})
	t.Cleanup(func() { sockets.Shutdown() })
	bus := events.New()
	registry := hosts.NewRegistry(local)
	svc := hosts.NewService(hosts.Options{DB: db, Registry: registry, Local: local, DataDir: dir, Image: image, LocalSocket: sockets.Path, Bus: bus})
	t.Cleanup(func() { svc.Close() })
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// ① 安裝腳本（以 root，像貼那一行指令）；跑兩次都要成功（可以重複執行）。
	id, err := svc.Identity()
	if err != nil {
		t.Fatal(err)
	}
	script := hosts.InstallScript(id.AuthorizedKey)
	for i := 1; i <= 2; i++ {
		start := time.Now()
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", th.Container, "sh", "-s", "--", "--no-tailscale")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("第 %d 次安裝腳本失敗：%v\n%s", i, err, out)
		}
		t.Logf("第 %d 次安裝腳本（%s）：\n%s", i, time.Since(start).Round(time.Millisecond), out)
	}
	if out, _ := th.Exec(ctx, "grep", "-c", "ssh-ed25519", "/home/xbot/.ssh/authorized_keys"); strings.TrimSpace(out) != "1" {
		t.Fatalf("公鑰應該只有一行：%q", out)
	}
	// 也試一次貼上的那一行（base64＋gzip）：跑得起來、而且還是不出錯。
	line := hosts.InstallCommand(id.AuthorizedKey, false)
	if out, err := th.Shell(ctx, strings.Replace(line, "| sudo sh", "| sh", 1)); err != nil {
		t.Fatalf("一行指令：%v\n%s", err, out)
	}

	// ② 加入：自動檢查全部通過，映像經 SSH 傳過去（快取 volume 裡已有同一版時跳過）。
	sub := bus.Subscribe(nil, 4096)
	defer sub.Close()
	start := time.Now()
	res, err := svc.Add(ctx, hosts.AddRequest{Target: hosts.Target{Address: th.IP(), Port: 22, User: "xbot"}, Name: "cloud-test", MaxRunning: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Report.OK || !res.Added {
		b, _ := json.MarshalIndent(res.Report, "", "  ")
		t.Fatalf("檢查沒過：\n%s", b)
	}
	for _, it := range res.Report.Items {
		if it.Status != hosts.CheckOK && !(it.ID == hosts.CheckImage && it.Status == hosts.CheckRunning) && it.ID != hosts.CheckNetwork {
			t.Fatalf("檢查 %s：%+v", it.ID, it)
		}
		t.Logf("檢查 %-10s %-7s %s", it.ID, it.Status, it.Detail)
	}
	hostID := res.Host.ID
	waitHost(ctx, t, db, hostID, store.HostOnline, 20*time.Minute)
	t.Logf("加入主機（含傳映像=%v）：%s", res.Loading, time.Since(start).Round(time.Second))
	if res.Loading {
		got := false
		for len(sub.C) > 0 {
			if (<-sub.C).Kind == hosts.KindHostImage {
				got = true
			}
		}
		if !got {
			t.Fatal("傳映像時應該有 host.image 進度事件")
		}
	}
	if kh, err := os.ReadFile(filepath.Join(dir, "hosts", hostID, "known_hosts")); err != nil || !bytes.Contains(kh, []byte(th.IP())) {
		t.Fatalf("主機金鑰沒有記下：%v", err)
	}
	// 同一台再加一次：不重複新增、映像已是最新。
	again, err := svc.Add(ctx, hosts.AddRequest{Target: hosts.Target{Address: th.IP(), Port: 22, User: "xbot"}})
	if err != nil || again.Host.ID != hostID || again.Loading || !again.Report.OK {
		t.Fatalf("重複加入：%+v %v", again, err)
	}

	// 映像傳輸本身：每次都用一個小映像實際走一次 docker save → SSH → docker load。
	testLoadTinyImage(ctx, t, cli, registry, hostID, th)

	// ③ 在這台開 Bot 的電腦並對話（product.sock 經反向轉發回到本機的 Model Proxy）。
	fake := &hermestest.FakeModel{}
	if err := providers.Put(ctx, proxy.Provider{ID: "openai", BaseURL: newHTTPServer(t, fake) + "/v1", APIKey: proxy.Secret("sk-FAKE-b12")}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, runtime.DefaultModelSetting, runtime.ModelRef{Provider: "openai", Model: "fake-model"}); err != nil {
		t.Fatal(err)
	}
	comp := &runtime.Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore, Hosts: registry}
	boxes := box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: comp,
		Admit: func(ctx context.Context, botID string, wait func(any)) (func(), error) {
			return svc.Admit(ctx, botID, func(w hosts.Wait) { wait(w) })
		}})
	newBot := func(name string) store.Bot {
		b, err := db.CreateBot(ctx, store.Bot{Name: name, Engine: hermes.Name, HostID: hostID})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			drv.Close(b.ID)
			_, _ = boxes.Remove(ctx, b.ID, true)
		})
		return b
	}
	bot := newBot("遠端小編")
	chat := func(botID, text string) store.Task {
		t.Helper()
		cctx, ccancel := context.WithTimeout(ctx, 4*time.Minute)
		defer ccancel()
		r, err := rt.Chat(cctx, botID, "", text)
		if err != nil {
			t.Fatal(err)
		}
		task, err := rt.Wait(cctx, r.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	if task := chat(bot.ID, "回覆 OK"); task.Verdict != runtime.VerdictAnswer {
		out, _ := th.Exec(ctx, "docker", "logs", "--tail", "40", box.ContainerName(bot.ID))
		t.Fatalf("在新主機上對話：%+v\n%s", task, out)
	}
	if out, err := th.Exec(ctx, "docker", "inspect", "-f", "{{.State.Running}}", box.ContainerName(bot.ID)); err != nil || out != "true" {
		t.Fatalf("Bot 的電腦應該在新主機上跑：%q %v", out, err)
	}
	v, _ := svc.Get(ctx, hostID)
	if v.Running != 1 || len(v.Bots) != 1 || v.ImageOutdated {
		t.Fatalf("主機清單：開機 %d、Bot %d、映像過期 %v", v.Running, len(v.Bots), v.ImageOutdated)
	}

	// ③-1 映像換了（make box）：主機標成映像過期；「更新電腦」把新映像傳過去，再重建上面的電腦（資料保留）。
	if _, err := th.Exec(ctx, "docker", "exec", "-u", "1000:1000", box.ContainerName(bot.ID), "sh", "-c", "echo 留著 > /workspace/keep.txt"); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "docker", "build", "-q", "-t", image, "-")
	build.Stdin = strings.NewReader("FROM " + boxImage() + "\nLABEL xbot.test=host-update\n")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("新映像：%v %s", err, out)
	}
	deadline := time.Now().Add(30 * time.Second)
	for v, _ = svc.Get(ctx, hostID); !v.ImageOutdated; v, _ = svc.Get(ctx, hostID) {
		if time.Now().After(deadline) {
			t.Fatal("make box 之後主機應該標成映像過期")
		}
		time.Sleep(time.Second)
	}
	upd := &computers.Service{DB: db, Boxes: boxes, Hosts: svc, Activity: rt, Bus: bus}
	start = time.Now()
	plan, err := upd.Update(ctx, hostID)
	if err != nil || plan.Total != 1 || !plan.Hosts[0].ImageOutdated {
		t.Fatalf("更新電腦：%+v %v", plan, err)
	}
	upd.Wait()
	final, _ := upd.Progress()
	if final.Hosts[0].State != computers.StateDone || final.Hosts[0].Bots[0].State != computers.StateRebuilt {
		t.Fatalf("更新電腦的結果：%+v", final)
	}
	t.Logf("更新電腦（傳新映像＋重建）：%s", time.Since(start).Round(time.Second))
	if v, _ = svc.Get(ctx, hostID); v.ImageOutdated {
		t.Fatal("更新後主機的映像應該是最新的")
	}
	if out, _ := th.Exec(ctx, "docker", "inspect", "-f", "{{index .Config.Labels \"xbot.test\"}} {{.State.Running}}", box.ContainerName(bot.ID)); out != "host-update true" {
		t.Fatalf("遠端的電腦應該用新映像重建、開著：%q", out)
	}
	if out, _ := th.Exec(ctx, "docker", "exec", box.ContainerName(bot.ID), "cat", "/workspace/keep.txt"); out != "留著" {
		t.Fatalf("更新後資料不見了：%q", out)
	}
	if task := chat(bot.ID, "回覆 OK"); task.Verdict != runtime.VerdictAnswer {
		t.Fatalf("更新後對話：%+v", task)
	}

	// 上限 1 台：第二個 Bot 要排隊「等電腦空出來」，第一台休眠後開機。
	second := newBot("遠端數據喵")
	waits := make(chan hosts.Wait, 4)
	admitted := make(chan func(), 1)
	go func() {
		r, err := svc.Admit(ctx, second.ID, func(w hosts.Wait) { waits <- w })
		if err == nil {
			admitted <- r
		}
	}()
	if w := <-waits; w.Reason != hosts.WaitHostFull || w.Position != 1 {
		t.Fatalf("排隊原因：%+v", w)
	}
	if _, err := boxes.Stop(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-admitted:
		r()
	case <-time.After(15 * time.Second):
		t.Fatal("第一台休眠後應該放行")
	}

	// ④ 主機離線（CL-6）：狀態變 offline，工作排隊；sshd 回來後自動重連、排隊的工作照常完成。
	if err := th.StopSSHD(ctx); err != nil {
		t.Fatal(err)
	}
	waitHost(ctx, t, db, hostID, store.HostOffline, time.Minute)
	type result struct {
		task store.Task
		err  error
	}
	done := make(chan result, 1)
	queued := bus.Subscribe(events.ForBot(bot.ID), 64)
	defer queued.Close()
	go func() {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Minute)
		defer ccancel()
		r, err := rt.Chat(cctx, bot.ID, "", "主機回來再做")
		if err != nil {
			done <- result{err: err}
			return
		}
		task, err := rt.Wait(cctx, r.TaskID)
		done <- result{task, err}
	}()
	waitWait(t, queued, hosts.WaitHostOffline)
	if err := th.StartSSHD(ctx); err != nil {
		t.Fatal(err)
	}
	waitHost(ctx, t, db, hostID, store.HostOnline, time.Minute)
	r := <-done
	if r.err != nil || r.task.Verdict != runtime.VerdictAnswer {
		t.Fatalf("主機回來後排隊的工作：%+v %v", r.task, r.err)
	}

	// ⑤ 移除：上面有電腦時不行；刪掉電腦後可以。
	if _, err := svc.Remove(ctx, hostID); err == nil {
		t.Fatal("上面有電腦時不應該能移除")
	}
	for _, b := range []store.Bot{bot, second} {
		drv.Close(b.ID)
		if _, err := boxes.Remove(ctx, b.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := db.DeleteBot(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Remove(ctx, hostID); err != nil {
		t.Fatal(err)
	}
}

func waitHost(ctx context.Context, t *testing.T, db *store.DB, id, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		h, err := db.GetHost(ctx, id)
		if err == nil && h.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("主機沒有變成 %s：%+v %v", want, h, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitWait 等到 task.state 帶著某個排隊原因。
func waitWait(t *testing.T, sub *events.Subscription, reason string) {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case e := <-sub.C:
			var p struct {
				State string     `json:"state"`
				Wait  hosts.Wait `json:"wait"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			if e.Kind == runtime.KindTaskState && p.Wait.Reason == reason {
				return
			}
		case <-deadline:
			t.Fatalf("沒有收到排隊原因 %s", reason)
		}
	}
}

// testLoadTinyImage 建一個很小的映像，經 SSH 的 Docker API 載進主機，確認主機上真的有它。
func testLoadTinyImage(ctx context.Context, t *testing.T, cli *client.Client, reg *hosts.Registry, hostID string, th *sshtesthost.Host) {
	t.Helper()
	const tag = "xbot-test-tiny:b12"
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "hello.txt"), []byte("xbot b12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM scratch\nCOPY hello.txt /\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "docker", "build", "-q", "-t", tag, d).CombinedOutput(); err != nil {
		t.Fatalf("建小映像：%v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", tag).Run()
		_, _ = th.Exec(context.Background(), "docker", "rmi", "-f", tag)
	})
	_, _ = th.Exec(ctx, "docker", "rmi", "-f", tag)
	h, err := reg.Get(hostID)
	if err != nil {
		t.Fatal(err)
	}
	img, err := hosts.LocalImage(ctx, cli, tag)
	if err != nil || img.ID == "" {
		t.Fatalf("小映像：%+v %v", img, err)
	}
	var last int64
	if err := hosts.LoadImage(ctx, cli, h.Docker().(hosts.ImageLoader), img, func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if last == 0 {
		t.Fatal("應該回報傳了多少")
	}
	// 兩邊的 Docker 版本不同時映像 id 可能不同（containerd 映像儲存），比內容（各層）。
	got, err := h.Docker().ImageInspect(ctx, tag)
	if err != nil || !hosts.SameImage(got.InspectResponse, img) {
		t.Fatalf("主機上的小映像：%s %v（要 %s）", got.ID, err, img.ID)
	}
}
