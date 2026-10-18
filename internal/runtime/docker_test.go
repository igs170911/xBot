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
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

func TestDockerChatEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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

	base, realKey, modelName := os.Getenv("XBOT_TEST_OPENAI_BASE"), os.Getenv("XBOT_TEST_OPENAI_KEY"), os.Getenv("XBOT_TEST_MODEL")
	var fake *hermestest.FakeModel
	if base == "" || realKey == "" || modelName == "" {
		fake = &hermestest.FakeModel{}
		srv := httptest.NewServer(fake)
		defer srv.Close()
		base, realKey, modelName = srv.URL+"/v1", "sk-FAKE-REAL-KEY-must-not-enter-box-7f3a", "fake-model"
		t.Logf("用假模型 %s", base)
	} else {
		t.Logf("用真的模型 %s（%s）", base, modelName)
	}

	// 短一點的資料目錄：product.sock 的路徑不能超過 108 字元。
	dir, err := os.MkdirTemp("", "xbe2e")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	// 和 cmd/xbot/stack.go 相同的組裝。
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	model := proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore})
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") }, Model: model, Keys: keys})
	defer sockets.Shutdown()
	comp := &Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore}
	boxes := box.New(db, hosts.NewRegistry(local), box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	rt := New(Options{DB: db, Driver: drv, Bus: events.New(), Models: comp})
	pstore.Attribute = rt.Active

	if err := providers.Put(ctx, proxy.Provider{ID: "openai", BaseURL: base, APIKey: proxy.Secret(realKey)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, DefaultModelSetting, ModelRef{Provider: "openai", Model: modelName}); err != nil {
		t.Fatal(err)
	}
	bot, err := db.CreateBot(ctx, store.Bot{Name: "端對端", Engine: hermes.Name})
	if err != nil {
		t.Fatal(err)
	}
	name := box.ContainerName(bot.ID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		drv.Close(bot.ID)
		if _, err := boxes.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	}()
	logs := func() string {
		out, _ := exec.Command("docker", "logs", "--tail", "60", name).CombinedOutput()
		return string(out)
	}

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
