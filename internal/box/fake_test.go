package box

import (
	"archive/tar"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path"
	"path/filepath"
	"strconv"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
)

// fakeDocker 是記憶體裡的 Docker Engine：記下每次建立容器的參數，模擬開機時隨機發布的埠。
type fakeDocker struct {
	mu         sync.Mutex
	images     map[string]string // 名稱 → ID
	containers map[string]*container.InspectResponse
	volumes    map[string]volume.Volume
	created    []client.ContainerCreateOptions
	stops      []*int
	copies     []string
	// files 是 CopyToContainer 寫進去的檔案內容（目的資料夾/檔名 → 內容）。
	files    map[string][]byte
	networks map[string]network.Inspect
	nextPort int
	seq      int
	// execs 記下開過的行程；execRun 是行程本身（nil 時讀完 stdin 就以 0 結束）。
	execs   map[string]*fakeExec
	execRun func(cmd []string, stdin io.Reader, stdout io.Writer) int
}

type fakeExec struct {
	container string
	opts      client.ExecCreateOptions
	running   bool
	exit      int
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		images:     map[string]string{DefaultImage: "sha256:image1"},
		containers: map[string]*container.InspectResponse{},
		volumes:    map[string]volume.Volume{},
		execs:      map[string]*fakeExec{},
		files:      map[string][]byte{},
		networks:   map[string]network.Inspect{},
		nextPort:   40000,
	}
}

func notFound(what string) error { return cerrdefs.ErrNotFound.WithMessage(what + " not found") }

func (f *fakeDocker) find(id string) *container.InspectResponse {
	if c, ok := f.containers[id]; ok {
		return c
	}
	for _, c := range f.containers {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (f *fakeDocker) Ping(context.Context, client.PingOptions) (client.PingResult, error) {
	return client.PingResult{}, nil
}

func (f *fakeDocker) Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
	return client.SystemInfoResult{}, nil
}

func (f *fakeDocker) ImageInspect(_ context.Context, name string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.images[name]
	if !ok {
		return client.ImageInspectResult{}, notFound("image")
	}
	return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: id}}, nil
}

func (f *fakeDocker) ContainerCreate(_ context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[o.Name]; ok {
		return client.ContainerCreateResult{}, cerrdefs.ErrConflict.WithMessage("name in use")
	}
	for _, m := range o.HostConfig.Mounts {
		if m.Type == "volume" {
			if _, ok := f.volumes[m.Source]; !ok {
				return client.ContainerCreateResult{}, fmt.Errorf("volume %s 應該先建立", m.Source)
			}
		}
	}
	nets := map[string]*network.EndpointSettings{}
	if o.NetworkingConfig != nil {
		for name, ep := range o.NetworkingConfig.EndpointsConfig {
			if _, ok := f.networks[name]; !ok {
				return client.ContainerCreateResult{}, notFound("network " + name)
			}
			nets[name] = ep
		}
	}
	f.seq++
	f.created = append(f.created, o)
	img := f.images[o.Config.Image]
	f.containers[o.Name] = &container.InspectResponse{
		ID:              "c" + strconv.Itoa(f.seq),
		Name:            "/" + o.Name,
		Image:           img,
		State:           &container.State{Status: container.StateCreated},
		Config:          o.Config,
		HostConfig:      o.HostConfig,
		NetworkSettings: &container.NetworkSettings{Ports: network.PortMap{}, Networks: nets},
	}
	return client.ContainerCreateResult{ID: f.containers[o.Name].ID}, nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return client.ContainerInspectResult{}, notFound("container")
	}
	cp := *c
	st := *c.State
	cp.State = &st
	ns := container.NetworkSettings{Ports: network.PortMap{}, Networks: c.NetworkSettings.Networks}
	for k, v := range c.NetworkSettings.Ports {
		ns.Ports[k] = v
	}
	cp.NetworkSettings = &ns
	return client.ContainerInspectResult{Container: cp}, nil
}

