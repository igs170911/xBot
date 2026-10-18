package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/auth"
	"xbot/internal/limits"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// echo 是測試用的「整個服務」：回報它看到的請求長什麼樣子。
type seen struct {
	Path     string `json:"path"`
	Local    bool   `json:"local"`
	Tailnet  bool   `json:"tailnet"`
	User     string `json:"user"`
	External bool   `json:"external"`
	Remote   string `json:"remote"`
	Cookie   string `json:"cookie"`
}

var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(seen{Path: r.URL.Path, Local: auth.IsLocal(r), Tailnet: auth.IsTailnet(r.Context()), User: auth.TailnetUser(r.Context()),
		External: auth.IsExternal(r.Context()), Remote: r.RemoteAddr, Cookie: r.Header.Get("Cookie")})
})

func TestAllowedPath(t *testing.T) {
	cases := map[string]bool{
		"/mcp":                 true,
		"/api/v1/agents.pair":  true,
		"/hooks/habc123":       true,
		"/":                    false,
		"/mcp/":                false,
		"/mcpx":                false,
		"/hooks/":              false,
		"/hooks/a/b":           false,
		"/hooks/../api/v1/ops": false,
		"/hooks//x":            false,
		"/api/v1/ops":          false,
		"/api/v1/bots.list":    false,
		"/api/v1/auth/login":   false,
		"/api/v1/events":       false,
		"/model/v1/chat":       false,
		"/index.html":          false,
		"/healthz":             false,
	}
	for p, want := range cases {
		r := &http.Request{URL: mustURL(p)}
		if got := AllowedPath(r); got != want {
			t.Errorf("%s：%v，應該是 %v", p, got, want)
		}
	}
	// 跳脫過的路徑（%2F 等）一律不收。
	r := httptest.NewRequest("GET", "/hooks/a%2Fb", nil)
	if AllowedPath(r) {
		t.Error("跳脫的路徑要擋")
	}
}

func mustURL(p string) *url.URL { u, _ := url.Parse(p); return u }

func gateReq(method, p, ip string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, "https://xbot.tail.ts.net"+p, strings.NewReader("{}"))
	r.RemoteAddr = "100.100.1.1:41000" // Funnel 轉送節點
	if ip != "" {
		r = r.WithContext(WithClient(r.Context(), netip.MustParseAddr(ip)))
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestGatePathsAndAuth(t *testing.T) {
	g := &Gate{Next: echo, AccessToken: "owner-secret", Limiter: &IPLimiter{}}
	do := func(r *http.Request) (*httptest.ResponseRecorder, seen) {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		var s seen
		_ = json.Unmarshal(w.Body.Bytes(), &s)
		return w, s
	}
	// 其他路徑 404（網頁、操作 API、登入、SSE、模型代理）。
	for _, p := range []string{"/", "/api/v1/ops", "/api/v1/bots.list", "/api/v1/auth/login", "/api/v1/events", "/model/x", "/hooks/../api/v1/ops"} {
		if w, _ := do(gateReq("POST", p, "203.0.113.7", nil)); w.Code != 404 {
			t.Errorf("%s 要 404：%d", p, w.Code)
		}
	}
	// /mcp 沒有權杖 → 401；登入碼 → 403；cookie 不算。
	if w, _ := do(gateReq("POST", "/mcp", "203.0.113.7", map[string]string{"Cookie": "xbot_session=x"})); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("/mcp 沒權杖要 401：%d", w.Code)
	}
	if w, _ := do(gateReq("POST", "/mcp", "203.0.113.7", map[string]string{"Authorization": "Bearer owner-secret"})); w.Code != 403 {
		t.Errorf("外部入口不收登入碼：%d", w.Code)
	}
	w, s := do(gateReq("POST", "/mcp", "203.0.113.7", map[string]string{"Authorization": "Bearer xbot_agent", "Cookie": "xbot_session=x"}))
	if w.Code != 200 || !s.External || s.Local || s.Cookie != "" || !strings.HasPrefix(s.Remote, "203.0.113.7:") {
		t.Fatalf("代理權杖放行、標成外部、拿掉 cookie、來源換成真正的 IP：%d %+v", w.Code, s)
	}
	// 配對與網址觸發不用權杖（它們自己驗證）。
	for _, p := range []string{"/api/v1/agents.pair", "/hooks/habc"} {
		if w, s := do(gateReq("POST", p, "203.0.113.7", nil)); w.Code != 200 || !s.External {
			t.Errorf("%s 要放行：%d %+v", p, w.Code, s)
		}
	}
}

