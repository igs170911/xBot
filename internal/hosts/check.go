package hosts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"

	"xbot/internal/limits"
)

// 檢查項目（J11 步驟 2）。
const (
	CheckSSH        = "ssh"
	CheckDocker     = "docker"
	CheckResources  = "resources"
	CheckArch       = "arch"
	CheckForwarding = "forwarding"
	CheckImage      = "image"
	CheckNetwork    = "network"
)

// 檢查結果。
const (
	CheckOK      = "ok"
	CheckWarn    = "warn"    // 可以用，但建議處理
	CheckFail    = "fail"    // 不能加入
	CheckPending = "pending" // 還沒做（前面的項目沒過），或映像還要傳過去
	CheckRunning = "running" // 正在做（映像傳輸中）
)

// CheckItem 是一項自動檢查。
type CheckItem struct {
	ID     string `json:"id" desc:"ssh、docker、resources、arch、forwarding、image、network"`
	Label  string `json:"label"`
	Status string `json:"status" desc:"ok、warn、fail、pending、running"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty" desc:"沒過時怎麼修"`
	// Progress 是映像傳輸進度 0～100（只有 image 項目、傳輸中才有）。
	Progress int `json:"progress,omitempty"`
}

// Resources 是主機的資源（存在 host.resources）。
type Resources struct {
	CPUs           int    `json:"cpus"`
	MemBytes       int64  `json:"mem_bytes"`
	DiskFreeBytes  int64  `json:"disk_free_bytes"`
	DiskTotalBytes int64  `json:"disk_total_bytes"`
	DockerVersion  string `json:"docker_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch" desc:"amd64 或 arm64"`
	Hostname       string `json:"hostname"`
	RTTMillis      int64  `json:"rtt_ms"`
	ImageID        string `json:"image_id,omitempty" desc:"主機上的電腦映像版本"`
	Tailscale      bool   `json:"tailscale" desc:"經 Tailscale 連線"`
}

// Report 是一次主機檢查的結果。
type Report struct {
	Items     []CheckItem `json:"items"`
	Resources Resources   `json:"resources"`
	// OK：沒有 fail 的項目（映像還沒傳過去不算 fail）。
	OK bool `json:"ok"`
	// NeedImage：主機上沒有（或不是同一版的）電腦映像，要由 XBot 傳過去。
	NeedImage bool `json:"need_image"`
}

// Item 回傳某一項。
func (r *Report) Item(id string) *CheckItem {
	for i := range r.Items {
		if r.Items[i].ID == id {
			return &r.Items[i]
		}
	}
	return nil
}

func (r *Report) set(id, status, detail, fix string) {
	if it := r.Item(id); it != nil {
		it.Status, it.Detail, it.Fix = status, detail, fix
	}
}

func (r *Report) finish() {
	r.OK = true
	for _, it := range r.Items {
		if it.Status == CheckFail {
			r.OK = false
		}
	}
}

func newReport() Report {
	return Report{Items: []CheckItem{
		{ID: CheckSSH, Label: "SSH 連線", Status: CheckPending},
		{ID: CheckDocker, Label: "Docker", Status: CheckPending},
		{ID: CheckResources, Label: "CPU／記憶體／磁碟", Status: CheckPending},
		{ID: CheckArch, Label: "架構", Status: CheckPending},
		{ID: CheckForwarding, Label: "SSH 轉發", Status: CheckPending},
		{ID: CheckImage, Label: "電腦映像", Status: CheckPending},
		{ID: CheckNetwork, Label: "連線方式", Status: CheckPending},
	}}
}

// ImageRef 是 XBot 這台的 Bot 電腦映像（要放到每台主機上的那一個）。
type ImageRef struct {
	Name string
	ID   string // 空白＝這台 XBot 沒有這個映像
	Arch string // amd64／arm64
	Size int64
	// Layers 是內容指紋（各層 diff id 的雜湊），用來比對不同 Docker 上是不是同一個映像。
	Layers string
}

// NormalizeArch 把 Docker／uname 的架構名稱統一成 amd64、arm64（其他原樣回傳）。
func NormalizeArch(a string) string {
	switch strings.ToLower(a) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return a
}

