// Package service 把 XBot 安裝成這台機器的 systemd 使用者服務，並負責啟動、停止、狀態、更新與移除。
//
// 用使用者服務（systemctl --user），不需要 sudo；開機自動啟動要開 linger（會先詢問）。
package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// Unit 是 systemd 服務名稱。
const Unit = "xbot.service"

// Paths 是安裝會用到的位置。
type Paths struct {
	Bin     string // 安裝後的執行檔，例：~/.local/bin/xbot
	UnitDir string // ~/.config/systemd/user
	DataDir string
}

// DefaultPaths 依 XDG 慣例回傳安裝位置。
func DefaultPaths(dataDir string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return Paths{
		Bin:     filepath.Join(home, ".local", "bin", "xbot"),
		UnitDir: filepath.Join(cfg, "systemd", "user"),
		DataDir: dataDir,
	}, nil
}

// Settings 是寫進服務設定的環境變數。
type Settings struct {
	Bind string
	Port int
}

// Runner 執行外部指令並回傳輸出；測試時替換。
type Runner func(name string, args ...string) (string, error)

// SystemRunner 執行真實指令。
func SystemRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Manager 管理 XBot 服務。
type Manager struct {
	Paths    Paths
	Settings Settings
	Run      Runner
	Out      io.Writer
	In       *bufio.Reader
	Yes      bool
	// Healthy 檢查服務是否回應；測試時替換。
	Healthy func(url string) bool
	// Wait 是等待服務起來的上限。
	Wait time.Duration
}

var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=XBot：你的 AI 工作團隊
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.Bin}} serve
Environment=XBOT_DATA_DIR={{.DataDir}}
Environment=XBOT_BIND={{.Bind}}
Environment=XBOT_PORT={{.Port}}
Restart=on-failure
RestartSec=3
TimeoutStopSec=20

