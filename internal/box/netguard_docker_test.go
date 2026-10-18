//go:build docker

package box

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/store"
)

// SEC-4 的整合測試：真的開一台 Bot 電腦，確認它
//   - 連不到主機：127.0.0.1 上的服務、bridge 閘道與 docker0 上的服務、主機的其他位址；
//   - 連不到私有網段 10.x、192.168.x、172.16.x，也連不到同一個網路上的其他電腦；
//   - 連得到「網際網路」：測試用一個假的網際網路（198.51.100.0/24 的另一個 Docker 網路），
//     因為 Docker 本來就會隔開不同的 bridge，測試在 DOCKER-USER 裡、XBot 的規則之後，放行這幾個測試網路；
//     所以「通」或「不通」只取決於 XBot 的規則。有真的網際網路時順便看 1.1.1.1:443；
//   - 白名單打開的私有位址連得到，關掉後又連不到。
//
// 做完把測試用的容器、網路、規則刪掉（xbot-box 網路與 xbot-netguard 留著，其他 XBot 也在用）。
func TestDockerNetguard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("沒有 docker 指令")
	}
	docker := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	bot, err := db.CreateBot(ctx, store.Bot{Name: "網路測試"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, hosts.NewRegistry(host), Options{Image: image})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := m.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	})
	if _, err := m.Start(ctx, bot.ID); err != nil {
		t.Fatalf("開機：%v", err)
	}
	row, err := db.GetBox(ctx, bot.ID)
	if err != nil || row.NetIP == "" {
		t.Fatalf("box 表：%+v %v", row, err)
	}
	gw := m.guard.host(host.ID()).gateway
	t.Logf("電腦 %s，閘道 %s", row.NetIP, gw)

	// probe 從電腦裡連 addr:port（TCP），回答連不連得上。
	box := ContainerName(bot.ID)
	probe := func(from, ip string, port int) bool {
		out, err := docker("exec", from, "timeout", "4", "bash", "-c", fmt.Sprintf("exec 3<>/dev/tcp/%s/%d", ip, port))
		_ = out
		return err == nil
	}
	ipt := func(args ...string) error {
		out, err := docker(append([]string{"exec", GuardName, guardScript, "ipt"}, args...)...)
		if err != nil {
			return fmt.Errorf("%v：%s", err, out)
		}
		return nil
	}

	// ── 主機 ───────────────────────────────
	// 主機上聽所有介面的服務：電腦經閘道、docker0、主機的其他位址都連不到。
	anyLn, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer anyLn.Close()
	loLn, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer loLn.Close()
	for _, ln := range []net.Listener{anyLn, loLn} {
		go func(ln net.Listener) {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}(ln)
	}
	anyPort, loPort := anyLn.Addr().(*net.TCPAddr).Port, loLn.Addr().(*net.TCPAddr).Port
	hostAddrs := []string{gw.String()}
	if br, err := cli.NetworkInspect(ctx, "bridge", client.NetworkInspectOptions{}); err == nil {
		for _, c := range br.Network.IPAM.Config {
			if c.Gateway.IsValid() {
				hostAddrs = append(hostAddrs, c.Gateway.String()) // docker0（host.docker.internal 指的就是它）
			}
		}
	}
	if ifs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() && !strings.HasPrefix(p.Addr().String(), "172.") {
				hostAddrs = append(hostAddrs, p.Addr().String()) // 主機在區網、tailnet 的位址
			}
		}
	}
	// 對照組：沒有網路防護的容器（Docker 預設網路）連得到主機嗎？主機自己的防火牆擋掉時，這組比較沒有意義，只記下來。
	control := "xbot-test-netctl-" + strconv.Itoa(os.Getpid())
	if _, err := docker("run", "-d", "--rm", "--name", control, "--entrypoint", "sleep", image, "300"); err != nil {
		t.Fatalf("對照容器：%v", err)
	}
	defer docker("rm", "-f", control) //nolint:errcheck
	for _, a := range hostAddrs {
		if probe(box, a, anyPort) {
			t.Errorf("電腦連得到主機 %s:%d（SEC-4 要擋）", a, anyPort)
		}
		t.Logf("主機 %s:%d：電腦連不到；沒有防護的容器 %v", a, anyPort, map[bool]string{true: "連得到", false: "也連不到（主機防火牆）"}[probe(control, a, anyPort)])
	}
	if probe(box, "127.0.0.1", loPort) || probe(box, gw.String(), loPort) {
		t.Errorf("電腦連得到主機 127.0.0.1:%d 上的服務", loPort)
	}

	// ── 私有網段與假的網際網路 ───────────────
	if err := ipt("-n", "-L", "DOCKER-USER"); err != nil {
		t.Skipf("這台 Docker 沒有 DOCKER-USER 鏈（nftables 防火牆後端），略過私有網段的測試：%v", err)
	}
	tag := strconv.Itoa(os.Getpid())
	type target struct {
		name, subnet, ip string
		allowed          bool // 預設應該連得到
	}
	targets := []target{
		{"inet", "198.51.100.0/24", "198.51.100.10", true},
		{"p10", "10.250.77.0/24", "10.250.77.10", false},
		{"p192", "192.168.250.0/24", "192.168.250.10", false},
		{"p172", "172.16.250.0/24", "172.16.250.10", false},
	}
	for _, tg := range targets {
		netName, bridge, web := "xbot-test-"+tg.name+"-"+tag, "xbt"+tg.name+tag, "xbot-test-web-"+tg.name+"-"+tag
		if len(bridge) > 15 {
			bridge = bridge[:15]
		}
		if out, err := docker("network", "create", "--subnet", tg.subnet, "-o", "com.docker.network.bridge.name="+bridge,
			"-o", "com.docker.network.bridge.gateway_mode_ipv4=nat-unprotected", netName); err != nil {
			t.Logf("建不了測試網路 %s（%s），略過：%s", tg.subnet, err, out)
			continue
		}
		defer docker("network", "rm", netName) //nolint:errcheck
		if out, err := docker("run", "-d", "--rm", "--name", web, "--network", netName, "--ip", tg.ip, "--entrypoint", "python3", image,
			"-m", "http.server", "8080"); err != nil {
			t.Fatalf("測試服務 %s：%v %s", tg.ip, err, out)
		}
		defer docker("rm", "-f", web) //nolint:errcheck
		// 在 XBot 的規則之後放行電腦和這個網路之間的流量（Docker 本來會隔開不同的 bridge）。
		for _, rule := range [][]string{{"-i", BridgeName, "-o", bridge, "-j", "ACCEPT"}, {"-i", bridge, "-o", BridgeName, "-j", "ACCEPT"}} {
			if err := ipt(append([]string{"-I", "DOCKER-USER", "2"}, rule...)...); err != nil {
				t.Fatal(err)
			}
			defer ipt(append([]string{"-D", "DOCKER-USER"}, rule...)...) //nolint:errcheck
		}
		reach := false
		for range 10 { // 等測試服務起來
			if reach = probe(box, tg.ip, 8080); reach || !tg.allowed {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if reach != tg.allowed {
			t.Errorf("%s（%s）：連得到＝%v，應該是 %v", tg.name, tg.ip, reach, tg.allowed)
		} else {
			t.Logf("%s（%s）：連得到＝%v ✓", tg.name, tg.ip, reach)
		}
	}

	// ── 白名單 ───────────────────────────────
	if _, err := m.SetNetAllow(ctx, bot.ID, []string{"10.250.77.10:8080"}); err != nil {
		t.Fatal(err)
	}
	if !probe(box, "10.250.77.10", 8080) {
		t.Error("白名單打開的 10.250.77.10:8080 應該連得到")
	}
	if probe(box, "192.168.250.10", 8080) {
		t.Error("沒在白名單的 192.168.250.10 還是要擋")
	}
	if _, err := m.SetNetAllow(ctx, bot.ID, nil); err != nil {
		t.Fatal(err)
	}
	if probe(box, "10.250.77.10", 8080) {
		t.Error("關掉白名單後 10.250.77.10 又要連不到")
	}

	// ── 同一個網路上的其他電腦 ─────────────────
	peer := "xbot-test-peer-" + tag
	peerIP := netip.MustParseAddr(row.NetIP).Next().String()
	if out, err := docker("run", "-d", "--rm", "--name", peer, "--network", NetworkName, "--ip", peerIP, "--entrypoint", "python3", image,
		"-m", "http.server", "8080"); err != nil {
		t.Logf("開不了同網路的測試電腦（%s），略過：%s", err, out)
	} else {
		defer docker("rm", "-f", peer) //nolint:errcheck
		time.Sleep(time.Second)
		if probe(box, peerIP, 8080) {
			t.Error("電腦之間不能互連")
		}
	}

	// ── 真的網際網路（有的話）───────────────────
	if probe(control, "1.1.1.1", 443) {
		if !probe(box, "1.1.1.1", 443) {
			t.Error("有網際網路時電腦應該連得到 1.1.1.1:443")
		} else {
			t.Log("真的網際網路 1.1.1.1:443：連得到 ✓")
		}
		if out, err := docker("exec", box, "curl", "-sS", "-m", "10", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com/"); err != nil {
			t.Errorf("電腦裡的 DNS 與 HTTPS：%v %s", err, out)
		}
	} else {
		t.Log("這台沒有網際網路，略過真的網際網路檢查")
	}
}
