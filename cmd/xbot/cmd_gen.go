package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"xbot/internal/api"
)

// cmdGen：xbot gen ts [--out 檔案] [--check]。由操作登錄表產生前端 TypeScript（make gen 呼叫）。
func cmdGen(args []string) error {
	if len(args) == 0 || args[0] != "ts" {
		return errors.New("用法：xbot gen ts [--out 檔案] [--check]")
	}
	fs := flag.NewFlagSet("gen ts", flag.ContinueOnError)
	out := fs.String("out", api.GeneratedTSPath, "輸出檔案；- 表示印到畫面")
	check := fs.Bool("check", false, "只檢查檔案是不是最新的，過期就回錯誤（make check 用）")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	src := api.GenerateTS(cliRegistry().List())
	if *out == "-" {
		_, err := os.Stdout.Write(src)
		return err
	}
	cur, readErr := os.ReadFile(*out)
	upToDate := readErr == nil && bytes.Equal(cur, src)
	if *check {
		if !upToDate {
			return fmt.Errorf("%s 過期了（操作有改過），請執行 make gen 並提交", *out)
		}
		return nil
	}
	if upToDate {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		return err
	}
	fmt.Println("已更新", *out)
	return nil
}
