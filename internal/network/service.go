// Package network 是內建 Tailscale（tsnet）與外部入口（C35，PRD §17、MCP-2）。
//
//   - 內建 Tailscale：預設關閉。開啟後服務自己就是 tailnet 上的一台機器（預設名字 xbot），
//     在 https://xbot.<tailnet>.ts.net 提供同一個網頁與 API（正式 HTTPS 憑證，不用裝 CA、不開公開埠）。
//     tailnet 來的請求一律要登入或權杖，永遠不當成本機的你；登入畫面會提示是哪個 Tailscale 使用者。
//   - 外部入口（Funnel）：預設關閉、要你本人確認才能開。只開放 /mcp、/api/v1/agents.pair、/hooks/<id>，
//     其他一律 404；/mcp 一律要代理權杖；可設 IP 白名單；速率上限比 tailnet 內嚴（Gate）。
//   - 反向代理模式：XBOT_PUBLIC_URL＝你自己的 HTTPS 網址。從反向代理進來的請求同樣經過 Gate。
package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"xbot/internal/auth"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 設定存在 setting 表。登入金鑰另外存（加密過；名稱含 key，xbot.settings.get 也不會回傳）。
const (
	SettingKey     = "network"
	AuthKeySetting = "network.tailscale_auth_key"
	authKeyAAD     = "network:tailscale-auth-key"
	// StateDir 是資料目錄底下放 tsnet 狀態（節點金鑰、憑證）的目錄。
	StateDir = "tailscale"
)

// Tailscale 的狀態。
const (
	StateOff        = "off"
	StateStarting   = "starting"
	StateNeedsLogin = "needs_login"
	StateRunning    = "running"
	StateError      = "error"
)

// 憑證狀態。
const (
	CertNone    = "none"    // 還沒連上
	CertMissing = "missing" // tailnet 沒開 MagicDNS 或 HTTPS 憑證（TS-3）
	CertPending = "pending" // 正在向 Tailscale 拿
	CertReady   = "ready"
	CertError   = "error"
)

// Funnel 的狀態。
const (
	FunnelOff         = "off"
	FunnelStarting    = "starting"
	FunnelOn          = "on"
	FunnelError       = "error"
	FunnelUnavailable = "unavailable" // 內建 Tailscale 沒開或還沒連上
)

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Settings 是存在 setting 表的網路設定。
type Settings struct {
	Tailscale bool     `json:"tailscale"`
	Hostname  string   `json:"hostname,omitempty"`
	Funnel    bool     `json:"funnel"`
	AllowIPs  []string `json:"allow_ips,omitempty"`
}

// Sealer 加密登入金鑰（proxy.Keyring）。
type Sealer interface {
	Seal(plaintext []byte, aad string) ([]byte, error)
	Open(sealed []byte, aad string) ([]byte, error)
}

// Options 是建立 Service 需要的東西。
type Options struct {
	DB      *store.DB
	Sealer  Sealer
	DataDir string
	// LocalURL 是本機網址（例：http://localhost:8780）。
	LocalURL string
	// PublicURL 是反向代理模式的對外網址（XBOT_PUBLIC_URL）；空白表示沒有用反向代理。
	PublicURL string
	// TrustedProxies 是可以標 X-Forwarded-For 的反向代理位址（XBOT_TRUSTED_PROXIES）；空白用 127.0.0.1、::1。
	TrustedProxies []netip.Prefix
	// AccessToken 是登入碼：外部入口不收。
	AccessToken string
	Logger      *slog.Logger
	// NewNode 開一個 Tailscale 節點；空白用 NewTSNode（tsnet）。測試換成假的。
	NewNode func(NodeConfig) Node
	// Poll 是狀態檢查間隔；0 用 limits.TailscalePoll。
	Poll time.Duration
}

// Service 管理內建 Tailscale 與外部入口。
type Service struct {
	o       Options
	limiter *IPLimiter

	mu      sync.Mutex
	set     Settings
	allow   []netip.Prefix
	hasKey  bool
	handler http.Handler
	run     *runner
	whois   map[string]whoisEntry
}

type whoisEntry struct {
	user string
	at   time.Time
}

