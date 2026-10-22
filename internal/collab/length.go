package collab

import (
	"fmt"
	"unicode"
)

// LengthClass is the message length category
type LengthClass string

const (
	ClassFact     LengthClass = "fact"     // 60 characters
	ClassRoom     LengthClass = "room"     // 200 characters
	ClassPeer     LengthClass = "peer"     // 300 characters
	ClassResearch LengthClass = "research" // 600 characters
)

// LengthCap returns the character limit for a given class
func LengthCap(class LengthClass) int {
	switch class {
	case ClassFact:
		return 60
	case ClassRoom:
		return 200
	case ClassPeer:
		return 300
	case ClassResearch:
		return 600
	default:
		return 600
	}
}

// EnforceLength truncates text to the class limit, cutting at sentence boundaries.
// Returns (output, overflow). Never cuts inside code fences or URLs.
func EnforceLength(text string, class LengthClass) (out string, overflow string) {
	cap := LengthCap(class)
	runes := []rune(text)

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

// FormatResearchOverflow writes overflow to file and returns the link line
func FormatResearchOverflow(fullText, home string, runeCount int) string {
	return fmt.Sprintf("\n\n…（完整回覆共 %d 字，已存成檔案：/workspace/%s）", runeCount, home)
}

// ClassifyLength determines the length class based on whether it's a fact or research reply
func ClassifyLength(isFact bool) LengthClass {
	if isFact {
		return ClassFact
	}
	return ClassResearch
}
