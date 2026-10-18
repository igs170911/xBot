package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const realKey = "sk-REAL-upstream-key-0123456789"

// upstreamCall 記下假供應商收到的請求。
type upstreamCall struct {
	Method, Path, Query, Auth, APIKey, Body string
	Header                                  http.Header
}

type fixture struct {
	t        *testing.T
	mem      *Memory
	keys     *WorkerKeys
	srv      *httptest.Server // XBot（只掛 Model Proxy）
	upstream *httptest.Server
	logs     *syncBuffer
	mu       sync.Mutex
	calls    []upstreamCall
	botKey   string
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newFixture(t *testing.T, upstream http.HandlerFunc, opts ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{t: t, mem: NewMemory(), logs: &syncBuffer{}}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, upstreamCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), string(b), r.Header.Clone()})
		f.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(b))
		upstream(w, r)
	}))
	t.Cleanup(f.upstream.Close)

	ctx := context.Background()
	kr := testKeyring(t)
	providers := NewProviders(f.mem, kr)
	if err := providers.Put(ctx, Provider{ID: "openai", BaseURL: f.upstream.URL + "/v1", APIKey: realKey}); err != nil {
		t.Fatal(err)
	}
	f.keys = NewWorkerKeys(f.mem, kr)
	k, err := f.keys.Issue(ctx, "bot-1")
	if err != nil {
		t.Fatal(err)
	}
	f.botKey = string(k)
	o := Options{Keys: f.keys, Providers: providers, Usage: f.mem, Logger: slog.New(slog.NewTextHandler(f.logs, nil))}
	for _, fn := range opts {
		fn(&o)
	}
	mux := http.NewServeMux()
	mux.Handle(Prefix, NewHandler(o))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) do(method, path, auth, body string) *http.Response {
	f.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fixture) upstreamCalls() []upstreamCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamCall(nil), f.calls...)
}

func jsonUpstream(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "upstream=1")
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const chatBody = `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`

