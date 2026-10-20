package inbox

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// setupC12 和 setup 一樣，另外記下推出去的事件，並多一個會失敗一次的 confirm 操作。
func setupC12(t *testing.T) (*env, *[]store.Event, *atomic.Int32) {
	t.Helper()
	e := setup(t)
	var mu sync.Mutex
	var evs []store.Event
	reg := ops.New(ops.Config{DB: e.db, Notify: func(ev store.Event) {
		mu.Lock()
		evs = append(evs, ev)
		mu.Unlock()
	}})
	Register(reg)
	var calls atomic.Int32
	type in struct {
		BotID string `json:"bot_id" resource:"bot"`
	}
	type out struct {
		OK bool `json:"ok"`
	}
	ops.Register(reg, ops.Op[in, out]{Name: "xbot.test.flaky", Summary: "對外發送（第一次會失敗）", Scope: ops.ScopeOperate, Risk: ops.RiskConfirm,
		Handle: func(_ context.Context, call *ops.Call, _ in) (out, error) {
			if call.DryRun {
				return out{}, nil
			}
			if calls.Add(1) == 1 {
				return out{}, ops.Conflict("對方的伺服器暫時沒回應")
			}
			return out{OK: true}, nil
		}})
	e.reg = reg
	return e, &evs, &calls
}

func kinds(evs []store.Event) map[string]int {
	m := map[string]int{}
	for _, e := range evs {
		m[e.Kind]++
	}
	return m
}

// C05 待確認 2：允許了但執行失敗時，approvals.wait 要分得出來（failed＋error），再允許一次成功後就清掉。
func TestWaitReportsExecutionFailure(t *testing.T) {
	e, evs, calls := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	a := agent("a1", nil)
	_, err := e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"})
	oe := asErr(err)
	if oe == nil || oe.Code != ops.CodePendingApproval {
		t.Fatalf("應該等核准：%v", err)
	}
	if k := kinds(*evs); k[notify.KindCreated] != 1 {
		t.Fatalf("開卡要發 inbox.created：%v", k)
	}
	d := get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": oe.NeedsApprovalID, "decision": "allow"})
	if !d.Failed || d.Executed || d.Error == nil || d.Error.Code != ops.CodeConflict {
		t.Fatalf("允許了但失敗：%+v", d)
	}
	// 決定時結掉卡片，執行失敗再把它重新打開、寫上原因：兩次 inbox.updated
	if k := kinds(*evs); k[notify.KindUpdated] != 2 {
		t.Fatalf("決定、失敗重開都要發 inbox.updated：%v", k)
	}
	card := func() store.InboxItem {
		items, err := e.db.ListInboxByRef(bg, oe.NeedsApprovalID)
		if err != nil || len(items) != 1 {
			t.Fatalf("%v %+v", err, items)
		}
		return items[0]
	}
	if c := card(); c.Status != "open" || !strings.Contains(string(c.Payload), "對方的伺服器暫時沒回應") {
		t.Fatalf("失敗要重新打開卡片並寫上原因（才看得到、能再試一次）：%+v %s", c, c.Payload)
	}
	w := get[Outcome](t, e, a, "xbot.approvals.wait", map[string]any{"approval_id": oe.NeedsApprovalID, "timeout_s": 5})
	if !w.Settled || !w.Failed || w.Executed || w.Error == nil || w.Error.Message != "對方的伺服器暫時沒回應" {
		t.Fatalf("等待要回報失敗：%+v", w)
	}
	// 再允許一次：這次成功，失敗紀錄清掉。
	d = get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": oe.NeedsApprovalID, "decision": "allow"})
	if d.Failed || !d.Executed || calls.Load() != 2 {
		t.Fatalf("重試：%+v（%d 次）", d, calls.Load())
	}
	w = get[Outcome](t, e, a, "xbot.approvals.wait", map[string]any{"approval_id": oe.NeedsApprovalID, "timeout_s": 5})
	if !w.Executed || w.Failed || w.Error != nil {
		t.Fatalf("重試後：%+v", w)
	}
	if ap, _ := e.db.GetApproval(bg, oe.NeedsApprovalID); ap.Error != "" {
		t.Fatalf("成功後要清掉錯誤：%q", ap.Error)
	}
	if c := card(); c.Status != "resolved" || c.Resolution != "allow" || strings.Contains(string(c.Payload), "failed") {
		t.Fatalf("再試成功後卡片結掉、拿掉失敗原因：%+v %s", c, c.Payload)
	}
}

