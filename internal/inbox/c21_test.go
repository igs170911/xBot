package inbox

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/delegation"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// C21 代為核准（MCP-10）的測試：授權類別、每日次數與金額、期限、永遠不能代為核准的動作、
// 紀錄卡與 10 分鐘內復原。

type postIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	Text  string `json:"text" max:"200"`
}

type postOut struct {
	PostID string `json:"post_id"`
}

type payIn struct {
	BotID        string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	AmountMicros int64  `json:"amount_micros" min:"0"`
}

type c21env struct {
	*env
	posted   atomic.Int32
	reverted atomic.Int32
}

// setupC21 登錄幾個假的 confirm 操作：對外發送（可復原）、寄信（不能復原）、付款（帶金額）。
func setupC21(t *testing.T) *c21env {
	t.Helper()
	e := &c21env{env: setup(t)}
	delegation.Register(e.reg)
	ops.Register(e.reg, ops.Op[postIn, postOut]{
		Name: "xbot.test.post", Summary: "發一則貼文", Scope: ops.ScopeOperate, Risk: ops.RiskConfirm,
		Category: ops.CategoryExternalSend,
		Handle: func(_ context.Context, call *ops.Call, in postIn) (postOut, error) {
			if !call.DryRun {
				e.posted.Add(1)
			}
			return postOut{PostID: "p-" + in.Text}, nil
		},
		Revert: func(_ context.Context, _ *ops.Call, _ postIn, out postOut) error {
			if out.PostID == "" {
				return ops.Conflict("沒有貼文")
			}
			e.reverted.Add(1)
			return nil
		},
	})
	ops.Register(e.reg, ops.Op[postIn, postOut]{
		Name: "xbot.test.mail", Summary: "寄一封信", Scope: ops.ScopeOperate, Risk: ops.RiskConfirm,
		Category: ops.CategoryExternalSend,
		Handle:   func(context.Context, *ops.Call, postIn) (postOut, error) { return postOut{PostID: "m"}, nil },
	})
	ops.Register(e.reg, ops.Op[payIn, postOut]{
		Name: "xbot.test.pay", Summary: "付一筆款", Scope: ops.ScopeOperate, Risk: ops.RiskConfirm,
		Category: ops.CategorySpendSmall,
		AmountOf: func(in payIn) int64 { return in.AmountMicros },
		Handle:   func(context.Context, *ops.Call, payIn) (postOut, error) { return postOut{PostID: "pay"}, nil },
	})
	return e
}

