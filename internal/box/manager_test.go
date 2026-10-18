package box

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/store"
)

var ctx = context.Background()

type env struct {
	db     *store.DB
	docker *fakeDocker
	host   *fakeHost
	m      *Manager
	bot    string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	row, err := db.CreateHost(ctx, store.Host{Name: hosts.LocalName, Kind: "local"})
	if err != nil {
		t.Fatal(err)
	}
	bot, err := db.CreateBot(ctx, store.Bot{Name: "小秘"})
	if err != nil {
		t.Fatal(err)
	}
	d := newFakeDocker()
	h := &fakeHost{id: row.ID, dir: dir, docker: d}
	return &env{db: db, docker: d, host: h, m: New(db, hosts.NewRegistry(h), Options{}), bot: bot.ID}
}

// creates 是建立過幾台 Bot 電腦（不算 xbot-netguard）。
func (e *env) creates() int {
	n := 0
	for _, o := range e.docker.created {
		if strings.HasPrefix(o.Name, NamePrefix) {
			n++
		}
	}
	return n
}

// lastCreate 是最後一次建立 Bot 電腦的參數。
func (e *env) lastCreate() client.ContainerCreateOptions {
	for i := len(e.docker.created) - 1; i >= 0; i-- {
		if strings.HasPrefix(e.docker.created[i].Name, NamePrefix) {
			return e.docker.created[i]
		}
	}
	return client.ContainerCreateOptions{}
}

// vols 是 Bot 的資料區數量（不算網路防護的）。
func (e *env) vols() int {
	n := 0
	for _, v := range e.docker.volumes {
		if v.Labels[LabelBot] != "" {
			n++
		}
	}
	return n
}

// boxes 是還在的 Bot 電腦數（不算 xbot-netguard）。
func (e *env) boxes() int {
	n := 0
	for name := range e.docker.containers {
		if strings.HasPrefix(name, NamePrefix) {
			n++
		}
	}
	return n
}

