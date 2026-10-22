package collab

import (
	"strings"
	"testing"
)

func TestEnforceLength(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		class    LengthClass
		wantOut  string
		wantOver string
	}{
		{
			name:     "short text unchanged",
			text:     "Hello, world!",
			class:    ClassFact,
			wantOut:  "Hello, world!",
			wantOver: "",
		},
		{
			name:     "exactly at limit",
			text:     strings.Repeat("a", 60),
			class:    ClassFact,
			wantOut:  strings.Repeat("a", 60),
			wantOver: "",
		},
		{
			name:     "cut at period boundary",
			text:     "This is a sentence. And another one that goes beyond the limit for facts.",
			class:    ClassFact,
			wantOut:  "This is a sentence.",
			wantOver: " And another one that goes beyond the limit for facts.",
		},
		{
			name:     "cut at Chinese period",
			text:     "這是第一句。這是第二句，超過了字數限制，需要被截斷在合適的地方，所以要加長一點內容才行，真的需要很長很長的內容，繼續增加更多文字。",
			class:    ClassFact,
			wantOut:  "這是第一句。",
			wantOver: "這是第二句，超過了字數限制，需要被截斷在合適的地方，所以要加長一點內容才行，真的需要很長很長的內容，繼續增加更多文字。",
		},
		{
			name:     "cut at newline",
			text:     "First line\nSecond line that is very long and exceeds the character limit for facts",
			class:    ClassFact,
			wantOut:  "First line\n",
			wantOver: "Second line that is very long and exceeds the character limit for facts",
		},
		{
			name:     "cut at exclamation mark",
			text:     "Wow! This is amazing and goes way beyond the character limit, really beyond! And more!",
			class:    ClassFact,
			wantOut:  "Wow!",
			wantOver: " This is amazing and goes way beyond the character limit, really beyond! And more!",
		},
		{
			name:     "cut at question mark",
			text:     "Is this good? Maybe not since it exceeds the limit by quite a bit?",
			class:    ClassFact,
			wantOut:  "Is this good?",
			wantOver: " Maybe not since it exceeds the limit by quite a bit?",
		},
		{
			name:     "hard cut when no boundary",
			text:     strings.Repeat("a", 100),
			class:    ClassFact,
			wantOut:  strings.Repeat("a", 60) + "…",
			wantOver: strings.Repeat("a", 40),
		},
		{
			name:     "CJK text with no boundary",
			text:     strings.Repeat("你", 100),
			class:    ClassFact,
			wantOut:  strings.Repeat("你", 60) + "…",
			wantOver: strings.Repeat("你", 40),
		},
		{
			name:     "protect code fence",
			text:     "Here is code:\n```go\nfunc main() {\n    println(\"hello\")\n}\n```\nAnd more",
			class:    ClassFact,
			wantOut:  "Here is code:\n```go\nfunc main() {\n    println(\"hello\")\n}\n```\n",
			wantOver: "And more",
		},
		{
			name:     "protect URL",
			text:     "Text https://example.com/very/long/url/path more beyond the limit here yes",
			class:    ClassFact,
			wantOut:  "Text https://example.com/very/long/url/path more beyond the …",
			wantOver: "limit here yes",
		},
		{
			name:     "mixed CJK and English",
			text:     "這是中文。This is English text that goes way beyond the limit here. 更多中文內容。",
			class:    ClassFact,
			wantOut:  "這是中文。",
			wantOver: "This is English text that goes way beyond the limit here. 更多中文內容。",
		},
		{
			name:     "room class has more space",
			text:     strings.Repeat("This is a test sentence. ", 20),
			class:    ClassRoom,
			wantOut:  "This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence.",
			wantOver: " This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. This is a test sentence. ",
		},
		{
			name:     "peer class",
			text:     strings.Repeat("x", 500),
			class:    ClassPeer,
			wantOut:  strings.Repeat("x", 300) + "…",
			wantOver: strings.Repeat("x", 200),
		},
		{
			name:     "research class",
			text:     strings.Repeat("研究內容。", 150), // 750 chars, exceeds 600
			class:    ClassResearch,
			wantOut:  strings.Repeat("研究內容。", 120), // 600 chars
			wantOver: strings.Repeat("研究內容。", 30),  // 150 chars
		},
		{
			name:     "code fence not closed protects to end",
			text:     "Start ```go\nfunc() { very long code that exceeds limit",
			class:    ClassFact,
			wantOut:  "Start ```go\nfunc() { very long code that exceeds limit",
			wantOver: "",
		},
		{
			name:     "multiple URLs",
			text:     "See http://a.com/t and https://b.com/p for more beyond the limit",
			class:    ClassFact,
			wantOut:  "See http://a.com/t and https://b.com/p for more beyond the l…",
			wantOver: "imit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut, gotOver := EnforceLength(tt.text, tt.class)

			if gotOut != tt.wantOut {
				t.Errorf("EnforceLength() out = %q, want %q", gotOut, tt.wantOut)
			}

			if gotOver != tt.wantOver {
				t.Errorf("EnforceLength() overflow = %q, want %q", gotOver, tt.wantOver)
			}

			// Verify combined length matches original
			gotOutRunes := []rune(gotOut)
			gotOverRunes := []rune(gotOver)
			originalRunes := []rune(tt.text)

			// Remove … if present for comparison
			if len(gotOut) > 0 && gotOut[len(gotOut)-len("…"):] == "…" {
				gotOutRunes = []rune(gotOut[:len(gotOut)-len("…")])
			}

			combinedLen := len(gotOutRunes) + len(gotOverRunes)
			if combinedLen != len(originalRunes) && tt.wantOver != "" {
				t.Errorf("Combined length %d != original %d (out=%d, over=%d)",
					combinedLen, len(originalRunes), len(gotOutRunes), len(gotOverRunes))
			}
		})
	}
}

