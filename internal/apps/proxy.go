package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"xbot/internal/ipc"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 轉送 Bot 的 MCP 到遠端（product.sock 上的 /internal/apps/<名字>，C34、SEC-5）。
//
// Bot 電腦裡的 XBot MCP 橋接逐行把 JSON-RPC 送來（無狀態）；XBot 在這裡：
//   - 確認這個 Bot 有授權（身分由 product.sock 決定，worker-key 已在 ipc 驗過）；
//   - 帶上權杖或金鑰（快過期先換新；401 換新後重試一次），權杖從頭到尾不進容器；
//   - 幫 Bot 保管遠端的 Mcp-Session-Id（遠端 session 過期時自動重新 initialize 再重送）；
//   - 拿掉你關掉的工具、工具結果超過上限時截斷，並記下最近使用（I02）。

// upSession 是一個 Bot 對一個連接的遠端 session。
type upSession struct {
	mu       sync.Mutex
	id       string // Mcp-Session-Id
	protocol string
	init     []byte // Bot 的 initialize（遠端 session 過期時重送）
}

func (s *Service) session(botID, connectionID string) *upSession {
	v, _ := s.sessions.LoadOrStore(botID+"\x00"+connectionID, &upSession{})
	return v.(*upSession)
}

// Proxy 回傳一個 Bot 的轉送處理器（接到 ipc.Server.SetApps）。
func (s *Service) Proxy(botID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodDelete {
			// 橋接不開伺服器推送的串流，也不主動結束 session。
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, ipc.AppsPrefix)
		body, err := io.ReadAll(io.LimitReader(r.Body, limits.AppBodyBytes+1))
		if err != nil || int64(len(body)) > limits.AppBodyBytes {
			rpcFail(w, nil, "請求太大或讀不到")
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			rpcFail(w, nil, "請求不是 JSON-RPC")
			return
		}
		s.forward(r.Context(), w, botID, name, body, msg, r.Header.Get("MCP-Protocol-Version"))
	})
}

