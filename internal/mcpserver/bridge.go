package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/ops"
	"xbot/internal/version"
)

// Dial 用權杖連上服務的 /mcp（Streamable HTTP）。baseURL 例：http://127.0.0.1:8780。
func Dial(ctx context.Context, baseURL, token string, hc *http.Client) (*mcp.ClientSession, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	authed := *hc
	authed.Transport = bearer{token: token, base: base}
	c := mcp.NewClient(&mcp.Implementation{Name: "xbot-cli", Version: version.Version}, nil)
	endpoint := strings.TrimRight(baseURL, "/") + Path
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &authed}, nil)
	if err != nil {
		return nil, fmt.Errorf("連不上 XBot 的 MCP（%s）：%w", endpoint, err)
	}
	return cs, nil
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}

// Remote 是一條連到服務 /mcp 的連線；斷線（例：服務重開）時下一次呼叫會自動重連一次。
type Remote struct {
	dial func(ctx context.Context) (*mcp.ClientSession, error)

	mu sync.Mutex
	cs *mcp.ClientSession
}

// NewRemote 建立連線（dial 會先呼叫一次，確認連得上、權杖有效）。
func NewRemote(ctx context.Context, dial func(ctx context.Context) (*mcp.ClientSession, error)) (*Remote, error) {
	cs, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	return &Remote{dial: dial, cs: cs}, nil
}

func (r *Remote) session() *mcp.ClientSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cs
}

func (r *Remote) redial(ctx context.Context, old *mcp.ClientSession) (*mcp.ClientSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cs != old { // 別的呼叫已經重連好了
		return r.cs, nil
	}
	cs, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	_ = old.Close()
	r.cs = cs
	return cs, nil
}

// CallTool 呼叫遠端工具。服務重開造成連線失效（請求確定沒送到）時重連再試一次；
// 其他錯誤不重試，避免同一個動作做兩次。
func (r *Remote) CallTool(ctx context.Context, name string, args json.RawMessage) (*mcp.CallToolResult, error) {
	cs := r.session()
	params := &mcp.CallToolParams{Name: name, Arguments: ArgsValue(args)}
	res, err := cs.CallTool(ctx, params)
	if err == nil || ctx.Err() != nil || !(errors.Is(err, mcp.ErrSessionMissing) || errors.Is(err, mcp.ErrConnectionClosed)) {
		return res, err
	}
	cs2, derr := r.redial(ctx, cs)
	if derr != nil {
		return nil, fmt.Errorf("%v（重新連線也失敗：%v）", err, derr)
	}
	return cs2.CallTool(ctx, params)
}

// Tools 列出遠端所有工具。
func (r *Remote) Tools(ctx context.Context) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for t, err := range r.session().Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Close 關閉連線。
func (r *Remote) Close() error {
	return r.session().Close()
}

// Bridge 產生一個本機 MCP 伺服器（給 stdio 用），工具和遠端服務一樣，呼叫轉給遠端。
// 這樣 stdio 和 HTTP 是同一個伺服器、同一個身分（權杖），稽核、事件、核准都在服務裡發生。
func Bridge(ctx context.Context, remote *Remote) (*mcp.Server, error) {
	tools, err := remote.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("讀不到工具清單：%w", err)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: Name, Title: "XBot", Version: version.Version},
		&mcp.ServerOptions{Instructions: Instructions, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	for _, t := range tools {
		name := t.Name
		srv.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args json.RawMessage
			if req.Params != nil {
				args = req.Params.Arguments
			}
			res, err := remote.CallTool(ctx, name, args)
			if err != nil {
				e := ops.Errorf(ops.CodeInternal, "連不上 XBot 服務：%v", err)
				return errorResult(e), nil
			}
			return res, nil
		})
	}
	return srv, nil
}

// ArgsValue 把 JSON 參數解開成一般的值再交給 SDK（SDK 的編碼器不保證認得 json.RawMessage）。數字保留原文。
func ArgsValue(raw json.RawMessage) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || v == nil {
		return map[string]any{}
	}
	return v
}
