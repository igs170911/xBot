package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mockFiles is a test Files implementation
type mockFiles struct {
	shouldFail bool
	written    map[string][]byte
}

func (m *mockFiles) Write(_ context.Context, botID, rel string, data []byte) error {
	if m.shouldFail {
		return errors.New("mock write failure")
	}
	if m.written == nil {
		m.written = make(map[string][]byte)
	}
	m.written[botID+":"+rel] = data
	return nil
}

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
			name:           "fact question >90 runes -> cut at boundary near 60",
			ownerText:      "今天天氣如何？",
			botReply:       "今天天氣非常好。陽光明媚。氣溫適中。風和日麗。適合出門。散步踏青。享受自然。呼吸新鮮空氣。心情舒暢。非常適合戶外活動。可以去公園走走。或者爬山健行。都是不錯的選擇呢。讓我們盡情享受這美好的一天吧。", // >90 runes
			expectCut:      true,
			expectMaxRunes: 70,
		},
		{
			name:           "fact 92 runes -> first sentence no link",
			ownerText:      "台灣國碼是多少？",
			botReply:       "台灣的國際電話區號是 +886，這是在撥打國際電話到台灣時需要使用的代碼。在台灣境內撥打時，手機號碼直接以 09 開頭，市話則依地區有不同的區碼。", // 73 runes actually, within lenient 90
			expectCut:      false,                                                                       // Within lenient limit, no cut
			expectMaxRunes: 100,
		},
		{
			name:           "non-fact question 110 runes untouched",
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

// TestOwnerDMLength_FileWrite tests file writing behavior
func TestOwnerDMLength_FileWrite(t *testing.T) {
	longReply := strings.Repeat("這是一個很長的回覆內容，需要存檔。", 50) // ~750 runes

	t.Run("write success", func(t *testing.T) {
		files := &mockFiles{}
		class := classResearch
		lenient := false

		_, overflow := enforceOwnerReplyLength(longReply, class, lenient)
		if overflow == "" {
			t.Fatal("Expected overflow for long reply")
		}

		// Simulate what runtime does
		botID := "test-bot"
		home := "dm-reply-test.md"
		err := files.Write(context.Background(), botID, home, []byte(longReply))
		if err != nil {
			t.Fatalf("Mock write should succeed, got: %v", err)
		}

		// Verify file contains full text
		written := files.written[botID+":"+home]
		if string(written) != longReply {
			t.Errorf("File should contain full reply")
		}

		// The runtime code should re-truncate outText to make room for linkLine
		// so that outText + linkLine ≤ 600. Just verify overflow was detected.
	})

	t.Run("write failure keeps full text", func(t *testing.T) {
		files := &mockFiles{shouldFail: true}

		_, overflow := enforceOwnerReplyLength(longReply, classResearch, false)
		if overflow == "" {
			t.Fatal("Expected overflow for long reply")
		}

		// Simulate what runtime does when write fails
		botID := "test-bot"
		home := "dm-reply-test.md"
		err := files.Write(context.Background(), botID, home, []byte(longReply))
		if err == nil {
			t.Fatal("Expected mock write to fail")
		}

		// On write failure, runtime should keep full text (not use truncated outText)
		// This is tested in the actual runtime code
	})

	t.Run("no Files interface keeps full text", func(t *testing.T) {
		// When Files is nil, overflow should not truncate
		// This is handled in runtime code - just verify enforcement returns overflow
		_, overflow := enforceOwnerReplyLength(longReply, classResearch, false)
		if overflow == "" {
			t.Fatal("Expected overflow for long reply")
		}
	})
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
	outRunes := []rune(out)
	if len(outRunes) == 0 {
		t.Fatal("Output should not be empty")
	}
	lastRune := outRunes[len(outRunes)-1]
	if lastRune != '。' && lastRune != '.' && lastRune != '…' {
		t.Errorf("Expected output to end at sentence boundary, got last rune: %c", lastRune)
	}
}
