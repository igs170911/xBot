package ipc

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/proxy"
)

// C34：連接的應用程式經 /internal/apps/<名字> 轉送；要這個 Bot 的 worker-key，處理器拿到的就是這個 Bot。
func TestAppsRouteNeedsOwnWorkerKey(t *testing.T) {
	ctx := context.Background()
	mem := proxy.NewMemory()
	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{4}, 32))
	keys := proxy.NewWorkerKeys(mem, kr)
	k1, _ := keys.Issue(ctx, "bot1")
	k2, _ := keys.Issue(ctx, "bot2")
	root := t.TempDir()
	s := New(Options{Dir: func(id string) string { return filepath.Join(root, id, "ipc") }, Keys: keys})
	defer s.Shutdown()
	if err := s.Ensure("bot1"); err != nil {
		t.Fatal(err)
	}
	c := unixClient(s.Path("bot1"))
	post := func(key string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, "http://product"+AppsPrefix+"notes", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := post(string(k1)); code != http.StatusServiceUnavailable {
		t.Fatalf("還沒接上時：%d", code)
	}
	s.SetApps(func(botID string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, botID+" "+r.URL.Path) })
	})
	if code, _ := post(string(k2)); code != http.StatusUnauthorized {
		t.Fatalf("別的 Bot 的 key：%d", code)
	}
	if code, body := post(string(k1)); code != 200 || body != "bot1 "+AppsPrefix+"notes" {
		t.Fatalf("自己的 key：%d %s", code, body)
	}
}
