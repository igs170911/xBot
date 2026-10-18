package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
}

// Manager 管理每個 Bot 的電腦（容器）：建立、開機、停止、刪除、重建，狀態寫回 box 表。
// 同一個 Bot 的操作依序執行；不同 Bot 互不影響。
type Manager struct {
	db    *store.DB
	hosts Hosts
	image string
	http  *http.Client

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New 建立 Manager。
func New(db *store.DB, hs Hosts, opts Options) *Manager {
	if opts.Image == "" {
		opts.Image = DefaultImage
	}
	return &Manager{db: db, hosts: hs, image: opts.Image, http: &http.Client{Timeout: limits.BoxProbeTimeout}, locks: map[string]*sync.Mutex{}}
}

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

// sync 依容器實際狀態更新 box 列並寫回資料庫。
func (m *Manager) sync(ctx context.Context, b store.Box, h hosts.Host, c *container.InspectResponse) (Status, error) {
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
	return status(b, h, running(c)), nil
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
	res, err := api.ContainerCreate(ctx, createOptions(b.BotID, m.image, ipc, lim))
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

func ensureVolume(ctx context.Context, api hosts.DockerAPI, name, botID string) error {
	_, err := api.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return dockerErr(err)
	}
	_, err = api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: map[string]string{LabelBot: botID, LabelManaged: "true"}})
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
	b.Error = ""
	return m.sync(ctx, b, h, nil)
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
