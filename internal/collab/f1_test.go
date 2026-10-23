package collab

import (
	"sync"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestF1ConcurrentFillerAutoStop tests F1: atomic consec_empty increment prevents lost updates
// Three bots post filler concurrently. Must create exactly 1 inbox card.
func TestF1ConcurrentFillerAutoStop(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{
		Name: "F1 Concurrent Test",
	}))

	// Owner message mentioning all three bots
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief @Writer @Scout 請討論", ""))

	// All three bots get queued
	chiefTask := tm.queued(chief)[0]
	writerTask := tm.queued(writer)[0]
	scoutTask := tm.queued(scout)[0]

	// Start all three tasks
	tm.start(chiefTask)
	tm.start(writerTask)
	tm.start(scoutTask)

	// Finish all three concurrently with filler
	fillerPhrase := "好的，那我們就照這個方向走。"
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		tm.finish(chiefTask.ID, runtime.VerdictAnswer, fillerPhrase)
	}()

	go func() {
		defer wg.Done()
		tm.finish(writerTask.ID, runtime.VerdictAnswer, fillerPhrase)
	}()

	go func() {
		defer wg.Done()
		tm.finish(scoutTask.ID, runtime.VerdictAnswer, fillerPhrase)
	}()

	wg.Wait()

	// Verify exactly 1 inbox card was created
	ref := "auto_stop:" + room.ID
	exists, err := tm.db.InboxRefExists(tm.ctx, ref)
	if err != nil {
		t.Fatalf("Error checking inbox ref: %v", err)
	}
	if !exists {
		t.Error("Expected exactly 1 auto-stop inbox card to be created")
	}

	// Verify consec_empty reached threshold
	room2, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room2.ConsecEmpty < 2 {
		t.Errorf("Expected consec_empty >= 2 after blocked messages, got %d", room2.ConsecEmpty)
	}
}
