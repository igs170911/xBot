package collab

import (
	"context"
	"strings"
	"sync"
	"testing"

	"xbot/internal/store"
)

// TestH2AtomicConsecEmpty tests UPDATE...RETURNING atomicity
func TestH2AtomicConsecEmpty(t *testing.T) {
	tm := setup(t)
	ctx := context.Background()
	chief := tm.bots["Chief"]
	writer := tm.bots["Writer"]

	// Create a room with threshold=2
	room := must[store.Room](t)(tm.s.CreateRoom(ctx, []string{chief, writer}, RoomOptions{
		Name:          "H2 Atomic Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Start 10 goroutines that each increment consec_empty 10 times
	// Total = 100 increments, threshold is 2, so only the 2nd increment should create a card
	var wg sync.WaitGroup
	numGoroutines := 10
	incrementsEach := 10

	var createdCounts []int
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsEach; j++ {
				val, err := tm.db.IncrementRoomConsecEmpty(ctx, room.ID)
				if err != nil {
					continue
				}
				// If this increment hit the threshold, try to create a card
				if val == 2 {
					botName := "TestBot"
					ref := "auto_stop:" + room.ID
					payload := "{}"
					_, created, err := tm.db.CreateInboxItemIgnoreDupe(ctx, store.InboxItem{
						Kind:    "mention",
						BotID:   chief,
						Ref:     ref,
						Title:   botName + " stopped",
						Body:    "Auto-stopped",
						Payload: []byte(payload),
					})
					if err == nil && created {
						mu.Lock()
						createdCounts = append(createdCounts, val)
						mu.Unlock()
					}
				}
			}
		}()
	}
	wg.Wait()

	// H2: Exactly 1 goroutine should have successfully created the card
	if len(createdCounts) != 1 {
		t.Errorf("Expected exactly 1 card creation, got %d creations at values %v", len(createdCounts), createdCounts)
	}

	// Verify the card exists
	ref := "auto_stop:" + room.ID
	allItems, err := tm.db.ListInboxFiltered(ctx, store.InboxFilter{
		Status: "open",
	})
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, item := range allItems {
		if item.Ref == ref && strings.Contains(item.Title, "stopped") {
			count++
		}
	}

	if count != 1 {
		t.Errorf("Expected exactly 1 auto-stop card in DB, got %d", count)
	}

	// Verify final consec_empty count
	r, err := tm.db.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := numGoroutines * incrementsEach
	if r.ConsecEmpty != expected {
		t.Errorf("Expected consec_empty=%d, got %d", expected, r.ConsecEmpty)
	}
}
