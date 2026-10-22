package judge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
)

// GrokJudge is a peer-reply judge backed by a one-shot ACP session on the grok engine.
// It uses a fast model for quick classification and does not count toward the per-box
// LRU session cap since it never shares sessions with user conversations.
// Each judgment spawns, uses, and closes a fresh ACP session immediately.
//
// On timeout or error, falls back to strict (Rules).
//
// Session lifecycle and LRU cap: GrokJudge does NOT count toward the per-box LRU cap
// because it uses synthetic botIDs (judge-<timestamp>) that are never reused and are
// immediately closed after each classification. The ACP driver's session LRU only
// evicts when the cap is reached, so these short-lived judge sessions are closed
// before they can displace user conversation sessions.
type GrokJudge struct {
	Driver *acp.Driver
	Log    *slog.Logger
	// Timeout for each one-shot judgment (2026-10-22: 5 seconds as per judge use definitions).
	Timeout time.Duration
	// ModelName is the fast model to use for classification (e.g. "grok-2-mini-public", "grok-beta").
	ModelName string
}

// Classify implements collab.PeerJudge by spawning a one-shot ACP session, prompting it with
// the peer-reply classification task, parsing the output, and closing the session immediately.
// Timeout or error -> returns ("", err), signaling fallback to Rules (B3: strict fallback).
func (g *GrokJudge) Classify(ctx context.Context, prev, msg, botID string) (string, error) {
	if g.Driver == nil {
		return "", fmt.Errorf("grok judge: no driver")
	}

	log := g.Log
	if log == nil {
		log = slog.Default()
	}

	timeout := g.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	model := g.ModelName
	if model == "" {
		// B3: Default to a real model name (grok-beta is the fast free-tier model)
		model = "grok-beta"
	}

	// Create a one-shot session context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// B3: Use REAL botID (not synthetic) to get per-bot credential path.
	// The session is still one-shot and immediately closed, but uses the actual bot's auth.
	// ThreadID is synthetic to avoid interfering with real sessions.
	threadID := fmt.Sprintf("judge-%d", time.Now().UnixNano())
	sess, err := g.Driver.Start(ctx, engine.StartOptions{BotID: botID, ThreadID: threadID, Model: model})
	if err != nil {
		log.Warn("grok judge: failed to start session", "err", err)
		// B3: Return error to trigger strict fallback
		return "", fmt.Errorf("grok judge start: %w", err)
	}
	// CRITICAL: close session on every exit path via defer
	defer func() {
		_ = g.Driver.CloseSession(sess)
	}()

	// Build peer-reply judgment prompt (same logic as judge.Service uses for "peer_reply")
	prompt := buildGrokPrompt(msg)

	// Send judgment request to the ACP session and collect events
	events := g.Driver.Events(sess)
	if err := g.Driver.Send(ctx, sess, engine.Message{Text: prompt}); err != nil {
		if ctx.Err() != nil {
			log.Debug("grok judge: send timeout", "thread", threadID)
			return "", fmt.Errorf("grok judge timeout: %w", ctx.Err())
		}
		log.Warn("grok judge: send failed", "thread", threadID, "err", err)
		return "", fmt.Errorf("grok judge send: %w", err)
	}

	// Collect response from events stream until done or error
	var content strings.Builder
	for evt := range events {
		switch evt.Type {
		case "content":
			content.WriteString(evt.Text)
		case "done":
			if evt.Status == "error" {
				log.Warn("grok judge: done with error", "thread", threadID, "status", evt.Status, "text", evt.Text)
				return "", fmt.Errorf("grok judge error: %s", evt.Text)
			}
			// Success: parse the accumulated content
			verdict, err := parseGrokContent(content.String())
			if err != nil {
				log.Warn("grok judge: parse failed", "thread", threadID, "err", err)
				return "", fmt.Errorf("grok judge parse: %w", err)
			}
			log.Debug("grok judge: classified", "verdict", verdict, "thread", threadID)
			return verdict, nil
		case "error":
			if evt.Fatal {
				log.Warn("grok judge: fatal error", "thread", threadID, "text", evt.Text)
				return "", fmt.Errorf("grok judge fatal: %s", evt.Text)
			}
		}
		// Check context timeout on each event
		if ctx.Err() != nil {
			log.Debug("grok judge: timeout while reading events", "thread", threadID)
			return "", fmt.Errorf("grok judge timeout: %w", ctx.Err())
		}
	}

	// Events channel closed without done event
	log.Warn("grok judge: events closed without done", "thread", threadID)
	return "", fmt.Errorf("grok judge: incomplete response")
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
