package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// EN-7：供應商回錯誤時，把狀態碼、標頭與內容的開頭交給 ProviderError（金鑰遮掉），回應照常轉給 Bot。
func TestProviderErrorProbe(t *testing.T) {
	type seen struct {
		bot, provider string
		status        int
		retry         string
		body          string
	}
	var mu sync.Mutex
	var got []seen
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"error":{"type":"insufficient_quota","message":"quota for `+realKey+`"}}`)
	}, func(o *Options) {
		o.ProviderError = func(botID, provider string, status int, h http.Header, body []byte) {
			mu.Lock()
			got = append(got, seen{botID, provider, status, h.Get("Retry-After"), string(body)})
			mu.Unlock()
		}
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	if body := readAll(t, resp); resp.StatusCode != 429 || !strings.Contains(body, "insufficient_quota") {
		t.Fatalf("回應要照常轉回：%d %s", resp.StatusCode, body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].bot != "bot-1" || got[0].provider != "openai" || got[0].status != 429 || got[0].retry != "120" {
		t.Fatalf("ProviderError：%+v", got)
	}
	if strings.Contains(got[0].body, realKey) || !strings.Contains(got[0].body, "insufficient_quota") {
		t.Fatalf("內容要遮掉金鑰：%q", got[0].body)
	}
	// 成功的回應不呼叫。
	f2 := newFixture(t, jsonUpstream(200, `{"choices":[]}`), func(o *Options) {
		o.ProviderError = func(string, string, int, http.Header, []byte) { t.Error("成功的回應不該呼叫 ProviderError") }
	})
	readAll(t, f2.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f2.botKey, chatBody))
}

func TestCapped(t *testing.T) {
	c := &capped{max: 5}
	for _, s := range []string{"abc", "defg", "hij"} {
		if n, err := c.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatal(n, err)
		}
	}
	if string(c.b) != "abcde" {
		t.Fatalf("%q", c.b)
	}
}
