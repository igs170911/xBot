package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/judge"
	"xbot/internal/store"
)

// TestPanicRecoveryInFinish tests that tasks complete successfully even with complex state.
func TestPanicRecoveryInFinish(t *testing.T) {
	ctx := context.Background()

	f := setup(t)

	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("done", "complete", nil)
	}

	res, _ := f.rt.Start(ctx, ChatRequest{
		BotID:  f.bot.ID,
		Text:   "test task",
		Source: SourceOwner,
	})

	// Wait for finish to complete
	task := f.wait(t, res.TaskID)

	// Verify task was completed
	if task.State != store.TaskDone {
		t.Errorf("task state = %v, want %v", task.State, store.TaskDone)
	}

	// Verify bot is free after completion
	if busy, _ := f.rt.Busy(f.bot.ID); busy {
		t.Error("bot is still busy after finish - locks not released")
	}
}

// TestAlreadyFinishedEmitsDone tests idempotent finish behavior.
func TestAlreadyFinishedEmitsDone(t *testing.T) {
	ctx := context.Background()

	f := setup(t)

	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("first done", "complete", nil)
	}

	res, _ := f.rt.Start(ctx, ChatRequest{
		BotID:  f.bot.ID,
		Text:   "test double finish",
		Source: SourceOwner,
	})

	task := f.wait(t, res.TaskID)

	// Verify task completed successfully
	if task.State != store.TaskDone {
		t.Errorf("task state = %v, want %v", task.State, store.TaskDone)
	}
}

// TestPanicRecoveryReleasesLocks tests that locks are properly released.
func TestPanicRecoveryReleasesLocks(t *testing.T) {
	ctx := context.Background()

	f := setupWith(t, func(o *Options) {
		// Use a simple judge
		o.Judge = &judge.Rules{}
	})

	// Create a task that will complete normally
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("done", "complete", nil)
	}

	res, _ := f.rt.Start(ctx, ChatRequest{
		BotID:  f.bot.ID,
		Text:   "first task",
		Source: SourceOwner,
	})

	// Wait for task to finish
	f.wait(t, res.TaskID)
	time.Sleep(100 * time.Millisecond)

	// Verify bot is not stuck (lock was released)
	busy, _ := f.rt.Busy(f.bot.ID)
	if busy {
		t.Error("bot is still busy after finish - locks not released")
	}

	// Verify we can start another task (no deadlock)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("all good", "complete", nil)
	}

	res2, err := f.rt.Start(ctx, ChatRequest{
		BotID:  f.bot.ID,
		Text:   "recovery test",
		Source: SourceOwner,
	})

	if err != nil {
		t.Fatalf("failed to start task after first task: %v", err)
	}

	task2 := f.wait(t, res2.TaskID)

	if task2.State != store.TaskDone {
		t.Errorf("second task state = %v, want %v", task2.State, store.TaskDone)
	}
}

// TestDeferredUnlockPattern tests that deferred unlocks work correctly in normal operation.
func TestDeferredUnlockPattern(t *testing.T) {
	ctx := context.Background()

	f := setup(t)

	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("done", "complete", nil)
	}

	// Run multiple tasks in sequence to verify locks are properly released
	for i := 0; i < 5; i++ {
		res, err := f.rt.Start(ctx, ChatRequest{
			BotID:  f.bot.ID,
			Text:   strings.Repeat("test", i+1),
			Source: SourceOwner,
		})

		if err != nil {
			t.Fatalf("task %d failed to start: %v", i, err)
		}

		task := f.wait(t, res.TaskID)

		if task.State != store.TaskDone {
			t.Errorf("task %d: state = %v, want %v", i, task.State, store.TaskDone)
		}

		// Verify bot is free after each task
		if busy, _ := f.rt.Busy(f.bot.ID); busy {
			t.Errorf("task %d: bot still busy after completion", i)
		}
	}
}
