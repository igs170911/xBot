package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"xbot/internal/engine"
	"xbot/internal/limits"
)

// Hermes TUI gateway 的通訊協定（容器內 hermes_host.py 的 /rpc，轉給 tui_gateway.ws.handle_ws）：
//   - WebSocket，每個訊息是一或多行 JSON-RPC 2.0（換行分隔）。連線時用 Authorization: Bearer <worker-key>。
//   - 連上後伺服器先送 {"method":"event","params":{"type":"gateway.ready",…}}；用戶端再呼叫 client.capabilities
//     {"server_requests":true}，表示會回覆伺服器發來的請求（clarify、approval 等）。
//   - 用戶端 → 伺服器：{"jsonrpc":"2.0","id":"1","method":…,"params":{…}}，回覆 {"id":"1","result":…} 或 {"id":"1","error":{code,message}}。
//   - 伺服器 → 用戶端事件：{"method":"event","params":{"type":…,"session_id":…,"payload":{…}}}。
//   - 伺服器 → 用戶端請求：{"id":…,"method":"clarify"|"approval"|…,"params":{…,"session_id":…}}，用戶端回 result 或 -32601。
// 這裡照 LazyBoy server/rpc.py（HermesRPC）實作。

// RPCError 是 Hermes 回覆的 JSON-RPC 錯誤。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Bot 操作失敗（%d）", e.Code)
	}
	return e.Message
}

type frame struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// eventParams 是事件的 params。
type eventParams struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Payload   json.RawMessage `json:"payload"`
}

// serverRequest 是 Hermes 發給用戶端的請求。
type serverRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type callResult struct {
	result json.RawMessage
	err    error
}

// conn 是一條到 Hermes gateway 的連線。一個 Bot 只開一條，所有對話串的 session 共用（同 LazyBoy）。
type conn struct {
	ws      *websocket.Conn
	log     *slog.Logger
	onEvent func(eventParams)
	onReq   func(serverRequest)
	onClose func(error)

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan callResult
	counter int
	closed  bool
	done    chan struct{}
	ready   chan struct{}
	cancel  context.CancelFunc
}

// dial 連上 ws://addr/rpc，等 gateway.ready，再宣告會回覆伺服器請求。
func dial(ctx context.Context, addr, key string, log *slog.Logger, onEvent func(eventParams), onReq func(serverRequest), onClose func(error)) (*conn, error) {
	log.Debug("hermes: dialing WebSocket", "addr", addr)
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	dctx, cancel := context.WithTimeout(ctx, limits.HermesReadyTimeout)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, "ws://"+addr+"/rpc", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		log.Error("hermes: WebSocket dial failed", "addr", addr, "error", err)
		return nil, fmt.Errorf("連不上 Bot 的 Hermes：%w", err)
	}
	log.Debug("hermes: WebSocket connected", "addr", addr)
	ws.SetReadLimit(limits.HermesMaxFrameBytes)
	life, stop := context.WithCancel(context.Background())
	c := &conn{ws: ws, log: log, onEvent: onEvent, onReq: onReq, onClose: onClose, pending: map[string]chan callResult{},
		done: make(chan struct{}), ready: make(chan struct{}), cancel: stop}
	go c.read(life)
	go c.heartbeat(life)
	log.Debug("hermes: waiting for gateway.ready")
	select {
	case <-c.ready:
		log.Debug("hermes: gateway ready")
	case <-c.done:
		log.Error("hermes: connection closed before ready")
		return nil, fmt.Errorf("連上 Hermes 後還沒就緒就斷線了：%w", engine.ErrDisconnected)
	case <-dctx.Done():
		log.Error("hermes: ready timeout", "error", dctx.Err())
		c.close()
		return nil, fmt.Errorf("等不到 Bot 的 Hermes 就緒：%w", dctx.Err())
	}
	if _, err := c.call(ctx, "client.capabilities", map[string]any{"server_requests": true}, limits.HermesReadyTimeout); err != nil {
		log.Error("hermes: client.capabilities failed", "error", err)
		c.close()
		return nil, err
	}
	log.Debug("hermes: RPC connection established")
	return c, nil
}

func (c *conn) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *conn) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !c.alive() {
		return engine.ErrDisconnected
	}
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("%w：%v", engine.ErrDisconnected, err)
	}
	return nil
}

// call 送出一個請求並等回覆。timeout 為 0 時用 limits.HermesCallTimeout。
func (c *conn) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = limits.HermesCallTimeout
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, engine.ErrDisconnected
	}
	c.counter++
	id := strconv.Itoa(c.counter)
	ch := make(chan callResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if params == nil {
		params = map[string]any{}
	}
	if err := c.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, fmt.Errorf("等不到 Hermes 回應 %s", method)
	}
}

// answer 回覆伺服器的請求。
func (c *conn) answer(ctx context.Context, id json.RawMessage, result any) error {
	return c.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// refuse 回覆伺服器的請求：這個用戶端不處理（JSON-RPC -32601）。
func (c *conn) refuse(ctx context.Context, id json.RawMessage, message string) error {
	return c.send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": message}})
}

func (c *conn) read(ctx context.Context) {
	var cause error
	defer func() { c.shutdown(cause) }()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			cause = err
			return
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			// 一個壞掉的訊息不能拖垮整條連線（同 LazyBoy _read）。
			var f frame
			if err := json.Unmarshal(line, &f); err != nil {
				c.log.Warn("Hermes 送來無法解析的訊息", "err", err)
				continue
			}
			c.dispatch(f)
		}
	}
}

func (c *conn) dispatch(f frame) {
	hasID := len(f.ID) > 0 && string(f.ID) != "null"
	switch {
	case f.Method != "" && hasID:
		c.onReq(serverRequest{ID: f.ID, Method: f.Method, Params: f.Params})
	case hasID:
		id := string(f.ID)
		var s string
		if json.Unmarshal(f.ID, &s) == nil {
			id = s
		}
		c.mu.Lock()
		ch := c.pending[id]
		c.mu.Unlock()
		if ch == nil {
			return
		}
		r := callResult{result: f.Result}
		if f.Error != nil {
			r = callResult{err: f.Error}
		}
		select {
		case ch <- r:
		default: // 重複的回覆
		}
	case f.Method == "event":
		var p eventParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return
		}
		if p.Type == "gateway.ready" {
			select {
			case <-c.ready:
			default:
				close(c.ready)
			}
		}
		c.onEvent(p)
	}
}

func (c *conn) heartbeat(ctx context.Context) {
	t := time.NewTicker(limits.HermesPingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, limits.HermesPingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil && ctx.Err() == nil {
				c.log.Warn("Hermes 心跳逾時，關閉連線", "err", err)
				c.close()
				return
			}
		}
	}
}

// shutdown 在讀取結束時執行一次：讓等待中的呼叫失敗、通知上層。
func (c *conn) shutdown(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[string]chan callResult{}
	c.mu.Unlock()
	close(c.done)
	c.cancel()
	for _, ch := range pending {
		select {
		case ch <- callResult{err: engine.ErrDisconnected}:
		default:
		}
	}
	_ = c.ws.CloseNow()
	if cause != nil && !errors.Is(cause, context.Canceled) {
		c.log.Info("Hermes 連線結束", "err", cause)
	}
	c.onClose(cause)
}

// close 主動關閉連線；讀取迴圈會收尾。
func (c *conn) close() {
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
	c.cancel()
	<-c.done
}
