package apps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/ipc"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

var ctx = context.Background()

type env struct {
	db      *store.DB
	reg     *ops.Registry
	svc     *Service
	bot     store.Bot
	other   store.Bot
	browser *fakeBrowser
	now     time.Time
	mu      sync.Mutex
	changes [][]string
	opened  []string // OpenOnBot 開過的「Bot id 網址」
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	keys, err := proxy.NewKeyring(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), browser: &fakeBrowser{jars: map[string][]Cookie{}}, now: time.Unix(1_800_000_000, 0)}
	ops.RegisterBuiltin(e.reg)
	e.bot, _ = db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes"})
	e.other, _ = db.CreateBot(ctx, store.Bot{Name: "阿哲", Engine: "claude"})
	e.svc = New(Options{DB: db, Keys: keys, BaseURL: "http://127.0.0.1:8780", Browser: e.browser,
		Now: func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
		OpenOnBot: func(_ context.Context, botID, u string) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.opened = append(e.opened, botID+" "+u)
			return nil
		},
		OnChange: func(_ context.Context, bots []string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.changes = append(e.changes, bots)
		}})
	Register(e.reg, e.svc)
	return e
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

func (e *env) call(t *testing.T, id ops.Identity, op string, in, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(ctx, id, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return err
}

func (e *env) must(t *testing.T, op string, in, out any) {
	t.Helper()
	if err := e.call(t, ops.Owner("test"), op, in, out); err != nil {
		t.Fatalf("%s：%v", op, err)
	}
}

func codeOf(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

var owner = ops.Owner("test")

// rpc 經轉送處理器送一個 JSON-RPC 訊息（像 Bot 電腦裡的橋接），回傳解開的回覆。
func rpc(t *testing.T, h http.Handler, name, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, ipc.AppsPrefix+name, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusAccepted {
		return nil
	}
	raw := rec.Body.String()
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "data: ") {
				raw = strings.TrimPrefix(line, "data: ")
			}
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("回覆看不懂（%d）：%s", rec.Code, rec.Body.String())
	}
	return out
}

func errMsg(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		s, _ := e["message"].(string)
		return s
	}
	return ""
}

func TestCatalogListsHostedAppsAndLeavesOutOnesNeedingADeveloperApp(t *testing.T) {
	all, cats := Catalog()
	if all[0].ID != "notion" || all[0].Auth != AuthOAuth2 || all[0].Title != "Notion" {
		t.Fatalf("第一個應該是 Notion：%+v", all[0])
	}
	byID := map[string]App{}
	for _, a := range all {
		byID[a.ID] = a
		if !strings.HasPrefix(a.URL, "https://") || !slices.Contains(cats, a.Category) || a.Icon == "" || a.Transport != "http" {
			t.Fatalf("目錄項目不完整：%+v", a)
		}
	}
	if byID["context7"].Auth != AuthNone || byID["github"].Auth != AuthAPIKey || !byID["slack"].NeedsClient {
		t.Fatal("驗證方式不對")
	}
	for _, skip := range []string{"asana", "n8n-official", "unreal-engine"} {
		if _, ok := byID[skip]; ok {
			t.Fatalf("%s 要你自己的開發者 App，不該在目錄", skip)
		}
	}
	if len(byID["calendly"].Exclude) == 0 {
		t.Fatal("manifest 的 default_excluded 工具要保留")
	}
	var out CatalogOut
	e := setup(t)
	e.must(t, "xbot.apps.catalog", CatalogIn{Query: "notion"}, &out)
	if len(out.Apps) != 1 || out.Apps[0].State != "available" || out.Total != len(all) {
		t.Fatalf("搜尋：%+v", out)
	}
}

