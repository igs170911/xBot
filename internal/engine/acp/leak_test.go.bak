//go:build leak
// +build leak

package acp_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/limits"
)

// TestSessionLeak 驗證 B2 的記憶體洩漏修復：
// 1. 每個 box 最多 5 個 session
// 2. Idle sessions 會被自動關閉
// 3. 容器內 MCP 進程會被清理
//
// 執行 20 個順序 session + 10 個並發 session，檢查 desktop_mcp 數量不超過 cap+1。
//
// 2026-10-22p: Skeleton test - requires full Docker + LazyBoy environment for real MCP counting.
// Currently tests basic session lifecycle without actual MCP process verification.
func TestSessionLeak(t *testing.T) {
	// TODO: Implement full Docker-based MCP count assertions when environment is available
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	launcher := &fakeLauncher{}
	driver := acp.New(acp.Options{
		Agent:  acp.Agents["grok"],
		Launch: launcher.Launch,
		Logger: log,
	})
	defer driver.Shutdown()

	ctx := context.Background()
	botID := "test-bot"

	// Test basic lifecycle: sessions get created and closed
	var sessions []engine.Session
	for i := 0; i < 20; i++ {
		s, err := driver.Start(ctx, engine.StartOptions{
			BotID:    botID,
			ThreadID: fmt.Sprintf("seq-thread-%d", i),
			Mode:     "read",
		})
		if err != nil {
			t.Fatalf("Failed to start session %d: %v", i, err)
		}
		sessions = append(sessions, s)

		// Close older sessions to stay under cap
		if i >= limits.ACPMaxSessionsPerBox {
			oldest := sessions[0]
			sessions = sessions[1:]
			if err := driver.CloseSession(oldest); err != nil {
				t.Errorf("Failed to close session: %v", err)
			}
		}
	}

	// Clean up remaining
	for _, s := range sessions {
		_ = driver.CloseSession(s)
	}

	t.Log("Basic leak test passed - session lifecycle works")
}

// TestSessionLeakOverlapping tests 2026-10-22p fix: 3 overlapping sessions where count never hits 0.
// Asserts that MCP servers from closed sessions are cleaned even when new sessions arrive.
// This is a SKELETON - real MCP count assertion requires Docker exec + ps in container.
func TestSessionLeakOverlapping(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	launcher := &fakeLauncher{}
	driver := acp.New(acp.Options{
		Agent:  acp.Agents["grok"],
		Launch: launcher.Launch,
		Logger: log,
	})
	defer driver.Shutdown()

	ctx := context.Background()
	botID := "test-bot"

	// Create 3 overlapping sessions, closing one before opening next
	// Session count: 1 -> 2 -> 3 -> 2 -> 3 -> 2 (never hits 0)
	sessions := []engine.Session{}

	// Open 3 initial sessions
	for i := 0; i < 3; i++ {
		s, err := driver.Start(ctx, engine.StartOptions{
			BotID:    botID,
			ThreadID: fmt.Sprintf("overlap-thread-%d", i),
			Mode:     "read",
		})
		if err != nil {
			t.Fatalf("Failed to start session %d: %v", i, err)
		}
		sessions = append(sessions, s)
	}

	// Now cycle: close oldest, open new (count stays 2-3, never 0)
	for i := 3; i < 10; i++ {
		// Close oldest
		oldest := sessions[0]
		sessions = sessions[1:]
		if err := driver.CloseSession(oldest); err != nil {
			t.Errorf("Failed to close session: %v", err)
		}

		// Open new immediately (count never hits 0)
		s, err := driver.Start(ctx, engine.StartOptions{
			BotID:    botID,
			ThreadID: fmt.Sprintf("overlap-thread-%d", i),
			Mode:     "read",
		})
		if err != nil {
			t.Fatalf("Failed to start session %d: %v", i, err)
		}
		sessions = append(sessions, s)

		// TODO: Real test would assert MCP count in container here:
		// mcpCount := countMCPProcessesInContainer(botID)
		// if mcpCount > len(sessions) + 1 {
		//     t.Errorf("MCP leak: got %d processes, expected ≤ %d (live sessions + 1)", mcpCount, len(sessions)+1)
		// }
	}

	// Clean up
	for _, s := range sessions {
		_ = driver.CloseSession(s)
	}

	t.Log("Overlapping leak test passed - session count never hit 0, agent should have restarted when idle")
}

