package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// X05：服務內部（快判）經 Model Proxy 呼叫模型：真金鑰、XBot 的 User-Agent、OpenCode 的對話標頭、用量標記 judge。
func TestInternalCall(t *testing.T) {
	var mu sync.Mutex
	var got *http.Request
	var gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got, gotBody = r.Clone(context.Background()), string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13","choices":[{"message":{"content":"{\"label\":\"done\",\"p\":0.9,\"why\":\"ok `+realKey+`\"}"}}],"usage":{"prompt_tokens":1000000,"completion_tokens":5}}`)
	}))
	defer up.Close()
	mem := NewMemory()
	kr := testKeyring(t)
	providers := NewProviders(mem, kr)
	if err := providers.Put(context.Background(), Provider{ID: "opencode-zen", BaseURL: up.URL + "/zen/v1", Preset: "opencode-zen", APIKey: realKey}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(Options{Keys: NewWorkerKeys(mem, kr), Providers: providers, Usage: mem, Pricing: &Pricing{}})
	resp, err := h.Call(t.Context(), CallRequest{Provider: "opencode-zen", Path: "chat/completions", Engine: "judge", Session: "xbot-judge-task_done",
		Body: []byte(`{"model":"jev-1.13","messages":[]}`)})
	if err != nil || resp.Status != 200 {
		t.Fatalf("%v %d", err, resp.Status)
	}
	if strings.Contains(string(resp.Body), realKey) {
		t.Fatal("回應裡的金鑰要遮掉")
	}
	mu.Lock()
	defer mu.Unlock()
	if got.URL.Path != "/zen/v1/chat/completions" || got.Header.Get("Authorization") != "Bearer "+realKey ||
		!strings.HasPrefix(got.Header.Get("User-Agent"), "XBot/") || got.Header.Get("X-Opencode-Session") != "xbot-judge-task_done" ||
		!strings.Contains(gotBody, "jev-1.13") {
		t.Fatalf("請求：%s %v %s", got.URL.Path, got.Header, gotBody)
	}
	u := mem.Usage()
	// Jev 經 Zen：輸入 $0.042／百萬、輸出免費 → 100 萬 token = 42000 micros。
	if len(u) != 1 || u[0].Engine != "judge" || u[0].BotID != "" || u[0].Model != "jev-1.13" || u[0].CostMicros != 42000 {
		t.Fatalf("用量：%+v", u)
	}

	if _, err := h.Call(t.Context(), CallRequest{Provider: "nope", Path: "chat/completions", Body: []byte(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有設定的供應商：%v", err)
	}
	if _, err := h.Call(t.Context(), CallRequest{Provider: "opencode-zen", Path: "models"}); !errors.Is(err, ErrCallPath) {
		t.Fatalf("只開放模型請求：%v", err)
	}
}
