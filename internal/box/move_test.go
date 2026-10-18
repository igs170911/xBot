package box

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"xbot/internal/limits"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// volumeTar 做一份像 docker cp（CopyFromContainer /xbot-move/home）那樣的 tar。
func volumeTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(&tar.Header{Name: "home/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 1000}, "")
	add(&tar.Header{Name: "home/chrome-profile/", Typeflag: tar.TypeDir, Mode: 0o700, Uid: 1000}, "")
	add(&tar.Header{Name: "home/chrome-profile/Cookies", Typeflag: tar.TypeReg, Mode: 0o600, Uid: 1000}, "session=1")
	add(&tar.Header{Name: "home/空 白.txt", Typeflag: tar.TypeReg, Mode: 0o644}, "")
	add(&tar.Header{Name: "home/link", Typeflag: tar.TypeSymlink, Linkname: "chrome-profile"}, "")
	add(&tar.Header{Name: "home/hard", Typeflag: tar.TypeLink, Linkname: "home/chrome-profile/Cookies"}, "")
	add(&tar.Header{Name: "home/fifo", Typeflag: tar.TypeFifo}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	// docker cp 的 tar 後面常有多出來的補零。
	buf.Write(make([]byte, 4096))
	return buf.Bytes()
}

func TestManifestRead(t *testing.T) {
	m := newManifest()
	r := bytes.NewReader(volumeTar(t))
	if err := m.Read(r); err != nil {
		t.Fatal(err)
	}
	if r.Len() != 0 {
		t.Fatal("tar 結尾後的資料也要讀完（新主機那邊才不會卡住）")
	}
	want := map[string]Entry{
		"home":                        {Type: 'd'},
		"home/chrome-profile":         {Type: 'd'},
		"home/chrome-profile/Cookies": {Type: 'f', Size: 9, Sum: sum("session=1")},
		"home/空 白.txt":                {Type: 'f', Size: 0, Sum: sum("")},
		"home/link":                   {Type: 'l'},
		"home/hard":                   {Type: 'f', Size: 9, Sum: sum("session=1")},
	}
	if len(m.Entries) != len(want) {
		t.Fatalf("項目：%+v", m.Entries)
	}
	for k, v := range want {
		if m.Entries[k] != v {
			t.Errorf("%s：%+v，想要 %+v", k, m.Entries[k], v)
		}
	}
	if n, size := m.Totals(); n != 3 || size != 18 {
		t.Fatalf("合計：%d 個 %d 位元組", n, size)
	}
}

// targetOutput 是新主機輔助容器 verifyScript 的輸出（find -printf '%y %s %p\0' 與 sha256sum -z）。
func targetOutput(entries map[string]Entry) (tree, sums []byte) {
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range entries {
			if !yield(k) {
				return
			}
		}
	})
	for _, n := range names {
		e := entries[n]
		size := "4096"
		if e.Type == 'f' {
			size = strconv.FormatInt(e.Size, 10)
		}
		tree = append(tree, []byte(string(e.Type)+" "+size+" "+n+"\x00")...)
		if e.Type == 'f' {
			sums = append(sums, []byte(e.Sum+"  "+n+"\x00")...)
		}
	}
	// find 也會列出 tar 帶不過去的 socket：不算。
	tree = append(tree, []byte("s 0 home/.X11-unix\x00")...)
	return tree, sums
}

