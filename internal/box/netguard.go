package box

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/boxfs"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 網路防護（SEC-4）：Bot 的電腦預設連不到主機與私有網段，只連得到網際網路。
//
// 每台 Docker 主機（本機與遠端都一樣，只用 Docker API，不需要主機的 root 或 SSH 指令）上：
//   - 電腦接在 XBot 自己的 bridge 網路 xbot-box（介面 xbot-box0，關掉容器間互通），每台有固定 IP（box.net_ip）；
//   - 一個 xbot-netguard 容器（同一個映像、主機網路、只有 NET_ADMIN／NET_RAW、唯讀根目錄、restart always）
//     把 XBot 算好的 iptables 規則放進 XBOT-FWD（DOCKER-USER 的第一條）與 XBOT-IN（INPUT 的第一條），
//     每 5 秒檢查一次，規則被防火牆重新載入清掉或被擠到後面時放回去（box/xbot-netguard）；
//   - 電腦開機時（start-bot.sh）先等 xbot-netguard 寫下「這次開機的規則已生效」的標記，主機重開機時電腦不會搶在規則前連網。
//
// 擋下的：送到主機本身的連線（bridge 閘道、docker0、主機在區網或 tailnet 的位址；主機上聽在所有介面的服務也連不到）、
// 私有與保留網段（10/8、172.16/12、192.168/16、100.64/10（含 Tailscale）、169.254/16 等）、電腦之間互連。
// 放行的：主機開的連線的回應（noVNC、Hermes 埠）、DNS（53 埠，往私有網段與主機本身都放行：Docker 內建 DNS 往上游查詢要用，
// 上游常常是區網的路由器或主機自己）、每個 Bot 的私有網路白名單。
// product.sock 是掛進容器的 Unix socket，不經網路，不受影響。

// 網路防護用到的 Docker 物件名稱。
const (
	NetworkName      = "xbot-box"
	BridgeName       = "xbot-box0"
	GuardName        = "xbot-netguard"
	GuardStateVolume = "xbot-netguard-state"
	GuardReadyVolume = "xbot-netguard-ready"
	// LabelNetguard 是 xbot-netguard 容器的版本；和 guardRevision 不同時重建它。
	LabelNetguard = "xbot.netguard"

	guardRevision = "2026.10.23.v6b"
	guardStateDir = "/var/lib/xbot-netguard"
	guardReadyDir = "/var/lib/xbot-netguard-ready"
	guardRules    = "rules.v4"
	guardRulesV6  = "rules.v6"
	guardScript   = "/usr/local/bin/xbot-netguard"
)

// blockedRanges 是電腦預設連不到的目的地（IPv4；xbot-box 網路沒有開 IPv6）。
var blockedRanges = prefixes(
	"0.0.0.0/8",      // 「這個網路」
	"10.0.0.0/8",     // 私有網段
	"100.64.0.0/10",  // CGNAT，Tailscale 的 100.x 位址也在這裡
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local（含雲端的 metadata 服務 169.254.169.254）
	"172.16.0.0/12",  // 私有網段（含 Docker 的各個 bridge）
	"192.0.0.0/24",   // IETF 保留
	"192.168.0.0/16", // 私有網段
	"198.18.0.0/15",  // 效能測試保留
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // 保留與廣播
)

// subnetCandidates 是 xbot-box 網路的子網路候選（依序挑第一個沒和其他 Docker 網路重疊的）。
// 要用固定 IP 就得自己指定子網路；挑不常見的段，降低和區網撞到的機會。
var subnetCandidates = prefixes("172.30.0.0/16", "172.29.0.0/16", "172.28.0.0/16", "10.213.0.0/16", "10.214.0.0/16", "10.215.0.0/16")

func prefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// ── 私有網路白名單 ──────────────────────────────────

