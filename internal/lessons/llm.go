package lessons

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"xbot/internal/proxy"
)

// Model 是老師用的模型（經 Model Proxy；金鑰只在 Model Proxy 裡）。
type Model struct {
	Provider, Model string
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string
}

func (m Model) String() string { return m.Provider + "/" + m.Model }

// Caller 是 XBot 服務自己呼叫模型（proxy.Handler.Call）。
type Caller func(ctx context.Context, req proxy.CallRequest) (proxy.CallResponse, error)

// UsageEngine 是老師的用量在帳本上的標記。
const UsageEngine = "lessons"

// complete 請模型回一段文字：照呼叫方式組 chat/completions、Responses（ChatGPT 方案）或 Messages 的請求，讀出回覆的文字。
func complete(ctx context.Context, call Caller, m Model, system, user string, maxOut int) (string, error) {
	var path string
	var body map[string]any
	switch m.Transport {
	case proxy.TransportResponses:
		path = "responses"
		body = map[string]any{"model": m.Model, "instructions": system, "stream": true, "store": false,
			"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": user}}}}}
	case proxy.TransportMessages:
		path = "messages"
		body = map[string]any{"model": m.Model, "system": system, "max_tokens": maxOut,
			"messages": []any{map[string]any{"role": "user", "content": user}}}
	default:
		path = "chat/completions"
		body = map[string]any{"model": m.Model, "max_tokens": maxOut,
			"messages": []any{map[string]any{"role": "system", "content": system}, map[string]any{"role": "user", "content": user}}}
	}
	raw, _ := json.Marshal(body)
	resp, err := call(ctx, proxy.CallRequest{Provider: m.Provider, Path: path, Body: raw, Engine: UsageEngine, MaxBytes: 4 << 20})
	if err != nil {
		return "", err
	}
	if resp.Status >= 300 {
		return "", fmt.Errorf("老師模型回 %d：%s", resp.Status, clip(string(resp.Body), 200))
	}
	text := replyText(resp.Body)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("老師模型沒有回覆文字")
	}
	return text, nil
}

// replyText 從三種回應讀出文字：chat/completions、Messages、Responses（JSON 或 SSE 串流）。
func replyText(body []byte) string {
	var v struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Output []respOutput `json:"output"`
	}
	if json.Unmarshal(body, &v) == nil {
		if len(v.Choices) > 0 {
			return v.Choices[0].Message.Content
		}
		var b strings.Builder
		for _, c := range v.Content {
			if c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
		if t := outputText(v.Output); t != "" {
			return t
		}
	}
	// Responses 的 SSE：累加 output_text.delta；沒有 delta 時用 response.completed 的完整回應。
	var deltas, done strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(bytes.TrimSpace(sc.Bytes()), []byte("data:"))
		if !ok {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				Output []respOutput `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal(bytes.TrimSpace(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			deltas.WriteString(ev.Delta)
		case "response.completed":
			done.WriteString(outputText(ev.Response.Output))
		}
	}
	if deltas.Len() > 0 {
		return deltas.String()
	}
	return done.String()
}

type respOutput struct {
	Type    string `json:"type"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func outputText(out []respOutput) string {
	var b strings.Builder
	for _, o := range out {
		for _, c := range o.Content {
			if c.Type == "output_text" {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
