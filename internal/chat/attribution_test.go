package chat

import "testing"

// MCP-13：代理送的訊息記下是哪個代理，畫面標「由 ○○（MCP）傳送」；工作記在代理名下（MCP-12 每日花費）。
func TestAgentMessageAttribution(t *testing.T) {
	e := setup(t)
	a := agent(e.bot.ID)
	raw, err := e.call(a, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "回覆 OK"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, a, sent.TaskID)
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID})
	r := must[ReadOut](t, raw, err)
	if m := r.Messages[0]; m.Actor != "agent:ag" || m.ActorName != "Agent" || m.Via != "mcp" || m.Sender != "owner" {
		t.Fatalf("代理送的訊息：%+v", m)
	}
	if m := r.Messages[1]; m.Actor != "" {
		t.Fatalf("Bot 的回覆不標代理：%+v", m)
	}
	task, err := e.db.GetTask(bg, sent.TaskID)
	if err != nil || string(task.Meta) != `{"actor":"agent:ag"}` {
		t.Fatalf("工作記在代理名下：%s %v", task.Meta, err)
	}
	// 你自己送的不標。
	raw, err = e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "再一次", "thread_id": sent.ThreadID})
	s2 := must[SendOut](t, raw, err)
	e.waitFor(t, owner, s2.TaskID)
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID})
	r = must[ReadOut](t, raw, err)
	if m := r.Messages[2]; m.Actor != "" || m.Content != "再一次" {
		t.Fatalf("你送的：%+v", m)
	}
}