// token 建一個真的代理權杖（授權存在 delegation 表，要有對應的權杖）。
func (e *c21env) token(t *testing.T, name string) ops.Identity {
	t.Helper()
	tok, err := e.db.CreateAgentToken(bg, store.AgentToken{Name: name, TokenHash: "h-" + name, Scopes: []string{"read", "operate", "manage"}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ops.Agent(tok, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *c21env) grant(t *testing.T, agentID string, in map[string]any) delegation.View {
	t.Helper()
	in["id"], in["enabled"] = agentID, true
	if _, ok := in["days"]; !ok {
		in["days"] = 7
	}
	return get[delegation.View](t, e.env, owner, "xbot.agents.set_delegation", in)
}

// request 讓 who 呼叫一個 confirm 操作，回傳核准 id。
func (e *c21env) request(t *testing.T, who ops.Identity, op string, in map[string]any) string {
	t.Helper()
	_, err := e.call(who, op, in)
	oe := asErr(err)
	if oe == nil || oe.Code != ops.CodePendingApproval {
		t.Fatalf("應該等核准：%v", err)
	}
	return oe.NeedsApprovalID
}

func (e *c21env) bot(t *testing.T) string {
	t.Helper()
	b, err := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func TestDelegatedApprovalFlow(t *testing.T) {
	e := setupC21(t)
	cc := e.token(t, "Claude Code")
	botID := e.bot(t)
	apID := e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": "秋季新品"})

	// 預設關閉：沒有授權不能代為核准。
	if _, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("沒有授權：%v", err)
	}
	// 代理不能幫自己設授權（只有你本人；而且 set_delegation 永遠不能代為核准）。
	if _, err := e.call(cc, "xbot.agents.set_delegation", map[string]any{"id": cc.ID, "enabled": true, "categories": []string{"external_send"},
		"daily_count": 5, "days": 7}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能設授權：%v", err)
	}
	if info, _ := e.reg.Get("xbot.agents.set_delegation"); !info.NoDelegate {
		t.Fatal("改代為核准授權要 NoDelegate")
	}
	// 授權不包含這個類別：還是不行。
	e.grant(t, cc.ID, map[string]any{"categories": []string{"spend_small"}, "daily_count": 5, "daily_amount_micros": 1_000_000})
	if _, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("類別不對：%v", err)
	}
	v := e.grant(t, cc.ID, map[string]any{"categories": []string{"external_send", "spend_small"}, "daily_count": 5, "daily_amount_micros": 1_000_000})
	if !v.Enabled || len(v.Categories) != 2 || v.UsedToday != 0 || len(v.Never) != 4 {
		t.Fatalf("授權：%+v", v)
	}
	// 代理可以查自己的授權，不能查別人的。
	if got := get[delegation.View](t, e.env, cc, "xbot.agents.get_delegation", map[string]any{"id": cc.ID}); !got.Enabled {
		t.Fatalf("查自己的：%+v", got)
	}
	other := e.token(t, "Codex")
	if _, err := e.call(other, "xbot.agents.get_delegation", map[string]any{"id": cc.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("不能查別人的：%v", err)
	}
	// 代理只能代為允許，不能代為退回。
	if _, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "deny"}); code(err) != ops.CodeForbidden {
		t.Fatalf("不能代為退回：%v", err)
	}
	// 預演：檢查授權但不記錄、不執行。
	d := get[Outcome](t, e.env, cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}, ops.Options{DryRun: true})
	if d.Approval.Status != "approved" || d.Executed || e.posted.Load() != 0 {
		t.Fatalf("預演：%+v", d)
	}
	if n, _, _ := e.db.DelegationUsedSince(bg, cc.ID, 0); n != 0 {
		t.Fatal("預演不應該計次")
	}

	o := get[Outcome](t, e.env, cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"})
	if !o.Executed || o.Approval.Status != "approved" || o.Approval.DecidedBy != "agent:"+cc.ID || e.posted.Load() != 1 {
		t.Fatalf("代為核准後要執行：%+v", o)
	}
	// 再按一次：回第一次的結果，不會再做一次、也不再計次。
	if o2 := get[Outcome](t, e.env, cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); !o2.Executed || e.posted.Load() != 1 {
		t.Fatalf("按兩次：%+v", o2)
	}
	// 別的代理不能再按。
	if _, err := e.call(e.token(t, "別人"), "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("別的代理：%v", err)
	}

	// 原本的核准卡結掉，另外留一張紀錄卡（C16、F08）。
	if open := get[InboxListOut](t, e.env, owner, "xbot.inbox.list", map[string]any{}); len(open.Items) != 0 {
		t.Fatalf("核准卡應該結掉了：%+v", open.Items)
	}
	all := get[InboxListOut](t, e.env, owner, "xbot.inbox.list", map[string]any{"kind": "delegated", "status": "all"})
	if len(all.Items) != 1 {
		t.Fatalf("要有一張代為核准紀錄卡：%+v", all.Items)
	}
	card := all.Items[0]
	if card.Title != "已由 Claude Code（MCP）代為核准 · 對外發送" || !strings.Contains(card.Body, "今天第 1／5 次") ||
		card.Ref != apID || card.Read || card.Group != "done" || card.UndoUntil == 0 || card.Resolution != "delegated" {
		t.Fatalf("紀錄卡：%+v", card)
	}
	var p delegatedPayload
	_ = json.Unmarshal(card.Payload, &p)
	if !p.Reversible || p.ActorName != "Claude Code" || p.Category != "external_send" || p.Count != 1 || p.Limit != 5 {
		t.Fatalf("紀錄卡內容：%+v", p)
	}
	// 有事件推給畫面（SSE）。
	evs, _ := e.db.ListEvents(bg, "", 0, 0)
	created := false
	for _, ev := range evs {
		if ev.Kind == "inbox.created" && strings.Contains(string(ev.Payload), `"kind":"delegated"`) {
			created = true
		}
	}
	if !created {
		t.Fatal("要發 inbox.created 事件")
	}
	if g := get[delegation.View](t, e.env, owner, "xbot.agents.get_delegation", map[string]any{"id": cc.ID}); g.UsedToday != 1 {
		t.Fatalf("今天用了 1 次：%+v", g)
	}

	// 只有你本人能復原；復原會收回動作，之後不能再復原。
	if _, err := e.call(cc, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能復原：%v", err)
	}
	u := get[ItemOut](t, e.env, owner, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID})
	if u.Item.Resolution != "undone" || u.Item.UndoUntil != 0 || e.reverted.Load() != 1 {
		t.Fatalf("復原：%+v reverted=%d", u.Item, e.reverted.Load())
	}
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID}); code(err) != ops.CodeConflict || e.reverted.Load() != 1 {
		t.Fatalf("不能復原兩次：%v", err)
	}
	entries, _ := e.db.ListAudit(bg, store.AuditFilter{Actor: "owner"})
	reverted := false
	for _, en := range entries {
		reverted = reverted || (en.Op == "xbot.test.post" && en.Status == "reverted")
	}
	if !reverted {
		t.Fatalf("復原要寫稽核：%+v", entries)
	}
}