func (s *Service) forward(ctx context.Context, w http.ResponseWriter, botID, name string, body []byte, msg rpcMessage, protocol string) {
	a, err := s.o.DB.GetAppConnectionByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		rpcFail(w, msg.ID, "找不到這個連接（可能已經中斷了）")
		return
	}
	if err != nil {
		rpcFail(w, msg.ID, "XBot 讀不到連接的設定")
		return
	}
	ok, err := s.o.DB.AppGranted(ctx, a.ID, botID)
	if err != nil || !ok {
		rpcFail(w, msg.ID, "你沒有被授權使用「"+a.Title+"」，請使用者到「連接應用程式」授權給你")
		return
	}
	if Transport(a.Transport) != TransportHTTP {
		rpcFail(w, msg.ID, "這個連接是本機程式，不經 XBot 轉送")
		return
	}
	switch a.Status {
	case StatusNeedsSignin, StatusExpired:
		rpcFail(w, msg.ID, "「"+a.Title+"」需要重新登入，請使用者到「連接應用程式」重新連接")
		return
	case StatusDisabled:
		rpcFail(w, msg.ID, "「"+a.Title+"」已停用")
		return
	}
	tool := ""
	if msg.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		tool = p.Name
		if s.toolOff(a, tool) {
			rpcFail(w, msg.ID, "這個工具已被使用者關掉："+tool)
			return
		}
	}
	us := s.session(botID, a.ID)
	us.mu.Lock()
	if msg.Method == "initialize" {
		us.init, us.id = body, ""
	}
	session, proto := us.id, us.protocol
	us.mu.Unlock()
	if protocol != "" {
		proto = protocol
	}

	sec, err := s.credentials(ctx, a, false)
	if err != nil {
		rpcFail(w, msg.ID, s.authMessage(a, err))
		return
	}
	resp, err := s.send(ctx, a, sec, upstreamRequest{Body: body, Session: session, Protocol: proto})
	if errors.Is(err, errUnauthorized) {
		resp, err = s.retryAuth(ctx, a, body, session, proto)
	}
	if err == nil && resp.StatusCode == http.StatusNotFound && msg.Method != "initialize" {
		// 遠端的 session 過期了：用 Bot 原本的 initialize 重新開一個再重送（Bot 不會發現）。
		resp.Body.Close()
		if session, err = s.reinit(ctx, a, us, proto); err == nil {
			sec, err = s.credentials(ctx, a, false)
			if err == nil {
				resp, err = s.send(ctx, a, sec, upstreamRequest{Body: body, Session: session, Protocol: proto})
			}
		}
	}
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			_ = s.o.DB.SetAppStatus(context.WithoutCancel(ctx), a.ID, s.reloginStatus(a), "遠端拒絕了登入資料")
		}
		if tool != "" {
			s.recordUse(ctx, a.ID, botID, tool, false)
		}
		rpcFail(w, msg.ID, s.authMessage(a, err))
		return
	}
	defer resp.Body.Close()
	if msg.Method == "initialize" {
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" || resp.StatusCode < 300 {
			us.mu.Lock()
			us.id = id
			us.mu.Unlock()
		}
	}
	if resp.StatusCode == http.StatusAccepted {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if tool != "" {
			s.recordUse(ctx, a.ID, botID, tool, false)
		}
		rpcFail(w, msg.ID, a.Title+" 回應 HTTP "+http.StatusText(resp.StatusCode)+"："+truncate(strings.TrimSpace(string(b)), 200))
		return
	}
	msgs, sse, err := readMessages(resp)
	if err != nil {
		rpcFail(w, msg.ID, a.Title+" 的回應看不懂："+err.Error())
		return
	}
	if len(msgs) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	for i, m := range msgs {
		msgs[i] = s.rewrite(ctx, a, botID, msg, tool, m)
	}
	if msg.Method == "initialize" {
		var res rpcMessage
		if json.Unmarshal(msgs[len(msgs)-1], &res) == nil && res.Result != nil {
			var r struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(res.Result, &r) == nil && r.ProtocolVersion != "" {
				us.mu.Lock()
				us.protocol = r.ProtocolVersion
				us.mu.Unlock()
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if !sse && len(msgs) == 1 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(msgs[0])
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	var out bytes.Buffer
	for _, m := range msgs {
		out.WriteString("event: message\ndata: ")
		out.Write(m)
		out.WriteString("\n\n")
	}
	_, _ = w.Write(out.Bytes())
}

// retryAuth：401 時換新權杖再送一次（API 金鑰沒得換，直接回錯）。
func (s *Service) retryAuth(ctx context.Context, a store.AppConnection, body []byte, session, proto string) (*http.Response, error) {
	if AuthKind(a.Auth) != AuthOAuth2 {
		return nil, errUnauthorized
	}
	sec, err := s.credentials(ctx, a, true)
	if err != nil {
		return nil, err
	}
	return s.send(ctx, a, sec, upstreamRequest{Body: body, Session: session, Protocol: proto})
}

// reinit 用 Bot 的 initialize 重新開一個遠端 session，回傳新的 session id。
func (s *Service) reinit(ctx context.Context, a store.AppConnection, us *upSession, proto string) (string, error) {
	us.mu.Lock()
	init := us.init
	us.mu.Unlock()
	if init == nil {
		init = []byte(initializeBody)
	}
	sec, err := s.credentials(ctx, a, false)
	if err != nil {
		return "", err
	}
	resp, err := s.send(ctx, a, sec, upstreamRequest{Body: init})
	if err != nil {
		return "", err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, limits.AppBodyBytes))
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", errors.New(a.Title + " 無法重新連線")
	}
	id := resp.Header.Get("Mcp-Session-Id")
	if r, err := s.send(ctx, a, sec, upstreamRequest{Body: []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), Session: id, Protocol: proto}); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 64<<10))
		r.Body.Close()
	}
	us.mu.Lock()
	us.id = id
	us.mu.Unlock()
	return id, nil
}

func (s *Service) reloginStatus(a store.AppConnection) string {
	if AuthKind(a.Auth) == AuthOAuth2 {
		return StatusExpired
	}
	return StatusError
}

func (s *Service) authMessage(a store.AppConnection, err error) string {
	switch {
	case errors.Is(err, errRelogin), errors.Is(err, errUnauthorized) && AuthKind(a.Auth) == AuthOAuth2:
		return "「" + a.Title + "」的授權已過期，請使用者到「連接應用程式」重新連接"
	case errors.Is(err, errUnauthorized):
		return "「" + a.Title + "」拒絕了金鑰，請使用者到「連接應用程式」更新"
	}
	return err.Error()
}

