// Package hermestest 是假的 Hermes TUI gateway（hermes_host.py 的 /rpc），給驅動與 runtime 的單元測試用。
// 協定照 tui_gateway/ws.py：換行分隔的 JSON-RPC、先送 gateway.ready、事件是 method=event。
package hermestest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Call 是 gateway 收到的一個呼叫。
type Call struct {
	Method string
	Params map[string]any
}

// Gateway 是假的 Hermes gateway。
type Gateway struct {
	Server *httptest.Server
	Key    string
	// OnPrompt 在每次 prompt.submit 之後於背景執行；nil 時用 ReplyOK。
	OnPrompt func(t *Turn)
	// Settle 模擬 Hermes 在一輪結束（OnPrompt 回傳）之後的收尾時間：這段期間 session 還算在跑，
	// 收完才送 session.info（running=false）。和真的 Hermes 一樣，跑的時候送來的 prompt.submit
	// 帶 queued=true 就排隊、收完尾自己開下一輪；沒帶就當成插話（steered），收下卻不會開新的一輪。
	Settle time.Duration
	// IgnoreQueued 模擬不認得 queued 參數的 Hermes：跑的時候送來的 prompt 一律當成插話。
	IgnoreQueued bool

	mu       sync.Mutex
	calls    []Call
	conns    []*websocket.Conn
	sessions int
	stored   map[string]string   // stored id → live session id
	turns    map[string]*Turn    // session → 進行中的一輪
	running  map[string]bool     // session → Hermes 那邊還在跑（含收尾）
	queued   map[string][]string // session → 排隊的 prompt
	steered  []string
	answers  map[string]chan json.RawMessage
	reqSeq   int
}

// New 啟動假的 gateway；key 是要求的 worker-key。
func New(key string) *Gateway {
	g := &Gateway{Key: key, stored: map[string]string{}, turns: map[string]*Turn{}, answers: map[string]chan json.RawMessage{},
		running: map[string]bool{}, queued: map[string][]string{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

// Addr 回傳 host:port。
func (g *Gateway) Addr() string { return strings.TrimPrefix(g.Server.URL, "http://") }

// Close 關閉 gateway。
func (g *Gateway) Close() {
	g.DropConnections()
	g.Server.Close()
}

// DropConnections 切斷所有連線（模擬容器重開）。
func (g *Gateway) DropConnections() {
	g.mu.Lock()
	conns := g.conns
	g.conns = nil
	g.mu.Unlock()
	for _, c := range conns {
		_ = c.CloseNow()
	}
}

// Calls 回傳收到的呼叫（不含 client.capabilities 以外的心跳）。
func (g *Gateway) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Call(nil), g.calls...)
}

// Steered 回傳被當成插話收下（不會開新的一輪）的 prompt。
func (g *Gateway) Steered() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.steered...)
}

// Methods 回傳收到的方法名稱。
func (g *Gateway) Methods() []string {
	var out []string
	for _, c := range g.Calls() {
		out = append(out, c.Method)
	}
	return out
}

func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/rpc" {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+g.Key {
		_ = ws.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}
	ws.SetReadLimit(16 << 20)
	g.mu.Lock()
	g.conns = append(g.conns, ws)
	g.mu.Unlock()
	c := &wconn{ws: ws}
	ctx := r.Context()
	_ = c.write(ctx, event("gateway.ready", "", map[string]any{"skin": map[string]any{}, "change_events": true, "replay_epoch": "e1", "heartbeat": true}))
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var f struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		if f.Method == "" {
			// 用戶端對伺服器請求的回覆。
			var id string
			_ = json.Unmarshal(f.ID, &id)
			g.mu.Lock()
			ch := g.answers[id]
			delete(g.answers, id)
			g.mu.Unlock()
			if ch != nil {
				if len(f.Error) > 0 {
					ch <- json.RawMessage(`{"error":` + string(f.Error) + `}`)
				} else {
					ch <- f.Result
				}
			}
			continue
		}
		g.mu.Lock()
		g.calls = append(g.calls, Call{Method: f.Method, Params: f.Params})
		g.mu.Unlock()
		result, rpcErr := g.handle(c, f.Method, f.Params)
		resp := map[string]any{"jsonrpc": "2.0", "id": f.ID}
		if rpcErr != "" {
			resp["error"] = map[string]any{"code": 4000, "message": rpcErr}
		} else {
			resp["result"] = result
		}
		_ = c.write(ctx, resp)
	}
}

