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
package opsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"xbot/internal/auth"
	"xbot/internal/limits"
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
	HTTP    *http.Client // 空白用預設（逾時 limits.ChatWaitMax＋1 分鐘）
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// 等待類操作（chat.wait_until_settled、approvals.wait）最多等 limits.ChatWaitMax，再多留一分鐘。
	return &http.Client{Timeout: limits.ChatWaitMax + time.Minute}
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
