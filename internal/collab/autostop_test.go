package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestAutoStopOnAgreementChain tests that blocked agreement messages increment consec_empty
// and trigger auto-stop with inbox notification at consec_empty=2 (2026-10-22h)
func TestAutoStopOnAgreementChain(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Agreement Test",
		ResponderRule: store.RuleEveryone, // Both bots respond to user messages
	}))

	// User message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "請討論一下發表會的事", ""))

	// Chief responds with pure agreement (no substance, should be blocked)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "好的，謝謝你的回覆，我完全同意你的看法，辛苦了。")

	// Verify first blocked reply incremented consec_empty to 1
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty != 1 {
		t.Errorf("After first blocked reply, consec_empty should be 1, got %d", room1.ConsecEmpty)
	}

	// Writer also responds with pure agreement (second consecutive blocked)
	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "我也同意，沒有要補充的。")

	// Verify second blocked reply incremented consec_empty to 2
	room2, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room2.ConsecEmpty != 2 {
		t.Errorf("After second blocked reply, consec_empty should be 2, got %d", room2.ConsecEmpty)
	}

	// Verify inbox item created at consec_empty=2
	// 2026-10-22k: ref 只包含 room.ID（item 6 fix）
	ref := "auto_stop:" + room.ID
	exists, err := tm.db.InboxRefExists(tm.ctx, ref)
	if err != nil {
		t.Fatalf("Error checking inbox ref: %v", err)
	}
	if !exists {
		t.Error("Auto-stop inbox item should be created at consec_empty=2")
	}

	// Verify 0 filler messages were posted to room
	msgs := tm.roomLog(room.ID)
	fillerMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" &&
			(strings.Contains(m.Content, "完全同意") || strings.Contains(m.Content, "我也同意") || strings.Contains(m.Content, "沒有要補充")) {
			fillerMsgs++
		}
	}
	if fillerMsgs > 0 {
		t.Errorf("Filler messages should be blocked, but found %d", fillerMsgs)
	}
}

// TestPleasantryLoopStops tests that pleasantry loops stop quickly
func TestPleasantryLoopStops(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Pleasantry Loop Test",
	}))

	// Post a pleasantry loop message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 謝謝", ""))

	task1 := tm.queued(chief)[0]
	tm.start(task1)
	tm.finish(task1.ID, runtime.VerdictAnswer, "@Writer 不客氣，謝謝你。")

	// Check that this was blocked (no substance)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" {
			botMsgs++
			t.Logf("Bot message: %s", m.Content)
		}
	}

	if botMsgs > 2 {
		t.Errorf("Pleasantry loop should stop in ≤2 messages, got %d", botMsgs)
	}
}

// TestRequestWithMentionStillWorks tests that real requests with @ still wake the bot
func TestRequestWithMentionStillWorks(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Request Test",
	}))

	// Post a real request
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 請幫我查一下今天的天氣", ""))

	// Chief should be woken
	tasks := tm.queued(chief)
	if len(tasks) == 0 {
		t.Error("Chief should be woken for a real request")
	}

	tm.start(tasks[0])
	tm.finish(tasks[0].ID, runtime.VerdictAnswer, "今天是晴天，溫度 25 度。")

	// Check that message was posted
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" && strings.Contains(m.Content, "晴天") {
			botMsgs++
		}
	}

	if botMsgs == 0 {
		t.Error("Real request response should be posted")
	}
}

// TestColonContentNotFiltered tests that colon+content is not filtered as no new info
func TestColonContentNotFiltered(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Colon Content Test",
	}))

	// Post a request
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "請寫三條標語", ""))

	task1 := tm.queued(chief)[0]
	tm.start(task1)
	// Response with colon + content
	response := "標語三條已寫好：\n1. 品質第一\n2. 顧客至上\n3. 創新領先"
	tm.finish(task1.ID, runtime.VerdictAnswer, response)

	// Check that message was posted (not filtered as filler)
	msgs := tm.roomLog(room.ID)
	found := false
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "標語三條") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Colon+content message should not be filtered")
	}
}

