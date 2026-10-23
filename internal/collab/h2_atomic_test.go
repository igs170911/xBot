package collab

import (
	"context"
	"sync"
	"testing"

	"xbot/internal/store"
)

// TestH2AtomicConsecEmpty tests that 1000 concurrent increments create exactly 1 card
func TestH2AtomicConsecEmpty(t *testing.T) {
	tm := setup(t)
	ctx := context.Background()
	chief := tm.bots["Chief"]

	// Create a room with threshold=2
	room := must[store.Room](t)(tm.s.CreateRoom(ctx, []string{chief}, RoomOptions{
		Name:          "H2 Atomic Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Run 1000 concurrent increments (all will hit threshold eventually)
	var wg sync.WaitGroup
	numGoroutines := 1000
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine does its own increment
			_, _ = tm.db.IncrementRoomConsecEmpty(ctx, room.ID)
		}()
	}
	wg.Wait()

	// Count how many auto-stop cards were created for this room
	ref := "auto_stop:" + room.ID
	items, err := tm.db.ListInboxFiltered(ctx, store.InboxFilter{
		Ref:    ref,
		Status: "open",
	})
	if err != nil {
		t.Fatal(err)
	}

	// H2: Exactly 1 card should be created despite 1000 concurrent callers
	if len(items) != 1 {
		t.Errorf("Expected exactly 1 auto-stop card, got %d", len(items))
	}

	// Verify final consec_empty count
	r, err := tm.db.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ConsecEmpty != numGoroutines {
		t.Errorf("Expected consec_empty=%d, got %d", numGoroutines, r.ConsecEmpty)
	}
}
