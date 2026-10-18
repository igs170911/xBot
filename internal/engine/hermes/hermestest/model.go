package hermestest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// FakeModel 是說 OpenAI Chat Completions（含串流）的假模型，每次都回「OK」。
type FakeModel struct {
	// Tool 有設定時，請求裡有名字（點換成底線）包含 Tool.Match 的工具、而且最後一則不是工具結果，就先呼叫那個工具（C11 端對端用）。
	Tool *FakeToolCall

	mu    sync.Mutex
	calls []string
	auth  []string
	tools map[string]bool
	used  []string // 呼叫過的工具
}

// deferredRE 找出延遲目錄裡的工具名稱（「- 名稱: 說明」或「- 名稱」一行）。
var deferredRE = regexp.MustCompile(`(?m)^- ([A-Za-z0-9_.\-]+)(?::|$)`)

// FakeToolCall 是假模型要呼叫的工具。
type FakeToolCall struct {
	Match string // 工具名稱包含這段文字
	Args  string // JSON 參數
}

// ToolNames 回傳請求裡出現過的所有工具名稱（Hermes 交給模型的工具）。
func (f *FakeModel) ToolNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.tools))
	for n := range f.tools {
		out = append(out, n)
	}
	return out
}

// ToolCalls 回傳假模型呼叫過的工具名稱。
func (f *FakeModel) ToolCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.used...)
}

// ServeHTTP 實作 http.Handler。
func (f *FakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"fake-model","object":"model","owned_by":"xbot"}]}`)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		var req struct {
			Stream bool `json:"stream"`
			Tools  []struct {
				Function struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		usage := `{"prompt_tokens":20,"completion_tokens":1,"total_tokens":21}`
		call, args := "", ""
		f.mu.Lock()
		if f.tools == nil {
			f.tools = map[string]bool{}
		}
		lastIsTool := len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role == "tool"
		match := func(name string) bool {
			return f.Tool != nil && strings.Contains(strings.ReplaceAll(name, ".", "_"), f.Tool.Match)
		}
		bridge := false
		for _, t := range req.Tools {
			f.tools[t.Function.Name] = true
			bridge = bridge || t.Function.Name == "tool_call"
			if call == "" && !lastIsTool && match(t.Function.Name) {
				call = t.Function.Name
				if f.Tool != nil {
					args = f.Tool.Args
				}
			}
		}
		// Hermes 把 MCP 工具放在延遲目錄裡（tool_search 的說明列出名字），用 tool_call 呼叫。
		for _, t := range req.Tools {
			if t.Function.Name != "tool_search" {
				continue
			}
			for _, name := range deferredRE.FindAllStringSubmatch(t.Function.Description, -1) {
				f.tools[name[1]] = true
				if call == "" && bridge && !lastIsTool && match(name[1]) {
					call = "tool_call"
					args = fmt.Sprintf(`{"calls":[{"name":%q,"arguments":%s}]}`, name[1], f.Tool.Args)
					f.used = append(f.used, name[1])
				}
			}
		}
		if call != "" && call != "tool_call" {
			f.used = append(f.used, call)
		}
		f.mu.Unlock()
		if call != "" {
			args, _ := json.Marshal(args)
			tc := fmt.Sprintf(`[{"index":0,"id":"call_1","type":"function","function":{"name":%q,"arguments":%s}}]`, call, args)
			if !req.Stream {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"fake-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":%s},"finish_reason":"tool_calls"}],"usage":%s}`, tc, usage)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":`+tc+`},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":`+usage+`}`)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"fake-model","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":%s}`, usage)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			w.(http.Flusher).Flush()
		}
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{"content":"O"},"finish_reason":null}]}`)
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{"content":"K"},"finish_reason":null}]}`)
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","choices":[],"usage":` + usage + `}`)
		chunk("[DONE]")
	default:
		http.NotFound(w, r)
	}
}

// ChatCalls 回傳收到幾次 chat/completions。
func (f *FakeModel) ChatCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c, "/chat/completions") {
			n++
		}
	}
	return n
}

// Auth 回傳每個請求帶的 Authorization 標頭（檢查供應商收到的是真金鑰）。
func (f *FakeModel) Auth() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}
