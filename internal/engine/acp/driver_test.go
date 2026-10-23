package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
	"xbot/internal/limits"
)

type harness struct {
	agent *acptest.Agent
	d     *acp.Driver

	mu      sync.Mutex
	allowed map[string]bool
	mcp     []acp.MCPServer
	models  []acp.ModelChoice
	current string
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{agent: acptest.New(), allowed: map[string]bool{"bot1": true}}
	h.mcp = []acp.MCPServer{{Name: "computer", Command: "/opt/hermes/.venv/bin/python", Args: []string{"/opt/xbot/desktop_mcp.py"}, Env: []acp.EnvVar{}}}
	h.d = acp.New(acp.Options{
		Agent: acp.Agents[acp.EngineClaude],
		Launch: func(_ context.Context, botID string, a acp.Agent) (acp.Process, error) {
			if a.Engine != acp.EngineClaude {
				t.Errorf("開的 agent 是 %s", a.Engine)
			}
			return h.agent.Start(), nil
		},
		Authorize: func(_ context.Context, botID, engine string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			if !h.allowed[botID] {
				return errors.New("這個 Bot 還沒連結你的 Claude 訂閱")
			}
			return nil
		},
		MCPServers: func(string) []acp.MCPServer { return h.mcp },
		OnModels: func(_ string, current string, models []acp.ModelChoice) {
			h.mu.Lock()
			h.current, h.models = current, models
			h.mu.Unlock()
		},
	})
	return h
}

func (h *harness) revoke(botID string) {
	h.mu.Lock()
	h.allowed[botID] = false
	h.mu.Unlock()
}

func start(t *testing.T, h *harness, thread string) engine.Session {
	t.Helper()
	s, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: thread, Model: "sonnet", Mode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// collect 讀事件直到 done 或 fatal error。
func collect(t *testing.T, ch <-chan engine.Event) []engine.Event {
	t.Helper()
	var out []engine.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
			if e.Type == engine.EventDone || e.Fatal {
				return out
			}
		case <-timeout:
			t.Fatalf("等不到 done，目前：%+v", out)
		}
	}
}

// until 讀事件直到遇到某一種。
func until(t *testing.T, ch <-chan engine.Event, typ engine.EventType) engine.Event {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("事件結束了，沒等到 %s", typ)
			}
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("等不到 %s", typ)
		}
	}
}

func types(evs []engine.Event) []engine.EventType {
	var out []engine.EventType
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func text(evs []engine.Event, typ engine.EventType) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Type == typ {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func TestStreamingPrompt(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptHello}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, h.d.Events(s))
	if got := text(evs, engine.EventText); got != "Hello" {
		t.Errorf("文字 = %q", got)
	}
	if got := text(evs, engine.EventThinking); got != "thinking…" {
		t.Errorf("思考 = %q", got)
	}
	last := evs[len(evs)-1]
	if last.Type != engine.EventDone || last.Status != engine.StatusComplete {
		t.Errorf("最後一個事件 = %+v", last)
	}
	var ctxUsage, tokens bool
	for _, e := range evs {
		if e.Type == engine.EventUsage && e.Usage.ContextMax == 200000 && e.Usage.ContextUsed == 1200 && e.Usage.CostUSD == 0.01 {
			ctxUsage = true
		}
		if e.Type == engine.EventUsage && e.Usage.InputTokens == 10 && e.Usage.OutputTokens == 5 {
			tokens = true
		}
		if e.Session != s.ID() {
			t.Errorf("事件的 session = %q", e.Session)
		}
	}
	if !ctxUsage || !tokens {
		t.Errorf("用量事件不完整：%+v", evs)
	}

	// initialize → session/new（cwd /workspace、desktop_mcp）→ 依權限等級設模式、選模型 → prompt。
	methods := h.agent.Methods()
	want := []string{"initialize", "session/new", "session/set_mode", "session/set_config_option", "session/prompt"}
	if !slices.Equal(methods, want) {
		t.Errorf("呼叫 = %v，想要 %v", methods, want)
	}
	calls := h.agent.Calls()
	var init struct {
		ProtocolVersion    int `json:"protocolVersion"`
		ClientCapabilities struct {
			Terminal bool `json:"terminal"`
		} `json:"clientCapabilities"`
	}
	_ = json.Unmarshal(calls[0].Params, &init)
	if init.ProtocolVersion != 1 || init.ClientCapabilities.Terminal {
		t.Errorf("initialize = %s", calls[0].Params)
	}
	var sn struct {
		Cwd        string          `json:"cwd"`
		MCPServers []acp.MCPServer `json:"mcpServers"`
	}
	_ = json.Unmarshal(calls[1].Params, &sn)
	if sn.Cwd != "/workspace" || len(sn.MCPServers) != 1 || sn.MCPServers[0].Name != "computer" || sn.MCPServers[0].Args[0] != "/opt/xbot/desktop_mcp.py" {
		t.Errorf("session/new = %s", calls[1].Params)
	}
	if !strings.Contains(string(calls[2].Params), `"modeId":"auto"`) {
		t.Errorf("full 應該對到 auto（SEC-2 不用 bypassPermissions）：%s", calls[2].Params)
	}
	if !strings.Contains(string(calls[3].Params), `"value":"sonnet"`) {
		t.Errorf("模型：%s", calls[3].Params)
	}

	// 同一個對話串再 Start 拿回同一個 session，不再開行程。
	again := start(t, h, "th1")
	if again != s || h.agent.Launches() != 1 {
		t.Errorf("同一個對話串應該共用 session（launches=%d）", h.agent.Launches())
	}
}

