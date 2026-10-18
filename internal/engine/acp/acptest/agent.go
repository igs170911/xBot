// Package acptest 是測試用的假 ACP agent：在同一個行程裡，經 pipe 講 ACP（換行分隔的 JSON-RPC 2.0），
// 照訊息內容演出固定的劇本（串流文字、工具呼叫、權限請求、等取消、當機）。
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// 劇本：session/prompt 的文字決定 agent 怎麼回。
const (
	// PromptHello：思考一段、分兩段回「Hello」，以 end_turn 結束並附用量。
	PromptHello = "hello"
	// PromptTool：計畫、一個成功的工具呼叫、一段文字。
	PromptTool = "tool"
	// PromptPermission：要求權限；允許就執行並回「allowed:<optionId>」，拒絕回「rejected」，被取消就以 cancelled 結束。
	PromptPermission = "permission"
	// PromptWait：送一段文字後等 session/cancel，再以 cancelled 結束。
	PromptWait = "wait"
	// PromptCrash：送一段文字後行程直接結束。
	PromptCrash = "crash"
	// PromptRefuse：以 refusal 結束。
	PromptRefuse = "refuse"
)

// Call 是 agent 收到的一個請求或通知。
type Call struct {
	Method string
	Params json.RawMessage
}

// Agent 是假的 ACP agent。
type Agent struct {
	// AuthRequired 時 session/new 回 auth_required（-32000），模擬還沒登入。
	AuthRequired bool
	// NoLoad 時不支援 session/load。
	NoLoad bool

	mu       sync.Mutex
	calls    []Call
	sessions map[string]bool
	cancels  map[string]chan struct{}
	waiting  map[string]chan json.RawMessage // 等 XBot 回覆的請求 id → 回覆
	nextID   int
	nextSess int
	launches int
}

// New 建立假的 agent。
func New() *Agent {
	return &Agent{sessions: map[string]bool{}, cancels: map[string]chan struct{}{}, waiting: map[string]chan json.RawMessage{}}
}

// Calls 回傳到目前為止收到的請求與通知。
func (a *Agent) Calls() []Call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Call(nil), a.calls...)
}

// Methods 回傳收到的方法名稱。
func (a *Agent) Methods() []string {
	var out []string
	for _, c := range a.Calls() {
		out = append(out, c.Method)
	}
	return out
}

// Launches 是開過幾次行程。
func (a *Agent) Launches() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.launches
}

// Process 是 XBot 這一端看到的行程。
type Process struct {
	r *io.PipeReader // agent 的 stdout
	w *io.PipeWriter // agent 的 stdin
}

func (p *Process) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *Process) Write(b []byte) (int, error) { return p.w.Write(b) }

// Close 關掉 stdin 與 stdout，agent 會結束。
func (p *Process) Close() error {
	_ = p.w.Close()
	return p.r.Close()
}

// Start 開一個新的 agent 行程（每次呼叫都是新的行程，session 記錄共用，模擬 session/load）。
func (a *Agent) Start() *Process {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	a.mu.Lock()
	a.launches++
	a.mu.Unlock()
	pr := &peer{a: a, out: outW}
	go pr.serve(inR)
	return &Process{r: outR, w: inW}
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// peer 是一個行程。
type peer struct {
	a   *Agent
	out *io.PipeWriter
	wmu sync.Mutex
}

func (p *peer) send(v any) {
	b, _ := json.Marshal(v)
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, _ = p.out.Write(append(b, '\n'))
}

func (p *peer) result(id json.RawMessage, v any) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
}

func (p *peer) fail(id json.RawMessage, code int, msg string) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (p *peer) update(session string, u map[string]any) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": u}})
}

func (p *peer) chunk(session, kind, text string) {
	p.update(session, map[string]any{"sessionUpdate": kind, "content": map[string]string{"type": "text", "text": text}})
}

func (p *peer) serve(in *io.PipeReader) {
	defer p.out.Close()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.Method == "" {
			// XBot 對 agent 請求的回覆。
			p.a.mu.Lock()
			ch := p.a.waiting[string(f.ID)]
			delete(p.a.waiting, string(f.ID))
			p.a.mu.Unlock()
			if ch != nil {
				if f.Error != nil {
					ch <- json.RawMessage(`{"error":true}`)
				} else {
					ch <- f.Result
				}
			}
			continue
		}
		p.a.mu.Lock()
		p.a.calls = append(p.a.calls, Call{Method: f.Method, Params: f.Params})
		p.a.mu.Unlock()
		if f.ID == nil {
			p.notification(f)
			continue
		}
		if f.Method == "session/prompt" {
			go p.prompt(f)
			continue
		}
		p.handle(f)
	}
}

