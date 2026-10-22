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

// TestCredLockNonReentrant tests that the lock is non-reentrant
func TestCredLockNonReentrant(t *testing.T) {
	cl := newCredLock()
	ctx := context.Background()
	credPath := filepath.Join(os.TempDir(), "test-cred-nonreentrant.json")

	// First lock should succeed
	if err := cl.lock(ctx, credPath); err != nil {
		t.Fatalf("First lock failed: %v", err)
	}

	// Second lock of same path should fail (non-reentrant)
	if err := cl.lock(ctx, credPath); err == nil {
		t.Fatal("Second lock succeeded, expected failure (non-reentrant)")
	}

	// Unlock and retry should succeed
	cl.unlock(credPath)
	if err := cl.lock(ctx, credPath); err != nil {
		t.Fatalf("Lock after unlock failed: %v", err)
	}
	cl.unlock(credPath)
}

// TestCredLockConcurrency tests that at most 1 holder can hold the lock
func TestCredLockConcurrency(t *testing.T) {
	credPath := filepath.Join(os.TempDir(), "test-cred-concurrent.json")
	const workers = 10
	const iterations = 100

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

			cl := newCredLock()
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
	if elapsed < lockTimeout-time.Second || elapsed > lockTimeout+time.Second {
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
		if _, ok := cl.locks[credPath]; !ok {
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
	if _, ok := cl.locks[credPath]; ok {
		t.Fatal("lock still held after withLock")
	}
}
