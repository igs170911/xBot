package apps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// 呼叫遠端 MCP（Streamable HTTP）：帶上連接的權杖或金鑰。測試連線與轉送 Bot 的請求共用。

// protocolVersion 是 XBot 自己連遠端時用的 MCP 版本（轉送 Bot 的請求時用 Bot 協商的版本）。
const protocolVersion = "2025-06-18"

const initializeBody = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` + protocolVersion +
	`","capabilities":{},"clientInfo":{"name":"XBot","version":"1"}}}`

// errUnauthorized 表示遠端回 401（權杖失效或金鑰不對）。
var errUnauthorized = errors.New("遠端拒絕了登入資料")

// credentials 回傳連接目前可用的祕密：OAuth 權杖快過期時先換新並存回去（force 表示不管到期時間都換）。
func (s *Service) credentials(ctx context.Context, a store.AppConnection, force bool) (secret, error) {
	sec, err := s.open(a.ID, a.Secret)
	if err != nil || sec.OAuth == nil || AuthKind(a.Auth) != AuthOAuth2 {
		return sec, err
	}
	if !force && !sec.OAuth.expiring(s.o.Now(), limits.AppTokenRefreshSkew) {
		return sec, nil
	}
	v, _ := s.refreshLocks.LoadOrStore(a.ID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	// 等鎖的時候可能已經有人換好了。
	cur, err := s.o.DB.GetAppConnection(ctx, a.ID)
	if err != nil {
		return sec, err
	}
	sec2, err := s.open(cur.ID, cur.Secret)
	if err != nil || sec2.OAuth == nil {
		return sec, err
	}
	if sec2.OAuth.AccessToken != sec.OAuth.AccessToken && !sec2.OAuth.expiring(s.o.Now(), limits.AppTokenRefreshSkew) {
		return sec2, nil
	}
	tr, err := s.refresh(ctx, sec2.OAuth)
	if err != nil {
		if errors.Is(err, errRelogin) {
			_ = s.o.DB.SetAppStatus(context.WithoutCancel(ctx), a.ID, StatusExpired, errRelogin.Error())
		}
		return sec2, err
	}
	sec2.OAuth.apply(tr, s.o.Now())
	sealed, err := s.seal(a.ID, sec2)
	if err != nil {
		return sec2, err
	}
	if err := s.o.DB.SetAppSecret(ctx, a.ID, sealed); err != nil {
		return sec2, err
	}
	return sec2, nil
}

// upstreamRequest 是送給遠端 MCP 的一個請求。
type upstreamRequest struct {
	Body     []byte
	Session  string // Mcp-Session-Id
	Protocol string // MCP-Protocol-Version
}

// send 把一個 JSON-RPC 訊息送到遠端，回傳 HTTP 回應（呼叫端要關 Body）。401 時回 errUnauthorized。
func (s *Service) send(ctx context.Context, a store.AppConnection, sec secret, r upstreamRequest) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range sec.Headers {
		req.Header.Set(k, v)
	}
	switch {
	case sec.OAuth != nil && sec.OAuth.AccessToken != "":
		req.Header.Set("Authorization", "Bearer "+sec.OAuth.AccessToken)
	case sec.APIKey != "":
		h := sec.TokenHeader
		if h == "" {
			h = "Authorization"
		}
		v := sec.APIKey
		if strings.EqualFold(h, "Authorization") && !strings.HasPrefix(strings.ToLower(v), "bearer ") && !strings.HasPrefix(strings.ToLower(v), "basic ") {
			v = "Bearer " + v
		}
		req.Header.Set(h, v)
	}
	if r.Session != "" {
		req.Header.Set("Mcp-Session-Id", r.Session)
	}
	if r.Protocol != "" {
		req.Header.Set("MCP-Protocol-Version", r.Protocol)
	}
	resp, err := s.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("連不上 %s：%w", a.Title, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusForbidden && strings.Contains(resp.Header.Get("WWW-Authenticate"), "invalid_token")) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, errUnauthorized
	}
	return resp, nil
}