func TestGateAllowList(t *testing.T) {
	allow, _, err := ParseAllowIPs([]string{"203.0.113.7", "198.51.100.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gate{Next: echo, AllowIPs: func() []netip.Prefix { return allow }}
	for ip, want := range map[string]int{"203.0.113.7": 200, "198.51.100.42": 200, "203.0.113.8": 403, "2001:db8::1": 403} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, gateReq("POST", "/hooks/h1", ip, nil))
		if w.Code != want {
			t.Errorf("%s：%d，應該是 %d", ip, w.Code, want)
		}
	}
	if _, _, err := ParseAllowIPs([]string{"not-an-ip"}); err == nil {
		t.Error("看不懂的 IP 要報錯")
	}
	many := make([]string, limits.AllowIPsMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.%d.1", i)
	}
	if _, _, err := ParseAllowIPs(many); err == nil {
		t.Error("白名單太多筆要報錯")
	}
}

func TestIPLimiterBuckets(t *testing.T) {
	now := time.Now()
	l := &IPLimiter{Rate: 3, Now: func() time.Time { return now }}
	g := &Gate{Next: echo, Limiter: l}
	code := func(ip string) int {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, gateReq("POST", "/api/v1/agents.pair", ip, nil))
		return w.Code
	}
	for range 3 {
		if c := code("203.0.113.7"); c != 200 {
			t.Fatalf("前 3 次放行：%d", c)
		}
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, gateReq("POST", "/api/v1/agents.pair", "203.0.113.7", nil))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("第 4 次 429：%d", w.Code)
	}
	if c := code("203.0.113.8"); c != 200 {
		t.Fatalf("別的 IP 是另一個計數：%d", c)
	}
	now = now.Add(61 * time.Second)
	if c := code("203.0.113.7"); c != 200 {
		t.Fatalf("一分鐘後恢復：%d", c)
	}
	// 路徑不對的請求不算進計數（404 先回）。
	l2 := &IPLimiter{Rate: 1, Now: func() time.Time { return now }}
	g2 := &Gate{Next: echo, Limiter: l2}
	for range 3 {
		g2.ServeHTTP(httptest.NewRecorder(), gateReq("GET", "/", "203.0.113.9", nil))
	}
	w = httptest.NewRecorder()
	g2.ServeHTTP(w, gateReq("POST", "/hooks/h", "203.0.113.9", nil))
	if w.Code != 200 {
		t.Fatalf("404 的請求不佔名額：%d", w.Code)
	}
}

