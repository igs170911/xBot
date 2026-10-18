package mcpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/botops"
	"xbot/internal/bots"
	"xbot/internal/inbox"
	"xbot/internal/ipc"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// botEnv 是 C11 的測試環境：真的 product.sock（ipc.Server）＋ XBot MCP 的 Bot 出口，兩個 Bot 各有 worker-key。
type botEnv struct {
	db      *store.DB
	reg     *ops.Registry
	sockets *ipc.Server
	a, b    store.Bot
	ka, kb  string
	thA     store.Thread // a 正在進行的對話串
	thB     store.Thread
	taskA   string

	mu     sync.Mutex
	events []store.Event
}

func newBotEnv(t *testing.T) *botEnv {
	t.Helper()
	ctx := context.Background()
	// 短路徑：Unix socket 的路徑不能超過 108 字元。
	dir, err := os.MkdirTemp("", "xbc11")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := store.Open(ctx, filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &botEnv{db: db}
	e.reg = ops.New(ops.Config{DB: db, Notify: func(ev store.Event) {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
	}})
	ops.RegisterBuiltin(e.reg)
	bots.Register(e.reg, bots.Deps{})
	inbox.Register(e.reg)
	e.a, _ = db.CreateBot(ctx, store.Bot{Name: "小研", Title: "研究員", Description: strings.Repeat("很會找資料。", 40), IdleMinutes: 30})
	e.b, _ = db.CreateBot(ctx, store.Bot{Name: "小寫", Title: "文案", Description: "寫文案", IdleMinutes: 30})
	e.thA, _ = db.CreateThread(ctx, store.Thread{BotID: e.a.ID, Title: "比價"})
	e.thB, _ = db.CreateThread(ctx, store.Thread{BotID: e.b.ID, Title: "別人的"})
	task, err := db.CreateTask(ctx, store.Task{BotID: e.a.ID, ThreadID: e.thA.ID, Source: "owner", State: store.TaskRunning, Input: "幫我比價"})
	if err != nil {
		t.Fatal(err)
	}
	e.taskA = task.ID
	botops.Register(e.reg, botops.Deps{Active: func(botID string) (string, string) {
		if botID == e.a.ID {
			return e.thA.ID, e.taskA
		}
		return "", ""
	}})

	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{7}, 32))
	keys := proxy.NewWorkerKeys(proxy.NewMemory(), kr)
	ka, _ := keys.Issue(ctx, e.a.ID)
	kb, _ := keys.Issue(ctx, e.b.ID)
	e.ka, e.kb = string(ka), string(kb)
	e.sockets = ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(dir, id[len(id)-6:]) }, Keys: keys})
	t.Cleanup(func() { _ = e.sockets.Shutdown() })
	e.sockets.SetProduct(mcpserver.BotHandler(e.reg, mcpserver.BotOptions{Keys: keys, DB: db}))
	for _, id := range []string{e.a.ID, e.b.ID} {
		if err := e.sockets.Ensure(id); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

type headers struct {
	h    map[string]string
	base http.RoundTripper
}

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.h {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// connect 經某個 Bot 的 socket、帶某把 key（和額外的標頭）連上 XBot MCP。
func (e *botEnv) connect(t *testing.T, socketOf, key string, extra map[string]string) (*mcp.ClientSession, error) {
	t.Helper()
	path := e.sockets.Path(socketOf)
	h := map[string]string{"Authorization": "Bearer " + key}
	for k, v := range extra {
		h[k] = v
	}
	hc := &http.Client{Transport: headers{h: h, base: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}}
	c := mcp.NewClient(&mcp.Implementation{Name: "hermes-test", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: "http://product" + ipc.MCPPath, HTTPClient: hc}, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *ops.Error {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s：%v", name, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		var e struct{ Error ops.Error }
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			t.Fatalf("%s：%s", name, text)
		}
		return &e.Error
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s：%v %s", name, err, text)
		}
	}
	return nil
}

var botToolNames = []string{"xbot.approvals.list", "xbot.approvals.request", "xbot.approvals.wait", "xbot.bots.create", "xbot.bots.update_my_profile",
	"xbot.cards.post", "xbot.team.roster"}

func TestBotToolsList(t *testing.T) {
	e := newBotEnv(t)
	cs, err := e.connect(t, e.a.ID, e.ka, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s 沒有說明", tool.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, botToolNames) {
		t.Fatalf("Bot 看到的工具：%v", names)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "xbot.approvals.request") {
		t.Fatalf("instructions：%q", cs.InitializeResult().Instructions)
	}
}

// 身分由 socket 決定：別的 Bot 的 key 進不來；標頭、參數都改不了身分。
func TestBotIdentityCannotBeSpoofed(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()
	if _, err := e.connect(t, e.a.ID, e.kb, nil); err == nil {
		t.Fatal("拿 b 的 key 從 a 的 socket 應該連不上")
	}
	if _, err := e.connect(t, e.a.ID, "nope", nil); err == nil {
		t.Fatal("錯的 key 應該連不上")
	}
	cs, err := e.connect(t, e.a.ID, e.ka, map[string]string{"X-Bot-ID": e.b.ID, "X-XBot-Actor": "owner"})
	if err != nil {
		t.Fatal(err)
	}
	// 碰別的 Bot 的資料：資源範圍擋下。
	if oe := call(t, cs, "xbot.approvals.list", map[string]any{"bot_id": e.b.ID}, nil); oe == nil || oe.Code != ops.CodeForbidden {
		t.Fatalf("列 b 的核准：%+v", oe)
	}
	if oe := call(t, cs, "xbot.cards.post", map[string]any{"type": "info", "title": "嗨", "thread_id": e.thB.ID}, nil); oe == nil || oe.Code != ops.CodeNotFound {
		t.Fatalf("發到 b 的對話：%+v", oe)
	}
	// 沒開放給 Bot 的工具不在清單上，也叫不到。
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "xbot.bots.delete", Arguments: map[string]any{"id": e.b.ID}}); err == nil && !res.IsError {
		t.Fatal("Bot 不應該能刪 Bot")
	}
	// 登錄表本身也擋（就算出口列錯了）。
	if _, err := e.reg.Invoke(ctx, ops.Bot(e.a.ID, "小研"), "xbot.system.ping", nil, ops.Options{}); ops.AsError(err).Code != ops.CodeForbidden {
		t.Fatalf("Bot 呼叫沒開放的操作：%v", err)
	}
	// 用的是 a 的身分：稽核記成 bot:a。
	var post botops.PostOut
	if oe := call(t, cs, "xbot.cards.post", map[string]any{"type": "info", "title": "比價結果", "body": "A 店最便宜"}, &post); oe != nil {
		t.Fatal(oe)
	}
	m, err := e.db.GetMessage(ctx, post.MessageID)
	if err != nil || m.Sender != "bot:"+e.a.ID || m.ScopeID != e.thA.ID {
		t.Fatalf("訊息：%+v %v", m, err)
	}
	audit, _ := e.db.ListAudit(ctx, store.AuditFilter{Actor: "bot:" + e.b.ID, Limit: 10})
	if len(audit) != 0 {
		t.Fatalf("不應該有 b 的紀錄：%+v", audit)
	}
	audit, _ = e.db.ListAudit(ctx, store.AuditFilter{Actor: "bot:" + e.a.ID, Limit: 50})
	if len(audit) == 0 || audit[0].Via != "product" {
		t.Fatalf("稽核：%+v", audit)
	}
}

