package acp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
)

// quiet 讀事件 d 這麼久，期間不能有 done。
func quiet(t *testing.T, ch <-chan engine.Event, d time.Duration) []engine.Event {
	t.Helper()
	var out []engine.Event
	deadline := time.After(d)
	for {
		select {
		case e := <-ch:
			if e.Type == engine.EventDone {
				t.Fatalf("背景工作還沒回報就結束了這一輪：%+v", append(out, e))
			}
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

// 2026-10-21 島灰：Grok 把研究丟到背景（workflow），session/prompt 就回來了。這一輪要留著：背景工作在步驟裡顯示成
// 進行中，做完後 agent 自己開的那一輪（讀網頁、寫報告）算在同一輪裡，權限請求照常處理，回報完才結束。
func TestBackgroundWorkKeepsTheTurnOpen(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptBackground}); err != nil {
		t.Fatal(err)
	}
	evs := quiet(t, ch, 300*time.Millisecond)
	if text(evs, engine.EventText) != "研究開跑了" {
		t.Fatalf("回覆：%+v", evs)
	}
	var bg *engine.ToolEvent
	for _, e := range evs {
		if e.Type == engine.EventToolCall && e.Tool.ID == "bg:wf1" {
			bg = e.Tool
		}
	}
	if bg == nil || bg.Name != "背景工作：deep-research" {
		t.Fatalf("背景工作要是一個進行中的步驟：%+v", evs)
	}
	agent.Finish()
	// 讀網頁 XBot 直接允許；ask 等級改檔案要問人。
	var rest []engine.Event
	for req := (engine.Event{}); req.Type != engine.EventNeedsApproval; {
		select {
		case req = <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("等不到改檔案的核准：%+v", rest)
		}
		rest = append(rest, req)
		if req.Type == engine.EventDone {
			t.Fatalf("回報完之前就結束了：%+v", rest)
		}
		if req.Type == engine.EventNeedsApproval {
			if !strings.Contains(req.Request.Prompt, "報告.md") {
				t.Fatalf("要問的是改檔案：%+v", req.Request)
			}
			if err := d.Respond(context.Background(), s, req.Request.ID, engine.Answer{Choice: "once"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	rest = append(rest, collect(t, ch)...)
	done := rest[len(rest)-1]
	if done.Type != engine.EventDone || done.Status != engine.StatusComplete {
		t.Fatalf("結束：%+v", rest)
	}
	if got := text(rest, engine.EventText); got != "報告好了：allow,allow" {
		t.Fatalf("回報：%q", got)
	}
	var finished, usage bool
	for _, e := range rest {
		finished = finished || (e.Type == engine.EventToolResult && e.Tool.ID == "bg:wf1" && e.Tool.OK)
		usage = usage || (e.Type == engine.EventUsage && e.Usage.InputTokens == 100)
	}
	if !finished || !usage {
		t.Fatalf("背景工作的步驟要結束、回報那一輪的用量要算：%+v", rest)
	}
	// 下一則照常。
	hello(t, d, s)
}

// 「一般的事自己做」以上：改 /workspace 裡的檔案也不用問，背景工作回報完整個做完。
func TestBackgroundWorkWithEditsLevelNeedsNoOne(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "edits"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptBackground}); err != nil {
		t.Fatal(err)
	}
	quiet(t, ch, 100*time.Millisecond)
	agent.Finish()
	evs := collect(t, ch)
	for _, e := range evs {
		if e.Type == engine.EventNeedsApproval {
			t.Fatalf("不用問人：%+v", e.Request)
		}
	}
	if got := text(evs, engine.EventText); got != "報告好了：allow,allow" {
		t.Fatalf("回報：%q", got)
	}
}

// 等背景工作時按停止：直接結束這一輪（prompt 已經回來了，不會再有回覆）。
func TestStopWhileWaitingForBackgroundWork(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptBackground}); err != nil {
		t.Fatal(err)
	}
	quiet(t, ch, 100*time.Millisecond)
	if err := d.Interrupt(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if done := evs[len(evs)-1]; done.Type != engine.EventDone || done.Status != engine.StatusInterrupted {
		t.Fatalf("結束：%+v", evs)
	}
	closed := false
	for _, e := range evs {
		closed = closed || (e.Type == engine.EventToolResult && e.Tool.ID == "bg:wf1" && !e.Tool.OK)
	}
	if !closed {
		t.Fatalf("放下的背景工作，步驟要以沒做完收掉：%+v", evs)
	}
	// 背景工作還在跑時問下一件事：不能被留著等它（審查抓到：下一則不相干的訊息也一直不結束）。
	hello(t, d, s)
	// 之後背景工作做完、agent 自己開的那一輪：不算進任何一輪，權限請求取消。
	agent.Finish()
	time.Sleep(100 * time.Millisecond)
	hello(t, d, s)
}

// 單獨開的背景子代理：prompt 回來時還沒做完，這一輪留著，工作步驟顯示成進行中，做完回報才結束。
func TestBackgroundSubagentIsShownAndWaitedFor(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptBgSubagent}); err != nil {
		t.Fatal(err)
	}
	evs := quiet(t, ch, 200*time.Millisecond)
	shown := false
	for _, e := range evs {
		shown = shown || (e.Type == engine.EventToolCall && e.Tool.ID == "sub:s9" && e.Tool.Name == "背景子代理：查價格")
	}
	if !shown {
		t.Fatalf("背景子代理要是一個進行中的步驟：%+v", evs)
	}
	agent.Finish()
	rest := collect(t, ch)
	if got := text(rest, engine.EventText); got != "價格查好了" {
		t.Fatalf("回報：%q %+v", got, rest)
	}
	ok := false
	for _, e := range rest {
		ok = ok || (e.Type == engine.EventToolResult && e.Tool.ID == "sub:s9" && e.Tool.OK)
	}
	if !ok || rest[len(rest)-1].Status != engine.StatusComplete {
		t.Fatalf("%+v", rest)
	}
}