func (p *peer) notification(f frame) {
	if f.Method != "session/cancel" {
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(f.Params, &params)
	p.a.mu.Lock()
	ch := p.a.cancels[params.SessionID]
	p.a.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (p *peer) handle(f frame) {
	a := p.a
	switch f.Method {
	case "initialize":
		p.result(f.ID, map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": !a.NoLoad},
			"agentInfo":         map[string]string{"name": "fake-agent", "version": "0"},
			"authMethods":       []any{},
		})
	case "session/new":
		if a.AuthRequired {
			p.fail(f.ID, -32000, "Authentication required")
			return
		}
		a.mu.Lock()
		a.nextSess++
		id := fmt.Sprintf("sess-%d", a.nextSess)
		a.sessions[id] = true
		a.mu.Unlock()
		p.result(f.ID, map[string]any{
			"sessionId": id,
			"modes": map[string]any{"currentModeId": "default", "availableModes": []map[string]string{
				{"id": "default", "name": "Manual"}, {"id": "acceptEdits", "name": "Accept edits"}, {"id": "auto", "name": "Auto"}, {"id": "bypassPermissions", "name": "Bypass"},
			}},
			"configOptions": []map[string]any{{"id": "model", "type": "select", "currentValue": "default",
				"options": []map[string]string{{"value": "default"}, {"value": "sonnet"}}}},
		})
	case "session/load":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(f.Params, &params)
		a.mu.Lock()
		ok := a.sessions[params.SessionID]
		a.mu.Unlock()
		if !ok {
			p.fail(f.ID, -32002, "Resource not found")
			return
		}
		// 重播舊的對話：不能變成這一輪的事件。
		p.chunk(params.SessionID, "user_message_chunk", "old question")
		p.chunk(params.SessionID, "agent_message_chunk", "old answer")
		p.result(f.ID, nil)
	case "session/set_mode", "session/set_config_option":
		p.result(f.ID, map[string]any{})
	default:
		p.fail(f.ID, -32601, "Method not found: "+f.Method)
	}
}

// ask 發一個請求給 XBot，等它回覆。
func (p *peer) ask(method string, params any) json.RawMessage {
	p.a.mu.Lock()
	p.a.nextID++
	id := fmt.Sprintf(`"perm-%d"`, p.a.nextID)
	ch := make(chan json.RawMessage, 1)
	p.a.waiting[id] = ch
	p.a.mu.Unlock()
	p.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	return <-ch
}

func (p *peer) prompt(f frame) {
	var params struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(f.Params, &params)
	sid := params.SessionID
	text := ""
	if len(params.Prompt) > 0 {
		text = strings.TrimSpace(params.Prompt[0].Text)
	}
	cancel := make(chan struct{})
	p.a.mu.Lock()
	p.a.cancels[sid] = cancel
	p.a.mu.Unlock()
	end := func(reason string) {
		p.result(f.ID, map[string]any{"stopReason": reason, "usage": map[string]int{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15}})
	}
	switch text {
	case PromptHello:
		p.chunk(sid, "agent_thought_chunk", "thinking…")
		p.chunk(sid, "agent_message_chunk", "Hel")
		p.chunk(sid, "agent_message_chunk", "lo")
		p.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 1200, "size": 200000, "cost": map[string]any{"amount": 0.01, "currency": "USD"}})
		end("end_turn")
	case PromptTool:
		p.update(sid, map[string]any{"sessionUpdate": "plan", "entries": []map[string]string{
			{"content": "列出檔案", "priority": "high", "status": "in_progress"}, {"content": "回報", "priority": "low", "status": "pending"}}})
		p.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "ls /workspace", "kind": "execute",
			"status": "pending", "rawInput": map[string]string{"command": "ls"}})
		p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress"})
		p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
			"rawOutput": map[string]string{"stdout": "a.txt"}})
		p.chunk(sid, "agent_message_chunk", "done")
		end("end_turn")
	case PromptPermission:
		tool := map[string]any{"toolCallId": "t2", "title": "rm -rf /workspace/tmp", "kind": "delete", "rawInput": map[string]string{"command": "rm -rf /workspace/tmp"}}
		p.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t2", "title": "rm -rf /workspace/tmp", "kind": "delete", "status": "pending"})
		raw := p.ask("session/request_permission", map[string]any{"sessionId": sid, "toolCall": tool, "options": []map[string]string{
			{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
			{"optionId": "always", "name": "Always allow", "kind": "allow_always"},
			{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		}})
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(raw, &res)
		switch {
		case res.Outcome.Outcome == "cancelled":
			<-cancel
			end("cancelled")
		case res.Outcome.OptionID == "reject":
			p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "status": "failed"})
			p.chunk(sid, "agent_message_chunk", "rejected")
			end("end_turn")
		default:
			p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "status": "completed"})
			p.chunk(sid, "agent_message_chunk", "allowed:"+res.Outcome.OptionID)
			end("end_turn")
		}
	case PromptWait:
		p.chunk(sid, "agent_message_chunk", "working")
		<-cancel
		end("cancelled")
	case PromptCrash:
		p.chunk(sid, "agent_message_chunk", "about to crash")
		_ = p.out.CloseWithError(io.ErrUnexpectedEOF)
	case PromptRefuse:
		end("refusal")
	default:
		p.chunk(sid, "agent_message_chunk", "echo:"+text)
		end("end_turn")
	}
}