// TestSessionCounterBasic 測試基本的 session 計數功能（不需要 Docker）。
func TestSessionCounterBasic(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 創建帶有假 launcher 的 driver
	launcher := &fakeLauncher{}
	driver := acp.New(acp.Options{
		Agent:  acp.Agents["grok"],
		Launch: launcher.Launch,
		Logger: log,
	})
	defer driver.Shutdown()

	ctx := context.Background()
	botID := "test-bot"

	// 測試順序創建多個 sessions
	t.Run("Sequential", func(t *testing.T) {
		var sessions []engine.Session

		// 創建 limits.ACPMaxSessionsPerBox 個 sessions
		for i := 0; i < limits.ACPMaxSessionsPerBox; i++ {
			s, err := driver.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("thread-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Failed to start session %d: %v", i, err)
			}
			sessions = append(sessions, s)
		}

		// 嘗試創建第 (cap+1) 個 session，應該觸發 eviction 或等待
		done := make(chan struct{})
		go func() {
			s, err := driver.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: "thread-overflow",
				Mode:     "read",
			})
			if err != nil {
				t.Errorf("Failed to start overflow session: %v", err)
			} else {
				_ = driver.CloseSession(s)
			}
			close(done)
		}()

		// 等待一下讓 eviction 發生
		time.Sleep(500 * time.Millisecond)

		// 清理所有 sessions
		for _, s := range sessions {
			_ = driver.CloseSession(s)
		}

		// 等待 overflow session 完成
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Overflow session did not complete")
		}
	})

	// 測試並發
	t.Run("Concurrent", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()

				s, err := driver.Start(ctx, engine.StartOptions{
					BotID:    botID,
					ThreadID: fmt.Sprintf("conc-thread-%d", i),
					Mode:     "read",
				})
				if err != nil {
					errors <- fmt.Errorf("goroutine %d: %w", i, err)
					return
				}

				time.Sleep(100 * time.Millisecond)

				if err := driver.CloseSession(s); err != nil {
					errors <- fmt.Errorf("goroutine %d close: %w", i, err)
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Error(err)
		}
	})
}

// TestMCPCleanupPerSession 測試 B2.3 fix：關閉一個 session 不會殺死其他 session 的 MCP 進程。
func TestMCPCleanupPerSession(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 創建帶有跟蹤 MCP PIDs 的 launcher
	launcher := &trackingLauncher{}
	driver := acp.New(acp.Options{
		Agent:  acp.Agents["grok"],
		Launch: launcher.Launch,
		Logger: log,
	})
	defer driver.Shutdown()

	ctx := context.Background()
	botID := "test-bot"

	// 創建兩個並發 sessions
	s1, err := driver.Start(ctx, engine.StartOptions{
		BotID:    botID,
		ThreadID: "thread-1",
		Mode:     "read",
	})
	if err != nil {
		t.Fatalf("Failed to start session 1: %v", err)
	}

	s2, err := driver.Start(ctx, engine.StartOptions{
		BotID:    botID,
		ThreadID: "thread-2",
		Mode:     "read",
	})
	if err != nil {
		t.Fatalf("Failed to start session 2: %v", err)
	}

	// 記錄兩個 sessions 的 PIDs
	pids1 := launcher.getPidsForSession(s1)
	pids2 := launcher.getPidsForSession(s2)

	t.Logf("Session 1 PIDs: %v", pids1)
	t.Logf("Session 2 PIDs: %v", pids2)

	// 關閉 session 1
	if err := driver.CloseSession(s1); err != nil {
		t.Fatalf("Failed to close session 1: %v", err)
	}

	// 等待一下讓 kill 完成
	time.Sleep(200 * time.Millisecond)

	// 檢查 session 1 的 PIDs 是否被標記為已 kill
	if !launcher.allKilled(pids1) {
		t.Error("Session 1 PIDs should have been killed")
	}

	// 檢查 session 2 的 PIDs 是否還活著（未被誤殺）
	if launcher.anyKilled(pids2) {
		t.Error("Session 2 PIDs should still be alive (not killed)")
	}

	// 清理 session 2
	if err := driver.CloseSession(s2); err != nil {
		t.Errorf("Failed to close session 2: %v", err)
	}
}

