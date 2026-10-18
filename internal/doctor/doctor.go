// Package doctor 檢查 XBot 需要的工具與環境，並告訴使用者怎麼修。
//
// 每一項檢查回傳 ✅／⚠️／❌ 與一行修正指令；必要項目失敗時 Run 回傳非零結果。
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Mode 決定要檢查哪些項目。
type Mode string

const (
	// ModeDev 是開發者（有原始碼）：要 Go、Node、pnpm、git 等。
	ModeDev Mode = "dev"
	// ModeRun 是只執行服務（已安裝的執行檔）：只要 Docker 與執行環境。
	ModeRun Mode = "run"
)

// Status 是一項檢查的結果。
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Result 是一項檢查。
type Result struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	// Cmd 是可以直接執行的修正指令（make setup 會詢問後執行）；空字串表示要手動處理。
	Cmd      string `json:"cmd,omitempty"`
	Required bool   `json:"required"`
}

// Env 讓測試可以替換掉系統呼叫。
type Env struct {
	LookPath    func(string) (string, error)
	Output      func(name string, args ...string) (string, error)
	FreeBytes   func(path string) (uint64, error)
	PortInUse   func(addr string) bool
	Healthy     func(url string) bool
	ReadFile    func(string) ([]byte, error)
	InContainer func() bool
	// NetworkStatus 向執行中的服務要 xbot.network.status（本機免登入）；nil 表示不檢查內建 Tailscale。
	NetworkStatus func(url string) ([]byte, error)
}

// SystemEnv 回傳使用真實系統的 Env。
func SystemEnv() Env {
	return Env{
		LookPath: exec.LookPath,
		Output: func(name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		FreeBytes: func(path string) (uint64, error) {
			var st syscall.Statfs_t
			for p := path; ; {
				if err := syscall.Statfs(p, &st); err == nil {
					return st.Bavail * uint64(st.Bsize), nil
				}
				parent := parentDir(p)
				if parent == p {
					return 0, fmt.Errorf("讀不到 %s 的磁碟空間", path)
				}
				p = parent
			}
		},
		PortInUse: func(addr string) bool {
			l, err := net.Listen("tcp", addr)
			if err != nil {
				return true
			}
			_ = l.Close()
			return false
		},
		Healthy: func(url string) bool {
			c := http.Client{Timeout: 2 * time.Second}
			resp, err := c.Get(url)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return resp.StatusCode == http.StatusOK && strings.Contains(string(b), `"ok"`)
		},
		ReadFile:    os.ReadFile,
		InContainer: inContainer,
		NetworkStatus: func(url string) ([]byte, error) {
			c := http.Client{Timeout: 3 * time.Second}
			resp, err := c.Post(url, "application/json", strings.NewReader("{}"))
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			if err != nil {
				return nil, err
			}
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			return b, nil
		},
	}
}

// Options 是一次檢查的參數。
type Options struct {
	Mode    Mode
	DataDir string
	Addr    string // 服務要用的 host:port
}

// MinDockerMajor 是需要的 Docker Engine 主版本（volume subpath）。
const MinDockerMajor = 26

// Check 跑所有檢查。
func Check(env Env, o Options) []Result {
	d := distro(env)
	var rs []Result
	if o.Mode == ModeDev {
		rs = append(rs,
			toolCheck(env, "mise", "mise", false, "make setup（會自動安裝 mise）", "version"),
			toolCheck(env, "Go", "go", true, "make setup", "version"),
			toolCheck(env, "Node.js", "node", true, "make setup", "--version"),
			toolCheck(env, "pnpm", "pnpm", true, "make setup", "--version"),
			toolCheck(env, "git", "git", true, d.install("git"), "--version"),
		)
	}
	rs = append(rs, dockerChecks(env, d)...)
	rs = append(rs,
		toolCheck(env, "ssh（連遠端主機用）", "ssh", false, d.install("openssh-client"), "-V"),
		tailscaleCheck(env, d),
	)
	if r, ok := embeddedTailscaleCheck(env, o); ok {
		rs = append(rs, r)
	}
	rs = append(rs, subscriptionChecks(env)...)
	rs = append(rs,
		diskCheck(env, o.DataDir),
		portCheck(env, o.Addr),
	)
	return rs
}

// Failed 回傳是否有必要項目沒過。
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Required && r.Status == Fail {
			return true
		}
	}
	return false
}