// runner 是一個開著的 Tailscale 節點。欄位由 Service.mu 保護（node、cancel、done、kick 建立後不變）。
type runner struct {
	node   Node
	cancel context.CancelFunc
	done   chan struct{}
	kick   chan struct{}

	state, message string
	st             NodeStatus
	cert, certMsg  string
	funnel, funMsg string
	// funnelGen 每次改 Funnel 設定（kick）就加一；funnelTried 是最後一次試過的設定版本。
	// 開失敗不自動重試，直到 funnelGen 又變（改設定）。
	funnelGen        uint64
	funnelTried      uint64
	tailnetSrv       *http.Server
	funnelSrv        *http.Server
	tailnetLn        net.Listener
	tailnetListenErr string
}

// New 建立 Service。要呼叫 Start 才會讀設定、開 Tailscale。
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.NewNode == nil {
		o.NewNode = NewTSNode
	}
	if o.Poll == 0 {
		o.Poll = limits.TailscalePoll
	}
	if len(o.TrustedProxies) == 0 {
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	}
	return &Service{o: o, limiter: &IPLimiter{}, whois: map[string]whoisEntry{}}
}

// Start 讀設定；內建 Tailscale 有開的話在背景連上 tailnet。handler 是整個服務的 HTTP 處理器（tailnet 與 Funnel 共用）。
func (s *Service) Start(ctx context.Context, handler http.Handler) error {
	set, err := s.load(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.handler = handler
	s.apply(set)
	s.mu.Unlock()
	if set.Tailscale {
		return s.restart(ctx)
	}
	return nil
}

// Close 關掉 Tailscale 節點。
func (s *Service) Close() error {
	s.stop()
	return nil
}

func (s *Service) load(ctx context.Context) (Settings, error) {
	var set Settings
	if s.o.DB == nil {
		return set, nil
	}
	if _, err := s.o.DB.GetSettingInto(ctx, SettingKey, &set); err != nil {
		return set, err
	}
	_, err := s.o.DB.GetSetting(ctx, AuthKeySetting)
	s.mu.Lock()
	s.hasKey = err == nil
	s.mu.Unlock()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return set, err
	}
	return set, nil
}

// apply 換上新的設定（s.mu 要鎖著）。
func (s *Service) apply(set Settings) {
	if set.Hostname == "" {
		set.Hostname = limits.TailnetHostname
	}
	allow, clean, err := ParseAllowIPs(set.AllowIPs)
	if err != nil { // 存進去之前驗證過；壞掉的話寧可全擋
		s.o.Logger.Warn("外部入口的 IP 白名單讀不懂，先全部擋下", "err", err)
		allow = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/32")}
	}
	set.AllowIPs = clean
	s.set, s.allow = set, allow
}

func (s *Service) settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

func (s *Service) allowIPs() []netip.Prefix {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allow
}

// ── Tailscale 節點 ─────────────────────────────────────

func (s *Service) stop() {
	s.mu.Lock()
	r := s.run
	s.run = nil
	s.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}

// restart 依目前的設定重開節點（設定改了名字或金鑰時）。
func (s *Service) restart(ctx context.Context) error {
	s.stop()
	set := s.settings()
	if !set.Tailscale {
		return nil
	}
	key, err := s.authKey(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.o.DataDir, StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("建立 Tailscale 狀態目錄失敗：%w", err)
	}
	node := s.o.NewNode(NodeConfig{Dir: dir, Hostname: set.Hostname, AuthKey: key, Logger: s.o.Logger})
	rctx, cancel := context.WithCancel(context.Background())
	r := &runner{node: node, cancel: cancel, done: make(chan struct{}), kick: make(chan struct{}, 1),
		state: StateStarting, cert: CertNone, funnel: FunnelOff, funnelGen: 1}
	s.mu.Lock()
	s.run = r
	s.mu.Unlock()
	go s.loop(rctx, r)
	return nil
}

// AuthKey 回傳存著的 Tailscale 登入金鑰（沒有時空白）。「從家裡出去」（B31）沒有自己的金鑰時借用。
func (s *Service) AuthKey(ctx context.Context) (string, error) { return s.authKey(ctx) }

