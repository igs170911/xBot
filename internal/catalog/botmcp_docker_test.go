//go:build docker

package catalog_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/inbox"
	"xbot/internal/ipc"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestDockerBotMCP 是 C11 的驗收：真的 Bot 電腦裡，Hermes 經 stdio 橋接（box/xbot_mcp.py）→ product.sock
// 拿到 XBot MCP 的工具；模型呼叫 xbot.approvals.request，對話與「待我處理」出現核准卡；你允許後 Bot 等得到結果。
// 另外直接在容器裡執行橋接，確認 tools/list 只有給 Bot 的工具。
func TestDockerBotMCP(t *testing.T) {
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
	fake := &hermestest.FakeModel{Tool: &hermestest.FakeToolCall{Match: "approvals_request",
		Args: `{"subject":"用 320 元買下 example.com","details":"三家比過，這家最便宜。","category":"花錢"}`}}
	model := httptest.NewServer(fake)
	defer model.Close()

	dir, err := os.MkdirTemp("", "xbc11")
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

	// 和 cmd/xbot/stack.go、cmd_serve.go 相同的組裝（含 SetProduct）。
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
	comp := &runtime.Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore, XBotMCP: runtime.XBotMCPServer()}
	boxes := box.New(db, hosts.NewRegistry(local), box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	bus := events.New()
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: comp})
	pstore.Attribute = rt.Active
	reg := ops.New(ops.Config{DB: db, Notify: func(e store.Event) { bus.Publish(events.FromStore(e)) }})
	ops.RegisterBuiltin(reg)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore})
	sockets.SetProduct(mcpserver.BotHandler(reg, mcpserver.BotOptions{Keys: keys, DB: db}))

	owner := ops.Owner("test")
	run := func(op string, in any, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, owner, op, raw, ops.Options{})
		if err != nil {
			t.Fatalf("%s：%v", op, err)
		}
		if out != nil {
			if err := json.Unmarshal(res.Output, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	run("xbot.models.set_provider", map[string]any{"id": "openai", "base_url": model.URL + "/v1", "api_key": "sk-fake", "default_model": "fake-model"}, nil)
	var bot struct {
		ID string `json:"id"`
	}
	run("xbot.bots.create", map[string]any{"name": "C11 小研"}, &bot)
	run("xbot.bots.create", map[string]any{"name": "C11 小寫", "title": "文案"}, nil)
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

	var sent struct {
		ThreadID string `json:"thread_id"`
		TaskID   string `json:"task_id"`
	}
	run("xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "幫我買網域，先問我"}, &sent)
	var w struct {
		Settled bool   `json:"settled"`
		Verdict string `json:"verdict"`
		Text    string `json:"text"`
	}
	for i := 0; i < 3 && !w.Settled; i++ {
		run("xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 300}, &w)
	}
	if !w.Settled || w.Verdict != "answer" {
		t.Fatalf("回覆：%+v\n%s", w, logs())
	}

	// Hermes 把 XBot MCP 的工具交給模型，模型呼叫了 approvals.request。
	var xbotTools []string
	for _, n := range fake.ToolNames() {
		if strings.Contains(n, "xbot") {
			xbotTools = append(xbotTools, n)
		}
	}
	t.Logf("Hermes 交給模型的 XBot 工具：%v；呼叫：%v", xbotTools, fake.ToolCalls())
	for _, want := range []string{"cards_post", "approvals_request", "approvals_wait", "team_roster"} {
		if !slices.ContainsFunc(xbotTools, func(n string) bool { return strings.Contains(strings.ReplaceAll(n, ".", "_"), want) }) {
			t.Fatalf("Hermes 沒有 %s：%v\n%s", want, fake.ToolNames(), logs())
		}
	}
	if slices.ContainsFunc(xbotTools, func(n string) bool { return strings.Contains(n, "bots_delete") || strings.Contains(n, "bots.delete") }) {
		t.Fatalf("Bot 不應該有刪除 Bot 的工具：%v", xbotTools)
	}
	if len(fake.ToolCalls()) == 0 {
		t.Fatalf("模型應該呼叫了工具\n%s", logs())
	}

	// 核准卡：待我處理一張（屬於這條對話），對話裡一張。
	var approvals inbox.ApprovalsListOut
	run("xbot.approvals.list", map[string]any{"status": "pending"}, &approvals)
	if len(approvals.Approvals) != 1 || approvals.Approvals[0].RequestedBy != "bot:"+bot.ID || approvals.Approvals[0].Op != "xbot.approvals.request" {
		t.Fatalf("核准：%+v\n%s", approvals, logs())
	}
	items, _ := db.ListInbox(ctx, store.InboxOpen)
	if len(items) != 1 || items[0].ThreadID != sent.ThreadID || !strings.Contains(items[0].Title, "example.com") {
		t.Fatalf("待我處理：%+v", items)
	}
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, sent.ThreadID, 0, 0)
	if !slices.ContainsFunc(msgs, func(m store.Message) bool { return m.Kind == "card" && m.Sender == "bot:"+bot.ID }) {
		t.Fatalf("對話裡要有核准卡：%+v", msgs)
	}
	var o inbox.Outcome
	run("xbot.approvals.decide", map[string]any{"approval_id": approvals.Approvals[0].ID, "decision": "allow"}, &o)
	if !o.Executed {
		t.Fatalf("允許：%+v", o)
	}

	// 在容器裡直接執行橋接（和 Claude Code、Codex 一樣）：tools/list 只有給 Bot 的工具；approvals.wait 看到允許。
	key, _ := keys.Issue(ctx, bot.ID)
	lines := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"acp","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"xbot.approvals.wait","arguments":{"approval_id":"` + approvals.Approvals[0].ID + `","timeout_s":5}}}`,
	}, "\n") + "\n"
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", "-u", "1000:1000", "-e", "XBOT_WORKER_KEY="+string(key), name, "xbot-mcp")
	cmd.Stdin = strings.NewReader(lines)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("橋接：%v %s", err, out)
	}
	replies := map[float64]json.RawMessage{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m struct {
			ID     float64         `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Result != nil {
			replies[m.ID] = m.Result
		}
	}
	var list struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(replies[2], &list)
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	// 至少要有 C11 的這幾個（X02 之後還有 xbot.collab.*）；不能有 Bot 不該用的（例：主機、刪除 Bot）。
	for _, want := range []string{"xbot.approvals.list", "xbot.approvals.request", "xbot.approvals.wait", "xbot.cards.post", "xbot.team.roster"} {
		if !slices.Contains(names, want) {
			t.Fatalf("橋接的 tools/list 少了 %s：%v\n%s", want, names, out)
		}
	}
	for _, n := range names {
		if strings.HasPrefix(n, "xbot.hosts.") || n == "xbot.bots.delete" {
			t.Fatalf("Bot 不該看得到 %s", n)
		}
	}
	if !strings.Contains(string(replies[3]), `\"executed\":true`) && !strings.Contains(string(replies[3]), `"executed":true`) {
		t.Fatalf("approvals.wait：%s", replies[3])
	}
	// 用 Hermes 的設定（不帶 XBOT_WORKER_KEY，讀 /hermes/.worker-key）也連得上。
	cmd = exec.CommandContext(ctx, "docker", "exec", "-i", "-u", "1000:1000", name, "/opt/hermes/.venv/bin/python", "/opt/xbot/xbot_mcp.py")
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "xbot.team.roster") {
		t.Fatalf("Hermes 設定的橋接：%v %s", err, out)
	}
	cfgOut, _ := exec.CommandContext(ctx, "docker", "exec", name, "cat", "/hermes/config.yaml").CombinedOutput()
	if !strings.Contains(string(cfgOut), "xbot_mcp.py") {
		t.Fatalf("Hermes 設定沒有 xbot：%s", cfgOut)
	}
}
