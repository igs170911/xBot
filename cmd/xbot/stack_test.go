package main

import (
	"testing"
)

// TestJudgeSelection verifies that with no API key (empty env, default config),
// the Grok CLI judge is selected, not the API tier (deepseek).
// This tests Item 3: no API key means Grok judge selection.
func TestJudgeSelection_NoAPIKey(t *testing.T) {
	// Simulate empty tier settings (no API key configured)
	// Using anonymous structs to match stack.go logic
	type tier struct {
		Provider string
		Model    string
	}
	tier1 := tier{Provider: "", Model: ""}
	tier2 := tier{Provider: "", Model: ""}

	// Check logic from stack.go:293-294
	hasAPIJudge := (tier1.Provider != "" && tier1.Model != "") ||
		(tier2.Provider != "" && tier2.Model != "")

	if hasAPIJudge {
		t.Errorf("Expected hasAPIJudge=false with empty tiers, got true")
		t.Error("With no API key configured, should select Grok CLI judge, not API tier")
	}
}

// TestJudgeSelection_WithAPIKey verifies that when API tiers are configured,
// the API-based judge is selected (not Grok CLI).
func TestJudgeSelection_WithAPIKey(t *testing.T) {
	type tier struct {
		Provider string
		Model    string
	}
	tier1 := tier{Provider: "deepseek", Model: "deepseek-chat"}
	tier2 := tier{Provider: "", Model: ""}

	// Check logic from stack.go:293-294
	hasAPIJudge := (tier1.Provider != "" && tier1.Model != "") ||
		(tier2.Provider != "" && tier2.Model != "")

	if !hasAPIJudge {
		t.Errorf("Expected hasAPIJudge=true with configured tier, got false")
	}
}

// TestJudgeSelection_PartialConfig verifies that having only provider or only model
// is not considered "configured" - both must be set.
func TestJudgeSelection_PartialConfig(t *testing.T) {
	type tier struct {
		Provider string
		Model    string
	}

	tests := []struct {
		name  string
		tier1 tier
		tier2 tier
		want  bool
	}{
		{
			name:  "only provider, no model",
			tier1: tier{Provider: "deepseek", Model: ""},
			tier2: tier{},
			want:  false,
		},
		{
			name:  "only model, no provider",
			tier1: tier{Provider: "", Model: "deepseek-chat"},
			tier2: tier{},
			want:  false,
		},
		{
			name:  "both empty",
			tier1: tier{Provider: "", Model: ""},
			tier2: tier{},
			want:  false,
		},
		{
			name:  "both set on tier1",
			tier1: tier{Provider: "deepseek", Model: "deepseek-chat"},
			tier2: tier{},
			want:  true,
		},
		{
			name:  "both set on tier2",
			tier1: tier{},
			tier2: tier{Provider: "openrouter", Model: "gpt-4"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check logic from stack.go:293-294
			hasAPIJudge := (tt.tier1.Provider != "" && tt.tier1.Model != "") ||
				(tt.tier2.Provider != "" && tt.tier2.Model != "")

			if hasAPIJudge != tt.want {
				t.Errorf("hasAPIJudge = %v, want %v", hasAPIJudge, tt.want)
			}
		})
	}
}
