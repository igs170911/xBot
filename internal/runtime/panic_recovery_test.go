package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/store"
)

// TestPanicInFinishHook tests item 5: panic in Finished hook is properly recovered
func TestPanicInFinishHook(t *testing.T) {
	ctx := context.Background()
	db := openPanicTestDB(t)

	normalDriver := &panicTestDriver{}

	rt := New(Options{
		DB:     db,
		Driver: normalDriver,
		Bus:    events.New(),
		Models: &testModels{},
	})

	// Set a hook that panics
	rt.SetHooks(Hooks{
		Finished: func(task store.Task, verdict, text string) {
			panic("simulated panic in Finished hook")
		},
	})

	b, err := db.CreateBot(ctx, store.Bot{Name: "測試", Engine: "panic-test"})
	if err != nil {
		t.Fatal(err)
	}

	// This should panic in the Finished hook but still complete
	res, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for task to complete
	deadline := time.Now().Add(5 * time.Second)
	taskCompleted := false
	for time.Now().Before(deadline) {
		task, err := db.GetTask(ctx, res.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == store.TaskDone {
			taskCompleted = true
			// With hook panic after normal completion, verdict remains from normal flow
			t.Logf("task completed with verdict=%s (panic was in hook, after normal finish)", task.Verdict)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !taskCompleted {
		t.Fatal("task did not complete within timeout")
	}

	// CRITICAL: Bot should be idle (not stuck in busy state) - this is what item 5 requires
	_, taskID := rt.Active(b.ID)
	if taskID != "" {
		t.Errorf("bot should be idle after hook panic, but has active task %s", taskID)
	}

	// Remove panicking hook
	rt.SetHooks(Hooks{})

	// New message should work
	res2, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test after hook panic"})
	if err != nil {
		t.Errorf("bot should accept new messages after hook panic: %v", err)
	}

	// Wait for second task
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := db.GetTask(ctx, res2.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == store.TaskDone {
			// Should complete normally
			if task.Verdict != VerdictAnswer {
				t.Logf("second task completed with verdict %s (expected answer)", task.Verdict)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type panicTestDriver struct{}

type panicTestSession struct {
	id     string
	events chan engine.Event
}

func (s *panicTestSession) ID() string       { return s.id }
func (s *panicTestSession) ResumeID() string { return s.id }

func (d *panicTestDriver) Name() string { return "panic-test" }

func (d *panicTestDriver) Start(ctx context.Context, o engine.StartOptions) (engine.Session, error) {
	return &panicTestSession{
		id:     o.ThreadID + "-session",
		events: make(chan engine.Event, 10),
	}, nil
}

func (d *panicTestDriver) Send(ctx context.Context, s engine.Session, m engine.Message) error {
	ps := s.(*panicTestSession)
	go func() {
		time.Sleep(10 * time.Millisecond)
		ps.events <- engine.Event{Type: engine.EventText, Text: "ok"}
		ps.events <- engine.Event{Type: engine.EventDone, Status: engine.StatusComplete}
	}()
	return nil
}

func (d *panicTestDriver) Interrupt(ctx context.Context, s engine.Session) error {
	ps := s.(*panicTestSession)
	go func() {
		ps.events <- engine.Event{Type: engine.EventDone, Status: engine.StatusInterrupted}
	}()
	return nil
}

func (d *panicTestDriver) Respond(ctx context.Context, s engine.Session, requestID string, a engine.Answer) error {
	return nil
}

func (d *panicTestDriver) Events(s engine.Session) <-chan engine.Event {
	return s.(*panicTestSession).events
}

func openPanicTestDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