// readMessages 讀回應裡的 JSON-RPC 訊息：一般 JSON，或 SSE 串流的 data 行。
func readMessages(resp *http.Response) ([]json.RawMessage, bool, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, limits.AppBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limits.AppBodyBytes {
		return nil, false, errors.New("遠端的回應太大")
	}
	sse := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	if !sse {
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, false, nil
		}
		trimmed := bytes.TrimSpace(body)
		if trimmed[0] == '[' { // 舊版的批次回應
			var many []json.RawMessage
			if err := json.Unmarshal(trimmed, &many); err != nil {
				return nil, false, err
			}
			return many, false, nil
		}
		if !json.Valid(trimmed) {
			return nil, false, errors.New("遠端的回應不是 JSON")
		}
		return []json.RawMessage{json.RawMessage(trimmed)}, false, nil
	}
	var out []json.RawMessage
	var data []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), int(limits.AppBodyBytes))
	flush := func() {
		if len(data) > 0 {
			if msg := []byte(strings.Join(data, "\n")); json.Valid(msg) {
				out = append(out, json.RawMessage(msg))
			}
			data = nil
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			flush()
		}
	}
	flush()
	return out, true, sc.Err()
}

// rpcMessage 是 JSON-RPC 訊息裡我們會看的欄位。
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// listTools 由 XBot 自己連一次遠端（initialize → initialized → tools/list），回傳工具清單（測試連線）。
func (s *Service) listTools(ctx context.Context, a store.AppConnection) ([]store.AppTool, error) {
	sec, err := s.credentials(ctx, a, false)
	if err != nil {
		return nil, err
	}
	call := func(body []byte, session string) ([]rpcMessage, string, error) {
		resp, err := s.send(ctx, a, sec, upstreamRequest{Body: body, Session: session, Protocol: protocolVersionIf(session)})
		if errors.Is(err, errUnauthorized) && sec.OAuth != nil && sec.OAuth.RefreshToken != "" {
			if sec, err = s.credentials(ctx, a, true); err == nil {
				resp, err = s.send(ctx, a, sec, upstreamRequest{Body: body, Session: session, Protocol: protocolVersionIf(session)})
			}
		}
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return nil, "", fmt.Errorf("%s 回應 HTTP %d：%s", a.Title, resp.StatusCode, truncate(strings.TrimSpace(string(b)), 200))
		}
		raw, _, err := readMessages(resp)
		if err != nil {
			return nil, "", err
		}
		var out []rpcMessage
		for _, m := range raw {
			var msg rpcMessage
			if json.Unmarshal(m, &msg) == nil {
				out = append(out, msg)
			}
		}
		return out, resp.Header.Get("Mcp-Session-Id"), nil
	}
	msgs, session, err := call([]byte(initializeBody), "")
	if err != nil {
		return nil, err
	}
	if res := response(msgs, "0"); res == nil || res.Error != nil {
		if res != nil && res.Error != nil {
			return nil, fmt.Errorf("%s 不接受連線：%s", a.Title, truncate(res.Error.Message, 200))
		}
		return nil, fmt.Errorf("%s 沒有回應 initialize", a.Title)
	}
	if _, _, err := call([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), session); err != nil {
		return nil, err
	}
	var tools []store.AppTool
	cursor := ""
	for page := 1; page <= 20 && len(tools) < limits.AppToolsMax; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": page, "method": "tools/list", "params": params})
		msgs, _, err := call(body, session)
		if err != nil {
			return nil, err
		}
		res := response(msgs, fmt.Sprint(page))
		if res == nil {
			return nil, fmt.Errorf("%s 沒有回應 tools/list", a.Title)
		}
		if res.Error != nil {
			return nil, fmt.Errorf("%s 列不出工具：%s", a.Title, truncate(res.Error.Message, 200))
		}
		var list struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		_ = json.Unmarshal(res.Result, &list)
		for _, t := range list.Tools {
			if t.Name != "" && len(tools) < limits.AppToolsMax {
				tools = append(tools, store.AppTool{Name: t.Name, Description: truncate(t.Description, 300)})
			}
		}
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	if tools == nil {
		tools = []store.AppTool{}
	}
	return tools, nil
}

func protocolVersionIf(session string) string {
	if session == "" {
		return ""
	}
	return protocolVersion
}

func response(msgs []rpcMessage, id string) *rpcMessage {
	for i := range msgs {
		if strings.Trim(string(msgs[i].ID), `"`) == id && (msgs[i].Result != nil || msgs[i].Error != nil) {
			return &msgs[i]
		}
	}
	return nil
}
