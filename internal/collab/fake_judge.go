package collab

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

// fakeJudge is a deterministic judge backed by fixture file for tests (2026-10-22h)
type fakeJudge struct {
	filler map[string]bool
	real   map[string]bool
}

func newFakeJudge() (*fakeJudge, error) {
	data, err := os.ReadFile("filler_fixtures.json")
	if err != nil {
		return nil, err
	}

	var fixtures struct {
		Filler []string `json:"filler"`
		Real   []string `json:"real"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		return nil, err
	}

	fj := &fakeJudge{
		filler: make(map[string]bool),
		real:   make(map[string]bool),
	}

	for _, f := range fixtures.Filler {
		fj.filler[f] = true
	}
	for _, r := range fixtures.Real {
		fj.real[r] = true
	}

	return fj, nil
}

// Classify implements PeerJudge for deterministic testing
func (fj *fakeJudge) Classify(ctx context.Context, prev, msg, botID string) (string, error) {
	// Exact match in fixtures
	if fj.filler[msg] {
		return "skip", nil
	}
	if fj.real[msg] {
		return "handle", nil
	}

	// Fallback: only for short messages (<50 runes), check if msg is a subset of known filler phrases
	msgRunes := []rune(msg)
	if len(msgRunes) < 50 {
		msgLower := strings.ToLower(strings.TrimSpace(msg))
		for fillerPhrase := range fj.filler {
			phraseLower := strings.ToLower(fillerPhrase)
			if strings.Contains(phraseLower, msgLower) || strings.Contains(msgLower, phraseLower) {
				return "skip", nil
			}
		}
	}

	// Unknown text or long text → NEW
	return "handle", nil
}
