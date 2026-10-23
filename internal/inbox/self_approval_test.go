package inbox

import (
	"testing"
	"xbot/internal/ops"
)

// TestSelfApprovalBlocked 驗證 B.3：代理不能核准自己要求的動作（除非有明確授權）。
func TestSelfApprovalBlocked(t *testing.T) {
	e := setup(t)
	a := agent("alice", nil)

	// 代理請求一個需要核准的動作（xbot.bots.delete 是 NoDelegate，代理永遠不能代為核准）
	botID, approvalID := e.pending(t, a, "req-self")

	// 代理試圖核准自己的請求 → 應該被拒絕（因為沒有授權，且刪除操作永遠要本人核准）
	call := &ops.Call{Identity: a, DB: e.db}
	_, err := decide(bg, e.reg, call, DecideIn{
		ApprovalID: approvalID,
		Decision:   "allow",
	})
	if err == nil {
		t.Fatal("代理不應該能核准自己的請求（尤其是 NoDelegate 操作）")
	}
	opErr := ops.AsError(err)
	if opErr.Code != ops.CodeForbidden {
		t.Fatalf("預期 forbidden 錯誤，得到：%s (%s)", opErr.Code, opErr.Message)
	}
	// 錯誤訊息應該明確說明是因為這個操作需要本人核准
	if !contains(opErr.Message, "永遠要你本人核准") && !contains(opErr.Message, "代理不能核准") {
		t.Fatalf("錯誤訊息不明確：%s", opErr.Message)
	}

	// 驗證核准狀態還是 pending
	ap, err := e.db.GetApproval(bg, approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if ap.Status != "pending" {
		t.Fatalf("核准應該還是 pending，實際：%s", ap.Status)
	}

	// 驗證 Bot 還沒被刪除
	_, err = e.db.GetBot(bg, botID)
	if err != nil {
		t.Fatalf("Bot 不應該被刪除：%v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestOwnerCanApprove 驗證使用者本人可以核准任何請求。
func TestOwnerCanApprove(t *testing.T) {
	e := setup(t)
	a := agent("alice", nil)

	// 代理請求一個需要核准的動作
	botID, approvalID := e.pending(t, a, "req-owner")

	// 使用者本人核准 → 應該成功
	call := &ops.Call{Identity: owner, DB: e.db}
	out, err := decide(bg, e.reg, call, DecideIn{
		ApprovalID: approvalID,
		Decision:   "allow",
	})
	if err != nil {
		t.Fatalf("使用者本人應該能核准：%v", err)
	}
	if !out.Executed || out.Approval.Status != "approved" {
		t.Fatalf("動作應該已執行：executed=%v status=%s", out.Executed, out.Approval.Status)
	}

	// 驗證 Bot 已被刪除
	_, err = e.db.GetBot(bg, botID)
	if err == nil {
		t.Fatal("Bot 應該被刪除")
	}
}
