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

			// Spawn sleep 3600 in this process group
			cmd := exec.Command("sleep", "3600")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