func TestLengthCap(t *testing.T) {
	tests := []struct {
		class LengthClass
		want  int
	}{
		{ClassFact, 60},
		{ClassRoom, 200},
		{ClassPeer, 300},
		{ClassResearch, 600},
		{LengthClass("unknown"), 600}, // default
	}

	for _, tt := range tests {
		t.Run(string(tt.class), func(t *testing.T) {
			if got := LengthCap(tt.class); got != tt.want {
				t.Errorf("LengthCap(%q) = %d, want %d", tt.class, got, tt.want)
			}
		})
	}
}

func TestClassifyLength(t *testing.T) {
	tests := []struct {
		name   string
		isFact bool
		want   LengthClass
	}{
		{"fact reply", true, ClassFact},
		{"research reply", false, ClassResearch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyLength(tt.isFact); got != tt.want {
				t.Errorf("ClassifyLength(%v) = %v, want %v", tt.isFact, got, tt.want)
			}
		})
	}
}

func TestFormatResearchOverflow(t *testing.T) {
	fullText := strings.Repeat("研究", 500)
	home := "from/Bot/result-20261022-120000.md"
	runeCount := len([]rune(fullText))

	got := FormatResearchOverflow(fullText, home, runeCount)
	want := "\n\n…（完整回覆共 1000 字，已存成檔案：/workspace/from/Bot/result-20261022-120000.md）"

	if got != want {
		t.Errorf("FormatResearchOverflow() = %q, want %q", got, want)
	}
}

func TestProtectedRegions(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int // number of protected regions
	}{
		{"no protection", "simple text", 0},
		{"one code fence", "text ```code``` more", 1},
		{"unclosed fence", "text ```code", 1},
		{"one URL", "see http://example.com for more", 1},
		{"multiple URLs", "http://a.com and https://b.com", 2},
		{"fence and URL", "```code``` and http://url.com", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regions := findProtectedRegions(tt.text)
			if len(regions) != tt.want {
				t.Errorf("findProtectedRegions() found %d regions, want %d", len(regions), tt.want)
			}
		})
	}
}
