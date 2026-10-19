package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/version"
)

// 模型探測（PRD EN-4），翻自 LazyBoy server/model_check.py：
// 每種呼叫方式送一個很小的請求（最可能的先），留下能用的那一種；失敗時分成幾種使用者能處理的原因。

// ProbeSettingPrefix 是探測結果在 setting 表的 key 開頭：model.probe.<供應商>。
const ProbeSettingPrefix = "model.probe."

// 探測結果的狀態。
const (
	CheckOK          = "ok"
	CheckProtocol    = "protocol"
	CheckUnavailable = "unavailable"
	CheckAuth        = "auth"
	CheckBusy        = "busy"
	CheckUnknown     = "unknown"
	// CheckFreeTier：OpenCode Zen 的免費模型（*-free）只給 OpenCode 自己的程式用，回 FreeTierError。XBot 新增。
	CheckFreeTier = "free_tier"
)

var checkMessages = map[string]string{
	CheckOK:          "可以使用",
	CheckProtocol:    "模型的 API 介面設定不對",
	CheckUnavailable: "這個模型目前無法使用，請換一個模型",
	CheckAuth:        "API 金鑰無效或額度不足",
	CheckBusy:        "供應商暫時忙碌或無法連線，稍後再試",
	CheckUnknown:     "無法確認這個模型能不能用，詳細原因請看提示",
	CheckFreeTier:    "這是只給 OpenCode 自己程式用的免費模型，XBot 不能用，請換付費模型",
}

// 順序有意義：抱怨請求參數，表示端點認得這個模型（model_check.py _RULES）。
var checkRules = []struct {
	kind string
	re   *regexp.Regexp
}{
	{CheckFreeTier, regexp.MustCompile(`FreeTierError`)},
	{CheckOK, regexp.MustCompile(`(?i)max_(?:output_)?tokens|max_completion_tokens|temperature|unsupported (?:parameter|value)|unknown parameter`)},
	{CheckProtocol, regexp.MustCompile(`(?i)protocol|not support(?:ed)? (?:on|by|for) this (?:endpoint|api)|unsupported (?:endpoint|api|route)|no route|unknown (?:endpoint|url|path)|invalid url|use the (?:responses|messages|chat)`)},
	{CheckUnavailable, regexp.MustCompile(`(?i)model[^.]{0,40}(?:unavailable|not (?:found|available|exist|supported))|does not exist|no such model|unknown model|invalid model|model_not_found|not a valid model`)},
	{CheckAuth, regexp.MustCompile(`(?i)api[ _-]?key|unauthori[sz]ed|authentication|forbidden|permission|credit|balance|quota exceeded|insufficient`)},
	{CheckBusy, regexp.MustCompile(`(?i)rate.?limit|too many requests|overloaded|endpoint is unavailable|temporarily|try again|timeout|capacity`)},
}

// ClassifyCheck 把 HTTP 狀態與錯誤文字分成 ok／protocol／unavailable／auth／busy／unknown（model_check.py classify）。
func ClassifyCheck(status int, text string) string {
	if status >= 200 && status < 300 {
		return CheckOK
	}
	for _, r := range checkRules {
		if r.re.MatchString(text) {
			return r.kind
		}
	}
	switch {
	case status == 401 || status == 403:
		return CheckAuth
	case status == 429 || status >= 500:
		return CheckBusy
	case status == 404 || status == 405:
		return CheckProtocol
	}
	// 看不懂的錯誤不代表模型不存在。
	return CheckUnknown
}

var anthropicURL = regexp.MustCompile(`/anthropic(/v1)?/?$`)

// opencodeRules 是 Hermes 對 OpenCode 轉售站的模型呼叫方式規則（model_check.py _OPENCODE_RULES）。
var opencodeRules = map[string][]struct {
	prefixes  []string
	transport string
}{
	"opencode-go": {
		{[]string{"gpt-", "grok-", "muse-spark"}, TransportResponses},
		{[]string{"minimax-", "qwen", "union-alpha"}, TransportMessages},
	},
	"opencode-zen": {
		{[]string{"claude-", "union-alpha"}, TransportMessages},
		{[]string{"gpt-", "grok-", "muse-spark"}, TransportResponses},
		{[]string{"qwen"}, TransportMessages},
	},
}

