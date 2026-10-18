package service

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSys struct {
	calls   []string
	healthy bool
	linger  bool
	// failHealthyFor 讓「目前安裝的執行檔內容」等於它時健康檢查失敗（模擬壞掉的新版）。
	failHealthyFor string
	bin            string
}

func (f *fakeSys) run(name string, args ...string) (string, error) {
	c := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	switch {
	case strings.Contains(c, "show-user"):
		if f.linger {
			return "Linger=yes", nil
		}
		return "Linger=no", nil
	case strings.Contains(c, "is-active"):
		return "active", nil
	case strings.Contains(c, "is-enabled"):
		return "enabled", nil
	}
	return "", nil
}

func (f *fakeSys) isHealthy(string) bool {
	if f.failHealthyFor != "" {
		b, _ := os.ReadFile(f.bin)
		if string(b) == f.failHealthyFor {
			return false
		}
	}
	return f.healthy
}

func newManager(t *testing.T, f *fakeSys, in string) (*Manager, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	out := &bytes.Buffer{}
	p := Paths{Bin: filepath.Join(dir, "bin", "xbot"), UnitDir: filepath.Join(dir, "units"), DataDir: filepath.Join(dir, "data")}
	f.bin = p.Bin
	return &Manager{
		Paths: p, Settings: Settings{Bind: "127.0.0.1", Port: 8780},
		Run: f.run, Out: out, In: bufio.NewReader(strings.NewReader(in)),
		Healthy: f.isHealthy, Wait: 200 * time.Millisecond,
	}, out
}

func writeBin(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "xbot")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderUnit(t *testing.T) {
	u := RenderUnit(Paths{Bin: "/home/u/.local/bin/xbot", DataDir: "/home/u/.local/share/xbot"}, Settings{Bind: "127.0.0.1", Port: 8780})
	for _, want := range []string{"ExecStart=/home/u/.local/bin/xbot serve", "Environment=XBOT_DATA_DIR=/home/u/.local/share/xbot", "Environment=XBOT_PORT=8780", "WantedBy=default.target", "Restart=on-failure"} {
		if !strings.Contains(u, want) {
			t.Fatalf("服務檔缺少 %q：\n%s", want, u)
		}
	}
}

func TestInstall(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, out := newManager(t, f, "")
	if err := m.Install(writeBin(t, "v1")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(m.Paths.Bin); string(b) != "v1" {
		t.Fatal("執行檔沒有被複製")
	}
	if _, err := os.Stat(filepath.Join(m.Paths.UnitDir, Unit)); err != nil {
		t.Fatal("服務檔沒有寫入")
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"systemctl --user daemon-reload", "systemctl --user enable xbot.service", "systemctl --user restart xbot.service"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("少了 %q：\n%s", want, joined)
		}
	}
	if !strings.Contains(out.String(), "服務正常") {
		t.Fatalf("應顯示服務正常：%s", out.String())
	}
}

func TestInstallAsksForLinger(t *testing.T) {
	f := &fakeSys{healthy: true, linger: false}
	m, _ := newManager(t, f, "n\n")
	if err := m.Install(writeBin(t, "v1")); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "enable-linger") {
			t.Fatal("回答 n 不該開啟 linger")
		}
	}
	m.Yes = true
	if err := m.Install(writeBin(t, "v1")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "loginctl enable-linger") {
		t.Fatal("YES 應開啟 linger")
	}
}

func TestInstallFailsWhenUnhealthy(t *testing.T) {
	f := &fakeSys{healthy: false, linger: true}
	m, _ := newManager(t, f, "")
	if err := m.Install(writeBin(t, "v1")); err == nil || !strings.Contains(err.Error(), "沒有回應") {
		t.Fatalf("服務沒回應應回錯誤：%v", err)
	}
}

func TestUpdateRollsBack(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, out := newManager(t, f, "")
	if err := m.Install(writeBin(t, "good")); err != nil {
		t.Fatal(err)
	}
	f.failHealthyFor = "bad"
	err := m.Update(writeBin(t, "bad"))
	if err == nil || !strings.Contains(err.Error(), "已換回上一版") {
		t.Fatalf("壞掉的新版應自動回滾：%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(m.Paths.Bin); string(b) != "good" {
		t.Fatalf("應換回舊版，實際 %q", b)
	}
}

func TestUpdateKeepsPrevious(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, _ := newManager(t, f, "")
	_ = m.Install(writeBin(t, "v1"))
	if err := m.Update(writeBin(t, "v2")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(m.Paths.Bin + ".prev"); string(b) != "v1" {
		t.Fatal("應保留上一版 .prev")
	}
}

func TestUninstallKeepsDataUnlessConfirmed(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, _ := newManager(t, f, "不要\n")
	_ = m.Install(writeBin(t, "v1"))
	if err := os.WriteFile(filepath.Join(m.Paths.DataDir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Paths.DataDir, "keep")); err != nil {
		t.Fatal("沒輸入「刪除」時資料應保留")
	}
	if _, err := os.Stat(m.Paths.Bin); err == nil {
		t.Fatal("執行檔應被移除")
	}

	m2, _ := newManager(t, &fakeSys{healthy: true, linger: true}, "刪除\n")
	_ = m2.Install(writeBin(t, "v1"))
	if err := m2.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m2.Paths.DataDir); err == nil {
		t.Fatal("輸入「刪除」後資料應被刪除")
	}
}