func (s *Service) authKey(ctx context.Context) (string, error) {
	if s.o.DB == nil || s.o.Sealer == nil {
		return "", nil
	}
	var sealed []byte
	ok, err := s.o.DB.GetSettingInto(ctx, AuthKeySetting, &sealed)
	if err != nil || !ok {
		return "", err
	}
	pt, err := s.o.Sealer.Open(sealed, authKeyAAD)
	if err != nil {
		return "", fmt.Errorf("存著的 Tailscale 登入金鑰解不開，請重新輸入：%w", err)
	}
	return string(pt), nil
}

// Kick 讓節點馬上重新檢查（Funnel 開關改了）。
func (s *Service) kick() {
	s.mu.Lock()
	r := s.run
	if r != nil {
		r.funnelGen++
	}
	s.mu.Unlock()
	if r != nil {
		select {
		case r.kick <- struct{}{}:
		default:
		}
	}
}

func (s *Service) loop(ctx context.Context, r *runner) {
	defer close(r.done)
	defer s.teardown(r)
	if err := r.node.Start(); err != nil {
		s.mu.Lock()
		r.state, r.message = StateError, "Tailscale 啟動失敗："+err.Error()
		s.mu.Unlock()
		s.o.Logger.Warn("內建 Tailscale 啟動失敗", "err", err)
		<-ctx.Done()
		return
	}
	t := time.NewTicker(s.o.Poll)
	defer t.Stop()
	for {
		s.tick(ctx, r)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.kick:
		}
	}
}

func (s *Service) tick(ctx context.Context, r *runner) {
	// 先記下設定版本再讀狀態：讀狀態（不鎖）的期間改了設定的話，這次讀到的狀態可能是改之前的，
	// 不能拿它判定「這版設定試過了」（不然 kick 觸發的下一輪會被跳過）。
	s.mu.Lock()
	gen := r.funnelGen
	s.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	st, err := r.node.Status(sctx)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			r.state, r.message = StateError, "讀不到 Tailscale 狀態："+err.Error()
		}
		return
	}
	r.st = st
	switch st.BackendState {
	case "Running":
		r.state, r.message = StateRunning, ""
	case "NeedsLogin":
		r.state, r.message = StateNeedsLogin, "打開登入網址，用你的 Tailscale 帳號核准這台 XBot"
		if st.AuthURL == "" {
			r.message = "等 Tailscale 給登入網址…"
		}
	case "NeedsMachineAuth":
		r.state, r.message = StateNeedsLogin, "這台 XBot 要在 Tailscale 管理後台核准（Machines → Approve）"
	default:
		r.state, r.message = StateStarting, ""
	}
	if r.state != StateRunning {
		return
	}
	s.ensureTailnet(r)
	s.ensureCert(ctx, r)
	s.ensureFunnel(r, gen)
}

func (s *Service) serverFor(h http.Handler, funnel bool) *http.Server {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if funnel {
		srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
			if src, ok := FunnelSource(c); ok {
				return WithClient(ctx, src.Addr().Unmap())
			}
			return ctx
		}
	}
	return srv
}

// ensureTailnet 在 tailnet 上開 HTTPS（s.mu 鎖著）。
func (s *Service) ensureTailnet(r *runner) {
	if r.tailnetSrv != nil || r.tailnetListenErr != "" {
		return
	}
	ln, err := r.node.ListenTLS(":" + strconv.Itoa(limits.TailnetHTTPSPort))
	if err != nil {
		r.tailnetListenErr = err.Error()
		r.state, r.message = StateError, "tailnet 上開不了 HTTPS："+err.Error()
		return
	}
	srv := s.serverFor(s.TailnetHandler(), false)
	r.tailnetSrv, r.tailnetLn = srv, ln
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.o.Logger.Warn("tailnet 的 HTTPS 停了", "err", err)
		}
	}()
	s.o.Logger.Info("內建 Tailscale 已連上", "url", "https://"+r.st.DNSName)
}