// SSHErrorFix 把 SSH 連線錯誤翻成白話的原因與修正方法。
func SSHErrorFix(addr string, err error) (string, string) {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrHostKeyMismatch):
		return "主機金鑰和第一次連線時不同", "可能有人冒充這台主機，或主機重灌過。確定是重灌的話，先移除這台主機再重新加入"
	case strings.Contains(msg, "unable to authenticate"):
		return "XBot 的公鑰不在 " + limits.HostSSHUser + " 帳號裡", "在主機上再貼一次第 1 步的指令（會補上公鑰）"
	case strings.Contains(msg, "connection refused"):
		return "連得到主機，但 SSH 沒有在 " + addr + " 上服務", "確認主機的 SSH 有開（第 1 步的指令會安裝並啟動），以及位址和埠號正確"
	case strings.Contains(msg, "no such host"):
		return "找不到這個主機名稱", "確認名稱拼對了；Tailscale 名稱要開 MagicDNS，或改填 Tailscale IP（100.x.y.z）"
	}
	return "連不上 " + addr, "確認主機已開機、已加入你的 tailnet，而且 Tailscale ACL 允許這台 XBot 連主機的 22 埠"
}

// Inspect 對一台已經（或正在）連線的 SSH 主機做 J11 步驟 2 的自動檢查。不會改主機上的任何東西
// （只建立再刪掉一個測試用的 socket）。img 是要放到主機上的電腦映像。
func (s *SSH) Inspect(ctx context.Context, img ImageRef) (r Report) {
	r = newReport()
	defer r.finish()
	addr := s.o.Addr

	cl, err := s.waitChecked(ctx)
	if err != nil {
		cause := err
		if h := s.Health(); h.LastError != "" {
			cause = errors.New(h.LastError)
			if h.State == SSHFailed {
				cause = fmt.Errorf("%w：%s", ErrHostKeyMismatch, h.LastError)
			}
		}
		detail, fix := SSHErrorFix(addr, cause)
		r.set(CheckSSH, CheckFail, detail+"（"+cause.Error()+"）", fix)
		return r
	}
	start := time.Now()
	if _, _, err := cl.SendRequest("keepalive@openssh.com", true, nil); err == nil {
		r.Resources.RTTMillis = time.Since(start).Milliseconds()
	}
	r.set(CheckSSH, CheckOK, fmt.Sprintf("ssh://%s@%s · 往返 %d ms", s.o.User, addr, r.Resources.RTTMillis), "")

	// Docker
	info, err := s.docker.Info(ctx, client.InfoOptions{})
	if err != nil {
		r.set(CheckDocker, CheckFail, "Docker 不能用："+err.Error(), "確認 Docker 有在執行，且 "+s.o.User+" 帳號在 docker 群組（再貼一次第 1 步的指令）")
		return r
	}
	in := info.Info
	r.Resources.DockerVersion, r.Resources.CPUs, r.Resources.MemBytes = in.ServerVersion, in.NCPU, in.MemTotal
	r.Resources.OS, r.Resources.Hostname = in.OperatingSystem, in.Name
	if major := dockerMajor(in.ServerVersion); major < limits.HostMinDockerMajor {
		r.set(CheckDocker, CheckFail, fmt.Sprintf("Docker %s 太舊", in.ServerVersion),
			fmt.Sprintf("需要 %d 以上。在主機上執行：curl -fsSL https://get.docker.com | sudo sh", limits.HostMinDockerMajor))
	} else {
		r.set(CheckDocker, CheckOK, in.ServerVersion+" · 可以使用", "")
	}

	// 資源
	free, total, derr := s.diskSpace(ctx, in.DockerRootDir)
	r.Resources.DiskFreeBytes, r.Resources.DiskTotalBytes = free, total
	res := fmt.Sprintf("%d 核 · %s · 可用 %s", in.NCPU, GB(in.MemTotal), GB(free))
	switch {
	case derr != nil:
		r.set(CheckResources, CheckWarn, fmt.Sprintf("%d 核 · %s · 磁碟空間看不到（%v）", in.NCPU, GB(in.MemTotal), derr), "")
	case free < minDiskBytes:
		r.set(CheckResources, CheckFail, res, fmt.Sprintf("磁碟要至少 %s 可用（%s）", GB(minDiskBytes), in.DockerRootDir))
	case in.NCPU < 2 || in.MemTotal < 3<<30:
		r.set(CheckResources, CheckWarn, res, "每台 Bot 電腦用 2 CPU／3 GB；這台可能只放得下一台，而且會比較慢")
	default:
		r.set(CheckResources, CheckOK, res, "")
	}

	// 架構
	arch := NormalizeArch(in.Architecture)
	r.Resources.Arch = arch
	switch {
	case arch != "amd64" && arch != "arm64":
		r.set(CheckArch, CheckFail, in.Architecture, "Bot 電腦映像只有 amd64 與 arm64")
	case img.Arch != "" && img.Arch != arch:
		r.set(CheckArch, CheckFail, fmt.Sprintf("%s，但這台 XBot 的電腦映像是 %s", arch, img.Arch),
			"換一台 "+img.Arch+" 的主機，或在 "+arch+" 的機器上建映像（make box）後再加入")
	default:
		other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[arch]
		r.set(CheckArch, CheckOK, fmt.Sprintf("%s（%s 也支援）", arch, other), "")
	}

	// SSH 轉發
	if err := s.checkForwarding(ctx, cl); err != nil {
		r.set(CheckForwarding, CheckFail, err.Error(), "在主機上再貼一次第 1 步的指令（會設定 /etc/ssh/sshd_config.d/10-xbot.conf）")
	} else {
		r.set(CheckForwarding, CheckOK, "埠轉發、socket 轉發、重連換 socket 都可以", "")
	}

	// 電腦映像
	switch {
	case img.ID == "":
		r.set(CheckImage, CheckFail, "這台 XBot 沒有電腦映像 "+img.Name, "在 XBot 這台先執行 make box")
	default:
		got, err := s.docker.ImageInspect(ctx, img.Name)
		switch {
		case err == nil && SameImage(got.InspectResponse, img):
			r.Resources.ImageID = got.ID
			r.set(CheckImage, CheckOK, img.Name+" · 已是最新", "")
		case err == nil || cerrdefs.IsNotFound(err):
			r.NeedImage = true
			if err == nil {
				r.Resources.ImageID = got.ID
			}
			r.set(CheckImage, CheckPending, fmt.Sprintf("%s · 要從 XBot 傳過去（約 %s）", img.Name, GB(img.Size)), "")
		default:
			r.set(CheckImage, CheckFail, "看不到主機上的映像："+err.Error(), "")
		}
	}

	// 連線方式（Tailscale）
	r.Resources.Tailscale = isTailnet(s.o.Addr)
	if !r.Resources.Tailscale {
		if out, err := s.Run(ctx, "tailscale ip -4 2>/dev/null | head -n1"); err == nil && strings.TrimSpace(out) != "" {
			r.set(CheckNetwork, CheckWarn, "主機在 tailnet（"+strings.TrimSpace(out)+"），但 XBot 不是經 Tailscale 連過去",
				"改用 Tailscale 名稱或 IP 加入，SSH 就不用對外開放")
		} else {
			r.set(CheckNetwork, CheckWarn, "不是經 Tailscale 連線", "建議把主機加入 tailnet、改用 Tailscale 位址，並在防火牆關掉公網的 22 埠（PRD §17）")
		}
	} else {
		r.set(CheckNetwork, CheckOK, "經 Tailscale；Bot 的畫面埠只綁 127.0.0.1", "")
	}
	return r
}