// Allow 是一筆私有網路白名單（SEC-4 allow_private_urls）：目的地網段，加上（可選的）TCP 埠。
type Allow struct {
	Prefix netip.Prefix
	// Port 是 0 時不限埠與通訊協定。
	Port uint16
}

// String 是白名單的標準寫法：192.168.1.10、192.168.1.0/24、192.168.1.10:8080。
func (a Allow) String() string {
	host := a.Prefix.String()
	if a.Prefix.IsSingleIP() {
		host = a.Prefix.Addr().String()
	}
	if a.Port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(int(a.Port))
}

// ErrNotPrivate 表示白名單寫的不是私有網段（本來就連得到，不用加）。
var ErrNotPrivate = errors.New("這不是私有網段，Bot 本來就連得到")

// ParseAllow 讀一筆白名單：IP（192.168.1.10）、網段（192.168.1.0/24）、IP:埠（192.168.1.10:8080），
// 或網址（http://192.168.1.10:8080/，只開那個埠；沒寫埠時用 http 80、https 443）。
// 只收 IPv4 位址（主機名稱在主機上和 XBot 這邊可能解析成不同位址，要寫 IP）。
func ParseAllow(s string) (Allow, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Allow{}, errors.New("是空的")
	}
	var host, port string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return Allow{}, fmt.Errorf("網址看不懂：%q", s)
		}
		host, port = u.Hostname(), u.Port()
		if port == "" {
			switch u.Scheme {
			case "http", "ws":
				port = "80"
			case "https", "wss":
				port = "443"
			default:
				return Allow{}, fmt.Errorf("網址要寫埠號：%q", s)
			}
		}
	} else if h, p, err := net.SplitHostPort(s); err == nil {
		host, port = h, p
	} else {
		host = s
	}
	var a Allow
	if strings.Contains(host, "/") {
		p, err := netip.ParsePrefix(host)
		if err != nil {
			return Allow{}, fmt.Errorf("網段看不懂：%q", host)
		}
		a.Prefix = p.Masked()
	} else {
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return Allow{}, fmt.Errorf("要寫 IP 位址（例：192.168.1.10），不能寫主機名稱：%q", host)
		}
		a.Prefix = netip.PrefixFrom(ip, ip.BitLen())
	}
	if !a.Prefix.Addr().Is4() {
		return Allow{}, fmt.Errorf("只支援 IPv4：%q", host)
	}
	if a.Prefix.Bits() < 8 {
		return Allow{}, fmt.Errorf("網段太大：%s（最多 /8）", a.Prefix)
	}
	if a.Prefix.Addr().IsLoopback() {
		return Allow{}, errors.New("127.x 是電腦自己，不用加")
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return Allow{}, fmt.Errorf("埠號不對：%q", port)
		}
		a.Port = uint16(n)
	}
	if !slices.ContainsFunc(blockedRanges, a.Prefix.Overlaps) {
		return Allow{}, fmt.Errorf("%w：%s", ErrNotPrivate, a)
	}
	return a, nil
}

