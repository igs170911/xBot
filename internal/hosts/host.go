package hosts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/moby/moby/client"

	"xbot/internal/store"
)

// DockerAPI 是 Box Manager 用到的 Docker Engine API。*client.Client 直接符合；
// 單元測試用假的實作。遠端主機（B12）只要換掉連線方式，回傳的仍是同一個介面。
type DockerAPI interface {
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
	Info(ctx context.Context, options client.InfoOptions) (client.SystemInfoResult, error)
	ImageInspect(ctx context.Context, image string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	VolumeCreate(ctx context.Context, options client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeInspect(ctx context.Context, id string, options client.VolumeInspectOptions) (client.VolumeInspectResult, error)
	VolumeRemove(ctx context.Context, id string, options client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	// CopyToContainer、CopyFromContainer 用來在開機前把引擎設定寫進 Bot 的 volume（E02）。
	CopyToContainer(ctx context.Context, id string, options client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, options client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
	// ExecCreate、ExecAttach、ExecInspect 在 Bot 的電腦裡開行程並接上 stdin／stdout（E12：ACP 引擎、收回憑證）。
	ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
}

var _ DockerAPI = (*client.Client)(nil)

// Host 是一台可以放 Bot 電腦的 Docker 主機。
//
// 本機（Local）直接連 Docker socket。遠端主機（B11、B12，`ssh://xbot@<tailnet 名稱>`）會在同一條 SSH 連線上
// 提供 Docker API（client.WithDialContext）、把容器發布在遠端 127.0.0.1 的埠轉發回來（Addr），
// 並把本機的 product.sock 反向轉發到遠端的目錄（PrepareIPC）。Box Manager 只透過這個介面使用主機。
type Host interface {
	// ID 是 host 資料表的主鍵。
	ID() string
	// Kind 是 local 或 ssh（同 host.kind）。
	Kind() string
	// Docker 是這台主機的 Docker API。
	Docker() DockerAPI
	// PrepareIPC 準備 Bot 的 product.sock 所在目錄，回傳它在 Docker 主機上的路徑（會唯讀掛到容器的 /product-ipc）。
	PrepareIPC(botID string) (string, error)
	// Addr 回傳 XBot 服務連到「容器發布在主機 127.0.0.1:port」的位址（host:port）。
	Addr(hostPort int) string
	// Close 關掉連線。
	Close() error
}

// LocalName 是本機主機在主機清單裡的名稱。
const LocalName = "這台電腦"

// BotDir 是 Bot 在資料目錄裡的資料夾：<data>/bots/<id>。
func BotDir(dataDir, botID string) string { return filepath.Join(dataDir, "bots", botID) }

// Local 是執行 XBot 服務的這台機器。
type Local struct {
	id      string
	dataDir string
	docker  *client.Client
}

// NewLocalDocker 依環境變數（DOCKER_HOST 等，沒設時用 /var/run/docker.sock）連本機 Docker，API 版本自動協商。
func NewLocalDocker() (*client.Client, error) {
	return client.New(client.FromEnv)
}

// EnsureLocal 確保 host 表裡有本機那一列（沒有就新增），連上本機 Docker，並記下健康狀態與架構。
// Docker 連不上時仍回傳主機（狀態記為 offline），讓服務照常啟動；之後的電腦操作會回報錯誤。
func EnsureLocal(ctx context.Context, db *store.DB, dataDir string) (*Local, error) {
	row, err := localRow(ctx, db)
	if err != nil {
		return nil, err
	}
	cli, err := NewLocalDocker()
	if err != nil {
		return nil, fmt.Errorf("無法建立 Docker 連線：%w", err)
	}
	h := &Local{id: row.ID, dataDir: dataDir, docker: cli}
	status := "online"
	if info, err := cli.Info(ctx, client.InfoOptions{}); err != nil {
		status = "offline"
	} else if row.Arch != info.Info.Architecture || row.Address != cli.DaemonHost() {
		row.Arch, row.Address = info.Info.Architecture, cli.DaemonHost()
		if err := db.UpdateHost(ctx, row); err != nil {
			return nil, err
		}
	}
	if err := db.SetHostStatus(ctx, row.ID, status); err != nil {
		return nil, err
	}
	return h, nil
}

func localRow(ctx context.Context, db *store.DB) (store.Host, error) {
	all, err := db.ListHosts(ctx)
	if err != nil {
		return store.Host{}, err
	}
	for _, h := range all {
		if h.Kind == "local" {
			return h, nil
		}
	}
	return db.CreateHost(ctx, store.Host{Name: LocalName, Kind: "local"})
}

// ID 實作 Host。
func (h *Local) ID() string { return h.id }

// Kind 實作 Host。
func (h *Local) Kind() string { return "local" }

// Docker 實作 Host。
func (h *Local) Docker() DockerAPI { return h.docker }

// PrepareIPC 建立 <data>/bots/<id>/ipc。權限 0711：容器裡的 Bot 使用者（UID 和服務不同）能進目錄連 socket，
// 但不能列出內容；socket 本身的權限由建立它的 XBot MCP（C11）決定。
func (h *Local) PrepareIPC(botID string) (string, error) {
	dir := filepath.Join(BotDir(h.dataDir, botID), "ipc")
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return "", fmt.Errorf("無法建立 Bot 的通訊資料夾：%w", err)
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return "", err
	}
	return dir, nil
}

// Addr 實作 Host：本機發布的埠就在 127.0.0.1。
func (h *Local) Addr(hostPort int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
}

// Close 實作 Host。
func (h *Local) Close() error { return h.docker.Close() }

// Registry 依 host.id 找到已連線的主機。B02 只有本機；B12 會在新增遠端主機時加進來。
type Registry struct {
	mu    sync.RWMutex
	hosts map[string]Host
	def   string
}

// NewRegistry 建立主機登錄表，def 是新 Bot 預設放的主機。
func NewRegistry(def Host) *Registry {
	return &Registry{hosts: map[string]Host{def.ID(): def}, def: def.ID()}
}

// ErrUnknownHost 表示 Bot 的電腦所在的主機還沒連上（或已移除）。
var ErrUnknownHost = errors.New("找不到這台主機")

// Add 加入（或換掉）一台主機。
func (r *Registry) Add(h Host) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[h.ID()] = h
}

// Get 依 id 找主機；id 空白時回傳預設主機。
func (r *Registry) Get(id string) (Host, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id == "" {
		id = r.def
	}
	h, ok := r.hosts[id]
	if !ok {
		return nil, fmt.Errorf("%w：%s", ErrUnknownHost, id)
	}
	return h, nil
}

// Remove 從登錄表拿掉一台主機（不會關掉它的連線）。預設主機不能拿掉。
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != r.def {
		delete(r.hosts, id)
	}
}

// Default 回傳預設主機（本機）的 id。
func (r *Registry) Default() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.def
}
