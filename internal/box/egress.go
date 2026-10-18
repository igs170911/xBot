package box

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/boxfs"
	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// 「從家裡出去」（CL-7）：雲端主機上的電腦用家裡的網路連外（機房 IP 容易被蝦皮、Threads 要求驗證）。
//
// 做法（proxy 版）：每台主機一個 Tailscale 小容器 xbot-egress（官方 tailscale/tailscale 映像、userspace networking，
// 不需要 TUN、NET_ADMIN 或任何 capability），以 exit node 指向家裡那台電腦，並在 xbot-box 網路上的固定 IP 開一個
// HTTP proxy（limits.EgressProxyPort）。開了這個選項的 Bot，電腦帶著 HTTP(S)_PROXY 環境變數開機——Chromium（XFCE 下讀
// 環境變數的 proxy 設定）、Python、curl、Hermes 的工具連外都經過它。
//
// 網路防護（SEC-4）照舊：xbot-box 上電腦之間仍然互不相通，只對開了這個選項的電腦放行「→ 小容器的 proxy 埠」；
// 小容器本身也在 xbot-box 上，連不到主機與私有網段。經 exit node 出去的連線要用 Tailscale 的存取控制
// 限制只能上網際網路（autogroup:internet），否則家裡的區網也連得到——操作說明與介面都有寫。
//
// 限制：只有走 proxy 的程式才會從家裡出去（不認 proxy 環境變數的程式、UDP 與 DNS 查詢仍從主機直接出去）；
// 小容器要用 Tailscale 登入金鑰（auth key）加入你的 tailnet。

// 「從家裡出去」用到的 Docker 物件與標籤。
const (
	EgressName        = "xbot-egress"
	EgressStateVolume = "xbot-egress-state"
	// LabelEgress 是 xbot-egress 容器的設定摘要（exit node、金鑰、映像換了就重建）。
	LabelEgress = "xbot.egress"
	// LabelProxy 標在電腦的容器上：建立時有沒有帶「從家裡出去」的 proxy（和設定不同時下次開機前重建）。
	LabelProxy = "xbot.proxy"
)

// EgressConfig 是「從家裡出去」的設定。
type EgressConfig struct {
	// ExitNode 是家裡那台電腦在 tailnet 上的名稱或 100.x IP（要在 Tailscale 設成 exit node）。
	ExitNode string
	// AuthKey 是小容器加入 tailnet 用的 Tailscale 登入金鑰。
	AuthKey string
	// Hostname 是小容器在 tailnet 上的名稱（空白用 xbot-egress-<主機 id 前 8 碼>）。
	Hostname string
}

// EgressSource 提供「從家裡出去」的設定（computers.Service）。
type EgressSource interface {
	EgressConfig(ctx context.Context) (EgressConfig, error)
}

// EgressFunc 讓一個函式當 EgressSource 用。
type EgressFunc func(ctx context.Context) (EgressConfig, error)

// EgressConfig 實作 EgressSource。
func (f EgressFunc) EgressConfig(ctx context.Context) (EgressConfig, error) { return f(ctx) }

// ErrEgressNotSet 表示還沒設定 exit node 或登入金鑰。
var ErrEgressNotSet = errors.New("還沒設定「從家裡出去」：要填家裡那台電腦（exit node）與 Tailscale 登入金鑰")

// EgressSetting 是一個 Bot 有沒有開「從家裡出去」在 setting 表的 key（值：true）。
func EgressSetting(botID string) string { return "box.egress." + botID }

var exitNodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$`)

// ValidExitNode 檢查 exit node 的寫法（tailnet 機器名稱、MagicDNS 名稱或 100.x IP）。
func ValidExitNode(s string) error {
	if !exitNodeRe.MatchString(s) {
		return fmt.Errorf("exit node 要寫家裡那台電腦在 Tailscale 上的名稱或 100.x IP：%q", s)
	}
	return nil
}

// EgressAddr 是小容器在 xbot-box 網路上的固定 IP：子網路最後一個可用的位址（電腦不會分到它，見 allocIP）。
func EgressAddr(subnet netip.Prefix) netip.Addr {
	if !subnet.IsValid() || !subnet.Addr().Is4() {
		return netip.Addr{}
	}
	b := subnet.Masked().Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v += uint32(1)<<(32-subnet.Bits()) - 2
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// ProxyURL 是電腦要用的 HTTP proxy。
func ProxyURL(ip netip.Addr) string {
	return "http://" + netip.AddrPortFrom(ip, limits.EgressProxyPort).String()
}

// proxyEnv 是開了「從家裡出去」的電腦的環境變數（大小寫兩種都給；本機的 CDP、Hermes 不經 proxy）。
func proxyEnv(url string) []string {
	no := "localhost,127.0.0.1,::1"
	return []string{"HTTP_PROXY=" + url, "HTTPS_PROXY=" + url, "http_proxy=" + url, "https_proxy=" + url,
		"NO_PROXY=" + no, "no_proxy=" + no, "XBOT_EGRESS=home"}
}

// EgressOn 讀一個 Bot 有沒有開「從家裡出去」。
func (m *Manager) EgressOn(ctx context.Context, botID string) (bool, error) {
	var on bool
	if _, err := m.db.GetSettingInto(ctx, EgressSetting(botID), &on); err != nil {
		return false, err
	}
	return on, nil
}

// SetEgress 開關一個 Bot 的「從家裡出去」：存設定；電腦已經在某台主機上時，開的話先把那台的小容器準備好，
// 並更新網路防護規則。電腦的 proxy 環境變數要重建容器才會生效：回傳 rebuild=true 表示要重建（沒開機的下次開機前自動重建）。
func (m *Manager) SetEgress(ctx context.Context, botID string, on bool) (rebuild bool, err error) {
	if on && m.noGuard {
		return false, errors.New("「從家裡出去」要用 Bot 電腦的專用網路，網路防護關掉時（XBOT_NETGUARD=off）不能用")
	}
	if _, err := m.db.GetBot(ctx, botID); err != nil {
		return false, err
	}
	if on {
		if err := m.db.SetSetting(ctx, EgressSetting(botID), true); err != nil {
			return false, err
		}
	} else if err := m.db.DeleteSetting(ctx, EgressSetting(botID)); err != nil {
		return false, err
	}
	b, err := m.db.GetBox(ctx, botID)
	if err != nil || b.HostID == "" || b.ContainerID == "" {
		return false, nil // 還沒有電腦：建立時套用
	}
	h, err := m.hosts.Get(b.HostID)
	if err != nil {
		return true, nil // 主機離線：下次開機前套用
	}
	if on {
		boxes, err := m.hostRules(ctx, h.ID())
		if err != nil {
			return false, err
		}
		hn, err := m.ensureNet(ctx, h, boxes)
		if err != nil {
			return false, err
		}
		if _, err := m.ensureEgress(ctx, h, hn); err != nil {
			return false, err
		}
	} else if !m.noGuard && b.NetIP != "" {
		boxes, err := m.hostRules(ctx, h.ID())
		if err != nil {
			return false, err
		}
		if _, err := m.ensureNet(ctx, h, boxes); err != nil {
			return false, err
		}
	}
	c, err := inspect(ctx, h, botID)
	if err != nil || c == nil || c.Config == nil {
		return false, err
	}
	return (c.Config.Labels[LabelProxy] != "") != on, nil
}

// boxProxy 是 Bot 的電腦建立時要帶的 proxy（沒開「從家裡出去」時空白）；開了就確保主機上的小容器在跑。
// 要在 attach（網路已就緒）之後呼叫。
func (m *Manager) boxProxy(ctx context.Context, h hosts.Host, botID string) (string, error) {
	on, err := m.EgressOn(ctx, botID)
	if err != nil || !on {
		return "", err
	}
	if m.noGuard {
		return "", errors.New("「從家裡出去」要用 Bot 電腦的專用網路，網路防護關掉時（XBOT_NETGUARD=off）不能用")
	}
	ip, err := m.ensureEgress(ctx, h, m.guard.host(h.ID()))
	if err != nil {
		return "", err
	}
	return ProxyURL(ip), nil
}

func egressDigest(cfg EgressConfig, hostname string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{limits.EgressImage, cfg.ExitNode, cfg.AuthKey, hostname, "1"}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func egressHostname(cfg EgressConfig, hostID string) string {
	if cfg.Hostname != "" {
		return cfg.Hostname
	}
	id := hostID
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return "xbot-egress-" + strings.ToLower(id)
}

// egressOptions 是 xbot-egress 容器的設定。
func egressOptions(cfg EgressConfig, hostname, digest string, ip netip.Addr) client.ContainerCreateOptions {
	pids := int64(256)
	return client.ContainerCreateOptions{
		Name: EgressName,
		Config: &container.Config{
			Image: limits.EgressImage,
			Env: []string{
				"TS_AUTHKEY=" + cfg.AuthKey,
				"TS_STATE_DIR=/var/lib/tailscale",
				"TS_USERSPACE=true",
				"TS_ACCEPT_DNS=false",
				"TS_HOSTNAME=" + hostname,
				"TS_OUTBOUND_HTTP_PROXY_LISTEN=0.0.0.0:" + strconv.Itoa(limits.EgressProxyPort),
				"TS_EXTRA_ARGS=--exit-node=" + cfg.ExitNode,
			},
			Labels: map[string]string{LabelManaged: "true", LabelEgress: digest},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(NetworkName),
			CapDrop:       []string{"ALL"},
			SecurityOpt:   secOpt,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts:        []mount.Mount{{Type: mount.TypeVolume, Source: EgressStateVolume, Target: "/var/lib/tailscale"}},
			Resources:     container.Resources{Memory: limits.EgressMemoryBytes, PidsLimit: &pids},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			NetworkName: {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip}},
		}},
	}
}

// ensureEgress 確保主機上的 xbot-egress 在跑、設定是最新的（沒有映像時先拉），回傳它的 IP。hn 要已經有子網路（ensureNet 之後）。
func (m *Manager) ensureEgress(ctx context.Context, h hosts.Host, hn *hostNet) (netip.Addr, error) {
	if m.egress == nil {
		return netip.Addr{}, ErrEgressNotSet
	}
	cfg, err := m.egress.EgressConfig(ctx)
	if err != nil {
		return netip.Addr{}, err
	}
	if cfg.ExitNode == "" || cfg.AuthKey == "" {
		return netip.Addr{}, ErrEgressNotSet
	}
	hn.mu.Lock()
	subnet := hn.subnet
	hn.mu.Unlock()
	ip := EgressAddr(subnet)
	if !ip.IsValid() {
		return ip, errors.New("電腦的網路還沒準備好")
	}
	api := h.Docker()
	hostname := egressHostname(cfg, h.ID())
	digest := egressDigest(cfg, hostname)
	res, err := api.ContainerInspect(ctx, EgressName, client.ContainerInspectOptions{})
	switch {
	case err == nil:
		c := res.Container
		if c.Config == nil || c.Config.Labels[LabelManaged] != "true" {
			return ip, fmt.Errorf("主機上已經有一個不是 XBot 建的 %s 容器", EgressName)
		}
		if c.Config.Labels[LabelEgress] == digest {
			if !running(&c) {
				if _, err := api.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
					return ip, fmt.Errorf("啟動 %s 失敗：%w", EgressName, dockerErr(err))
				}
			}
			return ip, nil
		}
		if _, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return ip, dockerErr(err)
		}
	case !cerrdefs.IsNotFound(err):
		return ip, dockerErr(err)
	}
	if err := pullImage(ctx, api, limits.EgressImage); err != nil {
		return ip, err
	}
	if err := ensureVolume(ctx, api, EgressStateVolume, ""); err != nil {
		return ip, err
	}
	created, err := api.ContainerCreate(ctx, egressOptions(cfg, hostname, digest, ip))
	if err != nil {
		return ip, fmt.Errorf("建立 %s 失敗：%w", EgressName, dockerErr(err))
	}
	if _, err := api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return ip, fmt.Errorf("啟動 %s 失敗：%w", EgressName, dockerErr(err))
	}
	m.log.Info("從家裡出去：Tailscale 小容器已啟動", "host", h.ID(), "exit_node", cfg.ExitNode, "ip", ip)
	return ip, nil
}

func pullImage(ctx context.Context, api hosts.DockerAPI, ref string) error {
	if _, err := api.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return dockerErr(err)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.EgressPullTimeout)
	defer cancel()
	rd, err := api.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("主機拉不到 %s：%w", ref, dockerErr(err))
	}
	if err := rd.Wait(ctx); err != nil {
		return fmt.Errorf("主機拉不到 %s：%w", ref, err)
	}
	return nil
}

// EgressState 是一台主機上「從家裡出去」小容器的狀態。
type EgressState struct {
	HostID string `json:"host_id"`
	// Present：主機上有 xbot-egress 容器；Running：它在跑。
	Present bool   `json:"present"`
	Running bool   `json:"running"`
	IP      string `json:"ip,omitempty"`
	// Backend 是 Tailscale 的狀態（Running＝已登入可用；NeedsLogin＝金鑰無效或過期）。
	Backend string `json:"backend,omitempty" desc:"Tailscale 狀態：Running、NeedsLogin、Starting、Stopped"`
	// ExitNode 是正在用的 exit node；ExitNodeOnline 是它在不在線上。
	ExitNode       string `json:"exit_node,omitempty"`
	ExitNodeOnline bool   `json:"exit_node_online"`
	Error          string `json:"error,omitempty"`
}

// EgressStatus 讀一台主機上 xbot-egress 的狀態（在小容器裡執行 tailscale status --json）。
func (m *Manager) EgressStatus(ctx context.Context, hostID string) (EgressState, error) {
	st := EgressState{HostID: hostID}
	h, err := m.hosts.Get(hostID)
	if err != nil {
		return st, err
	}
	api := h.Docker()
	res, err := api.ContainerInspect(ctx, EgressName, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return st, nil
	}
	if err != nil {
		return st, dockerErr(err)
	}
	c := res.Container
	st.Present, st.Running = true, running(&c)
	if c.NetworkSettings != nil {
		if ep := c.NetworkSettings.Networks[NetworkName]; ep != nil && ep.IPAddress.IsValid() {
			st.IP = ep.IPAddress.String()
		}
	}
	if !st.Running {
		return st, nil
	}
	sctx, cancel := context.WithTimeout(ctx, limits.EgressStatusTimeout)
	defer cancel()
	d := &boxfs.Docker{API: api, ID: c.ID, Running: true}
	out, err := d.Output(sctx, "0", 4<<20, "tailscale", "status", "--json", "--peers=true")
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	parseTailscaleStatus(out, &st)
	return st, nil
}

// parseTailscaleStatus 從 tailscale status --json 取出狀態與 exit node。
func parseTailscaleStatus(raw []byte, st *EgressState) {
	var s struct {
		BackendState   string
		ExitNodeStatus *struct {
			Online       bool
			TailscaleIPs []string
		}
		Peer map[string]struct {
			HostName     string
			DNSName      string
			ExitNode     bool
			Online       bool
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		st.Error = "看不懂 tailscale status：" + err.Error()
		return
	}
	st.Backend = s.BackendState
	if s.ExitNodeStatus != nil {
		st.ExitNodeOnline = s.ExitNodeStatus.Online
	}
	for _, p := range s.Peer {
		if p.ExitNode {
			st.ExitNode = p.HostName
			st.ExitNodeOnline = p.Online
		}
	}
}

// RemoveEgress 刪掉主機上的 xbot-egress（沒有 Bot 要用時）。登入狀態 volume 留著，之後再開不用重新登入。
func (m *Manager) RemoveEgress(ctx context.Context, hostID string) error {
	h, err := m.hosts.Get(hostID)
	if err != nil {
		return err
	}
	if _, err := h.Docker().ContainerRemove(ctx, EgressName, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return dockerErr(err)
	}
	return nil
}
