package acp

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"xbot/internal/engine"
	"xbot/internal/limits"
)

// update 是 session/update 通知的 params.update（只列用得到的欄位）。
type update struct {
	SessionUpdate string `json:"sessionUpdate"`
	// agent_message_chunk／agent_thought_chunk
	Content json.RawMessage `json:"content,omitempty"`
	// tool_call／tool_call_update
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage `json:"rawOutput,omitempty"`
	// plan
	Entries []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"entries,omitempty"`
	// usage_update
	Used int64 `json:"used,omitempty"`
	Size int64 `json:"size,omitempty"`
	Cost *struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"cost,omitempty"`
}

type updateParams struct {
	SessionID string `json:"sessionId"`
	Update    update `json:"update"`
	Meta      struct {
		IsReplay bool `json:"isReplay"`
	} `json:"_meta"`
}

// toolState 記住進行中的工具呼叫（tool_call_update 不一定再帶名稱）。
type toolState struct {
	name    string
	started time.Time
	done    bool
}

// textOf 取出 ContentBlock 的文字（只處理 text；圖片等先略過）。
func textOf(raw json.RawMessage) string {
	var c struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &c) != nil || (c.Type != "" && c.Type != "text") {
		return ""
	}
	return c.Text
}

// normalize 把一個 session/update 轉成正規化事件。tools 是這個 session 的工具狀態（呼叫端持有鎖）。
func normalize(u update, tools map[string]*toolState, now time.Time) []engine.Event {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if t := textOf(u.Content); t != "" {
			return []engine.Event{{Type: engine.EventText, Text: t}}
		}
	case "agent_thought_chunk":
		if t := textOf(u.Content); t != "" {
			return []engine.Event{{Type: engine.EventThinking, Text: t}}
		}
	case "tool_call":
		name := firstNonEmpty(u.Title, u.Kind, "tool")
		ts := &toolState{name: name, started: now}
		tools[u.ToolCallID] = ts
		evs := []engine.Event{{Type: engine.EventToolCall, Tool: &engine.ToolEvent{
			ID: u.ToolCallID, Name: name, Args: clip(u.RawInput), Context: u.Kind,
		}}}
		if u.Status == "completed" || u.Status == "failed" {
			evs = append(evs, toolResult(u, ts, now))
		}
		return evs
	case "tool_call_update":
		ts := tools[u.ToolCallID]
		if ts == nil {
			ts = &toolState{name: firstNonEmpty(u.Title, u.Kind, "tool"), started: now}
			tools[u.ToolCallID] = ts
		} else if u.Title != "" {
			ts.name = u.Title
		}
		if (u.Status == "completed" || u.Status == "failed") && !ts.done {
			return []engine.Event{toolResult(u, ts, now)}
		}
	case "plan":
		if len(u.Entries) == 0 {
			return nil
		}
		var b strings.Builder
		b.WriteString("計畫：")
		for _, e := range u.Entries {
			mark := "☐"
			switch e.Status {
			case "in_progress":
				mark = "▶"
			case "completed":
				mark = "☑"
			}
			b.WriteString("\n" + mark + " " + e.Content)
		}
		return []engine.Event{{Type: engine.EventStatus, Text: b.String()}}
	case "usage_update":
		us := &engine.Usage{ContextUsed: u.Used, ContextMax: u.Size}
		if u.Cost != nil && strings.EqualFold(u.Cost.Currency, "USD") {
			us.CostUSD = u.Cost.Amount
		}
		return []engine.Event{{Type: engine.EventUsage, Usage: us}}
	}
	return nil
}

func toolResult(u update, ts *toolState, now time.Time) engine.Event {
	ts.done = true
	out := u.RawOutput
	if len(out) == 0 {
		out = u.Content
	}
	return engine.Event{Type: engine.EventToolResult, Tool: &engine.ToolEvent{
		ID: u.ToolCallID, Name: ts.name, OK: u.Status == "completed", Result: clip(out),
		Duration: now.Sub(ts.started).Seconds(),
	}}
}

// clip 讓很大的工具輸入輸出（例：截圖的 base64）不整包進事件紀錄。
func clip(raw json.RawMessage) json.RawMessage {
	if len(raw) <= limits.ACPToolPayloadBytes {
		return raw
	}
	s, _ := json.Marshal(string(raw[:limits.ACPToolPayloadBytes]) + "…（內容太長，已截斷）")
	return s
}

// promptUsage 是 session/prompt 回覆裡的用量（ACP 的 usage，選用）。
type promptUsage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	ThoughtTokens     int64 `json:"thoughtTokens"`
	CachedReadTokens  int64 `json:"cachedReadTokens"`
	CachedWriteTokens int64 `json:"cachedWriteTokens"`
}

// stopStatus 把 ACP 的 stopReason 對到 done 的狀態與說明。
func stopStatus(reason string) (status, note string) {
	switch reason {
	case "cancelled":
		return engine.StatusInterrupted, ""
	case "refusal":
		return engine.StatusError, "模型拒絕回答這個要求"
	case "max_tokens":
		return engine.StatusComplete, "回覆太長，被截斷了"
	case "max_turn_requests":
		return engine.StatusComplete, "這一輪的步驟數到上限了"
	}
	return engine.StatusComplete, ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// queue 是一個 session 的事件佇列：放入永遠不會卡住（讀 agent 輸出的迴圈不能被慢的讀者拖住），
// 由一個 goroutine 依序送到 out。同 Hermes 驅動。
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
