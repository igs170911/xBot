package hermes_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
)

const key = "xbw_bot1.sig"

func setup(t *testing.T) (*hermestest.Gateway, *hermes.Driver, *int) {
	t.Helper()
	g := hermestest.New(key)
	t.Cleanup(g.Close)
	dials := 0
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		dials++
		return hermes.Endpoint{Addr: g.Addr(), Key: key}, nil
	}, nil)
	return g, d, &dials
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

func types(evs []engine.Event) []engine.EventType {
	var out []engine.EventType
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func TestStreamingReply(t *testing.T) {
	ctx := context.Background()
	g, d, dials := setup(t)
	s, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1", Title: "小幫手", Model: "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != "live-1" || s.ResumeID() != "stored-live-1" {
		t.Fatalf("session：%s %s", s.ID(), s.ResumeID())
	}
	// 同一條對話串再 Start 拿到同一個 session，不會重連。
	if s2, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"}); s2 != s || *dials != 1 {
		t.Fatalf("應該沿用 session（連線 %d 次）", *dials)
	}
	if err := d.Send(ctx, s, engine.Message{Text: "回覆 OK"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	want := []engine.EventType{engine.EventText, engine.EventText, engine.EventUsage, engine.EventDone}
	if !slices.Equal(types(evs), want) {
		t.Fatalf("事件：%+v", evs)
	}
	if evs[0].Text+evs[1].Text != "OK" || evs[3].Text != "OK" || evs[3].Status != engine.StatusComplete || evs[2].Usage.InputTokens != 12 {
		t.Fatalf("內容：%+v", evs)
	}
	calls := g.Calls()
	if !slices.Equal(g.Methods(), []string{"client.capabilities", "session.create", "prompt.submit"}) {
		t.Fatalf("呼叫：%v", g.Methods())
	}
	create := calls[1].Params
	if create["provider"] != hermes.Provider || create["model"] != "gpt-x" || create["cwd"] != "/workspace" || create["source"] != "cli" {
		t.Fatalf("session.create 參數：%v", create)
	}
	if calls[2].Params["text"] != "回覆 OK" || calls[2].Params["session_id"] != "live-1" {
		t.Fatalf("prompt.submit 參數：%v", calls[2].Params)
	}
}

func TestResumeFallsBackToCreate(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	s, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1", Resume: "stored-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.Methods(), []string{"client.capabilities", "session.resume", "session.create"}) || s.ResumeID() != "stored-"+s.ID() {
		t.Fatalf("接不回時應該開新的：%v %s", g.Methods(), s.ResumeID())
	}
	// 斷線後重連，用存下的 id 接回。
	stored := s.ResumeID()
	g.DropConnections()
	evs := collect(t, d.Events(s))
	if len(evs) != 1 || !evs[0].Fatal || evs[0].Type != engine.EventError {
		t.Fatalf("斷線事件：%+v", evs)
	}
	if err := d.Send(ctx, s, engine.Message{Text: "x"}); !errors.Is(err, engine.ErrDisconnected) {
		t.Fatalf("斷線後送訊息：%v", err)
	}
	s2, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1", Resume: stored})
	if err != nil {
		t.Fatal(err)
	}
	m := g.Methods()
	if m[len(m)-1] != "session.resume" || s2.ResumeID() != stored || s2 == s {
		t.Fatalf("應該接回：%v %s", m, s2.ResumeID())
	}
}

func TestInterrupt(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "做到一半"})
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	s, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Send(ctx, s, engine.Message{Text: "做很久的事"}); err != nil {
		t.Fatal(err)
	}
	if e := <-d.Events(s); e.Type != engine.EventText {
		t.Fatalf("先收到文字：%+v", e)
	}
	if err := d.Interrupt(ctx, s); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	if len(evs) != 1 || evs[0].Status != engine.StatusInterrupted {
		t.Fatalf("中斷：%+v", evs)
	}
}

