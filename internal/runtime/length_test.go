package runtime

import (
	"strings"
	"testing"
)

// TestOwnerDMLength tests length enforcement for owner DM replies
func TestOwnerDMLength(t *testing.T) {
	tests := []struct {
		name           string
		ownerText      string // Owner's question
		botReply       string // Bot's full reply
		expectCut      bool   // Should be truncated
		expectMaxRunes int    // Max runes in final output (approximate)
	}{
		{
			name:      "fact question >90 runes -> cut at boundary near 60",
			ownerText: "今天天氣如何？", // Short fact question
			// Create a 100-rune reply with sentence boundaries
			botReply:       strings.Repeat("測試。", 20), // 20 * 3 = 60 chars, plus more
			expectCut:      true,
			expectMaxRunes: 65, // Will cut at sentence boundary near or after 60
		},
		{
			name:           "non-fact question 120 runes untouched",
			ownerText:      "請幫我分析一下這個問題，並給出詳細的解決方案",                                                                          // Not a short fact question (no ?)
			botReply:       "這個問題需要從多個角度來分析。首先，我們要了解問題的背景和原因。其次，需要評估現有的資源和限制。然後，我們可以提出幾個可行的方案，每個方案都有其優缺點。最後，我會建議一個最佳方案並說明原因。", // ~110 runes
			expectCut:      false,
			expectMaxRunes: 120,
		},
		{
			name:           "research answer 1125 runes -> ≤600 with file",
			ownerText:      "請寫一份詳細的報告？",                       // Question but longer expected
			botReply:       strings.Repeat("這是一個很長的報告內容。", 75), // 75 * 15 = 1125 runes
			expectCut:      true,
			expectMaxRunes: 600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test length classification
			isFact := isShortFactQuestion(tt.ownerText)
			class := classResearch
			lenient := false
			if isFact {
				class = classFact
				lenient = true
			}

			// Apply enforcement
			outText, overflow := enforceOwnerReplyLength(tt.botReply, class, lenient)

			// Check results
			outRunes := runes(outText)
			if tt.expectCut {
				if outRunes > tt.expectMaxRunes {
					t.Errorf("Expected output ≤%d runes, got %d", tt.expectMaxRunes, outRunes)
				}
				if overflow == "" {
					t.Errorf("Expected overflow, got none")
				}
			} else {
				if outText != tt.botReply {
					t.Errorf("Expected no truncation, but got truncated output")
				}
				if overflow != "" {
					t.Errorf("Expected no overflow, got: %s", overflow)
				}
			}

			// For research class with overflow, verify length with link
			if tt.expectCut && tt.expectMaxRunes == 600 && overflow != "" {
				// Add link line
				linkLine := formatResearchOverflow(tt.botReply, "test.md", runes(tt.botReply))
				finalText := outText + linkLine
				finalRunes := runes(finalText)
				if finalRunes > 600 {
					t.Errorf("Expected final text with link ≤600 runes, got %d", finalRunes)
				}
			}
		})
	}
}

// TestIsShortFactQuestion tests the heuristic for identifying fact questions
func TestIsShortFactQuestion(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"今天天氣如何？", true},
		{"What is 2+2?", true},
		{"請幫我分析一下這個問題，並給出詳細的解決方案", false},                                    // No ?
		{"這是一個很長的問題，需要超過四十五個字符才行，所以即使有問號也不應該被視為短事實問題，因為它太長了，已經超出限制？", false}, // > 45 chars
		{"", false},
		{"  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := isShortFactQuestion(tt.text)
			if got != tt.expected {
				t.Errorf("isShortFactQuestion(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

// TestEnforceLengthAtSentenceBoundary tests that cuts happen at sentence boundaries
func TestEnforceLengthAtSentenceBoundary(t *testing.T) {
	text := "第一句話。第二句話。第三句話。第四句話。第五句話。第六句話。第七句話。" // Each sentence is clear
	out, _ := enforceOwnerReplyLength(text, classFact, false)

	// Should end with a sentence terminator
	lastRune := []rune(out)[len([]rune(out))-1]
	if lastRune != '。' && lastRune != '.' && lastRune != '…' {
		t.Errorf("Expected output to end at sentence boundary, got last rune: %c", lastRune)
	}
}