// ensureCert 第一次連上時先拿一次憑證，狀態頁才知道 HTTPS 好了沒（s.mu 鎖著）。
func (s *Service) ensureCert(ctx context.Context, r *runner) {
	if len(r.st.CertDomains) == 0 {
		r.cert, r.certMsg = CertMissing, "tailnet 沒開 HTTPS 憑證：到 Tailscale 管理後台 DNS 頁打開 MagicDNS 與 HTTPS Certificates"
		if !r.st.MagicDNS {
			r.certMsg = "tailnet 沒開 MagicDNS：到 Tailscale 管理後台 DNS 頁打開 MagicDNS，再打開 HTTPS Certificates"
		}
		return
	}
	if r.cert != CertNone && r.cert != CertMissing {
		return
	}
	r.cert, r.certMsg = CertPending, "正在向 Tailscale 拿 HTTPS 憑證（第一次要幾十秒）"
	domain := r.st.CertDomains[0]
	go func() {
		cctx, cancel := context.WithTimeout(ctx, limits.TailscaleCertTimeout)
		defer cancel()
		err := r.node.Cert(cctx, domain)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			r.cert, r.certMsg = CertError, "拿不到 HTTPS 憑證："+err.Error()
			return
		}
		r.cert, r.certMsg = CertReady, ""
	}()
}

// ensureFunnel 依設定開關 Funnel（s.mu 鎖著）。開失敗不自動重試，改設定時才再試。
// statusGen 是讀 r.st 之前的設定版本。
func (s *Service) ensureFunnel(r *runner, statusGen uint64) {
	want := s.set.Funnel
	switch {
	case want && r.funnelSrv == nil && r.funnelTried < r.funnelGen:
		if !r.st.Funnel {
			// 這個判斷靠的是 r.st：只算它讀到時的那版設定試過了。
			r.funnelTried = statusGen
			r.funnel, r.funMsg = FunnelError, "tailnet 的存取規則沒有允許這台用 Funnel：在 Tailscale 管理後台的 Access controls 加上 nodeAttrs 的 funnel"
			return
		}
		r.funnelTried = r.funnelGen // ListenFunnel 用的是現在的設定
		r.funnel, r.funMsg = FunnelStarting, ""
		node := r.node
		s.mu.Unlock() // ListenFunnel 會改 Tailscale 的 serve 設定，可能要幾秒
		ln, err := node.ListenFunnel(":" + strconv.Itoa(limits.TailnetHTTPSPort))
		s.mu.Lock()
		if s.run != r || !s.set.Funnel { // 這段時間被關掉了
			if ln != nil {
				_ = ln.Close()
			}
			if s.run == r {
				r.funnel, r.funMsg = FunnelOff, ""
			}
			return
		}
		if err != nil {
			r.funnel, r.funMsg = FunnelError, "Funnel 開不起來："+err.Error()
			return
		}
		srv := s.serverFor(s.FunnelHandler(), true)
		r.funnelSrv = srv
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.o.Logger.Warn("Funnel 停了", "err", err)
			}
		}()
		r.funnel, r.funMsg = FunnelOn, ""
		s.o.Logger.Warn("外部入口（Tailscale Funnel）已開啟：網際網路上的任何人都連得到 /mcp、/api/v1/agents.pair、/hooks/*", "url", "https://"+r.st.DNSName)
	case !want && r.funnelSrv != nil:
		_ = r.funnelSrv.Close() // 關掉 listener 時 tsnet 會把 Funnel 設定收回
		r.funnelSrv = nil
		r.funnel, r.funMsg = FunnelOff, ""
		s.o.Logger.Info("外部入口（Tailscale Funnel）已關閉")
	case !want:
		r.funnel, r.funMsg = FunnelOff, ""
	}
}

func (s *Service) teardown(r *runner) {
	s.mu.Lock()
	srvs := []*http.Server{r.tailnetSrv, r.funnelSrv}
	r.tailnetSrv, r.funnelSrv = nil, nil
	s.mu.Unlock()
	for _, srv := range srvs {
		if srv != nil {
			_ = srv.Close()
		}
	}
	if err := r.node.Close(); err != nil {
		s.o.Logger.Debug("關閉 Tailscale 節點", "err", err)
	}
}

// ── 處理器 ─────────────────────────────────────────

func (s *Service) inner() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handler == nil {
		return http.NotFoundHandler()
	}
	return s.handler
}

// TailnetHandler 是 tailnet 入口：同一個網頁與 API，但一律當成從別的機器來（要登入或權杖），
// 並查出是哪個 Tailscale 使用者（只給登入畫面提示用）。
func (s *Service) TailnetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithTailnet(r.Context(), s.whoIs(r.Context(), r.RemoteAddr))
		s.inner().ServeHTTP(w, r.WithContext(ctx))
	})
}

