package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// fakeDocker 是測試用的 Docker Engine API（只有主機管理用到的幾個端點），聽在 unix socket 上。
type fakeDocker struct {
	mu      sync.Mutex
	images  map[string]string // 名稱 → id
	arch    string
	version string
	save    []byte // docker save 的內容
	loaded  int64  // docker load 收到的位元組
	loadID  string // load 之後映像的 id
	root    string
}

func startFakeDocker(t *testing.T, sock string, d *fakeDocker) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: d}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })
}

func (d *fakeDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Api-Version", "1.47")
	p := r.URL.Path
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.HasSuffix(p, "/_ping"):
		_, _ = io.WriteString(w, "OK")
	case strings.HasSuffix(p, "/info"):
		_ = json.NewEncoder(w).Encode(map[string]any{"ServerVersion": d.version, "NCPU": 4, "MemTotal": int64(8 << 30),
			"Architecture": d.arch, "DockerRootDir": d.root, "Name": "cloud-01", "OperatingSystem": "Debian GNU/Linux 13"})
	case strings.HasSuffix(p, "/images/get"):
		_, _ = w.Write(d.save)
	case strings.HasSuffix(p, "/images/load"):
		d.mu.Unlock()
		n, _ := io.Copy(io.Discard, r.Body)
		d.mu.Lock()
		d.loaded += n
		d.images["xbot-box:dev"] = d.loadID
		_, _ = io.WriteString(w, `{"stream":"Loaded image: xbot-box:dev\n"}`)
	case strings.Contains(p, "/images/") && strings.HasSuffix(p, "/json"):
		name := strings.TrimSuffix(p[strings.Index(p, "/images/")+len("/images/"):], "/json")
		id, ok := d.images[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, `{"message":"No such image: %s"}`, name)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Architecture": "amd64", "Size": len(d.save)})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"message":"fake docker: %s %s"}`, r.Method, p)
	}
}

type svcEnv struct {
	t       *testing.T
	db      *store.DB
	svc     *Service
	local   *Local
	remote  *fakeDocker
	sshd    *fakeSSHD
	bus     *events.Bus
	dataDir string
	port    int
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	minDiskBytes = 1 << 20 // 測試機的 /tmp 可能很小
	ctx := context.Background()
	// unix socket 路徑不能太長，不用 t.TempDir()。
	dir, err := os.MkdirTemp("", "xbh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	data := filepath.Join(dir, "data")
	remoteDir := filepath.Join(dir, "remote")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	localDocker := &fakeDocker{images: map[string]string{"xbot-box:dev": "sha256:new"}, arch: "x86_64", version: "29.0.1",
		save: []byte(strings.Repeat("x", 300_000)), root: dir}
	startFakeDocker(t, filepath.Join(dir, "ld.sock"), localDocker)
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "ld.sock"))
	remote := &fakeDocker{images: map[string]string{}, arch: "x86_64", version: "27.3.1", loadID: "sha256:new", root: dir}
	startFakeDocker(t, filepath.Join(dir, "rd.sock"), remote)

	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	local, err := EnsureLocal(ctx, db, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	id, err := EnsureIdentity(data)
	if err != nil {
		t.Fatal(err)
	}
	d := startSSHD(t, newSigner(t), id.Signer.PublicKey())
	_, port, _ := net.SplitHostPort(d.addr())
	bus := events.New()
	svc := NewService(Options{DB: db, Registry: NewRegistry(local), Local: local, DataDir: data, Image: "xbot-box:dev",
		LocalSocket: func(b string) string { return filepath.Join(dir, b+".sock") }, Bus: bus,
		RemoteDir: remoteDir, DockerSocket: filepath.Join(dir, "rd.sock")})
	t.Cleanup(func() { svc.Close() })
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var p int
	fmt.Sscan(port, &p)
	return &svcEnv{t: t, db: db, svc: svc, local: local, remote: remote, sshd: d, bus: bus, dataDir: data, port: p}
}