// OAuth 的整個流程：找授權伺服器 → 動態註冊 → PKCE 登入網址 → 回呼換權杖（加密存放）→ 測試連線 → Bot 經轉送使用。
func TestOAuthSigninAndProxyKeepTokensServerSide(t *testing.T) {
	e := setup(t)
	f := newFakeRemote(t)
	var out ConnectOut
	e.must(t, "xbot.apps.add_mcp_server", ServerIn{Name: "notes", Title: "筆記", Transport: TransportHTTP, URL: f.srv.URL + "/mcp",
		Auth: AuthOAuth2, RedirectBase: "http://xbot.test"}, &out)
	if out.Signin == nil || out.Connection.Status != StatusSigningIn {
		t.Fatalf("要開始登入：%+v", out)
	}
	u, _ := url.Parse(out.Signin.AuthURL)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("client_id") != "cid" || q.Get("code_challenge_method") != "S256" || q.Get("resource") != f.srv.URL+"/mcp" ||
		q.Get("redirect_uri") != "http://xbot.test"+CallbackPath || q.Get("scope") != "read write offline_access" || q.Get("state") == "" {
		t.Fatalf("登入網址不對：%s", out.Signin.AuthURL)
	}
	f.challenge = q.Get("code_challenge")
	var st SigninResult
	e.must(t, "xbot.apps.signin_status", FlowIn{FlowID: out.Signin.FlowID}, &st)
	if st.Status != "pending" {
		t.Fatalf("還在等登入：%+v", st)
	}
	// 授權伺服器把瀏覽器導回 XBot。
	rec := httptest.NewRecorder()
	e.svc.CallbackHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, CallbackPath+"?code=good-code&state="+q.Get("state"), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "已連接 筆記") {
		t.Fatalf("回呼：%d %s", rec.Code, rec.Body.String())
	}
	e.must(t, "xbot.apps.signin_status", FlowIn{FlowID: out.Signin.FlowID}, &st)
	c := st.Connection
	if st.Status != "done" || c.Status != StatusOK || c.Account != "陳家選物" || len(c.Tools) != 2 {
		t.Fatalf("登入完成後：%+v %+v", st, c)
	}
	if v := f.tokenCalls[0]["code_verifier"]; v == "" {
		t.Fatal("要帶 PKCE code_verifier")
	}
	// 同一個 state 不能再用一次。
	rec = httptest.NewRecorder()
	e.svc.CallbackHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, CallbackPath+"?code=good-code&state="+q.Get("state"), nil))
	if f.issued != 1 {
		t.Fatal("state 只能用一次")
	}
	// 權杖加密存放：資料庫裡找不到明文，畫面也拿不到。
	raw, _ := e.db.GetAppConnection(ctx, c.ID)
	if strings.Contains(string(raw.Secret), "tok1") || strings.Contains(string(raw.Secret), "r1") {
		t.Fatal("權杖要加密")
	}
	full, _ := json.Marshal(c)
	if strings.Contains(string(full), "tok1") {
		t.Fatal("畫面不能看到權杖")
	}

	// Bot 電腦裡：只有 XBot MCP 橋接＋XBOT_MCP_PATH，沒有任何權杖。
	servers, err := e.svc.Servers(ctx, e.bot.ID)
	if err != nil || len(servers) != 1 || servers[0].Command != hermes.Python || servers[0].Env["XBOT_MCP_PATH"] != ipc.AppsPrefix+"notes" || len(servers[0].Secret) != 0 {
		t.Fatalf("Bot 的 MCP 伺服器：%+v %v", servers, err)
	}

	// 經轉送使用：XBot 帶上權杖。
	h := e.svc.Proxy(e.bot.ID)
	init := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"hermes"}}}`)
	if init["result"] == nil || f.lastAuth != "Bearer tok1" {
		t.Fatalf("initialize：%v（%s）", init, f.lastAuth)
	}
	rpc(t, h, "notes", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	// 關掉 delete_page：工具清單看不到，呼叫也被擋。
	var conn Connection
	e.must(t, "xbot.apps.update", UpdateIn{ID: c.ID, DisabledTools: []string{"delete_page"}}, &conn)
	list := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "search" {
		t.Fatalf("關掉的工具不該出現：%v", list)
	}
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_page","arguments":{}}}`); !strings.Contains(errMsg(m), "關掉") {
		t.Fatalf("關掉的工具不能用：%v", m)
	}
	call := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"q":"報價單"}}}`)
	if call["result"] == nil || !slices.Equal(f.calls, []string{"search"}) {
		t.Fatalf("呼叫工具：%v %v", call, f.calls)
	}
	// 太長的結果截斷。
	long := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"long"}}`)
	text := long["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if n := len([]rune(text)); n > 25_100 || !strings.Contains(text, "結果太長") {
		t.Fatalf("結果要截到 25,000 字：%d", n)
	}
	// 遠端 session 過期：自動重新 initialize，Bot 不會發現。
	f.dropSessions()
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"search"}}`); m["result"] == nil {
		t.Fatalf("session 過期要自動重連：%v", m)
	}
	// 權杖被撤換：用 refresh token 換新再重試一次。
	f.rotate()
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search"}}`); m["result"] == nil || f.lastAuth != "Bearer tok2" {
		t.Fatalf("401 要換新權杖重試：%v %s", m, f.lastAuth)
	}
	// 最近使用（I02）。
	e.must(t, "xbot.apps.get", IDIn{ID: c.ID}, &conn)
	if len(conn.Uses) < 3 || conn.Uses[0].BotID != e.bot.ID || conn.Uses[0].Tool != "search" || conn.LastUsedAt == 0 {
		t.Fatalf("最近使用：%+v", conn.Uses)
	}

	// 每 Bot 隔離：收回阿哲以外…改成只給阿哲，小秘就不能用。
	e.must(t, "xbot.apps.revoke_from_bot", RevokeIn{ID: c.ID, BotID: e.bot.ID}, &conn)
	if conn.AllBots || !slices.Equal(conn.BotIDs, []string{e.other.ID}) {
		t.Fatalf("收回後：%+v", conn)
	}
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":8,"method":"tools/list"}`); !strings.Contains(errMsg(m), "沒有被授權") {
		t.Fatalf("沒授權的 Bot 不能用：%v", m)
	}
	if s, _ := e.svc.Servers(ctx, e.bot.ID); len(s) != 0 {
		t.Fatal("沒授權的 Bot 電腦裡不該有這個連接")
	}
	if last := e.changes[len(e.changes)-1]; !slices.Contains(last, e.bot.ID) {
		t.Fatalf("收回要通知被收回的 Bot：%v", last)
	}
	e.must(t, "xbot.apps.grant_to_bot", GrantIn{ID: c.ID, BotID: e.bot.ID}, &conn)
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`); m["result"] == nil {
		t.Fatalf("授權後可以用：%v", m)
	}

	// refresh token 失效：狀態變成授權已過期，Bot 拿到白話的錯誤。
	f.mu.Lock()
	f.refreshTok = "changed"
	f.mu.Unlock()
	f.rotate()
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":10,"method":"tools/list"}`); !strings.Contains(errMsg(m), "授權已過期") {
		t.Fatalf("要說授權過期：%v", m)
	}
	e.must(t, "xbot.apps.get", IDIn{ID: c.ID}, &conn)
	if conn.Status != StatusExpired {
		t.Fatalf("狀態：%s", conn.Status)
	}
	if s, _ := e.svc.Servers(ctx, e.bot.ID); len(s) != 0 {
		t.Fatal("過期的連接先不給 Bot")
	}

	// 重新登入：在別的裝置登入，貼上停住的網址完成（I03 備案）。
	e.must(t, "xbot.apps.signin", SigninIn{ID: c.ID, RedirectBase: "http://xbot.test", SigninOnBot: e.bot.ID}, &out)
	if out.Signin.BotID != e.bot.ID || len(e.opened) != 1 || e.opened[0] != e.bot.ID+" "+out.Signin.AuthURL {
		t.Fatalf("登入頁要開在 Bot 的電腦上：%+v %v", out.Signin, e.opened)
	}
	u, _ = url.Parse(out.Signin.AuthURL)
	f.challenge = u.Query().Get("code_challenge")
	f.mu.Lock()
	f.refreshTok = "r1"
	f.mu.Unlock()
	if f.registered != 1 {
		t.Fatalf("回呼網址沒變就不用重新註冊：%d", f.registered)
	}
	if err := e.call(t, owner, "xbot.apps.finish_signin", FinishIn{URL: "https://example.com/nothing"}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("網址不對：%v", err)
	}
	e.must(t, "xbot.apps.finish_signin", FinishIn{URL: "http://localhost:8791/api/v1/apps/oauth/callback?code=good-code&state=" + u.Query().Get("state")}, &st)
	if st.Status != "done" || st.Connection.Status != StatusOK {
		t.Fatalf("貼網址完成登入：%+v", st)
	}

	// 中斷：要核准（代理呼叫時），本人直接做；連接與權杖一起刪掉。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "Claude Code", Scopes: ops.Scopes}
	if err := e.call(t, agent, "xbot.apps.disconnect", IDIn{ID: c.ID}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("代理中斷要核准：%v", err)
	}
	e.must(t, "xbot.apps.disconnect", IDIn{ID: c.ID}, nil)
	if _, err := e.db.GetAppConnection(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("中斷後要刪掉")
	}
	if m := rpc(t, h, "notes", `{"jsonrpc":"2.0","id":11,"method":"tools/list"}`); !strings.Contains(errMsg(m), "找不到") {
		t.Fatalf("中斷後不能用：%v", m)
	}
}

