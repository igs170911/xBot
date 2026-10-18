package box

import (
	"errors"
	"io"
	"net/netip"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"xbot/internal/store"
)

func TestParseAllow(t *testing.T) {
	good := map[string]string{
		"192.168.1.10":                 "192.168.1.10",
		" 192.168.1.0/24 ":             "192.168.1.0/24",
		"192.168.1.77/24":              "192.168.1.0/24",
		"10.0.0.5:8080":                "10.0.0.5:8080",
		"http://192.168.1.10:5000/x?y": "192.168.1.10:5000",
		"http://192.168.1.10/":         "192.168.1.10:80",
		"https://172.16.3.4":           "172.16.3.4:443",
		"100.101.7.23":                 "100.101.7.23",
		"169.254.169.254":              "169.254.169.254",
	}
	for in, want := range good {
		a, err := ParseAllow(in)
		if err != nil || a.String() != want {
			t.Errorf("%q → %q %v，想要 %q", in, a.String(), err, want)
		}
	}
	bad := []string{"", "nas.local", "http://nas.local:5000", "8.8.8.8", "1.1.1.0/24", "127.0.0.1", "10.0.0.0/4",
		"192.168.1.10:0", "192.168.1.10:99999", "fd00::1", "ftp://192.168.1.10", "192.168.1.300"}
	for _, in := range bad {
		if a, err := ParseAllow(in); err == nil {
			t.Errorf("%q 應該不行，得到 %s", in, a)
		}
	}
	if _, err := ParseAllow("8.8.8.8"); !errors.Is(err, ErrNotPrivate) {
		t.Errorf("公開位址要說本來就連得到：%v", err)
	}
	list, err := ParseAllowList([]string{"192.168.1.10", "192.168.1.10", "10.0.0.0/8"})
	if err != nil || len(list) != 2 || list[0].String() != "10.0.0.0/8" {
		t.Fatalf("去重與排序：%v %v", list, err)
	}
	if _, err := ParseAllowList(make([]string, 33)); err == nil {
		t.Fatal("超過上限要擋")
	}
}

func TestBuildRules(t *testing.T) {
	ip := netip.MustParseAddr("172.30.0.9")
	r := buildRules([]ruleBox{{IP: ip, Allow: []Allow{
		{Prefix: netip.MustParsePrefix("192.168.1.10/32"), Port: 8080},
		{Prefix: netip.MustParsePrefix("10.1.0.0/16")},
	}}})
	lines := strings.Split(strings.TrimSpace(r), "\n")
	idx := func(sub string) int {
		return slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, sub) })
	}
	must := []string{
		"*filter", ":XBOT-FWD - [0:0]", ":XBOT-IN - [0:0]", "-F XBOT-FWD", "-F XBOT-IN", "COMMIT",
		"-A XBOT-FWD -i xbot-box0 -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN",
		"-A XBOT-FWD -i xbot-box0 -o xbot-box0 -j DROP",
		"-A XBOT-FWD -i xbot-box0 -p udp -m udp --dport 53 -j RETURN",
		"-A XBOT-FWD -s 172.30.0.9/32 -i xbot-box0 -d 192.168.1.10/32 -p tcp -m tcp --dport 8080 -j RETURN",
		"-A XBOT-FWD -s 172.30.0.9/32 -i xbot-box0 -d 10.1.0.0/16 -j RETURN",
		"-A XBOT-IN -s 172.30.0.9/32 -i xbot-box0 -d 10.1.0.0/16 -j RETURN",
		"-A XBOT-IN -i xbot-box0 -j DROP",
	}
	for _, m := range must {
		if idx(m) < 0 {
			t.Errorf("少了 %q", m)
		}
	}
	for _, p := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8"} {
		i := idx("-A XBOT-FWD -i xbot-box0 -d " + p + " -j DROP")
		if i < 0 {
			t.Fatalf("沒擋 %s", p)
		}
		// 白名單要在擋私有網段之前，否則永遠到不了。
		if i < idx("-d 192.168.1.10/32") {
			t.Fatalf("白名單排在 %s 的 DROP 之後", p)
		}
	}
	if idx("-A XBOT-IN -i xbot-box0 -j DROP") != len(lines)-2 {
		t.Fatal("XBOT-IN 最後要全部擋下")
	}
	if buildRules(nil) == r || !strings.Contains(buildRules(nil), "-A XBOT-IN -i xbot-box0 -j DROP") {
		t.Fatal("沒有白名單時也要有基本規則")
	}
	if buildRules([]ruleBox{{IP: ip}}) != buildRules(nil) {
		t.Fatal("空白名單不會多出規則")
	}
}

