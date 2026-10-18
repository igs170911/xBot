// Package opsclient 讓指令列（xbot <領域> <動作>、xbot mcp pair）呼叫執行中的 XBot 服務。
//
// 呼叫方式（C03 的 HTTP 出口）：
//
//	POST <base>/api/v1/<操作名稱去掉 xbot.>    例：/api/v1/bots.create
//	Authorization: Bearer <登入碼或代理權杖>     （沒有就不帶，交給服務決定本機是否免登入）
//	X-Request-Id: <request_id>                  （可省略，MCP-4）
//	X-Dry-Run: 1                                （可省略）
//	內容：操作的輸入 JSON
//
// 成功回 200 與 ops.Result（{op, output, dry_run?, needs_approval?, replayed?}）；
// 失敗回 {"error": {code, message, retryable, needs_approval_id?}}（MCP-5）。
// 服務還沒有這條路由（回 404／405 且不是上面的錯誤格式）時，改走 /mcp 的同名工具，結果一樣。
package opsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/auth"
	"xbot/internal/mcpserver"
	"xbot/internal/ops"
)

// 請求標頭。
const (
	HeaderRequestID = "X-Request-Id"
	HeaderDryRun    = "X-Dry-Run"
)

// Client 呼叫一個執行中的 XBot 服務。
type Client struct {
	BaseURL string       // 例：http://127.0.0.1:8780
	Token   string       // 登入碼（使用者本人）或代理權杖；空白就不帶
	HTTP    *http.Client // 空白用預設（60 秒逾時）
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) base() string { return strings.TrimRight(c.BaseURL, "/") }

// RoutePath 是操作的 HTTP 路徑：xbot.bots.create → /api/v1/bots.create。
func RoutePath(op string) string {
	return "/api/v1/" + strings.TrimPrefix(op, "xbot.")
}

// Call 呼叫一個操作。錯誤是 *ops.Error（服務回的錯誤）或連線錯誤。
func (c *Client) Call(ctx context.Context, op string, input json.RawMessage, opts ops.Options) (ops.Result, error) {
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+RoutePath(op), bytes.NewReader(input))
	if err != nil {
		return ops.Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if opts.RequestID != "" {
		req.Header.Set(HeaderRequestID, opts.RequestID)
	}
	if opts.DryRun {
		req.Header.Set(HeaderDryRun, "1")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return ops.Result{}, fmt.Errorf("連不上 XBot 服務（%s）：%w。服務有在執行嗎？可用 XBOT_URL 指定網址", c.base(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return ops.Result{}, err
	}
	if e := parseError(body); e != nil {
		return ops.Result{Op: op}, e
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return c.callMCP(ctx, op, input, opts) // 服務還沒有 /api/v1 路由
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ops.Result{Op: op}, ops.Forbidden("服務不接受這個身分：請確認 XBOT_TOKEN，或在服務那台機器上執行")
	}
	if resp.StatusCode/100 != 2 {
		return ops.Result{Op: op}, fmt.Errorf("服務回了 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseResult(op, body)
}

// parseError 認得 {"error": {code, message}} 或直接是 {code, message}；不是錯誤格式回 nil。
func parseError(body []byte) *ops.Error {
	var wrapped struct {
		Error *ops.Error `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error != nil && wrapped.Error.Code != "" {
		return wrapped.Error
	}
	return nil
}

func parseResult(op string, body []byte) (ops.Result, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return ops.Result{}, fmt.Errorf("服務回的不是 JSON：%s", strings.TrimSpace(string(body)))
	}
	if _, ok := probe["output"]; !ok {
		return ops.Result{Op: op, Output: body}, nil // 直接回輸出的寫法也接受
	}
	var res ops.Result
	if err := json.Unmarshal(body, &res); err != nil {
		return ops.Result{}, err
	}
	if res.Op == "" {
		res.Op = op
	}
	return res, nil
}

// callMCP 經 /mcp 呼叫同名工具。
func (c *Client) callMCP(ctx context.Context, op string, input json.RawMessage, opts ops.Options) (ops.Result, error) {
	args := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &args); err != nil {
		return ops.Result{}, ops.InvalidInput(ops.FieldError{Message: "輸入要是 JSON 物件"})
	}
	if opts.RequestID != "" {
		args[mcpserver.ArgRequestID], _ = json.Marshal(opts.RequestID)
	}
	if opts.DryRun {
		args[mcpserver.ArgDryRun] = json.RawMessage("true")
	}
	raw, _ := json.Marshal(args)
	cs, err := mcpserver.Dial(ctx, c.BaseURL, c.Token, c.HTTP)
	if err != nil {
		return ops.Result{}, err
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: op, Arguments: mcpserver.ArgsValue(raw)})
	if err != nil {
		return ops.Result{}, err
	}
	return FromToolResult(op, res)
}

// FromToolResult 把 MCP 工具結果轉回 ops.Result／*ops.Error。
func FromToolResult(op string, res *mcp.CallToolResult) (ops.Result, error) {
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return ops.Result{}, err
	}
	if res.IsError {
		var e ops.Error
		if json.Unmarshal(structured, &e) == nil && e.Code != "" {
			return ops.Result{Op: op}, &e
		}
		return ops.Result{Op: op}, errors.New(toolText(res))
	}
	out := ops.Result{Op: op, Output: structured}
	if res.StructuredContent == nil {
		out.Output = json.RawMessage(toolText(res))
	}
	out.DryRun, _ = res.Meta["xbot/dry_run"].(bool)
	out.NeedsApproval, _ = res.Meta["xbot/needs_approval"].(bool)
	out.Replayed, _ = res.Meta["xbot/replayed"].(bool)
	return out, nil
}

func toolText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// Pair 用配對碼換權杖（POST /api/v1/agents.pair，不需要登入）。
func (c *Client) Pair(ctx context.Context, code, name string) (auth.PairResult, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+auth.PairPath, bytes.NewReader(body))
	if err != nil {
		return auth.PairResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return auth.PairResult{}, fmt.Errorf("連不上 XBot 服務（%s）：%w", c.base(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return auth.PairResult{}, err
	}
	if e := parseError(b); e != nil {
		return auth.PairResult{}, e
	}
	if resp.StatusCode != http.StatusOK {
		return auth.PairResult{}, fmt.Errorf("服務回了 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var res auth.PairResult
	if err := json.Unmarshal(b, &res); err != nil || res.Token == "" {
		return auth.PairResult{}, fmt.Errorf("服務回的內容看不懂：%s", strings.TrimSpace(string(b)))
	}
	return res, nil
}
