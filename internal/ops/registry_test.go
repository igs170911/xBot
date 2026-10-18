package ops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"xbot/internal/store"
)

var ctx = context.Background()

type testEnv struct {
	db       *store.DB
	reg      *Registry
	creates  atomic.Int32
	deletes  atomic.Int32
	events   atomic.Int32
	failNext atomic.Bool
}

type nestedOpts struct {
	Token string `json:"token,omitempty"`
	Color string `json:"color,omitempty" enum:"red,blue"`
}

type createIn struct {
	Name     string     `json:"name" required:"true" max:"20"`
	BotID    string     `json:"bot_id,omitempty" resource:"bot"`
	APIKey   string     `json:"api_key,omitempty"`
	Pwd      string     `json:"pwd,omitempty" secret:"true"`
	Options  nestedOpts `json:"options,omitzero"`
	Contacts []string   `json:"contacts,omitempty" resource:"bot"`
}

type createOut struct {
	Name    string `json:"name"`
	Preview bool   `json:"preview,omitempty"`
	N       int32  `json:"n"`
}

type deleteIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot"`
}

type deleteOut struct {
	Deleted bool `json:"deleted"`
}

type evilIn struct{}
type evilOut struct {
	WriteErr string `json:"write_err"`
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	env := &testEnv{db: db}
	env.reg = New(Config{DB: db, Notify: func(store.Event) { env.events.Add(1) }})
	RegisterBuiltin(env.reg)
	Register(env.reg, Op[createIn, createOut]{
		Name: "xbot.test.create", Summary: "建立測試資料", Scope: ScopeManage, Risk: RiskSafe,
		Handle: func(ctx context.Context, call *Call, in createIn) (createOut, error) {
			if env.failNext.Swap(false) {
				return createOut{}, Limit("太多了")
			}
			if call.DryRun {
				return createOut{Name: in.Name, Preview: true}, nil
			}
			n := env.creates.Add(1)
			return createOut{Name: in.Name, N: n}, call.DB.SetSetting(ctx, "created."+in.Name, n)
		},
	})
	Register(env.reg, Op[deleteIn, deleteOut]{
		Name: "xbot.test.delete", Summary: "刪除 Bot", Scope: ScopeManage, Risk: RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *Call, in deleteIn) (deleteOut, error) {
			if call.DryRun {
				return deleteOut{}, nil
			}
			env.deletes.Add(1)
			return deleteOut{Deleted: true}, call.DB.DeleteBot(ctx, in.BotID)
		},
	})
	Register(env.reg, Op[evilIn, evilOut]{
		Name: "xbot.test.evil", Summary: "預演時偷寫", Scope: ScopeOperate, Risk: RiskSafe,
		Handle: func(ctx context.Context, call *Call, _ evilIn) (evilOut, error) {
			err := call.DB.SetSetting(ctx, "evil", true)
			if err == nil {
				err = call.DB.Tx(ctx, func(q *store.Queries) error { return q.SetSetting(ctx, "evil2", true) })
			}
			if err == nil {
				return evilOut{}, nil
			}
			return evilOut{WriteErr: err.Error()}, nil
		},
	})
	return env
}

