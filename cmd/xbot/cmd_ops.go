package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/auth"
	"xbot/internal/config"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
	"xbot/internal/opsclient"
)

// errSilent：錯誤已經印出來了，只要以非零結束。
var errSilent = errors.New("")

// cliRegistry 是指令列用的登錄表（只用來產生旗標與說明）。
func cliRegistry() *ops.Registry { return buildRegistry(nil, auth.NewPairing(nil), nil) }

// serviceURL 是執行中服務的網址：XBOT_URL，否則依 XBOT_BIND／XBOT_PORT（預設 http://127.0.0.1:8780）。
func serviceURL() (string, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", cfg, err
	}
	if v := strings.TrimSpace(os.Getenv("XBOT_URL")); v != "" {
		return strings.TrimRight(v, "/"), cfg, nil
	}
	return cfg.URL(), cfg, nil
}

// cliToken：XBOT_TOKEN（代理權杖）優先；否則讀這台機器的登入碼，以你本人的身分呼叫。
func cliToken(cfg config.Config) string {
	if v := strings.TrimSpace(os.Getenv("XBOT_TOKEN")); v != "" {
		return v
	}
	tok, err := auth.ReadAccessToken(cfg.DataDir)
	if err != nil {
		return ""
	}
	return tok
}

// cmdOps 列出所有操作。
func cmdOps(args []string) error {
	fs := flag.NewFlagSet("ops", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "輸出 JSON（含輸入／輸出 schema）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	infos := cliRegistry().List()
	if *asJSON {
		return printJSON(os.Stdout, infos)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "指令\t權限\t風險\t說明")
	for _, in := range infos {
		_, _ = fmt.Fprintf(w, "xbot %s %s\t%s\t%s\t%s\n", in.Domain, in.Action, in.Scope, in.Risk, in.Summary)
	}
	_ = w.Flush()
	fmt.Println("\n每個指令加 --help 看欄位。也可以用 MCP 呼叫同名工具（xbot.<領域>.<動作>）。")
	return nil
}

// isDomain 回答 name 是不是某個操作的領域。
func isDomain(reg *ops.Registry, name string) bool {
	for _, in := range reg.List() {
		if in.Domain == name {
			return true
		}
	}
	return false
}

// runOp 執行 xbot <領域> <動作> [旗標]。
func runOp(reg *ops.Registry, domain string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("用法：xbot %s <動作> [旗標]\n\n動作：\n", domain)
		for _, in := range reg.List() {
			if in.Domain == domain {
				fmt.Printf("  %-16s %s\n", in.Action, in.Summary)
			}
		}
		return nil
	}
	name := "xbot." + domain + "." + strings.ReplaceAll(args[0], "-", "_")
	info, ok := reg.Get(name)
	if !ok {
		return fmt.Errorf("沒有 xbot %s %s 這個指令（xbot %s --help 看可用的動作）", domain, args[0], domain)
	}
	parsed, err := opsclient.ParseArgs(info, args[1:], os.Stdin)
	if errors.Is(err, opsclient.ErrHelp) {
		fmt.Print(opsclient.Usage(info))
		return nil
	}
	if err != nil {
		return err
	}
	base, cfg, err := serviceURL()
	if err != nil {
		return err
	}
	c := &opsclient.Client{BaseURL: base, Token: cliToken(cfg)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := c.Call(ctx, name, parsed.Input, parsed.Opts)
	var oe *ops.Error
	if errors.As(err, &oe) {
		_ = printJSON(os.Stderr, map[string]any{"error": oe})
		return errSilent
	}
	if err != nil {
		return err
	}
	if res.DryRun {
		fmt.Fprintln(os.Stderr, "（預演：沒有真的執行）")
	}
	if res.NeedsApproval {
		fmt.Fprintln(os.Stderr, "（真的執行時會先送到「待我處理」）")
	}
	if res.Replayed {
		fmt.Fprintln(os.Stderr, "（同一個 request_id 已經做過，這是第一次的結果）")
	}
	return printJSON(os.Stdout, res.Output)
}

func printJSON(w io.Writer, v any) error {
	if raw, ok := v.(json.RawMessage); ok {
		var x any
		if err := json.Unmarshal(raw, &x); err == nil {
			v = x
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

const mcpUsage = `用法：
  xbot mcp [--url 網址] [--token 權杖 | --owner]   以 stdio 提供 XBot MCP（給 Claude Code、Codex 等本機代理）
  xbot mcp pair <配對碼> [--name 名字] [--url 網址]  用配對碼換代理權杖（只會顯示一次）

stdio 模式會連上執行中的服務（XBOT_URL，預設 http://127.0.0.1:8780）的 /mcp，所有呼叫都在服務裡執行、記稽核。
身分：
  --token 或環境變數 XBOT_TOKEN   代理權杖（建議：先在 XBot 產生配對碼，再用 xbot mcp pair 換權杖）
  --owner                         以你本人的身分（讀這台機器資料目錄裡的登入碼，所以只能在服務那台機器、同一個使用者執行）
`

// cmdMCP：xbot mcp（stdio）與 xbot mcp pair。
func cmdMCP(args []string) error {
	if len(args) > 0 && args[0] == "pair" {
		return cmdMCPPair(args[1:])
	}
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, mcpUsage) }
	urlFlag := fs.String("url", "", "服務網址（預設 XBOT_URL 或 http://127.0.0.1:8780）")
	tokenFlag := fs.String("token", "", "代理權杖（預設 XBOT_TOKEN）")
	owner := fs.Bool("owner", false, "以你本人的身分連線（讀本機登入碼）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, cfg, err := serviceURL()
	if err != nil {
		return err
	}
	if *urlFlag != "" {
		base = strings.TrimRight(*urlFlag, "/")
	}
	token := *tokenFlag
	if token == "" {
		token = strings.TrimSpace(os.Getenv("XBOT_TOKEN"))
	}
	if token == "" && *owner {
		if token, err = auth.ReadAccessToken(cfg.DataDir); err != nil {
			return err
		}
	}
	if token == "" {
		return errors.New("沒有權杖：請用 --token／XBOT_TOKEN 給代理權杖（xbot mcp pair <配對碼> 取得），或在服務那台機器上加 --owner")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	remote, err := mcpserver.NewRemote(ctx, func(ctx context.Context) (*mcp.ClientSession, error) {
		return mcpserver.Dial(ctx, base, token, nil)
	})
	if err != nil {
		return err
	}
	defer func() { _ = remote.Close() }()
	srv, err := mcpserver.Bridge(ctx, remote)
	if err != nil {
		return err
	}
	err = srv.Run(ctx, &mcp.StdioTransport{})
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func cmdMCPPair(args []string) error {
	fs := flag.NewFlagSet("mcp pair", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, mcpUsage) }
	urlFlag := fs.String("url", "", "服務網址（預設 XBOT_URL 或 http://127.0.0.1:8780）")
	name := fs.String("name", "", "這個代理的名字（產生配對碼時沒填才會用）")
	// 配對碼可以放在旗標前或後。
	var code string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		code, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if code == "" && fs.NArg() > 0 {
		code = fs.Arg(0)
	}
	if code == "" {
		return errors.New("要給配對碼：xbot mcp pair <配對碼>（在 XBot 產生，或 xbot agents pair_start --scopes read）")
	}
	base, _, err := serviceURL()
	if err != nil {
		return err
	}
	if *urlFlag != "" {
		base = strings.TrimRight(*urlFlag, "/")
	}
	c := &opsclient.Client{BaseURL: base}
	res, err := c.Pair(context.Background(), code, *name)
	var oe *ops.Error
	if errors.As(err, &oe) {
		_ = printJSON(os.Stderr, map[string]any{"error": oe})
		return errSilent
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✅ 已配對：%s（%s），權限 %v\n   權杖只會顯示這一次，請存好：\n", res.Agent.Name, res.Agent.ID, res.Agent.Scopes)
	fmt.Println(res.Token)
	fmt.Fprintf(os.Stderr, "\n接上 Claude Code：\n  claude mcp add --transport http xbot %s/mcp --header \"Authorization: Bearer <權杖>\"\n", base)
	return nil
}
