package engines

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCredLockSequentialWait tests that 4 concurrent lockers for the same path
// all succeed in sequence with at most 1 holder
func TestCredLockSequentialWait(t *testing.T) {
	cl := newCredLock()
	ctx := context.Background()
	credPath := filepath.Join(os.TempDir(), "test-cred-sequential.json")

	const workers = 4
	var (
		wg         sync.WaitGroup
		holdCount  atomic.Int32
		maxHold    atomic.Int32
		violations atomic.Int32
		successes  atomic.Int32
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			if err := cl.lock(ctx, credPath); err != nil {
				t.Logf("Worker %d lock failed: %v", workerID, err)
				return
			}
			defer cl.unlock(credPath)

			// Increment holder count
			current := holdCount.Add(1)

			// Update max hold count
			for {
				max := maxHold.Load()
				if current <= max || maxHold.CompareAndSwap(max, current) {
					break
				}
			}

			// Check violation: more than 1 holder
			if current > 1 {
				violations.Add(1)
			}

			// Hold the lock briefly
			time.Sleep(10 * time.Millisecond)

			// Decrement holder count
			holdCount.Add(-1)
			successes.Add(1)
		}(i)
	}

	wg.Wait()

	if v := violations.Load(); v > 0 {
		t.Errorf("Lock violation: %d times more than 1 holder simultaneously", v)
	}

	t.Logf("Max concurrent holders: %d (should be 1)", maxHold.Load())
	if maxHold.Load() > 1 {
		t.Errorf("Max holders was %d, expected at most 1", maxHold.Load())
	}

	t.Logf("Successes: %d (should be %d)", successes.Load(), workers)
	if successes.Load() != workers {
		t.Errorf("Only %d/%d workers succeeded", successes.Load(), workers)
	}
}

// TestCredLockDifferentPaths tests that concurrent locks on different paths don't contend
func TestCredLockDifferentPaths(t *testing.T) {
	cl := newCredLock()
	ctx := context.Background()
	credPath1 := filepath.Join(os.TempDir(), "test-cred-path1.json")
	credPath2 := filepath.Join(os.TempDir(), "test-cred-path2.json")

	var (
		wg         sync.WaitGroup
		lock1Start time.Time
		lock2Start time.Time
		mu         sync.Mutex
	)

	// Both locks should succeed concurrently
	wg.Add(2)

	go func() {
		defer wg.Done()
		mu.Lock()
		lock1Start = time.Now()
		mu.Unlock()

		if err := cl.lock(ctx, credPath1); err != nil {
			t.Errorf("Lock 1 failed: %v", err)
			return
		}
		defer cl.unlock(credPath1)

		// Hold for 100ms
		time.Sleep(100 * time.Millisecond)
	}()

	go func() {
		defer wg.Done()
		// Start slightly after the first
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		lock2Start = time.Now()
		mu.Unlock()

		if err := cl.lock(ctx, credPath2); err != nil {
			t.Errorf("Lock 2 failed: %v", err)
			return
		}
		defer cl.unlock(credPath2)

		// Hold for 100ms
		time.Sleep(100 * time.Millisecond)
	}()

	wg.Wait()

	// Lock 2 should not have waited for lock 1 (different paths)
	mu.Lock()
	gap := lock2Start.Sub(lock1Start)
	mu.Unlock()

	// Gap should be ~10ms (the delay), not 100ms+ (waiting for lock1)
	if gap > 50*time.Millisecond {
		t.Errorf("Lock 2 seems to have waited for lock 1: gap=%v", gap)
	}
}

