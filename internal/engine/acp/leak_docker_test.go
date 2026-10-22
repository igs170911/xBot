//go:build docker && leak

package acp_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// TestSessionLeakWithDocker 驗證 B2 的記憶體洩漏修復（真實 Docker 環境）。
// 需要：Docker、xbot-box 映像、docker CLI。
//
// 測試：
// 1. 啟動真實的 LazyBoy box 容器
// 2. 創建 ACP driver（使用 fake agent，不需要真實 CLI）
// 3. 運行 20 個順序 sessions + 10 個並發 sessions
// 4. 驗證容器內 desktop_mcp 進程數不超過 cap+1
// 5. 驗證 idle timeout 後進程返回 baseline
func TestSessionLeakWithDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// 檢查 Docker
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("沒有 docker 指令")
	}

	// 設置測試環境
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	host, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })

	bot, err := db.CreateBot(ctx, store.Bot{Name: "leak-test"})
	if err != nil {
		t.Fatal(err)
	}

	// 創建並啟動 box
	guardlock.Shared(t)
	mgr := box.New(db, hosts.NewRegistry(host), box.Options{Image: image})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := mgr.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	})

	if _, err := mgr.Create(ctx, bot.ID); err != nil {
		t.Fatalf("建立 box：%v", err)
	}
	if _, err := mgr.Start(ctx, bot.ID); err != nil {
		t.Fatalf("開機：%v", err)
	}
	boxStatus, err := mgr.WaitReady(ctx, bot.ID)
	if err != nil {
		t.Fatalf("等待 box 就緒：%v", err)
	}

	containerID := boxStatus.ContainerID
	t.Logf("Box ready: container %s", containerID)

	// 創建 ACP driver with fake process (we're testing session management, not real agent)
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	launcher := &dockerLauncher{containerID: containerID, docker: cli}
	driver := acp.New(acp.Options{
		Agent:  acp.Agents["grok"],
		Launch: launcher.Launch,
		Logger: log,
	})
	defer driver.Shutdown()

	// Helper: count MCP processes in container
	countMCP := func() int {
		cmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", "-c",
			"ps -eo cmd 2>/dev/null | grep -E 'sleep 3600|python.*sleep' | grep -v grep | wc -l")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("Warning: failed to count MCP: %v", err)
			return 0
		}
		count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		return count
	}

	// Test 1: Sequential sessions
	t.Run("Sequential", func(t *testing.T) {
		baseline := countMCP()
		t.Logf("Baseline MCP count: %d", baseline)

		for i := 0; i < 20; i++ {
			s, err := driver.Start(ctx, engine.StartOptions{
				BotID:    bot.ID,
				ThreadID: fmt.Sprintf("seq-thread-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Failed to start session %d: %v", i, err)
			}

			mcpCount := countMCP()
			if mcpCount > baseline+limits.ACPMaxSessionsPerBox+1 {
				t.Errorf("Session %d: MCP count %d exceeds baseline(%d) + cap(%d) + 1",
					i, mcpCount, baseline, limits.ACPMaxSessionsPerBox)
			}

			if err := driver.CloseSession(s); err != nil {
				t.Errorf("Failed to close session %d: %v", i, err)
			}

			// Sample check every 5 sessions
			if i%5 == 4 {
				t.Logf("After session %d: MCP count %d", i, mcpCount)
			}
		}

		// Wait for idle reaper
		t.Logf("Waiting for idle timeout cleanup...")
		time.Sleep(limits.ACPSessionIdleTimeout + 2*time.Second)

		finalCount := countMCP()
		if finalCount > baseline+1 {
			t.Errorf("After idle timeout: MCP count %d should return near baseline %d", finalCount, baseline)
		}
		t.Logf("Final MCP count after cleanup: %d (baseline: %d)", finalCount, baseline)
	})

	// Test 2: Concurrent sessions
	t.Run("Concurrent", func(t *testing.T) {
		baseline := countMCP()
		t.Logf("Baseline MCP count: %d", baseline)

		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()

				s, err := driver.Start(ctx, engine.StartOptions{
					BotID:    bot.ID,
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

		mcpCount := countMCP()
		if mcpCount > baseline+limits.ACPMaxSessionsPerBox+1 {
			t.Errorf("After concurrent: MCP count %d exceeds baseline(%d) + cap(%d) + 1",
				mcpCount, baseline, limits.ACPMaxSessionsPerBox)
		}
		t.Logf("After concurrent test: MCP count %d", mcpCount)

		// Wait for cleanup
		time.Sleep(limits.ACPSessionIdleTimeout + 2*time.Second)
		finalCount := countMCP()
		t.Logf("Final MCP count after cleanup: %d", finalCount)
	})
}

// dockerLauncher creates fake ACP processes for Docker testing.
type dockerLauncher struct {
	containerID string
	docker      *client.Client
}

func (d *dockerLauncher) Launch(ctx context.Context, botID string, agent acp.Agent) (acp.Process, error) {
	p := &dockerFakeProcess{containerID: d.containerID, docker: d.docker}
	
	// Spawn 2 long-lived child processes to simulate MCP servers (desktop_mcp, etc.)
	if err := p.spawnChild(); err != nil {
		return nil, fmt.Errorf("failed to spawn child 1: %w", err)
	}
	if err := p.spawnChild(); err != nil {
		return nil, fmt.Errorf("failed to spawn child 2: %w", err)
	}
	
	return p, nil
}

// dockerFakeProcess implements acp.Process for Docker testing.
// It spawns real long-lived child processes inside the container to simulate MCP servers.
type dockerFakeProcess struct {
	containerID string
	docker      *client.Client
	closed      bool
	childPIDs   []int // PIDs of spawned children
	mu          sync.Mutex
}

func (p *dockerFakeProcess) Read(b []byte) (int, error) {
	if !p.closed {
		response := `{"jsonrpc":"2.0","id":"1","result":{"protocolVersion":1,"agentCapabilities":{}}}`
		return copy(b, response+"\n"), nil
	}
	return 0, fmt.Errorf("EOF")
}

func (p *dockerFakeProcess) Write(b []byte) (int, error) {
	return len(b), nil
}

func (p *dockerFakeProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.closed {
		return nil
	}
	p.closed = true
	
	// Kill spawned children
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	for _, pid := range p.childPIDs {
		killCmd := []string{"sh", "-c", fmt.Sprintf("kill %d 2>/dev/null || true", pid)}
		created, err := p.docker.ExecCreate(ctx, p.containerID, client.ExecCreateOptions{Cmd: killCmd})
		if err == nil {
			att, err := p.docker.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
			if err == nil {
				att.Close()
			}
		}
	}
	
	return nil
}

func (p *dockerFakeProcess) ContainerID() string {
	return p.containerID
}

func (p *dockerFakeProcess) Execer() acp.Execer {
	return p.docker
}

// spawnChild spawns a long-lived child process inside the container to simulate desktop_mcp.
func (p *dockerFakeProcess) spawnChild() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.closed {
		return fmt.Errorf("process already closed")
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// Spawn a long-lived process that simulates desktop_mcp behavior
	// Run in background using sh -c with & to detach
	cmd := []string{"sh", "-c", "(python3 -c 'import time; time.sleep(3600)' 2>/dev/null || sleep 3600) &"}
	
	created, err := p.docker.ExecCreate(ctx, p.containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: false,
		AttachStderr: false,
	})
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}
	
	att, err := p.docker.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach exec: %w", err)
	}
	att.Close()
	
	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)
	
	return nil
}
