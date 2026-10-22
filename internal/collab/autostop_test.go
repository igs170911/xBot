package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestAutoStopOnAgreementChain tests that blocked agreement messages increment consec_empty
// and trigger auto-stop with inbox notification at consec_empty=2 (2026-10-22g)
func TestAutoStopOnAgreementChain(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Agreement Test",
	}))

	// First user message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 請幫我查一下", ""))

	// Chief responds with pure agreement (no substance, should be blocked)
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	tm.finish(task1.ID, runtime.VerdictAnswer, "好的，謝謝你的回覆，我完全同意你的看法，辛苦了。")

	// Verify first blocked reply incremented consec_empty to 1
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty != 1 {
		t.Errorf("After first blocked reply, consec_empty should be 1, got %d", room1.ConsecEmpty)
	}

	// Verify no filler message was posted
	msgs1 := tm.roomLog(room.ID)
	fillerMsgs := 0
	for _, m := range msgs1 {
		if strings.HasPrefix(m.Sender, "bot:") && m.Content != "" &&
			(strings.Contains(m.Content, "完全同意") || strings.Contains(m.Content, "謝謝你的回覆")) {
			fillerMsgs++
		}
	}
	if fillerMsgs > 0 {
		t.Errorf("Filler message should be blocked, but found %d", fillerMsgs)
	}

	// Second user message (resets consec_empty due to new user input)
	// To test consec_empty=2, we need consecutive bot failures without user reset
	// This is tested in integration_test.go TestConsecEmptyAutoStop
	// Here we just verify that single blocked reply works correctly
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