// netEnv 的電腦會接上 xbot-box 網路；rules 是 xbot-netguard 裡現在的規則。
func (e *env) rules() string { return e.docker.file(path.Join(guardStateDir, guardRules)) }

func TestNetguardSetup(t *testing.T) {
	e := setup(t)
	var applies [][]string
	e.docker.execRun = func(cmd []string, _ io.Reader, _ io.Writer) int {
		applies = append(applies, cmd)
		return 0
	}
	s := ok[Status](t)(e.m.Start(ctx, e.bot))

	// 網路：XBot 自己的 bridge、關掉容器互通、固定子網路。
	n, has := e.docker.networks[NetworkName]
	if !has || n.Driver != "bridge" || n.Options["com.docker.network.bridge.name"] != BridgeName ||
		n.Options["com.docker.network.bridge.enable_icc"] != "false" || len(n.IPAM.Config) != 1 || n.IPAM.Config[0].Subnet.String() != "172.30.0.0/16" {
		t.Fatalf("網路：%+v", n)
	}
	// xbot-netguard：主機網路、只有 NET_ADMIN／NET_RAW、唯讀根目錄、一直重啟。
	g := e.docker.containers[GuardName]
	if g == nil || !g.State.Running {
		t.Fatal("xbot-netguard 要在跑")
	}
	hc := g.HostConfig
	if hc.NetworkMode != "host" || !slices.Equal(hc.CapDrop, []string{"ALL"}) || !slices.Equal(hc.CapAdd, []string{"NET_ADMIN", "NET_RAW"}) ||
		!hc.ReadonlyRootfs || hc.Privileged || hc.RestartPolicy.Name != "always" || !slices.Contains(hc.SecurityOpt, "no-new-privileges:true") {
		t.Fatalf("xbot-netguard 設定：%+v", hc)
	}
	if g.Config.Image != DefaultImage || !slices.Equal(g.Config.Cmd, []string{guardScript, "run"}) {
		t.Fatalf("xbot-netguard 指令：%v %v", g.Config.Image, g.Config.Cmd)
	}
	// 規則寫進去並套用（apply 成功才開機）。
	if !strings.Contains(e.rules(), "-A XBOT-IN -i xbot-box0 -j DROP") || len(applies) == 0 || !slices.Equal(applies[0], []string{guardScript, "apply"}) {
		t.Fatalf("規則：%q apply：%v", e.rules(), applies)
	}
	// 電腦在網路上有固定 IP，記在 box 表。
	ip := e.docker.ipOf(ContainerName(e.bot))
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	if !netip.MustParsePrefix("172.30.0.0/16").Contains(ip) || row.NetIP != ip.String() || ip == netip.MustParseAddr("172.30.0.1") {
		t.Fatalf("IP：%s，box 表 %q", ip, row.NetIP)
	}
	if !s.Running {
		t.Fatalf("開機：%+v", s)
	}

	// 規則沒變時不再寫（只確認 xbot-netguard 還在）。
	n0 := len(applies)
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if len(applies) != n0 {
		t.Fatalf("規則沒變不用再套用：%d → %d", n0, len(applies))
	}

	// 白名單：立刻更新規則（以這台電腦的 IP 為來源）。
	allow := ok[[]Allow](t)(e.m.SetNetAllow(ctx, e.bot, []string{"http://192.168.1.10:8080", "10.9.0.0/16"}))
	if len(allow) != 2 || !strings.Contains(e.rules(), "-s "+ip.String()+"/32 -i xbot-box0 -d 192.168.1.10/32 -p tcp -m tcp --dport 8080 -j RETURN") {
		t.Fatalf("白名單規則：%v\n%s", allow, e.rules())
	}
	if got := ok[[]Allow](t)(e.m.NetAllow(ctx, e.bot)); len(got) != 2 {
		t.Fatalf("讀回白名單：%v", got)
	}
	if _, err := e.m.SetNetAllow(ctx, e.bot, []string{"8.8.8.8"}); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("公開位址：%v", err)
	}

	// 重建：IP 不變（白名單規則仍然對）。
	ok[Status](t)(e.m.Rebuild(ctx, e.bot))
	if got := e.docker.ipOf(ContainerName(e.bot)); got != ip {
		t.Fatalf("重建後 IP 換了：%s → %s", ip, got)
	}

	// 連資料一起刪：白名單與規則一起清掉。
	ok[Status](t)(e.m.Remove(ctx, e.bot, true))
	if strings.Contains(e.rules(), ip.String()) {
		t.Fatalf("刪掉的電腦的 IP 還在規則裡：\n%s", e.rules())
	}
	if got := ok[[]Allow](t)(e.m.NetAllow(ctx, e.bot)); len(got) != 0 {
		t.Fatalf("白名單要一起刪：%v", got)
	}
}

