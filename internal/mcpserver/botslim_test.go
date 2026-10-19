package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/botops"
	"xbot/internal/bots"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/org"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// slimEnv 是 Bot 版工具清單的測試環境：含團隊操作（有只給管理者的工具），經 HTTP 直接接 BotHandler。
type slimEnv struct {
	db  *store.DB
	bot store.Bot
	key string
	url string
}

func newSlimEnv(t *testing.T) *slimEnv {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	bots.Register(reg, bots.Deps{})
	botops.Register(reg, botops.Deps{})
	org.Register(reg, org.New(org.Options{DB: db}))
	b, err := db.CreateBot(ctx, store.Bot{Name: "水哥哥", IdleMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{7}, 32))
	keys := proxy.NewWorkerKeys(proxy.NewMemory(), kr)
	key, _ := keys.Issue(ctx, b.ID)
	srv := httptest.NewServer(mcpserver.BotHandler(reg, mcpserver.BotOptions{Keys: keys, DB: db})(b.ID))
	t.Cleanup(srv.Close)
	return &slimEnv{db: db, bot: b, key: string(key), url: srv.URL}
}

func (e *slimEnv) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{Transport: headers{h: map[string]string{"Authorization": "Bearer " + e.key}, base: http.DefaultTransport}}
	c := mcp.NewClient(&mcp.Implementation{Name: "hermes-test", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.url, HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func (e *slimEnv) tools(t *testing.T) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range e.session(t).Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

var managerTools = []string{"xbot.teams.create", "xbot.teams.remove_bot", "xbot.teams.remove_many", "xbot.teams.remove_team", "xbot.teams.update_bot"}

// 沒有團隊管理權限的 Bot 看不到只給管理者的工具；權限打開後（下一次連線）就看得到。
func TestBotToolsFollowManagePermission(t *testing.T) {
	e := newSlimEnv(t)
	tools := e.tools(t)
	for _, name := range managerTools {
		if tools[name] != nil {
			t.Errorf("沒有團隊管理權限卻看得到 %s", name)
		}
	}
	for _, name := range []string{"xbot.teams.team_status", "xbot.teams.work_review", "xbot.cards.post", "xbot.team.roster"} {
		if tools[name] == nil {
			t.Errorf("每個 Bot 都要看得到 %s", name)
		}
	}
	member := len(tools)

	e.bot.CanManage = true
	if _, err := e.db.UpdateBot(context.Background(), e.bot); err != nil {
		t.Fatal(err)
	}
	tools = e.tools(t)
	for _, name := range managerTools {
		if tools[name] == nil {
			t.Errorf("有團隊管理權限要看得到 %s", name)
		}
	}
	// 多出來的剛好是這幾個（新的只給管理者的操作要記得加進 managerTools）。
	if len(tools) != member+len(managerTools) {
		t.Fatalf("管理者多出的工具：%d → %d", member, len(tools))
	}
}

// Bot 版的工具省 token：沒有 request_id、dry_run，schema 沒有長度與範圍限制、沒有 additionalProperties: false、
// 沒有 x-*，說明沒有「需要權限」。
func TestBotToolsAreSlim(t *testing.T) {
	e := newSlimEnv(t)
	e.bot.CanManage = true
	if _, err := e.db.UpdateBot(context.Background(), e.bot); err != nil {
		t.Fatal(err)
	}
	tools := e.tools(t)
	for name, tool := range tools {
		raw, _ := json.Marshal(tool.InputSchema)
		for _, bad := range []string{`"request_id"`, `"dry_run"`, `"maxLength"`, `"minimum"`, `"maximum"`, `"additionalProperties":false`, `"x-`} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s 的 schema 有 %s：%s", name, bad, raw)
			}
		}
		if strings.Contains(tool.Description, "需要權限") {
			t.Errorf("%s 的說明有「需要權限」", name)
		}
	}
	// 必填與型別還在（模型要知道怎麼填）。
	var s struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	raw, _ := json.Marshal(tools["xbot.cards.post"].InputSchema)
	if err := json.Unmarshal(raw, &s); err != nil || !slices.Contains(s.Required, "title") || s.Properties["title"]["type"] != "string" ||
		s.Properties["title"]["description"] == nil {
		t.Fatalf("cards.post 的 schema：%s", raw)
	}
	// 代理用的完整版不變。
	full := mcpserver.Tool(ops.Info{Name: "xbot.x.y", Summary: "x", Scope: ops.ScopeOperate, Mutates: true,
		Input: &ops.Schema{Type: "object", Properties: map[string]*ops.Schema{}}})
	if b, _ := json.Marshal(full.InputSchema); !strings.Contains(string(b), `"request_id"`) || !strings.Contains(full.Description, "需要權限") {
		t.Fatalf("代理的工具：%s %q", b, full.Description)
	}
}