func TestSigninExpiresAndNeedsClientWithoutRegistration(t *testing.T) {
	e := setup(t)
	f := newFakeRemote(t)
	f.noDCR = true
	err := e.call(t, owner, "xbot.apps.add_mcp_server", ServerIn{Name: "crm", Transport: TransportHTTP, URL: f.srv.URL + "/mcp", Auth: AuthOAuth2}, nil)
	if codeOf(err) != ops.CodeInvalidInput || !strings.Contains(err.Error(), "用戶端 ID") {
		t.Fatalf("不開放動態註冊要用戶端 ID：%v", err)
	}
	var c Connection
	conns, _ := e.svc.list(ctx, "")
	c = conns[0]
	var out ConnectOut
	f.noDCR = false
	e.must(t, "xbot.apps.signin", SigninIn{ID: c.ID}, &out)
	e.advance(16 * time.Minute)
	var st SigninResult
	e.must(t, "xbot.apps.signin_status", FlowIn{FlowID: out.Signin.FlowID}, &st)
	if st.Status != "error" || !strings.Contains(st.Error, "太久") {
		t.Fatalf("15 分鐘沒登入要取消：%+v", st)
	}
	// 拒絕存取。
	e.must(t, "xbot.apps.signin", SigninIn{ID: c.ID}, &out)
	u, _ := url.Parse(out.Signin.AuthURL)
	rec := httptest.NewRecorder()
	e.svc.CallbackHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, CallbackPath+"?error=access_denied&state="+u.Query().Get("state"), nil))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "沒有允許") {
		t.Fatalf("拒絕存取：%d %s", rec.Code, rec.Body.String())
	}
	// 回呼帶的不是 http(s) 網址開頭時拒絕。
	if err := e.call(t, owner, "xbot.apps.signin", SigninIn{ID: c.ID}, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.redirect("javascript:alert(1)")
	if err == nil {
		t.Fatal("redirect_base 要是網址")
	}
}

