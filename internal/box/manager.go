package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// DefaultImage 是 make box 建出的 Bot 電腦映像。
const DefaultImage = "xbot-box:dev"

// box.state 的值。容器存在但沒在跑叫「sleeping」（建好還沒開機、或被停止）。
const (
	StateNone     = "none"
	StateCreating = "creating"
	StateRunning  = "running"
	StateSleeping = "sleeping"
	StateUpdating = "updating"
	StateError    = "error"
)

// Hosts 依 host.id 找到主機；id 空白時是新 Bot 的預設主機。*hosts.Registry 符合。
type Hosts interface {
	Get(id string) (hosts.Host, error)
}

// Options 是 Manager 的設定。
type Options struct {
	// Image 是 Bot 電腦映像，空白時用 DefaultImage。
	Image string
	// Prepare 在每次開機前（容器已建立，可能還沒開機）呼叫，用來寫入引擎設定、開好 product.sock（E02）。
	// 已經在跑時也會呼叫，讓設定保持最新。回傳錯誤時不開機。
	Prepare PrepareFunc
	// NoNetGuard 關掉網路防護（SEC-4）：電腦接 Docker 預設的 bridge 網路，連得到主機與私有網段。
	// 只給不能用 iptables 的環境（XBOT_NETGUARD=off），不建議。
	NoNetGuard bool
	// Notify 在電腦狀態變了時呼叫（開機、休眠、重建、刪除、錯誤）；可為 nil。不能在裡面呼叫 Manager（會卡住）。
	Notify func(Status)
	// Release 在電腦連資料一起刪除（purge）前呼叫，用來關掉這個 Bot 在服務這邊的 product.sock；可為 nil。
	Release func(botID string)
	Logger  *slog.Logger
}

// PrepareFunc 準備一台電腦開機需要的東西。
type PrepareFunc func(ctx context.Context, h hosts.Host, botID, containerID string) error

// Manager 管理每個 Bot 的電腦（容器）：建立、開機、停止、刪除、重建，狀態寫回 box 表。
// 同一個 Bot 的操作依序執行；不同 Bot 互不影響。
type Manager struct {
	db      *store.DB
	hosts   Hosts
	image   string
	prepare PrepareFunc
	noGuard bool
	notify  func(Status)
	release func(botID string)
	log     *slog.Logger
	http    *http.Client
	guard   netguard

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	holds map[string]int // 有人正在看畫面的電腦（B14）：不休眠
}

