// Package config 讀取 XBot 服務的執行設定（環境變數 → 預設值）。
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPort 是 XBot 服務的預設 HTTP 埠。
const DefaultPort = 8780

// Config 是服務啟動時需要的設定。
type Config struct {
	// Bind 是監聽位址；只想本機使用時設成 127.0.0.1。
	Bind string
	// Port 是 HTTP 埠。
	Port int
	// DataDir 存放資料庫、登入碼、每個 Bot 的資料。
	DataDir string
	// Dev 為 true 時前端由 Vite 提供，服務只提供 API。
	Dev bool
	// LocalNoAuth 為 true 時，從本機（localhost）來的請求不用登入（SEC-7）。XBOT_LOCAL_NOAUTH=0 關掉。
	LocalNoAuth bool
	// PublicURL 是手機連得到的網址（XBOT_PUBLIC_URL，例：https://xbot.tail3a9c.ts.net），放進連手機的 QR；
	// 空白時用你打開網頁的網址。
	PublicURL string
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

// Load 從環境變數讀設定：XBOT_BIND、XBOT_PORT、XBOT_DATA_DIR、XBOT_DEV、XBOT_LOCAL_NOAUTH、XBOT_PUBLIC_URL。
func Load() (Config, error) {
	c := Config{
		Bind:    envOr("XBOT_BIND", "127.0.0.1"),
		Port:    DefaultPort,
		DataDir: os.Getenv("XBOT_DATA_DIR"),
		Dev:     os.Getenv("XBOT_DEV") == "1",
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("XBOT_PUBLIC_URL")), "/"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return c, fmt.Errorf("XBOT_PUBLIC_URL 要像 https://主機名稱[:埠]：%q", v)
		}
		c.PublicURL = v
	}
	switch v := os.Getenv("XBOT_LOCAL_NOAUTH"); v {
	case "", "1", "true":
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