func TestDelegatedUndoWindowAndIrreversible(t *testing.T) {
	e := setupC21(t)
	cc := e.token(t, "Claude Code")
	botID := e.bot(t)
	e.grant(t, cc.ID, map[string]any{"categories": []string{"external_send"}, "daily_count": 10})

	// 不能復原的動作（寄出的信）：紀錄卡沒有復原。
	mail := e.request(t, cc, "xbot.test.mail", map[string]any{"bot_id": botID})
	get[Outcome](t, e.env, cc, "xbot.approvals.decide", map[string]any{"approval_id": mail, "decision": "allow"})
	post := e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": "a"})
	get[Outcome](t, e.env, cc, "xbot.approvals.decide", map[string]any{"approval_id": post, "decision": "allow"})
	cards := get[InboxListOut](t, e.env, owner, "xbot.inbox.list", map[string]any{"kind": "delegated", "status": "all"}).Items
	if len(cards) != 2 {
		t.Fatalf("%+v", cards)
	}
	byRef := map[string]Item{}
	for _, c := range cards {
		byRef[c.Ref] = c
	}
	if byRef[mail].UndoUntil != 0 || byRef[post].UndoUntil == 0 || !strings.Contains(byRef[post].Body, "今天第 2／10 次") {
		t.Fatalf("寄信不能復原、貼文可以：%+v", cards)
	}
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": byRef[mail].ID}); code(err) != ops.CodeConflict {
		t.Fatalf("不能復原：%v", err)
	}
	// 超過 10 分鐘：不能復原。
	clock = func() time.Time { return time.Now().Add(11 * time.Minute) }
	t.Cleanup(func() { clock = time.Now })
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": byRef[post].ID}); code(err) != ops.CodeConflict || e.reverted.Load() != 0 {
		t.Fatalf("超過 10 分鐘：%v", err)
	}
}

