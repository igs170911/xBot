package engine

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"xbot/internal/limits"
)

// EN-7：引擎或模型供應商的額度用完、限流、餘額不足、登入過期時，正規化成 QuotaError，
// runtime 收掉這一輪、說清楚，並開一張卡建議換引擎（或等額度恢復）。

// QuotaError 的種類。
const (
	// QuotaRateLimit 是暫時限流（太多請求），通常幾分鐘內恢復。
	QuotaRateLimit = "rate_limit"
	// QuotaExhausted 是方案額度用完（例：Claude 的 5 小時用量、Codex 的用量上限、Gemini 每日配額）。
	QuotaExhausted = "quota"
	// QuotaFunds 是儲值餘額或點數不足（例：OpenCode Zen、API 帳戶）。
	QuotaFunds = "funds"
	// QuotaAuth 是登入過期或被撤銷，要重新登入。
	QuotaAuth = "auth"
)

// QuotaError 是額度類的錯誤：哪個引擎（Hermes 時再加 Model Proxy 的供應商）、哪一種、大約什麼時候恢復。
type QuotaError struct {
	Engine string `json:"engine"`
	// Provider 是 Model Proxy 的供應商（Hermes 的 Bot 才有）。
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind"`
	// ResetAt 是預計恢復的時間（毫秒）；0 表示不知道。
	ResetAt int64 `json:"reset_at,omitempty"`
	// Message 是引擎或供應商原本的訊息（截短，不含金鑰）。
	Message string `json:"message,omitempty"`
}

func (e *QuotaError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "額度用完（" + e.Kind + "）"
}

// AsQuota 取出錯誤鏈裡的 QuotaError。
func AsQuota(err error) (*QuotaError, bool) {
	var q *QuotaError
	ok := errors.As(err, &q)
	return q, ok
}

// 錯誤文字的分類表：依序比對，第一個符合的算數。XBot 自己的花費上限（budget_exceeded）另有卡片，不算。
var (
	quotaSkipRe = regexp.MustCompile(`(?i)budget_exceeded|花費上限|context[_ ]?(window|length)|context_exhausted|maximum context`)
	quotaRules  = []struct {
		kind string
		re   *regexp.Regexp
	}{
		{QuotaFunds, regexp.MustCompile(`(?i)billing_error|insufficient[_ ](balance|funds|credits?)|insufficient account funds|` +
			`out of (usage )?credits|credit balance is too low|no (remaining )?credits|payment required|\b402\b|add funds|` +
			`balance is (too low|insufficient)|餘額不足|余额不足|點數不足`)},
		{QuotaExhausted, regexp.MustCompile(`(?i)usage[_ ]?limit|usageLimitExceeded|quota_exhausted|insufficient_quota|quota exceeded|` +
			`exceeded (your|the) (current |daily |monthly )?quota|resource_exhausted|exhausted your|you've hit your|you've reached your|` +
			`you're out of|out of usage|limit reached|reached (your|the) (usage |daily |weekly |monthly )?limit|daily quota|` +
			`額度(已)?用(完|盡)|用量(已)?(達|到)(上限)?`)},
		{QuotaRateLimit, regexp.MustCompile(`(?i)rate[_ -]?limit|rateLimitExceeded|too many requests|\b429\b|請求太頻繁|限流`)},
		{QuotaAuth, regexp.MustCompile(`(?i)authentication_failed|auth_required|authentication required|unauthori[sz]ed|\b401\b|` +
			`invalid[_ ](api[_ ]?)?key|invalid_grant|(token|session|login|credentials?) (has |have )?(expired|been revoked)|` +
			`expired (token|credentials|session)|please (log ?in|sign in|re-?authenticate)|not (logged|signed) in|` +
			`登入(已)?過期|還沒登入|重新登入`)},
	}
)

// outageRe 認出供應商那邊暫時壞掉（上游斷線、服務不可用）：有的供應商用 429 回這種錯，但不是你用太多
// （2026-10-21 OpenCode Go 回「Upstream request failed: Endpoint is unavailable.」，被說成「用量太多，被限流了」）。
var outageRe = regexp.MustCompile(`(?i)upstream|unavailable|server[_ ]error|overloaded|bad gateway|internal error`)

// Outage 回答這個「限流」其實是供應商暫時壞掉：不開額度卡，當成暫時的錯誤自動再試。
func (e *QuotaError) Outage() bool {
	return e.Kind == QuotaRateLimit && outageRe.MatchString(e.Message)
}

