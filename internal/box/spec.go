package box

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/limits"
)

// 容器與 volume 的命名、標籤、埠與掛載點。掛載的配置沿用 LazyBoy server/computers.py start()。
const (
	// NamePrefix：每個 Bot 一台容器 xbot-box-<id>（BX-1）。
	NamePrefix = "xbot-box-"

	LabelBot     = "xbot.bot"
	LabelManaged = "xbot.managed"
	// LabelLimits 記下建立容器時的資源設定；設定變了就要重建（BX-11）：沒開機時下次開機前自動重建。
	LabelLimits = "xbot.limits"
	// LabelSpec 是容器設定（強化、網路）的版本，和 specRevision 不同的舊容器下次開機前自動重建（資料保留）。
	LabelSpec = "xbot.spec"
	// specRevision：B13 起以 Bot 使用者、唯讀根目錄、沒有 capability 開機，接 xbot-box 網路。
	specRevision = "b13"

	// NoVNCPort 是容器內的 noVNC（websockify）埠。
	NoVNCPort = "6080/tcp"
	// HermesPort 是容器內的 Hermes 主機（hermes_host.py）埠。
	HermesPort = "8642/tcp"

	// 容器內的掛載點：引擎狀態、Bot 的家目錄（瀏覽器設定檔與登入）、工作資料夾、XBot 的 product.sock、
	// 訂閱 CLI 的憑證（EN-2b；唯讀根檔案系統之下改放在自己的 volume，見 internal/engines）、網路防護的就緒標記（SEC-4）。
	HermesDir    = "/hermes"
	HomeDir      = "/home/box"
	WorkspaceDir = "/workspace"
	IPCDir       = "/product-ipc"
	CredDir      = "/opt/xbot/cred"
	NetguardDir  = "/opt/xbot/netguard"

	// BotUID、BotGID 是容器內 Bot 使用者的身分（box/Dockerfile 建好的 bot 使用者，容器直接以它開機）。
	// LazyBoy 用服務本身的 UID，因為它掛的是主機資料夾；XBot 用具名 volume，檔案只在容器裡被讀寫，
	// 固定一個值，遠端主機也一樣。
	BotUID = 1000
	BotGID = 1000
)

// Volumes 是一台電腦的持久 volume（BX-3），重建容器時保留：home／workspace／hermes，加上訂閱憑證（Cred）。
type Volumes struct{ Home, Workspace, Hermes, Cred string }

// ContainerName 回傳 Bot 的容器名稱。
func ContainerName(botID string) string { return NamePrefix + botID }

// VolumeNames 回傳 Bot 的 volume 名稱：xbot-box-<id>-home／-workspace／-hermes／-cred。
func VolumeNames(botID string) Volumes {
	p := ContainerName(botID) + "-"
	return Volumes{Home: p + "home", Workspace: p + "workspace", Hermes: p + "hermes", Cred: p + "cred"}
}

func (v Volumes) list() []string { return []string{v.Home, v.Workspace, v.Hermes, v.Cred} }

// Limits 是一台電腦的資源限制（BX-11）。存在 box.limits，沒寫的欄位用 internal/limits 的預設值。
type Limits struct {
	CPUs     int   `json:"cpus,omitempty"`
	MemoryMB int64 `json:"memory_mb,omitempty"`
	ShmMB    int64 `json:"shm_mb,omitempty"`
	Pids     int64 `json:"pids,omitempty"`
}

// DefaultLimits 是 PRD BX-11 的預設：2 CPU／3 GB／shm 512 MB，加上 SEC-3 的行程數上限。
func DefaultLimits() Limits {
	return Limits{CPUs: limits.BoxCPUs, MemoryMB: limits.BoxMemoryBytes >> 20, ShmMB: limits.BoxShmBytes >> 20, Pids: limits.BoxPidsLimit}
}

// parseLimits 讀 box.limits，缺的欄位補預設值。
func parseLimits(raw json.RawMessage) Limits {
	l := DefaultLimits()
	var got Limits
	if len(raw) > 0 && json.Unmarshal(raw, &got) == nil {
		if got.CPUs > 0 {
			l.CPUs = got.CPUs
		}
		if got.MemoryMB > 0 {
			l.MemoryMB = got.MemoryMB
		}
		if got.ShmMB > 0 {
			l.ShmMB = got.ShmMB
		}
		if got.Pids > 0 {
			l.Pids = got.Pids
		}
	}
	return l
}

func (l Limits) json() json.RawMessage {
	b, _ := json.Marshal(l)
	return b
}

// signature 是寫在容器標籤上的資源設定摘要。
func (l Limits) signature() string {
	return fmt.Sprintf("cpu-%dx%dus,mem-%dm,shm-%dm,pids-%d", l.CPUs, limits.BoxCPUPeriod.Microseconds(), l.MemoryMB, l.ShmMB, l.Pids)
}

