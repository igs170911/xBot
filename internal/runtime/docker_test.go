//go:build docker

// 端對端：真的開一台 Bot 電腦，經 Hermes → hermes_host.py → product.sock → Model Proxy → 模型。
// 需要 Docker 與 make box 建好的映像（BOX_IMAGE，預設 xbot-box:dev）；沒有時略過。
// 有設 XBOT_TEST_OPENAI_BASE、XBOT_TEST_OPENAI_KEY、XBOT_TEST_MODEL 時用真的模型，否則用主機上的假模型。
package runtime

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// dockerEnv 是一台真的 Bot 電腦加上和 cmd/xbot/stack.go 相同的組裝。
type dockerEnv struct {
	cli     *client.Client
	db      *store.DB
	keys    *proxy.WorkerKeys
	rt      *Runtime
	comp    *Computers
	boxes   *box.Manager
	bot     store.Bot
	fake    *hermestest.FakeModel
	realKey string
	name    string
}

func (e *dockerEnv) logs() string {
	out, _ := exec.Command("docker", "logs", "--tail", "60", e.name).CombinedOutput()
	return string(out)
}

// newDockerEnv 開好組裝與一個 Hermes Bot（電腦在第一則訊息時才開機）；測試結束時刪掉電腦。沒有 Docker 或映像時略過。
func newDockerEnv(ctx context.Context, t *testing.T) *dockerEnv {
	t.Helper()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	t.Cleanup(func() { cli.Close() })
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	e := &dockerEnv{cli: cli}

	base, realKey, modelName := os.Getenv("XBOT_TEST_OPENAI_BASE"), os.Getenv("XBOT_TEST_OPENAI_KEY"), os.Getenv("XBOT_TEST_MODEL")
	if base == "" || realKey == "" || modelName == "" {
		e.fake = &hermestest.FakeModel{}
		srv := httptest.NewServer(e.fake)
		t.Cleanup(srv.Close)
		base, realKey, modelName = srv.URL+"/v1", "sk-FAKE-REAL-KEY-must-not-enter-box-7f3a", "fake-model"
		t.Logf("用假模型 %s", base)
	} else {
		t.Logf("用真的模型 %s（%s）", base, modelName)
	}
	e.realKey = realKey

	// 短一點的資料目錄：product.sock 的路徑不能超過 108 字元。
	dir, err := os.MkdirTemp("", "xbe2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e.db = db
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })

	// 和 cmd/xbot/stack.go 相同的組裝。
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	e.keys = proxy.NewWorkerKeys(pstore, kr)
	model := proxy.NewHandler(proxy.Options{Keys: e.keys, Providers: providers, Usage: pstore})
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") }, Model: model, Keys: e.keys})
	t.Cleanup(func() { _ = sockets.Shutdown() })
	comp := &Computers{DB: db, Keys: e.keys, Sockets: sockets, Providers: pstore}
	// 和其他套件共用本機的網路防護（xbot-netguard）；網路防護測試在跑時先等它做完（見 guardlock）。
	guardlock.Shared(t)
	boxes := box.New(db, hosts.NewRegistry(local), box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	comp.Hosts = hosts.NewRegistry(local)
	e.comp, e.boxes = comp, boxes
	drv := hermes.New(comp.Connect, nil)
	e.rt = New(Options{DB: db, Driver: drv, Bus: events.New(), Models: comp})
	pstore.Attribute = e.rt.Active

	if err := providers.Put(ctx, proxy.Provider{ID: "openai", BaseURL: base, APIKey: proxy.Secret(realKey)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, DefaultModelSetting, ModelRef{Provider: "openai", Model: modelName}); err != nil {
		t.Fatal(err)
	}
	e.bot, err = db.CreateBot(ctx, store.Bot{Name: "端對端", Engine: hermes.Name})
	if err != nil {
		t.Fatal(err)
	}
	e.name = box.ContainerName(e.bot.ID)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		drv.Close(e.bot.ID)
		if _, err := boxes.Remove(ctx, e.bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	})
	return e
}

func TestDockerChatEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	env := newDockerEnv(ctx, t)
	rt, db, bot, fake, realKey, cli, keys, name, logs := env.rt, env.db, env.bot, env.fake, env.realKey, env.cli, env.keys, env.name, env.logs

	sub := rt.Bus().Subscribe(events.ForBot(bot.ID), 0)
	defer sub.Close()
	res, err := rt.Chat(ctx, bot.ID, "", "回覆 OK")
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	var kinds []string
	var done map[string]string
	for done == nil {
		select {
		case e := <-sub.C:
			if e.TaskID != res.TaskID {
				continue
			}
			kinds = append(kinds, e.Kind)
			switch e.Kind {
			case KindText:
				var p map[string]string
				_ = json.Unmarshal(e.Payload, &p)
				streamed.WriteString(p["text"])
			case KindDone:
				_ = json.Unmarshal(e.Payload, &done)
			case KindError:
				t.Logf("錯誤事件：%s", e.Payload)
			}
		case <-ctx.Done():
			t.Fatalf("等不到回覆：%v\n%s", kinds, logs())
		}
	}
	t.Logf("事件：%v；串流：%q；done：%v", kinds, streamed.String(), done)
	if done["verdict"] != VerdictAnswer {
		t.Fatalf("應該正常完成：%v\n%s", done, logs())
	}
	if streamed.Len() == 0 || !strings.Contains(strings.ToUpper(streamed.String()), "OK") {
		t.Fatalf("應該收到串流文字 OK：%q", streamed.String())
	}
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || !strings.Contains(strings.ToUpper(msgs[1].Content), "OK") {
		t.Fatalf("訊息：%+v", msgs)
	}
	if fake != nil && fake.ChatCalls() == 0 {
		t.Fatal("假模型應該被呼叫")
	}
	if fake != nil {
		for _, a := range fake.Auth() {
			if a != "Bearer "+realKey {
				t.Errorf("供應商收到的不是真金鑰：%q", a)
			}
		}
	}
	usage, _ := db.ListUsage(ctx, store.UsageFilter{BotID: bot.ID})
	if len(usage) == 0 || usage[0].TaskID != res.TaskID {
		t.Fatalf("用量應該記到這個任務：%+v", usage)
	}

	// 真金鑰不進容器：環境變數與 /hermes、/home/box 的設定檔都沒有它，只有 worker-key。
	got, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Container.Config.Env {
		if strings.Contains(e, realKey) {
			t.Fatalf("容器環境變數有真金鑰：%s", e)
		}
	}
	out, _ := exec.CommandContext(ctx, "docker", "exec", name, "grep", "-rlF", realKey, "/hermes", "/home/box/.config").Output()
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Fatalf("容器裡的檔案有真金鑰：%s", s)
	}
	cfgOut, err := exec.CommandContext(ctx, "docker", "exec", name, "cat", "/hermes/config.yaml", "/hermes/.env").CombinedOutput()
	if err != nil {
		t.Fatalf("讀設定：%v %s", err, cfgOut)
	}
	wk, _ := keys.Issue(ctx, bot.ID)
	if !strings.Contains(string(cfgOut), "http://127.0.0.1:8642/model/openai/v1") || !strings.Contains(string(cfgOut), "XBOT_MODEL_TOKEN="+string(wk)) {
		t.Fatalf("設定內容：%s", cfgOut)
	}
	if !strings.Contains(string(cfgOut), "tool_search:") {
		t.Fatalf("XBot 的工具要直接給模型看（tool_search 關掉）：%s", cfgOut)
	}

	// 服務重開時 product.sock 會被關掉，電腦還開著；下一次接上要補開，不然 Bot 連不到模型和 XBot 工具。
	_ = env.comp.Sockets.Close(bot.ID)
	if _, err := env.comp.Connect(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env.comp.Sockets.Path(bot.ID)); err != nil {
		t.Fatalf("電腦開著時接上要補開 product.sock：%v", err)
	}

	// 同一條對話串再說一次（沿用連線與 session），再測停止。
	res2, err := rt.Chat(ctx, bot.ID, res.ThreadID, "再回覆一次 OK")
	if err != nil {
		t.Fatal(err)
	}
	task, err := rt.Wait(ctx, res2.TaskID)
	if err != nil || task.Verdict != VerdictAnswer {
		t.Fatalf("第二輪：%+v %v\n%s", task, err, logs())
	}
}