// FunnelHandler 是 Funnel 入口：經過 Gate。
func (s *Service) FunnelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gate(s.inner()).ServeHTTP(w, r)
	})
}

func (s *Service) gate(next http.Handler) *Gate {
	return &Gate{Next: next, AllowIPs: s.allowIPs, AccessToken: s.o.AccessToken, Limiter: s.limiter}
}

func (s *Service) whoIs(ctx context.Context, remoteAddr string) string {
	s.mu.Lock()
	r := s.run
	if e, ok := s.whois[remoteAddr]; ok && time.Since(e.at) < limits.WhoIsCacheTTL {
		s.mu.Unlock()
		return e.user
	}
	s.mu.Unlock()
	if r == nil {
		return ""
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	user, err := r.node.WhoIs(wctx, remoteAddr)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	if len(s.whois) > 1000 {
		clear(s.whois)
	}
	s.whois[remoteAddr] = whoisEntry{user: user, at: time.Now()}
	s.mu.Unlock()
	return user
}

// Front 包住本機的 HTTP 入口：設了 XBOT_PUBLIC_URL（反向代理模式）時，從反向代理進來的請求改走 Gate
// （和 Funnel 一樣只開放三條路徑、要權杖、白名單、比較嚴的速率上限）。沒設時原樣不動。
func (s *Service) Front(next http.Handler) http.Handler {
	if s.o.PublicURL == "" {
		return next
	}
	g := s.gate(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := s.Proxied(r); ok {
			g.ServeHTTP(w, r.WithContext(WithClient(r.Context(), ip)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

var forwarded = []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host"}

// Proxied 回答請求是不是從反向代理進來（反向代理模式）：Host 是對外網址，或來自信任的代理且帶了轉送標頭。
// 是的話也回真正的來源 IP（信任的代理標的最後一個 X-Forwarded-For）。
func (s *Service) Proxied(r *http.Request) (netip.Addr, bool) {
	if s.o.PublicURL == "" {
		return netip.Addr{}, false
	}
	remote := clientOf(r)
	trusted := inPrefixes(remote, s.o.TrustedProxies)
	fwd := false
	for _, h := range forwarded {
		if r.Header.Get(h) != "" {
			fwd = true
			break
		}
	}
	if !(trusted && fwd) && !strings.EqualFold(hostOnly(r.Host), s.publicHost()) {
		return netip.Addr{}, false
	}
	if trusted {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
				return ip.Unmap(), true
			}
		}
	}
	return remote, true
}

func (s *Service) publicHost() string {
	u, err := url.Parse(s.o.PublicURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(h, "[]")
}

// ── 狀態 ─────────────────────────────────────────

// TailscaleView 是內建 Tailscale 的狀態。
type TailscaleView struct {
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state" enum:"off,starting,needs_login,running,error"`
	Message     string   `json:"message,omitempty" desc:"給人看的說明（例：要去 Tailscale 後台開什麼）"`
	LoginURL    string   `json:"login_url,omitempty" desc:"互動登入網址（needs_login 時；只給有 admin 的身分）"`
	Hostname    string   `json:"hostname"`
	URL         string   `json:"url,omitempty" desc:"tailnet 網址，例：https://xbot.tail3a9e.ts.net"`
	Tailnet     string   `json:"tailnet,omitempty"`
	User        string   `json:"user,omitempty" desc:"這台 XBot 登入的 Tailscale 帳號"`
	IPs         []string `json:"ips"`
	MagicDNS    bool     `json:"magic_dns"`
	HTTPS       bool     `json:"https" desc:"tailnet 有沒有開 HTTPS 憑證"`
	Cert        string   `json:"cert" enum:"none,missing,pending,ready,error"`
	CertMessage string   `json:"cert_message,omitempty"`
	Devices     int      `json:"devices" desc:"tailnet 上其他裝置數"`
	Online      int      `json:"online" desc:"其中在線上的"`
	HasAuthKey  bool     `json:"has_auth_key" desc:"有沒有存登入金鑰（不回傳金鑰本身）"`
	QR          []string `json:"qr" desc:"tailnet 網址的 QR（每列一個字串，1 是黑點）"`
}

// FunnelView 是外部入口（Funnel）的狀態。
type FunnelView struct {
	Enabled  bool     `json:"enabled" desc:"設定上有沒有開"`
	State    string   `json:"state" enum:"off,starting,on,error,unavailable"`
	Message  string   `json:"message,omitempty"`
	URL      string   `json:"url,omitempty" desc:"網際網路上連得到的網址（開著時）"`
	Allowed  bool     `json:"allowed" desc:"tailnet 的存取規則有沒有允許這台用 Funnel"`
	AllowIPs []string `json:"allow_ips" desc:"IP 白名單；空白表示不限"`
}

// ProxyView 是反向代理模式（XBOT_PUBLIC_URL）。
type ProxyView struct {
	Enabled        bool     `json:"enabled"`
	URL            string   `json:"url,omitempty"`
	TrustedProxies []string `json:"trusted_proxies"`
}

// Status 是 xbot.network.status 的輸出。
type Status struct {
	LocalURL  string        `json:"local_url"`
	Tailscale TailscaleView `json:"tailscale"`
	Funnel    FunnelView    `json:"funnel"`
	Proxy     ProxyView     `json:"proxy"`
	// PublicURL 是外部（tailnet 外）連得到的網址：Funnel 開著時是 tailnet 網址，否則是反向代理的網址；都沒有時空白。
	PublicURL string `json:"public_url" desc:"外部連得到的網址；網址觸發與代理配對說明用它"`
	// BaseURL 是給其他裝置用的最佳網址：public_url → tailnet 網址 → 空白（前端退回目前網址）。
	BaseURL       string   `json:"base_url"`
	ExternalPaths []string `json:"external_paths" desc:"外部入口開放的路徑"`
	ExternalRate  int      `json:"external_rate_per_min" desc:"外部入口代理每分鐘呼叫上限"`
}

// Status 回傳目前的網路狀態。withLogin 為 false 時不回登入網址（只有 admin 能看：拿到它的人能把這台 XBot 登入到自己的 tailnet）。
func (s *Service) Status(withLogin bool) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.set
	out := Status{
		LocalURL: s.o.LocalURL,
		Tailscale: TailscaleView{Enabled: set.Tailscale, State: StateOff, Hostname: set.Hostname, Cert: CertNone,
			HasAuthKey: s.hasKey, IPs: []string{}, QR: []string{}},
		Funnel:        FunnelView{Enabled: set.Funnel, State: FunnelUnavailable, AllowIPs: append([]string{}, set.AllowIPs...)},
		Proxy:         ProxyView{Enabled: s.o.PublicURL != "", URL: strings.TrimRight(s.o.PublicURL, "/"), TrustedProxies: []string{}},
		ExternalPaths: ExternalPaths,
		ExternalRate:  limits.ExternalAgentRatePerMin,
	}
	for _, p := range s.o.TrustedProxies {
		out.Proxy.TrustedProxies = append(out.Proxy.TrustedProxies, p.String())
	}
	if !set.Funnel {
		out.Funnel.State = FunnelOff
	}
	if r := s.run; r != nil {
		t := &out.Tailscale
		t.State, t.Message = r.state, r.message
		if r.state == StateNeedsLogin && withLogin {
			t.LoginURL = r.st.AuthURL
		}
		t.Tailnet, t.User, t.MagicDNS, t.HTTPS = r.st.Tailnet, r.st.User, r.st.MagicDNS, len(r.st.CertDomains) > 0
		t.Devices, t.Online = r.st.Devices, r.st.Online
		t.IPs = append(t.IPs, r.st.IPs...)
		if r.state == StateRunning {
			t.Cert, t.CertMessage = r.cert, r.certMsg
			if r.st.DNSName != "" {
				t.URL = "https://" + r.st.DNSName
				t.QR = qrRows(t.URL)
			}
			out.Funnel.Allowed = r.st.Funnel
			if set.Funnel {
				out.Funnel.State, out.Funnel.Message = r.funnel, r.funMsg
				if r.funnel == FunnelOn {
					out.Funnel.URL = t.URL
				}
			}
		}
	}
	if set.Funnel && out.Funnel.State == FunnelUnavailable {
		out.Funnel.Message = "內建 Tailscale 連上之後才會開"
	}
	out.PublicURL = out.Funnel.URL
	if out.PublicURL == "" {
		out.PublicURL = out.Proxy.URL
	}
	out.BaseURL = out.PublicURL
	if out.BaseURL == "" {
		out.BaseURL = out.Tailscale.URL
	}
	return out
}

// qrRows 把網址編成 QR（每列一個 0／1 字串，不含外框留白；前端畫成 SVG）。
func qrRows(text string) []string {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return []string{}
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	rows := make([]string, len(bm))
	for i, row := range bm {
		var b strings.Builder
		for _, on := range row {
			if on {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		rows[i] = b.String()
	}
	return rows
}

// ── 改設定 ─────────────────────────────────────────

// TailscaleChange 是改內建 Tailscale 設定的內容。
type TailscaleChange struct {
	Enabled      bool
	Hostname     string // 空白：不改（第一次用 xbot）
	AuthKey      string // 空白：不改
	ClearAuthKey bool
}

// ValidHostname 檢查 tailnet 名字（小寫英數字與 -）。
func ValidHostname(h string) bool {
	return len(h) <= limits.TailnetHostnameMax && hostnameRE.MatchString(h)
}

// SetTailscale 存設定並重開節點。
func (s *Service) SetTailscale(ctx context.Context, c TailscaleChange) error {
	set := s.settings()
	before := set
	set.Tailscale = c.Enabled
	if c.Hostname != "" {
		set.Hostname = c.Hostname
	}
	keyChanged := c.AuthKey != "" || c.ClearAuthKey
	err := s.o.DB.Tx(ctx, func(q *store.Queries) error {
		if c.ClearAuthKey {
			if err := q.DeleteSetting(ctx, AuthKeySetting); err != nil {
				return err
			}
		}
		if c.AuthKey != "" {
			if s.o.Sealer == nil {
				return errors.New("沒有加密金鑰，存不了登入金鑰")
			}
			sealed, err := s.o.Sealer.Seal([]byte(c.AuthKey), authKeyAAD)
			if err != nil {
				return err
			}
			if err := q.SetSetting(ctx, AuthKeySetting, sealed); err != nil {
				return err
			}
		}
		return q.SetSetting(ctx, SettingKey, set)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if c.AuthKey != "" {
		s.hasKey = true
	} else if c.ClearAuthKey {
		s.hasKey = false
	}
	s.apply(set)
	running := s.run != nil
	s.mu.Unlock()
	if !set.Tailscale {
		s.stop()
		return nil
	}
	if running && !keyChanged && before.Hostname == s.settings().Hostname && before.Tailscale {
		return nil // 沒有改到節點
	}
	return s.restart(ctx)
}

// SetFunnel 存外部入口設定；Funnel 開關立刻生效，白名單下一個請求就生效。
func (s *Service) SetFunnel(ctx context.Context, enabled bool, allowIPs []string) error {
	_, clean, err := ParseAllowIPs(allowIPs)
	if err != nil {
		return err
	}
	set := s.settings()
	set.Funnel, set.AllowIPs = enabled, clean
	if err := s.o.DB.SetSetting(ctx, SettingKey, set); err != nil {
		return err
	}
	s.mu.Lock()
	s.apply(set)
	s.mu.Unlock()
	s.kick()
	return nil
}

// Preview 是預演用：套上改變後的設定看起來會怎樣（不存、不動節點）。
func (s *Service) Preview(f func(*Settings)) Settings {
	set := s.settings()
	set.AllowIPs = append([]string{}, set.AllowIPs...)
	f(&set)
	return set
}

// ParseTrustedProxies 讀 XBOT_TRUSTED_PROXIES（逗號分隔的 IP 或 CIDR）。
func ParseTrustedProxies(v string) ([]netip.Prefix, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	p, _, err := ParseAllowIPs(strings.Split(v, ","))
	if err != nil {
		return nil, fmt.Errorf("XBOT_TRUSTED_PROXIES：%w", err)
	}
	return p, nil
}

// CheckPublicURL 檢查 XBOT_PUBLIC_URL：要是 https:// 開頭、沒有路徑的網址。
func CheckPublicURL(v string) (string, error) {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("XBOT_PUBLIC_URL 要是像 https://xbot.example.com 的網址：%q", v)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("XBOT_PUBLIC_URL 要用 https（外部入口一律走 HTTPS）：%q", v)
	}
	return v, nil
}
