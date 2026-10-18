package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/auth"
	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

const ownerToken = "owner-access-token"

// TestMCPFirstConversation 是 G0 的流程（不需要 Docker）：配對好的代理經 MCP 建 Bot → 說話 → 等到回覆。
// 引擎是假的 Hermes gateway；模型設定走真的 models.set_provider 與 runtime.Computers.ResolveModel。
func TestMCPFirstConversation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	g := hermestest.New("wk")
	defer g.Close()
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{1}, 32))
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	drv := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: g.Addr(), Key: "wk"}, nil
	}, nil)
	bus := events.New()
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: &runtime.Computers{DB: db, Providers: pstore}})

	pairing := auth.NewPairing(db)
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, pairing)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore})
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.Handler(reg, mcpserver.HTTPOptions{Auth: &auth.Authenticator{DB: db, AccessToken: ownerToken}}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 你本人：設定模型供應商（金鑰只寫不讀），再產生配對碼。
	ownerCS, err := mcpserver.Dial(ctx, srv.URL, ownerToken, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer ownerCS.Close()
	callOK(t, ownerCS, "xbot.models.set_provider", map[string]any{"id": "openai", "base_url": "https://api.example.com/v1", "api_key": "sk-secret", "default_model": "gpt-x"}, nil)
	var code auth.PairStartOut
	callOK(t, ownerCS, "xbot.agents.pair_start", map[string]any{"name": "Claude Code", "scopes": []string{"read", "operate", "manage"}}, &code)
	pr, perr := pairing.Complete(ctx, code.Code, "", "test")
	if perr != nil {
		t.Fatal(perr)
	}

	// 代理：建 Bot → chat.send → wait_until_settled。
	cs, err := mcpserver.Dial(ctx, srv.URL, pr.Token, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var models struct {
		Ready bool `json:"ready"`
	}
	callOK(t, cs, "xbot.models.get", map[string]any{}, &models)
	if !models.Ready {
		t.Fatal("模型應該設定好了")
	}
	var bot struct {
		ID           string `json:"id"`
		ApprovalMode string `json:"approval_mode"`
	}
	callOK(t, cs, "xbot.bots.create", map[string]any{"name": "小秘", "title": "幕僚長", "request_id": "create-1"}, &bot)
	if bot.ID == "" || bot.ApprovalMode != "ask" {
		t.Fatalf("%+v", bot)
	}
	var sent struct {
		ThreadID string `json:"thread_id"`
		TaskID   string `json:"task_id"`
	}
	callOK(t, cs, "xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "回覆 OK"}, &sent)
	var w struct {
		Settled bool   `json:"settled"`
		State   string `json:"state"`
		Verdict string `json:"verdict"`
		Text    string `json:"text"`
	}
	callOK(t, cs, "xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 20}, &w)
	if !w.Settled || w.State != "done" || w.Verdict != "answer" || w.Text != "OK" {
		t.Fatalf("回覆：%+v", w)
	}
	var read struct {
		Messages []struct {
			Sender  string `json:"sender"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	callOK(t, cs, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID}, &read)
	if len(read.Messages) != 2 || read.Messages[1].Content != "OK" {
		t.Fatalf("%+v", read)
	}
	// 代理不能刪 Bot：送到待我處理；也不能自己核准。
	res := call(t, cs, "xbot.bots.delete", map[string]any{"id": bot.ID})
	e := toolErr(t, res)
	if e.Code != ops.CodePendingApproval || e.NeedsApprovalID == "" {
		t.Fatalf("%+v", e)
	}
	if e2 := toolErr(t, call(t, cs, "xbot.approvals.decide", map[string]any{"approval_id": e.NeedsApprovalID, "decision": "allow"})); e2.Code != ops.CodeForbidden {
		t.Fatalf("%+v", e2)
	}
	// 稽核看得到代理做過的事，金鑰不會出現在任何紀錄。
	audit, _ := db.ListAudit(ctx, store.AuditFilter{})
	b, _ := json.Marshal(audit)
	if !strings.Contains(string(b), "xbot.chat.send") || strings.Contains(string(b), "sk-secret") {
		t.Fatalf("稽核：%s", b)
	}
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func callOK(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) {
	t.Helper()
	res := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s：%+v", name, toolErr(t, res))
	}
	if out != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatal(err)
		}
	}
}

func toolErr(t *testing.T, res *mcp.CallToolResult) ops.Error {
	t.Helper()
	if !res.IsError {
		t.Fatal("應該是錯誤")
	}
	b, _ := json.Marshal(res.StructuredContent)
	var e ops.Error
	_ = json.Unmarshal(b, &e)
	return e
}
