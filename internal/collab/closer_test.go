package collab

import (
	"testing"

	"xbot/internal/store"
)

// TestCloserDerivation 測試 closer 派生邏輯（2026-10-22）。
func TestCloserDerivation(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	// Case 1: LeadID 優先
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name:   "Test Lead",
		LeadID: writerID,
	}))
	closerID, _ := tm.s.deriveCloser(tm.ctx, room)
	if closerID != writerID {
		t.Errorf("LeadID 優先：got %s, want %s", closerID, writerID)
	}

	// Case 2: CreatedBy bot:
	room2 := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name:      "Test Creator",
		CreatedBy: "bot:" + chiefID,
	}))
	closerID, _ = tm.s.deriveCloser(tm.ctx, room2)
	if closerID != chiefID {
		t.Errorf("CreatedBy：got %s, want %s", closerID, chiefID)
	}
}

// TestConsecBlockedTracking 測試連續阻擋計數（2026-10-22）。
func TestConsecBlockedTracking(t *testing.T) {
	tm := setup(t)
	chiefID := tm.bots["Chief"]

	// 創建一個任務，初始 consec_blocked = 0
	taskID := tm.running(chiefID, map[string]any{"room_id": "test-room", "consec_blocked": 0})
	task, _ := tm.db.GetTask(tm.ctx, taskID)
	m := metaOf(task)
	if m.ConsecBlocked != 0 {
		t.Errorf("初始 consec_blocked 應為 0，got %d", m.ConsecBlocked)
	}

	// 更新為 consec_blocked = 1
	_ = tm.db.PatchTaskMeta(tm.ctx, taskID, map[string]any{"consec_blocked": 1})
	task, _ = tm.db.GetTask(tm.ctx, taskID)
	m = metaOf(task)
	if m.ConsecBlocked != 1 {
		t.Errorf("更新後 consec_blocked 應為 1，got %d", m.ConsecBlocked)
	}
}
