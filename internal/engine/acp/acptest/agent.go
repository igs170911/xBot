// Package acptest 是測試用的假 ACP agent：在同一個行程裡，經 pipe 講 ACP（換行分隔的 JSON-RPC 2.0），
// 照訊息內容演出固定的劇本（串流文字、工具呼叫、權限請求、等取消、當機）。
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
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
	// PromptQuota：回一段文字後，照 Profile 的格式回額度用完的錯誤（EN-7）。
	PromptQuota = "quota"
	// PromptNarrate：先說一句「我先看一下」、用一個工具、再寫答案（最後的回答只該是答案）。
	PromptNarrate = "narrate"
	// PromptAnswerFirst：先寫完一大段答案、再用工具存檔、最後說「已存檔」（答案不能被丟掉）。
	PromptAnswerFirst = "answer-first"
	// PromptNarrateMany：做了很多步，每用一個工具前都說一句（加起來超過 limits.InterimNarrationMax），最後寫答案。
	PromptNarrateMany = "narrate-many"
	// PromptBackground：照 Grok 的樣子把工作丟到背景（_x.ai/session/update 的 workflow_updated），說「研究開跑了」就結束
	// session/prompt。測試呼叫 Agent.Finish 後背景工作做完，agent 自己開一輪回報：讀一個網頁（fetch，要權限）、
	// 改 /workspace 的報告（edit，要權限），說「報告好了」，以 turn_completed 結束。
	PromptBackground = "background"
	// PromptLate：回「done」結束這一輪之後，又送一段文字（不能混進下一輪）。
	PromptLate = "late"
	// PromptBgSubagent：開一個背景子代理（不在 workflow 裡）就結束這一輪；Finish 後子代理做完、叫醒 agent 回報「價格查好了」。
	PromptBgSubagent = "bg-subagent"
	// PromptDenyCancels：照 Grok 的樣子要求跑指令；被拒絕就以 cancelled 結束整輪（等人下指令），允許就回「ran」。
	PromptDenyCancels = "deny-cancels"
	// PromptSteer：說「我先看一下」、開始跑一個工具，等 XBot 插話（_session/steering，最多 5 秒）；工具做完後回
	// 「收到補充：<插話的內容>」（沒等到就回「沒有補充」）。
	PromptSteer = "steer"
)

// AnswerFirstText 是 PromptAnswerFirst 先寫的那一大段答案。
var AnswerFirstText = strings.Repeat("這是完整的答案內容。", 30)

// Profile 是一種引擎的假 agent 長相：session/new 回的模式、額度用完時的錯誤格式（照各 CLI 實際的寫法）。
type Profile struct {
	// Modes 是 session/new 回的 availableModes；空的時不回 modes（例：OpenCode、Grok）。
	Modes       []string
	CurrentMode string
	// QuotaCode、QuotaMessage、QuotaData 是 session/prompt 遇到額度用完時的 JSON-RPC 錯誤。
	QuotaCode    int
	QuotaMessage string
	QuotaData    any
	// RateLimitResetsAt 不是 0 時，額度錯誤之前先送一個帶 _claude/rateLimit 的 usage_update（Claude Code）。
	RateLimitResetsAt int64
	// Efforts 是 session/new 回的思考強度選項（reasoning_effort，目前是 high）；空的時沒有這個選項。
	Efforts []string
}

