// Package setup 安裝 XBot 開發與執行需要的東西（make setup 的實際邏輯）。
//
// Go 本身由 scripts/bootstrap.sh 先裝好（沒有 Go 就跑不了這支程式）；
// 這裡負責其餘的工具、專案相依套件，以及需要 sudo 的系統套件（會先詢問）。
package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"xbot/internal/doctor"
)

// Options 是 setup 的參數。
type Options struct {
	// Root 是專案根目錄（有 go.mod 的地方）。
	Root string
	// Yes 為 true 時不詢問，直接執行需要 sudo 的步驟（YES=1）。
	Yes bool
	// WithTailscale 為 true 時也安裝 Tailscale（WITH_TAILSCALE=1）。
	WithTailscale bool
	// SkipBrowsers 為 true 時不下載 Playwright 瀏覽器（SKIP_BROWSERS=1）。
	SkipBrowsers bool
	DataDir      string
	Addr         string
	Out          io.Writer
	In           io.Reader
}

// Runner 執行外部指令；測試時可以替換。
type Runner func(dir, name string, args ...string) error

// SystemRunner 在 dir 執行指令，輸出直接接到終端機。
func SystemRunner(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

// Run 依序執行所有安裝步驟；回傳 true 表示最後的檢查全部通過。
func Run(o Options, env doctor.Env, run Runner) (bool, error) {
	s := &session{o: o, env: env, run: run, in: bufio.NewReader(o.In)}
	web := filepath.Join(o.Root, "web")

	s.step("安裝開發工具（mise：Go、Node、pnpm、staticcheck、air）", func() error {
		if _, err := env.LookPath("mise"); err != nil {
			return fmt.Errorf("找不到 mise，請先執行 make setup（會用 scripts/bootstrap.sh 安裝）")
		}
		return run(o.Root, "mise", "install")
	})
	s.step("下載 Go 套件", func() error { return run(o.Root, "go", "mod", "download") })
	s.step("安裝前端套件（pnpm）", func() error {
		args := []string{"install"}
		if _, err := os.Stat(filepath.Join(web, "pnpm-lock.yaml")); err == nil {
			args = append(args, "--frozen-lockfile")
		}
		return run(web, "pnpm", args...)
	})
	if !o.SkipBrowsers {
		s.step("下載測試用瀏覽器（Playwright Chromium）", func() error {
			return run(web, "pnpm", "exec", "playwright", "install", "chromium")
		})
	}

	// 需要 sudo 的系統項目：依 doctor 的結果處理。
	results := doctor.Check(env, doctor.Options{Mode: doctor.ModeDev, DataDir: o.DataDir, Addr: o.Addr})
	for _, r := range results {
		if r.Cmd == "" || r.Status == doctor.OK {
			continue
		}
		if strings.HasPrefix(r.Name, "Tailscale") {
			if !o.WithTailscale {
				continue
			}
		} else if !r.Required || r.Status != doctor.Fail {
			continue
		}
		s.system(r)
	}

	fmt.Fprintln(o.Out, "\n── 最後檢查（make doctor）──")
	final := doctor.Check(env, doctor.Options{Mode: doctor.ModeDev, DataDir: o.DataDir, Addr: o.Addr})
	fmt.Fprint(o.Out, doctor.Format(final))
	if s.failed > 0 {
		return false, fmt.Errorf("有 %d 個安裝步驟失敗，請看上面的訊息", s.failed)
	}
	return !doctor.Failed(final), nil
}

type session struct {
	o      Options
	env    doctor.Env
	run    Runner
	in     *bufio.Reader
	n      int
	failed int
}

func (s *session) step(title string, fn func() error) {
	s.n++
	fmt.Fprintf(s.o.Out, "\n[%d] %s\n", s.n, title)
	if err := fn(); err != nil {
		s.failed++
		fmt.Fprintf(s.o.Out, "   ❌ 失敗：%v\n", err)
		return
	}
	fmt.Fprintln(s.o.Out, "   ✅ 完成")
}

// system 處理需要 sudo 的項目：先說明要執行什麼，再詢問（YES=1 直接執行）。
func (s *session) system(r doctor.Result) {
	s.n++
	fmt.Fprintf(s.o.Out, "\n[%d] %s：%s\n   要執行：%s\n", s.n, r.Name, r.Detail, r.Cmd)
	if !s.o.Yes && !s.confirm("   要現在執行嗎？[y/N] ") {
		fmt.Fprintln(s.o.Out, "   ⏭  略過。之後可以自己執行上面那行，或用 make setup YES=1")
		return
	}
	if err := s.run(s.o.Root, "sh", "-c", r.Cmd); err != nil {
		s.failed++
		fmt.Fprintf(s.o.Out, "   ❌ 失敗：%v\n", err)
		return
	}
	fmt.Fprintln(s.o.Out, "   ✅ 完成")
	if strings.Contains(r.Cmd, "usermod") {
		fmt.Fprintln(s.o.Out, "   ⚠️  要登出再登入（或執行 newgrp docker）才會生效")
	}
}

func (s *session) confirm(prompt string) bool {
	fmt.Fprint(s.o.Out, prompt)
	line, err := s.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(s.o.Out)
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