func ok[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestCreateOptions(t *testing.T) {
	ip := netip.MustParseAddr("172.30.4.7")
	o := createOptions(spec{BotID: "b1", Image: "xbot-box:dev", IPCDir: "/data/bots/b1/ipc", Limits: DefaultLimits(),
		Net: &netAttach{Network: NetworkName, IP: ip, ReadyVolume: GuardReadyVolume}})
	cfg, hc := o.Config, o.HostConfig

	if o.Name != "xbot-box-b1" || cfg.Image != "xbot-box:dev" {
		t.Fatalf("名稱與映像：%s %s", o.Name, cfg.Image)
	}
	if cfg.Labels["xbot.bot"] != "b1" || cfg.Labels["xbot.managed"] != "true" || cfg.Labels["xbot.limits"] == "" || cfg.Labels[LabelSpec] != specRevision {
		t.Fatalf("標籤：%v", cfg.Labels)
	}
	if !slices.Contains(cfg.Env, "XBOT_UID=1000") || !slices.Contains(cfg.Env, "XBOT_GID=1000") {
		t.Fatalf("環境變數：%v", cfg.Env)
	}
	for _, e := range cfg.Env {
		if strings.Contains(strings.ToUpper(e), "KEY") || strings.Contains(strings.ToUpper(e), "TOKEN") {
			t.Fatalf("金鑰不能進容器：%s", e)
		}
	}
	if cfg.StopTimeout == nil || *cfg.StopTimeout != 20 {
		t.Fatalf("停止等待時間：%v", cfg.StopTimeout)
	}

	// SEC-3 強化：Bot 使用者開機、沒有任何 capability、no-new-privileges、唯讀根檔案系統＋tmpfs。
	if cfg.User != "1000:1000" {
		t.Fatalf("要以 Bot 使用者開機：%q", cfg.User)
	}
	if !slices.Equal(hc.CapDrop, []string{"ALL"}) || len(hc.CapAdd) != 0 {
		t.Fatalf("cap-drop=%v cap-add=%v", hc.CapDrop, hc.CapAdd)
	}
	if !slices.Contains(hc.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("security-opt：%v", hc.SecurityOpt)
	}
	if !hc.ReadonlyRootfs {
		t.Fatal("根檔案系統要唯讀")
	}
	for _, dir := range []string{"/tmp", "/run", "/var/tmp"} {
		opts, ok := hc.Tmpfs[dir]
		if !ok || !strings.Contains(opts, "size=") || !strings.Contains(opts, "nosuid") || !strings.Contains(opts, "nodev") {
			t.Fatalf("tmpfs %s：%q", dir, opts)
		}
	}
	if len(hc.Tmpfs) != 3 || !strings.Contains(hc.Tmpfs["/tmp"], "mode=1777") || !strings.Contains(hc.Tmpfs["/run"], "uid=1000") {
		t.Fatalf("tmpfs：%v", hc.Tmpfs)
	}
	if hc.Privileged || hc.NetworkMode == "host" || hc.PidMode == "host" || hc.IpcMode == "host" || hc.UsernsMode == "host" {
		t.Fatal("不能用特權或主機命名空間")
	}
	if len(hc.ExtraHosts) != 0 {
		t.Fatalf("不需要直接連主機（SEC-4）：%v", hc.ExtraHosts)
	}
	if hc.RestartPolicy.Name != container.RestartPolicyUnlessStopped {
		t.Fatalf("重啟策略：%v", hc.RestartPolicy)
	}

	// SEC-4：接在 xbot-box 網路，固定 IP。
	if string(hc.NetworkMode) != NetworkName || o.NetworkingConfig == nil {
		t.Fatalf("網路：%s %+v", hc.NetworkMode, o.NetworkingConfig)
	}
	if ep := o.NetworkingConfig.EndpointsConfig[NetworkName]; ep == nil || ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address != ip {
		t.Fatalf("固定 IP：%+v", o.NetworkingConfig.EndpointsConfig)
	}

	// BX-11 資源限制。
	r := hc.Resources
	if r.Memory != 3<<30 || hc.ShmSize != 512<<20 || r.CPUPeriod != 10000 || r.CPUQuota != 20000 {
		t.Fatalf("資源：mem=%d shm=%d period=%d quota=%d", r.Memory, hc.ShmSize, r.CPUPeriod, r.CPUQuota)
	}
	if r.PidsLimit == nil || *r.PidsLimit <= 0 {
		t.Fatalf("pids-limit：%v", r.PidsLimit)
	}

	// 掛載：四個具名 volume＋唯讀的 product.sock 資料夾＋唯讀的網路防護就緒標記；不能有 Docker socket（SEC-9）。
	want := map[string]mount.Mount{
		"/hermes":            {Type: mount.TypeVolume, Source: "xbot-box-b1-hermes", Target: "/hermes"},
		"/home/box":          {Type: mount.TypeVolume, Source: "xbot-box-b1-home", Target: "/home/box"},
		"/workspace":         {Type: mount.TypeVolume, Source: "xbot-box-b1-workspace", Target: "/workspace"},
		"/opt/xbot/cred":     {Type: mount.TypeVolume, Source: "xbot-box-b1-cred", Target: "/opt/xbot/cred"},
		"/product-ipc":       {Type: mount.TypeBind, Source: "/data/bots/b1/ipc", Target: "/product-ipc", ReadOnly: true},
		"/opt/xbot/netguard": {Type: mount.TypeVolume, Source: GuardReadyVolume, Target: "/opt/xbot/netguard", ReadOnly: true},
	}
	if len(hc.Mounts) != len(want) || len(hc.Binds) != 0 {
		t.Fatalf("掛載數量：%+v %v", hc.Mounts, hc.Binds)
	}
	for _, m := range hc.Mounts {
		if w, ok := want[m.Target]; !ok || w.Type != m.Type || w.Source != m.Source || w.ReadOnly != m.ReadOnly {
			t.Fatalf("掛載 %s：%+v", m.Target, m)
		}
		if strings.Contains(m.Source, "docker.sock") {
			t.Fatal("不能把 Docker socket 掛進容器")
		}
	}

	// 埠：noVNC 與 Hermes 只發布在 127.0.0.1，主機埠隨機。
	for _, p := range []string{"6080/tcp", "8642/tcp"} {
		port := network.MustParsePort(p)
		if _, ok := cfg.ExposedPorts[port]; !ok {
			t.Fatalf("沒有 expose %s", p)
		}
		bs := hc.PortBindings[port]
		if len(bs) != 1 || bs[0].HostIP.String() != "127.0.0.1" || bs[0].HostPort != "" {
			t.Fatalf("%s 的發布：%+v", p, bs)
		}
	}
	if len(hc.PortBindings) != 2 || hc.PublishAllPorts {
		t.Fatalf("只發布兩個埠：%+v", hc.PortBindings)
	}

	// 關掉網路防護時接 Docker 預設網路、沒有就緒標記。
	o = createOptions(spec{BotID: "b1", Image: "img", IPCDir: "/ipc", Limits: DefaultLimits()})
	if o.HostConfig.NetworkMode != "" || o.NetworkingConfig != nil || len(o.HostConfig.Mounts) != 5 {
		t.Fatalf("沒有網路防護：%s %+v %d", o.HostConfig.NetworkMode, o.NetworkingConfig, len(o.HostConfig.Mounts))
	}
}

func TestLimitsFromRow(t *testing.T) {
	l := parseLimits(json.RawMessage(`{"cpus":4,"memory_mb":8192}`))
	if l.CPUs != 4 || l.MemoryMB != 8192 || l.ShmMB != 512 || l.Pids != DefaultLimits().Pids {
		t.Fatalf("合併預設值：%+v", l)
	}
	o := createOptions(spec{BotID: "b1", Image: "img", IPCDir: "/ipc", Limits: l})
	if o.HostConfig.CPUQuota != 40000 || o.HostConfig.Memory != 8<<30 {
		t.Fatalf("自訂限制沒有套用：%+v", o.HostConfig.Resources)
	}
	if parseLimits(json.RawMessage(`{}`)) != DefaultLimits() || parseLimits(nil) != DefaultLimits() {
		t.Fatal("空白時用預設值")
	}
}

func TestLifecycle(t *testing.T) {
	e := setup(t)
	if s := ok[Status](t)(e.m.Status(ctx, e.bot)); s.State != StateNone || s.Running {
		t.Fatalf("還沒建立：%+v", s)
	}

	// 第一次開機：建立三個 volume 與容器，再開機（BX-1 延遲建立）。
	s := ok[Status](t)(e.m.Start(ctx, e.bot))
	if s.State != StateRunning || !s.Running || s.NoVNCPort == 0 || s.HermesPort == 0 || s.HostID != e.host.id {
		t.Fatalf("開機：%+v", s)
	}
	if s.NoVNCAddr != e.host.Addr(s.NoVNCPort) || s.HermesAddr != e.host.Addr(s.HermesPort) {
		t.Fatalf("位址：%+v", s)
	}
	if e.vols() != 4 || e.creates() != 1 {
		t.Fatalf("volume=%d 容器=%d", e.vols(), e.creates())
	}
	for name, v := range e.docker.volumes {
		if strings.HasPrefix(name, NamePrefix) && (v.Labels["xbot.bot"] != e.bot || v.Labels["xbot.managed"] != "true") {
			t.Fatalf("volume %s 標籤：%v", name, v.Labels)
		}
	}
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	if row.State != StateRunning || row.ContainerID != s.ContainerID || row.NoVNCPort != s.NoVNCPort || row.HermesPort != s.HermesPort ||
		row.ImageRev != "sha256:image1" || row.HostID != e.host.id || row.LastActiveAt == 0 || row.NetIP == "" {
		t.Fatalf("box 表：%+v", row)
	}
	if parseLimits(row.Limits) != DefaultLimits() || !strings.Contains(string(row.Limits), "memory_mb") {
		t.Fatalf("box.limits：%s", row.Limits)
	}
	id := s.ContainerID

	// 再開一次不會重建。
	if s2 := ok[Status](t)(e.m.Start(ctx, e.bot)); s2.ContainerID != id || e.creates() != 1 {
		t.Fatalf("重複開機不該重建：%+v", s2)
	}

	// 停止：休眠，埠清掉，等 20 秒收尾。
	s = ok[Status](t)(e.m.Stop(ctx, e.bot))
	if s.State != StateSleeping || s.Running || s.NoVNCPort != 0 || s.ContainerID != id {
		t.Fatalf("停止：%+v", s)
	}
	if n := len(e.docker.stops); n != 1 || *e.docker.stops[0] != 20 {
		t.Fatalf("停止參數：%v", e.docker.stops)
	}
	if row = ok[store.Box](t)(e.db.GetBox(ctx, e.bot)); row.State != StateSleeping || row.NoVNCPort != 0 || row.HermesPort != 0 {
		t.Fatalf("box 表停止後：%+v", row)
	}

	// 重開：同一台容器，新的隨機埠。
	s = ok[Status](t)(e.m.Start(ctx, e.bot))
	if s.ContainerID != id || !s.Running || e.creates() != 1 {
		t.Fatalf("重開：%+v", s)
	}

	// 刪除但保留資料。
	s = ok[Status](t)(e.m.Remove(ctx, e.bot, false))
	if s.State != StateNone || s.ContainerID != "" || e.boxes() != 0 || e.vols() != 4 {
		t.Fatalf("刪除保留資料：%+v volumes=%d", s, e.vols())
	}
	// 再建立會接回原本的 volume。
	s = ok[Status](t)(e.m.Create(ctx, e.bot))
	if s.State != StateSleeping || s.Running || s.ContainerID == id || e.vols() != 4 {
		t.Fatalf("重新建立：%+v", s)
	}
	// 連資料一起刪。
	ok[Status](t)(e.m.Remove(ctx, e.bot, true))
	if e.boxes() != 0 || e.vols() != 0 {
		t.Fatalf("purge 後還有 容器=%d volume=%d", e.boxes(), e.vols())
	}
	if row, err := e.db.GetBox(ctx, e.bot); err != nil || row.NetIP != "" {
		t.Fatalf("purge 後 IP 要還回去：%+v %v", row, err)
	}
	// 對已經不存在的電腦再刪一次不會出錯。
	ok[Status](t)(e.m.Remove(ctx, e.bot, true))
	ok[Status](t)(e.m.Stop(ctx, e.bot))
}

func TestRebuildKeepsVolumes(t *testing.T) {
	e := setup(t)
	old := ok[Status](t)(e.m.Start(ctx, e.bot))
	e.docker.images[DefaultImage] = "sha256:image2"
	s := ok[Status](t)(e.m.Rebuild(ctx, e.bot))
	if s.ContainerID == old.ContainerID || !s.Running || s.State != StateRunning {
		t.Fatalf("重建後應該是新容器且在執行：%+v", s)
	}
	if e.vols() != 4 || e.creates() != 2 || len(e.docker.stops) != 1 {
		t.Fatalf("volume=%d 建立=%d 停止=%d", e.vols(), e.creates(), len(e.docker.stops))
	}
	if row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot)); row.ImageRev != "sha256:image2" {
		t.Fatalf("映像版本：%s", row.ImageRev)
	}

	// 休眠中的電腦重建後維持休眠。
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	s = ok[Status](t)(e.m.Rebuild(ctx, e.bot))
	if s.Running || s.State != StateSleeping || e.creates() != 3 {
		t.Fatalf("休眠中重建：%+v", s)
	}
}