func (env *testEnv) bot(t *testing.T, name string) store.Bot {
	t.Helper()
	b, err := env.db.CreateBot(ctx, store.Bot{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func agent(scopes []Scope, res Resources) Identity {
	return Identity{Kind: KindAgent, ID: "tok1", Name: "Claude Code", Scopes: scopes, Resources: res, Via: "mcp"}
}

func (env *testEnv) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	var err error
	switch table {
	case "audit_log":
		var rows []store.AuditEntry
		rows, err = env.db.ListAudit(ctx, store.AuditFilter{})
		n = len(rows)
	case "event":
		var rows []store.Event
		rows, err = env.db.ListEvents(ctx, "", 0, 0)
		n = len(rows)
	case "approval":
		var rows []store.Approval
		rows, err = env.db.ListApprovals(ctx, "")
		n = len(rows)
	case "inbox_item":
		var rows []store.InboxItem
		rows, err = env.db.ListInbox(ctx, "")
		n = len(rows)
	case "setting":
		var m map[string]json.RawMessage
		m, err = env.db.ListSettings(ctx)
		n = len(m)
	default:
		t.Fatalf("不認得 %s", table)
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustErr(t *testing.T, err error, code Code) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("想要錯誤 %s，結果成功", code)
	}
	e := AsError(err)
	if e.Code != code {
		t.Fatalf("想要 %s，得到 %v", code, err)
	}
	return e
}

func mustOK(t *testing.T) func(Result, error) Result {
	return func(res Result, err error) Result {
		t.Helper()
		if err != nil {
			t.Fatalf("不應該失敗：%v", err)
		}
		return res
	}
}

func lastAudit(t *testing.T, env *testEnv) store.AuditEntry {
	t.Helper()
	rows, err := env.db.ListAudit(ctx, store.AuditFilter{Limit: 1})
	if err != nil || len(rows) == 0 {
		t.Fatalf("沒有稽核紀錄：%v", err)
	}
	return rows[0]
}

func TestUnknownOpAndIdentity(t *testing.T) {
	env := newEnv(t)
	_, err := env.reg.Invoke(ctx, Owner("ui"), "xbot.nope.nope", nil, Options{})
	mustErr(t, err, CodeNotFound)

	for _, id := range []Identity{{}, {Kind: KindAgent}, {Kind: "alien", ID: "x"}} {
		_, err := env.reg.Invoke(ctx, id, "xbot.system.ping", nil, Options{})
		mustErr(t, err, CodeForbidden)
	}
}

func TestScopeDenied(t *testing.T) {
	env := newEnv(t)
	reader := agent([]Scope{ScopeRead}, Resources{})
	_, err := env.reg.Invoke(ctx, reader, "xbot.test.create", json.RawMessage(`{"name":"a"}`), Options{RequestID: "r1"})
	e := mustErr(t, err, CodeForbidden)
	if !strings.Contains(e.Message, "管理") || e.Retryable {
		t.Fatalf("錯誤訊息要說缺哪個權限：%+v", e)
	}
	if env.creates.Load() != 0 || env.count(t, "setting") != 0 {
		t.Fatal("權限不足不能執行")
	}
	if a := lastAudit(t, env); a.Status != "error" || a.ErrorCode != "forbidden" || a.Actor != "agent:tok1" || a.Via != "mcp" {
		t.Fatalf("被擋的呼叫要記稽核：%+v", a)
	}
	// 權限範圍是集合：只有 manage 不能讀。
	manager := agent([]Scope{ScopeManage}, Resources{})
	_, err = env.reg.Invoke(ctx, manager, "xbot.system.ping", nil, Options{})
	mustErr(t, err, CodeForbidden)
	mustOK(t)(env.reg.Invoke(ctx, manager, "xbot.test.create", json.RawMessage(`{"name":"a"}`), Options{}))
	// 只有使用者本人有 admin。
	_, err = env.reg.Invoke(ctx, Bot("b1", "小秘"), "xbot.settings.get", json.RawMessage(`{"name":"x"}`), Options{})
	mustErr(t, err, CodeForbidden)
	mustOK(t)(env.reg.Invoke(ctx, Owner("ui"), "xbot.settings.get", json.RawMessage(`{"name":"x"}`), Options{}))
}

func TestResourceScope(t *testing.T) {
	env := newEnv(t)
	a, b := env.bot(t, "甲"), env.bot(t, "乙")
	limited := agent([]Scope{ScopeManage}, Resources{Bots: []string{a.ID}})

	mustOK(t)(env.reg.Invoke(ctx, limited, "xbot.test.create", json.RawMessage(`{"name":"x","bot_id":"`+a.ID+`"}`), Options{}))
	_, err := env.reg.Invoke(ctx, limited, "xbot.test.create", json.RawMessage(`{"name":"x","bot_id":"`+b.ID+`"}`), Options{})
	if e := mustErr(t, err, CodeForbidden); !strings.Contains(e.Message, b.ID) {
		t.Fatalf("要說是哪個 Bot：%s", e.Message)
	}
	// 陣列裡的資源也要檢查。
	_, err = env.reg.Invoke(ctx, limited, "xbot.test.create", json.RawMessage(`{"name":"x","contacts":["`+a.ID+`","`+b.ID+`"]}`), Options{})
	mustErr(t, err, CodeForbidden)
	// 空陣列＝一個都不能碰；nil＝不限制。
	none := agent([]Scope{ScopeManage}, Resources{Bots: []string{}})
	_, err = env.reg.Invoke(ctx, none, "xbot.test.create", json.RawMessage(`{"name":"x","bot_id":"`+a.ID+`"}`), Options{})
	mustErr(t, err, CodeForbidden)
	mustOK(t)(env.reg.Invoke(ctx, agent([]Scope{ScopeManage}, Resources{}), "xbot.test.create", json.RawMessage(`{"name":"x","bot_id":"`+b.ID+`"}`), Options{}))
	// 沒有指定資源的操作照常可用。
	mustOK(t)(env.reg.Invoke(ctx, none, "xbot.test.create", json.RawMessage(`{"name":"y"}`), Options{}))

	// Bot 只能碰自己。
	self := Bot(a.ID, "甲")
	if !self.CanAccess(ResourceBot, a.ID) || self.CanAccess(ResourceBot, b.ID) || self.HasScope(ScopeManage) {
		t.Fatalf("Bot 身分範圍不對：%+v", self)
	}
	if !Owner("ui").CanAccess(ResourceHost, "anything") {
		t.Fatal("使用者本人不受資源範圍限制")
	}
}

func TestAgentIdentityFromToken(t *testing.T) {
	id, err := Agent(store.AgentToken{ID: "t1", Name: "Codex", Scopes: []string{"read", "operate"},
		Resources: json.RawMessage(`{"bots":["b1"]}`)}, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if id.Actor() != "agent:t1" || !id.HasScope(ScopeOperate) || id.HasScope(ScopeAdmin) || !id.CanAccess(ResourceBot, "b1") ||
		id.CanAccess(ResourceBot, "b2") || !id.CanAccess(ResourceTeam, "t") {
		t.Fatalf("身分不對：%+v", id)
	}
	if _, err := Agent(store.AgentToken{ID: "t1", Scopes: []string{"root"}}, "mcp"); err == nil {
		t.Fatal("不認得的權限範圍要報錯")
	}
	if id, _ := Agent(store.AgentToken{ID: "t1", Resources: json.RawMessage(`{}`)}, "mcp"); !id.CanAccess(ResourceBot, "any") {
		t.Fatal("沒有限制資源時不限制")
	}
}

func TestRequestIDReplay(t *testing.T) {
	env := newEnv(t)
	owner := Owner("ui")
	in := json.RawMessage(`{"name":"a"}`)
	first := mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.test.create", in, Options{RequestID: "r1"}))
	// 同內容、不同排版也算同一個。
	again := mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{ "name" : "a" }`), Options{RequestID: "r1"}))
	if env.creates.Load() != 1 || !again.Replayed || string(again.Output) != string(first.Output) || first.Replayed {
		t.Fatalf("重送不能重做：creates=%d first=%s again=%+v", env.creates.Load(), first.Output, again)
	}
	events := env.count(t, "event")

	// 同一個 request_id 用在不同內容。
	_, err := env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":"b"}`), Options{RequestID: "r1"})
	mustErr(t, err, CodeConflict)
	// 不同身分的 request_id 互不影響。
	mustOK(t)(env.reg.Invoke(ctx, agent([]Scope{ScopeManage}, Resources{}), "xbot.test.create", in, Options{RequestID: "r1"}))
	if env.creates.Load() != 2 {
		t.Fatalf("不同身分要各做一次：%d", env.creates.Load())
	}
	if env.count(t, "event") != events+1 {
		t.Fatal("重送不應該再發事件")
	}

	// 失敗的呼叫不佔住 request_id，可以重試。
	env.failNext.Store(true)
	_, err = env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":"c"}`), Options{RequestID: "r2"})
	mustErr(t, err, CodeLimit)
	res := mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":"c"}`), Options{RequestID: "r2"}))
	if res.Replayed || env.creates.Load() != 3 {
		t.Fatalf("失敗後重試要真的執行：%+v", res)
	}

	// 處理中的 request_id 回 conflict（可重試）。
	if _, _, err := env.db.ClaimOpRequest(ctx, "owner", "r3", "xbot.test.create", paramsHash("xbot.test.create", in), 1e9); err != nil {
		t.Fatal(err)
	}
	_, err = env.reg.Invoke(ctx, owner, "xbot.test.create", in, Options{RequestID: "r3"})
	if e := mustErr(t, err, CodeConflict); !e.Retryable {
		t.Fatal("處理中要可以重試")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	env := newEnv(t)
	b := env.bot(t, "甲")
	tables := []string{"audit_log", "event", "approval", "inbox_item", "setting"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = env.count(t, tb)
	}
	ag := agent([]Scope{ScopeManage, ScopeOperate}, Resources{})

	res := mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.create", json.RawMessage(`{"name":"a"}`), Options{DryRun: true, RequestID: "r1"}))
	if !res.DryRun || res.NeedsApproval || !strings.Contains(string(res.Output), `"preview":true`) {
		t.Fatalf("預演結果不對：%+v", res)
	}
	// confirm 的動作預演：告訴呼叫端會需要核准，但不建立核准。
	res = mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b.ID+`"}`), Options{DryRun: true}))
	if !res.DryRun || !res.NeedsApproval {
		t.Fatalf("confirm 預演要標出需要核准：%+v", res)
	}
	// handler 偷寫也寫不進去。
	res = mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.evil", nil, Options{DryRun: true}))
	if !strings.Contains(string(res.Output), "write_err") || strings.Contains(string(res.Output), `"write_err":""`) {
		t.Fatalf("預演時資料庫要是唯讀的：%s", res.Output)
	}
	// 預演時輸入錯誤、權限不足也不寫稽核。
	_, err := env.reg.Invoke(ctx, ag, "xbot.test.create", json.RawMessage(`{}`), Options{DryRun: true})
	mustErr(t, err, CodeInvalidInput)

	for _, tb := range tables {
		if n := env.count(t, tb); n != before[tb] {
			t.Errorf("預演後 %s 從 %d 筆變成 %d 筆", tb, before[tb], n)
		}
	}
	if env.creates.Load() != 0 || env.deletes.Load() != 0 || env.events.Load() != 0 {
		t.Fatal("預演不能真的執行或發通知")
	}
	if _, err := env.db.GetOpRequest(ctx, ag.Actor(), "r1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("預演不能佔住 request_id：%v", err)
	}
	// 同一個 request_id 之後真的執行。
	res = mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.create", json.RawMessage(`{"name":"a"}`), Options{RequestID: "r1"}))
	if res.Replayed || res.DryRun || env.creates.Load() != 1 {
		t.Fatalf("真的執行：%+v", res)
	}
	// 不是預演時 evil 寫得進去（證明上面的唯讀是因為預演）。
	res = mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.evil", nil, Options{}))
	if string(res.Output) != `{"write_err":""}` {
		t.Fatalf("一般執行應該能寫：%s", res.Output)
	}
}

