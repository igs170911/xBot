package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/limits"
)

// runFakeAgent implements minimal ACP protocol for testing
func runFakeAgent(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)
	sessionCount := 0

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

			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessionId": sessID,
				},
			})

		case "session/prompt":
			// Reply with end_turn
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"stopReason": "end_turn",
				},
			})

		case "session/cancel":
			// Acknowledge silently
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

// Test C1(c): cap+1 distinct threads should not panic and all tasks complete
func TestCapPlusOneNoPanic(t *testing.T) {
	t.Run("Sequential", func(t *testing.T) {
		d, cleanup := testDriverWithFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Open cap+1 sessions sequentially
		sessions := make([]engine.Session, 0, limits.ACPMaxSessionsPerBox+1)
		for i := 0; i < limits.ACPMaxSessionsPerBox+1; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("seq-thread-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start session %d failed: %v", i, err)
			}
			sessions = append(sessions, s)
		}

		// All sessions should be valid
		if len(sessions) != limits.ACPMaxSessionsPerBox+1 {
			t.Fatalf("Expected %d sessions, got %d", limits.ACPMaxSessionsPerBox+1, len(sessions))
		}

		// Close all sessions
		for i, s := range sessions {
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Failed to close session %d: %v", i, err)
			}
		}
	})

	t.Run("Concurrent", func(t *testing.T) {
		d, cleanup := testDriverWithFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		var wg sync.WaitGroup
		errors := make(chan error, limits.ACPMaxSessionsPerBox+1)
		sessions := make(chan engine.Session, limits.ACPMaxSessionsPerBox+1)

		// Launch cap+1 concurrent Start requests
		for i := 0; i < limits.ACPMaxSessionsPerBox+1; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				s, err := d.Start(ctx, engine.StartOptions{
					BotID:    botID,
					ThreadID: fmt.Sprintf("conc-thread-%d", i),
					Mode:     "read",
				})
				if err != nil {
					errors <- fmt.Errorf("goroutine %d: %w", i, err)
					return
				}
				sessions <- s
			}(i)
		}

		wg.Wait()
		close(errors)
		close(sessions)

		// Check for errors
		for err := range errors {
			t.Error(err)
		}

		// Collect all sessions
		var sessionList []engine.Session
		for s := range sessions {
			sessionList = append(sessionList, s)
		}

		if len(sessionList) != limits.ACPMaxSessionsPerBox+1 {
			t.Fatalf("Expected %d sessions, got %d", limits.ACPMaxSessionsPerBox+1, len(sessionList))
		}

		// Close all sessions
		for i, s := range sessionList {
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Failed to close session %d: %v", i, err)
			}
		}
	})

	t.Run("Overlapping", func(t *testing.T) {
		d, cleanup := testDriverWithFakeAgent(t)
		defer cleanup()

		ctx := context.Background()
		botID := "test-bot"

		// Open 3 sessions and keep them open
		var sessions []engine.Session
		for i := 0; i < 3; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("overlap-thread-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start session %d failed: %v", i, err)
			}
			sessions = append(sessions, s)
		}

		// Open and close cap more sessions (should trigger evictions)
		for i := 0; i < limits.ACPMaxSessionsPerBox; i++ {
			s, err := d.Start(ctx, engine.StartOptions{
				BotID:    botID,
				ThreadID: fmt.Sprintf("churn-thread-%d", i),
				Mode:     "read",
			})
			if err != nil {
				t.Fatalf("Start churn session %d failed: %v", i, err)
			}
			_ = d.CloseSession(s)
		}

		// Close original 3
		for i, s := range sessions {
			if err := d.CloseSession(s); err != nil {
				t.Errorf("Failed to close overlap session %d: %v", i, err)
			}
		}
	})
}

// testDriverWithFakeAgent creates a driver with fake agent launcher
func testDriverWithFakeAgent(t *testing.T) (*acp.Driver, func()) {
	t.Helper()

	launcher := func(ctx context.Context, botID string, a acp.Agent) (acp.Process, error) {
		// Create pipe for fake agent communication
		ours, theirs := net.Pipe()

		go runFakeAgent(theirs, theirs)

		return &fakeProcess{conn: ours}, nil
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

type fakeProcess struct {
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

func (p *fakeProcess) Read(b []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, io.EOF
	}
	return p.conn.Read(b)
}

func (p *fakeProcess) Write(b []byte) (int, error) {
	return p.conn.Write(b)
}

func (p *fakeProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.conn.Close()
}

func (p *fakeProcess) StderrTail() string {
	return ""
}