func TestCustomLimitsApplied(t *testing.T) {
	e := setup(t)
	if err := e.db.UpsertBox(ctx, store.Box{BotID: e.bot, HostID: e.host.id, Limits: json.RawMessage(`{"cpus":1}`)}); err != nil {
		t.Fatal(err)
	}
	ok[Status](t)(e.m.Create(ctx, e.bot))
	if q := e.lastCreate().HostConfig.CPUQuota; q != 10000 {
		t.Fatalf("CPU 配額：%d", q)
	}
}

func TestMissingImage(t *testing.T) {
	e := setup(t)
	delete(e.docker.images, DefaultImage)
	_, err := e.m.Start(ctx, e.bot)
	if err == nil || !strings.Contains(err.Error(), "make box") {
		t.Fatalf("沒有映像：%v", err)
	}
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	if row.State != StateError || !strings.Contains(row.Error, "make box") {
		t.Fatalf("錯誤要記在 box 表：%+v", row)
	}
	// 補上映像後可以恢復。
	e.docker.images[DefaultImage] = "sha256:image1"
	if s := ok[Status](t)(e.m.Start(ctx, e.bot)); s.State != StateRunning || s.Error != "" {
		t.Fatalf("恢復：%+v", s)
	}
}

func TestNameTakenByOtherContainer(t *testing.T) {
	e := setup(t)
	o := createOptions(spec{BotID: e.bot, Image: DefaultImage, IPCDir: "/ipc", Limits: DefaultLimits()})
	o.Config.Labels = map[string]string{"other": "app"}
	o.HostConfig.Mounts = nil
	if _, err := e.docker.ContainerCreate(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Start(ctx, e.bot); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("名稱被佔用：%v", err)
	}
	if _, err := e.m.Remove(ctx, e.bot, true); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("不能刪別人的容器：%v", err)
	}
	if len(e.docker.containers) != 1 {
		t.Fatal("別人的容器不能被動到")
	}
}

