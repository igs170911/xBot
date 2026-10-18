package network

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"xbot/internal/auth"
	"xbot/internal/limits"
)

// 外部入口（Funnel／反向代理，PRD MCP-2）只開放這幾條路徑，其他一律 404。
const (
	PathMCP   = "/mcp"
	PathPair  = "/api/v1/agents.pair"
	PathHooks = "/hooks/"
)

// ExternalPaths 是外部入口開放的路徑（給狀態頁與文件顯示）。
var ExternalPaths = []string{PathMCP, PathPair, PathHooks + "*"}

// AllowedPath 回答外部入口能不能進這條路徑：/mcp、/api/v1/agents.pair、/hooks/<id>。
// 路徑要是已經整理過的樣子（沒有 .. 、//、結尾斜線、跳脫字元），避免繞過。
func AllowedPath(r *http.Request) bool {
	p := r.URL.Path
	if p == "" || path.Clean(p) != p || r.URL.RawPath != "" {
		return false
	}
	switch p {
	case PathMCP, PathPair:
		return true
	}
	id, ok := strings.CutPrefix(p, PathHooks)
	return ok && id != "" && !strings.Contains(id, "/")
}

type clientKey struct{}

// WithClient 記下外部請求真正的來源 IP（Funnel 的 FunnelConn.Src，或反向代理標的 X-Forwarded-For）。
func WithClient(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientKey{}, ip)
}

func clientOf(r *http.Request) netip.Addr {
	if ip, ok := r.Context().Value(clientKey{}).(netip.Addr); ok && ip.IsValid() {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, _ := netip.ParseAddr(host)
	return ip.Unmap()
}

// Gate 是外部入口的守門員（PRD MCP-2）：
//  1. 只開放 /mcp、/api/v1/agents.pair、/hooks/<id>，其他 404；
//  2. 有 IP 白名單時，不在名單裡的 403；
//  3. 每個來源 IP 每分鐘最多 limits.ExternalRequestsPerMin 個請求（驗證權杖之前就擋），超過 429；
//  4. /mcp 每個請求都要帶代理權杖（Authorization: Bearer）；不接受 cookie，也不接受登入碼（你本人的完整權限不經外部入口）；
//  5. 之後的處理器看到的是外部請求：auth.IsExternal 為 true（不算本機、MCP 用比較嚴的速率上限），RemoteAddr 換成真正的來源。
//
// 配對與網址觸發有它們自己的驗證（配對碼、網址金鑰）與失敗鎖定。
type Gate struct {
	Next        http.Handler
	AllowIPs    func() []netip.Prefix // 空白：不限 IP
	AccessToken string                // 登入碼：外部入口不收
	Limiter     *IPLimiter
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !AllowedPath(r) {
		gateError(w, http.StatusNotFound, "not_found", "外部入口沒有這個路徑")
		return
	}
	ip := clientOf(r)
	if g.AllowIPs != nil {
		if allow := g.AllowIPs(); len(allow) > 0 && !inPrefixes(ip, allow) {
			gateError(w, http.StatusForbidden, "forbidden", "這個 IP 不在外部入口的白名單裡")
			return
		}
	}
	if g.Limiter != nil {
		if wait := g.Limiter.Allow(ip.String()); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
			gateError(w, http.StatusTooManyRequests, "limit", "外部入口的請求太頻繁，請等一下再試")
			return
		}
	}
	r = r.Clone(auth.WithExternal(r.Context()))
	r.Header.Del("Cookie") // 外部入口不認登入的 session
	if ip.IsValid() {
		r.RemoteAddr = netip.AddrPortFrom(ip, 0).String()
	}
	if r.URL.Path == PathMCP {
		raw, ok := bearerToken(r)
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="xbot"`)
			gateError(w, http.StatusUnauthorized, "unauthenticated", "外部入口一律要代理權杖（Authorization: Bearer xbot_…）")
			return
		}
		if g.AccessToken != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(g.AccessToken)) == 1 {
			gateError(w, http.StatusForbidden, "forbidden", "外部入口不接受登入碼；請配對一個代理權杖")
			return
		}
	}
	g.Next.ServeHTTP(w, r)
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(raw), true
}

func inPrefixes(ip netip.Addr, ps []netip.Prefix) bool {
	if !ip.IsValid() {
		return false
	}
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func gateError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "retryable": status == http.StatusTooManyRequests}})
}

// IPLimiter 是外部入口每個來源 IP 的請求計數（固定一分鐘的滑動視窗）。和代理的操作上限是不同的計數。
type IPLimiter struct {
	Rate int              // 每分鐘幾個；0 用 limits.ExternalRequestsPerMin
	Now  func() time.Time // 測試用

	mu   sync.Mutex
	hits map[string][]time.Time
}

// Allow 記一次請求；超過上限時回要等多久（被拒的不算）。
func (l *IPLimiter) Allow(key string) time.Duration {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	rate := l.Rate
	if rate <= 0 {
		rate = limits.ExternalRequestsPerMin
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-limits.RateWindow)
	if len(l.hits) > limits.ExternalIPTrackMax {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
				delete(l.hits, k)
			}
		}
	}
	ts := l.hits[key]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= rate {
		l.hits[key] = ts
		return ts[0].Add(limits.RateWindow).Sub(now)
	}
	l.hits[key] = append(ts, now)
	return 0
}

// ParseAllowIPs 把白名單（IP 或 CIDR，例：203.0.113.7、198.51.100.0/24）轉成網段。
func ParseAllowIPs(list []string) ([]netip.Prefix, []string, error) {
	var out []netip.Prefix
	var clean []string
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(s, "/") {
			pp, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, nil, &allowErr{s}
			}
			p = pp.Masked()
		} else {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				return nil, nil, &allowErr{s}
			}
			ip = ip.Unmap()
			p = netip.PrefixFrom(ip, ip.BitLen())
		}
		out = append(out, p)
		clean = append(clean, p.String())
	}
	if len(clean) > limits.AllowIPsMax {
		return nil, nil, fmt.Errorf("IP 白名單最多 %d 筆", limits.AllowIPsMax)
	}
	return out, clean, nil
}

type allowErr struct{ s string }

func (e *allowErr) Error() string { return "看不懂的 IP 或網段：" + e.s }
