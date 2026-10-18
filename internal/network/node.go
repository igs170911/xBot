package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// NodeConfig 是開一個內建 Tailscale 節點需要的設定。
type NodeConfig struct {
	Dir      string // 狀態目錄（資料目錄底下）：登入後的節點金鑰存在這裡，重開服務不用再登入
	Hostname string
	AuthKey  string // 空白：互動登入（狀態會給登入網址）
	Logger   *slog.Logger
}

// NodeStatus 是節點狀態裡 XBot 用得到的部分。
type NodeStatus struct {
	// BackendState：NoState、NeedsLogin、NeedsMachineAuth、Starting、Running、Stopped。
	BackendState string
	AuthURL      string // 互動登入的網址（NeedsLogin 時）
	DNSName      string // 例：xbot.tail3a9e.ts.net（結尾沒有點）
	Tailnet      string // tailnet 名稱
	MagicDNS     bool
	CertDomains  []string // 空白表示 tailnet 沒開 HTTPS 憑證
	IPs          []string
	User         string // 節點擁有者的 Tailscale 登入名稱
	Devices      int    // tailnet 上其他裝置數（不含這台）
	Online       int    // 其中在線上的
	Funnel       bool   // tailnet 的 ACL 有沒有允許這台用 Funnel（nodeAttrs funnel）
}

// Node 是內建 Tailscale 節點。正式版是 tsnet.Server；測試換成假的，不用真的 Tailscale。
type Node interface {
	Start() error
	Status(ctx context.Context) (NodeStatus, error)
	// ListenTLS 在 tailnet 上聽 HTTPS（憑證自動向 Tailscale 拿）。
	ListenTLS(addr string) (net.Listener, error)
	// ListenFunnel 只在網際網路（Funnel）上聽 HTTPS（和 tailnet 的 ListenTLS 同一個埠、各自一個 listener）。
	// 連線的真正來源用 FunnelSource 取得。
	ListenFunnel(addr string) (net.Listener, error)
	// WhoIs 回答 tailnet 上某個來源位址是哪個 Tailscale 使用者。
	WhoIs(ctx context.Context, remoteAddr string) (string, error)
	// Cert 先拿一次 HTTPS 憑證（第一次要幾秒到幾十秒），讓狀態頁能顯示憑證好了沒。
	Cert(ctx context.Context, domain string) error
	Close() error
}

// NewTSNode 用 tailscale.com/tsnet 開一個節點。
func NewTSNode(c NodeConfig) Node {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &tsnet.Server{
		Dir:      c.Dir,
		Hostname: c.Hostname,
		AuthKey:  c.AuthKey,
		// tsnet 的內部紀錄很多；只在除錯等級留下。登入網址由狀態頁顯示，不靠紀錄。
		Logf:     func(format string, args ...any) { logger.Debug("tailscale：" + fmt.Sprintf(format, args...)) },
		UserLogf: func(format string, args ...any) { logger.Info("tailscale：" + fmt.Sprintf(format, args...)) },
	}
	return &tsNode{s: s}
}

type tsNode struct{ s *tsnet.Server }

func (n *tsNode) Start() error { return n.s.Start() }

func (n *tsNode) Status(ctx context.Context) (NodeStatus, error) {
	lc, err := n.s.LocalClient()
	if err != nil {
		return NodeStatus{}, err
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return NodeStatus{}, err
	}
	return fromIPN(st), nil
}

func fromIPN(st *ipnstate.Status) NodeStatus {
	out := NodeStatus{BackendState: st.BackendState, AuthURL: st.AuthURL, CertDomains: st.CertDomains}
	if st.CurrentTailnet != nil {
		out.Tailnet = st.CurrentTailnet.Name
		out.MagicDNS = st.CurrentTailnet.MagicDNSEnabled
	}
	for _, ip := range st.TailscaleIPs {
		out.IPs = append(out.IPs, ip.String())
	}
	if self := st.Self; self != nil {
		out.DNSName = strings.TrimSuffix(self.DNSName, ".")
		if u, ok := st.User[self.UserID]; ok {
			out.User = u.LoginName
		}
		out.Funnel = ipn.CheckFunnelAccess(uint16(443), self) == nil
	}
	for _, p := range st.Peer {
		out.Devices++
		if p.Online {
			out.Online++
		}
	}
	return out
}

func (n *tsNode) ListenTLS(addr string) (net.Listener, error) { return n.s.ListenTLS("tcp", addr) }

func (n *tsNode) ListenFunnel(addr string) (net.Listener, error) {
	return n.s.ListenFunnel("tcp", addr, tsnet.FunnelOnly())
}

func (n *tsNode) WhoIs(ctx context.Context, remoteAddr string) (string, error) {
	lc, err := n.s.LocalClient()
	if err != nil {
		return "", err
	}
	w, err := lc.WhoIs(ctx, remoteAddr)
	if err != nil {
		return "", err
	}
	if w.UserProfile == nil {
		return "", errors.New("查不到使用者")
	}
	return w.UserProfile.LoginName, nil
}

func (n *tsNode) Cert(ctx context.Context, domain string) error {
	lc, err := n.s.LocalClient()
	if err != nil {
		return err
	}
	_, _, err = lc.CertPair(ctx, domain)
	return err
}

func (n *tsNode) Close() error { return n.s.Close() }

// FunnelSource 回答一條連線是不是從 Tailscale Funnel（網際網路）來的；是的話回真正的來源位址。
// tsnet 把 Funnel 的連線包成 *ipn.FunnelConn，外面再包一層 TLS。
func FunnelSource(c net.Conn) (netip.AddrPort, bool) {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if fc, ok := c.(*ipn.FunnelConn); ok {
		return fc.Src, true
	}
	return netip.AddrPort{}, false
}
