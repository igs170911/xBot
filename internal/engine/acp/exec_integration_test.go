// go:build integration
//go:build integration

package acp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"xbot/internal/hosts"
)

// TestExecRealChild tests the real exec wrapper against a real child process
// to verify that setsid -w keeps the stream open and we receive output.
// This test requires Docker to be available.
func TestExecRealChild(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TESTS") != "" {
		t.Skip("Skipping Docker test (SKIP_DOCKER_TESTS set)")
	}

	// Use a simple docker container with setsid installed (busybox has it)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start a temporary container
	api := &hosts.RealDockerAPI{}
	containerID, err := api.ContainerCreate(ctx, "busybox:latest", []string{"sleep", "3600"})
	if err != nil {
		t.Skipf("Cannot create container (Docker not available?): %v", err)
	}
	defer func() {
		ctx := context.Background()
		_ = api.ContainerStop(ctx, containerID, 1)
		_ = api.ContainerRemove(ctx, containerID)
	}()

	if err := api.ContainerStart(ctx, containerID); err != nil {
		t.Fatalf("Cannot start container: %v", err)
	}

	// Create agent with real executor
	agent := Agent{
		Engine:  "test",
		Command: []string{"sh", "-c", "sleep 0.5; echo ok; echo done >&2"},
	}

	exec := &execProcess{
		a:   agent,
		api: api,
	}

	// Run the command
	proc, err := exec.run(ctx, containerID, "test-bot")
	if err != nil {
		t.Fatalf("exec.run failed: %v", err)
	}

	// Read output
	var stdout, stderr strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			n, err := proc.stdout.Read(buf)
			if n > 0 {
				stdout.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := proc.stderr.Read(buf)
			if n > 0 {
				stderr.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for completion
	select {
	case <-done:
		// Success: stream stayed open and we received output
	case <-ctx.Done():
		t.Fatal("Timeout waiting for output")
	}

	outStr := stdout.String()
	errStr := stderr.String()

	t.Logf("stdout: %q", outStr)
	t.Logf("stderr: %q", errStr)

	// Verify we received the expected output
	if !strings.Contains(outStr, "ok") {
		t.Errorf("Expected 'ok' in stdout, got: %q", outStr)
	}
	if !strings.Contains(errStr, "done") {
		t.Errorf("Expected 'done' in stderr, got: %q", errStr)
	}

	// Verify process exited cleanly
	proc.wait()
	if proc.exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", proc.exitCode)
	}
}
