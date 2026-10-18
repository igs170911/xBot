// Package config 讀取 XBot 服務的執行設定（環境變數 → 預設值）。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultPort 是 XBot 服務的預設 HTTP 埠。
const DefaultPort = 8780

// Config 是服務啟動時需要的設定。
type Config struct {
	// Bind 是監聽位址。預設 0.0.0.0（使用者決定 2026-10-18：部署後區網其他裝置也連得到；
	// 非本機的連線一律要登入碼，見 auth）。只想本機使用時設 XBOT_BIND=127.0.0.1。
	Bind string
	// Port 是 HTTP 埠。
	Port int
	// DataDir 存放資料庫、登入碼、每個 Bot 的資料。
	DataDir string
	// Dev 為 true 時前端由 Vite 提供，服務只提供 API。
	Dev bool
	// LocalNoAuth 為 true 時，從本機（localhost）來的請求不用登入（SEC-7）。XBOT_LOCAL_NOAUTH=0 關掉；
	// 設了 XBOT_PUBLIC_URL 或 XBOT_TRUSTED_PROXIES 時預設關掉，除非明確設 XBOT_LOCAL_NOAUTH=1。
	LocalNoAuth bool
	// PublicURL 是反向代理模式的對外網址（XBOT_PUBLIC_URL，例：https://xbot.example.com）。空白表示沒有用反向代理（C35）。
	PublicURL string
	// TrustedProxies 是可以標 X-Forwarded-For 的反向代理位址（XBOT_TRUSTED_PROXIES，逗號分隔的 IP 或 CIDR）；空白是 127.0.0.1、::1。
	TrustedProxies string
	// FakeNow 是測試用的假「現在」（XBOT_FAKE_NOW，RFC 3339）：只在 XBOT_DEV=1 時才讀，正式版一律是零值。
	// 定時任務的時鐘從這個時間開始照常往前走，讓端對端測試能模擬週日深夜、月底等情境。
	FakeNow time.Time
}

// Clock 回傳服務排程用的「現在」：沒設 FakeNow 就是 time.Now。
func (c Config) Clock() func() time.Time {
	if c.FakeNow.IsZero() {
		return time.Now
	}
	off := time.Until(c.FakeNow)
	return func() time.Time { return time.Now().Add(off) }
}

// Addr 回傳 host:port。
func (c Config) Addr() string { return fmt.Sprintf("%s:%d", c.Bind, c.Port) }

// URL 回傳給人看的本機網址。
func (c Config) URL() string {
	host := c.Bind
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, c.Port)
}

// Load 從環境變數讀設定：XBOT_BIND、XBOT_PORT、XBOT_DATA_DIR、XBOT_DEV、XBOT_LOCAL_NOAUTH、XBOT_PUBLIC_URL、XBOT_TRUSTED_PROXIES、XBOT_FAKE_NOW（只在 XBOT_DEV=1 時）。
func Load() (Config, error) {
	c := Config{
		Bind:    envOr("XBOT_BIND", "0.0.0.0"),
		Port:    DefaultPort,
		DataDir: os.Getenv("XBOT_DATA_DIR"),
		Dev:     os.Getenv("XBOT_DEV") == "1",

		PublicURL:      os.Getenv("XBOT_PUBLIC_URL"),
		TrustedProxies: os.Getenv("XBOT_TRUSTED_PROXIES"),
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("XBOT_PUBLIC_URL")), "/"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return c, fmt.Errorf("XBOT_PUBLIC_URL 要像 https://主機名稱[:埠]：%q", v)
		}
		c.PublicURL = v
	}
	switch v := os.Getenv("XBOT_LOCAL_NOAUTH"); v {
	case "":
		// 有反向代理（XBOT_PUBLIC_URL 或 XBOT_TRUSTED_PROXIES）時預設關掉：同一台機器上的反向代理沒帶 X-Forwarded-For
		// 又把 Host 改成 127.0.0.1 時，外面來的請求看起來就像本機。真的要開請明確設 XBOT_LOCAL_NOAUTH=1。
		c.LocalNoAuth = c.PublicURL == "" && strings.TrimSpace(c.TrustedProxies) == ""
	case "1", "true":
		c.LocalNoAuth = true
	case "0", "false":
	default:
		return c, fmt.Errorf("XBOT_LOCAL_NOAUTH 只能是 1 或 0：%q", v)
	}
	if v := os.Getenv("XBOT_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return c, fmt.Errorf("XBOT_PORT 不是有效的埠號：%q", v)
		}
		c.Port = p
	}
	if v := os.Getenv("XBOT_FAKE_NOW"); v != "" && c.Dev {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return c, fmt.Errorf("XBOT_FAKE_NOW 要是 RFC 3339 時間（例：2026-10-18T23:30:00+08:00）：%q", v)
		}
		c.FakeNow = t
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, fmt.Errorf("資料目錄路徑有問題：%w", err)
	}
	c.DataDir = abs
	return c, nil
}

// DefaultDataDir 是安裝版的資料目錄：$XDG_DATA_HOME/xbot（通常是 ~/.local/share/xbot）。
func DefaultDataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "xbot")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".data"
	}
	return filepath.Join(home, ".local", "share", "xbot")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