// Profiles 是各引擎的假 agent（key 是 bot.engine）。
var Profiles = map[string]Profile{
	"claude-code": {Modes: []string{"default", "acceptEdits", "auto", "bypassPermissions"}, CurrentMode: "default",
		QuotaCode: -32603, QuotaMessage: "You've hit your limit", QuotaData: map[string]string{"errorKind": "rate_limit"}, RateLimitResetsAt: 4102444800},
	"codex": {Modes: []string{"read-only", "workspace-write", "agent", "agent-full-access"}, CurrentMode: "read-only",
		QuotaCode: -32603, QuotaMessage: "Internal error",
		QuotaData: map[string]string{"message": "You've hit your usage limit. Try again in 2 hours 5 minutes.", "codexErrorInfo": "usageLimitExceeded"}},
	"gemini": {Modes: []string{"default", "autoEdit", "yolo", "plan"}, CurrentMode: "default",
		QuotaCode: -32603, QuotaMessage: `[API Error: You have exhausted your daily quota on this model.] {"status":"RESOURCE_EXHAUSTED","retryDelay":"3600s"}`},
	"opencode": {QuotaCode: -32603, QuotaMessage: "Insufficient account funds"},
	"grok":     {QuotaCode: -32603, QuotaMessage: "Rate limit exceeded. Please retry after 30m", Efforts: []string{"xhigh", "high", "medium", "low"}},
}

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
	// CanClose 時支援 session/close（sessionCapabilities.close）：收掉的 session 還能用 session/load 接回。
	CanClose bool
	// Profile 是這個假 agent 扮演的引擎（New 是 Claude Code）。
	Profile Profile
	// Steering 時在 initialize 宣告支援插話（_meta.steering.supported，Claude Code、Codex）。沒有可以插話的一輪時回
	// promptRequired；StartsNewTurn 時改成像 Codex 一樣回 startedNewTurn（自己開一輪）。
	Steering      bool
	StartsNewTurn bool

	mu       sync.Mutex
	calls    []Call
	sessions map[string]bool
	open     map[string]bool // 開著（還沒 session/close）的 session
	cancels  map[string]chan struct{}
	waiting  map[string]chan json.RawMessage // 等 XBot 回覆的請求 id → 回覆
	nextID   int
	nextSess int
	launches int
	finish   chan struct{}
	answers  []json.RawMessage
	steerIn  map[string]chan string // 正在等插話的 session（PromptSteer）
	steers   []string               // 收到的插話（依序）
}

// Steers 是收到的插話內容，依序。
func (a *Agent) Steers() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.steers...)
}

func (a *Agent) capabilities() map[string]any {
	c := map[string]any{"loadSession": !a.NoLoad}
	if a.CanClose {
		c["sessionCapabilities"] = map[string]any{"close": map[string]any{}}
	}
	return c
}

// Open 是現在開著的 session（開過、還沒 session/close；行程重開後歸零）。
func (a *Agent) Open() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for id := range a.open {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Finish 讓 PromptBackground 的背景工作做完。
func (a *Agent) Finish() { close(a.finish) }

// Answers 是 XBot 對 agent 的請求（權限請求）的回覆，依序。
func (a *Agent) Answers() []json.RawMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]json.RawMessage(nil), a.answers...)
}

// New 建立假的 agent（扮演 Claude Code）。
func New() *Agent { return NewFor("claude-code") }

// NewFor 建立扮演某個引擎的假 agent（Profiles）。
func NewFor(engine string) *Agent {
	return &Agent{Profile: Profiles[engine], sessions: map[string]bool{}, open: map[string]bool{}, cancels: map[string]chan struct{}{}, waiting: map[string]chan json.RawMessage{},
		finish: make(chan struct{}), steerIn: map[string]chan string{}}
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
	a.open = map[string]bool{} // 新的行程：之前開著的 session 跟著舊行程結束了
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

// xai 送 Grok 的擴充通知（實際的方法名稱是 _x.ai/session_notification；Grok 自己的紀錄檔寫成 _x.ai/session/update）。
func (p *peer) xai(session string, u map[string]any) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/session_notification", "params": map[string]any{"sessionId": session, "update": u}})
}

