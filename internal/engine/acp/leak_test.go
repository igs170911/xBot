//go:build leak
// +build leak

package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/limits"
)

// TestMain re-exec pattern for fake ACP agent
func TestMain(m *testing.M) {
	if os.Getenv("XBOT_FAKE_ACP") == "1" {
		runFakeAgent()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeAgent implements minimal ACP protocol with sleep processes
func runFakeAgent() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	sessionCount := 0
	var processes sync.Map // sessionID -> *exec.Cmd

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}

		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"loadSession": false,
					},
				},
			})

		case "session/new":
			sessionCount++
			sessID := fmt.Sprintf("sess_%d", sessionCount)

			// E8: Spawn sleep with unique marker (not just 3600) to avoid counting host-wide processes
			cmd := exec.Command("sleep", "3600.42")
			if err := cmd.Start(); err == nil {
				processes.Store(sessID, cmd)
			}

			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessionId": sessID,
				},
			})

		case "session/prompt":
			// Reply with end_turn immediately
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"stopReason": "end_turn",
				},
			})

		case "session/cancel":
			// Silent acknowledgment
			continue

		default:
			// Unknown method
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32601,
					"message": "Method not found",
				},
			})
		}
	}
}

// Test C1(c)+C7: cap+1 threads, concurrent, overlapping
func TestSessionLeakWithAgentGenerations(t *testing.T) {
	t.Run("SequentialCapPlus1", func(t *testing.T) {
		d, cleanup := testDriverWithRealFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Open cap+1 sessions sequentially
		for i := 0; i < limits.ACPMaxSessionsPerBox+1; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("seq-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start session %d: %v", i, err)
			}
			// Close immediately to trigger drain
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Close session %d: %v", i, err)
			}
		}

		// All should complete without panic
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("Concurrent10", func(t *testing.T) {
		d, cleanup := testDriverWithRealFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				s, err := d.Start(ctx, engine.StartOptions{
					BotID:    botID,
					ThreadID: fmt.Sprintf("conc-%d", i),
					Mode:     "read",
				})
				if err != nil {
					errors <- fmt.Errorf("start %d: %w", i, err)
					return
				}
				if err := d.CloseSession(s); err != nil {
					errors <- fmt.Errorf("close %d: %w", i, err)
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Error(err)
		}
	})

	t.Run("Overlapping3", func(t *testing.T) {
		d, cleanup := testDriverWithRealFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Keep 3 sessions open
		var persistent []engine.Session
		for i := 0; i < 3; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("persistent-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start persistent %d: %v", i, err)
			}
			persistent = append(persistent, s)
		}

		// Churn through cap more sessions
		for i := 0; i < limits.ACPMaxSessionsPerBox; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("churn-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start churn %d: %v", i, err)
			}
			_ = d.CloseSession(s)
		}

		// Close persistent
		for i, s := range persistent {
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Close persistent %d: %v", i, err)
			}
		}

		// Wait for drain
		time.Sleep(200 * time.Millisecond)
	})
}

// testDriverWithRealFakeAgent creates driver that launches our re-exec fake agent
func testDriverWithRealFakeAgent(t *testing.T) (*acp.Driver, func()) {
	t.Helper()

	launcher := func(ctx context.Context, botID string, a acp.Agent) (acp.Process, error) {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "XBOT_FAKE_ACP=1")
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err := cmd.Start(); err != nil {
			return nil, err
		}

		return &fakeAgentProcess{
			cmd:    cmd,
			stdin:  stdin,
			stdout: stdout,
		}, nil
	}

	d := acp.New(acp.Options{
		Agent:  acp.Agents[acp.EngineGrok],
		Launch: launcher,
	})

	cleanup := func() {
		d.Shutdown()
	}

	return d, cleanup
}

type fakeAgentProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	closed bool
}

func (p *fakeAgentProcess) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *fakeAgentProcess) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

func (p *fakeAgentProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.stdin.Close()
	// Kill process group (agent + all sleep children)
	if p.cmd.Process != nil {
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	p.cmd.Wait()
	return nil
}

func (p *fakeAgentProcess) StderrTail() string {
	return ""
}

// E8: countSleepChildren counts only our test's sleep processes (not host-wide)
func countSleepChildren(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", "^sleep 3600\\.42$").CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0 // No matches
		}
		t.Logf("pgrep failed: %v, output: %s", err, out)
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}

// D2: Process counting tests - sleep children must equal live sessions
func TestLeakWithProcessCounting(t *testing.T) {
	t.Run("SequentialSessions", func(t *testing.T) {
		d, cleanup := testDriverWithRealFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Open sessions one at a time, verify count
		var sessions []engine.Session
		for i := 0; i < 3; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("seq-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start session %d: %v", i, err)
			}
			sessions = append(sessions, s)

			time.Sleep(50 * time.Millisecond) // Let sleep spawn
			if count := countSleepChildren(t); count != i+1 {
				t.Errorf("After opening session %d: sleep children = %d, want %d", i, count, i+1)
			}
		}

		// Close sessions one at a time, verify count
		for i, s := range sessions {
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Close session %d: %v", i, err)
			}
			time.Sleep(100 * time.Millisecond) // Let kill propagate
			remaining := len(sessions) - i - 1
			if count := countSleepChildren(t); count != remaining {
				t.Errorf("After closing session %d: sleep children = %d, want %d", i, count, remaining)
			}
		}

		// All closed, should be 0
		if count := countSleepChildren(t); count != 0 {
			t.Errorf("After closing all sessions: sleep children = %d, want 0", count)
		}
	})

	t.Run("CloseWhileInFlight", func(t *testing.T) {
		d, cleanup := testDriverWithRealFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Open two sessions
		s1, err := d.Start(ctx, engine.StartOptions{BotID: botID, ThreadID: "t1", Mode: "read"})
		if err != nil {
			t.Fatalf("Start s1: %v", err)
		}
		s2, err := d.Start(ctx, engine.StartOptions{BotID: botID, ThreadID: "t2", Mode: "read"})
		if err != nil {
			t.Fatalf("Start s2: %v", err)
		}

		time.Sleep(50 * time.Millisecond)
		if count := countSleepChildren(t); count != 2 {
			t.Errorf("After opening 2 sessions: sleep children = %d, want 2", count)
		}

		// Start in-flight prompt on s1
		doneCh := make(chan error, 1)
		go func() {
			doneCh <- d.Send(ctx, s1, engine.Message{Text: "test"})
		}()

		time.Sleep(10 * time.Millisecond) // Let prompt start

		// Close s2 while s1 has in-flight prompt
		if err := d.CloseSession(s2); err != nil {
			t.Errorf("Close s2: %v", err)
		}

		// Wait for s1 prompt to complete
		if err := <-doneCh; err != nil {
			t.Errorf("s1 prompt: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
		if count := countSleepChildren(t); count != 1 {
			t.Errorf("After closing s2 (s1 still open): sleep children = %d, want 1", count)
		}

		// Close s1
		if err := d.CloseSession(s1); err != nil {
			t.Errorf("Close s1: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
		if count := countSleepChildren(t); count != 0 {
			t.Errorf("After closing all: sleep children = %d, want 0", count)
		}
	})
}