func TestErrorMapping(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	step := 0
	g.OnPrompt = func(tr *hermestest.Turn) {
		step++
		switch step {
		case 1: // 輪中失敗：message.complete status=error
			tr.Event("message.start", nil)
			tr.Event("tool.start", map[string]any{"tool_id": "t1", "name": "terminal", "context": "ls", "args": map[string]any{"command": "ls"}})
			tr.Event("tool.complete", map[string]any{"tool_id": "t1", "name": "terminal", "result": `{"exit_code": 2, "output": "no"}`, "duration_s": 0.5})
			tr.Complete("Error: provider 401", "error", map[string]any{"error": "模型供應商拒絕了金鑰"})
		case 2: // 輪還沒開始就失敗：只有 error 事件
			tr.Event("error", map[string]any{"message": "agent init failed"})
		}
	}
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	want := []engine.EventType{engine.EventToolCall, engine.EventToolResult, engine.EventError, engine.EventDone}
	if !slices.Equal(types(evs), want) {
		t.Fatalf("事件：%+v", evs)
	}
	if tc := evs[0].Tool; tc.ID != "t1" || tc.Name != "terminal" || tc.Context != "ls" || !strings.Contains(string(tc.Args), "ls") {
		t.Fatalf("tool_call：%+v", tc)
	}
	if tr := evs[1].Tool; tr.OK || tr.Duration != 0.5 {
		t.Fatalf("exit_code 2 應該算失敗：%+v", tr)
	}
	if evs[2].Text != "模型供應商拒絕了金鑰" || evs[3].Status != engine.StatusError {
		t.Fatalf("錯誤：%+v", evs[2:])
	}

	if err := d.Send(ctx, s, engine.Message{Text: "b"}); err != nil {
		t.Fatal(err)
	}
	evs = collect(t, d.Events(s))
	if !slices.Equal(types(evs), []engine.EventType{engine.EventError, engine.EventDone}) || evs[0].Text != "agent init failed" || evs[1].Status != engine.StatusError {
		t.Fatalf("輪外錯誤：%+v", evs)
	}

	// JSON-RPC 錯誤回成 Go 的錯誤。
	var rpcErr *hermes.RPCError
	if _, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th2", Resume: ""}); err != nil {
		t.Fatal(err)
	}
	if err := d.Respond(ctx, s, "nope", engine.Answer{Choice: "once"}); err == nil || errors.As(err, &rpcErr) {
		t.Fatalf("回覆不存在的請求：%v", err)
	}
}

func TestApprovalRequest(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	answers := make(chan json.RawMessage, 2)
	g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		answers <- tr.Request("approval", map[string]any{"command": "rm -rf /workspace/tmp", "choices": []string{"once", "always", "deny"}})
		// 不認得的請求要回 -32601。
		answers <- tr.Request("preview.read", map[string]any{})
		tr.Complete("好了", "complete", nil)
	}
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "清掉暫存"}); err != nil {
		t.Fatal(err)
	}
	e := <-d.Events(s)
	if e.Type != engine.EventNeedsApproval || e.Request.Kind != "approval" || e.Request.Prompt != "rm -rf /workspace/tmp" || len(e.Request.Choices) != 3 {
		t.Fatalf("needs_approval：%+v %+v", e, e.Request)
	}
	if err := d.Respond(ctx, s, e.Request.ID, engine.Answer{Choice: "once"}); err != nil {
		t.Fatal(err)
	}
	if got := string(<-answers); got != `{"choice":"once"}` {
		t.Fatalf("核准回覆：%s", got)
	}
	if got := string(<-answers); !strings.Contains(got, "-32601") {
		t.Fatalf("不認得的請求：%s", got)
	}
	// 同一個請求不能回兩次。
	if err := d.Respond(ctx, s, e.Request.ID, engine.Answer{Choice: "once"}); err == nil {
		t.Fatal("重複回覆應該失敗")
	}
	evs := collect(t, d.Events(s))
	if evs[len(evs)-1].Text != "好了" {
		t.Fatalf("結束：%+v", evs)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	g := hermestest.New("right")
	defer g.Close()
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: g.Addr(), Key: "wrong"}, nil
	}, nil)
	if _, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"}); err == nil {
		t.Fatal("錯的 key 應該連不上")
	}
}
