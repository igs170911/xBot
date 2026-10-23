package collab

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// ProductionJudge implements PeerJudge using the judge service's Decide method.
// It falls back to STRICT Stage A if no judge backend is available.
type ProductionJudge struct {
	log     *slog.Logger
	decide  func(ctx context.Context, kind, content, botID string) (string, error)
	strict  bool // true = fall back to strict Stage A (no judge backend)
	logOnce sync.Once
}

// NewProductionJudge creates a production judge using the given decide function.
// If decide is nil, it falls back to strict Stage A.
func NewProductionJudge(decide func(ctx context.Context, kind, content, botID string) (string, error), log *slog.Logger) *ProductionJudge {
	if log == nil {
		log = slog.Default()
	}
	strict := decide == nil
	p := &ProductionJudge{
		log:    log,
		decide: decide,
		strict: strict,
	}
	if strict {
		p.logOnce.Do(func() {
			p.log.Warn("No judge backend available, falling back to STRICT Stage A")
		})
	}
	return p
}

// Classify implements PeerJudge.
func (p *ProductionJudge) Classify(ctx context.Context, prev, msg, botID string) (string, error) {
	if p.strict {
		// Strict Stage A: no judge backend, must have concrete residue
		if !hasConcreteResidue(msg) {
			return "skip", nil
		}
		return "handle", nil
	}

	// Call judge service
	verdict, err := p.decide(ctx, "peer_reply", msg, botID)
	if errors.Is(err, ErrJudgeNotConfigured) || err != nil {
		// Judge not configured or failed -> fall back to STRICT (2026-10-22i)
		p.log.Debug("judge failed, falling back to STRICT", "err", err)
		if !hasConcreteResidue(msg) {
			return "skip", nil
		}
		return "handle", nil
	}
	return verdict, nil
}

// hasConcreteResidue checks if message has concrete evidence beyond deference phrases.
// Strict mode requires: question OR number/date/URL/code/quote OR ≥2 new content tokens (2026-10-22i).
func hasConcreteResidue(text string) bool {
	stripped := strings.TrimSpace(text)
	if len([]rune(stripped)) < 2 {
		return false
	}

	// Check for concrete markers FIRST (before stripping)
	// Question?
	if strings.ContainsAny(stripped, "?？") {
		return true
	}
	// URL or path
	if strings.Contains(stripped, "http") || strings.Contains(stripped, "://") ||
		(strings.Contains(stripped, "/") && (strings.Contains(stripped, "workspace") || strings.Contains(stripped, ".pdf") || strings.Contains(stripped, ".md"))) {
		return true
	}
	// Code block
	if strings.Contains(stripped, "```") || (strings.Count(stripped, "`") >= 2) {
		return true
	}
	// Quoted string (substantial quote, not just punctuation)
	for _, q := range []string{"「", "」", "『", "』", `"`, `"`} {
		if strings.Contains(stripped, q) {
			// Check if there's content inside quotes
			parts := strings.Split(stripped, q)
			for _, p := range parts {
				if len([]rune(strings.TrimSpace(p))) >= 2 {
					return true
				}
			}
		}
	}

	// Step 1: Strip @mentions
	atMentionRe := regexp.MustCompile(`@\S+`)
	remaining := atMentionRe.ReplaceAllString(stripped, "")
	remaining = strings.TrimSpace(remaining)

	// Step 2: Strip pleasantries and deference/agreement phrases
	deferencePatterns := []string{
		"好的", "了解", "收到", "明白", "知道了", "知道", "同意", "沒問題", "沒意見", "沒有問題", "沒有意見",
		"謝謝", "辛苦", "感謝", "不客氣", "感恩", "非常感謝",
		"照你說的", "交給你", "你決定", "我都可以", "就這樣", "照這個方向", "沒有要補充", "沒有補充",
		"那我們就", "我們就", "就照", "照著",
		"+1", "同上", "贊成", "支持", "👍",
		"ok", "okay", "okok", "可以",
		"looks", "good", "lgtm", "agree", "agreed", "sounds", "got", "it",
	}
	for _, pat := range deferencePatterns {
		// Case-insensitive replacement
		lowerPat := strings.ToLower(pat)
		remaining = strings.ReplaceAll(remaining, pat, " ")
		remaining = strings.ReplaceAll(remaining, lowerPat, " ")
		remaining = strings.ReplaceAll(remaining, strings.ToUpper(pat), " ")
	}
	// Remove punctuation
	for _, p := range []string{"，", "。", "、", "！", ".", ",", "!", "；", "：", ":", "（", "）", "(", ")"} {
		remaining = strings.ReplaceAll(remaining, p, " ")
	}
	remaining = strings.TrimSpace(remaining)

	// Check for numbers AFTER stripping deference (so "+1" alone won't pass)
	// But multi-digit numbers or numbers with context count
	if hasSignificantNumber(remaining) {
		return true
	}

	// Check if ≥2 content tokens remain after stripping deference
	tokens := extractContentTokens(remaining)
	return len(tokens) >= 2
}

// hasSignificantNumber checks if text contains meaningful numbers (not just single digit fillers).
func hasSignificantNumber(text string) bool {
	// Multi-digit numbers (10, 25, 123, etc.)
	multiDigitRe := regexp.MustCompile(`\d{2,}`)
	if multiDigitRe.MatchString(text) {
		return true
	}
	// Percentage
	if strings.Contains(text, "%") && strings.ContainsAny(text, "0123456789") {
		return true
	}
	// Single digit with meaningful context (iOS 15, A 方案, etc.)
	// But not standalone "1" or "2"
	digitRe := regexp.MustCompile(`\d`)
	if digitRe.MatchString(text) {
		// Check if there's other content around the digit
		textWithoutDigits := digitRe.ReplaceAllString(text, "")
		if len([]rune(strings.TrimSpace(textWithoutDigits))) >= 2 {
			return true
		}
	}
	return false
}

// extractContentTokens extracts CJK bigrams and Latin words (length ≥3, excluding common fillers).
// Returns distinct tokens.
func extractContentTokens(text string) []string {
	var tokens []string
	seen := make(map[string]bool)

	// Extract Latin words (length ≥3, excluding filler words)
	fillerWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true,
		"this": true, "from": true, "are": true, "was": true, "but": true,
	}
	wordRe := regexp.MustCompile(`[a-zA-Z]+`)
	for _, match := range wordRe.FindAllString(text, -1) {
		word := strings.ToLower(match)
		if len(word) >= 3 && !fillerWords[word] && !seen[word] {
			tokens = append(tokens, word)
			seen[word] = true
		}
	}

	// Extract CJK bigrams
	runes := []rune(text)
	for i := 0; i < len(runes)-1; i++ {
		r1, r2 := runes[i], runes[i+1]
		if isCJK(r1) && isCJK(r2) {
			bigram := string([]rune{r1, r2})
			if !seen[bigram] {
				tokens = append(tokens, bigram)
				seen[bigram] = true
			}
		}
	}

	return tokens
}

// isCJK checks if a rune is a CJK character.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Unified Ideographs Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Unified Ideographs Extension B
		(r >= 0x2A700 && r <= 0x2B73F) || // CJK Unified Ideographs Extension C
		(r >= 0x2B740 && r <= 0x2B81F) || // CJK Unified Ideographs Extension D
		(r >= 0x2B820 && r <= 0x2CEAF) || // CJK Unified Ideographs Extension E
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x2F800 && r <= 0x2FA1F) // CJK Compatibility Ideographs Supplement
}
