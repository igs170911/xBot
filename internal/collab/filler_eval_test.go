package collab

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// FillerFixtures holds test cases for filler detection
type FillerFixtures struct {
	Filler []string `json:"filler"`
	Real   []string `json:"real"`
}

// TestFillerEvalRealJudge runs fixtures against the real judge (skips if no credentials)
func TestFillerEvalRealJudge(t *testing.T) {
	if testing.Short() {
		t.Skip("跳過：需要真實快判服務")
	}

	// Load fixtures
	data, err := os.ReadFile("filler_fixtures.json")
	if err != nil {
		t.Fatalf("Failed to load fixtures: %v", err)
	}

	var fixtures FillerFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("Failed to parse fixtures: %v", err)
	}

	if len(fixtures.Filler) < 20 || len(fixtures.Real) < 20 {
		t.Fatalf("Fixtures must have ≥20 filler and ≥20 real phrasings, got %d filler and %d real",
			len(fixtures.Filler), len(fixtures.Real))
	}

	// Create service with real judge
	tm := setup(t)
	if tm.s.o.JudgeService == nil {
		t.Skip("跳過：沒有快判服務")
	}

	ctx := context.Background()
	var fillerErrors, realErrors []string

	// Test filler phrasings (should be classified as skip/fyi)
	for _, text := range fixtures.Filler {
		verdict := tm.s.peerReplyJudge(ctx, text, "test-bot")
		if verdict == "handle" {
			fillerErrors = append(fillerErrors, text)
		}
	}

	// Test real phrasings (should be classified as handle)
	for _, text := range fixtures.Real {
		verdict := tm.s.peerReplyJudge(ctx, text, "test-bot")
		if verdict != "handle" && verdict != "" {
			realErrors = append(realErrors, text)
		}
	}

	// Report results
	t.Logf("Filler fixtures: %d total, %d misclassified as handle", len(fixtures.Filler), len(fillerErrors))
	t.Logf("Real fixtures: %d total, %d misclassified as non-handle", len(fixtures.Real), len(realErrors))

	if len(fillerErrors) > 0 {
		t.Errorf("Filler phrasings misclassified as handle:\n  - %s", strings.Join(fillerErrors, "\n  - "))
	}

	if len(realErrors) > 0 {
		t.Errorf("Real phrasings not classified as handle:\n  - %s", strings.Join(realErrors, "\n  - "))
	}
}