// 拿掉的限制伺服器照樣擋：太長的輸入經 Bot 的出口一樣回 invalid_input；Bot 自己帶 request_id 也照樣收。
func TestBotToolsStillValidated(t *testing.T) {
	e := newSlimEnv(t)
	cs := e.session(t)
	if oe := call(t, cs, "xbot.cards.post", map[string]any{"type": "info", "title": strings.Repeat("長", 500)}, nil); oe == nil ||
		oe.Code != ops.CodeInvalidInput || len(oe.Fields) == 0 || oe.Fields[0].Field != "title" {
		t.Fatalf("太長的標題：%+v", oe)
	}
	if oe := call(t, cs, "xbot.bots.update_my_profile", map[string]any{"title": strings.Repeat("長", 500)}, nil); oe == nil || oe.Code != ops.CodeInvalidInput {
		t.Fatalf("太長的職稱：%+v", oe)
	}
	if oe := call(t, cs, "xbot.bots.update_my_profile", map[string]any{"title": "研究員", "request_id": "r1"}, nil); oe != nil {
		t.Fatalf("帶 request_id：%+v", oe)
	}
	if b, _ := e.db.GetBot(context.Background(), e.bot.ID); b.Title != "研究員" {
		t.Fatalf("職稱沒改：%q", b.Title)
	}
	// 沒有權限的 Bot 叫不到只給管理者的工具（清單裡沒有，伺服器也沒有）。
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "xbot.teams.remove_team", Arguments: map[string]any{"team": "x"}}); err == nil && !res.IsError {
		t.Fatal("沒有權限的 Bot 不應該叫得到 remove_team")
	}
}

// Bot 送核准卡不算錯誤：Hermes 把 isError 算成伺服器出錯，連續 3 次就暫停整個 xbot MCP。
// 連送幾張都要是成功的結果，帶核准 id 和怎麼等；代理的出口照舊回 pending_approval 錯誤（server_test.go）。
func TestBotPendingApprovalIsNotAnError(t *testing.T) {
	e := newSlimEnv(t)
	ctx := context.Background()
	e.bot.CanManage = true
	if _, err := e.db.UpdateBot(ctx, e.bot); err != nil {
		t.Fatal(err)
	}
	cs := e.session(t)
	seen := map[string]bool{}
	for i, name := range []string{"甲", "乙", "丙", "丁"} {
		if _, err := e.db.CreateBot(ctx, store.Bot{Name: name, IdleMinutes: 30}); err != nil {
			t.Fatal(err)
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "xbot.teams.remove_bot", Arguments: map[string]any{"bot": name}})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("第 %d 張核准卡被當成錯誤：%s", i+1, res.Content[0].(*mcp.TextContent).Text)
		}
		var out mcpserver.PendingOut
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		if out.Status != "pending_approval" || out.ApprovalID == "" || seen[out.ApprovalID] {
			t.Fatalf("結果：%+v", out)
		}
		seen[out.ApprovalID] = true
		for _, want := range []string{"已送出核准卡", "按允許", out.ApprovalID, "xbot.approvals.wait"} {
			if !strings.Contains(out.Message, want) {
				t.Errorf("說明少了 %q：%s", want, out.Message)
			}
		}
		if res.Meta["xbot/pending_approval"] != true {
			t.Errorf("meta：%v", res.Meta)
		}
		if a, err := e.db.GetApproval(ctx, out.ApprovalID); err != nil || a.Status != "pending" {
			t.Fatalf("核准紀錄：%+v %v", a, err)
		}
	}
	// 還沒允許：Bot 都還在
	if bots, _ := e.db.ListBots(ctx, true); len(bots) != 5 {
		t.Fatalf("核准前不能刪：剩 %d 個", len(bots))
	}
}