func TestForwardsWithRealKeyAndRecordsUsage(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{"id":"c1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`))
	resp := f.do("POST", "/model/openai/v1/chat/completions?x=1", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, `"id":"c1"`) {
		t.Fatalf("回應 %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") != "req-1" || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("回應標頭轉送不對：%v", resp.Header)
	}
	calls := f.upstreamCalls()
	if len(calls) != 1 {
		t.Fatalf("上游被呼叫 %d 次", len(calls))
	}
	c := calls[0]
	if c.Path != "/v1/chat/completions" || c.Query != "x=1" || c.Auth != "Bearer "+realKey || c.Body != chatBody {
		t.Fatalf("上游收到 %+v", c)
	}
	if strings.Contains(fmt.Sprint(c.Header), f.botKey) {
		t.Fatal("worker-key 被轉送給供應商")
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	e := f.mem.Usage()[0]
	if e.BotID != "bot-1" || e.Provider != "openai" || e.Model != "gpt-test" || e.Endpoint != "chat/completions" ||
		e.InputTokens != 12 || e.OutputTokens != 5 || e.CachedTokens != 4 || e.ReasoningTokens != 2 || !e.OK || e.Status != 200 || e.At.IsZero() {
		t.Fatalf("用量不對：%+v", e)
	}
}

func TestAuthRequired(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{}`))
	for _, auth := range []string{"", "Bearer ", "Bearer " + realKey, "Bearer xbw_bot-1.AAAA", "Basic " + f.botKey, f.botKey} {
		resp := f.do("POST", "/model/openai/v1/chat/completions", auth, chatBody)
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth=%q 應回 401，實際 %d %s", auth, resp.StatusCode, body)
		}
	}
	// 換掉 nonce 後舊 key 被拒。
	if _, err := f.keys.Rotate(context.Background(), "bot-1"); err != nil {
		t.Fatal(err)
	}
	if resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotate 後舊 key 應回 401，實際 %d", resp.StatusCode)
	}
	if n := len(f.upstreamCalls()); n != 0 {
		t.Fatalf("沒通過驗證卻打到上游 %d 次", n)
	}
	if n := len(f.mem.Usage()); n != 0 {
		t.Fatalf("沒通過驗證卻記了用量 %d 筆", n)
	}
}

func TestAnthropicStyleXAPIKey(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{"type":"message","usage":{"input_tokens":10,"output_tokens":3,"cache_read_input_tokens":6}}`))
	req, _ := http.NewRequest("POST", f.srv.URL+"/model/openai/v1/messages", strings.NewReader(`{"model":"claude-x"}`))
	req.Header.Set("X-Api-Key", f.botKey)
	req.Header.Set("Anthropic-Beta", "tools-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("狀態 %d", resp.StatusCode)
	}
	c := f.upstreamCalls()[0]
	if c.APIKey != realKey || c.Header.Get("Anthropic-Version") != "2023-06-01" || c.Header.Get("Anthropic-Beta") != "tools-1" {
		t.Fatalf("上游標頭 %v", c.Header)
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	e := f.mem.Usage()[0]
	if e.InputTokens != 16 || e.OutputTokens != 3 || e.CachedTokens != 6 || e.Model != "claude-x" {
		t.Fatalf("用量 %+v", e)
	}
}

func TestPathAllowlist(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{"data":[]}`))
	for _, p := range []string{"files", "fine_tuning/jobs", "chat/completions/extra", "batches", "responses/../files", "assistants", "audio/speech"} {
		resp := f.do("POST", "/model/openai/v1/"+p, "Bearer "+f.botKey, chatBody)
		_ = readAll(t, resp)
		// responses/../files 會被 ServeMux 清理成 /model/openai/v1/files 並轉址；轉址後一樣是 403。
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s 應回 403，實際 %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"models", "responses/resp_123", "responses/resp_123/cancel", "embeddings", "images/generations", "audio/transcriptions"} {
		method, body := "POST", chatBody
		if p == "models" {
			method, body = "GET", ""
		}
		resp := f.do(method, "/model/openai/v1/"+p, "Bearer "+f.botKey, body)
		_ = readAll(t, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("%s 應該通過，實際 %d", p, resp.StatusCode)
		}
	}
	for _, c := range f.upstreamCalls() {
		if strings.Contains(c.Path, "files") || strings.Contains(c.Path, "batches") {
			t.Fatalf("不允許的路徑打到上游：%s", c.Path)
		}
	}
	// models 不記用量；其餘 metered 路徑會記。
	for _, e := range f.mem.Usage() {
		if e.Endpoint == "models" {
			t.Fatal("models 不該記用量")
		}
	}
	if resp := f.do("DELETE", "/model/openai/v1/responses/r1", "Bearer "+f.botKey, ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE 應回 405，實際 %d", resp.StatusCode)
	}
	if resp := f.do("POST", "/model/other/v1/chat/completions", "Bearer "+f.botKey, chatBody); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("沒設定的供應商應回 404，實際 %d", resp.StatusCode)
	}
	if resp := f.do("POST", "/model/openai/v2/chat/completions", "Bearer "+f.botKey, chatBody); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("錯誤網址應回 404，實際 %d", resp.StatusCode)
	}
}

func TestStreamingPassthroughAndUsage(t *testing.T) {
	release := make(chan struct{})
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-release // 第一段送出後先停住：代理要在上游結束前就把它送到容器。
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, `{"model":"gpt-stream","stream":true}`)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	first := make(chan string, 1)
	go func() {
		line, _ := rd.ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if !strings.Contains(line, "Hel") {
			t.Fatalf("第一段 %q", line)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("串流沒有即時轉送（被緩衝住了）")
	}
	close(release)
	rest, _ := io.ReadAll(rd)
	if !strings.Contains(string(rest), "lo") || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("其餘內容 %q", rest)
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	e := f.mem.Usage()[0]
	if e.InputTokens != 7 || e.OutputTokens != 2 || e.Model != "gpt-stream" || !e.OK {
		t.Fatalf("串流用量 %+v", e)
	}
}

func TestAnthropicStreamUsageTakesMax(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":1}}}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":9}}")
		// 最後一行沒有換行結尾，也要讀得到。
	})
	resp := f.do("POST", "/model/openai/v1/messages", "Bearer "+f.botKey, `{"model":"m"}`)
	_ = readAll(t, resp)
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	if e := f.mem.Usage()[0]; e.InputTokens != 20 || e.OutputTokens != 9 {
		t.Fatalf("用量 %+v", e)
	}
}

func TestResponsesStreamUsage(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":30,\"output_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":10},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n")
	})
	_ = readAll(t, f.do("POST", "/model/openai/v1/responses", "Bearer "+f.botKey, `{"model":"r"}`))
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	if e := f.mem.Usage()[0]; e.InputTokens != 30 || e.OutputTokens != 4 || e.CachedTokens != 10 || e.ReasoningTokens != 3 {
		t.Fatalf("用量 %+v", e)
	}
}

func TestUpstreamErrorRecordedAndKeyRedacted(t *testing.T) {
	// 有些供應商會在錯誤訊息裡回顯金鑰；不能原樣還給容器。
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Openai-Echo", "key="+realKey)
		w.WriteHeader(401)
		// 分好幾段送，讓金鑰被切在兩次 Write 之間。
		msg := `{"error":{"message":"Incorrect API key provided: ` + realKey + `"}}`
		for i := 0; i < len(msg); i += 7 {
			_, _ = io.WriteString(w, msg[i:min(i+7, len(msg))])
			w.(http.Flusher).Flush()
		}
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if resp.StatusCode != 401 {
		t.Fatalf("上游狀態碼應原樣轉回，實際 %d", resp.StatusCode)
	}
	if strings.Contains(body, realKey) || strings.Contains(fmt.Sprint(resp.Header), realKey) {
		t.Fatalf("回應裡有真金鑰：%s %v", body, resp.Header)
	}
	if !strings.Contains(body, "Incorrect API key provided: "+Masked) {
		t.Fatalf("遮蔽後內容 %q", body)
	}
	waitFor(t, func() bool { return len(f.mem.Usage()) == 1 })
	if e := f.mem.Usage()[0]; e.OK || e.Status != 401 {
		t.Fatalf("失敗的請求也要記一筆：%+v", e)
	}
	if strings.Contains(f.logs.String(), realKey) {
		t.Fatal("紀錄裡有真金鑰")
	}
}

func TestUpstreamDownNoLeak(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{}`))
	f.upstream.Close()
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("狀態 %d", resp.StatusCode)
	}
	for _, s := range []string{body, f.logs.String()} {
		if strings.Contains(s, realKey) {
			t.Fatalf("外流真金鑰：%s", s)
		}
	}
	// 錯誤訊息不透露供應商的內部網址。
	if strings.Contains(body, f.upstream.URL) {
		t.Fatalf("錯誤訊息透露上游網址：%s", body)
	}
	var e struct {
		Error struct{ Message, Code string }
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error.Code != "upstream_unavailable" {
		t.Fatalf("錯誤格式 %s", body)
	}
}

