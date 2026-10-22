package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestNilJudgeFallsBackToLenientPass tests that nil PeerJudge uses Stage A lenient pass (2026-10-22h)
func TestNilJudgeFallsBackToLenientPass(t *testing.T) {
	tm := setup(t)
	// Override with nil judge to test lenient pass
	tm.s.o.PeerJudge = nil

	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Nil Judge Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Post user message
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "討論一下", ""))

	// Chief responds with non-Stage-A-blocked text (nil judge → Stage A lenient pass fallback)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	// Use a phrase that Stage A doesn't explicitly block (not [silent], not pure acknowledgment)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "我覺得可以。")

	// With nil judge, Stage A lenient pass should allow unclear messages through
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "可以") {
			botMsgs++
		}
	}
	if botMsgs == 0 {
		t.Error("Nil judge should use lenient pass, allowing non-blocked messages through")
	}
}

// TestThreeBotFillerChainTripsAutoStop tests that 3 bots posting '好的，那我們就照這個方向走。' triggers auto-stop (2026-10-22h)
func TestThreeBotFillerChainTripsAutoStop(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{
		Name: "Filler Chain Test",
		// Don't use RuleEveryone - only wake bots when explicitly mentioned
	}))

	// User message mentioning all three bots explicitly
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief @Writer @Scout 請討論", ""))

	// All three bots get queued initially
	if len(tm.queued(chief)) == 0 || len(tm.queued(writer)) == 0 || len(tm.queued(scout)) == 0 {
		t.Fatal("All three bots should be queued initially")
	}

	fillerPhrase := "好的，那我們就照這個方向走。"

	// Chief posts filler (blocked, consec_empty = 1)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, fillerPhrase)

	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty != 1 {
		t.Errorf("After first blocked filler, consec_empty should be 1, got %d", room1.ConsecEmpty)
	}

	// Writer posts filler (blocked, consec_empty = 2, triggers auto-stop)
	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, fillerPhrase)

	room2, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room2.ConsecEmpty != 2 {
		t.Errorf("After second blocked filler, consec_empty should be 2, got %d", room2.ConsecEmpty)
	}

	// Verify exactly 1 inbox card created
	ref := "auto_stop:" + room.ID + ":" + writer
	exists, err := tm.db.InboxRefExists(tm.ctx, ref)
	if err != nil {
		t.Fatalf("Error checking inbox ref: %v", err)
	}
	if !exists {
		t.Error("Auto-stop should create exactly 1 inbox card at consec_empty=2")
	}

	// Verify 0 filler messages posted to room
	msgs := tm.roomLog(room.ID)
	fillerMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, fillerPhrase) {
			fillerMsgs++
		}
	}
	if fillerMsgs > 0 {
		t.Errorf("Filler messages should be blocked, but found %d", fillerMsgs)
	}

	// Scout was queued initially but since auto-stop occurred, it should remain queued
	// (auto-stop doesn't cancel queued tasks, just prevents new ones)
	// This is expected behavior - Scout's task exists but won't create new work
}
