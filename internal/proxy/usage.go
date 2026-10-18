package proxy

import (
	"bytes"
	"encoding/json"

	"xbot/internal/limits"
)

// 從 LazyBoy server/usage.py 翻過來：回應經過代理時順便讀出 usage，
// 支援 Chat Completions、Responses、Anthropic Messages 三種格式，串流與非串流都可以。

// counts 是正規化後的用量；input 一律包含快取命中的部分。
type counts struct {
	Input, Output, Cached, CacheWrite, Reasoning int64
	Cost                                         float64
	// Seconds 是語音辨識按秒計費時回報的長度（usage.type = duration，例：whisper-1）。
	Seconds float64
}

func num(v any) float64 {
	if f, ok := v.(float64); ok && f >= 0 {
		return f
	}
	return 0
}

func sub(m map[string]any, key string) map[string]any {
	if d, ok := m[key].(map[string]any); ok {
		return d
	}
	return map[string]any{}
}

// normalize 照 usage.normalize：Chat Completions 用 prompt_tokens；
// Responses 的 input_tokens 已含快取；Anthropic 的快取另外列在 cache_*_input_tokens。
func normalize(u map[string]any) counts {
	var c counts
	cacheRead, cacheWrite := num(u["cache_read_input_tokens"]), num(u["cache_creation_input_tokens"])
	if _, ok := u["prompt_tokens"]; ok {
		c.Input = int64(num(u["prompt_tokens"]))
		c.Output = int64(num(u["completion_tokens"]))
		c.Cached = int64(num(sub(u, "prompt_tokens_details")["cached_tokens"]))
		if c.Cached == 0 {
			// DeepSeek（含 OpenCode Go 的 deepseek-*）：prompt_tokens = 命中 + 未命中。
			c.Cached = int64(num(u["prompt_cache_hit_tokens"]))
		}
		c.Reasoning = int64(num(sub(u, "completion_tokens_details")["reasoning_tokens"]))
	} else {
		c.Input = int64(num(u["input_tokens"]) + cacheRead + cacheWrite)
		c.Output = int64(num(u["output_tokens"]))
		c.Cached = int64(num(sub(u, "input_tokens_details")["cached_tokens"]))
		c.Reasoning = int64(num(sub(u, "output_tokens_details")["reasoning_tokens"]))
	}
	if c.Cached == 0 {
		c.Cached = int64(cacheRead)
	}
	c.CacheWrite = int64(cacheWrite)
	c.Cost = num(u["cost"])
	if t, _ := u["type"].(string); t == "duration" {
		c.Seconds = num(u["seconds"])
	}
	return c
}

// usageIn 找出一個回應或串流事件裡的 usage 區塊（本身、response.usage 或 message.usage）。
func usageIn(event any) map[string]any {
	m, ok := event.(map[string]any)
	if !ok {
		return nil
	}
	for _, holder := range []any{m, m["response"], m["message"]} {
		if h, ok := holder.(map[string]any); ok {
			if u, ok := h["usage"].(map[string]any); ok {
				return u
			}
		}
	}
	return nil
}

// meter 讀過整個回應，每個欄位留最大值：
// Chat Completions、Responses 在最後回報一次，Anthropic 回報累計值，取最大值兩者都對。
type meter struct {
	streaming bool
	buf       []byte
	size      int
	seen      counts
	found     bool
}

func newMeter(streaming bool) *meter { return &meter{streaming: streaming} }

func (m *meter) Write(p []byte) (int, error) {
	if m.streaming {
		m.buf = append(m.buf, p...)
		for {
			i := bytes.IndexByte(m.buf, '\n')
			if i < 0 {
				break
			}
			m.line(m.buf[:i])
			m.buf = m.buf[i+1:]
		}
		// response.completed 會在一行裡重複整個回應，可能好幾百 KB；只放棄離譜的長行。
		if len(m.buf) > limits.ModelUsageJSONBytes {
			m.buf = nil
		}
		return len(p), nil
	}
	if m.size <= limits.ModelUsageJSONBytes {
		m.size += len(p)
		if m.size <= limits.ModelUsageJSONBytes {
			m.buf = append(m.buf, p...)
		} else {
			m.buf = nil
		}
	}
	return len(p), nil
}

func (m *meter) line(line []byte) {
	line = bytes.TrimSpace(line)
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok || !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	m.take(data)
}

func (m *meter) take(raw []byte) {
	var event any
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	u := usageIn(event)
	if u == nil {
		return
	}
	c := normalize(u)
	m.found = true
	m.seen.Input = max(m.seen.Input, c.Input)
	m.seen.Output = max(m.seen.Output, c.Output)
	m.seen.Cached = max(m.seen.Cached, c.Cached)
	m.seen.CacheWrite = max(m.seen.CacheWrite, c.CacheWrite)
	m.seen.Reasoning = max(m.seen.Reasoning, c.Reasoning)
	m.seen.Cost = max(m.seen.Cost, c.Cost)
	m.seen.Seconds = max(m.seen.Seconds, c.Seconds)
}

// result 回傳讀到的用量；ok 為 false 表示整個回應都沒有 usage。
func (m *meter) result() (counts, bool) {
	if m.streaming {
		m.line(m.buf)
	} else if len(m.buf) > 0 {
		m.take(m.buf)
	}
	m.buf = nil
	return m.seen, m.found
}

// requestModel 讀出請求內容裡的 model 欄位；不是 JSON 就回空字串。
func requestModel(body []byte) string {
	var req struct {
		Model any `json:"model"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	s, _ := req.Model.(string)
	return s
}
