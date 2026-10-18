package apps

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeRemote 是一台假的「遠端 MCP＋授權伺服器」：RFC 9728 resource metadata、RFC 8414、動態註冊、PKCE 權杖端點，
// 以及要 Bearer 權杖、有 session 的 Streamable HTTP MCP。
type fakeRemote struct {
	srv *httptest.Server
	mu  sync.Mutex
	// 授權
	noDCR      bool
	challenge  string // 測試從登入網址抄過來
	token      string // 現在有效的存取權杖
	refreshTok string
	issued     int
	tokenCalls []map[string]string
	registered int
	// MCP
	sessions  map[string]bool
	nextSess  int
	calls     []string // tools/call 的工具名稱
	lastAuth  string
	apiHeader string // 有設定時改用這個標頭驗證（API 金鑰）
	apiKey    string
}

func newFakeRemote(t *testing.T) *fakeRemote {
	f := &fakeRemote{sessions: map[string]bool{}, refreshTok: "r1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"resource": f.srv.URL + "/mcp", "authorization_servers": []string{f.srv.URL}, "scopes_supported": []string{"read", "write"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]any{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"},
			"token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"read", "write", "offline_access"}}
		if !f.noDCR {
			m["registration_endpoint"] = f.srv.URL + "/register"
		}
		writeJSON(w, 200, m)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var meta map[string]any
		_ = json.NewDecoder(r.Body).Decode(&meta)
		f.mu.Lock()
		f.registered++
		f.mu.Unlock()
		writeJSON(w, 201, map[string]any{"client_id": "cid", "redirect_uris": meta["redirect_uris"], "token_endpoint_auth_method": "none"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		f.tokenCalls = append(f.tokenCalls, form)
		if form["resource"] != f.srv.URL+"/mcp" || form["client_id"] == "" {
			writeJSON(w, 400, map[string]any{"error": "invalid_request", "error_description": "resource 或 client_id 不對"})
			return
		}
		switch form["grant_type"] {
		case "authorization_code":
			sum := sha256.Sum256([]byte(form["code_verifier"]))
			if form["code"] != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
		case "refresh_token":
			if form["refresh_token"] != f.refreshTok {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
		default:
			writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
			return
		}
		f.issued++
		f.token = fmt.Sprintf("tok%d", f.issued)
		writeJSON(w, 200, map[string]any{"access_token": f.token, "token_type": "Bearer", "expires_in": 3600, "refresh_token": f.refreshTok,
			"workspace_name": "陳家選物"})
	})
	mux.HandleFunc("/mcp", f.mcp)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeRemote) mcp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAuth = r.Header.Get("Authorization")
	if f.apiHeader != "" {
		if r.Header.Get(f.apiHeader) != f.apiKey {
			w.WriteHeader(401)
			return
		}
	} else if f.token == "" || r.Header.Get("Authorization") != "Bearer "+f.token {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp", scope="read write"`)
		w.WriteHeader(401)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &msg)
	if msg.Method == "initialize" {
		f.nextSess++
		id := fmt.Sprintf("s%d", f.nextSess)
		f.sessions[id] = true
		w.Header().Set("Mcp-Session-Id", id)
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake", "version": "1"}}})
		return
	}
	if !f.sessions[r.Header.Get("Mcp-Session-Id")] {
		w.WriteHeader(404)
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	switch msg.Method {
	case "tools/list":
		// 用 SSE 回，確認兩種格式都讀得懂。
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []map[string]any{
			{"name": "search", "description": "找頁面", "inputSchema": map[string]any{"type": "object"}},
			{"name": "delete_page", "description": "刪頁面", "inputSchema": map[string]any{"type": "object"}},
		}}})
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	case "tools/call":
		f.calls = append(f.calls, msg.Params.Name)
		text := "找到 3 頁"
		if msg.Params.Name == "long" {
			text = strings.Repeat("字", 30000)
		}
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
	default:
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "no such method"}})
	}
}

// dropSessions 模擬遠端重開：所有 session 失效。
func (f *fakeRemote) dropSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]bool{}
}

// rotate 讓目前的存取權杖失效（要換新才能用）。
func (f *fakeRemote) rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = "revoked-" + f.token
}

// fakeBrowser 是假的 Bot 瀏覽器（每個 Bot 一罐 cookies）。
type fakeBrowser struct {
	mu   sync.Mutex
	jars map[string][]Cookie
}

func (b *fakeBrowser) Cookies(_ context.Context, botID string) ([]Cookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Cookie{}
	for _, c := range b.jars[botID] {
		cp := Cookie{}
		for k, v := range c {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out, nil
}

func (b *fakeBrowser) AddCookies(_ context.Context, botID string, cs []Cookie) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jars[botID] = append(b.jars[botID], cs...)
	return len(cs), nil
}

func (b *fakeBrowser) ClearSites(_ context.Context, botID string, sites []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var keep []Cookie
	for _, c := range b.jars[botID] {
		if !Belongs(c, sites) {
			keep = append(keep, c)
		}
	}
	b.jars[botID] = keep
	return nil
}