// toolOff 回答一個工具是不是被關掉（你關的，或目錄預設不給的）。
func (s *Service) toolOff(a store.AppConnection, tool string) bool {
	if slices.Contains(a.DisabledTools, tool) {
		return true
	}
	if app, ok := Lookup(a.AppID); ok && slices.Contains(app.Exclude, tool) {
		return true
	}
	return false
}

// rewrite 改寫遠端的回覆：tools/list 拿掉關掉的工具並記下清單；tools/call 截斷太長的結果並記下最近使用。
func (s *Service) rewrite(ctx context.Context, a store.AppConnection, botID string, req rpcMessage, tool string, raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m["result"] == nil || string(m["id"]) != string(req.ID) {
		return raw
	}
	switch req.Method {
	case "tools/list":
		var res map[string]json.RawMessage
		if json.Unmarshal(m["result"], &res) != nil {
			return raw
		}
		var tools []map[string]any
		if json.Unmarshal(res["tools"], &tools) != nil {
			return raw
		}
		kept := tools[:0]
		var seen []store.AppTool
		for _, t := range tools {
			name, _ := t["name"].(string)
			desc, _ := t["description"].(string)
			if len(seen) < limits.AppToolsMax {
				seen = append(seen, store.AppTool{Name: name, Description: truncate(desc, 300)})
			}
			if !s.toolOff(a, name) {
				kept = append(kept, t)
			}
		}
		if len(seen) > 0 && !sameTools(a.Tools, seen) {
			// 第一頁的工具清單記下來（I02 進階、I04 工具膠囊）。
			if cur, err := s.o.DB.GetAppConnection(context.WithoutCancel(ctx), a.ID); err == nil {
				cur.Tools = seen
				if cur.Status == StatusChecking || cur.Status == StatusError {
					cur.Status, cur.Error = StatusOK, ""
				}
				cur.CheckedAt = s.o.Now().UnixMilli()
				_, _ = s.o.DB.UpdateAppConnection(context.WithoutCancel(ctx), cur)
			}
		}
		b, _ := json.Marshal(kept)
		res["tools"] = b
		m["result"], _ = json.Marshal(res)
	case "tools/call":
		var res map[string]json.RawMessage
		if json.Unmarshal(m["result"], &res) != nil {
			return raw
		}
		isErr := string(res["isError"]) == "true"
		s.recordUse(ctx, a.ID, botID, tool, !isErr)
		var content []map[string]any
		if json.Unmarshal(res["content"], &content) != nil {
			return raw
		}
		if limitText(content, limits.AppResultChars) {
			res["content"], _ = json.Marshal(content)
			delete(res, "structuredContent") // 原本完整的結構化結果也一樣太大
			m["result"], _ = json.Marshal(res)
		}
	default:
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func sameTools(a, b []store.AppTool) bool {
	return slices.EqualFunc(a, b, func(x, y store.AppTool) bool { return x.Name == y.Name })
}

// limitText 把 content 裡的文字總長截到 limit 字；有截斷時回 true。
func limitText(content []map[string]any, limit int) bool {
	left := limit
	cut := false
	for _, c := range content {
		text, ok := c["text"].(string)
		if !ok {
			continue
		}
		n := utf8.RuneCountInString(text)
		if n <= left {
			left -= n
			continue
		}
		r := []rune(text)
		c["text"] = string(r[:max(left, 0)]) + "\n…（結果太長，只給前 25,000 字；需要其他部分請縮小查詢範圍）"
		left = 0
		cut = true
	}
	return cut
}

func (s *Service) recordUse(ctx context.Context, connectionID, botID, tool string, ok bool) {
	if tool == "" {
		return
	}
	if err := s.o.DB.AddAppUse(context.WithoutCancel(ctx), store.AppUse{ConnectionID: connectionID, BotID: botID, Tool: tool, OK: ok}, limits.AppUsesKept); err != nil {
		s.o.Logger.Warn("記錄應用程式使用失敗", "err", err)
	}
}

// rpcFail 回一個 JSON-RPC 錯誤（橋接會原樣交給 Bot）；通知（沒有 id）回 202。
func rpcFail(w http.ResponseWriter, id json.RawMessage, message string) {
	if len(id) == 0 || string(id) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": message}})
}
