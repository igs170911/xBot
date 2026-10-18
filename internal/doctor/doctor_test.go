package doctor

import (
	"errors"
	"strings"
	"testing"
)

// fakeEnv 模擬一台機器：installed 是有安裝的指令，outputs 是指令的輸出。
func fakeEnv(installed map[string]bool, outputs map[string]string, errs map[string]bool) Env {
	return Env{
		LookPath: func(b string) (string, error) {
			if installed[b] {
				return "/usr/bin/" + b, nil
			}
			return "", errors.New("not found")
		},
		Output: func(name string, args ...string) (string, error) {
			k := name + " " + strings.Join(args, " ")
			if errs[k] {
				return outputs[k], errors.New("exit 1")
			}
			return outputs[k], nil
		},
		FreeBytes:   func(string) (uint64, error) { return 50 << 30, nil },
		PortInUse:   func(string) bool { return false },
		Healthy:     func(string) bool { return false },
		ReadFile:    func(string) ([]byte, error) { return []byte("ID=debian\n"), nil },
		InContainer: func() bool { return false },
	}
}

func find(rs []Result, name string) Result {
	for _, r := range rs {
		if strings.HasPrefix(r.Name, name) {
			return r
		}
	}
	return Result{}
}

var allTools = map[string]bool{"mise": true, "go": true, "node": true, "pnpm": true, "git": true, "docker": true, "ssh": true}

func TestAllGood(t *testing.T) {
	env := fakeEnv(allTools, map[string]string{
		"docker version --format {{.Server.Version}}": "29.7.2",
		"docker compose version --short":              "5.5.1",
		"go version":                                  "go version go1.27.2 linux/amd64",
	}, nil)
	rs := Check(env, Options{Mode: ModeDev, DataDir: "/tmp", Addr: "127.0.0.1:8780"})
	if Failed(rs) {
		t.Fatalf("應該全部通過：\n%s", Format(rs))
	}
	if r := find(rs, "Go"); r.Detail != "go version go1.27.2 linux/amd64" {
		t.Fatalf("Go 版本沒顯示：%+v", r)
	}
}

func TestMissingGoFails(t *testing.T) {
	tools := map[string]bool{"node": true, "pnpm": true, "git": true, "docker": true}
	env := fakeEnv(tools, map[string]string{"docker version --format {{.Server.Version}}": "29.0.0"}, nil)
	rs := Check(env, Options{Mode: ModeDev, DataDir: "/tmp", Addr: "127.0.0.1:8780"})
	if !Failed(rs) {
		t.Fatal("缺 Go 應該失敗")
	}
	r := find(rs, "Go")
	if r.Status != Fail || r.Fix != "make setup" {
		t.Fatalf("Go 檢查結果錯誤：%+v", r)
	}
	out := Format(rs)
	if !strings.Contains(out, "❌ Go") || !strings.Contains(out, "修正：make setup") {
		t.Fatalf("輸出應顯示 ❌ 和修正指令：\n%s", out)
	}
}

func TestRunModeSkipsDevTools(t *testing.T) {
	env := fakeEnv(map[string]bool{"docker": true}, map[string]string{"docker version --format {{.Server.Version}}": "27.1.0"}, nil)
	rs := Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: "127.0.0.1:8780"})
	if Failed(rs) {
		t.Fatalf("執行模式不需要 Go：\n%s", Format(rs))
	}
	if find(rs, "Go").Name != "" {
		t.Fatal("執行模式不該檢查 Go")
	}
}

func TestDockerProblems(t *testing.T) {
	cases := []struct {
		name, out string
		fail      bool
		want      string
	}{
		{"太舊", "24.0.7", false, "太舊"},
		{"沒權限", "permission denied while trying to connect", true, "usermod -aG docker"},
		{"沒執行", "Cannot connect to the Docker daemon", true, "systemctl enable --now docker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := "docker version --format {{.Server.Version}}"
			env := fakeEnv(allTools, map[string]string{k: c.out}, map[string]bool{k: c.fail})
			r := find(Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: ":1"}), "Docker")
			if r.Status != Fail || !(strings.Contains(r.Detail, c.want) || strings.Contains(r.Fix, c.want)) {
				t.Fatalf("結果錯誤：%+v", r)
			}
		})
	}
}

func TestDockerInstallFixPerDistro(t *testing.T) {
	env := fakeEnv(map[string]bool{}, nil, nil)
	env.ReadFile = func(string) ([]byte, error) { return []byte("ID=omarchy\nID_LIKE=arch\n"), nil }
	r := find(Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: ":1"}), "Docker")
	if !strings.Contains(r.Fix, "pacman") {
		t.Fatalf("Arch 系應建議 pacman：%s", r.Fix)
	}
}

func TestDockerInContainerIsOptional(t *testing.T) {
	env := fakeEnv(map[string]bool{}, nil, nil)
	env.InContainer = func() bool { return true }
	rs := Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: ":1"})
	if Failed(rs) {
		t.Fatalf("容器內缺 Docker 不算必要失敗：\n%s", Format(rs))
	}
}

func TestDiskAndPort(t *testing.T) {
	env := fakeEnv(allTools, map[string]string{"docker version --format {{.Server.Version}}": "29.0.0"}, nil)
	env.FreeBytes = func(string) (uint64, error) { return 1 << 30, nil }
	env.PortInUse = func(string) bool { return true }
	rs := Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: "127.0.0.1:8780"})
	if r := find(rs, "磁碟空間"); r.Status != Fail {
		t.Fatalf("1 GB 應該失敗：%+v", r)
	}
	if r := find(rs, "服務埠"); r.Status != Warn || !strings.Contains(r.Fix, "XBOT_PORT") {
		t.Fatalf("埠被占用應警告：%+v", r)
	}
	env.Healthy = func(string) bool { return true }
	if r := find(Check(env, Options{Mode: ModeRun, DataDir: "/tmp", Addr: "127.0.0.1:8780"}), "服務埠"); r.Status != OK {
		t.Fatalf("XBot 自己占用應算 OK：%+v", r)
	}
}

func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{"/a/b/c": "/a/b", "/a": "/", "/": "/"} {
		if got := parentDir(in); got != want {
			t.Fatalf("parentDir(%q)=%q，應為 %q", in, got, want)
		}
	}
}