func TestDelegationLimits(t *testing.T) {
	e := setupC21(t)
	cc := e.token(t, "Claude Code")
	botID := e.bot(t)
	e.grant(t, cc.ID, map[string]any{"categories": []string{"external_send", "spend_small"}, "daily_count": 3, "daily_amount_micros": 5_000_000})

	allow := func(apID string) error {
		_, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"})
		return err
	}
	// 金額：US$3 可以；再 US$3 超過每日 US$5。
	if err := allow(e.request(t, cc, "xbot.test.pay", map[string]any{"bot_id": botID, "amount_micros": 3_000_000})); err != nil {
		t.Fatal(err)
	}
	over := e.request(t, cc, "xbot.test.pay", map[string]any{"bot_id": botID, "amount_micros": 3_000_000})
	if err := allow(over); code(err) != ops.CodeLimit {
		t.Fatalf("超過金額上限：%v", err)
	}
	if a, _ := e.db.GetApproval(bg, over); a.Status != "pending" {
		t.Fatalf("超過上限的核准要留給你本人：%+v", a)
	}
	// 次數：第 2、3 次可以，第 4 次超過。
	if err := allow(e.request(t, cc, "xbot.test.pay", map[string]any{"bot_id": botID, "amount_micros": 2_000_000})); err != nil {
		t.Fatal(err)
	}
	if err := allow(e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": "b"})); err != nil {
		t.Fatal(err)
	}
	fourth := e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": "c"})
	if err := allow(fourth); code(err) != ops.CodeLimit {
		t.Fatalf("超過每日次數：%v", err)
	}
	// 你本人照樣可以允許。
	if o := get[Outcome](t, e.env, owner, "xbot.approvals.decide", map[string]any{"approval_id": fourth, "decision": "allow"}); !o.Executed {
		t.Fatalf("%+v", o)
	}
	// 授權過期：不能再代為核准。
	d, _ := e.db.GetDelegation(bg, cc.ID)
	d.ExpiresAt, d.DailyCount = time.Now().Add(-time.Minute).UnixMilli(), 100
	if _, err := e.db.PutDelegation(bg, d); err != nil {
		t.Fatal(err)
	}
	if err := allow(e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": "d"})); code(err) != ops.CodeForbidden {
		t.Fatalf("過期：%v", err)
	}
	if g := get[delegation.View](t, e.env, owner, "xbot.agents.get_delegation", map[string]any{"id": cc.ID}); g.Enabled || !g.Expired {
		t.Fatalf("過期的授權：%+v", g)
	}
	// 收回授權。
	if g := get[delegation.View](t, e.env, owner, "xbot.agents.set_delegation", map[string]any{"id": cc.ID, "enabled": false}); g.Enabled || g.ExpiresAt != 0 {
		t.Fatalf("收回：%+v", g)
	}
	// 輸入檢查。
	if _, err := e.call(owner, "xbot.agents.set_delegation", map[string]any{"id": cc.ID, "enabled": true, "categories": []string{}, "daily_count": 0}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("開啟要有類別、次數、天數：%v", err)
	}
	if _, err := e.call(owner, "xbot.agents.set_delegation", map[string]any{"id": cc.ID, "enabled": true, "categories": []string{"delete_bot"}, "daily_count": 1, "days": 1}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("不認得的類別：%v", err)
	}
}