func TestNetguardFailClosed(t *testing.T) {
	e := setup(t)
	e.docker.execRun = func([]string, io.Reader, io.Writer) int { return 1 } // apply 失敗（例：主機不能用 iptables）
	_, err := e.m.Start(ctx, e.bot)
	var ne *NetError
	if !errors.As(err, &ne) || !strings.Contains(err.Error(), "SEC-4") {
		t.Fatalf("網路防護沒生效時不能開機：%v", err)
	}
	if e.boxes() != 0 {
		t.Fatal("不能建出沒有防護的電腦")
	}
	// 修好之後可以開機。
	e.docker.execRun = nil
	if s := ok[Status](t)(e.m.Start(ctx, e.bot)); !s.Running {
		t.Fatalf("修好後開機：%+v", s)
	}
}

func TestNetguardSubnetAndForeignNetwork(t *testing.T) {
	e := setup(t)
	// 候選子網路被別的 Docker 網路用掉了：換下一個。
	e.docker.networks["other"] = network.Inspect{Network: network.Network{Name: "other", Driver: "bridge",
		IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: netip.MustParsePrefix("172.30.5.0/24")}}}}}
	ok[Status](t)(e.m.Create(ctx, e.bot))
	if got := e.docker.networks[NetworkName].IPAM.Config[0].Subnet.String(); got != "172.29.0.0/16" {
		t.Fatalf("子網路：%s", got)
	}

	// 已經有一個不是 XBot 建的 xbot-box 網路：不要用它。
	f := setup(t)
	f.docker.networks[NetworkName] = network.Inspect{Network: network.Network{Name: NetworkName, Driver: "macvlan"}}
	if _, err := f.m.Start(ctx, f.bot); err == nil || !strings.Contains(err.Error(), "不是 XBot 建的") {
		t.Fatalf("別人的網路：%v", err)
	}
}

func TestNetguardRecreatesOldGuard(t *testing.T) {
	e := setup(t)
	ok[Status](t)(e.m.Start(ctx, e.bot))
	old := e.docker.containers[GuardName].ID
	e.docker.containers[GuardName].Config.Labels[LabelNetguard] = "0"
	e.m.guard.forget(e.host.id)
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if g := e.docker.containers[GuardName]; g == nil || g.ID == old || g.Config.Labels[LabelNetguard] != guardRevision {
		t.Fatal("舊版的 xbot-netguard 要重建")
	}
	// 被停掉的 xbot-netguard 會被叫起來。
	_, _ = e.docker.ContainerStop(ctx, GuardName, client.ContainerStopOptions{})
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if !e.docker.containers[GuardName].State.Running {
		t.Fatal("xbot-netguard 要重新開起來")
	}
}

func TestIPsAreUnique(t *testing.T) {
	e := setup(t)
	seen := map[netip.Addr]bool{}
	for i := range 20 {
		b := ok[store.Bot](t)(e.db.CreateBot(ctx, store.Bot{Name: "b" + string(rune('a'+i))}))
		ok[Status](t)(e.m.Create(ctx, b.ID))
		ip := e.docker.ipOf(ContainerName(b.ID))
		if !ip.IsValid() || seen[ip] {
			t.Fatalf("IP 重複或沒有：%s", ip)
		}
		seen[ip] = true
	}
}

func TestNoNetGuard(t *testing.T) {
	e := setup(t)
	m := New(e.db, e.m.hosts, Options{NoNetGuard: true})
	s := ok[Status](t)(m.Start(ctx, e.bot))
	if !s.Running || e.docker.containers[GuardName] != nil || len(e.docker.networks) != 0 {
		t.Fatalf("關掉網路防護：%+v", s)
	}
	o := e.lastCreate()
	if o.HostConfig.NetworkMode != "" || o.Config.Labels[LabelSpec] != specRevision+"-nonet" {
		t.Fatalf("沒有網路防護的電腦：%s %v", o.HostConfig.NetworkMode, o.Config.Labels)
	}
	// 之後打開網路防護：沒開機的舊電腦在開機前重建，接上 xbot-box 網路（資料保留）。
	ok[Status](t)(m.Stop(ctx, e.bot))
	s = ok[Status](t)(e.m.Start(ctx, e.bot))
	if !s.Running || e.creates() != 2 || !e.docker.ipOf(ContainerName(e.bot)).IsValid() || e.vols() != 4 {
		t.Fatalf("打開網路防護後重建：%+v creates=%d", s, e.creates())
	}
}
