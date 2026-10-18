package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"xbot/internal/bots"
	"xbot/internal/ops"
	"xbot/internal/store"
)

var bg = context.Background()

type env struct {
	db  *store.DB
	reg *ops.Registry
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	bots.Register(reg, bots.Deps{})
	Register(reg)
	return &env{db: db, reg: reg}
}

var owner = ops.Owner("test")

func agent(id string, botIDs []string) ops.Identity {
	return ops.Identity{Kind: ops.KindAgent, ID: id, Name: "代理" + id, Via: "mcp",
		Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate, ops.ScopeManage}, Resources: ops.Resources{Bots: botIDs}}
}

func (e *env) call(id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(bg, id, op, raw, o)
	return res.Output, err
}

func must[T any](t *testing.T, raw json.RawMessage, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func asErr(err error) *ops.Error {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe
	}
	return nil
}

func code(err error) ops.Code {
	if oe := asErr(err); oe != nil {
		return oe.Code
	}
	return ""
}

// pending 讓代理要求刪掉一個新 Bot，回傳 Bot id 與核准 id。
func (e *env) pending(t *testing.T, a ops.Identity, reqID string) (string, string) {
	t.Helper()
	b, err := e.db.CreateBot(bg, store.Bot{Name: "要刪的"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.call(a, "xbot.bots.delete", map[string]any{"id": b.ID}, ops.Options{RequestID: reqID})
	oe := asErr(err)
	if oe == nil || oe.Code != ops.CodePendingApproval || oe.NeedsApprovalID == "" {
		t.Fatalf("應該等核准：%v", err)
	}
	return b.ID, oe.NeedsApprovalID
}

func TestApproveFlow(t *testing.T) {
	e := setup(t)
	a, other := agent("a1", nil), agent("a2", nil)
	botID, apID := e.pending(t, a, "del-1")

	// 列表：你本人看得到、要求的代理看得到、別的代理看不到。
	l := get[ApprovalsListOut](t, e, owner, "xbot.approvals.list", map[string]any{"status": "pending"})
	if len(l.Approvals) != 1 || l.Approvals[0].Op != "xbot.bots.delete" || l.Approvals[0].RequestedBy != "agent:a1" || l.Approvals[0].Summary == "" {
		t.Fatalf("%+v", l)
	}
	if l := get[ApprovalsListOut](t, e, a, "xbot.approvals.list", map[string]any{}); len(l.Approvals) != 1 {
		t.Fatalf("要求者：%+v", l)
	}
	if l := get[ApprovalsListOut](t, e, other, "xbot.approvals.list", map[string]any{}); len(l.Approvals) != 0 {
		t.Fatalf("別的代理：%+v", l)
	}
	if _, err := e.call(other, "xbot.approvals.wait", map[string]any{"approval_id": apID, "timeout_s": 1}); code(err) != ops.CodeNotFound {
		t.Fatalf("別的代理不能等：%v", err)
	}
	// 待我處理有一張卡。
	items := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{})
	if len(items.Items) != 1 || items.Items[0].Kind != "approval" || items.Items[0].Ref != apID {
		t.Fatalf("%+v", items)
	}

	// 等到逾時：還沒決定。
	o := get[Outcome](t, e, a, "xbot.approvals.wait", map[string]any{"approval_id": apID, "timeout_s": 1})
	if o.Settled || o.Approval.Status != "pending" {
		t.Fatalf("%+v", o)
	}
	// 代理不能決定（沒有代為核准授權，MCP-10），就算是要求的那個代理。
	if _, err := e.call(a, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能核准：%v", err)
	}
	// 預演不改狀態。
	d := get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}, ops.Options{DryRun: true})
	if d.Approval.Status != "approved" || d.Executed {
		t.Fatalf("預演：%+v", d)
	}
	if ap, _ := e.db.GetApproval(bg, apID); ap.Status != "pending" {
		t.Fatal("預演不應該改狀態")
	}

	// 代理在等的時候你按允許：代理拿到執行結果。
	got := make(chan Outcome, 1)
	go func() {
		raw, err := e.call(a, "xbot.approvals.wait", map[string]any{"approval_id": apID, "timeout_s": 10})
		if err != nil {
			t.Error(err)
		}
		var o Outcome
		_ = json.Unmarshal(raw, &o)
		got <- o
	}()
	time.Sleep(100 * time.Millisecond)
	d = get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"})
	if !d.Executed || d.Error != nil || d.Approval.Status != "approved" || d.Approval.DecidedBy != "owner" {
		t.Fatalf("允許：%+v", d)
	}
	var del bots.DeleteOut
	_ = json.Unmarshal(d.Output, &del)
	if !del.Deleted || del.ID != botID {
		t.Fatalf("輸出：%s", d.Output)
	}
	if _, err := e.db.GetBot(bg, botID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("核准後應該刪掉了")
	}
	select {
	case o := <-got:
		if !o.Settled || !o.Executed || o.Approval.Status != "approved" {
			t.Fatalf("等待結果：%+v", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等不到")
	}
	// 代理用同一個 request_id 重送：拿到第一次的結果，不重做。
	raw, err := e.call(a, "xbot.bots.delete", map[string]any{"id": botID}, ops.Options{RequestID: "del-1"})
	if err != nil || string(raw) != string(d.Output) {
		t.Fatalf("重送：%s %v", raw, err)
	}
}

func TestDenyFlow(t *testing.T) {
	e := setup(t)
	a := agent("a1", nil)
	botID, apID := e.pending(t, a, "")
	d := get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "deny"})
	if d.Executed || d.Approval.Status != "denied" {
		t.Fatalf("%+v", d)
	}
	if _, err := e.db.GetBot(bg, botID); err != nil {
		t.Fatal("退回後 Bot 還在")
	}
	if items := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{}); len(items.Items) != 0 {
		t.Fatalf("卡片應該結掉：%+v", items)
	}
	if items := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{"status": "resolved"}); len(items.Items) != 1 {
		t.Fatalf("%+v", items)
	}
	o := get[Outcome](t, e, a, "xbot.approvals.wait", map[string]any{"approval_id": apID, "timeout_s": 5})
	if !o.Settled || o.Executed || o.Approval.Status != "denied" {
		t.Fatalf("%+v", o)
	}
	for _, dec := range []string{"allow", "deny"} {
		if _, err := e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": dec}); code(err) != ops.CodeConflict {
			t.Fatalf("已經決定過：%v", err)
		}
	}
	if _, err := e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": "nope", "decision": "allow"}); code(err) != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}
	if _, err := e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "maybe"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("%v", err)
	}
}