[Install]
WantedBy=default.target
`))

// RenderUnit 產生 systemd 服務檔內容。
func RenderUnit(p Paths, s Settings) string {
	var b strings.Builder
	_ = unitTmpl.Execute(&b, map[string]any{"Bin": p.Bin, "DataDir": p.DataDir, "Bind": s.Bind, "Port": s.Port})
	return b.String()
}

// URL 是服務在本機的網址。
func (m *Manager) URL() string {
	host := m.Settings.Bind
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, m.Settings.Port)
}

// HasSystemd 回報這台機器能不能用 systemd 使用者服務。
func (m *Manager) HasSystemd() bool {
	_, err := m.Run("systemctl", "--user", "show-environment")
	return err == nil
}

// Install 複製執行檔、寫入服務檔、啟用並啟動，等服務健康後回傳。
func (m *Manager) Install(srcBin string) error {
	if !m.HasSystemd() {
		return errors.New("這台機器沒有可用的 systemd 使用者服務。可以改用 make up（docker compose），或直接執行 xbot serve")
	}
	if err := m.copyBin(srcBin, m.Paths.Bin); err != nil {
		return err
	}
	if err := os.MkdirAll(m.Paths.UnitDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(m.Paths.DataDir, 0o700); err != nil {
		return err
	}
	unit := filepath.Join(m.Paths.UnitDir, Unit)
	if err := os.WriteFile(unit, []byte(RenderUnit(m.Paths, m.Settings)), 0o644); err != nil {
		return fmt.Errorf("寫入服務檔失敗：%w", err)
	}
	m.say("✅ 服務檔：%s", unit)
	if err := m.systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := m.systemctl("enable", Unit); err != nil {
		return err
	}
	if err := m.systemctl("restart", Unit); err != nil {
		return err
	}
	if err := m.waitHealthy(); err != nil {
		return err
	}
	m.ensureLinger()
	return nil
}

// Update 換成新的執行檔並重啟；新版起不來就自動換回舊版。
func (m *Manager) Update(srcBin string) error {
	prev := m.Paths.Bin + ".prev"
	hadOld := fileExists(m.Paths.Bin)
	if hadOld {
		if err := copyFile(m.Paths.Bin, prev, 0o755); err != nil {
			return fmt.Errorf("保留舊版失敗：%w", err)
		}
	}
	if err := m.Install(srcBin); err != nil {
		if !hadOld {
			return err
		}
		m.say("❌ 新版啟動失敗：%v", err)
		m.say("↩️  換回上一版…")
		if rerr := copyFile(prev, m.Paths.Bin, 0o755); rerr != nil {
			return fmt.Errorf("新版失敗，而且換回舊版也失敗：%v（原因：%w）", rerr, err)
		}
		if rerr := m.systemctl("restart", Unit); rerr != nil {
			return fmt.Errorf("新版失敗，舊版重啟也失敗：%v（原因：%w）", rerr, err)
		}
		if rerr := m.waitHealthy(); rerr != nil {
			return fmt.Errorf("新版失敗，舊版也沒有起來：%v（原因：%w）", rerr, err)
		}
		return fmt.Errorf("已換回上一版，服務正常；新版的問題：%w", err)
	}
	return nil
}

// Uninstall 停止並移除服務與執行檔；purge 為 true 時也刪除資料（會再確認一次）。
func (m *Manager) Uninstall(purge bool) error {
	unit := filepath.Join(m.Paths.UnitDir, Unit)
	if fileExists(unit) {
		_ = m.systemctl("disable", "--now", Unit)
		if err := os.Remove(unit); err != nil {
			return err
		}
		_ = m.systemctl("daemon-reload")
		m.say("✅ 已移除服務")
	} else {
		m.say("（沒有安裝服務）")
	}
	for _, b := range []string{m.Paths.Bin, m.Paths.Bin + ".prev"} {
		if fileExists(b) {
			if err := os.Remove(b); err != nil {
				return err
			}
		}
	}
	if !purge {
		m.say("資料保留在 %s（要一起刪除請用 make uninstall PURGE=1）", m.Paths.DataDir)
		return nil
	}
	if !fileExists(m.Paths.DataDir) {
		return nil
	}
	m.say("⚠️  即將永久刪除所有資料：%s（Bot、對話、設定、登入資料）", m.Paths.DataDir)
	if !m.Yes {
		fmt.Fprint(m.Out, "   確定的話請輸入「刪除」：")
		line, _ := m.In.ReadString('\n')
		if strings.TrimSpace(line) != "刪除" {
			m.say("已取消，資料保留。")
			return nil
		}
	}
	if err := os.RemoveAll(m.Paths.DataDir); err != nil {
		return err
	}
	m.say("✅ 資料已刪除")
	return nil
}

// Ctl 執行 start／stop／restart。
func (m *Manager) Ctl(action string) error {
	if err := m.systemctl(action, Unit); err != nil {
		return err
	}
	if action == "start" || action == "restart" {
		return m.waitHealthy()
	}
	m.say("✅ 已%s", map[string]string{"stop": "停止"}[action])
	return nil
}

// Status 印出服務狀態。
func (m *Manager) Status() error {
	state, _ := m.Run("systemctl", "--user", "is-active", Unit)
	enabled, _ := m.Run("systemctl", "--user", "is-enabled", Unit)
	health := "沒有回應"
	if m.Healthy(m.URL() + "/healthz") {
		health = "正常"
	}
	m.say("服務：%s（開機自動啟動：%s）", zh(state), zh(enabled))
	m.say("網址：%s　健康檢查：%s", m.URL(), health)
	m.say("執行檔：%s", m.Paths.Bin)
	m.say("資料：%s", m.Paths.DataDir)
	if state != "active" {
		return errors.New("服務沒有在執行（make start 啟動，make logs 看原因）")
	}
	return nil
}

func zh(s string) string {
	switch s {
	case "active":
		return "執行中"
	case "inactive":
		return "已停止"
	case "failed":
		return "失敗"
	case "enabled":
		return "是"
	case "disabled":
		return "否"
	case "":
		return "未安裝"
	}
	return s
}

func (m *Manager) waitHealthy() error {
	url := m.URL() + "/healthz"
	deadline := time.Now().Add(m.Wait)
	for {
		if m.Healthy(url) {
			m.say("✅ 服務正常：%s", m.URL())
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("服務在 %s 內沒有回應 %s（make logs 看原因）", m.Wait, url)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// ensureLinger 讓使用者服務在開機時（沒有登入也）自動啟動。
func (m *Manager) ensureLinger() {
	user := os.Getenv("USER")
	out, err := m.Run("loginctl", "show-user", user, "-p", "Linger")
	if err == nil && strings.TrimSpace(out) == "Linger=yes" {
		m.say("✅ 開機會自動啟動")
		return
	}
	m.say("要讓 XBot 開機就自動啟動（不用先登入），需要開啟 linger：loginctl enable-linger %s", user)
	if !m.Yes {
		fmt.Fprint(m.Out, "   要現在開啟嗎？[y/N] ")
		line, _ := m.In.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			m.say("   ⏭  略過：目前只有在你登入時才會執行")
			return
		}
	}
	if _, err := m.Run("loginctl", "enable-linger", user); err != nil {
		m.say("   ❌ 開啟失敗，請改用：sudo loginctl enable-linger %s", user)
		return
	}
	m.say("✅ 開機會自動啟動")
}

func (m *Manager) systemctl(args ...string) error {
	out, err := m.Run("systemctl", append([]string{"--user"}, args...)...)
	if err != nil {
		return fmt.Errorf("systemctl --user %s 失敗：%s", strings.Join(args, " "), out)
	}
	return nil
}

func (m *Manager) copyBin(src, dst string) error {
	if src == "" {
		return errors.New("沒有指定要安裝的執行檔（先執行 make build）")
	}
	sa, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	da, _ := filepath.Abs(dst)
	if sa == da {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := copyFile(sa, dst, 0o755); err != nil {
		return fmt.Errorf("安裝執行檔失敗：%w", err)
	}
	m.say("✅ 執行檔：%s", dst)
	return nil
}

func (m *Manager) say(format string, a ...any) { fmt.Fprintf(m.Out, format+"\n", a...) }

// copyFile 先寫到暫存檔再改名，避免執行中的檔案被寫壞。
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// HTTPHealthy 是真實的健康檢查。
func HTTPHealthy(url string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return resp.StatusCode == http.StatusOK && strings.Contains(string(b), `"ok":true`)
}