// 這一輪結束之後 agent 又送來的文字不能混進下一輪的回覆。
func TestLateUpdatesDoNotLeakIntoTheNextTurn(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptLate}); err != nil {
		t.Fatal(err)
	}
	if got := text(collect(t, ch), engine.EventText); got != "done" {
		t.Fatalf("回覆：%q", got)
	}
	time.Sleep(50 * time.Millisecond)
	hello(t, d, s)
}

// Grok 一拒絕就整輪取消、等人下指令。XBot 的「拒絕」只是不准那個動作（同 Hermes）：同一輪請它換方法接著做完。
// 按停止（Interrupt）的取消照舊結束。
func TestDenyDoesNotEndTheTurn(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, nil)
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptDenyCancels}); err != nil {
		t.Fatal(err)
	}
	req := until(t, ch, engine.EventNeedsApproval)
	if err := d.Respond(context.Background(), s, req.Request.ID, engine.Answer{Choice: "deny"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	done := evs[len(evs)-1]
	if done.Status != engine.StatusComplete || !strings.Contains(text(evs, engine.EventText), "echo:"+acp.DeniedNote) {
		t.Fatalf("拒絕後要接著做：%+v", evs)
	}
	if ps := prompts(t, agent); len(ps) != 2 || ps[1].Prompt[0].Text != acp.DeniedNote {
		t.Fatalf("送了：%+v", ps)
	}
	// 按停止的取消不接著做。
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptDenyCancels}); err != nil {
		t.Fatal(err)
	}
	until(t, ch, engine.EventNeedsApproval)
	if err := d.Interrupt(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if evs := collect(t, ch); evs[len(evs)-1].Status != engine.StatusInterrupted {
		t.Fatalf("按停止：%+v", evs)
	}
	if ps := prompts(t, agent); len(ps) != 3 {
		t.Fatalf("按停止不再送：%d", len(ps))
	}
}