func TestManifestVerify(t *testing.T) {
	sent := newManifest()
	if err := sent.Read(bytes.NewReader(volumeTar(t))); err != nil {
		t.Fatal(err)
	}
	same := func() map[string]Entry {
		out := map[string]Entry{}
		for k, v := range sent.Entries {
			out[k] = v
		}
		return out
	}
	tree, sums := targetOutput(same())
	got, err := parseManifest(tree, sums)
	if err != nil {
		t.Fatal(err)
	}
	if err := sent.Compare(got); err != nil {
		t.Fatalf("一樣的資料要通過驗證：%v", err)
	}

	cases := map[string]func(m map[string]Entry){
		"少了": func(m map[string]Entry) { delete(m, "home/chrome-profile/Cookies") },
		"多了": func(m map[string]Entry) { m["home/extra"] = Entry{Type: 'f', Size: 1, Sum: sum("x")} },
		"內容不同": func(m map[string]Entry) {
			e := m["home/chrome-profile/Cookies"]
			e.Sum = sum("session=2")
			m["home/chrome-profile/Cookies"] = e
		},
		"大小不同": func(m map[string]Entry) {
			e := m["home/hard"]
			e.Size = 8
			m["home/hard"] = e
		},
		"種類不同": func(m map[string]Entry) { m["home/link"] = Entry{Type: 'f', Size: 0, Sum: sum("")} },
	}
	for want, change := range cases {
		m := same()
		change(m)
		tree, sums := targetOutput(m)
		got, err := parseManifest(tree, sums)
		if err != nil {
			t.Fatal(err)
		}
		err = sent.Compare(got)
		if !errors.Is(err, ErrVerify) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s：%v", want, err)
		}
	}
	if _, err := parseManifest([]byte("f x home/a\x00"), nil); err == nil {
		t.Error("看不懂的清單要回錯誤")
	}
	if _, err := parseManifest([]byte("f 1 home/a\x00"), []byte(sum("a")+"  home/b\x00")); err == nil {
		t.Error("檢查碼裡有清單沒有的檔案要回錯誤")
	}
}

func TestParseMeasure(t *testing.T) {
	if got := parseMeasure([]byte("10\n20\n\n5\n")); got != 35 {
		t.Fatal(got)
	}
}

func TestHelperOptions(t *testing.T) {
	o := helperOptions("img", "b1", "src", true, measureScript)
	hc := o.HostConfig
	if o.Name != "xbot-move-b1-src" || o.Config.User != "0:0" || !o.Config.NetworkDisabled || hc.NetworkMode != "none" {
		t.Fatalf("輔助容器：%+v %+v", o.Config, hc)
	}
	if !slices.Equal(hc.CapDrop, []string{"ALL"}) || !slices.Equal(hc.CapAdd, []string{"DAC_READ_SEARCH"}) || len(hc.Mounts) != 3 {
		t.Fatalf("權限與掛載：%+v", hc)
	}
	for _, m := range hc.Mounts {
		if !m.ReadOnly || !strings.HasPrefix(m.Target, "/xbot-move/") || !strings.HasPrefix(m.Source, "xbot-box-b1-") {
			t.Fatalf("原主機唯讀掛上：%+v", m)
		}
		if strings.HasSuffix(m.Source, "-cred") {
			t.Fatal("訂閱憑證不搬")
		}
	}
	if d := helperOptions("img", "b1", "dst", false, verifyScript); d.HostConfig.Mounts[0].ReadOnly || d.Name != "xbot-move-b1-dst" {
		t.Fatalf("新主機要能寫：%+v", d.HostConfig.Mounts)
	}
}

func TestEgressAddrAndRules(t *testing.T) {
	sub := netip.MustParsePrefix("172.30.0.0/16")
	eg := EgressAddr(sub)
	if eg.String() != "172.30.255.254" {
		t.Fatalf("小容器的 IP：%s", eg)
	}
	if EgressAddr(netip.MustParsePrefix("10.213.4.0/24")).String() != "10.213.4.254" {
		t.Fatal("/24")
	}
	if ProxyURL(eg) != "http://172.30.255.254:1055" {
		t.Fatal(ProxyURL(eg))
	}
	box := netip.MustParseAddr("172.30.0.9")
	r := buildRules([]ruleBox{{IP: box, Egress: eg}, {IP: netip.MustParseAddr("172.30.0.10")}})
	lines := strings.Split(r, "\n")
	idx := func(sub string) int {
		return slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, sub) })
	}
	allow := idx("-A XBOT-FWD -s 172.30.0.9/32 -i xbot-box0 -o xbot-box0 -d 172.30.255.254/32 -p tcp -m tcp --dport 1055 -j ACCEPT")
	reply := idx("-A XBOT-FWD -s 172.30.255.254/32 -i xbot-box0 -o xbot-box0 -d 172.30.0.9/32 -p tcp -m tcp --sport 1055 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT")
	drop := idx("-A XBOT-FWD -i xbot-box0 -o xbot-box0 -j DROP")
	if allow < 0 || reply < 0 || drop < 0 || allow > drop || reply > drop {
		t.Fatalf("只放行這台電腦到小容器的 proxy 埠，而且要在電腦互連的 DROP 之前：\n%s", r)
	}
	if strings.Contains(r, "172.30.0.10/32 -i xbot-box0 -o") {
		t.Fatal("沒開的電腦不能連小容器")
	}
}

