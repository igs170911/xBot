package collab

import (
	"testing"

	"xbot/internal/store"
)

// TestDedupeWindowLogic verifies that dedupe window correctly handles:
// 1. Immediate repeat is blocked
// 2. 'A: @S 換你 / S: <real work> / A: @S 換你' is allowed
// 3. 'A: X / B: filler / A: X' is blocked
// (2026-10-22i: fixed dedupe window to check OTHER speaker's last new info)
func TestDedupeWindowLogic(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	scout := tm.bots["Scout"]
	room, _ := tm.s.CreateRoom(tm.ctx, []string{chief, scout}, RoomOptions{Name: "test"})

	// Test 1: Immediate repeat is blocked
	t.Run("immediate_repeat_blocked", func(t *testing.T) {
		// Chief posts a message
		_, _, _ = tm.s.speak(tm.ctx, room, chief, "請開始執行", roomMeta{}, 1)

		// Chief tries to post the same message immediately - should be BLOCKED
		recentMsgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room.ID, 0, 20)
		recentTexts := getRecentTexts(chief, recentMsgs)
		shouldSend, reason := ShouldSendToPeer("請開始執行", "answer", false, recentTexts, "handle", true)
		if shouldSend || reason != "duplicate" {
			t.Errorf("Expected immediate repeat to be blocked, got shouldSend=%v reason=%s (recentTexts=%v)", shouldSend, reason, recentTexts)
		}
	})

	// Test 2: 'A: @S 換你 / S: <real work> / A: @S 換你' is allowed
	t.Run("relay_after_real_work_allowed", func(t *testing.T) {
		// Clear previous messages by creating new room
		room2, _ := tm.s.CreateRoom(tm.ctx, []string{chief, scout}, RoomOptions{Name: "test2"})

		// Chief posts handoff
		_, _, _ = tm.s.speak(tm.ctx, room2, chief, "@Scout 換你", roomMeta{}, 1)

		// Scout posts substantive work
		_, _, _ = tm.s.speak(tm.ctx, room2, scout, "已完成任務，結果如下：data", roomMeta{}, 2)

		// Chief posts same handoff again - should be ALLOWED (real work from Scout in between)
		recentMsgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room2.ID, 0, 20)
		recentTexts := getRecentTexts(chief, recentMsgs)
		shouldSend, reason := ShouldSendToPeer("@Scout 換你", "answer", false, recentTexts, "handle", true)
		if !shouldSend {
			t.Errorf("Expected relay after real work to be allowed, got shouldSend=%v reason=%s (recentTexts=%v)", shouldSend, reason, recentTexts)
		}
	})

	// Test 3: 'A: X / B: filler / A: X' is blocked
	t.Run("repeat_after_filler_blocked", func(t *testing.T) {
		// Clear previous messages by creating new room
		room3, _ := tm.s.CreateRoom(tm.ctx, []string{chief, scout}, RoomOptions{Name: "test3"})

		// Chief posts a message
		_, _, _ = tm.s.speak(tm.ctx, room3, chief, "@Scout 換你", roomMeta{}, 1)

		// Scout posts filler only
		_, _, _ = tm.s.speak(tm.ctx, room3, scout, "好的", roomMeta{}, 2)

		// Chief tries to post same message again - should be BLOCKED (only filler since last)
		recentMsgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room3.ID, 0, 20)
		recentTexts := getRecentTexts(chief, recentMsgs)
		shouldSend, reason := ShouldSendToPeer("@Scout 換你", "answer", false, recentTexts, "handle", true)
		if shouldSend || reason != "duplicate" {
			t.Errorf("Expected repeat after filler to be blocked, got shouldSend=%v reason=%s (recentTexts=%v)", shouldSend, reason, recentTexts)
		}
	})
}

// getRecentTexts extracts recent texts for a bot using the corrected dedupe window logic:
// Find last message with new info from ANOTHER speaker, collect all sender's messages since then.
func getRecentTexts(botID string, recentMsgs []store.Message) []string {
	var recentTexts []string
	botSender := "bot:" + botID

	// Find last message with new info from ANOTHER speaker
	lastOtherNewInfoIdx := -1
	for i := len(recentMsgs) - 1; i >= 0; i-- {
		if recentMsgs[i].Sender != botSender && HasNewInformation(recentMsgs[i].Content) {
			lastOtherNewInfoIdx = i
			break
		}
	}

	// Collect all messages from this bot since that point (including bot's own last message)
	startIdx := 0
	if lastOtherNewInfoIdx >= 0 {
		startIdx = lastOtherNewInfoIdx + 1
	}
	for i := len(recentMsgs) - 1; i >= startIdx; i-- {
		if recentMsgs[i].Sender == botSender {
			recentTexts = append(recentTexts, recentMsgs[i].Content)
		}
	}

	return recentTexts
}