func (g *Gateway) handle(c *wconn, method string, p map[string]any) (any, string) {
	sid, _ := p["session_id"].(string)
	switch method {
	case "client.capabilities":
		return map[string]any{"server_requests": []string{"clarify", "approval"}}, ""
	case "session.create":
		g.mu.Lock()
		g.sessions++
		id := fmt.Sprintf("live-%d", g.sessions)
		g.stored["stored-"+id] = id
		g.mu.Unlock()
		return map[string]any{"session_id": id, "stored_session_id": "stored-" + id, "message_count": 0, "messages": []any{}, "info": map[string]any{}}, ""
	case "session.resume":
		g.mu.Lock()
		defer g.mu.Unlock()
		if _, ok := g.stored[sid]; !ok {
			return nil, "session not found: " + sid
		}
		g.sessions++
		id := fmt.Sprintf("live-%d", g.sessions)
		g.stored[sid] = id
		return map[string]any{"session_id": id}, ""
	case "prompt.submit":
		text, _ := p["text"].(string)
		queued, _ := p["queued"].(bool)
		g.mu.Lock()
		if g.running[sid] {
			defer g.mu.Unlock()
			if queued && !g.IgnoreQueued {
				g.queued[sid] = append(g.queued[sid], text)
				return map[string]any{"status": "queued"}, ""
			}
			g.steered = append(g.steered, text)
			return map[string]any{"status": "steered"}, ""
		}
		g.running[sid] = true
		g.mu.Unlock()
		g.startTurn(c, sid, text)
		return map[string]any{"status": "streaming"}, ""
	case "config.set":
		return map[string]any{"key": p["key"], "value": p["value"]}, ""
	case "session.interrupt":
		g.mu.Lock()
		t := g.turns[sid]
		g.mu.Unlock()
		if t == nil {
			return map[string]any{"status": "not_interrupted"}, ""
		}
		t.once.Do(func() { close(t.interrupted) })
		return map[string]any{"status": "interrupted", "interrupted": true}, ""
	}
	return nil, "unknown method " + method
}

// startTurn 在背景跑一輪；結束後照 Hermes 的順序收尾：等 Settle、放開 running、送 session.info，再接排隊的 prompt。
func (g *Gateway) startTurn(c *wconn, sid, text string) {
	t := &Turn{g: g, c: c, SessionID: sid, Text: text, interrupted: make(chan struct{})}
	g.mu.Lock()
	g.turns[sid] = t
	g.mu.Unlock()
	fn := g.OnPrompt
	if fn == nil {
		fn = ReplyOK
	}
	go func() {
		fn(t)
		time.Sleep(g.Settle)
		g.mu.Lock()
		g.running[sid] = false
		g.mu.Unlock()
		t.Event("session.info", map[string]any{"running": false})
		g.mu.Lock()
		var next string
		q := g.queued[sid]
		drain := len(q) > 0 && !g.running[sid]
		if drain {
			next, g.queued[sid] = q[0], q[1:]
			g.running[sid] = true
		}
		g.mu.Unlock()
		if drain {
			g.startTurn(c, sid, next)
		}
	}()
}

// Turn 是假 gateway 上進行中的一輪，測試用它送事件。
type Turn struct {
	g           *Gateway
	c           *wconn
	SessionID   string
	Text        string
	interrupted chan struct{}
	once        sync.Once
}

// Event 送一個事件。
func (t *Turn) Event(typ string, payload any) {
	_ = t.c.write(context.Background(), event(typ, t.SessionID, payload))
}

// Batch 把多個事件放在同一個 WebSocket 訊息裡（換行分隔，同 Hermes 的 token 合併）。
func (t *Turn) Batch(evs ...[2]any) {
	var lines []string
	for _, e := range evs {
		b, _ := json.Marshal(event(e[0].(string), t.SessionID, e[1]))
		lines = append(lines, string(b))
	}
	_ = t.c.writeRaw(context.Background(), []byte(strings.Join(lines, "\n")+"\n"))
}

// Interrupted 在用戶端呼叫 session.interrupt 時關閉。
func (t *Turn) Interrupted() <-chan struct{} { return t.interrupted }

// Request 送一個伺服器請求（例：approval）並等用戶端回覆。
func (t *Turn) Request(method string, params map[string]any) json.RawMessage {
	t.g.mu.Lock()
	t.g.reqSeq++
	id := fmt.Sprintf("srq-%d", t.g.reqSeq)
	ch := make(chan json.RawMessage, 1)
	t.g.answers[id] = ch
	t.g.mu.Unlock()
	params["session_id"] = t.SessionID
	_ = t.c.write(context.Background(), map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		return nil
	}
}

// Complete 結束這一輪。
func (t *Turn) Complete(text, status string, extra map[string]any) {
	p := map[string]any{"text": text, "status": status}
	for k, v := range extra {
		p[k] = v
	}
	t.Event("message.complete", p)
}

// ReplyOK 是預設的回覆：串流「OK」並附用量。
func ReplyOK(t *Turn) {
	t.Event("message.start", nil)
	t.Batch([2]any{"message.delta", map[string]any{"text": "O"}}, [2]any{"message.delta", map[string]any{"text": "K"}})
	t.Complete("OK", "complete", map[string]any{"usage": map[string]any{"model": "fake", "input": 12, "output": 2, "calls": 1}})
}

func event(typ, sid string, payload any) map[string]any {
	params := map[string]any{"type": typ}
	if sid != "" {
		params["session_id"] = sid
	}
	if payload != nil {
		params["payload"] = payload
	}
	return map[string]any{"jsonrpc": "2.0", "method": "event", "params": params}
}

type wconn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (c *wconn) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeRaw(ctx, b)
}

func (c *wconn) writeRaw(ctx context.Context, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, b)
}
