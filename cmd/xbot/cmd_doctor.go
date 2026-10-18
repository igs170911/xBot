package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"xbot/internal/config"
	"xbot/internal/doctor"
	"xbot/internal/setup"
)

// errChecksFailed 讓 doctor 以非零結束，但不再多印一行錯誤。
var errChecksFailed = errors.New("有必要項目沒通過")

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	mode := fs.String("mode", "", "dev（有原始碼，預設在專案目錄內）或 run（只執行服務）")
	asJSON := fs.Bool("json", false, "輸出 JSON（給程式或 Agent 讀）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m := doctor.Mode(*mode)
	if m == "" {
		m = doctor.ModeRun
		if _, err := findRoot(); err == nil {
			m = doctor.ModeDev
		}
	}
	rs := doctor.Check(doctor.SystemEnv(), doctor.Options{Mode: m, DataDir: cfg.DataDir, Addr: cfg.Addr()})
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rs); err != nil {
			return err
		}
	} else {
		fmt.Print(doctor.Format(rs))
	}
	if doctor.Failed(rs) {
		os.Exit(1)
	}
	return nil
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ok, err := setup.Run(setup.Options{
		Root:          root,
		Yes:           os.Getenv("YES") == "1",
		WithTailscale: os.Getenv("WITH_TAILSCALE") == "1",
		SkipBrowsers:  os.Getenv("SKIP_BROWSERS") == "1",
		DataDir:       cfg.DataDir,
		Addr:          cfg.Addr(),
		Out:           os.Stdout,
		In:            os.Stdin,
	}, doctor.SystemEnv(), setup.SystemRunner)
	if err != nil {
		return err
	}
	if !ok {
		return errChecksFailed
	}
	return nil
}

// findRoot 從目前目錄往上找 go.mod，找到的就是專案根目錄。
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("找不到專案目錄（go.mod），請在 XBot 專案裡執行")
		}
		dir = parent
	}
}