func TestBodySizeLimit(t *testing.T) {
	f := newFixture(t, jsonUpstream(200, `{}`), func(o *Options) { o.MaxBodyBytes = 100 })
	big := `{"model":"m","x":"` + strings.Repeat("a", 200) + `"}`
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, big)
	_ = readAll(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("Content-Length 超過應回 413，實際 %d", resp.StatusCode)
	}
	// 沒有 Content-Length（chunked）也要擋。
	req, _ := http.NewRequest("POST", f.srv.URL+"/model/openai/v1/chat/completions", io.MultiReader(strings.NewReader(big)))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+f.botKey)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	_, _ = io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked 超過應回 413，實際 %d", resp2.StatusCode)
	}
	if n := len(f.upstreamCalls()); n != 0 {
		t.Fatalf("太大的請求不該送到上游（%d 次）", n)
	}
	small := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, `{"model":"m"}`)
	if small.StatusCode != 200 {
		t.Fatalf("小請求應通過，實際 %d", small.StatusCode)
	}
}

func TestNoRedirectFollow(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.invalid/steal", http.StatusTemporaryRedirect)
	})
	resp := f.do("POST", "/model/openai/v1/chat/completions", "Bearer "+f.botKey, chatBody)
	_ = readAll(t, resp)
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "" {
		t.Fatalf("轉址應原樣回傳狀態但不帶 Location，實際 %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestRedactWriterSplits(t *testing.T) {
	secret := "sk-abcdef"
	for split := 0; split <= len(secret)+4; split++ {
		var out bytes.Buffer
		rw := newRedactWriter(&out, secret)
		msg := "xx" + secret + "yy" + secret
		_, _ = rw.Write([]byte(msg[:split]))
		_, _ = rw.Write([]byte(msg[split:]))
		_ = rw.flush()
		if out.String() != "xx"+Masked+"yy"+Masked {
			t.Fatalf("split=%d 得到 %q", split, out.String())
		}
	}
	// 結尾剛好像金鑰開頭但不是金鑰，flush 後要原樣送出。
	var out bytes.Buffer
	rw := newRedactWriter(&out, secret)
	_, _ = rw.Write([]byte("data: sk-ab"))
	_ = rw.flush()
	if out.String() != "data: sk-ab" {
		t.Fatalf("得到 %q", out.String())
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("等太久")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
