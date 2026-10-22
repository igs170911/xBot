package engines

import (
	"context"
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
// Uses flock(2) for inter-process locking.
type credLock struct {
	mu    sync.Mutex
	locks map[string]*os.File
}

func newCredLock() *credLock {
	return &credLock{locks: make(map[string]*os.File)}
}

// lockTimeout is how long to wait for a credential lock
const lockTimeout = 30 * time.Second

var (
	// ErrLockTimeout indicates the lock could not be acquired in time
	ErrLockTimeout = errors.New("credential lock timeout")
)

// lock acquires an exclusive lock for the given credential path.
// The lock is held until unlock() is called with the same path.
// Multiple goroutines within the same process are serialized by a mutex,
// and multiple processes are serialized by flock.
func (cl *credLock) lock(ctx context.Context, credPath string) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if _, ok := cl.locks[credPath]; ok {
		// Already locked in this process
		return nil
	}

	lockDir := filepath.Join(os.TempDir(), "xbot-cred-locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// Use a lock file per credential path
	lockPath := filepath.Join(lockDir, filepath.Base(credPath)+".lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire the lock with timeout
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Lock acquired
			cl.locks[credPath] = lockFile
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

		// Check context cancellation
		select {
		case <-ctx.Done():
			lockFile.Close()
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// Retry
		}
	}
}

// unlock releases the lock for the given credential path
func (cl *credLock) unlock(credPath string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	lockFile, ok := cl.locks[credPath]
	if !ok {
		return
	}

	delete(cl.locks, credPath)

	// Release the flock and close the file
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
}

// withLock executes fn while holding an exclusive lock on credPath
func (cl *credLock) withLock(ctx context.Context, credPath string, fn func() error) error {
	if err := cl.lock(ctx, credPath); err != nil {
		return err
	}
	defer cl.unlock(credPath)
	return fn()
}
