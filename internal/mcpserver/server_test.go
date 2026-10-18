package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/auth"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type env struct {
	db      *store.DB
	reg     *ops.Registry
	pairing *auth.Pairing
	srv     *httptest.Server
}

const ownerSecret = "owner-access-token"

func newEnv(t *testing.T, localOwner bool) *env {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := auth.NewPairing(db)
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, p)
	mux := http.NewServeMux()
	mux.Handle("/mcp", Handler(reg, HTTPOptions{Auth: &auth.Authenticator{DB: db, AccessToken: ownerSecret}, LocalOwner: localOwner}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{db: db, reg: reg, pairing: p, srv: srv}
}

// pairAgent 由本人產生配對碼再換成權杖。
func (e *env) pairAgent(t *testing.T, scopes ...ops.Scope) auth.PairResult {
	t.Helper()
	code, _, err := e.pairing.Start(auth.PairGrant{Name: "Test Agent", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	res, oe := e.pairing.Complete(context.Background(), code, "", "test")
	if oe != nil {
		t.Fatal(oe)
	}
	return res
}

func (e *env) dial(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	cs, err := Dial(context.Background(), e.srv.URL, token, e.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func errCode(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		return ""
	}
	b, _ := json.Marshal(res.StructuredContent)
	var e ops.Error
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return string(e.Code)
}

func TestToolsListReflectsRegistry(t *testing.T) {
	e := newEnv(t, false)
	agent := e.pairAgent(t, ops.ScopeRead)
	tools := listTools(t, e.dial(t, agent.Token))

	var names []string
	for _, info := range e.reg.List() {
		names = append(names, info.Name)
		tool := tools[info.Name]
		if tool == nil {
			t.Fatalf("少了工具 %s", info.Name)
		}
		if !strings.HasPrefix(tool.Description, info.Summary) {
			t.Errorf("%s 說明不對：%q", info.Name, tool.Description)
		}
		schema := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, k := range info.Input.Order {
			if props[k] == nil {
				t.Errorf("%s 少了欄位 %s", info.Name, k)
			}
		}
		_, hasReq := props[ArgRequestID]
		_, hasDry := props[ArgDryRun]
		if hasReq != info.Mutates || hasDry != info.Mutates {
			t.Errorf("%s：request_id／dry_run 只給會改東西的工具（mutates=%v）", info.Name, info.Mutates)
		}
		if tool.Annotations.ReadOnlyHint != (info.Scope == ops.ScopeRead) {
			t.Errorf("%s readOnlyHint 不對", info.Name)
		}
		if info.Mutates && *tool.Annotations.DestructiveHint != (info.Risk == ops.RiskConfirm) {
			t.Errorf("%s destructiveHint 不對", info.Name)
		}
	}
	if len(tools) != len(names) {
		t.Fatalf("工具數量 %d，登錄表 %d", len(tools), len(names))
	}
	if !*tools["xbot.agents.revoke"].Annotations.DestructiveHint {
		t.Fatal("revoke 是 confirm，應該標 destructive")
	}
	req := tools["xbot.agents.pair_start"].InputSchema.(map[string]any)["required"].([]any)
	if !slices.Contains(req, any("scopes")) {
		t.Fatalf("必填欄位沒帶到：%v", req)
	}
}

func TestToolCallScopeAndIdentity(t *testing.T) {
	e := newEnv(t, false)
	agent := e.pairAgent(t, ops.ScopeRead)
	cs := e.dial(t, agent.Token)

	res := call(t, cs, "xbot.system.ping", map[string]any{"echo": "hi"})
	if res.IsError {
		t.Fatalf("ping 失敗：%v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	if out["caller"] != "agent:"+agent.Agent.ID || out["echo"] != "hi" {
		t.Fatalf("身分或結果不對：%v", out)
	}
	// 權限不足（admin 操作）→ 工具錯誤，固定格式。
	res = call(t, cs, "xbot.settings.get", map[string]any{"name": "x"})
	if errCode(t, res) != "forbidden" {
		t.Fatalf("應該 forbidden：%v", res.StructuredContent)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"code":"forbidden"`) {
		t.Fatalf("文字內容也要有錯誤代碼：%s", text)
	}
	res = call(t, cs, "xbot.agents.pair_start", map[string]any{"scopes": []string{"admin"}})
	if errCode(t, res) != "forbidden" {
		t.Fatalf("read 代理不能配對：%v", res.StructuredContent)
	}
	// 輸入錯誤
	res = call(t, cs, "xbot.system.ping", map[string]any{"nope": 1})
	if errCode(t, res) != "invalid_input" {
		t.Fatalf("應該 invalid_input：%v", res.StructuredContent)
	}
	// 稽核記下代理的呼叫與經由 mcp。
	entries, err := e.db.ListAudit(context.Background(), store.AuditFilter{Actor: "agent:" + agent.Agent.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Via != "mcp" {
		t.Fatalf("稽核沒有記到：%+v", entries)
	}
}

func TestOwnerDryRunAndRequestID(t *testing.T) {
	e := newEnv(t, false)
	cs := e.dial(t, ownerSecret)
	res := call(t, cs, "xbot.agents.pair_start", map[string]any{"scopes": []string{"read"}, "dry_run": true})
	if res.IsError || res.Meta["xbot/dry_run"] != true {
		t.Fatalf("預演結果不對：%+v", res)
	}
	if code := res.StructuredContent.(map[string]any)["code"]; code != nil {
		t.Fatalf("預演不應該產生配對碼：%v", code)
	}
	args := map[string]any{"scopes": []string{"read"}, "request_id": "r-1"}
	first := call(t, cs, "xbot.agents.pair_start", args)
	second := call(t, cs, "xbot.agents.pair_start", args)
	if first.IsError || second.Meta["xbot/replayed"] != true {
		t.Fatalf("同一個 request_id 應該回第一次的結果：%+v", second)
	}
	a, b := first.StructuredContent.(map[string]any)["code"], second.StructuredContent.(map[string]any)["code"]
	if a == nil || a != b {
		t.Fatalf("重送不應該產生新配對碼：%v %v", a, b)
	}
}

func TestPendingApprovalForAgents(t *testing.T) {
	e := newEnv(t, false)
	admin := e.pairAgent(t, ops.ScopeAdmin)
	other := e.pairAgent(t, ops.ScopeRead)
	res := call(t, e.dial(t, admin.Token), "xbot.agents.revoke", map[string]any{"id": other.Agent.ID})
	if errCode(t, res) != "pending_approval" {
		t.Fatalf("代理做 confirm 動作要等核准：%v", res.StructuredContent)
	}
	if res.StructuredContent.(map[string]any)["needs_approval_id"] == "" {
		t.Fatal("要回核准 id")
	}
}

func TestRejectsMissingAndRevokedTokens(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	if _, err := Dial(ctx, e.srv.URL, "", e.srv.Client()); err == nil {
		t.Fatal("沒有權杖應該連不上（本機免權杖預設關閉）")
	}
	if _, err := Dial(ctx, e.srv.URL, "xbot_wrong", e.srv.Client()); err == nil {
		t.Fatal("錯的權杖應該連不上")
	}
	agent := e.pairAgent(t, ops.ScopeRead)
	cs := e.dial(t, agent.Token)
	if res := call(t, cs, "xbot.system.ping", nil); res.IsError {
		t.Fatal("撤銷前應該可以")
	}
	if err := e.db.RevokeAgentToken(ctx, agent.Agent.ID); err != nil {
		t.Fatal(err)
	}
	// 同一條連線的下一個請求就被拒絕。
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "xbot.system.ping", Arguments: map[string]any{}}); err == nil {
		t.Fatal("撤銷後的權杖應該被拒絕")
	}
	if _, err := Dial(ctx, e.srv.URL, agent.Token, e.srv.Client()); err == nil {
		t.Fatal("撤銷後不能再連線")
	}
	// 直接打 HTTP：401。
	resp, err := http.Post(e.srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("沒權杖應該 401：%d", resp.StatusCode)
	}
}

func TestLocalOwnerOptIn(t *testing.T) {
	e := newEnv(t, true)
	cs := e.dial(t, "") // httptest 是 127.0.0.1，Host 也是
	res := call(t, cs, "xbot.system.ping", nil)
	if res.IsError || res.StructuredContent.(map[string]any)["caller"] != "owner" {
		t.Fatalf("開啟 XBOT_MCP_LOCAL_OWNER 時本機應該是本人：%+v", res)
	}
	// 經反向代理（Host 不是 localhost）不算本機。
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Host = "xbot.example.ts.net"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("非本機 Host 應該要權杖：%d", resp.StatusCode)
	}
}

// stdio：xbot mcp 是把 stdio 接到服務 /mcp 的橋。這裡用 io.Pipe 模擬 stdin／stdout 走一圈。
func TestStdioBridgeRoundTrip(t *testing.T) {
	e := newEnv(t, false)
	agent := e.pairAgent(t, ops.ScopeRead)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote, err := NewRemote(ctx, func(ctx context.Context) (*mcp.ClientSession, error) {
		return Dial(ctx, e.srv.URL, agent.Token, e.srv.Client())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remote.Close() }()
	bridge, err := Bridge(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	clientR, serverW := io.Pipe() // 伺服器 stdout → 用戶端
	serverR, clientW := io.Pipe() // 用戶端 → 伺服器 stdin
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx, &mcp.IOTransport{Reader: serverR, Writer: serverW}) }()

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.IOTransport{Reader: clientR, Writer: clientW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := listTools(t, cs)
	if len(tools) != len(e.reg.List()) || tools["xbot.system.ping"] == nil {
		t.Fatalf("stdio 的工具清單不對：%d", len(tools))
	}
	res := call(t, cs, "xbot.system.ping", map[string]any{"echo": "stdio"})
	out := res.StructuredContent.(map[string]any)
	if res.IsError || out["echo"] != "stdio" || out["caller"] != "agent:"+agent.Agent.ID {
		t.Fatalf("stdio 呼叫結果不對：%+v", res)
	}
	if errCode(t, call(t, cs, "xbot.settings.get", map[string]any{"name": "x"})) != "forbidden" {
		t.Fatal("stdio 也要擋權限")
	}
	_ = cs.Close()
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) && !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Fatalf("bridge 結束時出錯：%v", err)
	}
}

func TestSplitArgs(t *testing.T) {
	in, opts, e := SplitArgs(json.RawMessage(`{"name":"x","request_id":"r","dry_run":true}`), true)
	if e != nil || string(in) != `{"name":"x"}` || opts.RequestID != "r" || !opts.DryRun {
		t.Fatalf("%s %+v %v", in, opts, e)
	}
	in, _, _ = SplitArgs(nil, true)
	if string(in) != "{}" {
		t.Fatalf("空白參數應該是 {}：%s", in)
	}
	if _, _, e := SplitArgs(json.RawMessage(`{"dry_run":"yes"}`), true); e == nil || e.Code != ops.CodeInvalidInput {
		t.Fatalf("dry_run 型別錯應該回 invalid_input：%v", e)
	}
	// 不會改東西的操作不拿掉（交給驗證回「不認得這個欄位」）。
	in, _, _ = SplitArgs(json.RawMessage(`{"dry_run":true}`), false)
	if !strings.Contains(string(in), "dry_run") {
		t.Fatal("唯讀操作不應該處理 dry_run")
	}
}
