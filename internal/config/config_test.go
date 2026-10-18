package config

import (
	"path/filepath"
	"testing"
	"time"
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
	if c.Port != DefaultPort || c.Bind != "0.0.0.0" {
		t.Fatalf("預設值錯誤：%+v", c)
	}
	if c.DataDir != filepath.Join("/tmp/xdg", "xbot") {
		t.Fatalf("資料目錄錯誤：%s", c.DataDir)
	}
	if c.URL() != "http://localhost:8780" {
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

func TestPublicURL(t *testing.T) {
	t.Setenv("XBOT_PUBLIC_URL", "https://xbot.tail3a9c.ts.net/")
	c, err := Load()
	if err != nil || c.PublicURL != "https://xbot.tail3a9c.ts.net" {
		t.Fatalf("%q %v", c.PublicURL, err)
	}
	for _, bad := range []string{"xbot.ts.net", "ftp://x", "https://x/sub"} {
		t.Setenv("XBOT_PUBLIC_URL", bad)
		if _, err := Load(); err == nil {
			t.Fatalf("%q 應該不接受", bad)
		}
	}
}

func TestFakeNowOnlyInDev(t *testing.T) {
	t.Setenv("XBOT_FAKE_NOW", "2026-10-18T23:30:00+08:00")
	t.Setenv("XBOT_DEV", "")
	c, err := Load()
	if err != nil || !c.FakeNow.IsZero() {
		t.Fatalf("正式版不理 XBOT_FAKE_NOW：%v %v", c.FakeNow, err)
	}
	if d := time.Since(c.Clock()()); d < -time.Second || d > time.Second {
		t.Fatalf("正式版用真的時間：%v", d)
	}
	t.Setenv("XBOT_DEV", "1")
	c, err = Load()
	if err != nil || c.FakeNow.IsZero() {
		t.Fatalf("開發模式讀 XBOT_FAKE_NOW：%v %v", c.FakeNow, err)
	}
	want := time.Date(2026, 10, 18, 15, 30, 0, 0, time.UTC)
	if d := c.Clock()().Sub(want); d < 0 || d > time.Second {
		t.Fatalf("假時鐘從設定的時間開始走：%v", c.Clock()())
	}
	t.Setenv("XBOT_FAKE_NOW", "明天")
	if _, err := Load(); err == nil {
		t.Fatal("格式錯要回錯誤")
	}
}