// TestCredLockConcurrency tests that at most 1 holder can hold the lock
func TestCredLockConcurrency(t *testing.T) {
	cl := newCredLock()
	credPath := filepath.Join(os.TempDir(), "test-cred-concurrent.json")
	const workers = 10
	const iterations = 10

	var (
		wg         sync.WaitGroup
		holdCount  atomic.Int32
		maxHold    atomic.Int32
		violations atomic.Int32
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			ctx := context.Background()

			for j := 0; j < iterations; j++ {
				if err := cl.lock(ctx, credPath); err != nil {
					t.Logf("Worker %d iteration %d lock failed: %v", workerID, j, err)
					continue
				}

				// Increment holder count
				current := holdCount.Add(1)

				// Update max hold count
				for {
					max := maxHold.Load()
					if current <= max || maxHold.CompareAndSwap(max, current) {
						break
					}
				}

				// Check violation: more than 1 holder
				if current > 1 {
					violations.Add(1)
				}

				// Hold the lock briefly
				time.Sleep(time.Millisecond)

				// Decrement holder count
				holdCount.Add(-1)

				cl.unlock(credPath)

				// Small delay between iterations
				time.Sleep(time.Microsecond * 100)
			}
		}(i)
	}

	wg.Wait()

	if v := violations.Load(); v > 0 {
		t.Errorf("Lock violation: %d times more than 1 holder simultaneously", v)
	}

	t.Logf("Max concurrent holders: %d (should be 1)", maxHold.Load())
	if maxHold.Load() > 1 {
		t.Errorf("Max holders was %d, expected at most 1", maxHold.Load())
	}
}

// TestCredLockTimeout tests that lock acquisition respects timeout
func TestCredLockTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timeout test in short mode")
	}

	cl1 := newCredLock()
	cl2 := newCredLock()
	ctx := context.Background()
	credPath := filepath.Join(os.TempDir(), "test-cred-timeout.json")

	// First process locks
	if err := cl1.lock(ctx, credPath); err != nil {
		t.Fatalf("First lock failed: %v", err)
	}
	defer cl1.unlock(credPath)

	// Second process should timeout
	start := time.Now()
	err := cl2.lock(ctx, credPath)
	elapsed := time.Since(start)

	if err != ErrLockTimeout {
		t.Fatalf("Expected ErrLockTimeout, got: %v", err)
	}

	// Check that it waited approximately lockTimeout
	if elapsed < lockTimeout-time.Second || elapsed > lockTimeout+2*time.Second {
		t.Errorf("Timeout took %v, expected ~%v", elapsed, lockTimeout)
	}
}

// TestCredLockContextCancellation tests that lock respects context cancellation
func TestCredLockContextCancellation(t *testing.T) {
	cl1 := newCredLock()
	cl2 := newCredLock()
	credPath := filepath.Join(os.TempDir(), "test-cred-cancel.json")

	// First process locks
	ctx1 := context.Background()
	if err := cl1.lock(ctx1, credPath); err != nil {
		t.Fatalf("First lock failed: %v", err)
	}
	defer cl1.unlock(credPath)

	// Second process with cancellable context
	ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := cl2.lock(ctx2, credPath)
	elapsed := time.Since(start)

	if err != context.DeadlineExceeded {
		t.Fatalf("Expected context.DeadlineExceeded, got: %v", err)
	}

	// Should timeout in ~1 second, not the full lockTimeout (30s)
	if elapsed < 800*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("Context cancellation took %v, expected ~1s", elapsed)
	}
}

// TestCredLockWithLock tests the convenience withLock method
func TestCredLockWithLock(t *testing.T) {
	cl := newCredLock()
	ctx := context.Background()
	credPath := filepath.Join(os.TempDir(), "test-cred-withlock.json")

	var executed bool
	err := cl.withLock(ctx, credPath, func() error {
		executed = true
		// Lock should be held here
		lockKey := filepath.Clean(credPath)
		cl.mu.Lock()
		entry, ok := cl.locks[lockKey]
		cl.mu.Unlock()
		if !ok || entry == nil || entry.file == nil {
			return fmt.Errorf("lock not held in withLock callback")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("withLock failed: %v", err)
	}

	if !executed {
		t.Fatal("withLock callback not executed")
	}

	// Lock should be released after withLock
	lockKey := filepath.Clean(credPath)
	cl.mu.Lock()
	entry, ok := cl.locks[lockKey]
	cl.mu.Unlock()
	if ok && entry != nil {
		entry.cond.L.Lock()
		held := entry.held
		entry.cond.L.Unlock()
		if held {
			t.Fatal("lock still held after withLock")
		}
	}
}
