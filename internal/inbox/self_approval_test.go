package inbox

import (
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// TestSelfApprovalBlocked 驗證代理不能核准自己提出的請求（B.3）。
func TestSelfApprovalBlocked(t *testing.T) {
	e := setup(t)
	
	// 建立一個代理請求的核准
	agentA := agent("a1", nil)
	a := store.Approval{
		Action:      "xbot.test.dangerous",
		Status:      "pending",
		RequestedBy: agentA.Actor(),
	}
	a, err := e.db.CreateApproval(bg, a)
	if err != nil {
		t.Fatal(err)
	}
	
	// 同一個代理嘗試核准自己的請求
	_, err = e.call(agentA, "xbot.approvals.decide", DecideIn{
		ApprovalID: a.ID,
		Decision:   "allow",
	})
	
	// 應該被禁止
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	
	opErr := asErr(err)
	if opErr == nil {
		t.Fatalf("expected *ops.Error, got %T", err)
	}
	
	if opErr.Code != ops.CodeForbidden {
		t.Errorf("expected forbidden, got %s", opErr.Code)
	}
	
	if opErr.Message != "代理不能核准自己提出的請求" {
		t.Errorf("unexpected message: %s", opErr.Message)
	}
}

// TestDifferentAgentCanAttemptApprove 驗證不同的代理不會被「自我核准」阻擋。
func TestDifferentAgentCanAttemptApprove(t *testing.T) {
	e := setup(t)
	
	agentA := agent("a1", nil)
	agentB := agent("a2", nil)
	
	// 建立一個代理 A 請求的核准
	a := store.Approval{
		Action:      "xbot.test.safe",
		Status:      "pending",
		RequestedBy: agentA.Actor(),
	}
	a, err := e.db.CreateApproval(bg, a)
	if err != nil {
		t.Fatal(err)
	}
	
	// 代理 B 嘗試核准（應該進入 decideDelegated 流程）
	_, err = e.call(agentB, "xbot.approvals.decide", DecideIn{
		ApprovalID: a.ID,
		Decision:   "allow",
	})
	
	// 預期會因為其他原因失敗（例如沒有授權），但錯誤訊息不應該是「代理不能核准自己提出的請求」
	if err != nil {
		opErr := asErr(err)
		if opErr != nil && opErr.Message == "代理不能核准自己提出的請求" {
			t.Errorf("different agent should not get self-approval error, got: %s", opErr.Message)
		}
	}
}