// ClassifyQuota 看一段錯誤文字是不是額度類的錯誤；不是時回 nil。now 用來算恢復時間。
func ClassifyQuota(engineName, text string, now time.Time) *QuotaError {
	t := strings.TrimSpace(text)
	if t == "" || quotaSkipRe.MatchString(t) {
		return nil
	}
	for _, r := range quotaRules {
		if !r.re.MatchString(t) {
			continue
		}
		q := &QuotaError{Engine: engineName, Kind: r.kind, Message: clipRunes(t, limits.QuotaMessageMax)}
		if r.kind != QuotaAuth {
			if at, ok := ParseReset(t, now); ok {
				q.ResetAt = at.UnixMilli()
			}
		}
		return q
	}
	return nil
}

// ClassifyHTTP 看模型供應商的 HTTP 回應（Model Proxy 轉送的）是不是額度類的錯誤；不是時回 nil。
// Retry-After 等標頭優先；其次是回應內容裡的時間。Engine、Provider 由呼叫的人填。
func ClassifyHTTP(status int, h http.Header, body []byte, now time.Time) *QuotaError {
	if status < 400 {
		return nil
	}
	text := strings.TrimSpace(string(body))
	var q *QuotaError
	switch status {
	case http.StatusTooManyRequests:
		q = ClassifyQuota("", text, now)
		if q == nil || q.Kind == QuotaAuth {
			q = &QuotaError{Kind: QuotaRateLimit}
		}
	case http.StatusPaymentRequired:
		q = &QuotaError{Kind: QuotaFunds}
	case http.StatusUnauthorized:
		q = &QuotaError{Kind: QuotaAuth}
	default:
		q = ClassifyQuota("", text, now)
		if q == nil || (q.Kind == QuotaAuth && status != http.StatusForbidden) {
			return nil
		}
	}
	if q.Message == "" {
		q.Message = clipRunes(firstNonEmpty(text, http.StatusText(status)), limits.QuotaMessageMax)
	}
	if q.Kind == QuotaAuth {
		q.ResetAt = 0
		return q
	}
	if at, ok := resetFromHeader(h, now); ok {
		q.ResetAt = at.UnixMilli()
	} else if q.ResetAt == 0 {
		if at, ok := ParseReset(text, now); ok {
			q.ResetAt = at.UnixMilli()
		}
	}
	return q
}

func resetFromHeader(h http.Header, now time.Time) (time.Time, bool) {
	if h == nil {
		return time.Time{}, false
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
			return now.Add(time.Duration(n * float64(time.Second))), true
		}
		if t, err := http.ParseTime(v); err == nil {
			return t, true
		}
	}
	// Anthropic 的訂閱用量（unified）與一般的限流標頭：epoch 秒、或秒數。
	for _, k := range []string{"Anthropic-Ratelimit-Unified-Reset", "X-Ratelimit-Reset", "X-Ratelimit-Reset-Requests", "X-Ratelimit-Reset-Tokens"} {
		v := strings.TrimSpace(h.Get(k))
		if v == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			if n > 1e9 {
				return epoch(int64(n)), true
			}
			return now.Add(time.Duration(n * float64(time.Second))), true
		}
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return now.Add(d), true
		}
	}
	return time.Time{}, false
}

var (
	// Claude 舊格式「Claude AI usage limit reached|1760000000」與 JSON 裡的 resetsAt／resets_at。
	resetEpochRe = regexp.MustCompile(`(?i)(?:\||"(?:resets?_?at|reset_?at|resetsAt)"\s*:\s*)(\d{10,13})\b`)
	resetSecsRe  = regexp.MustCompile(`(?i)"(?:resets?_in_seconds|retry_?after(?:_seconds)?|retryAfter)"\s*:\s*(\d+(?:\.\d+)?)`)
	retryDelayRe = regexp.MustCompile(`(?i)"retryDelay"\s*:\s*"(\d+(?:\.\d+)?)s"`)
	resetISORe   = regexp.MustCompile(`(?i)(?:reset|retry|again|until|after)[^0-9]{0,24}(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)`)
	// 「in 2 hours 5 minutes」「after 30s」「retry in 23.5s」。
	resetDurRe = regexp.MustCompile(`(?i)\b(?:in|after)\s+((?:\d+(?:\.\d+)?\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b[\s,]*(?:and\s+)?)+)`)
	durPartRe  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
	// 「try again at 3:05 PM」「resets 2pm」「resets at 14:00 (Asia/Taipei)」「約 14:20 恢復」。
	resetClockRe = regexp.MustCompile(`(?i)(?:try again|retry|resets?|available( again)?|back)\s*(?:at|on|after)?\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?`)
	// 中文的時長：「3 小時 20 分鐘後」。
	resetZhRe = regexp.MustCompile(`(?:(\d+)\s*小時)?\s*(?:(\d+)\s*分(?:鐘)?)?後`)
)

