package collab

import (
	"testing"

	"xbot/internal/store"
)

// TestIdenticalHandoffWithFillerBlocked verifies that repeating identical handoff
// with only filler messages in between is blocked (2026-10-22h windowed dedupe).
func TestIdenticalHandoffWithFillerBlocked(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	scout := tm.bots["Scout"]
	room, _ := tm.s.CreateRoom(tm.ctx, []string{chief, scout}, RoomOptions{Name: "test"})

	// Chief posts handoff
	_, _, _ = tm.s.speak(tm.ctx, room, chief, "@Scout 換你", roomMeta{}, 1)

	// Scout posts substantive work
	_, _, _ = tm.s.speak(tm.ctx, room, scout, "已完成任務，結果如下：data", roomMeta{}, 2)

	// Chief posts same handoff again - should be allowed (new info from Scout in between)
	_, _, _ = tm.s.speak(tm.ctx, room, chief, "@Scout 換你", roomMeta{}, 3)

	// Scout posts filler only
	_, _, _ = tm.s.speak(tm.ctx, room, scout, "好的", roomMeta{}, 4)

	// Chief tries to post identical handoff again - should be BLOCKED (only filler since last)
	recentMsgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room.ID, 0, 20)
	var recentTexts []string
	botSender := "bot:" + chief
	lastNewInfoIdx := -1
	for i := len(recentMsgs) - 1; i >= 0; i-- {
		if HasNewInformation(recentMsgs[i].Content) {
			lastNewInfoIdx = i
			break
		}
	}
	startIdx := 0
	if lastNewInfoIdx >= 0 {
		startIdx = lastNewInfoIdx + 1
	}
	for i := len(recentMsgs) - 1; i >= startIdx; i-- {
		if recentMsgs[i].Sender == botSender {
			recentTexts = append(recentTexts, recentMsgs[i].Content)
		}
	}
	shouldSend, reason := ShouldSendToPeer("@Scout 換你", "answer", false, recentTexts, "handle", true)
	if shouldSend || reason != "duplicate" {
		t.Fatalf("Expected identical handoff after filler to be blocked, got shouldSend=%v reason=%s (recentTexts=%v)", shouldSend, reason, recentTexts)
	}
}
