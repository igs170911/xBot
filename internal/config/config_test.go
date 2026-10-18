package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XBOT_BIND", "")
	t.Setenv("XBOT_PORT", "")
	t.Setenv("XBOT_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != DefaultPort || c.Bind != "127.0.0.1" {
		t.Fatalf("預設值錯誤：%+v", c)
	}
	if c.DataDir != filepath.Join("/tmp/xdg", "xbot") {
		t.Fatalf("資料目錄錯誤：%s", c.DataDir)
	}
	if c.URL() != "http://127.0.0.1:8780" {
		t.Fatalf("URL 錯誤：%s", c.URL())
	}
}

func TestLoadBadPort(t *testing.T) {
	t.Setenv("XBOT_PORT", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("無效的埠號應該回錯誤")
	}
}

func TestLocalNoAuth(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "0": false, "false": false} {
		t.Setenv("XBOT_LOCAL_NOAUTH", v)
		c, err := Load()
		if err != nil || c.LocalNoAuth != want {
			t.Fatalf("XBOT_LOCAL_NOAUTH=%q：%v %v", v, c.LocalNoAuth, err)
		}
	}
	t.Setenv("XBOT_LOCAL_NOAUTH", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("不認得的值應該回錯誤")
	}
}

func TestURLForAnyBind(t *testing.T) {
	c := Config{Bind: "0.0.0.0", Port: 9000}
	if c.URL() != "http://localhost:9000" {
		t.Fatalf("URL 錯誤：%s", c.URL())
	}
}
