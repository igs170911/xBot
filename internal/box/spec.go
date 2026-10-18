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
	// LabelLimits 記下建立容器時的資源設定；設定變了就要重建（BX-11，B13 使用）。
	LabelLimits = "xbot.limits"

	// NoVNCPort 是容器內的 noVNC（websockify）埠。
	NoVNCPort = "6080/tcp"
	// HermesPort 是容器內的 Hermes 主機（hermes_host.py）埠。
	HermesPort = "8642/tcp"

	// 容器內的掛載點：引擎狀態、Bot 的家目錄（瀏覽器設定檔與登入）、工作資料夾、XBot 的 product.sock。
	HermesDir    = "/hermes"
	HomeDir      = "/home/box"
	WorkspaceDir = "/workspace"
	IPCDir       = "/product-ipc"

	// BotUID、BotGID 是容器內 Bot 使用者的身分（box/start-bot.sh 用 XBOT_UID／XBOT_GID 降權）。
	// LazyBoy 用服務本身的 UID，因為它掛的是主機資料夾；XBot 用具名 volume，檔案只在容器裡被讀寫，
	// 固定一個值，遠端主機也一樣。
	BotUID = 1000
	BotGID = 1000
)

// Volumes 是一台電腦的三個持久 volume（BX-3），重建容器時保留。
type Volumes struct{ Home, Workspace, Hermes string }

// ContainerName 回傳 Bot 的容器名稱。
func ContainerName(botID string) string { return NamePrefix + botID }

// VolumeNames 回傳 Bot 的三個 volume 名稱：xbot-box-<id>-home／-workspace／-hermes。
func VolumeNames(botID string) Volumes {
	p := ContainerName(botID) + "-"
	return Volumes{Home: p + "home", Workspace: p + "workspace", Hermes: p + "hermes"}
}

func (v Volumes) list() []string { return []string{v.Home, v.Workspace, v.Hermes} }

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
// 先丟掉所有 capability，再只補 box/start-bot.sh 開機時以 root 身分要做的事：
//   - CHOWN、DAC_OVERRIDE、FOWNER：把三個 volume 交給 Bot 使用者（chown -R）、建立 Bot 使用者（寫 /etc/passwd 等）、
//     調整 /tmp/.X11-unix 權限。
//   - SETUID、SETGID：setpriv 降權成 XBOT_UID／XBOT_GID。
//   - KILL：PID 1 的 tini 以 root 身分把 docker stop 的 SIGTERM 轉給已降權的子行程；少了它，
//     關機時 Chromium 沒機會存狀態，只能等逾時被強制結束。
//
// 降權之後 Bot 的行程沒有任何 capability；no-new-privileges 讓 sudo 和 setuid 程式無法再提權（SEC-3「容器內 sudo 預設關閉」）。
var (
	capDrop = []string{"ALL"}
	capAdd  = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "KILL", "SETGID", "SETUID"}
	secOpt  = []string{"no-new-privileges:true"}
)

// createOptions 組出建立容器的完整參數。對應 LazyBoy server/computers.py start() 的 docker run，
// 差別：持久資料改用具名 volume、加上 SEC-3 強化、不加 host.docker.internal（SEC-4：容器不需要直接連主機，
// 模型與 XBot 都經 /product-ipc 的 socket）。
func createOptions(botID, image, ipcDir string, lim Limits) client.ContainerCreateOptions {
	vols := VolumeNames(botID)
	loopback := netip.MustParseAddr("127.0.0.1")
	novnc, hermes := network.MustParsePort(NoVNCPort), network.MustParsePort(HermesPort)
	period := limits.BoxCPUPeriod.Microseconds()
	stop := int(limits.BoxStopTimeout.Seconds())
	pids := lim.Pids
	volume := func(src, dst string) mount.Mount {
		return mount.Mount{Type: mount.TypeVolume, Source: src, Target: dst}
	}
	return client.ContainerCreateOptions{
		Name: ContainerName(botID),
		Config: &container.Config{
			Image: image,
			Env: []string{
				"XBOT_UID=" + strconv.Itoa(BotUID),
				"XBOT_GID=" + strconv.Itoa(BotGID),
			},
			Labels: map[string]string{
				LabelBot:     botID,
				LabelManaged: "true",
				LabelLimits:  lim.signature(),
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
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: ipcDir, Target: IPCDir, ReadOnly: true},
				volume(vols.Hermes, HermesDir),
				volume(vols.Home, HomeDir),
				volume(vols.Workspace, WorkspaceDir),
			},
			ShmSize:     lim.ShmMB << 20,
			CapDrop:     capDrop,
			CapAdd:      capAdd,
			SecurityOpt: secOpt,
			Resources: container.Resources{
				Memory:    lim.MemoryMB << 20,
				CPUPeriod: period,
				CPUQuota:  int64(lim.CPUs) * period,
				PidsLimit: &pids,
			},
		},
	}
}
