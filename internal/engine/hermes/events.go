package hermes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"xbot/internal/engine"
)

// normalize 把一個 Hermes 事件轉成零或多個 engine.Event（對應 LazyBoy runtime.event()）。
// 不認得的事件（session.title、subagent.*、voice.* 等）略過。
func normalize(p eventParams) []engine.Event {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(p.Payload, &payload)
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(payload[k], &s)
		return s
	}
	switch p.Type {
	case "message.delta":
		if t := str("text"); t != "" {
			return []engine.Event{{Type: engine.EventText, Text: t}}
		}
	case "reasoning.delta", "thinking.delta", "reasoning.available":
		if t := str("text"); t != "" {
			return []engine.Event{{Type: engine.EventThinking, Text: t}}
		}
	case "tool.start":
		return []engine.Event{{Type: engine.EventToolCall, Tool: tool(payload, str)}}
	case "tool.complete":
		t := tool(payload, str)
		t.OK = !toolFailed(payload)
		return []engine.Event{{Type: engine.EventToolResult, Tool: t}}
	case "session.usage":
		var u struct {
			Usage *hermesUsage `json:"usage"`
		}
		if json.Unmarshal(p.Payload, &u) == nil && u.Usage != nil {
			return []engine.Event{{Type: engine.EventUsage, Usage: u.Usage.normalize()}}
		}
	case "status.update":
		if t := str("text"); t != "" {
			return []engine.Event{{Type: engine.EventStatus, Text: t}}
		}
	case "error":
		msg := str("message")
		if msg == "" {
			msg = "Bot 發生錯誤"
		}
		return []engine.Event{{Type: engine.EventError, Text: msg}}
	case "message.complete":
		return complete(p.Payload)
	}
	return nil
}

func tool(payload map[string]json.RawMessage, str func(string) string) *engine.ToolEvent {
	t := &engine.ToolEvent{ID: str("tool_id"), Name: str("name"), Context: str("context"), Summary: str("summary")}
	if a := payload["args"]; len(a) > 0 && string(a) != "null" {
		t.Args = a
	}
	if r := payload["result"]; len(r) > 0 && string(r) != "null" {
		t.Result = r
	}
	_ = json.Unmarshal(payload["duration_s"], &t.Duration)
	return t
}

// complete 處理 message.complete：用量、（失敗時）錯誤，最後一定是 done。
// 狀態照 LazyBoy：interrupted／cancelled／aborted 算中斷，error／failed 算失敗，其他（含沒寫）算完成。
func complete(raw json.RawMessage) []engine.Event {
	var c struct {
		Text          any          `json:"text"`
		Status        string       `json:"status"`
		Usage         *hermesUsage `json:"usage"`
		Error         string       `json:"error"`
		FailureReason string       `json:"failure_reason"`
	}
	_ = json.Unmarshal(raw, &c)
	text, _ := c.Text.(string)
	status := engine.StatusComplete
	switch c.Status {
	case "interrupted", "cancelled", "aborted":
		status = engine.StatusInterrupted
	case "error", "failed":
		status = engine.StatusError
	}
	var out []engine.Event
	if c.Usage != nil {
		out = append(out, engine.Event{Type: engine.EventUsage, Usage: c.Usage.normalize()})
	}
	if status == engine.StatusError {
		msg := firstNonEmpty(c.Error, c.FailureReason, strings.TrimPrefix(text, "Error: "), "工作失敗")
		out = append(out, engine.Event{Type: engine.EventError, Text: msg})
	}
	done := engine.Event{Type: engine.EventDone, Status: status, Text: text}
	if loop := loopHalt(text); loop != nil {
		done.Loop = loop
		done.Text = fmt.Sprintf("我一直重複用 %s（%d 次）都沒有新進展，先停下來。", loop.Tool, loop.Count)
	}
	return append(out, done)
}

// loopHaltRe 是 Hermes 一直重複同一個工具、被 tool guardrail 停下時固定回的那段英文（run_agent.py
// _toolguard_controlled_halt_response）。message.complete 不帶 guardrail 欄位，只能認這段字。
// 2026-10-20：beam-free 把同一個 web_search 送了 50 次，這段英文直接顯示給使用者，還被判成「已完成」。
var loopHaltRe = regexp.MustCompile(`^I stopped retrying because I kept running (\S+) (\d+) times without making progress\.`)

func loopHalt(text string) *engine.Loop {
	m := loopHaltRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return nil
	}
	n, _ := strconv.Atoi(m[2])
	return &engine.Loop{Tool: m[1], Count: n}
}

// hermesUsage 是 Hermes 的 Usage（tui_gateway/contracts/common.py）。
type hermesUsage struct {
	Model       string   `json:"model"`
	Input       int64    `json:"input"`
	Output      int64    `json:"output"`
	Reasoning   int64    `json:"reasoning"`
	Calls       int64    `json:"calls"`
	ContextUsed int64    `json:"context_used"`
	ContextMax  int64    `json:"context_max"`
	CacheRead   int64    `json:"cache_read"`
	CacheWrite  int64    `json:"cache_write"`
	CostUSD     *float64 `json:"cost_usd"`
}

func (u *hermesUsage) normalize() *engine.Usage {
	out := &engine.Usage{Model: u.Model, InputTokens: u.Input, OutputTokens: u.Output, Reasoning: u.Reasoning,
		CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Calls: u.Calls, ContextUsed: u.ContextUsed, ContextMax: u.ContextMax}
	if u.CostUSD != nil {
		out.CostUSD = *u.CostUSD
	}
	return out
}

// toolFailed 照 LazyBoy runtime.tool_failed：Hermes 的 tool.complete 只帶原始結果，沒有失敗旗標。
func toolFailed(payload map[string]json.RawMessage) bool {
	var e any
	if json.Unmarshal(payload["error"], &e) == nil && truthy(e) {
		return true
	}
	var result any
	if json.Unmarshal(payload["result"], &result) != nil {
		return false
	}
	if s, ok := result.(string); ok {
		if json.Unmarshal([]byte(s), &result) != nil {
			return false
		}
	}
	m, ok := result.(map[string]any)
	if !ok {
		return false
	}
	if v, ok := m["success"].(bool); ok && !v {
		return true
	}
	if v, ok := m["ok"].(bool); ok && !v {
		return true
	}
	if s, ok := m["error"].(string); ok && strings.TrimSpace(s) != "" {
		return true
	}
	if code, ok := m["exit_code"].(float64); ok && code != 0 && code == float64(int64(code)) {
		return true
	}
	return false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// queue 是一個 session 的事件佇列：放入永遠不會卡住（Hermes 的讀取迴圈不能被慢的讀者拖住），
// 由一個 goroutine 依序送到 out。
type queue struct {
	mu     sync.Mutex
	items  []engine.Event
	closed bool
	wake   chan struct{}
	out    chan engine.Event
}

func newQueue() *queue {
	q := &queue{wake: make(chan struct{}, 1), out: make(chan engine.Event, 64)}
	go q.pump()
	return q
}

func (q *queue) push(e engine.Event) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, e)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) pump() {
	defer close(q.out)
	for {
		q.mu.Lock()
		items, closed := q.items, q.closed
		q.items = nil
		q.mu.Unlock()
		for _, e := range items {
			q.out <- e
		}
		if closed && len(items) == 0 {
			return
		}
		if len(items) == 0 {
			<-q.wake
		}
	}
}