// 同一個 Bot、同一條對話串，上一輪 done 之後立刻送下一則（不留空檔）。Hermes 在 message.complete 之後還在收尾，
// 這時送出的 prompt 曾經被當成「插話」收下卻不執行，那一輪就一直等不到結束。
func TestDockerBackToBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	env := newDockerEnv(ctx, t)
	threadID := ""
	for i, text := range []string{"回覆 OK", "再回覆一次 OK", "第三次回覆 OK", "回覆 OK"} {
		res, err := env.rt.Chat(ctx, env.bot.ID, threadID, text)
		if err != nil {
			t.Fatalf("第 %d 輪：%v", i+1, err)
		}
		threadID = res.ThreadID
		start := time.Now()
		wctx, wcancel := context.WithTimeout(ctx, 3*time.Minute)
		task, err := env.rt.Wait(wctx, res.TaskID)
		wcancel()
		if err != nil || task.Verdict != VerdictAnswer {
			t.Fatalf("第 %d 輪：%+v %v\n%s", i+1, task, err, env.logs())
		}
		t.Logf("第 %d 輪完成（%s）", i+1, time.Since(start).Round(time.Millisecond))
	}
	// 換新的對話串也一樣（同一條連線上馬上開新的 session）。
	for i := range 3 {
		res, err := env.rt.Chat(ctx, env.bot.ID, "", "回覆 OK")
		if err != nil {
			t.Fatalf("新對話串第 %d 輪：%v", i+1, err)
		}
		wctx, wcancel := context.WithTimeout(ctx, 3*time.Minute)
		task, err := env.rt.Wait(wctx, res.TaskID)
		wcancel()
		if err != nil || task.Verdict != VerdictAnswer {
			t.Fatalf("新對話串第 %d 輪：%+v %v\n%s", i+1, task, err, env.logs())
		}
	}
	msgs, _ := env.db.ListMessages(ctx, store.ScopeThread, threadID, 0, 0)
	if len(msgs) != 8 {
		t.Fatalf("應該有 4 問 4 答：%d 則", len(msgs))
	}
	for i := 1; i < len(msgs); i += 2 {
		if !strings.Contains(strings.ToUpper(msgs[i].Content), "OK") {
			t.Fatalf("第 %d 則回覆：%q", i, msgs[i].Content)
		}
	}
}

// BX-12、CL-9：閒置的電腦休眠後，有工作時自動叫醒（同一台電腦、同一條對話串接著做）；排隊中的工作也一樣。
func TestDockerIdleSleepAndWake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	env := newDockerEnv(ctx, t)
	wait := func(taskID string) {
		t.Helper()
		wctx, wcancel := context.WithTimeout(ctx, 3*time.Minute)
		defer wcancel()
		task, err := env.rt.Wait(wctx, taskID)
		if err != nil || task.Verdict != VerdictAnswer {
			t.Fatalf("工作：%+v %v\n%s", task, err, env.logs())
		}
	}
	res, err := env.rt.Chat(ctx, env.bot.ID, "", "回覆 OK")
	if err != nil {
		t.Fatal(err)
	}
	wait(res.TaskID)
	first, err := env.boxes.Status(ctx, env.bot.ID)
	if err != nil || !first.Running {
		t.Fatalf("開機：%+v %v", first, err)
	}

	// 剛做完：還沒閒置夠久。
	if slept, err := env.boxes.SleepIdle(ctx, env.rt, time.Now()); err != nil || len(slept) != 0 {
		t.Fatalf("還不該休眠：%v %v", slept, err)
	}
	// 30 分鐘後：休眠（docker stop），Hermes 的連線先關掉。
	slept, err := env.boxes.SleepIdle(ctx, env.rt, time.Now().Add(31*time.Minute))
	if err != nil || len(slept) != 1 {
		t.Fatalf("閒置休眠：%v %v", slept, err)
	}
	st, err := env.boxes.Status(ctx, env.bot.ID)
	if err != nil || st.Running || st.State != box.StateSleeping {
		t.Fatalf("休眠後：%+v %v", st, err)
	}
	got, err := env.cli.ContainerInspect(ctx, env.name, client.ContainerInspectOptions{})
	if err != nil || got.Container.State.Running || got.Container.State.ExitCode != 0 {
		t.Fatalf("容器應該正常停下：%+v %v", got.Container.State, err)
	}

	// 有工作：自動叫醒，同一台電腦，對話串接著做；後面排隊的工作也做完。
	start := time.Now()
	res2, err := env.rt.Chat(ctx, env.bot.ID, res.ThreadID, "再回覆一次 OK")
	if err != nil {
		t.Fatal(err)
	}
	res3, err := env.rt.Start(ctx, ChatRequest{BotID: env.bot.ID, ThreadID: res.ThreadID, Text: "第三次回覆 OK", Queue: true})
	if err != nil || !res3.Queued {
		t.Fatalf("排隊：%+v %v", res3, err)
	}
	wait(res2.TaskID)
	wait(res3.TaskID)
	t.Logf("叫醒並做完兩件工作：%s", time.Since(start).Round(time.Millisecond))
	again, err := env.boxes.Status(ctx, env.bot.ID)
	if err != nil || !again.Running || again.ContainerID != first.ContainerID {
		t.Fatalf("應該叫醒同一台電腦：%+v %v（原本 %s）", again, err, first.ContainerID)
	}
	if env.rt.HasWork(env.bot.ID) {
		t.Fatal("做完了不該還有工作")
	}
}