// OpenCodeFamily 回傳供應商是 OpenCode 的哪一種：opencode-go（訂閱方案）、opencode-zen（按量付費）；都不是時回空白。
func OpenCodeFamily(providerID, baseURL string) string {
	u, p := strings.ToLower(baseURL), strings.ToLower(providerID)
	switch {
	case p == "opencode-go" || strings.Contains(u, "opencode.ai/zen/go"):
		return "opencode-go"
	case p == "opencode-zen" || p == "opencode" || strings.Contains(u, "opencode.ai/zen"):
		return "opencode-zen"
	}
	return ""
}

// RuleTransport 回傳 Hermes 規則決定的呼叫方式；規則不管時回空白（model_check.py rule_protocol）。
func RuleTransport(providerID, baseURL, model string) string {
	u, p := strings.ToLower(baseURL), strings.ToLower(providerID)
	if p == "anthropic" || strings.Contains(u, "api.anthropic.com") || anthropicURL.MatchString(u) {
		return TransportMessages
	}
	family := OpenCodeFamily(providerID, baseURL)
	if family == "" {
		return ""
	}
	name := strings.ToLower(model)
	name = strings.TrimPrefix(strings.TrimPrefix(name, "opencode-zen/"), "opencode-go/")
	for _, r := range opencodeRules[family] {
		for _, pre := range r.prefixes {
			if strings.HasPrefix(name, pre) {
				return r.transport
			}
		}
	}
	return TransportChat
}

// ModelCheck 是一個模型的探測結果。
type ModelCheck struct {
	Model     string `json:"model"`
	Status    string `json:"status" desc:"ok、protocol、unavailable、auth、busy、unknown、free_tier"`
	Transport string `json:"transport,omitempty" desc:"能用的呼叫方式（status=ok 時）"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty" desc:"供應商回的錯誤（前 400 字）"`
	At        int64  `json:"at"`
}

// ProbeResult 是一個供應商的探測結果（存在 setting 表 model.probe.<供應商>）。
type ProbeResult struct {
	Provider  string                `json:"provider"`
	At        int64                 `json:"at"`
	Models    []string              `json:"models" desc:"供應商 /models 列出的模型"`
	ListError string                `json:"list_error,omitempty" desc:"列模型失敗的原因"`
	Checks    map[string]ModelCheck `json:"checks" desc:"每個測過的模型能不能用、用哪種呼叫方式"`
}

// Prober 對供應商送探測請求。
type Prober struct {
	Client  *http.Client // 預設 NewUpstreamClient()
	Timeout time.Duration
	Now     func() time.Time
}

func (p *Prober) client() *http.Client {
	if p.Client == nil {
		p.Client = NewUpstreamClient()
	}
	return p.Client
}

func (p *Prober) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Prober) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return limits.ModelProbeTimeout
}

func setAuth(h http.Header, key Secret, messages bool) {
	h.Set("User-Agent", "XBot/"+version.Version+" model check")
	if key != "" {
		h.Set("Authorization", "Bearer "+string(key))
	}
	if messages {
		if key != "" {
			h.Set("X-Api-Key", string(key))
		}
		h.Set("Anthropic-Version", "2023-06-01")
	}
}

// ListModels 讀供應商的 /models。失敗時回空清單與原因。
func (p *Prober) ListModels(ctx context.Context, pr Provider) ([]string, string) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pr.BaseURL+"/models", nil)
	if err != nil {
		return nil, err.Error()
	}
	setAuth(req.Header, pr.APIKey, pr.Transport == TransportMessages || RuleTransport(pr.ID, pr.BaseURL, "") == TransportMessages)
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, clip(redactString(err.Error(), string(pr.APIKey)))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limits.ModelProbeBodyBytes))
	if resp.StatusCode >= 300 {
		return nil, clip(redactString(errorText(resp.StatusCode, body), string(pr.APIKey)))
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, "模型清單不是 JSON"
	}
	var out []string
	for _, m := range list.Data {
		out = append(out, m.ID)
	}
	for _, m := range list.Models {
		out = append(out, cmpOr(m.ID, m.Name))
	}
	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > limits.ModelProbeMaxModels {
		out = out[:limits.ModelProbeMaxModels]
	}
	return out, ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// orderedTransports 把猜測的呼叫方式排第一。