// ParseAllowList 讀整份白名單：去掉重複、依標準寫法排序。
func ParseAllowList(in []string) ([]Allow, error) {
	if len(in) > limits.NetAllowMax {
		return nil, fmt.Errorf("白名單最多 %d 筆", limits.NetAllowMax)
	}
	seen := map[string]bool{}
	var out []Allow
	for _, s := range in {
		a, err := ParseAllow(s)
		if err != nil {
			return nil, err
		}
		if !seen[a.String()] {
			seen[a.String()] = true
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b Allow) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

// NetAllowSetting 是一個 Bot 的私有網路白名單在 setting 表的 key（值：字串陣列的 JSON，標準寫法）。
func NetAllowSetting(botID string) string { return "box.net_allow." + botID }

// ── 規則 ────────────────────────────────────────────

// ruleBox 是一台有白名單的電腦。
type ruleBox struct {
	IP    netip.Addr
	Allow []Allow
	// Egress 是這台電腦連外要經過的 Tailscale 小容器（「從家裡出去」，CL-7）在 xbot-box 網路上的 IP；無效值＝不用。
	// hostRules 只標 wantEgress，IP 等 ensureNet 知道子網路後才填。
	Egress     netip.Addr
	wantEgress bool
}

// buildRules 產生 iptables-restore（--noflush）的內容：只動 XBot 自己的兩條鏈。
func buildRules(boxes []ruleBox) string {
	var b strings.Builder
	in := "-i " + BridgeName
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# XBot network guard (SEC-4) for bridge %s; generated by internal/box/netguard.go", BridgeName)
	w("*filter")
	w(":XBOT-FWD - [0:0]")
	w(":XBOT-IN - [0:0]")
	w("-F XBOT-FWD")
	w("-F XBOT-IN")
	sorted := slices.Clone(boxes)
	slices.SortFunc(sorted, func(a, b ruleBox) int { return a.IP.Compare(b.IP) })
	// 從家裡出去（CL-7）：同一個 bridge 上只放行「這台電腦 → Tailscale 小容器的 proxy 埠」與它的回應，其餘電腦互連照樣擋。
	for _, rb := range sorted {
		if rb.Egress.IsValid() {
			w("-A XBOT-FWD -s %s/32 %s -o %s -d %s/32 -p tcp -m tcp --dport %d -j ACCEPT", rb.IP, in, BridgeName, rb.Egress, limits.EgressProxyPort)
			w("-A XBOT-FWD -s %s/32 %s -o %s -d %s/32 -p tcp -m tcp --sport %d -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
				rb.Egress, in, BridgeName, rb.IP, limits.EgressProxyPort)
		}
	}
	// 轉送（往其他網路）：先放行回應與 DNS，再放行白名單，最後擋電腦互連與私有網段；其餘（網際網路）交回 Docker 的規則。
	w("-A XBOT-FWD %s -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN", in)
	w("-A XBOT-FWD %s -o %s -j DROP", in, BridgeName)
	w("-A XBOT-FWD %s -p udp -m udp --dport 53 -j RETURN", in)
	w("-A XBOT-FWD %s -p tcp -m tcp --dport 53 -j RETURN", in)
	allow := func(chain string) {
		for _, rb := range sorted {
			for _, a := range rb.Allow {
				if a.Port == 0 {
					w("-A %s -s %s/32 %s -d %s -j RETURN", chain, rb.IP, in, a.Prefix)
				} else {
					w("-A %s -s %s/32 %s -d %s -p tcp -m tcp --dport %d -j RETURN", chain, rb.IP, in, a.Prefix, a.Port)
				}
			}
		}
	}
	allow("XBOT-FWD")
	for _, p := range blockedRanges {
		w("-A XBOT-FWD %s -d %s -j DROP", in, p)
	}
	// 送到主機本身：只放行主機開的連線的回應與白名單（主機在區網的位址），其餘全部擋下。
	w("-A XBOT-IN %s -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN", in)
	// DNS：Docker 的內建 DNS 會從電腦的網路往上游查詢，上游常常就是主機自己（daemon.json 的 dns 設成 docker0、dnsmasq 等）。
	w("-A XBOT-IN %s -p udp -m udp --dport 53 -j RETURN", in)
	w("-A XBOT-IN %s -p tcp -m tcp --dport 53 -j RETURN", in)
	allow("XBOT-IN")
	w("-A XBOT-IN %s -j DROP", in)
	w("COMMIT")
	return b.String()
}

// buildRulesV6 產生 ip6tables-restore（--noflush）的內容：Bot 電腦完全不許使用 IPv6（B.5）。
func buildRulesV6() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# XBot network guard IPv6 block (B.5) for bridge %s; generated by internal/box/netguard.go", BridgeName)
	w("*filter")
	w("# Drop all IPv6 traffic from bot containers (FORWARD and INPUT).")
	w("-I FORWARD 1 -i %s -j DROP", BridgeName)
	w("-I INPUT 1 -i %s -j DROP", BridgeName)
	w("COMMIT")
	return b.String()
}

// ── 每台主機的網路與 xbot-netguard ─────────────────────

// hostNet 是一台主機上的 xbot-box 網路與網路防護（快取）。
type hostNet struct {
	mu      sync.Mutex
	subnet  netip.Prefix
	gateway netip.Addr
	// applied 是最後一次套用成功的規則摘要；guardID 是當時的 xbot-netguard 容器。
	applied string
	guardID string
}

// netguard 管所有主機的網路防護。
type netguard struct {
	mu    sync.Mutex
	hosts map[string]*hostNet
	// alloc 讓「挑 IP、寫進 box 表」不會和另一台電腦同時進行。
	alloc sync.Mutex
}

func (g *netguard) host(id string) *hostNet {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hosts == nil {
		g.hosts = map[string]*hostNet{}
	}
	hn, ok := g.hosts[id]
	if !ok {
		hn = &hostNet{}
		g.hosts[id] = hn
	}
	return hn
}

// forget 清掉一台主機的快取（例：映像換了，下次重建 xbot-netguard）。
func (g *netguard) forget(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.hosts, id)
}

