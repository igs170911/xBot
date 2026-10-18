// xbot 是 XBot 的單一執行檔：服務、指令列、安裝與檢查工具都在這裡。
//
// 之後由操作登錄表產生的 CLI（xbot bots list …）也會掛在這個執行檔下（C04）。
package main

import (
	"fmt"
	"os"
)

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

func commands() []command {
	return []command{
		{"serve", "啟動 XBot 服務", cmdServe},
		{"doctor", "檢查需要的工具與環境（--mode dev|run，--json）", cmdDoctor},
		{"setup", "安裝缺少的工具與套件（make setup 會先裝好 Go 再呼叫）", cmdSetup},
		{"service", "安裝與管理系統服務：install|update|uninstall|start|stop|restart|status|logs", cmdService},
		{"backup", "備份資料（--out 檔案，--include-logins）", cmdBackup},
		{"restore", "從備份還原資料", cmdRestore},
		{"token", "顯示其他裝置用的登入碼", cmdToken},
		{"version", "顯示版本", cmdVersion},
	}
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		usage()
		return
	}
	for _, c := range commands() {
		if c.name == os.Args[1] {
			if err := c.run(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "xbot:", err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "xbot：不認識的指令 %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Println("用法：xbot <指令> [參數]\n\n指令：")
	for _, c := range commands() {
		fmt.Printf("  %-10s %s\n", c.name, c.summary)
	}
}