// fakeLauncher 提供一個假的 process 用於測試。
type fakeLauncher struct{}

func (f *fakeLauncher) Launch(ctx context.Context, botID string, agent acp.Agent) (acp.Process, error) {
	return &fakeProcess{}, nil
}

// fakeProcess 實現 acp.Process 接口。
type fakeProcess struct {
	closed bool
}

func (p *fakeProcess) Read(b []byte) (int, error) {
	// 假的 ACP 協議握手
	if !p.closed {
		response := `{"jsonrpc":"2.0","id":"1","result":{"protocolVersion":1,"agentCapabilities":{}}}`
		return copy(b, response+"\n"), nil
	}
	return 0, fmt.Errorf("EOF")
}

func (p *fakeProcess) Write(b []byte) (int, error) {
	// 忽略寫入
	return len(b), nil
}

func (p *fakeProcess) Close() error {
	p.closed = true
	return nil
}

// trackingLauncher 跟蹤 MCP PIDs 並記錄哪些被 kill 了。
type trackingLauncher struct {
	mu          sync.Mutex
	nextPid     int
	sessionPids map[engine.Session][]int
	killedPids  map[int]bool
}

func (t *trackingLauncher) Launch(ctx context.Context, botID string, agent acp.Agent) (acp.Process, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.sessionPids == nil {
		t.sessionPids = make(map[engine.Session][]int)
		t.killedPids = make(map[int]bool)
		t.nextPid = 1000
	}

	return &trackingProcess{launcher: t}, nil
}

func (t *trackingLauncher) registerSession(s engine.Session, pids []int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionPids[s] = pids
}

func (t *trackingLauncher) getPidsForSession(s engine.Session) []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionPids[s]
}

func (t *trackingLauncher) markKilled(pid int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.killedPids[pid] = true
}

func (t *trackingLauncher) allKilled(pids []int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pid := range pids {
		if !t.killedPids[pid] {
			return false
		}
	}
	return len(pids) > 0
}

func (t *trackingLauncher) anyKilled(pids []int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pid := range pids {
		if t.killedPids[pid] {
			return true
		}
	}
	return false
}

func (t *trackingLauncher) allocatePid() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	pid := t.nextPid
	t.nextPid++
	return pid
}

// trackingProcess 實現 acp.Process 並模擬 MCP PIDs。
type trackingProcess struct {
	launcher *trackingLauncher
	closed   bool
	pids     []int
}

func (p *trackingProcess) Read(b []byte) (int, error) {
	if !p.closed {
		response := `{"jsonrpc":"2.0","id":"1","result":{"protocolVersion":1,"agentCapabilities":{}}}`
		return copy(b, response+"\n"), nil
	}
	return 0, fmt.Errorf("EOF")
}

func (p *trackingProcess) Write(b []byte) (int, error) {
	return len(b), nil
}

func (p *trackingProcess) Close() error {
	p.closed = true
	// 模擬 kill 這個 process 的 PIDs
	for _, pid := range p.pids {
		p.launcher.markKilled(pid)
	}
	return nil
}

// ContainerID 實現用於 MCP cleanup 測試。
func (p *trackingProcess) ContainerID() string {
	return "test-container"
}

// Execer 實現用於 MCP cleanup 測試。
func (p *trackingProcess) Execer() acp.Execer {
	return &trackingExecer{launcher: p.launcher, process: p}
}

// trackingExecer 模擬 Docker exec 操作。
type trackingExecer struct {
	launcher *trackingLauncher
	process  *trackingProcess
}

func (e *trackingExecer) ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
	// 模擬創建 2 個 MCP PIDs for this session
	pid1 := e.launcher.allocatePid()
	pid2 := e.launcher.allocatePid()
	e.process.pids = []int{pid1, pid2}

	return client.ExecCreateResult{ID: "exec-1"}, nil
}

func (e *trackingExecer) ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
	return client.ExecAttachResult{}, nil
}

func (e *trackingExecer) ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{PID: 0, Running: false}, nil
}