func TestFrontReverseProxy(t *testing.T) {
	// 沒有 XBOT_PUBLIC_URL：原樣不動。
	off := New(Options{})
	if h := off.Front(echo); fmt.Sprintf("%p", h) != fmt.Sprintf("%p", echo) {
		t.Fatal("沒設對外網址時不包")
	}
	s := New(Options{PublicURL: "https://xbot.example.com", AccessToken: "owner-secret"})
	h := s.Front(echo)
	do := func(host, remote string, hdr map[string]string, p string) (int, seen) {
		r := httptest.NewRequest("POST", "http://"+host+p, nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var sn seen
		_ = json.Unmarshal(w.Body.Bytes(), &sn)
		return w.Code, sn
	}
	// 本機直接連：照常（本機的你）。
	if c, sn := do("localhost:8780", "127.0.0.1:5000", nil, "/api/v1/ops"); c != 200 || !sn.Local || sn.External {
		t.Fatalf("本機直接連照常：%d %+v", c, sn)
	}
	// 反向代理（本機、帶 X-Forwarded-For）：只開放三條路徑，來源用 X-Forwarded-For 的最後一個。
	fwd := map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.7"}
	if c, _ := do("xbot.example.com", "127.0.0.1:5000", fwd, "/api/v1/ops"); c != 404 {
		t.Fatalf("反向代理進來的操作 API 要 404：%d", c)
	}
	if c, _ := do("localhost", "127.0.0.1:5000", fwd, "/"); c != 404 {
		t.Fatalf("帶轉送標頭、Host 是 localhost 也算反向代理：%d", c)
	}
	c, sn := do("xbot.example.com", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.7", "Authorization": "Bearer xbot_a"}, "/mcp")
	if c != 200 || !sn.External || !strings.HasPrefix(sn.Remote, "203.0.113.7:") {
		t.Fatalf("反向代理的 /mcp：%d %+v", c, sn)
	}
	// Host 是對外網址、但不是從信任的代理來：X-Forwarded-For 不採信。
	c, sn = do("xbot.example.com", "192.0.2.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "/hooks/h1")
	if c != 200 || !strings.HasPrefix(sn.Remote, "192.0.2.5:") {
		t.Fatalf("不信任的來源不採信轉送標頭：%d %+v", c, sn)
	}
}

func TestCheckPublicURL(t *testing.T) {
	for v, ok := range map[string]bool{"": true, "https://xbot.example.com": true, "https://xbot.example.com/": true,
		"http://xbot.example.com": false, "https://x.com/sub": false, "xbot.example.com": false} {
		if _, err := CheckPublicURL(v); (err == nil) != ok {
			t.Errorf("%q：%v", v, err)
		}
	}
	if p, err := ParseTrustedProxies("10.0.0.1, 192.168.0.0/16"); err != nil || len(p) != 2 {
		t.Fatalf("%v %v", p, err)
	}
}

// ── 假的 Tailscale 節點 ─────────────────────────────

type fakeNode struct {
	mu          sync.Mutex
	cfg         NodeConfig
	st          NodeStatus
	funnelCalls int
	funnelErr   error
	closed      bool
	tailnet     net.Listener
	funnel      net.Listener
}

func (f *fakeNode) Start() error { return nil }
func (f *fakeNode) Status(context.Context) (NodeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, nil
}
func (f *fakeNode) set(fn func(*NodeStatus)) { f.mu.Lock(); fn(&f.st); f.mu.Unlock() }
func (f *fakeNode) ListenTLS(string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	f.mu.Lock()
	f.tailnet = ln
	f.mu.Unlock()
	return ln, err
}
func (f *fakeNode) ListenFunnel(string) (net.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funnelCalls++
	if f.funnelErr != nil {
		return nil, f.funnelErr
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	f.funnel = ln
	return ln, err
}
func (f *fakeNode) WhoIs(context.Context, string) (string, error) { return "me@example.com", nil }
func (f *fakeNode) Cert(context.Context, string) error            { return nil }
func (f *fakeNode) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeNode) addr(funnel bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ln := f.tailnet
	if funnel {
		ln = f.funnel
	}
	if ln == nil {
		return ""
	}
	return ln.Addr().String()
}

func newSvc(t *testing.T) (*Service, *[]*fakeNode, *store.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	nodes := &[]*fakeNode{}
	var mu sync.Mutex
	s := New(Options{DB: db, Sealer: kr, DataDir: dir, LocalURL: "http://localhost:8780", AccessToken: "owner-secret", Poll: 10 * time.Millisecond,
		NewNode: func(c NodeConfig) Node {
			mu.Lock()
			defer mu.Unlock()
			n := &fakeNode{cfg: c, st: NodeStatus{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/abc"}}
			*nodes = append(*nodes, n)
			return n
		}})
	if err := s.Start(context.Background(), echo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, nodes, db
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for range 300 {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等不到：%s", what)
}

func TestServiceLifecycle(t *testing.T) {
	s, nodes, db := newSvc(t)
	ctx := context.Background()
	if st := s.Status(true); st.Tailscale.Enabled || st.Tailscale.State != StateOff || len(*nodes) != 0 {
		t.Fatalf("預設關閉：%+v", st.Tailscale)
	}
	// 開啟（帶登入金鑰）：金鑰加密存，節點拿到明文。
	if err := s.SetTailscale(ctx, TailscaleChange{Enabled: true, AuthKey: "tskey-auth-xyz"}); err != nil {
		t.Fatal(err)
	}
	raw, err := db.GetSetting(ctx, AuthKeySetting)
	if err != nil || strings.Contains(string(raw), "tskey") {
		t.Fatalf("登入金鑰要加密存：%s %v", raw, err)
	}
	n := (*nodes)[0]
	if n.cfg.AuthKey != "tskey-auth-xyz" || n.cfg.Hostname != "xbot" || !strings.HasSuffix(n.cfg.Dir, StateDir) {
		t.Fatalf("節點設定：%+v", n.cfg)
	}
	eventually(t, "needs_login", func() bool { return s.Status(true).Tailscale.State == StateNeedsLogin })
	if st := s.Status(true); st.Tailscale.LoginURL == "" || !st.Tailscale.HasAuthKey {
		t.Fatalf("要給登入網址：%+v", st.Tailscale)
	}
	if s.Status(false).Tailscale.LoginURL != "" {
		t.Fatal("沒有 admin 不給登入網址")
	}
	// 連上：tailnet 網址、QR、憑證、裝置數。
	n.set(func(st *NodeStatus) {
		*st = NodeStatus{BackendState: "Running", DNSName: "xbot.tail3a9e.ts.net", Tailnet: "me@example.com", MagicDNS: true,
			CertDomains: []string{"xbot.tail3a9e.ts.net"}, IPs: []string{"100.64.0.5"}, User: "me@example.com", Devices: 3, Online: 2, Funnel: true}
	})
	eventually(t, "running", func() bool {
		st := s.Status(true)
		return st.Tailscale.State == StateRunning && st.Tailscale.Cert == CertReady
	})
	st := s.Status(true)
	if st.Tailscale.URL != "https://xbot.tail3a9e.ts.net" || len(st.Tailscale.QR) < 21 || st.Tailscale.Devices != 3 || st.BaseURL != st.Tailscale.URL || st.PublicURL != "" {
		t.Fatalf("連上後的狀態：%+v", st)
	}
	// tailnet 入口：同一個處理器，但不算本機、標出 Tailscale 使用者。
	eventually(t, "tailnet listener", func() bool { return n.addr(false) != "" })
	var sn seen
	getJSON(t, "http://"+n.addr(false)+"/api/v1/ops", &sn)
	if sn.Local || !sn.Tailnet || sn.User != "me@example.com" || sn.External {
		t.Fatalf("tailnet 來的不能算本機：%+v", sn)
	}
	// Funnel：開了才聽，只開放三條路徑。
	if err := s.SetFunnel(ctx, true, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "funnel on", func() bool { return s.Status(true).Funnel.State == FunnelOn })
	if st := s.Status(true); st.Funnel.URL != "https://xbot.tail3a9e.ts.net" || st.PublicURL != st.Funnel.URL || len(st.Funnel.AllowIPs) != 1 {
		t.Fatalf("funnel 狀態：%+v", st.Funnel)
	}
	fa := n.addr(true)
	if code := getCode(t, "http://"+fa+"/api/v1/ops"); code != 404 {
		t.Fatalf("Funnel 上的操作 API 要 404：%d", code)
	}
	if code := getCode(t, "http://"+fa+"/hooks/h1"); code != 200 {
		t.Fatalf("Funnel 上的網址觸發放行：%d", code)
	}
	// 白名單改掉：下一個請求就擋。
	if err := s.SetFunnel(ctx, true, []string{"203.0.113.7"}); err != nil {
		t.Fatal(err)
	}
	if code := getCode(t, "http://"+fa+"/hooks/h1"); code != 403 {
		t.Fatalf("不在白名單：%d", code)
	}
	// 關掉 Funnel：listener 收掉。
	if err := s.SetFunnel(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "funnel off", func() bool { return s.Status(true).Funnel.State == FunnelOff })
	if _, err := http.Get("http://" + fa + "/hooks/h1"); err == nil {
		t.Fatal("Funnel 關掉後連不到")
	}
	// 改名字：重開節點。
	if err := s.SetTailscale(ctx, TailscaleChange{Enabled: true, Hostname: "home-xbot"}); err != nil {
		t.Fatal(err)
	}
	if len(*nodes) != 2 || (*nodes)[1].cfg.Hostname != "home-xbot" || !n.closed {
		t.Fatalf("改名字要重開節點：%d", len(*nodes))
	}
	// 關閉：節點收掉，設定保留。
	if err := s.SetTailscale(ctx, TailscaleChange{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(true); st.Tailscale.State != StateOff || st.Tailscale.Hostname != "home-xbot" {
		t.Fatalf("關閉後：%+v", st.Tailscale)
	}
	// 重開服務：從設定讀回來。
	s2 := New(Options{DB: db, NewNode: func(NodeConfig) Node { return &fakeNode{} }})
	if err := s2.Start(ctx, echo); err != nil {
		t.Fatal(err)
	}
	if st := s2.Status(true); st.Tailscale.Enabled || !st.Tailscale.HasAuthKey || st.Tailscale.Hostname != "home-xbot" {
		t.Fatalf("重開後讀回設定：%+v", st.Tailscale)
	}
}

func TestFunnelNotAllowedByACL(t *testing.T) {
	s, nodes, _ := newSvc(t)
	ctx := context.Background()
	if err := s.SetTailscale(ctx, TailscaleChange{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	n := (*nodes)[0]
	n.set(func(st *NodeStatus) { *st = NodeStatus{BackendState: "Running", DNSName: "xbot.t.ts.net"} })
	if err := s.SetFunnel(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "funnel error", func() bool { return s.Status(true).Funnel.State == FunnelError })
	st := s.Status(true)
	if !strings.Contains(st.Funnel.Message, "nodeAttrs") || st.Tailscale.Cert != CertMissing || !strings.Contains(st.Tailscale.CertMessage, "MagicDNS") || n.funnelCalls != 0 {
		t.Fatalf("ACL 沒允許 Funnel、沒開 HTTPS 時要指出去哪裡開：%+v", st)
	}
	// Funnel 開不起來（例：Tailscale 拒絕）：顯示錯誤，不重試到改設定為止。
	n.set(func(st *NodeStatus) { st.Funnel = true; st.CertDomains = []string{"xbot.t.ts.net"}; st.MagicDNS = true })
	n.mu.Lock()
	n.funnelErr = errors.New("nope")
	n.mu.Unlock()
	if err := s.SetFunnel(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "funnel error 2", func() bool { return strings.Contains(s.Status(true).Funnel.Message, "nope") })
	time.Sleep(50 * time.Millisecond)
	n.mu.Lock()
	calls := n.funnelCalls
	n.mu.Unlock()
	if calls != 1 {
		t.Fatalf("失敗後不自動重試：%d", calls)
	}
}

func getCode(t *testing.T, u string) int {
	t.Helper()
	resp, err := http.Post(u, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