// Format 把結果排成給人看的文字。
func Format(rs []Result) string {
	var b strings.Builder
	for _, r := range rs {
		icon := map[Status]string{OK: "✅", Warn: "⚠️", Fail: "❌"}[r.Status]
		if r.Status == Fail && !r.Required {
			icon = "⚠️"
		}
		fmt.Fprintf(&b, "%s %s %s\n", icon, pad(r.Name, 24), r.Detail)
		if r.Fix != "" && r.Status != OK {
			fmt.Fprintf(&b, "   ↳ 修正：%s\n", r.Fix)
		}
	}
	if Failed(rs) {
		b.WriteString("\n有必要項目沒通過，照上面的「修正」處理後再跑一次 make doctor。\n")
	} else {
		b.WriteString("\n必要項目都通過了。\n")
	}
	return b.String()
}

func toolCheck(env Env, name, bin string, required bool, fix string, versionArg string) Result {
	r := Result{Name: name, Required: required, Fix: fix}
	if _, err := env.LookPath(bin); err != nil {
		r.Status, r.Detail = Fail, "沒有安裝"
		return r
	}
	out, _ := env.Output(bin, versionArg)
	r.Status, r.Detail = OK, firstLine(out)
	return r
}

func dockerChecks(env Env, d distroInfo) []Result {
	r := Result{Name: "Docker", Required: true}
	if env.InContainer() {
		if _, err := env.LookPath("docker"); err != nil {
			r.Status, r.Detail = Warn, "在容器內，沒有 Docker（Bot 電腦需要在主機上跑）"
			r.Required = false
			return []Result{r}
		}
	}
	if _, err := env.LookPath("docker"); err != nil {
		r.Status, r.Detail, r.Fix = Fail, "沒有安裝（Bot 的電腦需要 Docker）", d.dockerInstall()
		r.Cmd = r.Fix
		return []Result{r}
	}
	out, err := env.Output("docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		low := strings.ToLower(out)
		switch {
		case strings.Contains(low, "permission denied"):
			r.Status, r.Detail = Fail, "目前使用者沒有權限使用 Docker"
			r.Fix = "sudo usermod -aG docker $USER，然後登出再登入（或執行 newgrp docker）"
			r.Cmd = "sudo usermod -aG docker $USER"
		default:
			r.Status, r.Detail = Fail, "Docker 服務沒有在執行"
			r.Fix = "sudo systemctl enable --now docker"
			r.Cmd = r.Fix
		}
		return []Result{r}
	}
	ver := firstLine(out)
	if major(ver) < MinDockerMajor {
		r.Status, r.Detail = Fail, fmt.Sprintf("版本 %s 太舊，需要 %d 以上", ver, MinDockerMajor)
		r.Fix = d.dockerInstall()
		r.Cmd = r.Fix
		return []Result{r}
	}
	r.Status, r.Detail = OK, "Engine "+ver
	c := Result{Name: "docker compose", Required: false, Fix: d.composeInstall()}
	if out, err := env.Output("docker", "compose", "version", "--short"); err != nil {
		c.Status, c.Detail = Warn, "沒有安裝（只有 make up 需要）"
	} else {
		c.Status, c.Detail = OK, firstLine(out)
	}
	return []Result{r, c}
}

func tailscaleCheck(env Env, d distroInfo) Result {
	r := Result{Name: "Tailscale（選用）", Required: false,
		Fix: "curl -fsSL https://tailscale.com/install.sh | sh && sudo tailscale up",
		Cmd: "curl -fsSL https://tailscale.com/install.sh | sh"}
	if _, err := env.LookPath("tailscale"); err != nil {
		r.Status, r.Detail = Warn, "沒有安裝（手機連線與雲端主機才需要）"
		return r
	}
	out, err := env.Output("tailscale", "status", "--peers=false")
	if err != nil {
		r.Status, r.Detail, r.Fix, r.Cmd = Warn, "已安裝但沒有登入", "sudo tailscale up", ""
		return r
	}
	r.Status, r.Detail = OK, firstLine(out)
	return r
}