// 允許了、執行卻失敗的卡，「先不做了」（dismiss）要能收掉（審查 2026-10-21：原本 dismiss 擋、decide 說已處理過，永遠關不掉）。
// 還沒決定的核准卡照樣不能直接略過。
func TestDismissAFailedApproval(t *testing.T) {
	e, _, _ := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	a := agent("a1", nil)
	_, err := e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"})
	oe := asErr(err)
	items, _ := e.db.ListInboxByRef(bg, oe.NeedsApprovalID)
	if _, err := e.call(owner, "xbot.inbox.dismiss", map[string]any{"inbox_id": items[0].ID}, ops.Options{}); asErr(err) == nil || asErr(err).Code != ops.CodeConflict {
		t.Fatalf("還沒決定的不能略過：%v", err)
	}
	if d := get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": oe.NeedsApprovalID, "decision": "allow"}); !d.Failed {
		t.Fatalf("%+v", d)
	}
	if _, err := e.call(owner, "xbot.inbox.dismiss", map[string]any{"inbox_id": items[0].ID}, ops.Options{}); err != nil {
		t.Fatalf("失敗的卡要能收掉：%v", err)
	}
	if c, _ := e.db.GetInboxItem(bg, items[0].ID); c.Status != store.InboxDismissed {
		t.Fatalf("%+v", c)
	}
}

// F08：退回後 10 分鐘內可以復原，卡片和核准都回到等你決定；已經執行的不能復原。
func TestUndoDeny(t *testing.T) {
	e, _, _ := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	a := agent("a1", nil)
	_, err := e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"})
	apID := asErr(err).NeedsApprovalID
	get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "deny"})
	items := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{"status": "all"})
	if len(items.Items) != 1 || items.Items[0].Resolution != "deny" || items.Items[0].UndoUntil == 0 || !items.Items[0].Read {
		t.Fatalf("退回的卡：%+v", items)
	}
	card := items.Items[0]
	if _, err := e.call(a, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能復原：%v", err)
	}
	u := get[ItemOut](t, e, owner, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID})
	if u.Item.Status != "open" || u.Item.UndoUntil != 0 {
		t.Fatalf("復原後：%+v", u)
	}
	if ap, _ := e.db.GetApproval(bg, apID); ap.Status != "pending" {
		t.Fatalf("核准要回到 pending：%+v", ap)
	}
	// 代理重送同一個 request_id：又是等核准（不是「你沒有允許」）。
	if _, err := e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"}); code(err) != ops.CodePendingApproval {
		t.Fatalf("復原後重送：%v", err)
	}
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("open 的不能復原：%v", err)
	}
	// 允許（執行失敗也算交出去了）：不能復原。
	get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow"})
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": card.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("允許過的不能復原：%v", err)
	}
	// 超過時限也不行。
	_, err = e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r2"})
	ap2 := asErr(err).NeedsApprovalID
	get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": ap2, "decision": "deny"})
	old, _ := e.db.ListInboxByRef(bg, ap2)
	clock = func() time.Time { return time.Now().Add(11 * time.Minute) }
	t.Cleanup(func() { clock = time.Now })
	if _, err := e.call(owner, "xbot.inbox.undo", map[string]any{"inbox_id": old[0].ID}); code(err) != ops.CodeConflict || asErr(err).Message != "已經超過 10 分鐘，不能復原了" {
		t.Fatalf("超過 10 分鐘：%v", err)
	}
}