// minDiskBytes 是 limits.HostMinDiskBytes（測試可以調小）。
var minDiskBytes int64 = limits.HostMinDiskBytes

// waitChecked 等連線結果給檢查用：從來沒連上過時，帳號或金鑰不對馬上回報；連不上的話再試 limits.HostCheckGrace
// （剛跑完安裝腳本時 sshd 可能正在重新載入），不用等到逾時；連上過、正在重連的主機最多等 limits.SSHWaitOnline。
func (s *SSH) waitChecked(ctx context.Context) (*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.SSHDialTimeout+limits.SSHWaitOnline)
	defer cancel()
	start := time.Now()
	for {
		s.mu.Lock()
		cl, h, online := s.cl, s.health, s.online
		s.mu.Unlock()
		switch {
		case cl != nil:
			return cl, nil
		case h.State == SSHFailed || h.State == SSHClosed:
			return nil, fmt.Errorf("%w：%s", ErrHostOffline, h.LastError)
		case h.Connects == 0 && h.LastError != "" && (time.Since(start) > limits.HostCheckGrace || permanentSSHError(h.LastError)):
			return nil, errors.New(h.LastError)
		}
		select {
		case <-online:
		case <-ctx.Done():
			msg := h.LastError
			if msg == "" {
				msg = "連線逾時"
			}
			return nil, errors.New(msg)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// permanentSSHError：再試也不會好的錯誤（帳號或金鑰不對、找不到主機名稱），檢查時不用等。
func permanentSSHError(msg string) bool {
	for _, s := range []string{"unable to authenticate", "no such host", ErrHostKeyMismatch.Error()} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func dockerMajor(v string) int {
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0])
	return n
}

// isTailnet：位址是 Tailscale 的 100.64.0.0/10、fd7a:115c:a1e0::/48，或 *.ts.net 名稱。
func isTailnet(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if strings.HasSuffix(strings.TrimSuffix(h, "."), ".ts.net") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	for _, c := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
		_, n, _ := net.ParseCIDR(c)
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// GB 把位元組數寫成「8 GB」「1.2 GB」。
func GB(n int64) string {
	g := float64(n) / (1 << 30)
	if g >= 10 || g == float64(int64(g)) {
		return fmt.Sprintf("%.0f GB", g)
	}
	return fmt.Sprintf("%.1f GB", g)
}

func (s *SSH) diskSpace(ctx context.Context, dir string) (free, total int64, err error) {
	if dir == "" {
		dir = "/var/lib/docker"
	}
	// xbot 帳號不一定能進 Docker 的資料目錄，但 df 只要路徑存在就行；不存在時看根目錄。
	out, err := s.Run(ctx, "df -Pk "+shellQuote(dir)+" 2>/dev/null || df -Pk /")
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, 0, fmt.Errorf("看不懂 df 的輸出：%q", out)
	}
	t, err1 := strconv.ParseInt(f[1], 10, 64)
	a, err2 := strconv.ParseInt(f[3], 10, 64)
	if err := errors.Join(err1, err2); err != nil {
		return 0, 0, err
	}
	return a * 1024, t * 1024, nil
}

// checkForwarding 實際走一次 XBot 會用到的三種轉發：
//   - 反向 Unix socket（product.sock）：在遠端聽一個測試 socket，權限要是 0666（StreamLocalBindMask 0111）；
//   - direct-streamlocal（Docker socket 用的同一種）：連那個 socket，資料要繞回這邊；
//   - 再聽一次同一個路徑（重連時的情況，要 StreamLocalBindUnlink yes）；
//   - TCP 轉發（noVNC／Hermes 埠）：反向聽一個 127.0.0.1 埠再連過去。
func (s *SSH) checkForwarding(ctx context.Context, cl *ssh.Client) error {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	sock := path.Join(s.o.RemoteDir, ".check-"+hex.EncodeToString(suffix)+".sock")
	defer func() { _, _ = s.Run(context.WithoutCancel(ctx), "rm -f "+shellQuote(sock)) }()

	ln, err := cl.ListenUnix(sock)
	if err != nil {
		return fmt.Errorf("不能在主機上建立 socket 轉發（AllowStreamLocalForwarding）：%w", err)
	}
	if err := echoCheck(ln, func() (net.Conn, error) { return cl.Dial("unix", sock) }); err != nil {
		ln.Close()
		return fmt.Errorf("socket 轉發不通：%w", err)
	}
	mode, _ := s.Run(ctx, "stat -c %a "+shellQuote(sock)+" 2>/dev/null")
	ln.Close()
	if m := strings.TrimSpace(mode); m != "" && m != "666" {
		return fmt.Errorf("轉發的 socket 權限是 %s，要是 666（StreamLocalBindMask 0111），否則 Bot 的電腦連不回 XBot", m)
	}
	// 舊連線留下的 socket 檔還在時要能直接換掉（StreamLocalBindUnlink yes）。
	if _, err := s.Run(ctx, "test -S "+shellQuote(sock)); err == nil {
		ln2, err := cl.ListenUnix(sock)
		if err != nil {
			return fmt.Errorf("重連時換不掉舊的 socket（StreamLocalBindUnlink）：%w", err)
		}
		ln2.Close()
	}

	tl, err := cl.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("不能做埠轉發（AllowTcpForwarding）：%w", err)
	}
	defer tl.Close()
	if err := echoCheck(tl, func() (net.Conn, error) { return cl.Dial("tcp", tl.Addr().String()) }); err != nil {
		return fmt.Errorf("埠轉發不通（AllowTcpForwarding）：%w", err)
	}
	return nil
}

// echoCheck 經 dial 送一段資料，從 ln 收下來確認一樣。
func echoCheck(ln net.Listener, dial func() (net.Conn, error)) error {
	got := make(chan error, 1)
	want := []byte("xbot-check")
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(limits.SSHDialTimeout))
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(c, buf); err != nil {
			got <- err
			return
		}
		if string(buf) != string(want) {
			got <- fmt.Errorf("收到 %q", buf)
			return
		}
		got <- nil
	}()
	c, err := dial()
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Write(want); err != nil {
		return err
	}
	select {
	case err := <-got:
		return err
	case <-time.After(limits.SSHDialTimeout):
		return errors.New("逾時")
	}
}
