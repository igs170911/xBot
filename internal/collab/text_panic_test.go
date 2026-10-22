package collab

import (
	"fmt"
	"strings"
	"testing"
)

// TestHasSubstanceAfterMention_NoP panic tests HasSubstanceAfterMention with CJK, emoji, and @ at every position
func TestHasSubstanceAfterMention_NoPanic(t *testing.T) {
	// 2026-10-22f：Repro from round-5: '這是很長的中文句子說明一下結果 @小安' caused server crash
	// due to byte/rune index confusion
	tests := []struct {
		name string
		text string
	}{
		{
			name: "CJK before @",
			text: "這是很長的中文句子說明一下結果 @小安",
		},
		{
			name: "emoji before @",
			text: "🎉🎊🎈🎁 @Bob please check",
		},
		{
			name: "@ at start",
			text: "@Alice 你好",
		},
		{
			name: "@ at end",
			text: "這是一個測試 @Bob",
		},
		{
			name: "multiple @ with CJK",
			text: "你好 @Alice 和 @Bob 請查看這個問題",
		},
		{
			name: "CJK + emoji + @",
			text: "早安☀️ @小明 今天天氣很好 🌈",
		},
		{
			name: "mixed scripts",
			text: "Hello 你好 こんにちは @User مرحبا 안녕하세요",
		},
		{
			name: "@ at every position in CJK",
			text: "@測試一二三四五六七八九十",
		},
		{
			name: "proposal with @",
			text: "@Scout 我提議主題是人工智慧的未來發展",
		},
		{
			name: "request with CJK",
			text: "這個問題很複雜 @Engineer 請幫忙分析一下",
		},
		{
			name: "very long CJK before @",
			text: strings.Repeat("中", 100) + " @User 請回覆",
		},
		{
			name: "emoji in mention",
			text: "@User😊 你好",
		},
		{
			name: "zero-width characters",
			text: "\u200B@User\u200B請\u200C查看",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("HasSubstanceAfterMention panicked on %q: %v", tt.text, r)
				}
			}()

			// Call the function
			_ = HasSubstanceAfterMention(tt.text)
		})
	}
}

// TestHasSubstanceAfterMention_AtEveryPosition tests @ at every position with CJK
func TestHasSubstanceAfterMention_AtEveryPosition(t *testing.T) {
	baseText := "這是一個很長的中文測試句子用來檢查每個位置的@符號"
	runes := []rune(baseText)

	for i := 0; i < len(runes); i++ {
		text := string(runes[:i]) + "@User " + string(runes[i:])
		t.Run(fmt.Sprintf("at_pos_%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Panicked with @ at position %d: %v", i, r)
				}
			}()
			_ = HasSubstanceAfterMention(text)
		})
	}
}

// TestHasSubstanceAfterMention_Proposals tests that proposals wake the recipient
func TestHasSubstanceAfterMention_Proposals(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		wantSubstance bool
	}{
		{
			name:          "proposal wakes Scout",
			text:          "@Scout 我提議主題是人工智慧",
			wantSubstance: true,
		},
		{
			name:          "suggest wakes member",
			text:          "@Alice 我建議我們採用這個方案",
			wantSubstance: true,
		},
		{
			name:          "recommend wakes member",
			text:          "@Bob 我推薦使用新的工具",
			wantSubstance: true,
		},
		{
			name:          "pure greeting no substance",
			text:          "@Charlie 你好",
			wantSubstance: false,
		},
		{
			name:          "thanks no substance",
			text:          "@Dave 謝謝",
			wantSubstance: false,
		},
		{
			name:          "proposal without @",
			text:          "我提議主題是雲端運算",
			wantSubstance: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasSubstanceAfterMention(tt.text)
			if got != tt.wantSubstance {
				t.Errorf("HasSubstanceAfterMention(%q) = %v, want %v", tt.text, got, tt.wantSubstance)
			}
		})
	}
}

// TestClassifyMessage_Proposals tests proposal classification
func TestClassifyMessage_Proposals(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantClass MessageClass
	}{
		{
			name:      "proposal is handoff",
			text:      "我提議主題是機器學習",
			wantClass: ClassHandoff,
		},
		{
			name:      "suggestion is handoff",
			text:      "我建議採用新方案",
			wantClass: ClassHandoff,
		},
		{
			name:      "recommend is handoff",
			text:      "推薦使用這個工具",
			wantClass: ClassHandoff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyMessage(tt.text)
			if got != tt.wantClass {
				t.Errorf("ClassifyMessage(%q) = %v, want %v", tt.text, got, tt.wantClass)
			}
		})
	}
}
