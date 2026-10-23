package runtime

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// lengthClass is the message length category for owner DM replies
type lengthClass string

const (
	classFact     lengthClass = "fact"     // 60 characters (lenient: 90)
	classResearch lengthClass = "research" // 600 characters
)

// lengthCap returns the character limit for a given class
func lengthCap(class lengthClass) int {
	switch class {
	case classFact:
		return 60
	case classResearch:
		return 600
	default:
		return 600
	}
}

// enforceOwnerReplyLength truncates text to the class limit, cutting at sentence boundaries.
// Returns (output, overflow). Never cuts inside code fences or URLs.
// For fact class with lenient=true, only enforces when text exceeds 90 characters (1.5x the 60-char limit),
// and when cutting, still cuts at 60 but allows up to 90 without any truncation.
func enforceOwnerReplyLength(text string, class lengthClass, lenient bool) (out string, overflow string) {
	cap := lengthCap(class)
	runes := []rune(text)

	// Lenient fact enforcement: allow up to 90 chars without cutting
	if lenient && class == classFact {
		if len(runes) <= 90 {
			return text, ""
		}
		// Text > 90 chars, still cut at standard 60-char boundary
	}

	if len(runes) <= cap {
		return text, ""
	}

	// Find all protected regions (code fences and URLs)
	protected := findProtectedRegions(text)

	// Try to find last sentence boundary at or before cap
	for i := cap; i >= 0; i-- {
		if isSentenceBoundary(runes, i) {
			// Check if this position is inside a protected region
			if !isInProtectedRegion(i, protected) {
				out = string(runes[:i+1])
				overflow = string(runes[i+1:])
				return
			}
		}
	}

	// No sentence boundary found, hard cut and append '…'
	// But still respect protected regions
	cutPoint := cap
	for cutPoint > 0 && isInProtectedRegion(cutPoint, protected) {
		cutPoint--
	}

	if cutPoint <= 0 {
		// Can't cut without breaking protected region, cut at cap anyway
		cutPoint = cap
	}

	out = string(runes[:cutPoint]) + "…"
	overflow = string(runes[cutPoint:])
	return
}

// isSentenceBoundary checks if position i is a sentence boundary
func isSentenceBoundary(runes []rune, i int) bool {
	if i < 0 || i >= len(runes) {
		return false
	}

	r := runes[i]

	// Check for sentence terminators
	if r == '。' || r == '！' || r == '？' || r == '.' || r == '!' || r == '?' || r == '\n' {
		return true
	}

	return false
}

// protectedRegion represents a span that should not be cut
type protectedRegion struct {
	start int // rune index
	end   int // rune index (exclusive)
}

// findProtectedRegions finds all code fences and URLs in text
func findProtectedRegions(text string) []protectedRegion {
	var regions []protectedRegion
	runes := []rune(text)

	// Find code fences
	inFence := false
	fenceStart := -1
	for i := 0; i < len(runes)-2; i++ {
		if runes[i] == '`' && runes[i+1] == '`' && runes[i+2] == '`' {
			if !inFence {
				inFence = true
				fenceStart = i
			} else {
				regions = append(regions, protectedRegion{start: fenceStart, end: i + 3})
				inFence = false
			}
			i += 2
		}
	}

	// If fence not closed, protect to end
	if inFence {
		regions = append(regions, protectedRegion{start: fenceStart, end: len(runes)})
	}

	// Find URLs (simple pattern: http:// or https:// until whitespace)
	// Also protect /workspace/ paths
	for i := 0; i < len(runes)-7; i++ {
		if isURLStart(runes, i) || isWorkspacePath(runes, i) {
			urlEnd := i
			for urlEnd < len(runes) && !unicode.IsSpace(runes[urlEnd]) {
				urlEnd++
			}
			regions = append(regions, protectedRegion{start: i, end: urlEnd})
			i = urlEnd
		}
	}

	return regions
}

// isWorkspacePath checks if position i starts a /workspace/ path
func isWorkspacePath(runes []rune, i int) bool {
	if i+11 >= len(runes) {
		return false
	}
	return string(runes[i:i+11]) == "/workspace/"
}

// isURLStart checks if position i starts a URL
func isURLStart(runes []rune, i int) bool {
	if i+7 >= len(runes) {
		return false
	}

	// Check for http:// or https://
	if string(runes[i:i+7]) == "http://" {
		return true
	}
	if i+8 < len(runes) && string(runes[i:i+8]) == "https://" {
		return true
	}

	return false
}

// isInProtectedRegion checks if rune index i is inside any protected region
func isInProtectedRegion(i int, regions []protectedRegion) bool {
	for _, r := range regions {
		if i >= r.start && i < r.end {
			return true
		}
	}
	return false
}

// formatResearchOverflow writes overflow to file and returns the link line
func formatResearchOverflow(fullText, home string, runeCount int) string {
	return fmt.Sprintf("\n\n…（完整回覆共 %d 字，已存成檔案：/workspace/%s）", runeCount, home)
}

// runes counts the number of runes in a string
func runes(s string) int {
	return utf8.RuneCountInString(s)
}

// isShortFactQuestion determines if the owner's message is a short factual question
// that should get lenient enforcement (ClassFact with lenient=true).
// Heuristic: <= 45 runes (to safely stay under 50 with punctuation) and contains a question mark.
func isShortFactQuestion(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	runeCount := runes(text)
	if runeCount >= 45 { // Use 45 to have a buffer
		return false
	}
	// Check for question marks
	return strings.Contains(text, "？") || strings.Contains(text, "?")
}