func TestUnknownBotAndHost(t *testing.T) {
	e := setup(t)
	if _, err := e.m.Start(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的 Bot：%v", err)
	}
	other := ok[store.Host](t)(e.db.CreateHost(ctx, store.Host{Name: "遠端", Kind: "ssh"}))
	if err := e.db.UpsertBox(ctx, store.Box{BotID: e.bot, HostID: other.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Start(ctx, e.bot); !errors.Is(err, hosts.ErrUnknownHost) {
		t.Fatalf("主機沒連上：%v", err)
	}
}

func TestStatusReconciles(t *testing.T) {
	e := setup(t)
	s := ok[Status](t)(e.m.Start(ctx, e.bot))
	// 容器在 XBot 之外被停掉。
	if _, err := e.docker.ContainerStop(ctx, s.ContainerID, client.ContainerStopOptions{}); err != nil {
		t.Fatal(err)
	}
	if s = ok[Status](t)(e.m.Status(ctx, e.bot)); s.State != StateSleeping || s.NoVNCPort != 0 {
		t.Fatalf("同步停止：%+v", s)
	}
	// 容器在 XBot 之外被刪掉。
	delete(e.docker.containers, ContainerName(e.bot))
	if s = ok[Status](t)(e.m.Status(ctx, e.bot)); s.State != StateNone || s.ContainerID != "" {
		t.Fatalf("同步刪除：%+v", s)
	}
}

func TestPrepareRunsBeforeStart(t *testing.T) {
	e := setup(t)
	var calls []string
	e.m.prepare = func(_ context.Context, h hosts.Host, botID, containerID string) error {
		c := e.docker.find(containerID)
		if c == nil || botID != e.bot || h.ID() != e.host.ID() {
			t.Fatalf("Prepare 參數不對：%s %s", botID, containerID)
		}
		// 開機前呼叫：容器已建立但還沒執行（第一次），或已經在跑（之後）。
		calls = append(calls, map[bool]string{true: "running", false: "stopped"}[c.State.Running])
		return nil
	}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if !slices.Equal(calls, []string{"stopped", "running"}) {
		t.Fatalf("Prepare 呼叫：%v", calls)
	}

	// Prepare 失敗時不開機，錯誤記在 box 列上。
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	e.m.prepare = func(context.Context, hosts.Host, string, string) error { return errors.New("寫不進去") }
	if _, err := e.m.Start(ctx, e.bot); err == nil || !strings.Contains(err.Error(), "寫不進去") {
		t.Fatalf("Prepare 失敗應該回報：%v", err)
	}
	if b, _ := e.db.GetBox(ctx, e.bot); !strings.Contains(b.Error, "寫不進去") {
		t.Fatalf("錯誤應該記在 box 列：%+v", b)
	}
	if s := ok[Status](t)(e.m.Status(ctx, e.bot)); s.Running {
		t.Fatal("Prepare 失敗時不應該開機")
	}
}