func TestToolCallsAndPlan(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptTool}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, h.d.Events(s))
	want := []engine.EventType{engine.EventStatus, engine.EventToolCall, engine.EventToolResult, engine.EventText, engine.EventUsage, engine.EventDone}
	if got := types(evs); !slices.Equal(got, want) {
		t.Fatalf("事件 = %v，想要 %v", got, want)
	}
	if !strings.Contains(evs[0].Text, "▶ 列出檔案") || !strings.Contains(evs[0].Text, "☐ 回報") {
		t.Errorf("計畫 = %q", evs[0].Text)
	}
	call, res := evs[1].Tool, evs[2].Tool
	if call.ID != "t1" || call.Name != "ls /workspace" || call.Context != "execute" || !strings.Contains(string(call.Args), "ls") {
		t.Errorf("tool_call = %+v", call)
	}
	if res.ID != "t1" || res.Name != "ls /workspace" || !res.OK || !strings.Contains(string(res.Result), "a.txt") {
		t.Errorf("tool_result = %+v", res)
	}
}

func TestPermissionAllowAndReject(t *testing.T) {
	for _, tc := range []struct {
		choice, want string
	}{{"once", "allowed:allow"}, {"always", "allowed:always"}, {"deny", "rejected"}} {
		t.Run(tc.choice, func(t *testing.T) {
			h := setup(t)
			s := start(t, h, "th1")
			if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptPermission}); err != nil {
				t.Fatal(err)
			}
			ch := h.d.Events(s)
			e := until(t, ch, engine.EventNeedsApproval)
			r := e.Request
			if r.Kind != "approval" || r.Prompt != "rm -rf /workspace/tmp" || !slices.Equal(r.Choices, []string{"once", "always", "deny"}) {
				t.Errorf("needs_approval = %+v", r)
			}
			if err := h.d.Respond(context.Background(), s, r.ID, engine.Answer{Choice: tc.choice}); err != nil {
				t.Fatal(err)
			}
			evs := collect(t, ch)
			if got := text(evs, engine.EventText); got != tc.want {
				t.Errorf("回覆 = %q，想要 %q", got, tc.want)
			}
			// 回覆過的請求不能再回一次。
			if err := h.d.Respond(context.Background(), s, r.ID, engine.Answer{Choice: "once"}); err == nil {
				t.Error("重複回覆應該失敗")
			}
		})
	}
}

func TestCancelWhileWaitingForPermission(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptPermission}); err != nil {
		t.Fatal(err)
	}
	ch := h.d.Events(s)
	r := until(t, ch, engine.EventNeedsApproval).Request
	if err := h.d.Interrupt(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if last := evs[len(evs)-1]; last.Type != engine.EventDone || last.Status != engine.StatusInterrupted {
		t.Errorf("取消後 = %+v", evs)
	}
	if err := h.d.Respond(context.Background(), s, r.ID, engine.Answer{Choice: "once"}); err == nil {
		t.Error("取消後的請求不能再回覆")
	}
	if !slices.Contains(h.agent.Methods(), "session/cancel") {
		t.Error("沒有送 session/cancel")
	}
}

func TestCancel(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptWait}); err != nil {
		t.Fatal(err)
	}
	ch := h.d.Events(s)
	until(t, ch, engine.EventText)
	if err := h.d.Send(context.Background(), s, engine.Message{Text: "again"}); !errors.Is(err, acp.ErrBusy) {
		t.Errorf("進行中再送 = %v", err)
	}
	if err := h.d.Interrupt(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if last := evs[len(evs)-1]; last.Status != engine.StatusInterrupted {
		t.Errorf("取消後 = %+v", last)
	}
	// 取消後同一個 session 可以繼續用。
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptHello}); err != nil {
		t.Fatal(err)
	}
	if got := text(collect(t, ch), engine.EventText); got != "Hello" {
		t.Errorf("取消後再送 = %q", got)
	}
	// 沒有進行中的一輪時取消，直接收到 interrupted。
	if err := h.d.Interrupt(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if e := until(t, ch, engine.EventDone); e.Status != engine.StatusInterrupted {
		t.Errorf("閒置時取消 = %+v", e)
	}
}