func orderedTransports(guess string) []string {
	if !slices.Contains(Transports, guess) {
		return slices.Clone(Transports)
	}
	out := []string{guess}
	for _, t := range Transports {
		if t != guess {
			out = append(out, t)
		}
	}
	return out
}

func probeBody(transport, model string) []byte {
	var body any
	switch transport {
	case TransportMessages:
		body = map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	case TransportResponses:
		body = map[string]any{"model": model, "max_output_tokens": 16, "input": "hi"}
	default:
		body = map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	}
	b, _ := json.Marshal(body)
	return b
}

// CheckModel 依序試每種呼叫方式（guess 先），回傳第一個能用的；
// 金鑰不對、供應商忙碌、看不懂的錯誤就停（這些和呼叫方式無關，不要誤判成別的方式能用）。
func (p *Prober) CheckModel(ctx context.Context, pr Provider, model, guess string) ModelCheck {
	at := p.now().UnixMilli()
	lastKind, lastDetail := CheckUnavailable, ""
	firstFailKind, firstFailDetail := "", ""
	for _, t := range orderedTransports(guess) {
		kind, detail := p.try(ctx, pr, model, t)
		if kind == CheckOK {
			return ModelCheck{Model: model, Status: CheckOK, Transport: t, Message: checkMessages[CheckOK], At: at}
		}
		lastKind, lastDetail = kind, detail
		if kind == CheckAuth || kind == CheckBusy || kind == CheckUnknown || kind == CheckFreeTier {
			break
		}
		if kind == CheckUnavailable && firstFailKind == "" {
			firstFailKind, firstFailDetail = kind, detail
		}
	}
	// 每種方式都不行：回報最可能的那種方式對這個模型的說法。
	if lastKind == CheckProtocol && firstFailKind != "" {
		lastKind, lastDetail = firstFailKind, firstFailDetail
	}
	if lastKind == CheckProtocol {
		lastKind = CheckUnavailable
	}
	return ModelCheck{Model: model, Status: lastKind, Message: checkMessages[lastKind], Detail: lastDetail, At: at}
}

func (p *Prober) try(ctx context.Context, pr Provider, model, transport string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pr.BaseURL+"/"+TransportPath(transport), bytes.NewReader(probeBody(transport, model)))
	if err != nil {
		return CheckUnknown, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req.Header, pr.APIKey, transport == TransportMessages)
	if h := SessionHeaderFor(pr); h != "" {
		// OpenCode 沒有 session id 會拒絕（LazyBoy 同樣處理）。
		req.Header.Set(h, "xbot-check-"+pr.ID)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return CheckBusy, clip(redactString(err.Error(), string(pr.APIKey)))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limits.ModelProbeBodyBytes))
	detail := ""
	if resp.StatusCode >= 300 {
		detail = clip(redactString(errorText(resp.StatusCode, body), string(pr.APIKey)))
	}
	return ClassifyCheck(resp.StatusCode, detail), detail
}

// errorText 取出錯誤回應裡的 message（model_check.py _error_text）。
func errorText(status int, body []byte) string {
	var v any
	if json.Unmarshal(body, &v) == nil {
		if m, ok := v.(map[string]any); ok {
			e := any(m)
			if inner, ok := m["error"]; ok {
				e = inner
			}
			if em, ok := e.(map[string]any); ok {
				if msg, ok := em["message"].(string); ok && msg != "" {
					// 錯誤種類（例：OpenCode 的 FreeTierError）也留著，分類要用。
					if typ, ok := em["type"].(string); ok && typ != "" && !strings.Contains(msg, typ) {
						return typ + ": " + msg
					}
					return msg
				}
			}
			if s, ok := e.(string); ok {
				return s
			}
			b, _ := json.Marshal(e)
			return string(b)
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return http.StatusText(status)
	}
	return string(body)
}

// clip 只留前 limits.ModelProbeDetailChars 個字。
func clip(s string) string {
	if utf8.RuneCountInString(s) <= limits.ModelProbeDetailChars {
		return s
	}
	return string([]rune(s)[:limits.ModelProbeDetailChars])
}
