package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"xbot/internal/doctor"
)

func envWithoutDocker() doctor.Env {
	installed := map[string]bool{"mise": true, "go": true, "node": true, "pnpm": true, "git": true, "ssh": true}
	return doctor.Env{
		LookPath: func(b string) (string, error) {
			if installed[b] {
				return "/usr/bin/" + b, nil
			}
			return "", errors.New("not found")
		},
		Output:      func(string, ...string) (string, error) { return "v1", nil },
		FreeBytes:   func(string) (uint64, error) { return 50 << 30, nil },
		PortInUse:   func(string) bool { return false },
		Healthy:     func(string) bool { return false },
		ReadFile:    func(string) ([]byte, error) { return []byte("ID=debian\n"), nil },
		InContainer: func() bool { return false },
	}
}

type call struct{ dir, cmd string }

func recorder(calls *[]call) Runner {
	return func(dir, name string, args ...string) error {
		*calls = append(*calls, call{dir, name + " " + strings.Join(args, " ")})
		return nil
	}
}

func TestSetupAsksBeforeSudo(t *testing.T) {
	var calls []call
	out := &bytes.Buffer{}
	_, _ = Run(Options{Root: "/repo", Out: out, In: strings.NewReader("n\n"), DataDir: "/tmp", Addr: ":1", SkipBrowsers: true},
		envWithoutDocker(), recorder(&calls))
	for _, c := range calls {
		if strings.HasPrefix(c.cmd, "sh -c") {
			t.Fatalf("回答 n 時不該執行系統指令：%v", c)
		}
	}
	if !strings.Contains(out.String(), "get.docker.com") || !strings.Contains(out.String(), "略過") {
		t.Fatalf("應先顯示要執行的指令並略過：\n%s", out.String())
	}
	want := []string{"mise install", "go mod download", "pnpm install"}
	for i, w := range want {
		if i >= len(calls) || !strings.HasPrefix(calls[i].cmd, w) {
			t.Fatalf("第 %d 步應為 %q，實際 %v", i+1, w, calls)
		}
	}
	if calls[2].dir != "/repo/web" {
		t.Fatalf("pnpm 應在 web 目錄執行：%v", calls[2])
	}
}

func TestSetupYesRunsSudo(t *testing.T) {
	var calls []call
	_, _ = Run(Options{Root: "/repo", Yes: true, Out: &bytes.Buffer{}, In: strings.NewReader(""), DataDir: "/tmp", Addr: ":1", SkipBrowsers: true},
		envWithoutDocker(), recorder(&calls))
	found := false
	for _, c := range calls {
		if strings.HasPrefix(c.cmd, "sh -c") && strings.Contains(c.cmd, "get.docker.com") {
			found = true
		}
		if strings.Contains(c.cmd, "tailscale") {
			t.Fatal("沒有 WITH_TAILSCALE 不該安裝 Tailscale")
		}
	}
	if !found {
		t.Fatalf("YES=1 應直接安裝 Docker：%v", calls)
	}
}

func TestSetupWithTailscale(t *testing.T) {
	var calls []call
	_, _ = Run(Options{Root: "/repo", Yes: true, WithTailscale: true, Out: &bytes.Buffer{}, In: strings.NewReader(""), DataDir: "/tmp", Addr: ":1", SkipBrowsers: true},
		envWithoutDocker(), recorder(&calls))
	for _, c := range calls {
		if strings.Contains(c.cmd, "tailscale.com/install.sh") {
			return
		}
	}
	t.Fatalf("WITH_TAILSCALE=1 應安裝 Tailscale：%v", calls)
}

func TestSetupReportsFailedStep(t *testing.T) {
	run := func(dir, name string, args ...string) error {
		if name == "go" {
			return errors.New("network down")
		}
		return nil
	}
	out := &bytes.Buffer{}
	ok, err := Run(Options{Root: "/repo", Out: out, In: strings.NewReader("n\n"), DataDir: "/tmp", Addr: ":1", SkipBrowsers: true}, envWithoutDocker(), run)
	if ok || err == nil || !strings.Contains(out.String(), "network down") {
		t.Fatalf("失敗的步驟應回報：ok=%v err=%v\n%s", ok, err, out.String())
	}
}