func TestRefusal(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	_ = h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptRefuse})
	evs := collect(t, h.d.Events(s))
	if last := evs[len(evs)-1]; last.Status != engine.StatusError || last.Text == "" {
		t.Errorf("拒絕 = %+v", last)
	}
}

func TestCrash(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: acptest.PromptCrash}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, h.d.Events(s))
	last := evs[len(evs)-1]
	if last.Type != engine.EventError || !last.Fatal {
		t.Fatalf("當機後 = %+v", evs)
	}
	// 事件串流關閉。
	if _, ok := <-h.d.Events(s); ok {
		t.Error("事件應該已關閉")
	}
	if err := h.d.Send(context.Background(), s, engine.Message{Text: "x"}); !errors.Is(err, engine.ErrDisconnected) {
		t.Errorf("斷線後送出 = %v", err)
	}
	// 再 Start 會重開行程，並用 session/load 接回原本的 session；重播的舊內容不會變成事件。
	s2, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th1", Resume: s.ResumeID()})
	if err != nil {
		t.Fatal(err)
	}
	if h.agent.Launches() != 2 || s2.ID() != s.ID() {
		t.Errorf("launches=%d session=%s", h.agent.Launches(), s2.ID())
	}
	if !slices.Contains(h.agent.Methods(), "session/load") {
		t.Error("沒有用 session/load 接回")
	}
	_ = h.d.Send(context.Background(), s2, engine.Message{Text: "hi"})
	if got := text(collect(t, h.d.Events(s2)), engine.EventText); got != "echo:hi" {
		t.Errorf("接回後 = %q（重播的內容不能混進來）", got)
	}
}

func TestResumeUnknownStartsNew(t *testing.T) {
	h := setup(t)
	s, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th1", Resume: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() == "gone" || !slices.Contains(h.agent.Methods(), "session/new") {
		t.Errorf("接不回時應該開新的：%s %v", s.ID(), h.agent.Methods())
	}
}

func TestAuthorizeRefuses(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	h.revoke("bot1")
	if err := h.d.Send(context.Background(), s, engine.Message{Text: "hi"}); err == nil || !strings.Contains(err.Error(), "訂閱") {
		t.Errorf("收回後送出 = %v", err)
	}
	if _, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th2"}); err == nil {
		t.Error("收回後不能開 session")
	}
	// Close 結束行程，session 收到連線中斷。
	h.d.Close("bot1")
	if e := until(t, h.d.Events(s), engine.EventError); !e.Fatal {
		t.Errorf("Close 後 = %+v", e)
	}
}

func TestAuthRequired(t *testing.T) {
	h := setup(t)
	h.agent.AuthRequired = true
	_, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Errorf("沒登入 = %v", err)
	}
}

func TestLaunchFailure(t *testing.T) {
	d := acp.New(acp.Options{Agent: acp.Agents[acp.EngineCodex], Launch: func(context.Context, string, acp.Agent) (acp.Process, error) {
		return nil, errors.New("電腦沒開")
	}})
	if d.Name() != acp.EngineCodex {
		t.Errorf("Name = %s", d.Name())
	}
	if _, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"}); err == nil {
		t.Error("開不了行程應該失敗")
	}
}

// 開 session 時把 agent 列出的模型回報出去（對話頁的模型選擇器用）。
func TestReportsModels(t *testing.T) {
	h := setup(t)
	start(t, h, "th1")
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != "default" || len(h.models) != 2 || h.models[0].Value != "default" || h.models[1].Value != "sonnet" {
		t.Fatalf("current=%q models=%+v", h.current, h.models)
	}
}

// 用工具前的一兩句「我先看一下」不算最後的回答（同 Hermes 的 interim message）；先寫完答案再存檔的，答案要留著。
func TestFinalAnswerSkipsShortNarration(t *testing.T) {
	h := setup(t)
	s := start(t, h, "th1")
	done := func(prompt string) engine.Event {
		if err := h.d.Send(context.Background(), s, engine.Message{Text: prompt}); err != nil {
			t.Fatal(err)
		}
		evs := collect(t, h.d.Events(s))
		return evs[len(evs)-1]
	}
	if e := done(acptest.PromptNarrate); e.Type != engine.EventDone || e.Text != "答案是 42。" {
		t.Fatalf("最後的回答只要用完工具後那段：%+v", e)
	}
	if e := done(acptest.PromptAnswerFirst); e.Type != engine.EventDone || e.Text != "" {
		t.Fatalf("前面是一大段答案就整段保留（Text 空白＝用串流的全部）：%q", e.Text)
	}
}