// 同時很多個代為核准：每日次數不會被超過（檢查和記錄在同一個交易）。
func TestDelegationLimitConcurrent(t *testing.T) {
	e := setupC21(t)
	cc := e.token(t, "Claude Code")
	botID := e.bot(t)
	e.grant(t, cc.ID, map[string]any{"categories": []string{"external_send"}, "daily_count": 3})
	var ids []string
	for i := range 8 {
		ids = append(ids, e.request(t, cc, "xbot.test.post", map[string]any{"bot_id": botID, "text": string(rune('a' + i))}))
	}
	var wg sync.WaitGroup
	var ok, limited atomic.Int32
	for _, id := range ids {
		wg.Go(func() {
			_, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": id, "decision": "allow"})
			switch code(err) {
			case "":
				ok.Add(1)
			case ops.CodeLimit:
				limited.Add(1)
			default:
				t.Errorf("意外的錯誤：%v", err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 3 || limited.Load() != 5 || e.posted.Load() != 3 {
		t.Fatalf("只能成功 3 次：ok=%d limited=%d posted=%d", ok.Load(), limited.Load(), e.posted.Load())
	}
}

// 驗收（PLAN C21）：刪除類動作無論如何都不能代為核准。
func TestDeleteNeverDelegable(t *testing.T) {
	e := setupC21(t)
	cc := e.token(t, "Claude Code")
	// 給到最大的授權：所有類別、最高次數與金額。
	e.grant(t, cc.ID, map[string]any{"categories": []string{"external_send", "spend_small", "publish", "sign"},
		"daily_count": 100, "daily_amount_micros": 1_000_000_000, "days": 30})
	botID := e.bot(t)
	apID := e.request(t, cc, "xbot.bots.delete", map[string]any{"id": botID})
	if _, err := e.call(cc, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("刪除不能代為核准：%v", err)
	}
	if a, _ := e.db.GetApproval(bg, apID); a.Status != "pending" {
		t.Fatalf("還是等你本人：%+v", a)
	}
	if _, err := e.db.GetBot(bg, botID); err != nil {
		t.Fatal("Bot 不應該被刪")
	}
	// 代理看不到不能代按的核准（除了自己要求的）。
	other := e.token(t, "Codex")
	e.grant(t, other.ID, map[string]any{"categories": []string{"external_send", "spend_small", "publish", "sign"}, "daily_count": 100})
	if l := get[ApprovalsListOut](t, e.env, other, "xbot.approvals.list", map[string]any{}); len(l.Approvals) != 0 {
		t.Fatalf("別的代理不該看到刪除的核准：%+v", l)
	}
	if _, err := e.call(other, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("別的代理也不行：%v", err)
	}
	// 就算有人把刪除類的操作標了類別，NoDelegate 還是優先。
	if _, err := delegation.Authorize(bg, e.db.Queries, cc, ops.Info{Name: "xbot.bots.delete", Category: ops.CategoryExternalSend, NoDelegate: true},
		store.Approval{ID: "x"}, 0, false); code(err) != ops.CodeForbidden {
		t.Fatalf("NoDelegate 優先：%v", err)
	}
	// 所有登錄的操作：刪除、移除類，以及改授權、改代理權限的，都不能代為核准。
	for _, info := range e.reg.List() {
		dangerous := strings.HasSuffix(info.Name, ".delete") || strings.HasSuffix(info.Name, ".remove") ||
			strings.HasSuffix(info.Name, "_provider") && strings.Contains(info.Name, "remove") ||
			strings.HasPrefix(info.Name, "xbot.agents.") || info.Name == "xbot.usage.set_caps"
		if dangerous && info.Delegable() {
			t.Errorf("%s 不應該能代為核准", info.Name)
		}
	}
}

// 有授權的代理看得到授權類別內的核准，可以等、可以代按；沒授權的只看得到自己要求的。
func TestDelegateSeesCoveredApprovals(t *testing.T) {
	e := setupC21(t)
	requester, delegate := e.token(t, "Bot 管家"), e.token(t, "Claude Code")
	botID := e.bot(t)
	apID := e.request(t, requester, "xbot.test.post", map[string]any{"bot_id": botID, "text": "x"})
	if l := get[ApprovalsListOut](t, e.env, delegate, "xbot.approvals.list", map[string]any{}); len(l.Approvals) != 0 {
		t.Fatalf("沒授權看不到：%+v", l)
	}
	e.grant(t, delegate.ID, map[string]any{"categories": []string{"external_send"}, "daily_count": 2})
	if l := get[ApprovalsListOut](t, e.env, delegate, "xbot.approvals.list", map[string]any{"status": "pending"}); len(l.Approvals) != 1 {
		t.Fatalf("有授權看得到：%+v", l)
	}
	o := get[Outcome](t, e.env, delegate, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"})
	if !o.Executed {
		t.Fatalf("%+v", o)
	}
	// 要求的代理用 wait 拿到結果。
	if w := get[Outcome](t, e.env, requester, "xbot.approvals.wait", map[string]any{"approval_id": apID, "timeout_s": 1}); !w.Executed {
		t.Fatalf("%+v", w)
	}
	// 資源範圍外的 Bot：不能代按。
	limited := e.token(t, "受限")
	e.grant(t, limited.ID, map[string]any{"categories": []string{"external_send"}, "daily_count": 2})
	limited.Resources = ops.Resources{Bots: []string{}}
	ap2 := e.request(t, requester, "xbot.test.post", map[string]any{"bot_id": botID, "text": "y"})
	if _, err := e.call(limited, "xbot.approvals.decide", map[string]any{"approval_id": ap2, "decision": "allow"}); code(err) != ops.CodeForbidden {
		t.Fatalf("範圍外：%v", err)
	}
}
