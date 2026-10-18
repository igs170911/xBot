//go:build docker

package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/box"
	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/opsclient"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestDockerCLIFirstConversation：服務在這個程序裡跑（和 cmd/xbot serve 相同的組裝），
// 用指令列同一個用戶端（opsclient，走 /api/v1）設定模型 → 建 Bot → 對話 → 等回覆 → 刪除。
// Bot 電腦是真的容器，模型是主機上的假模型（hermestest.FakeModel）。
func TestDockerCLIFirstConversation(t *testing.T) {
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
	fake := &hermestest.FakeModel{}
	model := httptest.NewServer(fake)
	defer model.Close()
	const realKey = "sk-FAKE-REAL-KEY-c05-must-not-enter-box"

	// 短一點的資料目錄：product.sock 的路徑不能超過 108 字元。
	dir, err := os.MkdirTemp("", "xbc05")
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

	// 和 cmd/xbot/stack.go、registry.go、cmd_serve.go 相同的組裝。
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	mp := proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore})
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") }, Model: mp, Keys: keys})
	defer sockets.Shutdown()
	comp := &runtime.Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore}
	boxes := box.New(db, hosts.NewRegistry(local), box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	bus := events.New()
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: comp})
	pstore.Attribute = rt.Active

	pairing := auth.NewPairing(db)
	reg := ops.New(ops.Config{DB: db, Notify: func(e store.Event) { bus.Publish(events.FromStore(e)) }})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, pairing)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore,
		Teardown: catalog.Teardown(db, drv.Close, func(ctx context.Context, botID string) error {
			_, err := boxes.Remove(ctx, botID, true)
			return err
		})})
	const accessToken = "owner-access-token-c05"
	tokens := &auth.Authenticator{DB: db, AccessToken: accessToken}
	srv := httptest.NewServer(api.New(api.Options{
		ModelProxy: mp, MCP: mcpserver.Handler(reg, mcpserver.HTTPOptions{Auth: tokens}), AgentPair: pairing.Handler(),
		Registry: reg, Bus: bus, DB: db, Auth: auth.NewHTTP(auth.HTTPConfig{Tokens: tokens, AccessToken: accessToken}),
	}))
	defer srv.Close()

	c := &opsclient.Client{BaseURL: srv.URL, Token: accessToken}
	run := func(op string, in any, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := c.Call(ctx, op, raw, ops.Options{})
		if err != nil {
			t.Fatalf("%s：%v", op, err)
		}
		if out != nil {
			if err := json.Unmarshal(res.Output, out); err != nil {
				t.Fatal(err)
			}
		}
	}

	run("xbot.models.set_provider", map[string]any{"id": "openai", "base_url": model.URL + "/v1", "api_key": realKey, "default_model": "fake-model"}, nil)
	var bot struct {
		ID string `json:"id"`
	}
	run("xbot.bots.create", map[string]any{"name": "C05 端對端"}, &bot)
	name := box.ContainerName(bot.ID)
	deleted := false
	defer func() {
		if deleted {
			return
		}
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

	var sent struct {
		ThreadID string `json:"thread_id"`
		TaskID   string `json:"task_id"`
	}
	run("xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "回覆 OK"}, &sent)
	var w struct {
		Settled bool   `json:"settled"`
		State   string `json:"state"`
		Verdict string `json:"verdict"`
		Text    string `json:"text"`
	}
	// 第一次要開機：等到 settled（每次最多 300 秒）。
	for i := 0; i < 3 && !w.Settled; i++ {
		run("xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 300}, &w)
	}
	if !w.Settled || w.Verdict != "answer" || !strings.Contains(strings.ToUpper(w.Text), "OK") {
		t.Fatalf("回覆：%+v\n%s", w, logs())
	}
	if fake.ChatCalls() == 0 {
		t.Fatal("假模型應該被呼叫")
	}
	for _, a := range fake.Auth() {
		if a != "Bearer "+realKey {
			t.Errorf("供應商收到的不是真金鑰：%q", a)
		}
	}
	var read struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	run("xbot.chat.read", map[string]any{"thread_id": sent.ThreadID}, &read)
	if len(read.Messages) != 2 {
		t.Fatalf("%+v", read)
	}

	// 刪除（你本人直接執行）：容器與資料區都要不見。
	var del struct {
		Deleted bool `json:"deleted"`
	}
	run("xbot.bots.delete", map[string]any{"id": bot.ID}, &del)
	if !del.Deleted {
		t.Fatal("應該刪掉了")
	}
	deleted = true
	if _, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("容器應該刪掉了")
	}
	vols := box.VolumeNames(bot.ID)
	for _, v := range []string{vols.Home, vols.Workspace, vols.Hermes} {
		if _, err := cli.VolumeInspect(ctx, v, client.VolumeInspectOptions{}); err == nil {
			t.Fatalf("資料區 %s 應該刪掉了", v)
		}
	}
	if _, err := db.GetBot(ctx, bot.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("Bot 應該刪掉了")
	}
}
