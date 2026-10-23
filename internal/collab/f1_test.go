package collab

import (
	"fmt"
	"sync"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestF1ConcurrentFillerAutoStop tests F1: atomic consec_empty increment prevents lost updates
// G4: Three bots post filler concurrently. Must create exactly 1 inbox card, run 30 times.
func TestF1ConcurrentFillerAutoStop(t *testing.T) {
	// G4: Run 30 times to ensure no race conditions
	for run := 0; run < 30; run++ {
		t.Run(fmt.Sprintf("run%d", run), func(t *testing.T) {
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

			// G4: Verify exactly 1 inbox card was created (unique index + atomic check)
			ref := "auto_stop:" + room.ID
			exists, err := tm.db.InboxRefExists(tm.ctx, ref)
			if err != nil {
				t.Fatalf("Error checking inbox ref: %v", err)
			}
			if !exists {
				t.Error("Expected exactly 1 auto-stop inbox card to be created")
			}

			// Verify only 1 card by querying directly
			items, err := tm.db.ListInboxByRef(tm.ctx, ref)
			if err != nil {
				t.Fatalf("Error listing inbox items: %v", err)
			}
			if len(items) != 1 {
				t.Errorf("Expected exactly 1 auto-stop card, got %d", len(items))
			}

			// Verify consec_empty reached threshold
			room2, _ := tm.db.GetRoom(tm.ctx, room.ID)
			if room2.ConsecEmpty < 2 {
				t.Errorf("Expected consec_empty >= 2 after blocked messages, got %d", room2.ConsecEmpty)
			}
		})
	}
}
