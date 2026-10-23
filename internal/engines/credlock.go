package engines

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// credLock provides file-based locking for credential operations to prevent
// race conditions when multiple containers refresh the same credential.
// Uses flock(2) for inter-process locking and a process-level mutex.
// The lock path is per-credential directory to allow concurrent operations
// on different bots' credentials.
type credLock struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

type lockEntry struct {
	file *os.File
	cond *sync.Cond
	held bool
}

func newCredLock() *credLock {
	return &credLock{locks: make(map[string]*lockEntry)}
}

// lockTimeout is how long to wait for a credential lock
const lockTimeout = 30 * time.Second

var (
	// ErrLockTimeout indicates the lock could not be acquired in time
	ErrLockTimeout = errors.New("credential lock timeout")
)

// lock acquires an exclusive lock for the given credential path.
// The lock is held until unlock() is called with the same path.
// Contention: concurrent callers WAIT up to lockTimeout.
// The lock path is unique per credential directory.
func (cl *credLock) lock(ctx context.Context, credPath string) error {
	lockKey := filepath.Clean(credPath)

	lockDir := filepath.Join(os.TempDir(), "xbot-cred-locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// Use a hash of the full path to create a unique lock file name
	hash := sha256.Sum256([]byte(lockKey))
	lockName := fmt.Sprintf("%x.lock", hash[:8])
	lockPath := filepath.Join(lockDir, lockName)

	// Process-level coordination
	cl.mu.Lock()
	entry, exists := cl.locks[lockKey]
	if !exists {
		entry = &lockEntry{cond: sync.NewCond(&cl.mu)}
		cl.locks[lockKey] = entry
	}
	cl.mu.Unlock()

	// Wait for our turn in the process
	entry.cond.L.Lock()
	for entry.held {
		entry.cond.Wait()
		// Check if context was canceled while waiting
		select {
		case <-ctx.Done():
			entry.cond.L.Unlock()
			return ctx.Err()
		default:
		}
	}
	// We have the process-level lock, now get the file lock
	entry.cond.L.Unlock()

	// Acquire the file lock
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire the flock with timeout
	deadline := time.Now().Add(lockTimeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			lockFile.Close()
			return ctx.Err()
		case <-ticker.C:
			err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				// Lock acquired
				entry.cond.L.Lock()
				entry.file = lockFile
				entry.held = true
				entry.cond.L.Unlock()
				return nil
			}

			if !errors.Is(err, syscall.EWOULDBLOCK) {
				lockFile.Close()
				return fmt.Errorf("failed to acquire lock: %w", err)
			}

			// Check timeout
			if time.Now().After(deadline) {
				lockFile.Close()
				return ErrLockTimeout
			}
		}
	}
}

// unlock releases the lock for the given credential path
func (cl *credLock) unlock(credPath string) {
	lockKey := filepath.Clean(credPath)

	cl.mu.Lock()
	entry, ok := cl.locks[lockKey]
	cl.mu.Unlock()

	if !ok || entry == nil {
		return
	}

	entry.cond.L.Lock()
	if entry.file != nil {
		_ = syscall.Flock(int(entry.file.Fd()), syscall.LOCK_UN)
		_ = entry.file.Close()
		entry.file = nil
	}
	entry.held = false
	entry.cond.L.Unlock()

	// Wake up one waiting goroutine
	entry.cond.Signal()
}

// withLock executes fn while holding an exclusive lock on credPath
func (cl *credLock) withLock(ctx context.Context, credPath string, fn func() error) error {
	if err := cl.lock(ctx, credPath); err != nil {
		return err
	}
	defer cl.unlock(credPath)
	return fn()
}
