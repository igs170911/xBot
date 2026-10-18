package engine

import (
	"net/http"
	"testing"
	"time"
)

func taipei(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Skip("沒有時區資料")
	}
	return loc
}

// 各引擎與供應商實際的錯誤文字 → 種類與恢復時間（EN-7）。
func TestClassifyQuota(t *testing.T) {
	loc := taipei(t)
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, loc)
	at := func(h, m int) int64 { return time.Date(2026, 10, 18, h, m, 0, 0, loc).UnixMilli() }
	tomorrow := func(h, m int) int64 { return time.Date(2026, 10, 19, h, m, 0, 0, loc).UnixMilli() }
	cases := []struct {
		name, engine, text string
		kind               string // 空白＝不是額度類
		reset              int64
	}{
		// Claude Code（claude-agent-acp：USAGE_LIMIT_ERROR_PREFIXES、舊格式 |epoch、errorKind）
		{"claude 5 小時", "claude-code", "You've hit your limit · resets 2pm (Asia/Taipei)", QuotaExhausted, at(14, 0)},
		{"claude 舊格式", "claude-code", "Claude AI usage limit reached|1792339200", QuotaExhausted, time.Unix(1792339200, 0).UnixMilli()},
		{"claude errorKind rate_limit", "claude-code", `API Error: Request rejected (429) {"errorKind":"rate_limit"}`, QuotaRateLimit, 0},
		{"claude billing", "claude-code", `Credit balance is too low {"errorKind":"billing_error"}`, QuotaFunds, 0},
		{"claude 登入", "claude-code", `Invalid API key · Please run /login {"errorKind":"authentication_failed"}`, QuotaAuth, 0},
		{"claude 額外用量", "claude-code", "You're out of extra usage · resets 9:30am", QuotaExhausted, tomorrow(9, 30)},
		// Codex CLI（codex-acp：usageLimitExceeded、try again at / in）
		{"codex 用量", "codex", `You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), or try again at 3:05 PM. {"codexErrorInfo":"usageLimitExceeded"}`, QuotaExhausted, at(15, 5)},
		{"codex 幾小時後", "codex", "You've hit your usage limit. Try again in 2 hours 5 minutes.", QuotaExhausted, now.Add(2*time.Hour + 5*time.Minute).UnixMilli()},
		{"codex 限流", "codex", `stream error: rateLimitExceeded {"resets_in_seconds": 90}`, QuotaRateLimit, now.Add(90 * time.Second).UnixMilli()},
		// Gemini CLI（Code Assist 429 RESOURCE_EXHAUSTED、retryDelay）
		{"gemini 每日配額", "gemini", `[API Error: You have exhausted your daily quota on this model.]`, QuotaExhausted, 0},
		{"gemini 429", "gemini", `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"retryDelay":"23s"}]}}`, QuotaExhausted, now.Add(23 * time.Second).UnixMilli()},
		// OpenCode（Go 訂閱、Zen 餘額）
		{"opencode Zen 餘額", "opencode", "Insufficient account funds", QuotaFunds, 0},
		{"opencode Go 用量", "opencode", "OpenCode Go usage limit reached. Resets at 2026-10-18T16:00:00+08:00", QuotaExhausted, at(16, 0)},
		// Grok
		{"grok 限流", "grok", "Rate limit exceeded. Please retry after 30s", QuotaRateLimit, now.Add(30 * time.Second).UnixMilli()},
		{"grok 登入", "grok", "Authentication required: token has expired", QuotaAuth, 0},
		// Hermes 經 Model Proxy
		{"hermes 429", "hermes", "Error code: 429 - {'error': {'message': 'Too Many Requests'}}", QuotaRateLimit, 0},
		{"hermes insufficient_quota", "hermes", "Error code: 429 - {'error': {'type': 'insufficient_quota', 'message': 'You exceeded your current quota'}}", QuotaExhausted, 0},
		{"中文", "hermes", "額度用完了，約 3 小時 20 分鐘後恢復", QuotaExhausted, now.Add(3*time.Hour + 20*time.Minute).UnixMilli()},
		// 不是額度類
		{"花費上限另有卡", "hermes", "402 budget_exceeded：已超過這個 Bot 每日的花費上限", "", 0},
		{"一般錯誤", "claude-code", "找不到檔案 /workspace/a.txt", "", 0},
		{"斷線", "codex", "和 Bot 的連線已中斷", "", 0},
		{"context", "claude-code", "Prompt is too long: maximum context length exceeded (429 tokens over)", "", 0},
		{"空白", "grok", "  ", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ClassifyQuota(c.engine, c.text, now)
			if c.kind == "" {
				if q != nil {
					t.Fatalf("不該是額度類：%+v", q)
				}
				return
			}
			if q == nil {
				t.Fatalf("要認出 %s", c.kind)
			}
			if q.Kind != c.kind || q.Engine != c.engine || q.ResetAt != c.reset {
				t.Fatalf("得到 %+v（恢復 %v），要 %s／%v", q, time.UnixMilli(q.ResetAt).In(loc), c.kind, time.UnixMilli(c.reset).In(loc))
			}
			if q.Message == "" {
				t.Fatal("要保留原始訊息")
			}
		})
	}
}