func (e *svcEnv) waitStatus(id, want string) store.Host {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		h, err := e.db.GetHost(context.Background(), id)
		if err == nil && h.Status == want {
			return h
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("主機狀態沒有變成 %s：%+v %v", want, h, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newBot(t *testing.T, db *store.DB, name string) store.Bot {
	t.Helper()
	b, err := db.CreateBot(context.Background(), store.Bot{Name: name, MemoryEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestServiceAddLoadOfflineRemove(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	sub := e.bus.Subscribe(nil, 0)
	defer sub.Close()

	// 新增主機：檢查全部通過（映像要傳）、背景傳映像、傳完變在線。
	res, err := e.svc.Add(ctx, AddRequest{Target: Target{Address: "127.0.0.1", Port: e.port}, Name: "cloud-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Report.OK || !res.Added || !res.Report.NeedImage || !res.Loading {
		t.Fatalf("加入結果：%+v", res)
	}
	for _, it := range res.Report.Items {
		want := CheckOK
		switch it.ID {
		case CheckImage:
			want = CheckRunning
		case CheckNetwork:
			want = CheckWarn // 127.0.0.1 不是 Tailscale 位址
		}
		if it.Status != want {
			t.Fatalf("檢查 %s：%+v", it.ID, it)
		}
	}
	id := res.Host.ID
	row := e.waitStatus(id, store.HostOnline)
	if e.remote.loaded != int64(len("x")*300_000) {
		t.Fatalf("傳過去的映像大小：%d", e.remote.loaded)
	}
	var r Resources
	_ = json.Unmarshal(row.Resources, &r)
	if row.Arch != "amd64" || r.CPUs != 4 || r.DockerVersion != "27.3.1" || r.ImageID != "sha256:new" || row.MaxRunning != 3 || row.SSHUser != "xbot" {
		t.Fatalf("主機資料：%+v %+v", row, r)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "hosts", id, "known_hosts")); err != nil {
		t.Fatalf("主機金鑰應記在 hosts/<id>/known_hosts：%v", err)
	}
	gotKinds := map[string]bool{}
	for len(sub.C) > 0 {
		ev := <-sub.C
		gotKinds[ev.Kind] = true
	}
	if !gotKinds[KindHostStatus] || !gotKinds[KindHostImage] {
		t.Fatalf("事件：%v", gotKinds)
	}

	// 同一台再加一次：沿用同一列（可以重複執行），映像已是最新、不再傳。
	again, err := e.svc.Add(ctx, AddRequest{Target: Target{Address: "127.0.0.1", Port: e.port}})
	if err != nil || again.Host.ID != id || again.Loading || again.Report.NeedImage || again.Host.Name != "cloud-01" {
		t.Fatalf("重複加入：%+v %v", again, err)
	}
	all, _ := e.db.ListHosts(ctx)
	if len(all) != 2 {
		t.Fatalf("主機數：%d", len(all))
	}
	// 同名要換名字。
	if _, err := e.svc.SetLimits(ctx, all[0].ID, ptr("cloud-01"), nil); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("同名：%v", err)
	}

	// 主機離線（CL-6）→ offline；回來 → online。
	e.sshd.ln.Close()
	e.sshd.dropAll()
	e.waitStatus(id, store.HostOffline)
	startSSHDOn(t, net.JoinHostPort("127.0.0.1", fmt.Sprint(e.port)), e.sshd.cfgHostKey(), e.sshdUser())
	e.waitStatus(id, store.HostOnline)

	// 移除：上面有電腦時不行；電腦刪掉後可以，known_hosts 一起清掉，放在這台的 Bot 改回自動擺放。
	b := newBot(t, e.db, "阿凱")
	if err := e.db.UpsertBox(ctx, store.Box{BotID: b.ID, HostID: id, State: "sleeping"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Remove(ctx, id); !errors.Is(err, ErrHostInUse) {
		t.Fatalf("上面有電腦時移除：%v", err)
	}
	if _, err := e.svc.Remove(ctx, e.local.ID()); !errors.Is(err, ErrLocalHost) {
		t.Fatalf("移除這台電腦：%v", err)
	}
	if err := e.db.DeleteBot(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	c := newBot(t, e.db, "小編")
	_ = e.db.SetBotHost(ctx, c.ID, id)
	if _, err := e.svc.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "hosts", id)); !os.IsNotExist(err) {
		t.Fatalf("known_hosts 應該刪掉：%v", err)
	}
	if got, _ := e.db.GetBot(ctx, c.ID); got.HostID != "" {
		t.Fatalf("移除主機後 Bot 應改回自動擺放：%q", got.HostID)
	}
	if _, err := e.svc.o.Registry.Get(id); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("登錄表裡應該沒有了：%v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// cfgHostKey、sshdUser 讓測試在同一個位址重開 sshd（主機金鑰不變，模擬主機重開機）。
func (d *fakeSSHD) cfgHostKey() ssh.Signer { return d.hostKey }
func (e *svcEnv) sshdUser() ssh.PublicKey {
	id, err := EnsureIdentity(e.dataDir)
	if err != nil {
		e.t.Fatal(err)
	}
	return id.Signer.PublicKey()
}

func TestServiceAddFailsCleanly(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	// 連不上的位址：檢查沒過，不留下主機。
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	res, err := e.svc.Add(ctx, AddRequest{Target: Target{Address: "127.0.0.1", Port: closed}, Name: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.OK || res.Added || res.Report.Item(CheckSSH).Status != CheckFail || res.Report.Item(CheckSSH).Fix == "" {
		t.Fatalf("連不上時：%+v", res.Report)
	}
	if all, _ := e.db.ListHosts(ctx); len(all) != 1 {
		t.Fatalf("沒加入成功的主機不應該留下：%d", len(all))
	}

	// Docker 太舊：該項標紅、不能加入。
	e.remote.version = "20.10.24"
	res, err = e.svc.Add(ctx, AddRequest{Target: Target{Address: "127.0.0.1", Port: e.port}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.OK || res.Report.Item(CheckDocker).Status != CheckFail {
		t.Fatalf("Docker 太舊：%+v", res.Report.Item(CheckDocker))
	}
	if all, _ := e.db.ListHosts(ctx); len(all) != 1 {
		t.Fatalf("檢查沒過不應該留下：%d", len(all))
	}
}

func TestPlaceAndAdmit(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	res, err := e.svc.Add(ctx, AddRequest{Target: Target{Address: "127.0.0.1", Port: e.port}, Name: "cloud-01", MaxRunning: 1})
	if err != nil || !res.Added {
		t.Fatalf("%+v %v", res, err)
	}
	cloud := res.Host.ID
	e.waitStatus(cloud, store.HostOnline)
	localID := e.local.ID()

	// 自動擺放：預設是這台電腦（不限台數）。
	a := newBot(t, e.db, "阿凱")
	if got, err := e.svc.Place(ctx, a.ID); err != nil || got != localID {
		t.Fatalf("自動擺放：%q %v", got, err)
	}
	// 這台滿了（上限 1、已有 1 台開機）→ 新 Bot 放到有空位的 cloud-01。
	one := 1
	if _, err := e.svc.SetLimits(ctx, localID, nil, &one); err != nil {
		t.Fatal(err)
	}
	if err := e.db.UpsertBox(ctx, store.Box{BotID: a.ID, HostID: localID, State: "running"}); err != nil {
		t.Fatal(err)
	}
	b := newBot(t, e.db, "小編")
	if got, _ := e.svc.Place(ctx, b.ID); got != cloud {
		t.Fatalf("這台滿了應該放到 cloud-01：%q", got)
	}
	if got, _ := e.db.GetBot(ctx, b.ID); got.HostID != cloud {
		t.Fatalf("擺放結果要記在 bot.host_id：%q", got.HostID)
	}

	// cloud-01 上限 1：b 開機後，c 要排隊「等電腦空出來」。
	release, err := e.svc.Admit(ctx, b.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newBot(t, e.db, "數據喵")
	_ = e.db.SetBotHost(ctx, c.ID, cloud)
	waits := make(chan Wait, 10)
	admitted := make(chan func(), 1)
	go func() {
		r, err := e.svc.Admit(ctx, c.ID, func(w Wait) { waits <- w })
		if err == nil {
			admitted <- r
		}
	}()
	w := <-waits
	if w.Reason != WaitHostFull || w.Position != 1 || w.HostName != "cloud-01" || w.MaxRunning != 1 {
		t.Fatalf("排隊原因：%+v", w)
	}
	if v, _ := e.svc.Get(ctx, cloud); v.Queued != 1 {
		t.Fatalf("排隊數：%d", v.Queued)
	}
	// b 開好機（box running）、釋放名額：c 還是要等（b 佔著那一台）。
	_ = e.db.UpsertBox(ctx, store.Box{BotID: b.ID, HostID: cloud, State: "running"})
	release()
	select {
	case <-admitted:
		t.Fatal("cloud-01 還是滿的，不應該放行")
	case <-time.After(300 * time.Millisecond):
	}
	// b 休眠 → c 開機。
	_ = e.db.SetBoxState(ctx, b.ID, "sleeping", "")
	select {
	case r := <-admitted:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("有空位後應該放行")
	}

	// 主機離線：排隊（host_offline），停止工作時結束排隊。
	e.sshd.ln.Close()
	e.sshd.dropAll()
	e.waitStatus(cloud, store.HostOffline)
	cctx, cancel := context.WithCancel(ctx)
	got := make(chan error, 1)
	go func() {
		_, err := e.svc.Admit(cctx, c.ID, func(w Wait) { waits <- w })
		got <- err
	}()
	if w := <-waits; w.Reason != WaitHostOffline {
		t.Fatalf("離線原因：%+v", w)
	}
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("停止排隊：%v", err)
	}
	if v, _ := e.svc.Get(ctx, cloud); v.Queued != 0 {
		t.Fatalf("停止後不該還在隊伍裡：%d", v.Queued)
	}
}

func TestHostOps(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	reg := ops.New(ops.Config{DB: e.db})
	Register(reg, e.svc)
	owner := ops.Owner("test")
	call := func(name string, in any, o ops.Options) (json.RawMessage, error) {
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, owner, name, raw, o)
		return res.Output, err
	}
	out, err := call("xbot.hosts.install_script", InstallIn{NoTailscale: true}, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var inst InstallOut
	_ = json.Unmarshal(out, &inst)
	if !strings.Contains(inst.Command, "base64 -d | gunzip | sudo sh -s -- --no-tailscale") || !strings.HasPrefix(inst.PublicKey, "ssh-ed25519 ") ||
		!strings.Contains(inst.Script, inst.PublicKey) || inst.User != "xbot" {
		t.Fatalf("安裝指令：%+v", inst)
	}
	if _, err := call("xbot.hosts.add", AddIn{Address: "bad host"}, ops.Options{}); ops.AsError(err).Code != ops.CodeInvalidInput {
		t.Fatalf("位址不對：%v", err)
	}
	out, err = call("xbot.hosts.add", AddIn{Address: fmt.Sprintf("127.0.0.1:%d", e.port), Name: "cloud-01", SkipImage: true}, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var res AddResult
	_ = json.Unmarshal(out, &res)
	if !res.Added || res.Loading {
		t.Fatalf("加入（不傳映像）：%+v", res)
	}
	out, err = call("xbot.hosts.list", ListIn{}, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var list ListOut
	_ = json.Unmarshal(out, &list)
	if len(list.Hosts) != 2 || !list.Hosts[0].ControlCenter || !list.Hosts[0].Default || list.Hosts[1].Name != "cloud-01" {
		t.Fatalf("清單：%+v", list.Hosts)
	}
	// 移除是 confirm、不能代為核准；預演不會真的移除。
	info, _ := reg.Get("xbot.hosts.remove")
	if info.Risk != ops.RiskConfirm || !info.NoDelegate || info.Scope != ops.ScopeAdmin {
		t.Fatalf("remove 的風險：%+v", info)
	}
	if _, err := call("xbot.hosts.remove", IDIn{ID: res.Host.ID}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("xbot.hosts.remove", IDIn{ID: list.Hosts[0].ID}, ops.Options{}); ops.AsError(err).Code != ops.CodeConflict {
		t.Fatalf("移除這台電腦：%v", err)
	}
	if _, err := call("xbot.hosts.set_default", DefaultIn{ID: res.Host.ID}, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.Get(ctx, res.Host.ID); !v.Default {
		t.Fatal("預設主機沒有改")
	}
	if _, err := call("xbot.hosts.remove", IDIn{ID: res.Host.ID}, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	if def := e.svc.defaultHost(ctx); def != e.local.ID() {
		t.Fatalf("預設主機被移除後應改回這台電腦：%q", def)
	}
}

func TestParseTargetAndInstallScript(t *testing.T) {
	for in, want := range map[string]Target{
		"cloud-01":                 {Address: "cloud-01", Port: 22, User: "xbot"},
		"ssh://root@100.64.1.2":    {Address: "100.64.1.2", Port: 22, User: "root"},
		"vps.example.com:2222":     {Address: "vps.example.com", Port: 2222, User: "xbot"},
		"[fd7a:115c:a1e0::1]:2200": {Address: "fd7a:115c:a1e0::1", Port: 2200, User: "xbot"},
	} {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Fatalf("%q → %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a b", "x:0", "x:http"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Fatalf("%q 應該不對", bad)
		}
	}
	if !isTailnet("100.101.7.23:22") || !isTailnet("cloud-01.tail3f2a.ts.net:22") || isTailnet("192.168.1.2:22") {
		t.Fatal("isTailnet")
	}
	s := InstallScript("ssh-ed25519 AAAAC3Nza xbot@home")
	for _, want := range []string{"XBOT_KEY='ssh-ed25519 AAAAC3Nza xbot@home'", "StreamLocalBindUnlink yes", "StreamLocalBindMask 0111",
		"AllowStreamLocalForwarding yes", "DOCKER_MIN=26", "XBOT_DATA='/var/lib/xbot'"} {
		if !strings.Contains(s, want) {
			t.Fatalf("安裝腳本少了 %q", want)
		}
	}
	if strings.Contains(s, "__XBOT_") {
		t.Fatal("安裝腳本還有沒換掉的佔位字")
	}
}

func TestEnsureIdentity(t *testing.T) {
	dir := t.TempDir()
	a, err := EnsureIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EnsureIdentity(dir)
	if err != nil || a.AuthorizedKey != b.AuthorizedKey {
		t.Fatalf("第二次應該讀同一把：%v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "ssh", "id_ed25519"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("私鑰權限：%v %v", fi.Mode(), err)
	}
	if !strings.HasPrefix(a.AuthorizedKey, "ssh-ed25519 ") || !strings.HasPrefix(a.Fingerprint(), "SHA256:") {
		t.Fatalf("公鑰：%q", a.AuthorizedKey)
	}
}