// NetError 表示網路防護裝不起來（電腦因此不開機：寧可不開，也不要開一台沒有防護的電腦）。
type NetError struct{ Err error }

func (e *NetError) Error() string {
	return "Bot 電腦的網路防護沒有生效（SEC-4），為了安全先不開機：" + e.Err.Error()
}

func (e *NetError) Unwrap() error { return e.Err }

// ensureNet 確保這台主機有 xbot-box 網路、xbot-netguard 在跑，而且規則是最新的（boxes 是 hostRules 的結果）。
func (m *Manager) ensureNet(ctx context.Context, h hosts.Host, boxes []ruleBox) (*hostNet, error) {
	hn := m.guard.host(h.ID())
	hn.mu.Lock()
	defer hn.mu.Unlock()
	api := h.Docker()
	if !hn.subnet.IsValid() {
		subnet, gw, err := ensureNetwork(ctx, api)
		if err != nil {
			return nil, &NetError{err}
		}
		hn.subnet, hn.gateway = subnet, gw
	}
	gid, err := m.ensureGuard(ctx, api)
	if err != nil {
		return nil, &NetError{err}
	}
	for i := range boxes {
		if boxes[i].wantEgress {
			boxes[i].Egress = EgressAddr(hn.subnet)
		}
	}
	rules := buildRules(boxes)
	sum := sha256.Sum256([]byte(rules))
	digest := hex.EncodeToString(sum[:])
	if hn.applied == digest && hn.guardID == gid {
		return hn, nil
	}
	actx, cancel := context.WithTimeout(ctx, limits.NetguardApplyTimeout)
	defer cancel()
	if err := applyRules(actx, api, gid, rules); err != nil {
		hn.applied = ""
		return nil, &NetError{err}
	}
	hn.applied, hn.guardID = digest, gid
	return hn, nil
}

