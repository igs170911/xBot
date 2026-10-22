package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"

	"xbot/internal/engine"
	"xbot/internal/limits"
)

// ACP（Agent Client Protocol，https://agentclientprotocol.com）的傳輸：agent 行程的 stdin／stdout 上，
// 每行一個 JSON-RPC 2.0 訊息。雙向：XBot 呼叫 initialize、session/new、session/prompt…；
// agent 送 session/update 通知，也會反過來發請求（session/request_permission）等 XBot 回覆。

// RPCError 是 agent 回覆的 JSON-RPC 錯誤。
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("agent 回覆錯誤（%d）", e.Code)
	}
	return e.Message
}

// JSON-RPC 錯誤碼。
const (
	codeMethodNotFound = -32601
	// codeAuthRequired 是 ACP 的「需要登入」（auth_required）。
	codeAuthRequired = -32000
)

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// request 是 agent 發給 XBot 的請求。
type request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type reply func(result json.RawMessage, err error)

// conn 是一條到 agent 行程的 JSON-RPC 連線。
type conn struct {
	p      Process
	proc   Process // D1: Exposed process for killProcessGroup
	log    *slog.Logger
	onNote func(method string, params json.RawMessage)
	onReq  func(request)
	onExit func(error)

	writeMu sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[string]reply
	closed  bool
	exitErr error
	done    chan struct{}
}

func newConn(p Process, log *slog.Logger, onNote func(string, json.RawMessage), onReq func(request), onExit func(error)) *conn {
	c := &conn{p: p, proc: p, log: log, onNote: onNote, onReq: onReq, onExit: onExit, pending: map[string]reply{}, done: make(chan struct{})}
	go c.read()
	return c
}

func (c *conn) alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

func (c *conn) write(f frame) error {
	f.JSONRPC = "2.0"
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !c.alive() {
		return engine.ErrDisconnected
	}
	if _, err := c.p.Write(b); err != nil {
		c.shutdown(fmt.Errorf("寫入 agent 失敗：%w", err))
		return engine.ErrDisconnected
	}
	return nil
}

// start 送出一個請求，回覆時呼叫 done（只呼叫一次；連線中斷時帶 engine.ErrDisconnected）。
func (c *conn) start(method string, params any, done reply) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", engine.ErrDisconnected
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	c.pending[id] = done
	c.mu.Unlock()
	if err := c.write(frame{ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return "", err
	}
	return id, nil
}

// call 送出請求並等回覆。
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	type res struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan res, 1)
	id, err := c.start(method, params, func(raw json.RawMessage, err error) { ch <- res{raw, err} })
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s 沒有回應：%w", method, ctx.Err())
	}
}

func (c *conn) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(frame{Method: method, Params: raw})
}

// answer 回覆 agent 的請求。
func (c *conn) answer(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.write(frame{ID: id, Result: raw})
}

// refuse 用 JSON-RPC 錯誤回覆 agent 的請求。
func (c *conn) refuse(id json.RawMessage, code int, msg string) error {
	return c.write(frame{ID: id, Error: &RPCError{Code: code, Message: msg}})
}

func (c *conn) read() {
	r := bufio.NewReaderSize(c.p, 64<<10)
	var err error
	for {
		var line []byte
		line, err = readLine(r, limits.ACPMaxLineBytes)
		if err != nil {
			break
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var f frame
		if json.Unmarshal(line, &f) != nil {
			// adapter 偶爾把紀錄寫到 stdout；不是 JSON 的行略過。
			c.log.Debug("略過 agent 輸出的非 JSON 行", "line", truncate(string(line), 200))
			continue
		}
		switch {
		case f.Method != "" && len(f.ID) > 0:
			c.onReq(request{ID: f.ID, Method: f.Method, Params: f.Params})
		case f.Method != "":
			c.onNote(f.Method, f.Params)
		case len(f.ID) > 0:
			c.mu.Lock()
			key := idKey(f.ID)
			done := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if done == nil {
				continue
			}
			if f.Error != nil {
				done(nil, f.Error)
			} else {
				done(f.Result, nil)
			}
		}
	}
	if errors.Is(err, io.EOF) {
		err = errors.New("agent 行程已結束")
	}
	c.shutdown(err)
}

// readLine 讀一行（不含換行），超過 max 時回錯誤。
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, fmt.Errorf("agent 輸出的一行超過 %d bytes", max)
		}
		if err == nil {
			return buf, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return buf, nil
			}
			return nil, err
		}
	}
}

func idKey(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(id))
}

// close 結束連線（關掉 agent 行程）。
func (c *conn) close() { c.shutdown(errors.New("連線已關閉")) }

func (c *conn) shutdown(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.exitErr = cause
	pending := c.pending
	c.pending = map[string]reply{}
	c.mu.Unlock()
	_ = c.p.Close()
	for _, done := range pending {
		done(nil, engine.ErrDisconnected)
	}
	close(c.done)
	if c.onExit != nil {
		c.onExit(cause)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