func TestProxyEnvOnBox(t *testing.T) {
	o := createOptions(spec{BotID: "b1", Image: "img", IPCDir: "/x", Limits: DefaultLimits(), Proxy: "http://172.30.255.254:1055"})
	env := strings.Join(o.Config.Env, "\n")
	for _, want := range []string{"HTTPS_PROXY=http://172.30.255.254:1055", "http_proxy=http://172.30.255.254:1055", "NO_PROXY=localhost,127.0.0.1,::1"} {
		if !strings.Contains(env, want) {
			t.Errorf("少了 %s", want)
		}
	}
	if o.Config.Labels[LabelProxy] == "" {
		t.Fatal("要標記建立時帶了 proxy")
	}
	plain := createOptions(spec{BotID: "b1", Image: "img", IPCDir: "/x", Limits: DefaultLimits()})
	if strings.Contains(strings.Join(plain.Config.Env, " "), "PROXY") || plain.Config.Labels[LabelProxy] != "" {
		t.Fatal("沒開時不帶 proxy")
	}
}

func TestEgressOptions(t *testing.T) {
	cfg := EgressConfig{ExitNode: "home-pc", AuthKey: "tskey-auth-x"}
	o := egressOptions(cfg, egressHostname(cfg, "0123456789abcdef"), egressDigest(cfg, "h"), netip.MustParseAddr("172.30.255.254"))
	env := strings.Join(o.Config.Env, "\n")
	for _, want := range []string{"TS_USERSPACE=true", "TS_EXTRA_ARGS=--exit-node=home-pc", "TS_OUTBOUND_HTTP_PROXY_LISTEN=0.0.0.0:1055",
		"TS_HOSTNAME=xbot-egress-89abcdef", "TS_AUTHKEY=tskey-auth-x"} {
		if !strings.Contains(env, want) {
			t.Errorf("少了 %s", want)
		}
	}
	if o.Config.Image != limits.EgressImage || !slices.Equal(o.HostConfig.CapDrop, []string{"ALL"}) || len(o.HostConfig.CapAdd) != 0 ||
		string(o.HostConfig.NetworkMode) != NetworkName || o.HostConfig.Privileged {
		t.Fatalf("小容器不需要任何權限：%+v", o.HostConfig)
	}
	if egressDigest(cfg, "h") == egressDigest(EgressConfig{ExitNode: "other", AuthKey: "tskey-auth-x"}, "h") {
		t.Fatal("換 exit node 要重建")
	}
	for _, bad := range []string{"", "a b", "-x", "home;rm"} {
		if ValidExitNode(bad) == nil {
			t.Errorf("%q 要擋", bad)
		}
	}
	for _, ok := range []string{"home-pc", "100.101.7.23", "home-pc.tail1234.ts.net"} {
		if err := ValidExitNode(ok); err != nil {
			t.Errorf("%q：%v", ok, err)
		}
	}
}

func TestParseTailscaleStatus(t *testing.T) {
	var st EgressState
	parseTailscaleStatus([]byte(`{"BackendState":"Running","ExitNodeStatus":{"Online":true},
		"Peer":{"k":{"HostName":"home-pc","ExitNode":true,"Online":true},"j":{"HostName":"phone","Online":true}}}`), &st)
	if st.Backend != "Running" || st.ExitNode != "home-pc" || !st.ExitNodeOnline {
		t.Fatalf("%+v", st)
	}
}