func TestConfirmGoesToInbox(t *testing.T) {
	env := newEnv(t)
	b := env.bot(t, "甲")
	ag := agent([]Scope{ScopeManage}, Resources{})
	in := json.RawMessage(`{"bot_id":"` + b.ID + `"}`)

	_, err := env.reg.Invoke(ctx, ag, "xbot.test.delete", in, Options{RequestID: "r1"})
	e := mustErr(t, err, CodePendingApproval)
	if e.NeedsApprovalID == "" || e.Retryable {
		t.Fatalf("要回核准 id：%+v", e)
	}
	if env.deletes.Load() != 0 {
		t.Fatal("沒核准不能執行")
	}
	if _, err := env.db.GetBot(ctx, b.ID); err != nil {
		t.Fatal("Bot 不應該被刪")
	}
	a, err := env.db.GetApproval(ctx, e.NeedsApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "pending" || a.Action != "xbot.test.delete" || a.BotID != b.ID || a.RequestedBy != "agent:tok1" || a.RequestID != "r1" {
		t.Fatalf("核准內容不對：%+v", a)
	}
	items, _ := env.db.ListInbox(ctx, "open")
	if len(items) != 1 || items[0].Kind != "approval" || items[0].Ref != a.ID || items[0].BotID != b.ID ||
		!strings.Contains(items[0].Title, "Claude Code") || !strings.Contains(items[0].Title, "刪除 Bot") {
		t.Fatalf("待我處理卡片不對：%+v", items)
	}
	if au := lastAudit(t, env); au.Status != "pending_approval" || au.Target != b.ID {
		t.Fatalf("稽核要記等待核准：%+v", au)
	}
	if env.events.Load() != 1 {
		t.Fatalf("送去核准要發事件：%d", env.events.Load())
	}

	// 重送：同一個核准，不會多開一張。
	_, err = env.reg.Invoke(ctx, ag, "xbot.test.delete", in, Options{RequestID: "r1"})
	if e2 := mustErr(t, err, CodePendingApproval); e2.NeedsApprovalID != a.ID {
		t.Fatalf("重送要回同一個核准：%s", e2.NeedsApprovalID)
	}
	if env.count(t, "approval") != 1 || env.count(t, "inbox_item") != 1 {
		t.Fatal("重送不能多開核准")
	}

	// 還沒允許不能執行。
	_, err = env.reg.ExecuteApproved(ctx, a.ID)
	mustErr(t, err, CodeConflict)

	// 允許後執行一次，按兩次也只做一次。
	if err := env.db.DecideApproval(ctx, a.ID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	res := mustOK(t)(env.reg.ExecuteApproved(ctx, a.ID))
	if string(res.Output) != `{"deleted":true}` || env.deletes.Load() != 1 {
		t.Fatalf("核准後要執行：%+v", res)
	}
	res = mustOK(t)(env.reg.ExecuteApproved(ctx, a.ID))
	if !res.Replayed || env.deletes.Load() != 1 {
		t.Fatalf("第二次要回第一次的結果：%+v", res)
	}
	// 代理用原本的 request_id 重送，拿到結果。
	res = mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.delete", in, Options{RequestID: "r1"}))
	if !res.Replayed || string(res.Output) != `{"deleted":true}` {
		t.Fatalf("核准執行後重送要拿到結果：%+v", res)
	}
	if au := lastAudit(t, env); au.Status != "ok" || au.Actor != "agent:tok1" || au.Op != "xbot.test.delete" {
		t.Fatalf("核准後執行要記稽核（記在原本的代理名下）：%+v", au)
	}

	// 沒帶 request_id 的 confirm 呼叫，被拒絕後重送同一個 request_id 回 forbidden。
	b2 := env.bot(t, "乙")
	in2 := json.RawMessage(`{"bot_id":"` + b2.ID + `"}`)
	_, err = env.reg.Invoke(ctx, ag, "xbot.test.delete", in2, Options{})
	e = mustErr(t, err, CodePendingApproval)
	if err := env.db.DecideApproval(ctx, e.NeedsApprovalID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	mustOK(t)(env.reg.ExecuteApproved(ctx, e.NeedsApprovalID))
	if _, err := env.db.GetBot(ctx, b2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("沒帶 request_id 的核准也要能執行")
	}

	b3 := env.bot(t, "丙")
	_, err = env.reg.Invoke(ctx, ag, "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b3.ID+`"}`), Options{RequestID: "r9"})
	e = mustErr(t, err, CodePendingApproval)
	if err := env.db.DecideApproval(ctx, e.NeedsApprovalID, "denied", "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = env.reg.Invoke(ctx, ag, "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b3.ID+`"}`), Options{RequestID: "r9"})
	mustErr(t, err, CodeForbidden)

	// 對不存在的 Bot 要求核准：not_found，也不佔住 request_id。
	_, err = env.reg.Invoke(ctx, ag, "xbot.test.delete", json.RawMessage(`{"bot_id":"沒有"}`), Options{RequestID: "r10"})
	mustErr(t, err, CodeNotFound)
	if _, err := env.db.GetOpRequest(ctx, ag.Actor(), "r10"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("失敗要釋放 request_id：%v", err)
	}
}

func TestConfirmOwnerAndPolicyHook(t *testing.T) {
	env := newEnv(t)
	b := env.bot(t, "甲")
	// 使用者本人直接執行。
	mustOK(t)(env.reg.Invoke(ctx, Owner("ui"), "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b.ID+`"}`), Options{}))
	if env.deletes.Load() != 1 || env.count(t, "approval") != 0 {
		t.Fatal("使用者本人不用核准")
	}
	// 代為核准（C21）：換掉 policy。
	var seen *Info
	env.reg.SetPolicy(func(_ context.Context, call *Call) (Decision, error) {
		seen = call.Op
		if call.Op.NoDelegate {
			return NeedApproval, nil
		}
		return Execute, nil
	})
	b2 := env.bot(t, "乙")
	_, err := env.reg.Invoke(ctx, agent([]Scope{ScopeManage}, Resources{}), "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b2.ID+`"}`), Options{})
	mustErr(t, err, CodePendingApproval)
	if seen == nil || seen.Name != "xbot.test.delete" {
		t.Fatal("policy 要拿到操作資訊")
	}
	env.reg.SetPolicy(func(context.Context, *Call) (Decision, error) { return Execute, nil })
	mustOK(t)(env.reg.Invoke(ctx, agent([]Scope{ScopeManage}, Resources{}), "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b2.ID+`"}`), Options{}))
	if env.deletes.Load() != 2 {
		t.Fatal("policy 允許時直接執行")
	}
}

func TestDynamicRisk(t *testing.T) {
	env := newEnv(t)
	type capsIn struct {
		Cap int `json:"cap" min:"0"`
	}
	type capsOut struct {
		OK bool `json:"ok"`
	}
	Register(env.reg, Op[capsIn, capsOut]{
		Name: "xbot.test.set_caps", Summary: "改花費上限", Scope: ScopeAdmin, Risk: RiskSafe,
		RiskOf: func(in capsIn) Risk {
			if in.Cap > 10 {
				return RiskConfirm
			}
			return RiskSafe
		},
		Handle: func(context.Context, *Call, capsIn) (capsOut, error) { return capsOut{OK: true}, nil },
	})
	ag := agent([]Scope{ScopeAdmin}, Resources{})
	mustOK(t)(env.reg.Invoke(ctx, ag, "xbot.test.set_caps", json.RawMessage(`{"cap":5}`), Options{}))
	_, err := env.reg.Invoke(ctx, ag, "xbot.test.set_caps", json.RawMessage(`{"cap":50}`), Options{})
	mustErr(t, err, CodePendingApproval)
	if info, _ := env.reg.Get("xbot.test.set_caps"); !info.DynamicRisk {
		t.Fatal("Info 要標出風險依輸入而定")
	}
}

func TestAuditMasking(t *testing.T) {
	env := newEnv(t)
	b := env.bot(t, "甲")
	in := json.RawMessage(`{"name":"a","bot_id":"` + b.ID + `","api_key":"sk-live-123","pwd":"hunter2","options":{"token":"tok-xyz","color":"red"}}`)
	mustOK(t)(env.reg.Invoke(ctx, Owner("ui"), "xbot.test.create", in, Options{RequestID: "r1"}))
	a := lastAudit(t, env)
	p := string(a.Params)
	for _, secret := range []string{"sk-live-123", "hunter2", "tok-xyz"} {
		if strings.Contains(p, secret) {
			t.Fatalf("稽核不能有密鑰 %s：%s", secret, p)
		}
	}
	for _, want := range []string{`"api_key":"***"`, `"pwd":"***"`, `"token":"***"`, `"color":"red"`, `"name":"a"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("稽核參數少了 %s：%s", want, p)
		}
	}
	if a.Status != "ok" || a.Op != "xbot.test.create" || a.RequestID != "r1" || a.Target != b.ID || a.Actor != "owner" || a.Via != "ui" {
		t.Fatalf("稽核欄位不對：%+v", a)
	}

	// 核准卡片也遮蔽；核准的原始輸入（只在內部）保留，才能在核准後執行。
	type secretIn struct {
		BotID string `json:"bot_id" required:"true" resource:"bot"`
		Value string `json:"value" secret:"true"`
	}
	Register(env.reg, Op[secretIn, deleteOut]{Name: "xbot.test.set_secret", Summary: "設密鑰", Scope: ScopeAdmin, Risk: RiskConfirm,
		Handle: func(context.Context, *Call, secretIn) (deleteOut, error) { return deleteOut{}, nil }})
	_, err := env.reg.Invoke(ctx, agent([]Scope{ScopeAdmin}, Resources{}), "xbot.test.set_secret",
		json.RawMessage(`{"bot_id":"`+b.ID+`","value":"s3cr3t"}`), Options{})
	e := mustErr(t, err, CodePendingApproval)
	ap, _ := env.db.GetApproval(ctx, e.NeedsApprovalID)
	items, _ := env.db.ListInbox(ctx, "")
	if strings.Contains(string(ap.Payload), "s3cr3t") || strings.Contains(string(items[0].Payload), "s3cr3t") ||
		strings.Contains(string(lastAudit(t, env).Params), "s3cr3t") {
		t.Fatalf("核准內容不能有密鑰：%s／%s", ap.Payload, items[0].Payload)
	}
	if !strings.Contains(string(ap.Input), "s3cr3t") {
		t.Fatal("原始輸入要保留給核准後執行")
	}
	if b, _ := json.Marshal(ap); strings.Contains(string(b), "s3cr3t") {
		t.Fatal("Approval 轉成 JSON 不能帶出原始輸入")
	}
}

func TestMaskNames(t *testing.T) {
	for name, want := range map[string]bool{
		"api_key": true, "apiKey": true, "APIKey": true, "key": true, "access_token": true, "Password": true,
		"client-secret": true, "openai_api_key": true, "cookie": true,
		"name": false, "max_tokens": false, "monkey": false, "keyboard": false, "bot_id": false, "request_id": false, "": false,
	} {
		if got := IsSecretName(name); got != want {
			t.Errorf("IsSecretName(%q)=%v，想要 %v", name, got, want)
		}
	}
	got := string(Mask(json.RawMessage(`{"list":[{"secret":"x","n":1}],"m":{"token":null},"ok":"y"}`), nil))
	if got != `{"list":[{"n":1,"secret":"***"}],"m":{"token":null},"ok":"y"}` {
		t.Fatalf("巢狀遮蔽不對：%s", got)
	}
	if string(Mask(json.RawMessage(`not json`), nil)) != "{}" {
		t.Fatal("讀不懂的內容不能原樣記")
	}
}

func TestInvalidInput(t *testing.T) {
	env := newEnv(t)
	owner := Owner("ui")
	_, err := env.reg.Invoke(ctx, owner, "xbot.test.create",
		json.RawMessage(`{"name":"","extra":1,"options":{"color":"green"},"contacts":["a",3]}`), Options{})
	e := mustErr(t, err, CodeInvalidInput)
	got := map[string]string{}
	for _, f := range e.Fields {
		got[f.Field] = f.Message
	}
	want := map[string]string{"name": "不能是空的", "extra": "不認得這個欄位", "options.color": "只能是 red、blue", "contacts[1]": "要是文字"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("欄位 %s：想要 %q，得到 %q（全部：%v）", k, v, got[k], got)
		}
	}
	if !strings.HasPrefix(e.Message, "輸入有問題：") || e.Retryable {
		t.Fatalf("訊息：%s", e.Message)
	}
	if env.creates.Load() != 0 {
		t.Fatal("輸入錯誤不能執行")
	}
	_, err = env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`[1]`), Options{})
	mustErr(t, err, CodeInvalidInput)
	_, err = env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":`), Options{})
	mustErr(t, err, CodeInvalidInput)
	_, err = env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":"這個名字超過二十個字這個名字超過二十個字這個"}`), Options{})
	if e := mustErr(t, err, CodeInvalidInput); e.Fields[0].Message != "最多 20 個字" {
		t.Fatalf("長度：%+v", e.Fields)
	}
	// 會改東西的操作，輸入錯誤也記稽核。
	if a := lastAudit(t, env); a.ErrorCode != "invalid_input" {
		t.Fatalf("會改東西的操作輸入錯誤也要記：%+v", a)
	}
}