// ensureNetwork 找到（或建立）xbot-box 網路，回傳它的子網路與閘道。
func ensureNetwork(ctx context.Context, api hosts.DockerAPI) (netip.Prefix, netip.Addr, error) {
	if n, err := api.NetworkInspect(ctx, NetworkName, client.NetworkInspectOptions{}); err == nil {
		return checkNetwork(n.Network.Network)
	} else if !cerrdefs.IsNotFound(err) {
		return netip.Prefix{}, netip.Addr{}, dockerErr(err)
	}
	list, err := api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, dockerErr(err)
	}
	var used []netip.Prefix
	for _, n := range list.Items {
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() {
				used = append(used, c.Subnet)
			}
		}
	}
	off, on := "false", true
	var last error
	for _, cand := range subnetCandidates {
		if slices.ContainsFunc(used, cand.Overlaps) {
			continue
		}
		gw := cand.Addr().Next()
		_, err := api.NetworkCreate(ctx, NetworkName, client.NetworkCreateOptions{
			Driver:     "bridge",
			EnableIPv4: &on,
			IPAM:       &network.IPAM{Config: []network.IPAMConfig{{Subnet: cand, Gateway: gw}}},
			Options: map[string]string{
				"com.docker.network.bridge.name":       BridgeName,
				"com.docker.network.bridge.enable_icc": off,
			},
			Labels: map[string]string{LabelManaged: "true"},
		})
		if err == nil || cerrdefs.IsConflict(err) || strings.Contains(err.Error(), "already exists") {
			// 另一個程序同時建好了：用它的。
			n, ierr := api.NetworkInspect(ctx, NetworkName, client.NetworkInspectOptions{})
			if ierr != nil {
				return netip.Prefix{}, netip.Addr{}, dockerErr(ierr)
			}
			return checkNetwork(n.Network.Network)
		}
		last = err
	}
	if last == nil {
		last = errors.New("每個候選子網路都和主機上其他 Docker 網路重疊")
	}
	return netip.Prefix{}, netip.Addr{}, fmt.Errorf("建立 Bot 電腦的網路 %s 失敗：%w", NetworkName, last)
}

func checkNetwork(n network.Network) (netip.Prefix, netip.Addr, error) {
	if n.Driver != "bridge" || n.Options["com.docker.network.bridge.name"] != BridgeName {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("主機上已經有一個不是 XBot 建的 %s 網路（driver %s）；請刪掉它（docker network rm %s）", NetworkName, n.Driver, NetworkName)
	}
	for _, c := range n.IPAM.Config {
		if c.Subnet.IsValid() && c.Subnet.Addr().Is4() {
			gw := c.Gateway
			if !gw.IsValid() {
				gw = c.Subnet.Addr().Next()
			}
			return c.Subnet.Masked(), gw, nil
		}
	}
	return netip.Prefix{}, netip.Addr{}, fmt.Errorf("%s 網路沒有指定子網路，請刪掉它讓 XBot 重建（docker network rm %s）", NetworkName, NetworkName)
}

// guardOptions 是 xbot-netguard 容器的設定。
func guardOptions(image string) client.ContainerCreateOptions {
	pids := int64(64)
	return client.ContainerCreateOptions{
		Name: GuardName,
		Config: &container.Config{
			Image:      image,
			User:       "0:0",
			Entrypoint: []string{"/usr/bin/tini", "--"},
			Cmd:        []string{guardScript, "run"},
			Labels:     map[string]string{LabelManaged: "true", LabelNetguard: guardRevision},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    network.NetworkHost,
			CapDrop:        []string{"ALL"},
			CapAdd:         []string{"NET_ADMIN", "NET_RAW"},
			SecurityOpt:    secOpt,
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/run": "rw,noexec,nosuid,nodev,size=1m", "/tmp": "rw,noexec,nosuid,nodev,size=8m"},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyAlways},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: GuardStateVolume, Target: guardStateDir},
				{Type: mount.TypeVolume, Source: GuardReadyVolume, Target: guardReadyDir},
			},
			Resources: container.Resources{Memory: 128 << 20, PidsLimit: &pids},
		},
	}
}

// ensureGuard 確保 xbot-netguard 在跑（沒有就建立；版本舊了就重建），回傳容器 id。
func (m *Manager) ensureGuard(ctx context.Context, api hosts.DockerAPI) (string, error) {
	res, err := api.ContainerInspect(ctx, GuardName, client.ContainerInspectOptions{})
	switch {
	case err == nil:
		c := res.Container
		if c.Config == nil || c.Config.Labels[LabelManaged] != "true" {
			return "", fmt.Errorf("主機上已經有一個不是 XBot 建的 %s 容器", GuardName)
		}
		if c.Config.Labels[LabelNetguard] != guardRevision {
			if _, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
				return "", dockerErr(err)
			}
			return m.createGuard(ctx, api)
		}
		if !running(&c) {
			if _, err := api.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
				return "", fmt.Errorf("啟動 %s 失敗：%w", GuardName, dockerErr(err))
			}
		}
		return c.ID, nil
	case cerrdefs.IsNotFound(err):
		return m.createGuard(ctx, api)
	}
	return "", dockerErr(err)
}

