package collab

import (
	"context"
	"log/slog"
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
	if err != nil {
		p.log.Debug("judge call failed", "err", err)
		return "", err
	}
	return verdict, nil
}

// hasConcreteResidue checks if message has concrete evidence beyond deference phrases.
// Strict mode requires: number/date/URL/code/quote/question/Latin token, OR new ≥6-rune clause.
func hasConcreteResidue(text string) bool {
	stripped := strings.TrimSpace(text)
	if len([]rune(stripped)) < 6 {
		return false
	}

	// Check for concrete markers
	if strings.ContainsAny(stripped, "?？") {
		return true
	}
	if strings.Contains(stripped, "http") {
		return true
	}
	if strings.Contains(stripped, "```") {
		return true
	}
	// Number or date pattern
	for _, r := range stripped {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	// Latin letters (A-Z, a-z)
	for _, r := range stripped {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
	}
	// Quote marks
	if strings.ContainsAny(stripped, `「」『』""''`) {
		return true
	}

	// Check for substantive clauses (≥6 runes) beyond deference phrases
	deferencePatterns := []string{
		"好的", "了解", "收到", "明白", "知道", "同意", "沒問題", "沒意見",
		"謝謝", "辛苦", "感謝", "不客氣",
	}
	remaining := stripped
	for _, pat := range deferencePatterns {
		remaining = strings.ReplaceAll(remaining, pat, "")
	}
	remaining = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(remaining, "，", ""), "。", ""))
	return len([]rune(remaining)) >= 6
}