func TestEventsAndReadAudit(t *testing.T) {
	env := newEnv(t)
	owner := Owner("ui")
	mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.system.ping", json.RawMessage(`{"echo":"hi"}`), Options{}))
	if env.count(t, "event") != 0 || env.count(t, "audit_log") != 0 {
		t.Fatal("使用者查看東西不發事件、不記稽核")
	}
	reader := agent([]Scope{ScopeRead}, Resources{})
	res := mustOK(t)(env.reg.Invoke(ctx, reader, "xbot.system.ping", nil, Options{}))
	if !strings.Contains(string(res.Output), `"caller":"agent:tok1"`) || !strings.Contains(string(res.Output), `"api":"xbot.v1"`) {
		t.Fatalf("ping：%s", res.Output)
	}
	if env.count(t, "event") != 0 || env.count(t, "audit_log") != 1 {
		t.Fatal("代理查看要記稽核、不發事件")
	}
	b := env.bot(t, "甲")
	mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.test.create", json.RawMessage(`{"name":"a","bot_id":"`+b.ID+`"}`), Options{RequestID: "r1"}))
	evs, _ := env.db.ListEvents(ctx, b.ID, 0, 0)
	if len(evs) != 1 || evs[0].Kind != EventOp || !strings.Contains(string(evs[0].Payload), `"op":"xbot.test.create"`) ||
		!strings.Contains(string(evs[0].Payload), `"request_id":"r1"`) || env.events.Load() != 1 {
		t.Fatalf("會改東西要發事件：%+v", evs)
	}
	// 操作刪掉了 Bot：事件記成全域的，不會因外鍵失敗。
	b2 := env.bot(t, "乙")
	mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.test.delete", json.RawMessage(`{"bot_id":"`+b2.ID+`"}`), Options{}))
	evs, _ = env.db.ListEvents(ctx, "", 0, 0)
	if last := evs[len(evs)-1]; last.BotID != "" || !strings.Contains(string(last.Payload), "xbot.test.delete") {
		t.Fatalf("刪掉 Bot 後的事件：%+v", last)
	}
}