func TestAPIKeyServerAndStdioServerEnvStayPerBot(t *testing.T) {
	e := setup(t)
	f := newFakeRemote(t)
	f.apiHeader, f.apiKey = "X-Api-Key", "sk-live-abcd1234"
	var out ConnectOut
	e.must(t, "xbot.apps.add_mcp_server", ServerIn{Name: "shop-inventory", Transport: TransportHTTP, URL: f.srv.URL + "/mcp", Auth: AuthAPIKey,
		Token: "sk-live-abcd1234", TokenHeader: "X-Api-Key", Headers: map[string]string{"X-Shop-Id": "88213"}}, &out)
	c := out.Connection
	if c.Status != StatusOK || c.SecretHint != "1234" || !slices.Equal(c.HeaderNames, []string{"X-Shop-Id"}) || len(c.Tools) != 2 {
		t.Fatalf("API 金鑰連接：%+v", c)
	}
	// 換錯的金鑰：測試失敗，狀態寫明。
	var conn Connection
	e.must(t, "xbot.apps.update", UpdateIn{ID: c.ID, Token: "wrong-key-0000"}, &conn)
	if conn.Status != StatusError || !strings.Contains(conn.Error, "金鑰") {
		t.Fatalf("錯的金鑰：%+v", conn)
	}

	// 本機程式：只給阿哲；缺環境變數時停用並標出缺哪個。
	e.must(t, "xbot.apps.add_mcp_server", ServerIn{Name: "report-builder", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "@acme/report"},
		Env: map[string]string{"REPORT_API_KEY": ""}, BotIDs: []string{e.other.ID}}, &out)
	if out.Connection.Status != StatusDisabled || !slices.Equal(out.Connection.MissingEnv, []string{"REPORT_API_KEY"}) {
		t.Fatalf("缺環境變數：%+v", out.Connection)
	}
	if s, _ := e.svc.Servers(ctx, e.other.ID); len(s) != 1 { // 只有 shop-inventory
		t.Fatalf("缺環境變數的不給：%+v", s)
	}
	e.must(t, "xbot.apps.update", UpdateIn{ID: out.Connection.ID, Env: map[string]string{"REPORT_API_KEY": "rk-secret"}}, &conn)
	if conn.Status != StatusChecking || len(conn.MissingEnv) != 0 {
		t.Fatalf("補上後：%+v", conn)
	}
	servers, _ := e.svc.Servers(ctx, e.other.ID)
	var rb hermes.AppServer
	for _, s := range servers {
		if s.Name == "report-builder" {
			rb = s
		}
	}
	cmd, args := rb.Launch()
	if rb.Secret["REPORT_API_KEY"] != "rk-secret" || cmd != "/bin/sh" || args[3] != "npx" || args[2] != "/hermes/.app-report-builder.env" {
		t.Fatalf("本機程式：%+v %s %v", rb, cmd, args)
	}
	if s, _ := e.svc.Servers(ctx, e.bot.ID); slices.ContainsFunc(s, func(a hermes.AppServer) bool { return a.Name == "report-builder" }) {
		t.Fatal("沒授權的 Bot 不能拿到環境變數")
	}
	// 名字不能和 XBot 自己的撞。
	if err := e.call(t, owner, "xbot.apps.add_mcp_server", ServerIn{Name: "xbot", Transport: TransportStdio, Command: "x"}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("保留名字：%v", err)
	}
	if err := e.call(t, owner, "xbot.apps.add_mcp_server", ServerIn{Name: "shop-inventory", Transport: TransportStdio, Command: "x"}, nil); codeOf(err) != ops.CodeConflict {
		t.Fatalf("重複名字：%v", err)
	}
}

