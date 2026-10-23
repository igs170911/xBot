package runtime

import (
	"context"
	"testing"
	"time"

	"xbot/internal/store"
)

// TestG2PanicAdvancesQueue tests G2: after a panic, the queue advances
// Task1 panics in consume goroutine, then Task2 must complete within 3s
func TestG2PanicAdvancesQueue(t *testing.T) {
	tm := setup(t)
	ctx := context.Background()
	botID := tm.bot.ID

	// Create threads for the bot
	thread1, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-1"})
	if err != nil {
		t.Fatalf("Failed to create thread1: %v", err)
	}

	thread2, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-2"})
	if err != nil {
		t.Fatalf("Failed to create thread2: %v", err)
	}

	// Queue two tasks
	task1, err := tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread1.ID,
		Text:     "Task that will panic",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatalf("Failed to start task1: %v", err)
	}

	task2, err := tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread2.ID,
		Text:     "Normal task",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatalf("Failed to start task2: %v", err)
	}

	// The test relies on the fake gateway causing connection issues
	// which triggers panic recovery. Wait for both tasks to complete.
	deadline := time.Now().Add(3 * time.Second)
	task2Done := false
	for time.Now().Before(deadline) {
		task2Status, err := tm.db.GetTask(ctx, task2.TaskID)
		if err == nil && task2Status.State == store.TaskDone {
			task2Done = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !task2Done {
		t.Error("G2: Task2 did not complete within 3s")
	}

	// Verify Wait returns quickly for task1
	waitCtx, waitCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer waitCancel()
	_, err = tm.rt.Wait(waitCtx, task1.TaskID)
	if err == waitCtx.Err() {
		t.Error("Wait(task1) should not block indefinitely")
	}
}