// embeddedTailscaleCheck 顯示內建 Tailscale（C35）的狀態；沒開就不顯示。服務要在執行才查得到。
func embeddedTailscaleCheck(env Env, o Options) (Result, bool) {
	r := Result{Name: "內建 Tailscale", Required: false, Fix: "設定 → 進階 → 網路與 Tailscale"}
	if env.NetworkStatus == nil || o.Addr == "" {
		return r, false
	}
	host, port, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return r, false
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	base := "http://" + net.JoinHostPort(host, port)
	if !env.Healthy(base + "/healthz") {
		if _, err := env.ReadFile(filepath.Join(o.DataDir, "tailscale", "tailscaled.state")); err == nil {
			r.Status, r.Detail, r.Fix = Warn, "設定過，但服務沒在執行，看不到狀態", "make start"
			return r, true
		}
		return r, false
	}
	b, err := env.NetworkStatus(base + "/api/v1/network.status")
	if err != nil {
		return r, false // 舊版服務、或關掉了本機免登入
	}
	var res struct {
		Output struct {
			Tailscale struct {
				Enabled     bool   `json:"enabled"`
				State       string `json:"state"`
				Message     string `json:"message"`
				URL         string `json:"url"`
				Cert        string `json:"cert"`
				CertMessage string `json:"cert_message"`
				Devices     int    `json:"devices"`
			} `json:"tailscale"`
			Funnel struct {
				Enabled bool   `json:"enabled"`
				State   string `json:"state"`
				Message string `json:"message"`
			} `json:"funnel"`
		} `json:"output"`
	}
	if json.Unmarshal(b, &res) != nil || !res.Output.Tailscale.Enabled {
		return r, false
	}
	t, f := res.Output.Tailscale, res.Output.Funnel
	switch t.State {
	case "running":
		r.Status, r.Detail = OK, fmt.Sprintf("已連上 %s（tailnet 上 %d 台其他裝置）", t.URL, t.Devices)
		if t.Cert == "missing" || t.Cert == "error" {
			r.Status, r.Detail, r.Fix = Warn, t.URL+"：HTTPS 憑證沒好", t.CertMessage
		}
		if f.Enabled {
			if f.State == "on" {
				r.Detail += "；外部入口（Funnel）開著"
			} else if f.Message != "" {
				r.Status, r.Fix = Warn, "外部入口（Funnel）："+f.Message
			}
		}
	case "needs_login":
		r.Status, r.Detail = Warn, "還沒登入 tailnet"
		r.Fix = "到 設定 → 進階 → 網路與 Tailscale 打開登入網址，或給登入金鑰"
	case "starting":
		r.Status, r.Detail = Warn, "正在連線…"
	default:
		r.Status, r.Detail = Warn, firstLine(t.Message)
	}
	return r, true
}

// subscriptionChecks 看這台電腦上有沒有 Claude Code／Codex CLI／Gemini CLI／OpenCode／Grok 的登入（選用，EN-2b、E31）：
// Bot 用 Claude Code、Codex CLI 當引擎時，經你逐 Bot 同意後借用這份登入（xbot.engines.link_credentials）。
// CLI 本身已裝在 Bot 的電腦映像裡，這台電腦只需要登入過一次。
func subscriptionChecks(env Env) []Result {
	home, _ := os.UserHomeDir()
	var rs []Result
	for _, c := range []struct{ name, envDir, dir, file, fix string }{
		{"Claude Code 登入（選用）", "CLAUDE_CONFIG_DIR", ".claude", ".credentials.json", "安裝 Claude Code 後執行 claude，輸入 /login 登入"},
		{"Codex CLI 登入（選用）", "CODEX_HOME", ".codex", "auth.json", "安裝 Codex CLI 後執行 codex login"},
		{"Gemini CLI 登入（選用）", "GEMINI_CLI_HOME", ".gemini", "oauth_creds.json", "安裝 Gemini CLI 後執行 gemini，選 Login with Google"},
		{"OpenCode 登入（選用）", "XDG_DATA_HOME", ".local/share/opencode", "auth.json", "安裝 OpenCode 後執行 opencode auth login（OpenCode Go 或 Zen）"},
		{"Grok 登入（選用）", "GROK_HOME", ".grok", "auth.json", "執行 grok login"},
	} {
		dir := os.Getenv(c.envDir)
		switch {
		case dir == "":
			dir = home + "/" + c.dir
		case c.envDir == "GEMINI_CLI_HOME":
			dir += "/.gemini"
		case c.envDir == "XDG_DATA_HOME":
			dir += "/opencode"
		}
		r := Result{Name: c.name, Required: false, Fix: c.fix}
		if _, err := env.ReadFile(dir + "/" + c.file); err != nil {
			r.Status, r.Detail = Warn, "沒有登入（要讓 Bot 用你的訂閱時才需要）"
		} else {
			r.Status, r.Detail = OK, "已登入，可以讓 Bot 使用（要你逐 Bot 同意）"
		}
		rs = append(rs, r)
	}
	return rs
}

