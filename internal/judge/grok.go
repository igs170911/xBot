package judge

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// GrokJudge is a peer-reply judge backed by direct `grok` CLI exec.
// It bypasses the ACP Driver and MCP servers entirely: each classification
// runs `grok ask <prompt>` directly in the bot's container via Docker exec.
//
// Session isolation: Does NOT count toward the bot's session cap because
// it never creates an ACP session. Each judgment is a one-shot CLI invocation
// that terminates immediately after producing output.
//
// On timeout or error, falls back to strict (Rules).
type GrokJudge struct {
	// ContainerFinder returns the Docker container ID for a given botID.
	// If nil or returns empty string, judge will fail and fall back to Rules.
	ContainerFinder func(botID string) string
	Log             *slog.Logger
	// Timeout for each CLI invocation (default 5 seconds).
	Timeout time.Duration
	// ModelName is the fast model to use (e.g. "grok-4.7-build-fast", "grok-beta").
	ModelName string
}

// Classify implements collab.PeerJudge by executing `grok ask <prompt>` directly
// in the bot's container, bypassing the ACP Driver and MCP servers entirely.
// Timeout or error -> returns ("", err), signaling fallback to Rules (B3: strict fallback).
func (g *GrokJudge) Classify(ctx context.Context, prev, msg, botID string) (string, error) {
	if g.ContainerFinder == nil {
		return "", fmt.Errorf("grok judge: no container finder")
	}

	log := g.Log
	if log == nil {
		log = slog.Default()
	}

	timeout := g.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second // D3: Raised to 10s
	}

	model := g.ModelName
	if model == "" {
		model = "grok-4.7-build-fast"
	}

	containerID := g.ContainerFinder(botID)
	if containerID == "" {
		log.Warn("grok judge: no container found", "bot", botID)
		return "", fmt.Errorf("grok judge: no container for bot %s", botID)
	}

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build prompt
	prompt := buildGrokPrompt(msg)

	// D3 (Round 11): Execute `grok -p "<prompt>" --model <model>` in the container
	cmd := exec.CommandContext(ctx, "docker", "exec", containerID,
		"grok", "-p", prompt, "--model", model)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			log.Debug("grok judge: timeout", "bot", botID)
			return "", fmt.Errorf("grok judge timeout: %w", ctx.Err())
		}
		log.Warn("grok judge: exec failed", "bot", botID, "err", err, "output", string(output))
		return "", fmt.Errorf("grok judge exec: %w", err)
	}

	// Parse output
	verdict, err := parseGrokContent(string(output))
	if err != nil {
		log.Warn("grok judge: parse failed", "bot", botID, "err", err, "output", string(output))
		return "", fmt.Errorf("grok judge parse: %w", err)
	}

	log.Debug("grok judge: classified", "verdict", verdict, "bot", botID)
	return verdict, nil
}

// buildGrokPrompt constructs the peer-reply classification prompt for the fast model.
// Follows the same classification task as judge.Service "peer_reply" use.
func buildGrokPrompt(msg string) string {
	return fmt.Sprintf(`You are classifying a message from one bot to its teammate in a multi-bot collaboration system.
Output ONLY one word: "handle", "skip", or "fyi".

- "handle": The message contains new information, a request, or substantive content that requires the recipient's attention.
- "skip": The message is pure filler (pleasantries, acknowledgments like "got it", "thanks", "understood" without substance).
- "fyi": The message is informational but does not require immediate action or response.

Message to classify:
%s

Classification:`, msg)
}

// parseGrokContent extracts the verdict ("handle", "skip", "fyi") from the accumulated content.
// If the response is malformed or does not contain a recognized verdict, returns error.
func parseGrokContent(content string) (string, error) {
	content = strings.ToLower(strings.TrimSpace(content))

	// Extract first word
	words := strings.Fields(content)
	if len(words) == 0 {
		return "", fmt.Errorf("empty response")
	}
	verdict := words[0]

	// Validate verdict
	switch verdict {
	case "handle", "skip", "fyi":
		return verdict, nil
	default:
		return "", fmt.Errorf("unknown verdict: %s", verdict)
	}
}
