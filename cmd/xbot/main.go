// xbot 是 XBot 的單一執行檔：服務、指令列、安裝與檢查工具都在這裡。
//
// 操作登錄表產生的指令（xbot <領域> <動作> …）也在這裡：不是內建指令的第一個參數會當成操作的領域。
package main

import (
	"errors"
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
		{"service", "安裝與管理系統服務：install|update|update-computers|uninstall|start|stop|restart|status|logs", cmdService},
		{"host", "Bot 電腦主機：host add|check|script（make host-add、make host-check）", cmdHost},
		{"deploy", "把 XBot 部署到另一台機器（make deploy HOST=使用者@位址）", cmdDeploy},
		{"backup", "備份資料目錄與 Bot 電腦的資料（--out 檔案，--include-logins，--no-computers）", cmdBackup},
		{"restore", "從備份還原（--no-computers，--computers-only --bot id）", cmdRestore},
		{"token", "顯示其他裝置用的登入碼", cmdToken},
		{"mcp", "以 stdio 提供 XBot MCP；mcp pair <配對碼> 換代理權杖", cmdMCP},
		{"ops", "列出所有操作（xbot <領域> <動作> 呼叫）", cmdOps},
		{"gen", "由操作登錄表產生程式碼：gen ts（前端型別，make gen）", cmdGen},
		{"dev", "開發用：dev chat --bot <名稱> \"訊息\"（對 Bot 說一句話並印出串流回覆）", cmdDev},
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
			exit(c.run(os.Args[2:]))
			return
		}
	}
	if reg := cliRegistry(); isDomain(reg, os.Args[1]) {
		exit(runOp(reg, os.Args[1], os.Args[2:]))
		return
	}
	fmt.Fprintf(os.Stderr, "xbot：不認識的指令 %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func exit(err error) {
	if err == nil {
		return
	}
	if !errors.Is(err, errSilent) {
		fmt.Fprintln(os.Stderr, "xbot:", err)
	}
	os.Exit(1)
}

func usage() {
	fmt.Println("用法：xbot <指令> [參數]\n\n指令：")
	for _, c := range commands() {
		fmt.Printf("  %-10s %s\n", c.name, c.summary)
	}
	fmt.Println("\n操作（呼叫執行中的服務；xbot ops 看全部）：\n  xbot <領域> <動作> [--欄位 值 …] [--json '{…}'] [--dry-run] [--request-id ID]")
}