// 建議的空閒空間：Bot 電腦映像約 5 GB，加上每個 Bot 的資料。
const (
	diskWarnBytes = 10 << 30
	diskFailBytes = 3 << 30
)

func diskCheck(env Env, dataDir string) Result {
	r := Result{Name: "磁碟空間", Required: true}
	free, err := env.FreeBytes(dataDir)
	if err != nil {
		r.Status, r.Detail = Warn, err.Error()
		r.Required = false
		return r
	}
	gb := float64(free) / (1 << 30)
	switch {
	case free < diskFailBytes:
		r.Status, r.Detail = Fail, fmt.Sprintf("只剩 %.1f GB（%s）", gb, dataDir)
		r.Fix = "清出空間：docker system prune，或用 XBOT_DATA_DIR 改放到較大的磁碟"
	case free < diskWarnBytes:
		r.Status, r.Detail = Warn, fmt.Sprintf("剩 %.1f GB，建議至少 10 GB", gb)
		r.Fix = "docker system prune 可以清掉沒用的映像"
	default:
		r.Status, r.Detail = OK, fmt.Sprintf("剩 %.0f GB", gb)
	}
	return r
}

func portCheck(env Env, addr string) Result {
	r := Result{Name: "服務埠 " + addr, Required: false}
	if !env.PortInUse(addr) {
		r.Status, r.Detail = OK, "可以使用"
		return r
	}
	host, port, _ := net.SplitHostPort(addr)
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if env.Healthy("http://" + net.JoinHostPort(host, port) + "/healthz") {
		r.Status, r.Detail = OK, "XBot 已經在這個埠上執行"
		return r
	}
	r.Status, r.Detail = Warn, "被其他程式占用"
	r.Fix = "用 XBOT_PORT=<其他埠> 改埠，或找出占用的程式：ss -ltnp | grep " + port
	return r
}

// distroInfo 決定修正指令怎麼寫。
type distroInfo struct{ family string }

func distro(env Env) distroInfo {
	b, err := env.ReadFile("/etc/os-release")
	if err != nil {
		return distroInfo{}
	}
	s := strings.ToLower(string(b))
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "id=") && !strings.HasPrefix(line, "id_like=") {
			continue
		}
		v := strings.Trim(strings.SplitN(line, "=", 2)[1], `"`)
		for _, f := range strings.Fields(v) {
			switch f {
			case "debian", "ubuntu":
				return distroInfo{"debian"}
			case "arch":
				return distroInfo{"arch"}
			case "fedora", "rhel":
				return distroInfo{"fedora"}
			}
		}
	}
	return distroInfo{}
}

func (d distroInfo) install(pkg string) string {
	switch d.family {
	case "debian":
		return "sudo apt-get install -y " + pkg
	case "arch":
		if pkg == "openssh-client" {
			pkg = "openssh"
		}
		return "sudo pacman -S --needed " + pkg
	case "fedora":
		if pkg == "openssh-client" {
			pkg = "openssh-clients"
		}
		return "sudo dnf install -y " + pkg
	}
	return "用系統的套件管理員安裝 " + pkg
}

func (d distroInfo) dockerInstall() string {
	if d.family == "arch" {
		return "sudo pacman -S --needed docker docker-compose && sudo systemctl enable --now docker"
	}
	return "curl -fsSL https://get.docker.com | sh && sudo systemctl enable --now docker"
}

func (d distroInfo) composeInstall() string {
	switch d.family {
	case "arch":
		return "sudo pacman -S --needed docker-compose"
	case "debian":
		return "sudo apt-get install -y docker-compose-plugin（用 get.docker.com 安裝的 Docker 已內建）"
	}
	return "安裝 Docker Compose v2 外掛"
}

var majorRe = regexp.MustCompile(`^(\d+)`)

func major(v string) int {
	m := majorRe.FindStringSubmatch(strings.TrimPrefix(v, "v"))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// pad 依顯示寬度補空白（中文字算 2 格）。
func pad(s string, width int) string {
	w := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115f || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6)) {
			w += 2
		} else {
			w++
		}
	}
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func parentDir(p string) string {
	if p == "/" || p == "" || p == "." {
		return p
	}
	i := strings.LastIndexByte(strings.TrimRight(p, "/"), '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

// InContainer 回報目前是否在容器內執行。
func InContainer() bool { return inContainer() }