func TestInboxScope(t *testing.T) {
	e := setup(t)
	b1, _ := e.db.CreateBot(bg, store.Bot{Name: "1"})
	b2, _ := e.db.CreateBot(bg, store.Bot{Name: "2"})
	for _, i := range []store.InboxItem{{Kind: "question", BotID: b1.ID, Title: "q1"}, {Kind: "budget", BotID: b2.ID, Title: "b2"}, {Kind: "budget", Title: "全域"}} {
		if _, err := e.db.CreateInboxItem(bg, i); err != nil {
			t.Fatal(err)
		}
	}
	if l := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{}); len(l.Items) != 3 {
		t.Fatalf("%+v", l)
	}
	if l := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{"kind": "budget", "limit": 1}); len(l.Items) != 1 {
		t.Fatalf("%+v", l)
	}
	limited := agent("a", []string{b1.ID})
	if l := get[InboxListOut](t, e, limited, "xbot.inbox.list", map[string]any{}); len(l.Items) != 1 || l.Items[0].BotID != b1.ID {
		t.Fatalf("受限：%+v", l)
	}
	if _, err := e.call(limited, "xbot.inbox.list", map[string]any{"bot_id": b2.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	if l := get[InboxListOut](t, e, agent("free", nil), "xbot.inbox.list", map[string]any{"status": "all"}); len(l.Items) != 3 {
		t.Fatalf("不受限：%+v", l)
	}
}

func TestAuditList(t *testing.T) {
	e := setup(t)
	a, other := agent("a1", nil), agent("a2", nil)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "x"})
	_, _ = e.call(a, "xbot.bots.get", map[string]any{"id": b.ID})
	_, _ = e.call(other, "xbot.bots.get", map[string]any{"id": b.ID})
	_, _ = e.call(owner, "xbot.bots.update", map[string]any{"id": b.ID, "title": "t"})

	all := get[AuditListOut](t, e, owner, "xbot.audit.list", map[string]any{})
	if len(all.Entries) != 3 || all.Entries[0].Op != "xbot.bots.update" {
		t.Fatalf("%+v", all)
	}
	page := get[AuditListOut](t, e, owner, "xbot.audit.list", map[string]any{"limit": 2})
	if len(page.Entries) != 2 || page.NextBefore == 0 {
		t.Fatalf("%+v", page)
	}
	rest := get[AuditListOut](t, e, owner, "xbot.audit.list", map[string]any{"limit": 2, "before": page.NextBefore})
	if len(rest.Entries) != 1 || rest.NextBefore != 0 {
		t.Fatalf("%+v", rest)
	}
	mine := get[AuditListOut](t, e, a, "xbot.audit.list", map[string]any{})
	for _, en := range mine.Entries {
		if en.Actor != "agent:a1" {
			t.Fatalf("代理只看得到自己的：%+v", mine)
		}
	}
	if len(mine.Entries) < 1 {
		t.Fatal("至少有自己的一筆")
	}
	if _, err := e.call(a, "xbot.audit.list", map[string]any{"actor": "owner"}); code(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

func get[T any](t *testing.T, e *env, id ops.Identity, op string, in any, opts ...ops.Options) T {
	t.Helper()
	raw, err := e.call(id, op, in, opts...)
	return must[T](t, raw, err)
}