func (m *Manager) createGuard(ctx context.Context, api hosts.DockerAPI) (string, error) {
	if _, err := api.ImageInspect(ctx, m.image); err != nil {
		return "", fmt.Errorf("沒有 Bot 電腦映像 %s：%w", m.image, dockerErr(err))
	}
	for _, v := range []string{GuardStateVolume, GuardReadyVolume} {
		if err := ensureVolume(ctx, api, v, ""); err != nil {
			return "", err
		}
	}
	res, err := api.ContainerCreate(ctx, guardOptions(m.image))
	if cerrdefs.IsConflict(err) || (err != nil && strings.Contains(err.Error(), "already in use")) {
		// 另一個程序同時建好了。
		got, ierr := api.ContainerInspect(ctx, GuardName, client.ContainerInspectOptions{})
		if ierr != nil {
			return "", dockerErr(ierr)
		}
		if !running(&got.Container) {
			_, _ = api.ContainerStart(ctx, got.Container.ID, client.ContainerStartOptions{})
		}
		return got.Container.ID, nil
	}
	if err != nil {
		return "", fmt.Errorf("建立 %s 失敗：%w", GuardName, dockerErr(err))
	}
	if _, err := api.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("啟動 %s 失敗：%w", GuardName, dockerErr(err))
	}
	return res.ID, nil
}