func (f *fakeDocker) ContainerStart(_ context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return client.ContainerStartResult{}, notFound("container")
	}
	c.State.Running, c.State.Status = true, container.StateRunning
	for p, bs := range c.HostConfig.PortBindings {
		f.nextPort++
		c.NetworkSettings.Ports[p] = []network.PortBinding{{HostIP: bs[0].HostIP, HostPort: strconv.Itoa(f.nextPort)}}
	}
	return client.ContainerStartResult{}, nil
}

func (f *fakeDocker) ContainerStop(_ context.Context, id string, o client.ContainerStopOptions) (client.ContainerStopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return client.ContainerStopResult{}, notFound("container")
	}
	f.stops = append(f.stops, o.Timeout)
	c.State.Running, c.State.Status = false, container.StateExited
	c.NetworkSettings.Ports = network.PortMap{}
	return client.ContainerStopResult{}, nil
}

func (f *fakeDocker) ContainerRemove(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return client.ContainerRemoveResult{}, notFound("container")
	}
	delete(f.containers, c.Name[1:])
	return client.ContainerRemoveResult{}, nil
}

func (f *fakeDocker) VolumeCreate(_ context.Context, o client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := volume.Volume{Name: o.Name, Labels: o.Labels}
	f.volumes[o.Name] = v
	return client.VolumeCreateResult{Volume: v}, nil
}

func (f *fakeDocker) VolumeInspect(_ context.Context, id string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.volumes[id]
	if !ok {
		return client.VolumeInspectResult{}, notFound("volume")
	}
	return client.VolumeInspectResult{Volume: v}, nil
}

func (f *fakeDocker) VolumeRemove(_ context.Context, id string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.volumes[id]; !ok {
		return client.VolumeRemoveResult{}, notFound("volume")
	}
	delete(f.volumes, id)
	return client.VolumeRemoveResult{}, nil
}

// CopyToContainer 記下寫入的路徑與檔案內容（Prepare、網路防護規則的測試用）。
func (f *fakeDocker) CopyToContainer(_ context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.find(id) == nil {
		return client.CopyToContainerResult{}, notFound("container")
	}
	f.copies = append(f.copies, o.DestinationPath)
	if o.Content != nil {
		tr := tar.NewReader(o.Content)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(tr)
			f.files[path.Join(o.DestinationPath, h.Name)] = data
		}
	}
	return client.CopyToContainerResult{}, nil
}

func (f *fakeDocker) NetworkCreate(_ context.Context, name string, o client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.networks[name]; ok {
		return client.NetworkCreateResult{}, cerrdefs.ErrConflict.WithMessage("network with name " + name + " already exists")
	}
	n := network.Inspect{Network: network.Network{Name: name, ID: "n-" + name, Driver: o.Driver, Options: o.Options, Labels: o.Labels}}
	if o.IPAM != nil {
		n.IPAM = *o.IPAM
	}
	f.networks[name] = n
	return client.NetworkCreateResult{ID: n.ID}, nil
}

func (f *fakeDocker) NetworkInspect(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.networks[id]
	if !ok {
		return client.NetworkInspectResult{}, notFound("network")
	}
	return client.NetworkInspectResult{Network: n}, nil
}

func (f *fakeDocker) NetworkList(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out client.NetworkListResult
	for _, n := range f.networks {
		out.Items = append(out.Items, network.Summary{Network: n.Network})
	}
	return out, nil
}

// ContainerList 只支援 network 篩選（網路防護挑 IP 用）。
func (f *fakeDocker) ContainerList(_ context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out client.ContainerListResult
	for _, c := range f.containers {
		match := true
		for name := range o.Filters["network"] {
			if c.NetworkSettings == nil || c.NetworkSettings.Networks[name] == nil {
				match = false
			}
		}
		if !match || (!o.All && !c.State.Running) {
			continue
		}
		out.Items = append(out.Items, container.Summary{ID: c.ID, Names: []string{c.Name},
			NetworkSettings: &container.NetworkSettingsSummary{Networks: c.NetworkSettings.Networks}})
	}
	return out, nil
}

// ipOf 回傳容器在 xbot-box 網路上的固定 IP。
func (f *fakeDocker) ipOf(name string) netip.Addr {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.containers[name]
	if c == nil || c.NetworkSettings.Networks[NetworkName] == nil || c.NetworkSettings.Networks[NetworkName].IPAMConfig == nil {
		return netip.Addr{}
	}
	return c.NetworkSettings.Networks[NetworkName].IPAMConfig.IPv4Address
}

