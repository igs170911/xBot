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
// 註：這是一個 placeholder 測試骨架。完整實現需要：
// - Docker 環境設置
// - LazyBoy box 映像
// - 真實的 agent CLI（grok, claude-code 等）
// - Box manager 集成
//
// 目前編譯通過，但需要真實環境才能執行。
func TestSessionLeak(t *testing.T) {
	t.Skip("B2.5: Leak test placeholder - requires full Docker + LazyBoy environment")

	// TODO: Implement full leak test when Docker environment is available
	// Steps:
	// 1. Start LazyBoy box container
	// 2. Create ACP driver with real agent
	// 3. Run 20 sequential sessions
	// 4. Run 10 concurrent sessions
	// 5. Assert MCP process count ≤ cap+1 throughout
	// 6. Assert processes return to baseline after idle timeout
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