// applyRules 把規則寫進 xbot-netguard 的狀態 volume，並在裡面執行 apply（成功＝規則已生效、就緒標記已寫）。
func applyRules(ctx context.Context, api hosts.DockerAPI, guardID, rules string) error {
	rulesV6 := buildRulesV6()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: guardRules, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(rules)), ModTime: time.Now()}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(rules)); err != nil {
		return err
	}
	// IPv6 規則（B.5：完全阻擋 IPv6）。
	if err := tw.WriteHeader(&tar.Header{Name: guardRulesV6, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(rulesV6)), ModTime: time.Now()}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(rulesV6)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := api.CopyToContainer(ctx, guardID, client.CopyToContainerOptions{DestinationPath: guardStateDir, Content: &buf}); err != nil {
		return fmt.Errorf("寫入網路防護規則失敗：%w", dockerErr(err))
	}
	// 剛建立的 xbot-netguard 可能還在開機：exec 失敗就稍等再試。
	var err error
	for range 4 {
		if err = boxfs.Run(ctx, api, guardID, "0", guardScript, "apply"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("套用網路防護規則失敗：%w", errors.Join(err, ctx.Err()))
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("套用網路防護規則失敗（主機要能用 iptables；rootless Docker 不支援）：%w", err)
}

// hostRules 列出這台主機上有白名單或「從家裡出去」的電腦（ensureNet 依它產生規則）。
func (m *Manager) hostRules(ctx context.Context, hostID string) ([]ruleBox, error) {
	rows, err := m.db.ListBoxes(ctx, hostID)
	if err != nil {
		return nil, err
	}
	var boxes []ruleBox
	for _, r := range rows {
		ip, err := netip.ParseAddr(r.NetIP)
		if err != nil {
			continue
		}
		allow, err := m.NetAllow(ctx, r.BotID)
		if err != nil {
			return nil, err
		}
		eg, err := m.EgressOn(ctx, r.BotID)
		if err != nil {
			return nil, err
		}
		if len(allow) > 0 || eg {
			boxes = append(boxes, ruleBox{IP: ip, Allow: allow, wantEgress: eg})
		}
	}
	return boxes, nil
}

// NetAllow 讀一個 Bot 的私有網路白名單。
func (m *Manager) NetAllow(ctx context.Context, botID string) ([]Allow, error) {
	var raw []string
	if _, err := m.db.GetSettingInto(ctx, NetAllowSetting(botID), &raw); err != nil {
		return nil, err
	}
	out := make([]Allow, 0, len(raw))
	for _, s := range raw {
		if a, err := ParseAllow(s); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// SetNetAllow 換掉一個 Bot 的私有網路白名單（空的＝全部關掉），電腦已經在某台主機上時立刻更新那台的規則。
func (m *Manager) SetNetAllow(ctx context.Context, botID string, list []string) ([]Allow, error) {
	allow, err := ParseAllowList(list)
	if err != nil {
		return nil, err
	}
	if _, err := m.db.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	strs := make([]string, len(allow))
	for i, a := range allow {
		strs[i] = a.String()
	}
	if len(strs) == 0 {
		err = m.db.DeleteSetting(ctx, NetAllowSetting(botID))
	} else {
		err = m.db.SetSetting(ctx, NetAllowSetting(botID), strs)
	}
	if err != nil {
		return nil, err
	}
	if m.noGuard {
		return allow, nil
	}
	b, err := m.db.GetBox(ctx, botID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (b.HostID == "" || b.NetIP == "")) {
		return allow, nil // 還沒有電腦：開機時套用
	}
	if err != nil {
		return allow, err
	}
	h, err := m.hosts.Get(b.HostID)
	if err != nil {
		return allow, nil // 主機離線：下次開機時套用
	}
	rules, err := m.hostRules(ctx, b.HostID)
	if err != nil {
		return allow, err
	}
	_, err = m.ensureNet(ctx, h, rules)
	return allow, err
}

// allocIP 挑 Bot 在 xbot-box 網路上的固定 IP：沿用原本的（還在子網路裡、沒被別人用），否則隨機挑一個沒人用的。
// 呼叫時要拿著 guard.alloc。
func (m *Manager) allocIP(ctx context.Context, h hosts.Host, hn *hostNet, b *store.Box) (netip.Addr, error) {
	used := map[netip.Addr]bool{hn.subnet.Addr(): true, hn.gateway: true, EgressAddr(hn.subnet): true}
	rows, err := m.db.ListBoxes(ctx, h.ID())
	if err != nil {
		return netip.Addr{}, err
	}
	for _, r := range rows {
		if ip, err := netip.ParseAddr(r.NetIP); err == nil && r.BotID != b.BotID {
			used[ip] = true
		}
	}
	// 同一台 Docker 上可能還有別的 XBot（開發版、測試）用同一個網路：連它們的電腦一起看（含沒開機的）。
	list, err := h.Docker().ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("network", NetworkName)})
	if err != nil {
		return netip.Addr{}, dockerErr(err)
	}
	for _, c := range list.Items {
		if slices.Contains(c.Names, "/"+ContainerName(b.BotID)) || c.NetworkSettings == nil {
			continue
		}
		if ep := c.NetworkSettings.Networks[NetworkName]; ep != nil {
			if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address.IsValid() {
				used[ep.IPAMConfig.IPv4Address] = true
			}
			if ep.IPAddress.IsValid() {
				used[ep.IPAddress] = true
			}
		}
	}
	if ip, err := netip.ParseAddr(b.NetIP); err == nil && hn.subnet.Contains(ip) && !used[ip] {
		return ip, nil
	}
	base := hn.subnet.Masked().Addr().As4()
	size := uint32(1) << (32 - hn.subnet.Bits())
	start := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	for range 4096 {
		off := 2 + rand.Uint32N(size-3) // 不用網路位址、閘道（.1）、廣播
		v := start + off
		ip := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
		if !used[ip] {
			return ip, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("%s 網路沒有空的 IP 了", NetworkName)
}
