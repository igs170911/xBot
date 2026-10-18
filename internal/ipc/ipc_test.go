package ipc

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/proxy"
)

const realKey = "sk-REAL-ipc-key"

func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

func TestProductSocketRelaysModelRequests(t *testing.T) {
	ctx := context.Background()
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path+"?"+r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	mem := proxy.NewMemory()
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{3}, 32))
	providers := proxy.NewProviders(mem, kr)
	if err := providers.Put(ctx, proxy.Provider{ID: "openai", BaseURL: upstream.URL + "/v1", APIKey: realKey}); err != nil {
		t.Fatal(err)
	}
	keys := proxy.NewWorkerKeys(mem, kr)
	k1, _ := keys.Issue(ctx, "bot1")
	k2, _ := keys.Issue(ctx, "bot2")

	root := t.TempDir()
	s := New(Options{
		Dir:   func(id string) string { return filepath.Join(root, id, "ipc") },
		Model: proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: mem}),
		Keys:  keys,
	})
	defer s.Shutdown()
	// 上次留下的 socket 檔要被換掉。
	if err := os.MkdirAll(filepath.Join(root, "bot1", "ipc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path("bot1"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure("bot1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure("bot1"); err != nil {
		t.Fatalf("重複 Ensure：%v", err)
	}
	info, err := os.Stat(s.Path("bot1"))
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o666 {
		t.Fatalf("socket：%v %v", info.Mode(), err)
	}
	if d, _ := os.Stat(filepath.Dir(s.Path("bot1"))); d.Mode().Perm() != 0o700 {
		// 已經存在的資料夾不改權限（由 hosts.PrepareIPC 決定），這裡只確認沒被放寬。
		t.Fatalf("資料夾權限：%v", d.Mode())
	}

	c := unixClient(s.Path("bot1"))
	post := func(path, key string) (int, string) {
		req, _ := http.NewRequest("POST", "http://product"+path, strings.NewReader(`{"model":"m","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := post("/internal/providers/openai/v1/chat/completions?a=1", string(k1)); code != 200 || strings.Contains(body, realKey) {
		t.Fatalf("轉送：%d %s", code, body)
	}
	if gotPath != "/v1/chat/completions?a=1" || gotAuth != "Bearer "+realKey {
		t.Fatalf("上游收到：%s %s", gotPath, gotAuth)
	}
	// 別的 Bot 的 key 不能從這個 socket 進來。
	if code, _ := post("/internal/providers/openai/v1/chat/completions", string(k2)); code != 401 {
		t.Fatalf("別人的 key：%d", code)
	}
	if code, _ := post("/internal/providers/openai/v1/chat/completions", "nope"); code != 401 {
		t.Fatalf("錯的 key：%d", code)
	}
	// XBot MCP（C11）還沒接上時回 503；接上後 /internal/mcp 與 /internal/product 都交給這個 socket 的 Bot 的處理器。
	if code, _ := post("/internal/product", string(k1)); code != 503 {
		t.Fatalf("/internal/product：%d", code)
	}
	s.SetProduct(func(botID string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, botID+" "+r.URL.Path) })
	})
	for _, p := range []string{MCPPath, ProductPath} {
		if code, body := post(p, string(k1)); code != 200 || body != "bot1 "+p {
			t.Fatalf("%s：%d %s", p, code, body)
		}
	}
	if code, _ := post("/internal/providers/openai/v1/files", string(k1)); code != 403 {
		t.Fatalf("不開放的路徑：%d", code)
	}

	// 檔案被刪掉後 Ensure 會重新開。
	if err := os.Remove(s.Path("bot1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure("bot1"); err != nil {
		t.Fatal(err)
	}
	if code, _ := post("/internal/providers/openai/v1/models", string(k1)); code != 200 {
		t.Fatalf("重開後：%d", code)
	}
	if err := s.Close("bot1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path("bot1")); !os.IsNotExist(err) {
		t.Fatalf("Close 後 socket 應該刪掉：%v", err)
	}
}