// turn 送一則訊息並讀到這一輪結束。
func turn(t *testing.T, h *harness, s engine.Session, text string) {
	t.Helper()
	if err := h.d.Send(context.Background(), s, engine.Message{Text: text}); err != nil {
		t.Fatal(err)
	}
	until(t, h.d.Events(s), engine.EventDone)
}

// eventually 等到 cond 成立（session/close 在背景送出）。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 每個 session 帶著自己的 MCP 子行程（2026-10-22 一台電腦累積 31～35 個、3 GB 用完）：開新對話串時，超過
// limits.ACPOpenSessionsMax 的就把最久沒用的閒置 session 收掉（session/close），之後用 session/load 接回。
func TestIdleSessionsAreClosedPastTheCap(t *testing.T) {
	h := setup(t)
	h.agent.CanClose = true
	var ss []engine.Session
	for i := range limits.ACPOpenSessionsMax {
		s := start(t, h, fmt.Sprintf("th%d", i+1))
		turn(t, h, s, "hi")
		ss = append(ss, s)
	}
	if got := len(h.agent.Open()); got != limits.ACPOpenSessionsMax {
		t.Fatalf("開著 %d 個", got)
	}
	// 用過第一個：最久沒用的變成第二個。
	turn(t, h, start(t, h, "th1"), "hi")
	start(t, h, "th-new")
	eventually(t, "第二個（最久沒用）要被收掉", func() bool {
		return len(h.agent.Open()) == limits.ACPOpenSessionsMax && !slices.Contains(h.agent.Open(), ss[1].ID())
	})
	if _, ok := <-h.d.Events(ss[1]); ok {
		t.Error("收掉的 session 事件要關閉")
	}
	if !slices.Contains(h.agent.Open(), ss[0].ID()) {
		t.Error("剛用過的不收")
	}
	// 收掉的對話串再用：session/load 接回同一個 session，agent 行程沒有重開。
	back, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th2", Resume: ss[1].ResumeID()})
	if err != nil {
		t.Fatal(err)
	}
	if back.ID() != ss[1].ID() || h.agent.Launches() != 1 {
		t.Fatalf("session=%s launches=%d", back.ID(), h.agent.Launches())
	}
	turn(t, h, back, "hi")
}

// 還在跑的、在等權限回覆的 session 不收，即使超過上限。
func TestBusySessionsAreNotClosed(t *testing.T) {
	h := setup(t)
	h.agent.CanClose = true
	busy := start(t, h, "busy")
	if err := h.d.Send(context.Background(), busy, engine.Message{Text: acptest.PromptPermission}); err != nil {
		t.Fatal(err)
	}
	until(t, h.d.Events(busy), engine.EventNeedsApproval)
	for i := range limits.ACPOpenSessionsMax + 1 {
		turn(t, h, start(t, h, fmt.Sprintf("th%d", i+1)), "hi")
	}
	time.Sleep(100 * time.Millisecond)
	if !slices.Contains(h.agent.Open(), busy.ID()) {
		t.Fatal("在等權限回覆的不能收")
	}
	if err := h.d.Interrupt(context.Background(), busy); err != nil {
		t.Fatal(err)
	}
}

// 不支援 session/close 的 agent：開著的對話串到上限而且全部閒置，就整個重開（MCP 子行程跟著結束），舊的用 session/load 接回。
func TestAgentWithoutCloseIsRestartedWhenCrowded(t *testing.T) {
	h := setup(t)
	var first engine.Session
	for i := range limits.ACPOpenSessionsMax {
		s := start(t, h, fmt.Sprintf("th%d", i+1))
		turn(t, h, s, "hi")
		if first == nil {
			first = s
		}
	}
	if h.agent.Launches() != 1 {
		t.Fatalf("launches=%d", h.agent.Launches())
	}
	turn(t, h, start(t, h, "th-new"), "hi")
	if h.agent.Launches() != 2 {
		t.Fatalf("要重開 agent：launches=%d", h.agent.Launches())
	}
	back, err := h.d.Start(context.Background(), engine.StartOptions{BotID: "bot1", ThreadID: "th1", Resume: first.ResumeID()})
	if err != nil {
		t.Fatal(err)
	}
	if back.ID() != first.ID() {
		t.Fatalf("接回 %s，要 %s", back.ID(), first.ID())
	}
	turn(t, h, back, "hi")
}
