package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/store"
)

// TestH3HookPanicsOnce tests that a panic in the Finished hook doesn't block queue
func TestH3HookPanicsOnce(t *testing.T) {
	tm := setup(t)
	ctx := context.Background()
	botID := tm.bot.ID

	// Track hook calls
	var hookCalls atomic.Int32
	var hookPanicked atomic.Bool

	// Override the Finished hook to panic once
	tm.rt.SetHooks(Hooks{
		Finished: func(task store.Task, verdict, text string) {
			hookCalls.Add(1)
			if !hookPanicked.Load() {
				hookPanicked.Store(true)
				panic("simulated hook panic")
			}
		},
	})

	// Create threads
	thread1, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-1"})
	if err != nil {
		t.Fatal(err)
	}

	thread2, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-2"})
	if err != nil {
		t.Fatal(err)
	}

	// Queue two tasks
	_, err = tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread1.ID,
		Text:     "Task 1",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	task2, err := tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread2.ID,
		Text:     "Task 2",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for task2 to complete (proves queue advanced despite hook panic)
	deadline := time.Now().Add(time.Second)
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
		t.Error("H3: Task2 did not complete within 1s after task1 hook panic")
	}

	// H3: Verify hook was called exactly once for task1 (not twice due to panic)
	time.Sleep(100 * time.Millisecond) // Allow any delayed calls
	calls := hookCalls.Load()
	if calls != 2 { // Once for task1 (panicked), once for task2 (succeeded)
		t.Errorf("Expected 2 hook calls total, got %d", calls)
	}

	// H3: Verify no entry left in r.closing
	tm.rt.mu.Lock()
	closingCount := len(tm.rt.closing)
	tm.rt.mu.Unlock()
	if closingCount != 0 {
		t.Errorf("H3: Expected r.closing to be empty, got %d entries", closingCount)
	}
}

// TestH3HookPanicsTwice tests that even if hook panics in recovery, queue still advances
func TestH3HookPanicsTwice(t *testing.T) {
	tm := setup(t)
	ctx := context.Background()
	botID := tm.bot.ID

	// Track hook calls
	var hookCalls atomic.Int32

	// Override the Finished hook to always panic
	tm.rt.SetHooks(Hooks{
		Finished: func(task store.Task, verdict, text string) {
			hookCalls.Add(1)
			panic("hook always panics")
		},
	})

	// Create threads
	thread1, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-1"})
	if err != nil {
		t.Fatal(err)
	}

	thread2, err := tm.db.CreateThread(ctx, store.Thread{BotID: botID, Title: "test-thread-2"})
	if err != nil {
		t.Fatal(err)
	}

	// Queue two tasks
	_, err = tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread1.ID,
		Text:     "Task 1",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	task2, err := tm.rt.Start(ctx, ChatRequest{
		BotID:    botID,
		ThreadID: thread2.ID,
		Text:     "Task 2",
		Sender:   "owner",
		Source:   SourceOwner,
		Queue:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for task2 to complete (proves queue advanced despite repeated hook panics)
	deadline := time.Now().Add(time.Second)
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
		t.Error("H3: Task2 did not complete within 1s even with repeated hook panics")
	}

	// H3: Verify hook was attempted once per task (even if all panicked)
	time.Sleep(100 * time.Millisecond)
	calls := hookCalls.Load()
	if calls != 2 { // One attempt per task
		t.Errorf("Expected 2 hook calls (one per task), got %d", calls)
	}

	// H3: Verify no entry left in r.closing
	tm.rt.mu.Lock()
	closingCount := len(tm.rt.closing)
	tm.rt.mu.Unlock()
	if closingCount != 0 {
		t.Errorf("H3: Expected r.closing to be empty, got %d entries", closingCount)
	}
}