func TestSettingsGet(t *testing.T) {
	env := newEnv(t)
	owner := Owner("ui")
	if err := env.db.SetSetting(ctx, "ui.theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := env.db.SetSetting(ctx, "model.api_key", "sk-123"); err != nil {
		t.Fatal(err)
	}
	res := mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.settings.get", json.RawMessage(`{"name":"ui.theme"}`), Options{}))
	if string(res.Output) != `{"name":"ui.theme","set":true,"value":"dark"}` {
		t.Fatalf("設定：%s", res.Output)
	}
	res = mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.settings.get", json.RawMessage(`{"name":"model.api_key"}`), Options{}))
	if string(res.Output) != `{"name":"model.api_key","set":true,"secret":true}` {
		t.Fatalf("密鑰只能說已設定：%s", res.Output)
	}
	res = mustOK(t)(env.reg.Invoke(ctx, owner, "xbot.settings.get", json.RawMessage(`{"name":"none"}`), Options{}))
	if string(res.Output) != `{"name":"none","set":false}` {
		t.Fatalf("沒設定：%s", res.Output)
	}
}

func TestRegisterRules(t *testing.T) {
	reg := New(Config{})
	ok := Op[PingIn, PingOut]{Name: "xbot.a.b", Summary: "x", Scope: ScopeRead, Risk: RiskSafe,
		Handle: func(context.Context, *Call, PingIn) (PingOut, error) { return PingOut{}, nil }}
	Register(reg, ok)
	for name, op := range map[string]Op[PingIn, PingOut]{
		"重複":       ok,
		"沒前綴":      func() Op[PingIn, PingOut] { o := ok; o.Name = "bots.create"; return o }(),
		"大寫":       func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.Bots.create"; return o }(),
		"太多段":      func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.a.b.c"; return o }(),
		"沒說明":      func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.a.c"; o.Summary = ""; return o }(),
		"範圍錯":      func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.a.d"; o.Scope = "root"; return o }(),
		"風險錯":      func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.a.e"; o.Risk = ""; return o }(),
		"沒 handle": func() Op[PingIn, PingOut] { o := ok; o.Name = "xbot.a.f"; o.Handle = nil; return o }(),
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s：應該 panic", name)
				}
			}()
			Register(reg, op)
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("輸入不是 struct 應該 panic")
			}
		}()
		Register(reg, Op[string, PingOut]{Name: "xbot.a.g", Summary: "x", Scope: ScopeRead, Risk: RiskSafe,
			Handle: func(context.Context, *Call, string) (PingOut, error) { return PingOut{}, nil }})
	}()
	if list := reg.List(); len(list) != 1 || list[0].Name != "xbot.a.b" {
		t.Fatalf("失敗的登錄不能留下：%+v", list)
	}
}