func TestBotListsOnlyItsOwnAppsAndRequestsMissingOnes(t *testing.T) {
	e := setup(t)
	f := newFakeRemote(t)
	f.apiHeader, f.apiKey = "Authorization", "Bearer k-1234567"
	var out ConnectOut
	e.must(t, "xbot.apps.add_mcp_server", ServerIn{Name: "mine", Transport: TransportHTTP, URL: f.srv.URL + "/mcp", Auth: AuthAPIKey, Token: "k-1234567",
		BotIDs: []string{e.other.ID}}, &out)
	bot := ops.Bot(e.bot.ID, "小秘")
	var list ListOut
	if err := e.call(t, bot, "xbot.apps.list", ListIn{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Connections) != 0 {
		t.Fatalf("Bot 只看得到自己能用的：%+v", list)
	}
	var req RequestOut
	if err := e.call(t, bot, "xbot.apps.request", RequestIn{AppID: "notion", Reason: "要整理會議記錄"}, &req); err != nil {
		t.Fatal(err)
	}
	if req.Status != "pending" || req.ItemID == "" {
		t.Fatalf("請使用者連接：%+v", req)
	}
	it, _ := e.db.GetInboxItem(ctx, req.ItemID)
	if it.Kind != "signin" || it.BotID != e.bot.ID || !strings.Contains(it.Title, "Notion") {
		t.Fatalf("待我處理卡：%+v", it)
	}
	var again RequestOut
	_ = e.call(t, bot, "xbot.apps.request", RequestIn{AppID: "notion"}, &again)
	if again.ItemID != req.ItemID {
		t.Fatal("同一個請求不要重複出卡")
	}
	if err := e.call(t, owner, "xbot.apps.request", RequestIn{AppID: "notion"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("只有 Bot 能請求：%v", err)
	}
	// Bot 不能自己連接或授權。
	if err := e.call(t, bot, "xbot.apps.grant_to_bot", GrantIn{ID: out.Connection.ID, BotID: e.bot.ID}, nil); err == nil {
		t.Fatal("Bot 不能授權自己")
	}
}

func TestEnvFileQuotesValues(t *testing.T) {
	a := hermes.AppServer{Name: "report", Command: "npx", Secret: map[string]string{"K": "it's", "A": "x y"}}
	if string(a.EnvFile()) != "A='x y'\nK='it'\\''s'\n" {
		t.Fatalf("env 檔要跳脫單引號：%q", a.EnvFile())
	}
}