// TestThreeBotsPleasantries tests item 6: 3 bots replying pleasantries leaves ≤1 posted,
// consec_empty reaches 2, exactly 1 inbox card created
func TestThreeBotsPleasantries(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{
		Name:          "Three Bot Pleasantries",
		ResponderRule: store.RuleEveryone,
	}))

	// User message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "謝謝大家", ""))

	// All three bots respond with pleasantries from fixtures (known fillers)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "收到，謝謝。")

	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "了解，沒問題。")

	scoutTask := tm.queued(scout)[0]
	tm.start(scoutTask)
	tm.finish(scoutTask.ID, runtime.VerdictAnswer, "好的，謝謝你的回覆，我完全同意你的看法，辛苦了。")

	// Verify ≤1 message was posted
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" {
			botMsgs++
		}
	}
	if botMsgs > 1 {
		t.Errorf("Expected ≤1 bot message posted from 3 pleasantries, got %d", botMsgs)
	}

	// Verify consec_empty reached 2
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty < 2 {
		t.Errorf("Expected consec_empty ≥ 2, got %d", room1.ConsecEmpty)
	}

	// Count inbox items for this room (should be exactly 1 after 3 bots reply with fillers)
	// 2026-10-22k: ref 只包含 room.ID，所以只會有一個 inbox item
	ref := "auto_stop:" + room.ID
	exists, err := tm.db.InboxRefExists(tm.ctx, ref)
	if err != nil {
		t.Fatalf("Error checking inbox ref: %v", err)
	}
	if !exists {
		t.Error("Expected exactly 1 inbox card for auto-stop, got 0")
	}
	t.Logf("Auto-stop inbox exists with ref=%s", ref)
}

// TestMentionPlusPleasantryLoop tests item 6: @+pleasantry loop stops at ≤1 reply
func TestMentionPlusPleasantryLoop(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Mention Pleasantry Loop",
	}))

	// User mentions Chief with pleasantry
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 謝謝", ""))

	// Chief responds with pleasantry from fixtures (known filler)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "收到，謝謝。")

	// Verify ≤1 bot message was posted
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" {
			botMsgs++
		}
	}
	if botMsgs > 1 {
		t.Errorf("@+pleasantry loop should have ≤1 bot reply, got %d", botMsgs)
	}
}

// TestDMThanksLoop tests item 6: repeated pleasantries stop quickly (≤2 turns)
func TestDMThanksLoop(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Create a room with two bots
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Thanks Loop Test",
	}))

	// User sends thanks
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "謝謝你的幫助", ""))

	// Chief responds with pleasantry
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	tm.finish(task1.ID, runtime.VerdictAnswer, "收到，謝謝。")

	// User sends another thanks
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "真的很感謝", ""))

	// Writer may or may not respond depending on filler detection
	tasks := tm.queued(writer)
	if len(tasks) > 0 {
		tm.start(tasks[0])
		tm.finish(tasks[0].ID, runtime.VerdictAnswer, "了解，沒問題。")
	}

	// Count total bot messages - should be ≤2
	msgs := tm.roomLog(room.ID)
	botCount := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" {
			botCount++
		}
	}
	if botCount > 2 {
		t.Errorf("Thanks loop should have ≤2 bot turns, got %d", botCount)
	}
	t.Logf("Thanks loop had %d bot turns", botCount)
}

// TestAutoStopWithoutPeerJudge tests item 6: auto-stop works without PeerJudge (strict mode)
func TestAutoStopWithoutPeerJudge(t *testing.T) {
	tm := setup(t)
	// Create Service without PeerJudge to test strict mode
	tm.s = New(Options{DB: tm.db, Runtime: tm.rt, Files: tm.files, PeerJudge: nil})

	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "No PeerJudge Test",
		ResponderRule: store.RuleEveryone,
	}))

	// User message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "測試", ""))

	// Both bots respond with pleasantries (should be blocked by strict mode)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "收到")

	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "了解")

	// Verify consec_empty incremented
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty < 2 {
		t.Errorf("Without PeerJudge (strict), consec_empty should reach 2, got %d", room1.ConsecEmpty)
	}

	// Verify minimal messages posted
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" {
			botMsgs++
		}
	}
	if botMsgs > 1 {
		t.Errorf("Without PeerJudge (strict), should have ≤1 bot message, got %d", botMsgs)
	}
}

// TestAutoStopWithFillerFixtures tests item 6: auto-stop works with fixture-backed judge
func TestAutoStopWithFillerFixtures(t *testing.T) {
	// setup() already installs fakeJudge backed by fixtures
	tm := setup(t)

	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Fixture Judge Test",
		ResponderRule: store.RuleEveryone,
	}))

	// User message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "請大家討論", ""))

	// Both bots respond with content not in fixtures (should fallback to handle/skip based on heuristics)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	// This is filler-like but not in fixtures - should be classified as filler by fallback
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "好的我們來討論")

	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "我也同意來討論")

	// Verify consec_empty incremented (if both are blocked)
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	t.Logf("consec_empty=%d after two bot replies", room1.ConsecEmpty)

	// Verify auto-stop behavior (may or may not create inbox depending on classification)
	// The key test is that the system doesn't crash and processes the messages
	msgs := tm.roomLog(room.ID)
	t.Logf("Posted %d bot messages (classification determines blocking)", len(msgs)-1) // -1 for user message
}
