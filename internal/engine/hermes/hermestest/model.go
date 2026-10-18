package hermestest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// FakeModel 是說 OpenAI Chat Completions（含串流）的假模型，每次都回「OK」。
type FakeModel struct {
	mu    sync.Mutex
	calls []string
	auth  []string
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
		}
		_ = json.Unmarshal(body, &req)
		usage := `{"prompt_tokens":20,"completion_tokens":1,"total_tokens":21}`
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