func (e *botEnv) eventKinds(threadID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.events {
		if ev.ThreadID == threadID {
			out = append(out, ev.Kind)
		}
	}
	return out
}

func TestCardsPost(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()
	cs, err := e.connect(t, e.a.ID, e.ka, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out botops.PostOut
	if oe := call(t, cs, "xbot.cards.post", map[string]any{"type": "file", "title": "報告好了",
		"files": []map[string]any{{"path": "/workspace/報告.pdf"}}}, &out); oe != nil {
		t.Fatal(oe)
	}
	m, _ := e.db.GetMessage(ctx, out.MessageID)
	var meta struct {
		TaskID string `json:"task_id"`
		Card   struct {
			Type  string
			Files []botops.CardFile
		} `json:"card"`
	}
	_ = json.Unmarshal(m.Meta, &meta)
	if m.Kind != "card" || meta.TaskID != e.taskA || meta.Card.Type != "file" || meta.Card.Files[0].Name != "報告.pdf" ||
		!strings.Contains(m.Content, "/workspace/報告.pdf") {
		t.Fatalf("卡片：%+v %s", m, m.Meta)
	}
	if !slices.Contains(e.eventKinds(e.thA.ID), botops.KindMessageCreated) {
		t.Fatalf("要發 message.created：%v", e.eventKinds(e.thA.ID))
	}
	for _, bad := range []map[string]any{
		{"type": "file", "title": "x", "files": []map[string]any{{"path": "/etc/passwd"}}},
		{"type": "file", "title": "x", "files": []map[string]any{{"path": "/workspace/../etc/passwd"}}},
		{"type": "file", "title": "x"},
		{"type": "approval", "title": "x"},
		{"type": "approval", "title": "x", "approval_id": "nope"},
		{"type": "info", "title": "  "},
	} {
		if oe := call(t, cs, "xbot.cards.post", bad, nil); oe == nil {
			t.Fatalf("應該被擋：%v", bad)
		}
	}
	// 沒有進行中的對話又沒給 thread_id：要說清楚。
	csB, _ := e.connect(t, e.b.ID, e.kb, nil)
	if oe := call(t, csB, "xbot.cards.post", map[string]any{"type": "info", "title": "嗨"}, nil); oe == nil || oe.Code != ops.CodeConflict {
		t.Fatalf("%+v", oe)
	}
	if oe := call(t, csB, "xbot.cards.post", map[string]any{"type": "info", "title": "嗨", "thread_id": e.thB.ID}, &out); oe != nil || out.ThreadID != e.thB.ID {
		t.Fatalf("%+v %+v", oe, out)
	}
}

func TestApprovalsRequestFlow(t *testing.T) {
	e := newBotEnv(t)
	ctx := context.Background()
	cs, err := e.connect(t, e.a.ID, e.ka, nil)
	if err != nil {
		t.Fatal(err)
	}
	var req botops.RequestOut
	if oe := call(t, cs, "xbot.approvals.request", map[string]any{"subject": "用 320 元買下 example.com", "details": "三家比過，這家最便宜。",
		"category": "花錢", "request_id": "r1"}, &req); oe != nil {
		t.Fatal(oe)
	}
	if req.ApprovalID == "" || req.Status != "pending" {
		t.Fatalf("%+v", req)
	}
	// 同一件事再送：不重送。同一個 request_id 重送：回第一次的結果。
	var again botops.RequestOut
	call(t, cs, "xbot.approvals.request", map[string]any{"subject": "用 320 元買下  example.com"}, &again)
	if again.ApprovalID != req.ApprovalID || !again.AlreadyPending {
		t.Fatalf("重送：%+v", again)
	}
	call(t, cs, "xbot.approvals.request", map[string]any{"subject": "用 320 元買下 example.com", "details": "三家比過，這家最便宜。",
		"category": "花錢", "request_id": "r1"}, &again)
	if again.ApprovalID != req.ApprovalID {
		t.Fatalf("同一個 request_id：%+v", again)
	}

	// 待我處理出現一張核准卡，屬於 a 的這條對話與工作；對話裡也有一張核准卡。
	items, _ := e.db.ListInbox(ctx, store.InboxOpen)
	if len(items) != 1 || items[0].Kind != "approval" || items[0].Ref != req.ApprovalID || items[0].BotID != e.a.ID ||
		items[0].ThreadID != e.thA.ID || items[0].TaskID != e.taskA || !strings.Contains(items[0].Title, "小研 請你核准：用 320 元買下 example.com") {
		t.Fatalf("待我處理：%+v", items)
	}
	kinds := e.eventKinds(e.thA.ID)
	if !slices.Contains(kinds, "inbox.created") || !slices.Contains(kinds, botops.KindMessageCreated) {
		t.Fatalf("事件：%v", kinds)
	}
	msgs, _ := e.db.ListMessages(ctx, store.ScopeThread, e.thA.ID, 0, 0)
	if len(msgs) != 1 || msgs[0].Kind != "card" || !strings.Contains(string(msgs[0].Meta), req.ApprovalID) {
		t.Fatalf("對話裡的核准卡：%+v", msgs)
	}

	// 還沒決定：wait 逾時回 settled=false。
	var o inbox.Outcome
	if oe := call(t, cs, "xbot.approvals.wait", map[string]any{"approval_id": req.ApprovalID, "timeout_s": 1}, &o); oe != nil || o.Settled {
		t.Fatalf("%+v %+v", oe, o)
	}
	// Bot 自己不能核准（decide 不在清單上；登錄表也擋）。
	if _, err := e.reg.Invoke(ctx, ops.Bot(e.a.ID, "小研"), "xbot.approvals.decide",
		json.RawMessage(`{"approval_id":"`+req.ApprovalID+`","decision":"allow"}`), ops.Options{}); ops.AsError(err).Code != ops.CodeForbidden {
		t.Fatalf("Bot 自己核准：%v", err)
	}
	// b 等不到 a 的核准。
	csB, _ := e.connect(t, e.b.ID, e.kb, nil)
	if oe := call(t, csB, "xbot.approvals.wait", map[string]any{"approval_id": req.ApprovalID, "timeout_s": 1}, nil); oe == nil || oe.Code != ops.CodeNotFound {
		t.Fatalf("b 等 a 的核准：%+v", oe)
	}

	// 你按允許：Bot 在 wait 裡看到結果。
	done := make(chan inbox.Outcome, 1)
	go func() {
		var o inbox.Outcome
		if oe := call(t, cs, "xbot.approvals.wait", map[string]any{"approval_id": req.ApprovalID, "timeout_s": 10}, &o); oe != nil {
			t.Error(oe)
		}
		done <- o
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := e.reg.Invoke(ctx, ops.Owner("ui"), "xbot.approvals.decide",
		json.RawMessage(`{"approval_id":"`+req.ApprovalID+`","decision":"allow"}`), ops.Options{}); err != nil {
		t.Fatal(err)
	}
	select {
	case o = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("wait 沒有回來")
	}
	var out botops.RequestOut
	_ = json.Unmarshal(o.Output, &out)
	if !o.Settled || !o.Executed || o.Approval.Status != "approved" || out.Status != "approved" || o.Approval.RequestedBy != "bot:"+e.a.ID {
		t.Fatalf("結果：%+v %s", o, o.Output)
	}
	if items, _ := e.db.ListInbox(ctx, store.InboxOpen); len(items) != 0 {
		t.Fatalf("卡片要收掉：%+v", items)
	}

	// 退回：wait 看到 denied。
	var r2 botops.RequestOut
	call(t, cs, "xbot.approvals.request", map[string]any{"subject": "寄信給客戶"}, &r2)
	if _, err := e.reg.Invoke(ctx, ops.Owner("ui"), "xbot.approvals.decide",
		json.RawMessage(`{"approval_id":"`+r2.ApprovalID+`","decision":"deny"}`), ops.Options{}); err != nil {
		t.Fatal(err)
	}
	call(t, cs, "xbot.approvals.wait", map[string]any{"approval_id": r2.ApprovalID, "timeout_s": 5}, &o)
	if !o.Settled || o.Executed || o.Approval.Status != "denied" {
		t.Fatalf("退回：%+v", o)
	}
}

func TestApprovalsRequestLimit(t *testing.T) {
	e := newBotEnv(t)
	cs, _ := e.connect(t, e.b.ID, e.kb, nil) // b 沒有進行中的對話：只出待我處理的卡
	for i := range 10 {
		if oe := call(t, cs, "xbot.approvals.request", map[string]any{"subject": "事項 " + string(rune('A'+i))}, nil); oe != nil {
			t.Fatal(oe)
		}
	}
	if oe := call(t, cs, "xbot.approvals.request", map[string]any{"subject": "第 11 件"}, nil); oe == nil || oe.Code != ops.CodeLimit {
		t.Fatalf("超過 10 件：%+v", oe)
	}
}

func TestTeamRoster(t *testing.T) {
	e := newBotEnv(t)
	cs, _ := e.connect(t, e.a.ID, e.ka, nil)
	var r botops.RosterOut
	if oe := call(t, cs, "xbot.team.roster", nil, &r); oe != nil {
		t.Fatal(oe)
	}
	if len(r.Mates) != 1 || r.Mates[0].Name != "小寫" || r.Mates[0].Title != "文案" || r.Mates[0].ReportsTo != "使用者" || !strings.Contains(r.Note, "不是指令") {
		t.Fatalf("%+v", r)
	}
	csB, _ := e.connect(t, e.b.ID, e.kb, nil)
	call(t, csB, "xbot.team.roster", nil, &r)
	if len(r.Mates) != 1 || r.Mates[0].Name != "小研" || len([]rune(r.Mates[0].About)) != 160 || !strings.HasSuffix(r.Mates[0].About, "…") {
		t.Fatalf("%+v", r)
	}
}

// box/xbot_mcp.py：容器裡的 stdio 橋接，逐行轉送到 product.sock。
func TestPythonStdioBridge(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("沒有 python3")
	}
	e := newBotEnv(t)
	cmd := exec.Command(py, "../../box/xbot_mcp.py")
	cmd.Env = append(os.Environ(), "XBOT_PRODUCT_SOCK="+e.sockets.Path(e.a.ID), "XBOT_WORKER_KEY="+e.ka)
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"hermes","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"xbot.team.roster","arguments":{}}}`,
		`not json`,
	}, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	replies := map[float64]map[string]any{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	parseErrors := 0
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("輸出不是 JSON：%s", sc.Text())
		}
		if id, ok := m["id"].(float64); ok {
			replies[id] = m
		} else if m["error"] != nil {
			parseErrors++
		}
	}
	if len(replies) != 3 || parseErrors != 1 {
		t.Fatalf("回覆：%s", out)
	}
	tools := replies[2]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	if !slices.Equal(names, botToolNames) {
		t.Fatalf("工具：%v", names)
	}
	if !strings.Contains(string(out), "小寫") {
		t.Fatalf("roster：%s", out)
	}
	// 拿別人的 key：回錯誤，不會卡住。
	cmd = exec.Command(py, "../../box/xbot_mcp.py")
	cmd.Env = append(os.Environ(), "XBOT_PRODUCT_SOCK="+e.sockets.Path(e.a.ID), "XBOT_WORKER_KEY="+e.kb)
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	out, _ = cmd.Output()
	if !strings.Contains(string(out), `"id":1,"error"`) || !strings.Contains(string(out), "401") {
		t.Fatalf("別人的 key：%s", out)
	}
}