func TestList(t *testing.T) {
	env := newEnv(t)
	list := env.reg.List()
	var names []string
	for _, i := range list {
		names = append(names, i.Name)
	}
	if strings.Join(names, ",") != "xbot.settings.get,xbot.system.ping,xbot.test.create,xbot.test.delete,xbot.test.evil" {
		t.Fatalf("要依名稱排序：%v", names)
	}
	del, ok := env.reg.Get("xbot.test.delete")
	if !ok || del.Domain != "test" || del.Action != "delete" || del.Risk != RiskConfirm || del.Scope != ScopeManage ||
		!del.Mutates || !del.NoDelegate || del.Input.Property("bot_id").Resource != "bot" || del.Output.Property("deleted").Type != "boolean" {
		t.Fatalf("Info 不對：%+v", del)
	}
	if ping, _ := env.reg.Get("xbot.system.ping"); ping.Mutates {
		t.Fatal("read 操作不算會改東西")
	}
	if _, err := json.Marshal(list); err != nil {
		t.Fatalf("List 要能轉成 JSON：%v", err)
	}
}

func TestErrorFormat(t *testing.T) {
	b, _ := json.Marshal(pendingError("ap1"))
	if string(b) != `{"code":"pending_approval","message":"已送到「待我處理」，等你按允許後才會執行","retryable":false,"needs_approval_id":"ap1"}` {
		t.Fatalf("錯誤格式：%s", b)
	}
	b, _ = json.Marshal(Forbidden("不行"))
	if string(b) != `{"code":"forbidden","message":"不行","retryable":false}` {
		t.Fatalf("錯誤格式：%s", b)
	}
	cause := errors.New("disk on fire")
	in := AsError(cause)
	if in.Code != CodeInternal || !in.Retryable || strings.Contains(in.Message, "disk") || !errors.Is(in, cause) {
		t.Fatalf("內部錯誤不能洩漏原因：%+v", in)
	}
	if b, _ := json.Marshal(in); strings.Contains(string(b), "disk") {
		t.Fatal("JSON 不能帶出內部原因")
	}
	if AsError(store.ErrNotFound).Code != CodeNotFound || AsError(store.ErrAlreadyDecided).Code != CodeConflict || AsError(nil) != nil {
		t.Fatal("錯誤對應不對")
	}
	if e := Errorf(CodeLimit, "最多 %d 個", 3); e.Message != "最多 3 個" || e.Retryable {
		t.Fatalf("Errorf：%+v", e)
	}
	many := InvalidInput(FieldError{"a", "必填"}, FieldError{"b", "必填"}, FieldError{"c", "必填"}, FieldError{"d", "必填"})
	if many.Message != "輸入有問題：a 必填；b 必填；c 必填；還有 1 個" || len(many.Fields) != 4 {
		t.Fatalf("多個欄位：%s", many.Message)
	}
}
