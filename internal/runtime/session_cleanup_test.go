package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/store"
)

// Test that sessions are closed after tasks end (2026-10-22 session leak fix)

type sessionCounter struct {
	opened atomic.Int64
	closed atomic.Int64
}

type countingDriver struct {
	counter  *sessionCounter
	sessions sync.Map // sessionID -> *countingSession
	sendFunc func(ctx context.Context, s engine.Session, m engine.Message) error
}

type countingSession struct {
	id      string
	counter *sessionCounter
	events  chan engine.Event
	mu      sync.Mutex
	closed  bool
}

func (s *countingSession) ID() string       { return s.id }
func (s *countingSession) ResumeID() string { return s.id }

func (d *countingDriver) Name() string { return "counting" }

func (d *countingDriver) Start(ctx context.Context, o engine.StartOptions) (engine.Session, error) {
	id := o.ThreadID + "-session"
	s := &countingSession{
		id:      id,
		counter: d.counter,
		events:  make(chan engine.Event, 10),
	}
	d.sessions.Store(id, s)
	d.counter.opened.Add(1)
	return s, nil
}

func (d *countingDriver) Send(ctx context.Context, s engine.Session, m engine.Message) error {
	if d.sendFunc != nil {
		return d.sendFunc(ctx, s, m)
	}
	cs := s.(*countingSession)
	// Simulate immediate completion
	go func() {
		time.Sleep(10 * time.Millisecond)
		cs.mu.Lock()
		closed := cs.closed
		cs.mu.Unlock()
		if !closed {
			cs.events <- engine.Event{Type: engine.EventText, Text: "done"}
			cs.events <- engine.Event{Type: engine.EventDone, Status: engine.StatusComplete}
		}
	}()
	return nil
}

func (d *countingDriver) Interrupt(ctx context.Context, s engine.Session) error {
	cs := s.(*countingSession)
	go func() {
		cs.events <- engine.Event{Type: engine.EventDone, Status: engine.StatusInterrupted}
	}()
	return nil
}

func (d *countingDriver) Respond(ctx context.Context, s engine.Session, requestID string, a engine.Answer) error {
	return nil
}

func (d *countingDriver) Events(s engine.Session) <-chan engine.Event {
	return s.(*countingSession).events
}

// CloseSession implements the optional session cleanup interface
func (d *countingDriver) CloseSession(s engine.Session) error {
	cs, ok := s.(*countingSession)
	if !ok {
		return errors.New("not a counting session")
	}
	cs.mu.Lock()
	if cs.closed {
		cs.mu.Unlock()
		return nil
	}
	cs.closed = true
	cs.mu.Unlock()

	d.counter.closed.Add(1)
	d.sessions.Delete(cs.id)
	close(cs.events)
	return nil
}

func TestSessionCleanupAfterTask(t *testing.T) {
	ctx := context.Background()
	db := openCleanupTestDB(t)

	counter := &sessionCounter{}
	driver := &countingDriver{counter: counter}

	rt := New(Options{
		DB:     db,
		Driver: driver,
		Bus:    events.New(),
		Models: &testModels{},
	})

	b, err := db.CreateBot(ctx, store.Bot{Name: "測試", Engine: "counting"})
	if err != nil {
		t.Fatal(err)
	}

	// Start and finish a task
	res, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for task to complete
	waitCleanupTask(t, db, res.TaskID, 5*time.Second)

	// Check that session was opened and closed
	if opened := counter.opened.Load(); opened != 1 {
		t.Errorf("expected 1 session opened, got %d", opened)
	}
	if closed := counter.closed.Load(); closed != 1 {
		t.Errorf("expected 1 session closed, got %d", closed)
	}

	// Verify no sessions remain
	count := 0
	driver.sessions.Range(func(key, value any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected 0 remaining sessions, got %d", count)
	}
}

func TestSessionCleanupAfterMultipleTasks(t *testing.T) {
	ctx := context.Background()
	db := openCleanupTestDB(t)

	counter := &sessionCounter{}
	driver := &countingDriver{counter: counter}

	rt := New(Options{
		DB:     db,
		Driver: driver,
		Bus:    events.New(),
		Models: &testModels{},
	})

	b, err := db.CreateBot(ctx, store.Bot{Name: "測試", Engine: "counting"})
	if err != nil {
		t.Fatal(err)
	}

	// Run 20 sequential tasks
	const numTasks = 20
	for i := 0; i < numTasks; i++ {
		res, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test"})
		if err != nil {
			t.Fatal(err)
		}
		waitCleanupTask(t, db, res.TaskID, 5*time.Second)
	}

	// All sessions should be closed
	if opened := counter.opened.Load(); opened != numTasks {
		t.Errorf("expected %d sessions opened, got %d", numTasks, opened)
	}
	if closed := counter.closed.Load(); closed != numTasks {
		t.Errorf("expected %d sessions closed, got %d", numTasks, closed)
	}

	// Verify no sessions remain
	count := 0
	driver.sessions.Range(func(key, value any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected 0 remaining sessions after %d tasks, got %d", numTasks, count)
	}
}

func TestSessionCleanupOnError(t *testing.T) {
	ctx := context.Background()
	db := openCleanupTestDB(t)

	counter := &sessionCounter{}
	driver := &countingDriver{counter: counter}

	// Override sendFunc to simulate error
	driver.sendFunc = func(ctx context.Context, s engine.Session, m engine.Message) error {
		cs := s.(*countingSession)
		go func() {
			cs.events <- engine.Event{Type: engine.EventError, Text: "simulated error"}
			cs.events <- engine.Event{Type: engine.EventDone, Status: engine.StatusError}
		}()
		return nil
	}

	rt := New(Options{
		DB:     db,
		Driver: driver,
		Bus:    events.New(),
		Models: &testModels{},
	})

	b, err := db.CreateBot(ctx, store.Bot{Name: "測試", Engine: "counting"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test"})
	if err != nil {
		t.Fatal(err)
	}
	waitCleanupTask(t, db, res.TaskID, 5*time.Second)

	// Session should still be closed even on error
	if closed := counter.closed.Load(); closed != 1 {
		t.Errorf("expected session to be closed on error, got %d closed", closed)
	}
}

func TestSessionCleanupOnInterrupt(t *testing.T) {
	ctx := context.Background()
	db := openCleanupTestDB(t)

	counter := &sessionCounter{}
	driver := &countingDriver{counter: counter}

	rt := New(Options{
		DB:     db,
		Driver: driver,
		Bus:    events.New(),
		Models: &testModels{},
	})

	b, err := db.CreateBot(ctx, store.Bot{Name: "測試", Engine: "counting"})
	if err != nil {
		t.Fatal(err)
	}

	// Start a task
	res, err := rt.Start(ctx, ChatRequest{BotID: b.ID, Text: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Immediately stop it
	time.Sleep(5 * time.Millisecond)
	stopped, err := rt.StopTask(ctx, res.TaskID)
	if err != nil || !stopped {
		t.Fatalf("StopTask failed: stopped=%v err=%v", stopped, err)
	}

	waitCleanupTask(t, db, res.TaskID, 5*time.Second)

	// Session should be closed on interrupt
	if closed := counter.closed.Load(); closed != 1 {
		t.Errorf("expected session to be closed on interrupt, got %d closed", closed)
	}
}

func openCleanupTestDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func waitCleanupTask(t *testing.T, db *store.DB, taskID string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := db.GetTask(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == store.TaskDone {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s did not complete within %v", taskID, timeout)
}

type testModels struct{}

func (m *testModels) ResolveModel(ctx context.Context, b store.Bot) (ModelRef, error) {
	return ModelRef{}, nil
}
