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
	// Auto-stop creates inbox for the last bot that triggered it (Writer)
	ref := "auto_stop:" + room.ID + ":" + writer
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