func (f *fakeDocker) file(p string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.files[p])
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fakeDocker) CopyFromContainer(context.Context, string, client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	return client.CopyFromContainerResult{}, notFound("path")
}

func (f *fakeDocker) ContainerStatPath(context.Context, string, client.ContainerStatPathOptions) (client.ContainerStatPathResult, error) {
	return client.ContainerStatPathResult{}, notFound("path")
}

// ExecCreate 只能在開機中的容器上開行程。
func (f *fakeDocker) ExecCreate(_ context.Context, id string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return client.ExecCreateResult{}, notFound("container")
	}
	if c.State == nil || !c.State.Running {
		return client.ExecCreateResult{}, cerrdefs.ErrConflict.WithMessage("container is not running")
	}
	f.seq++
	eid := fmt.Sprintf("exec%d", f.seq)
	f.execs[eid] = &fakeExec{container: c.ID, opts: o}
	return client.ExecCreateResult{ID: eid}, nil
}

// ExecAttach 在記憶體裡跑 execRun，stdout 照 Docker 的多工格式送回。
func (f *fakeDocker) ExecAttach(_ context.Context, id string, _ client.ExecAttachOptions) (client.ExecAttachResult, error) {
	f.mu.Lock()
	e, ok := f.execs[id]
	run := f.execRun
	if ok {
		e.running = true
	}
	f.mu.Unlock()
	if !ok {
		return client.ExecAttachResult{}, notFound("exec")
	}
	if run == nil {
		run = func(_ []string, stdin io.Reader, _ io.Writer) int { _, _ = io.Copy(io.Discard, stdin); return 0 }
	}
	if !e.opts.AttachStdin {
		// 沒接 stdin 的行程（xbot-netguard apply、rm）：照 execRun 跑，但 stdin 是空的。
		inner := run
		run = func(cmd []string, _ io.Reader, out io.Writer) int { return inner(cmd, eofReader{}, out) }
	}
	ours, theirs := net.Pipe()
	go func() {
		code := run(e.opts.Cmd, theirs, muxWriter{theirs})
		f.mu.Lock()
		e.running, e.exit = false, code
		f.mu.Unlock()
		theirs.Close()
	}()
	return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(ours, "")}, nil
}

func (f *fakeDocker) ExecInspect(_ context.Context, id string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.execs[id]
	if !ok {
		return client.ExecInspectResult{}, notFound("exec")
	}
	return client.ExecInspectResult{ID: id, ContainerID: e.container, Running: e.running, ExitCode: e.exit}, nil
}

// muxWriter 用 Docker 的多工格式（8 bytes 標頭：串流 1＝stdout、長度）寫出 stdout。
type muxWriter struct{ w io.Writer }

func (m muxWriter) Write(b []byte) (int, error) {
	hdr := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(b)))
	if _, err := m.w.Write(append(hdr, b...)); err != nil {
		return 0, err
	}
	return len(b), nil
}

// fakeHost 是接上 fakeDocker 的主機。released 記下 ReleaseAddr 放掉的埠（像遠端主機），forgot 記下 ForgetBot。
type fakeHost struct {
	id       string
	dir      string
	docker   *fakeDocker
	mu       sync.Mutex
	released []int
	forgot   []string
}

func (h *fakeHost) ReleaseAddr(port int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.released = append(h.released, port)
}

func (h *fakeHost) ForgetBot(_ context.Context, botID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.forgot = append(h.forgot, botID)
	return nil
}

func (h *fakeHost) ID() string              { return h.id }
func (h *fakeHost) Kind() string            { return "local" }
func (h *fakeHost) Docker() hosts.DockerAPI { return h.docker }
func (h *fakeHost) Addr(port int) string    { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }
func (h *fakeHost) Close() error            { return nil }
func (h *fakeHost) PrepareIPC(id string) (string, error) {
	return filepath.Join(h.dir, "bots", id, "ipc"), nil
}