func TestDismissCountsMarkRead(t *testing.T) {
	e, evs, _ := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "抓蟲"})
	th, _ := e.db.CreateThread(bg, store.Thread{BotID: b.ID, Title: "t"})
	budget, _ := e.db.CreateInboxItem(bg, store.InboxItem{Kind: "budget", BotID: b.ID, Title: "預算用完，已暫停"})
	done, _ := e.db.CreateInboxItem(bg, store.InboxItem{Kind: "done", BotID: b.ID, ThreadID: th.ID, Status: store.InboxResolved, Resolution: "info"})
	_, _ = e.db.CreateInboxItem(bg, store.InboxItem{Kind: "error", BotID: b.ID, ThreadID: th.ID, Status: store.InboxResolved, Resolution: "info"})
	q, _ := e.db.CreateInboxItem(bg, store.InboxItem{Kind: "question", BotID: b.ID, TaskID: "t1", ThreadID: th.ID})

	c := get[CountsOut](t, e, owner, "xbot.inbox.counts", map[string]any{})
	if c.Open != 2 || c.Unread != 4 || c.Groups["decide"].Open != 2 || c.Groups["done"].Unread != 1 || c.Groups["error"].Unread != 1 || c.Groups["mention"].Unread != 0 {
		t.Fatalf("數量：%+v", c)
	}
	if l := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{"status": "all", "group": "done"}); len(l.Items) != 1 || l.Items[0].ID != done.ID || l.Items[0].Group != "done" {
		t.Fatalf("完成：%+v", l)
	}
	if l := get[InboxListOut](t, e, owner, "xbot.inbox.list", map[string]any{"status": "all", "thread_id": th.ID, "unread": true}); len(l.Items) != 3 {
		t.Fatalf("對話串未讀：%+v", l)
	}

	// 提問卡不能略過（Bot 在等，要用 chat.respond）；略過預算卡。
	if _, err := e.call(owner, "xbot.inbox.dismiss", map[string]any{"inbox_id": q.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("提問卡：%v", err)
	}
	if _, err := e.call(agent("a", nil), "xbot.inbox.dismiss", map[string]any{"inbox_id": budget.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能略過：%v", err)
	}
	dm := get[ItemOut](t, e, owner, "xbot.inbox.dismiss", map[string]any{"inbox_id": budget.ID})
	if dm.Item.Status != "dismissed" || dm.Item.UndoUntil == 0 {
		t.Fatalf("略過：%+v", dm)
	}
	if u := get[ItemOut](t, e, owner, "xbot.inbox.undo", map[string]any{"inbox_id": budget.ID}); u.Item.Status != "open" {
		t.Fatalf("復原略過：%+v", u)
	}

	// 打開對話：那條對話串的都標成已讀；再全部標已讀。
	before := len(*evs)
	if m := get[MarkReadOut](t, e, owner, "xbot.inbox.mark_read", map[string]any{"thread_id": th.ID}); m.Marked != 3 {
		t.Fatalf("標已讀：%+v", m)
	}
	if k := kinds((*evs)[before:]); k[notify.KindUpdated] != 3 {
		t.Fatalf("已讀也要發事件：%v", k)
	}
	if _, err := e.call(owner, "xbot.inbox.mark_read", map[string]any{}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("要擇一：%v", err)
	}
	if _, err := e.call(agent("a", nil), "xbot.inbox.mark_read", map[string]any{"all": true}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能標已讀：%v", err)
	}
	get[MarkReadOut](t, e, owner, "xbot.inbox.mark_read", map[string]any{"all": true})
	if c := get[CountsOut](t, e, owner, "xbot.inbox.counts", map[string]any{}); c.Unread != 0 || c.Open != 2 {
		t.Fatalf("全部已讀後：%+v", c)
	}
	// 受限的代理只算得到自己範圍的。
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "別人"})
	if c := get[CountsOut](t, e, agent("lim", []string{other.ID}), "xbot.inbox.counts", map[string]any{}); c.Open != 0 {
		t.Fatalf("受限：%+v", c)
	}
}

// 允許後執行到一半服務重開（op_request 停在 running）：啟動時退回可以再試，卡片以失敗重新打開，approvals.wait 回報失敗
// （審查 2026-10-21：原本永遠「正在執行中」，卡片也結掉了，什麼都不能做）。
func TestRecoverInterruptedApproval(t *testing.T) {
	e, _, _ := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	a := agent("a1", nil)
	_, err := e.call(a, "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"})
	oe := asErr(err)
	items, _ := e.db.ListInboxByRef(bg, oe.NeedsApprovalID)
	// 模擬：使用者允許了，開始執行時服務就停了
	if err := e.db.DecideApproval(bg, oe.NeedsApprovalID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := e.db.SettleInboxItem(bg, items[0].ID, "resolved", "allow", "owner", nil); err != nil {
		t.Fatal(err)
	}
	if _, started, err := e.db.StartApprovedOpRequest(bg, oe.NeedsApprovalID); err != nil || !started {
		t.Fatalf("%v %v", started, err)
	}
	if err := RecoverInterrupted(bg, e.reg, e.db); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.db.GetInboxItem(bg, items[0].ID); c.Status != "open" || !strings.Contains(string(c.Payload), "服務重開") {
		t.Fatalf("卡片要以失敗重新打開：%+v %s", c, c.Payload)
	}
	w := get[Outcome](t, e, a, "xbot.approvals.wait", map[string]any{"approval_id": oe.NeedsApprovalID, "timeout_s": 1})
	if !w.Settled || !w.Failed || w.Executed {
		t.Fatalf("等待要回報失敗：%+v", w)
	}
	if _, started, err := e.db.StartApprovedOpRequest(bg, oe.NeedsApprovalID); err != nil || !started {
		t.Fatalf("要能再試：%v %v", started, err)
	}
}