func TestClassifyHTTP(t *testing.T) {
	now := time.Date(2026, 10, 18, 2, 0, 0, 0, time.UTC)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
		kind   string
		reset  int64
	}{
		{"429 Retry-After 秒", 429, h("Retry-After", "120"), `{"error":{"message":"rate limited"}}`, QuotaRateLimit, now.Add(2 * time.Minute).UnixMilli()},
		{"429 insufficient_quota", 429, nil, `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`, QuotaExhausted, 0},
		{"429 Anthropic unified", 429, h("Anthropic-Ratelimit-Unified-Reset", "1792339200"), `{"type":"error"}`, QuotaRateLimit, time.Unix(1792339200, 0).UnixMilli()},
		{"402", 402, nil, `{"error":"Insufficient account funds"}`, QuotaFunds, 0},
		{"401", 401, nil, `{"error":"invalid token"}`, QuotaAuth, 0},
		{"403 額度", 403, nil, `{"error":{"message":"Quota exceeded for quota metric"}}`, QuotaExhausted, 0},
		{"400 不算", 400, nil, `{"error":"bad model"}`, "", 0},
		{"500 不算", 500, nil, `internal error`, "", 0},
		{"200", 200, nil, `ok`, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := ClassifyHTTP(c.status, c.header, []byte(c.body), now)
			if c.kind == "" {
				if q != nil {
					t.Fatalf("不該是額度類：%+v", q)
				}
				return
			}
			if q == nil || q.Kind != c.kind || q.ResetAt != c.reset {
				t.Fatalf("得到 %+v，要 %s／%d", q, c.kind, c.reset)
			}
		})
	}
}

func TestQuotaLog(t *testing.T) {
	now := time.Date(2026, 10, 18, 2, 0, 0, 0, time.UTC)
	l := &QuotaLog{Now: func() time.Time { return now }}
	start := now.Add(-time.Minute)
	if l.Since("b1", start) != nil {
		t.Fatal("還沒有紀錄")
	}
	l.Record("b1", &QuotaError{Engine: "hermes", Provider: "opencode-go", Kind: QuotaExhausted})
	if q := l.Since("b1", start); q == nil || q.Provider != "opencode-go" {
		t.Fatalf("要拿到剛記的：%+v", q)
	}
	if l.Since("b1", now.Add(time.Second)) != nil {
		t.Fatal("這一輪開始之前的不算")
	}
	if l.Since("b2", start) != nil {
		t.Fatal("別的 Bot 不算")
	}
	now = now.Add(time.Hour)
	if l.Since("b1", start) != nil {
		t.Fatal("太久以前的不算")
	}
	l.Forget("b1")
	var nilLog *QuotaLog
	nilLog.Record("b1", &QuotaError{})
	if nilLog.Since("b1", start) != nil {
		t.Fatal("nil 的 QuotaLog 什麼都沒有")
	}
}