// New 建立 Manager。
func New(db *store.DB, hs Hosts, opts Options) *Manager {
	if opts.Image == "" {
		opts.Image = DefaultImage
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{db: db, hosts: hs, image: opts.Image, prepare: opts.Prepare, noGuard: opts.NoNetGuard, notify: opts.Notify,
		release: opts.Release, log: opts.Logger, http: &http.Client{Timeout: limits.BoxProbeTimeout},
		locks: map[string]*sync.Mutex{}, holds: map[string]int{}}
}

// Image 回傳 Bot 電腦映像的名稱。
func (m *Manager) Image() string { return m.image }

// Status 是一台電腦目前的狀態。
type Status struct {
	BotID       string `json:"bot_id"`
	HostID      string `json:"host_id"`
	ContainerID string `json:"container_id"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
	Running     bool   `json:"running"`
	// NoVNCPort、HermesPort 是發布在主機 127.0.0.1 的埠（沒在跑時為 0）；NoVNCAddr、HermesAddr 是服務連過去的位址。
	NoVNCPort  int    `json:"novnc_port"`
	HermesPort int    `json:"hermes_port"`
	NoVNCAddr  string `json:"novnc_addr,omitempty"`
	HermesAddr string `json:"hermes_addr,omitempty"`
}

// ErrNameTaken 表示 xbot-box-<id> 這個名稱被不是這個 Bot 的容器佔用了。
var ErrNameTaken = errors.New("電腦名稱已被其他容器使用")

func (m *Manager) lock(botID string) func() {
	m.mu.Lock()
	l, ok := m.locks[botID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[botID] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// load 讀 box 列（沒有就是 state=none 的新列）與它所在的主機。
func (m *Manager) load(ctx context.Context, botID string) (store.Box, hosts.Host, error) {
	b, err := m.db.GetBox(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		if _, err := m.db.GetBot(ctx, botID); err != nil {
			return b, nil, err
		}
		b, err = store.Box{BotID: botID, State: StateNone}, nil
	}
	if err != nil {
		return b, nil, err
	}
	if b.HostID == "" {
		// 還沒有電腦：放在 Bot 指定（或自動擺放記下）的主機（CL-1、CL-4）；都沒有時是預設主機。
		bot, err := m.db.GetBot(ctx, botID)
		if err != nil {
			return b, nil, err
		}
		b.HostID = bot.HostID
	}
	h, err := m.hosts.Get(b.HostID)
	if err != nil {
		return b, nil, err
	}
	b.HostID = h.ID()
	return b, h, nil
}

// inspect 回傳 Bot 的容器；不存在時回傳 nil。
func inspect(ctx context.Context, h hosts.Host, botID string) (*container.InspectResponse, error) {
	res, err := h.Docker().ContainerInspect(ctx, ContainerName(botID), client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, dockerErr(err)
	}
	c := res.Container
	if c.Config == nil || c.Config.Labels[LabelBot] != botID {
		return nil, fmt.Errorf("%w：%s", ErrNameTaken, ContainerName(botID))
	}
	return &c, nil
}

func dockerErr(err error) error {
	if client.IsErrConnectionFailed(err) {
		return fmt.Errorf("無法連線到 Docker，請確認 Docker 有在執行：%w", err)
	}
	return err
}

func running(c *container.InspectResponse) bool { return c != nil && c.State != nil && c.State.Running }

func hostPort(c *container.InspectResponse, port string) int {
	if c == nil || c.NetworkSettings == nil {
		return 0
	}
	for _, b := range c.NetworkSettings.Ports[network.MustParsePort(port)] {
		if p, err := strconv.Atoi(b.HostPort); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// sync 依容器實際狀態更新 box 列並寫回資料庫。發布的埠換了（停止、重建、在 XBot 之外重開）時，
// 放掉遠端主機上舊埠的本機轉發；狀態變了時通知（Options.Notify）。
func (m *Manager) sync(ctx context.Context, b store.Box, h hosts.Host, c *container.InspectResponse) (Status, error) {
	prev, oldPorts := b.State, []int{b.NoVNCPort, b.HermesPort}
	if old, err := m.db.GetBox(ctx, b.BotID); err == nil {
		prev = old.State
		oldPorts = append(oldPorts, old.NoVNCPort, old.HermesPort)
	}
	switch {
	case c == nil:
		b.ContainerID, b.State = "", StateNone
	case running(c):
		b.ContainerID, b.State, b.Error = c.ID, StateRunning, ""
	default:
		b.ContainerID, b.State = c.ID, StateSleeping
	}
	b.NoVNCPort, b.HermesPort = hostPort(c, NoVNCPort), hostPort(c, HermesPort)
	if err := m.db.UpsertBox(ctx, b); err != nil {
		return Status{}, err
	}
	m.releasePorts(ctx, h, b.BotID, oldPorts, b.NoVNCPort, b.HermesPort)
	s := status(b, h, running(c))
	if prev != b.State && m.notify != nil {
		m.notify(s)
	}
	return s, nil
}

// releasePorts 放掉不再使用的舊埠在遠端主機上的本機轉發（hosts.SSH.ReleaseAddr；本機主機不用）。
// 同一台主機上別的電腦剛好拿到同一個埠時不放。
func (m *Manager) releasePorts(ctx context.Context, h hosts.Host, botID string, old []int, keep ...int) {
	r, ok := h.(interface{ ReleaseAddr(hostPort int) })
	if !ok {
		return
	}
	var stale []int
	for _, p := range old {
		if p > 0 && !slices.Contains(keep, p) && !slices.Contains(stale, p) {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		return
	}
	rows, err := m.db.ListBoxes(ctx, h.ID())
	if err != nil {
		return
	}
	for _, p := range stale {
		inUse := slices.ContainsFunc(rows, func(o store.Box) bool {
			return o.BotID != botID && (o.NoVNCPort == p || o.HermesPort == p)
		})
		if !inUse {
			r.ReleaseAddr(p)
		}
	}
}

func status(b store.Box, h hosts.Host, run bool) Status {
	s := Status{BotID: b.BotID, HostID: b.HostID, ContainerID: b.ContainerID, State: b.State, Error: b.Error, Running: run,
		NoVNCPort: b.NoVNCPort, HermesPort: b.HermesPort}
	if b.NoVNCPort > 0 {
		s.NoVNCAddr = h.Addr(b.NoVNCPort)
	}
	if b.HermesPort > 0 {
		s.HermesAddr = h.Addr(b.HermesPort)
	}
	return s
}

// fail 把錯誤記在 box 列上（state=error），回傳原本的錯誤。
func (m *Manager) fail(ctx context.Context, b store.Box, err error) error {
	b.State, b.Error = StateError, err.Error()
	if werr := m.db.UpsertBox(context.WithoutCancel(ctx), b); werr != nil {
		return errors.Join(err, werr)
	}
	if m.notify != nil {
		m.notify(Status{BotID: b.BotID, HostID: b.HostID, ContainerID: b.ContainerID, State: b.State, Error: b.Error})
	}
	return err
}

// Status 讀電腦的實際狀態，並同步回 box 表。
func (m *Manager) Status(ctx context.Context, botID string) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return Status{}, err
	}
	return m.sync(ctx, b, h, c)
}

// Create 建立電腦（volume 與容器），但不開機。已經存在就直接回傳狀態。
func (m *Manager) Create(ctx context.Context, botID string) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	c, err := m.ensure(ctx, &b, h)
	if err != nil {
		return Status{}, err
	}
	return m.sync(ctx, b, h, c)
}

// ensure 在容器不存在時建立它（BX-1：第一次需要時才建）。
// 建好時 b 會帶上新容器的 ID、映像版本與資源設定。
func (m *Manager) ensure(ctx context.Context, b *store.Box, h hosts.Host) (*container.InspectResponse, error) {
	c, err := inspect(ctx, h, b.BotID)
	if err != nil || c != nil {
		return c, err
	}
	if b.State != StateUpdating {
		b.State = StateCreating
	}
	b.Error, b.ContainerID = "", ""
	if err := m.db.UpsertBox(ctx, *b); err != nil {
		return nil, err
	}
	c, err = m.create(ctx, b, h)
	if err != nil {
		return nil, m.fail(ctx, *b, err)
	}
	return c, nil
}

func (m *Manager) create(ctx context.Context, b *store.Box, h hosts.Host) (*container.InspectResponse, error) {
	api := h.Docker()
	img, err := api.ImageInspect(ctx, m.image)
	if cerrdefs.IsNotFound(err) {
		if h.Kind() != "local" {
			return nil, fmt.Errorf("這台主機上沒有 Bot 電腦映像 %s：到「設定 → 電腦與裝置」重新檢查這台主機（會把映像傳過去）", m.image)
		}
		return nil, fmt.Errorf("找不到 Bot 電腦映像 %s，請先執行 make box", m.image)
	}
	if err != nil {
		return nil, dockerErr(err)
	}
	for _, name := range VolumeNames(b.BotID).list() {
		if err := ensureVolume(ctx, api, name, b.BotID); err != nil {
			return nil, err
		}
	}
	ipc, err := h.PrepareIPC(b.BotID)
	if err != nil {
		return nil, err
	}
	lim := parseLimits(b.Limits)
	sp := spec{BotID: b.BotID, Image: m.image, IPCDir: ipc, Limits: lim, Revision: m.specRevision()}
	if m.noGuard {
		b.NetIP = ""
	} else {
		if sp.Net, err = m.attach(ctx, h, b); err != nil {
			return nil, err
		}
	}
	res, err := api.ContainerCreate(ctx, createOptions(sp))
	if err != nil {
		return nil, fmt.Errorf("建立 Bot 電腦失敗：%w", dockerErr(err))
	}
	b.ContainerID, b.ImageRev, b.Limits = res.ID, img.ID, lim.json()
	c, err := inspect(ctx, h, b.BotID)
	if err == nil && c == nil {
		err = errors.New("建立 Bot 電腦後找不到容器（可能被其他程式刪除）")
	}
	return c, err
}

// attach 確保主機上的 xbot-box 網路與網路防護，挑好 Bot 的固定 IP 並寫進 box 列（SEC-4）。
func (m *Manager) attach(ctx context.Context, h hosts.Host, b *store.Box) (*netAttach, error) {
	rules, err := m.hostRules(ctx, h.ID())
	if err != nil {
		return nil, err
	}
	hn, err := m.ensureNet(ctx, h, rules)
	if err != nil {
		return nil, err
	}
	m.guard.alloc.Lock()
	defer m.guard.alloc.Unlock()
	ip, err := m.allocIP(ctx, h, hn, b)
	if err != nil {
		return nil, err
	}
	b.NetIP = ip.String()
	if err := m.db.UpsertBox(ctx, *b); err != nil {
		return nil, err
	}
	return &netAttach{Network: NetworkName, IP: ip, ReadyVolume: GuardReadyVolume}, nil
}

func ensureVolume(ctx context.Context, api hosts.DockerAPI, name, botID string) error {
	_, err := api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return dockerErr(err)
	}
	labels := map[string]string{LabelManaged: "true"}
	if botID != "" {
		labels[LabelBot] = botID
	}
	_, err = api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels})
	if err != nil {
		return fmt.Errorf("建立資料區 %s 失敗：%w", name, dockerErr(err))
	}
	return nil
}

// Start 開機；電腦還不存在時先建立。只等容器開始執行，桌面與 Hermes 就緒請用 WaitReady。
func (m *Manager) Start(ctx context.Context, botID string) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	return m.start(ctx, b, h)
}

func (m *Manager) start(ctx context.Context, b store.Box, h hosts.Host) (Status, error) {
	c, err := m.ensure(ctx, &b, h)
	if err != nil {
		return Status{}, err
	}
	if c != nil && !running(c) && m.stale(c, b) {
		// 容器是用舊的強化設定（SEC-3／SEC-4）或舊的資源限制（BX-11）建的：開機前重建，資料區保留。
		if _, err := h.Docker().ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return Status{}, m.fail(ctx, b, fmt.Errorf("重建 Bot 電腦失敗：%w", dockerErr(err)))
		}
		b.ContainerID = ""
		if c, err = m.ensure(ctx, &b, h); err != nil {
			return Status{}, err
		}
	}
	if !running(c) && !m.noGuard && b.NetIP != "" {
		// 開機前確認這台主機的網路防護還在、規則是最新的（含這台電腦的白名單）。
		rules, err := m.hostRules(ctx, h.ID())
		if err == nil {
			_, err = m.ensureNet(ctx, h, rules)
		}
		if err != nil {
			return Status{}, m.fail(ctx, b, err)
		}
	}
	// 每次開機前都確保 product.sock 的資料夾就緒：遠端主機在服務重啟或重連後要重新建立反向轉發（B11）。
	if _, err := h.PrepareIPC(b.BotID); err != nil {
		return Status{}, m.fail(ctx, b, fmt.Errorf("準備 Bot 電腦失敗：%w", err))
	}
	if m.prepare != nil {
		if err := m.prepare(ctx, h, b.BotID, c.ID); err != nil {
			return Status{}, m.fail(ctx, b, fmt.Errorf("準備 Bot 電腦失敗：%w", err))
		}
	}
	if !running(c) {
		if _, err := h.Docker().ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
			return Status{}, m.fail(ctx, b, fmt.Errorf("開機失敗：%w", dockerErr(err)))
		}
		if c, err = inspect(ctx, h, b.BotID); err != nil {
			return Status{}, err
		}
	}
	b.LastActiveAt = time.Now().UnixMilli()
	return m.sync(ctx, b, h, c)
}

// Stop 停止電腦（休眠）。容器與資料都保留。
func (m *Manager) Stop(ctx context.Context, botID string) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return Status{}, err
	}
	if running(c) {
		if err := stop(ctx, h, c.ID); err != nil {
			return Status{}, m.fail(ctx, b, err)
		}
		if c, err = inspect(ctx, h, botID); err != nil {
			return Status{}, err
		}
	}
	return m.sync(ctx, b, h, c)
}

func stop(ctx context.Context, h hosts.Host, id string) error {
	t := int(limits.BoxStopTimeout.Seconds())
	if _, err := h.Docker().ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &t}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("停止 Bot 電腦失敗：%w", dockerErr(err))
	}
	return nil
}

// Remove 刪除容器。purge 為 false 時 home／workspace／hermes 三個 volume 保留，之後再建立會接回原本的資料；
// purge 為 true 時連 volume 一起刪除（無法復原，呼叫端要先取得使用者核准）。
func (m *Manager) Remove(ctx context.Context, botID string, purge bool) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return Status{}, err
	}
	api := h.Docker()
	if c != nil {
		if _, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return Status{}, m.fail(ctx, b, fmt.Errorf("刪除 Bot 電腦失敗：%w", dockerErr(err)))
		}
	}
	if purge {
		for _, name := range VolumeNames(botID).list() {
			if _, err := api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
				return Status{}, m.fail(ctx, b, fmt.Errorf("刪除資料區 %s 失敗：%w", name, dockerErr(err)))
			}
		}
	}
	if purge {
		// 連資料一起刪：關掉服務這邊的 product.sock，停掉遠端主機的反向轉發並刪掉主機上的 bots/<id> 資料夾。
		if m.release != nil {
			m.release(botID)
		}
		if f, ok := h.(interface {
			ForgetBot(ctx context.Context, botID string) error
		}); ok {
			if err := f.ForgetBot(ctx, botID); err != nil {
				m.log.Warn("刪除主機上 Bot 的資料夾失敗", "bot", botID, "host", h.ID(), "err", err)
			}
		} else if r, ok := h.(interface{ ReleaseIPC(botID string) }); ok {
			r.ReleaseIPC(botID)
		}
		b.NetIP = ""
	}
	b.Error = ""
	s, err := m.sync(ctx, b, h, nil)
	if err == nil && purge {
		// 白名單規則裡不能留著這台已經刪掉的電腦的 IP（之後可能分給別台）。
		m.dropAllow(ctx, h, botID)
	}
	return s, err
}

// dropAllow 刪掉 Bot 的私有網路白名單，並更新它所在主機的規則。
func (m *Manager) dropAllow(ctx context.Context, h hosts.Host, botID string) {
	allow, err := m.NetAllow(ctx, botID)
	if err != nil || len(allow) == 0 {
		return
	}
	_ = m.db.DeleteSetting(ctx, NetAllowSetting(botID))
	if m.noGuard {
		return
	}
	rules, err := m.hostRules(ctx, h.ID())
	if err == nil {
		_, err = m.ensureNet(ctx, h, rules)
	}
	if err != nil {
		m.log.Warn("更新網路防護規則失敗", "host", h.ID(), "err", err)
	}
}

// stale 回答容器是不是用舊的設定建的（強化版本或資源限制和現在不同），沒開機時可以直接重建。
func (m *Manager) stale(c *container.InspectResponse, b store.Box) bool {
	if c.Config == nil {
		return false
	}
	if c.Config.Labels[LabelSpec] != m.specRevision() {
		return true
	}
	return c.Config.Labels[LabelLimits] != parseLimits(b.Limits).signature()
}

// specRevision 是容器設定的版本：強化（SEC-3）改了，或網路防護（SEC-4）開關不同時，舊容器要重建。
func (m *Manager) specRevision() string {
	if m.noGuard {
		return specRevision + "-nonet"
	}
	return specRevision
}

// Outdated 回答 Bot 的電腦要不要「更新電腦」（BX-15）：映像換了，或容器是舊的強化設定建的。沒有電腦時是 false。
func (m *Manager) Outdated(ctx context.Context, botID string) (bool, error) {
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return false, err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil || c == nil {
		return false, err
	}
	img, err := h.Docker().ImageInspect(ctx, m.image)
	if err != nil {
		return false, dockerErr(err)
	}
	return c.Image != img.ID || m.stale(c, b), nil
}

// Rebuild 用目前的映像與設定重建容器，保留三個 volume（BX-15）。原本在跑的話重建後會再開機。
func (m *Manager) Rebuild(ctx context.Context, botID string) (Status, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return Status{}, err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return Status{}, err
	}
	wasRunning := running(c)
	b.State, b.Error = StateUpdating, ""
	if err := m.db.UpsertBox(ctx, b); err != nil {
		return Status{}, err
	}
	if c != nil {
		if wasRunning {
			if err := stop(ctx, h, c.ID); err != nil {
				return Status{}, m.fail(ctx, b, err)
			}
		}
		if _, err := h.Docker().ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return Status{}, m.fail(ctx, b, fmt.Errorf("重建 Bot 電腦失敗：%w", dockerErr(err)))
		}
	}
	b.ContainerID = ""
	if wasRunning {
		return m.start(ctx, b, h)
	}
	if c, err = m.ensure(ctx, &b, h); err != nil {
		return Status{}, err
	}
	return m.sync(ctx, b, h, c)
}

// Touch 記錄電腦剛被使用（閒置休眠用，B13）。
func (m *Manager) Touch(ctx context.Context, botID string) error { return m.db.TouchBox(ctx, botID) }

// WaitReady 等到桌面與 Hermes 都就緒：noVNC 回應頁面、Hermes /health 回報桌面已就緒。
// 對應 LazyBoy server/computers.py start() 的等待；容器中途停掉時立刻回報。
func (m *Manager) WaitReady(ctx context.Context, botID string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.BoxReadyTimeout)
	defer cancel()
	for {
		s, err := m.Status(ctx, botID)
		if err != nil {
			return s, err
		}
		if !s.Running {
			return s, fmt.Errorf("電腦沒有在執行（狀態：%s）", s.State)
		}
		if m.ready(ctx, s) {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("電腦尚未就緒，請查看電腦日誌：%w", ctx.Err())
		case <-time.After(limits.BoxReadyPoll):
		}
	}
}

func (m *Manager) ready(ctx context.Context, s Status) bool {
	if s.NoVNCAddr == "" || s.HermesAddr == "" {
		return false
	}
	get := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		return m.http.Do(req)
	}
	res, err := get("http://" + s.NoVNCAddr + "/vnc.html")
	if err != nil {
		return false
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	res, err = get("http://" + s.HermesAddr + "/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var health struct {
		OK      bool `json:"ok"`
		Desktop bool `json:"desktop"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&health) == nil && health.OK && health.Desktop
}