// 容器強化（SEC-3）。
//
// 容器直接以 Bot 使用者（1000:1000，映像裡建好）開機，丟掉所有 capability、一個也不補回；no-new-privileges 讓 setuid
// 程式無法提權（映像裡也沒有 sudo，SEC-3「容器內 sudo 預設關閉」）。根檔案系統唯讀，可寫的只有：
//   - tmpfs：/tmp（桌面的 socket、記錄檔、暫存）、/run、/var/tmp；
//   - volume：/home/box、/workspace、/hermes、/opt/xbot/cred。
//
// B02 的做法（以 root 開機，start-bot.sh 寫 /etc/passwd、chown volume、再 setpriv 降權）需要 CHOWN、DAC_OVERRIDE、
// FOWNER、KILL、SETUID、SETGID 六個 capability；改成映像裡建好使用者、volume 一開始就屬於它之後全部不需要了。
var (
	capDrop = []string{"ALL"}
	secOpt  = []string{"no-new-privileges:true"}
)

// tmpfs 是唯讀根檔案系統之下可寫的地方（大小見 internal/limits）。/tmp 要能執行（Playwright、uv、Bot 自己的腳本）。
func tmpfs() map[string]string {
	owner := fmt.Sprintf("uid=%d,gid=%d", BotUID, BotGID)
	return map[string]string{
		"/tmp":     fmt.Sprintf("rw,exec,nosuid,nodev,mode=1777,size=%d", limits.BoxTmpfsTmpBytes),
		"/run":     fmt.Sprintf("rw,noexec,nosuid,nodev,mode=755,%s,size=%d", owner, limits.BoxTmpfsRunBytes),
		"/var/tmp": fmt.Sprintf("rw,noexec,nosuid,nodev,mode=1777,size=%d", limits.BoxTmpfsVarTmpBytes),
	}
}

// netAttach 是電腦接上的網路（SEC-4）：xbot-box 網路上的固定 IP，以及網路防護的就緒標記 volume。
type netAttach struct {
	Network     string
	IP          netip.Addr
	ReadyVolume string
}

// spec 是建立一台電腦需要的設定。
type spec struct {
	BotID, Image, IPCDir string
	// Revision 是寫在 LabelSpec 的版本；空白用 specRevision。
	Revision string
	Limits   Limits
	// Net 是 nil 時接 Docker 預設的 bridge 網路（關掉網路防護時）。
	Net *netAttach
}

// createOptions 組出建立容器的完整參數。對應 LazyBoy server/computers.py start() 的 docker run，
// 差別：持久資料改用具名 volume、加上 SEC-3 強化、接上有網路防護的 xbot-box 網路、不加 host.docker.internal
// （SEC-4：容器不需要直接連主機，模型與 XBot 都經 /product-ipc 的 socket）。
func createOptions(sp spec) client.ContainerCreateOptions {
	botID, lim, rev := sp.BotID, sp.Limits, sp.Revision
	if rev == "" {
		rev = specRevision
	}
	vols := VolumeNames(botID)
	loopback := netip.MustParseAddr("127.0.0.1")
	novnc, hermes := network.MustParsePort(NoVNCPort), network.MustParsePort(HermesPort)
	period := limits.BoxCPUPeriod.Microseconds()
	stop := int(limits.BoxStopTimeout.Seconds())
	pids := lim.Pids
	volume := func(src, dst string) mount.Mount {
		return mount.Mount{Type: mount.TypeVolume, Source: src, Target: dst}
	}
	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: sp.IPCDir, Target: IPCDir, ReadOnly: true},
		volume(vols.Hermes, HermesDir),
		volume(vols.Home, HomeDir),
		volume(vols.Workspace, WorkspaceDir),
		volume(vols.Cred, CredDir),
	}
	o := client.ContainerCreateOptions{
		Name: ContainerName(botID),
		Config: &container.Config{
			Image: sp.Image,
			User:  strconv.Itoa(BotUID) + ":" + strconv.Itoa(BotGID),
			Env: []string{
				"XBOT_UID=" + strconv.Itoa(BotUID),
				"XBOT_GID=" + strconv.Itoa(BotGID),
			},
			Labels: map[string]string{
				LabelBot:     botID,
				LabelManaged: "true",
				LabelLimits:  lim.signature(),
				LabelSpec:    rev,
			},
			ExposedPorts: network.PortSet{novnc: {}, hermes: {}},
			StopTimeout:  &stop,
		},
		HostConfig: &container.HostConfig{
			// 只發布在主機的 127.0.0.1，埠號由 Docker 隨機挑；只有 XBot 服務連得到。
			PortBindings: network.PortMap{
				novnc:  {{HostIP: loopback, HostPort: ""}},
				hermes: {{HostIP: loopback, HostPort: ""}},
			},
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			ShmSize:        lim.ShmMB << 20,
			CapDrop:        capDrop,
			SecurityOpt:    secOpt,
			ReadonlyRootfs: true,
			Tmpfs:          tmpfs(),
			Resources: container.Resources{
				Memory:    lim.MemoryMB << 20,
				CPUPeriod: period,
				CPUQuota:  int64(lim.CPUs) * period,
				PidsLimit: &pids,
			},
		},
	}
	if n := sp.Net; n != nil {
		o.HostConfig.NetworkMode = container.NetworkMode(n.Network)
		o.NetworkingConfig = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			n.Network: {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: n.IP}},
		}}
		// 網路防護的就緒標記（唯讀）：start-bot.sh 等規則在這次開機生效後才開始（主機重開機時不會先連上網）。
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: n.ReadyVolume, Target: NetguardDir, ReadOnly: true})
	}
	o.HostConfig.Mounts = mounts
	return o
}