// permission 發一個權限請求，回傳選到的 optionId（取消時是 "cancelled"）。
func (p *peer) permission(session string, tool map[string]any) string {
	raw := p.ask("session/request_permission", map[string]any{"sessionId": session, "toolCall": tool, "options": []map[string]string{
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
	if res.Outcome.Outcome == "cancelled" {
		return "cancelled"
	}
	return res.Outcome.OptionID
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
			p.a.answers = append(p.a.answers, f.Result)
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
		res := map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": a.capabilities(),
			"agentInfo":         map[string]string{"name": "fake-agent", "version": "0"},
			"authMethods":       []any{},
		}
		if a.Steering {
			res["_meta"] = map[string]any{"steering": map[string]bool{"supported": true}}
		}
		p.result(f.ID, res)
	case "_session/steering":
		var params struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(f.Params, &params)
		text := ""
		for _, b := range params.Prompt {
			text += b.Text
		}
		a.mu.Lock()
		ch := a.steerIn[params.SessionID]
		a.steers = append(a.steers, text)
		a.mu.Unlock()
		switch {
		case ch != nil:
			ch <- text
			p.result(f.ID, map[string]string{"outcome": "injected"})
		case a.StartsNewTurn:
			p.result(f.ID, map[string]string{"outcome": "startedNewTurn"})
		default:
			p.result(f.ID, map[string]string{"outcome": "promptRequired"})
		}
	case "session/new":
		if a.AuthRequired {
			p.fail(f.ID, -32000, "Authentication required")
			return
		}
		a.mu.Lock()
		a.nextSess++
		id := fmt.Sprintf("sess-%d", a.nextSess)
		a.sessions[id] = true
		a.open[id] = true
		a.mu.Unlock()
		opts := []map[string]any{{"id": "model", "type": "select", "currentValue": "default",
			"options": []map[string]string{{"value": "default"}, {"value": "sonnet"}}}}
		if len(a.Profile.Efforts) > 0 {
			ev := []map[string]string{}
			for _, e := range a.Profile.Efforts {
				ev = append(ev, map[string]string{"value": e})
			}
			opts = append(opts, map[string]any{"id": "reasoning_effort", "category": "thought_level", "type": "select", "currentValue": "high", "options": ev})
		}
		res := map[string]any{"sessionId": id, "configOptions": opts}
		if len(a.Profile.Modes) > 0 {
			modes := []map[string]string{}
			for _, m := range a.Profile.Modes {
				modes = append(modes, map[string]string{"id": m, "name": m})
			}
			res["modes"] = map[string]any{"currentModeId": a.Profile.CurrentMode, "availableModes": modes}
		}
		p.result(f.ID, res)
	case "session/load":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(f.Params, &params)
		a.mu.Lock()
		ok := a.sessions[params.SessionID]
		if ok {
			a.open[params.SessionID] = true
		}
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
	case "session/close":
		if !a.CanClose {
			p.fail(f.ID, -32601, "Method not found: "+f.Method)
			return
		}
		var params struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(f.Params, &params)
		a.mu.Lock()
		delete(a.open, params.SessionID)
		a.mu.Unlock()
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
	if n := len(params.Prompt); n > 0 {
		// 劇本看最後一段：前面可能是 XBot 在 session 第一則訊息前加的身分與守則
		text = strings.TrimSpace(params.Prompt[n-1].Text)
	}
	cancel := make(chan struct{})
	p.a.mu.Lock()
	p.a.cancels[sid] = cancel
	p.a.mu.Unlock()
	end := func(reason string) {
		p.result(f.ID, map[string]any{"stopReason": reason, "usage": map[string]int{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15}})
	}
	switch text {
	case PromptSteer:
		in := make(chan string, 1)
		p.a.mu.Lock()
		p.a.steerIn[sid] = in
		p.a.mu.Unlock()
		p.chunk(sid, "agent_message_chunk", "我先看一下。")
		p.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "s1", "title": "sleep 1", "kind": "execute", "status": "in_progress"})
		got := "沒有補充"
		select {
		case t := <-in:
			got = "收到補充：" + t
		case <-cancel:
		case <-time.After(5 * time.Second):
		}
		p.a.mu.Lock()
		delete(p.a.steerIn, sid)
		p.a.mu.Unlock()
		p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "s1", "status": "completed"})
		p.chunk(sid, "agent_message_chunk", got)
		end("end_turn")
	case PromptNarrate, PromptAnswerFirst:
		first, last := "我先看一下檔案。", "答案是 42。"
		if text == PromptAnswerFirst {
			first, last = AnswerFirstText, "已存檔。"
		}
		p.chunk(sid, "agent_message_chunk", first)
		p.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "n1", "title": "cat a.txt", "kind": "read", "status": "pending"})
		p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "n1", "status": "completed"})
		p.chunk(sid, "agent_message_chunk", last)
		end("end_turn")
	case PromptNarrateMany:
		for i := range 15 {
			p.chunk(sid, "agent_message_chunk", fmt.Sprintf("接著調整第 %d 個區塊的排版，再截圖確認一次。", i+1))
			id := fmt.Sprintf("m%d", i)
			p.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": "screenshot", "kind": "execute", "status": "pending"})
			p.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": "completed"})
		}
		p.chunk(sid, "agent_message_chunk", "做好了，在 /workspace/a.html。")
		end("end_turn")
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
	case PromptBackground:
		wf := map[string]any{"sessionUpdate": "workflow_updated", "run_id": "wf1", "name": "deep-research", "status": "active", "foreground": false, "current_phase": "Plan"}
		p.xai(sid, wf)
		p.chunk(sid, "agent_message_chunk", "研究開跑了")
		p.xai(sid, map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p1", "stop_reason": "end_turn"})
		end("end_turn")
		// workflow 裡的子代理：它自己的 session 每一輪結束也送 turn_completed（不能當成 agent 回報完了）。
		p.xai(sid, map[string]any{"sessionUpdate": "subagent_spawned", "subagent_id": "sub1", "parent_session_id": sid, "child_session_id": "child1",
			"workflow_run_id": "wf1"})
		// 這一輪結束後 Grok 才送的標題（不是開始回報）。
		p.update(sid, map[string]any{"sessionUpdate": "session_info_update", "title": "研究"})
		go func() {
			<-p.a.finish
			p.xai(sid, map[string]any{"sessionUpdate": "subagent_finished", "subagent_id": "sub1", "child_session_id": "child1", "status": "completed"})
			wf["status"], wf["current_phase"] = "complete", "Report"
			p.xai(sid, wf)
			p.update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]string{"type": "text", "text": "A background workflow stopped."},
				"_meta": map[string]any{"syntheticPrompt": true}})
			// 回報的這一輪裡開一個子代理：它自己的 session 一輪結束也送 turn_completed，不能當成 agent 回報完了。
			p.xai(sid, map[string]any{"sessionUpdate": "subagent_spawned", "subagent_id": "sub2", "parent_session_id": sid, "child_session_id": "child2"})
			p.xai("child2", map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "c2", "stop_reason": "end_turn"})
			p.xai(sid, map[string]any{"sessionUpdate": "subagent_finished", "subagent_id": "sub2", "child_session_id": "child2", "status": "completed"})
			time.Sleep(20 * time.Millisecond)
			fetch := p.permission(sid, map[string]any{"toolCallId": "f1", "title": "Fetch: https://example.com", "kind": "fetch", "rawInput": map[string]string{"url": "https://example.com"}})
			edit := p.permission(sid, map[string]any{"toolCallId": "e1", "title": "Write /workspace/報告.md", "kind": "edit", "rawInput": map[string]string{"file_path": "/workspace/報告.md"}})
			p.chunk(sid, "agent_message_chunk", "報告好了："+fetch+","+edit)
			p.xai(sid, map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "workflow-completed-wf1", "stop_reason": "end_turn",
				"usage": map[string]int{"inputTokens": 100, "outputTokens": 20}})
		}()
	case PromptBgSubagent:
		p.xai(sid, map[string]any{"sessionUpdate": "subagent_spawned", "subagent_id": "s9", "parent_session_id": sid, "child_session_id": "c9", "description": "查價格"})
		p.chunk(sid, "agent_message_chunk", "我請子代理去查。")
		end("end_turn")
		go func() {
			<-p.a.finish
			p.xai(sid, map[string]any{"sessionUpdate": "subagent_finished", "subagent_id": "s9", "child_session_id": "c9", "status": "completed", "will_wake": true})
			p.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "價格查好了"}})
			p.xai(sid, map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "sub-done", "stop_reason": "end_turn"})
		}()
	case PromptDenyCancels:
		p.chunk(sid, "agent_message_chunk", "我先查一下。")
		switch p.permission(sid, map[string]any{"toolCallId": "x1", "title": "Execute `curl https://example.com`", "kind": "execute",
			"rawInput": map[string]string{"command": "curl https://example.com"}}) {
		case "reject":
			end("cancelled")
			return
		case "cancelled":
			<-cancel
			end("cancelled")
			return
		}
		p.chunk(sid, "agent_message_chunk", "ran")
		end("end_turn")
	case PromptLate:
		p.chunk(sid, "agent_message_chunk", "done")
		end("end_turn")
		p.chunk(sid, "agent_message_chunk", "late")
	case PromptWait:
		p.chunk(sid, "agent_message_chunk", "working")
		<-cancel
		end("cancelled")
	case PromptCrash:
		p.chunk(sid, "agent_message_chunk", "about to crash")
		_ = p.out.CloseWithError(io.ErrUnexpectedEOF)
	case PromptRefuse:
		end("refusal")
	case PromptQuota:
		pf := p.a.Profile
		p.chunk(sid, "agent_message_chunk", "working")
		if pf.RateLimitResetsAt != 0 {
			p.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 1000, "size": 200000,
				"_meta": map[string]any{"_claude/rateLimit": map[string]any{"status": "rejected", "resetsAt": pf.RateLimitResetsAt, "rateLimitType": "five_hour"}}})
		}
		e := map[string]any{"code": pf.QuotaCode, "message": pf.QuotaMessage}
		if pf.QuotaData != nil {
			e["data"] = pf.QuotaData
		}
		p.send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": e})
	default:
		p.chunk(sid, "agent_message_chunk", "echo:"+text)
		end("end_turn")
	}
}