// ParseReset 從錯誤文字找出額度恢復的時間；找不到時 ok=false。時鐘時間（例：3:05 PM）照 now 的時區，取下一次出現的時間。
func ParseReset(text string, now time.Time) (time.Time, bool) {
	if m := resetEpochRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		if n > 1e12 {
			return time.UnixMilli(n), true
		}
		return epoch(n), true
	}
	if m := resetSecsRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return now.Add(time.Duration(n * float64(time.Second))), true
	}
	if m := retryDelayRe.FindStringSubmatch(text); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return now.Add(time.Duration(n * float64(time.Second))), true
	}
	if m := resetISORe.FindStringSubmatch(text); m != nil {
		v := strings.Replace(m[1], " ", "T", 1)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
			if t, err := time.ParseInLocation(layout, v, now.Location()); err == nil {
				return t, true
			}
		}
	}
	if m := resetDurRe.FindStringSubmatch(text); m != nil {
		var d time.Duration
		for _, p := range durPartRe.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.ParseFloat(p[1], 64)
			unit := strings.ToLower(p[2])
			switch {
			case strings.HasPrefix(unit, "d"):
				d += time.Duration(n * float64(24*time.Hour))
			case strings.HasPrefix(unit, "h"):
				d += time.Duration(n * float64(time.Hour))
			case strings.HasPrefix(unit, "m"):
				d += time.Duration(n * float64(time.Minute))
			default:
				d += time.Duration(n * float64(time.Second))
			}
		}
		if d > 0 {
			return now.Add(d), true
		}
	}
	for _, m := range resetClockRe.FindAllStringSubmatch(text, -1) {
		hour, _ := strconv.Atoi(m[2])
		minute := 0
		if m[3] != "" {
			minute, _ = strconv.Atoi(m[3])
		}
		ampm := strings.ToLower(strings.ReplaceAll(m[4], ".", ""))
		if m[3] == "" && ampm == "" {
			continue // 只有一個數字（例：「resets 5」）不算時間
		}
		switch ampm {
		case "pm":
			if hour < 12 {
				hour += 12
			}
		case "am":
			if hour == 12 {
				hour = 0
			}
		}
		if hour > 23 || minute > 59 {
			continue
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !t.After(now) {
			t = t.Add(24 * time.Hour)
		}
		return t, true
	}
	if m := resetZhRe.FindStringSubmatch(text); m != nil && (m[1] != "" || m[2] != "") {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		return now.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute), true
	}
	return time.Time{}, false
}

func epoch(sec int64) time.Time { return time.Unix(sec, 0) }

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// QuotaCardKind 是「額度用完」卡（待我處理，要你決定）的種類。
const QuotaCardKind = "engine_quota"

// SwitchOption 是額度用完卡上的一個選項：把 Bot 換到你有的另一個引擎或模型（xbot.engines.switch）。
type SwitchOption struct {
	Engine string `json:"engine"`
	// Model：Hermes 是「<供應商>/<模型>」；ACP 引擎空白＝用它自己的預設模型。
	Model string `json:"model,omitempty"`
	// Label 是選項上的字，例：「Codex CLI（ChatGPT 方案）」。
	Label string `json:"label"`
	// Note 是右邊的小字，例：「已登入」「要你同意」。
	Note string `json:"note,omitempty"`
	// NeedsLink：這個 Bot 還沒同意用你的這份訂閱，選它等於同意（xbot.engines.switch link=true）。
	NeedsLink bool `json:"needs_link,omitempty"`
}

// QuotaLog 記下每個 Bot 最近一次的額度類錯誤（Model Proxy 轉送時看到的供應商回應）。
// Hermes 只把錯誤變成一段文字；runtime 收掉失敗的一輪時，用這裡的結構化資料（供應商、恢復時間）。
type QuotaLog struct {
	// Now 預設 time.Now（測試可以換）。
	Now func() time.Time

	mu sync.Mutex
	m  map[string]quotaSeen
}

type quotaSeen struct {
	q  QuotaError
	at time.Time
}

func (l *QuotaLog) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Record 記下一個 Bot 的額度類錯誤。
func (l *QuotaLog) Record(botID string, q *QuotaError) {
	if l == nil || q == nil || botID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]quotaSeen{}
	}
	l.m[botID] = quotaSeen{q: *q, at: l.now()}
}

// Since 回傳 Bot 在 since 之後（且在 limits.QuotaRecent 之內）記下的額度類錯誤；沒有時回 nil。
func (l *QuotaLog) Since(botID string, since time.Time) *QuotaError {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.m[botID]
	if !ok || s.at.Before(since) || l.now().Sub(s.at) > limits.QuotaRecent {
		return nil
	}
	q := s.q
	return &q
}

// Forget 清掉 Bot 的紀錄（換了引擎或模型之後）。
func (l *QuotaLog) Forget(botID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.m, botID)
	l.mu.Unlock()
}
