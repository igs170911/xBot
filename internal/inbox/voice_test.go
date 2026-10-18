package inbox

import (
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// E32：通話中用說的不能允許 confirm 級的操作（要在畫面上按）；用說的退回可以；
// Bot 用 xbot.approvals.request 請你決定、看不出高風險的事可以用說的允許。
func TestDecideByVoice(t *testing.T) {
	e, _, calls := setupC12(t)
	b, _ := e.db.CreateBot(bg, store.Bot{Name: "小編"})
	_, err := e.call(agent("a1", nil), "xbot.test.flaky", map[string]any{"bot_id": b.ID}, ops.Options{RequestID: "r1"})
	apID := asErr(err).NeedsApprovalID
	_, err = e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "allow", "via": "voice"})
	if oe := asErr(err); oe == nil || oe.Code != ops.CodeForbidden || !strings.Contains(oe.Message, "畫面上按確認") {
		t.Fatalf("confirm 級的操作不能用說的允許：%v", err)
	}
	if a, _ := e.db.GetApproval(bg, apID); a.Status != "pending" || calls.Load() != 0 {
		t.Fatalf("被擋下的不能動到核准：%+v（執行 %d 次）", a, calls.Load())
	}
	d := get[Outcome](t, e, owner, "xbot.approvals.decide", map[string]any{"approval_id": apID, "decision": "deny", "via": "voice"})
	if d.Approval.Status != "denied" {
		t.Fatalf("用說的退回：%+v", d)
	}

	// Bot 請你決定的事：花錢的要按；調價這類可以用說的。
	mk := func(subject string) string {
		in, _ := json.Marshal(map[string]string{"subject": subject})
		a, err := e.db.CreateApproval(bg, store.Approval{BotID: b.ID, Action: "xbot.approvals.request", Input: in, Payload: json.RawMessage(`{}`), RequestedBy: "bot:" + b.ID})
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	_, err = e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": mk("付款 NT$1,280 給晨光廣告"), "decision": "allow", "via": "voice"})
	if oe := asErr(err); oe == nil || oe.Code != ops.CodeForbidden {
		t.Fatalf("花錢的事不能用說的允許：%v", err)
	}
	_, err = e.call(owner, "xbot.approvals.decide", map[string]any{"approval_id": mk("調降 5 項商品售價"), "decision": "allow", "via": "voice"})
	if oe := asErr(err); oe != nil && oe.Code == ops.CodeForbidden {
		t.Fatalf("低風險的事可以用說的允許：%v", err)
	}
}
