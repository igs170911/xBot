package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/store"
)

// waitKind 等到匯流排上出現某種事件。
func waitKind(t *testing.T, sub *events.Subscription, kind string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-sub.C:
			if e.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("等不到 %s", kind)
		}
	}
}

// steerTurn：先說「我先查一下」、跑一個工具，工具跑的時候等插話；工具做完照收到的插話回答。
func steerTurn(turn *hermestest.Turn) {
	turn.Event("message.start", nil)
	turn.Event("message.delta", map[string]any{"text": "我先查一下。"})
	turn.Event("tool.start", map[string]any{"tool_id": "c1", "name": "web_search"})
	answer := "海報做好了。"
	select {
	case s := <-turn.Steers():
		if strings.Contains(s, "改用藍色") {
			answer = "好，海報改成藍色了。"
		}
	case <-time.After(3 * time.Second):
	}
	turn.Event("tool.complete", map[string]any{"tool_id": "c1", "name": "web_search", "result": "ok"})
	turn.Event("message.delta", map[string]any{"text": answer})
	turn.Complete(answer, "complete", nil)
}

// 2026-10-24 使用者：「排隊的上限我想解除封印，讓他順一點」。Bot 在這條對話串做事（工具在跑）時你補的話，直接送進那一輪：
// 同一件工作、不排隊、不另開一輪；回答在你補充的那則之後的新泡泡，用工具前的旁白不留在前一個泡泡。
func TestOwnerFollowUpSteersIntoTheRunningTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	f.g.OnPrompt = steerTurn
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我做海報")
	if err != nil {
		t.Fatal(err)
	}
	waitKind(t, sub, KindToolCall)
	more, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "改用藍色")
	if err != nil {
		t.Fatal(err)
	}
	if !more.Steered || more.Queued || more.TaskID != res.TaskID {
		t.Fatalf("要送進正在做的那件：%+v", more)
	}
	f.wait(t, res.TaskID)
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var got []string
	for _, m := range msgs {
		got = append(got, m.Sender[:min(len(m.Sender), 5)]+":"+m.Content)
	}
	want := []string{"owner:幫我做海報", "bot:0:", "owner:改用藍色", "bot:0:好，海報改成藍色了。"}
	if len(got) != 4 || got[0] != want[0] || got[1] != "bot:0:" && !strings.HasPrefix(got[1], "bot:") || got[2] != want[2] || !strings.HasSuffix(got[3], "好，海報改成藍色了。") {
		t.Fatalf("訊息順序：%q", got)
	}
	if msgs[1].Content != "" {
		t.Fatalf("插話前只有旁白：前一個泡泡不留字：%q", msgs[1].Content)
	}
	var meta map[string]any
	_ = json.Unmarshal(msgs[2].Meta, &meta)
	if meta["steered"] != true || meta["task_id"] != res.TaskID {
		t.Fatalf("插話那則的 meta：%v", meta)
	}
	tasks, _ := f.db.ListTasks(ctx, f.bot.ID, false)
	if len(tasks) != 1 {
		t.Fatalf("只有一件工作：%d", len(tasks))
	}
	if steered := f.g.Methods(); !strings.Contains(strings.Join(steered, ","), "session.steer") {
		t.Fatalf("要用 session.steer：%v", steered)
	}
}

// 工具沒在跑（模型正在寫）時，Hermes 收下插話會等這一輪結束再自己開一輪：不插話，照原本排隊。
func TestFollowUpQueuesWhenNoToolIsRunning(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	release := make(chan struct{})
	f.g.OnPrompt = func(turn *hermestest.Turn) {
		turn.Event("message.start", nil)
		turn.Event("message.delta", map[string]any{"text": "寫"})
		if turn.Text == "第一則" || strings.HasPrefix(turn.Text, "第一則") {
			<-release
		}
		turn.Complete("好", "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "第一則")
	if err != nil {
		t.Fatal(err)
	}
	waitKind(t, sub, KindText)
	more, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, ThreadID: res.ThreadID, Text: "第二則", Queue: true, Steer: true}) // 同 xbot.chat.send
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if more.Steered || !more.Queued || more.TaskID == res.TaskID {
		t.Fatalf("沒有工具在跑：照原本排隊：%+v", more)
	}
	f.wait(t, more.TaskID)
}

// 還沒送給引擎（例：電腦還在開機）時補的話，直接併進要送的那一則：一件工作、Bot 一次看到兩句。
func TestFollowUpBeforeSendJoinsTheInput(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	f := setupWith(t, func(o *Options) {
		o.Admit = func(ctx context.Context, botID string, wait func(any)) (func(), error) {
			<-gate
			return func() {}, nil
		}
	})
	var seen []string
	f.g.OnPrompt = func(turn *hermestest.Turn) {
		seen = append(seen, turn.Text)
		hermestest.ReplyOK(turn)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我做海報")
	if err != nil {
		t.Fatal(err)
	}
	more, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "改用藍色")
	close(gate)
	if err != nil {
		t.Fatal(err)
	}
	if !more.Steered || more.TaskID != res.TaskID {
		t.Fatalf("開機中補的話併進同一件：%+v", more)
	}
	f.wait(t, res.TaskID)
	if len(seen) != 1 || !strings.Contains(seen[0], "幫我做海報") || !strings.Contains(seen[0], "改用藍色") {
		t.Fatalf("引擎收到的：%q", seen)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 3 || msgs[1].Content != "改用藍色" || msgs[2].Sender != "bot:"+f.bot.ID {
		t.Fatalf("回覆排在補充那則之後：%+v", msgs)
	}
}

// 壓縮對話時 Hermes 把插話排到下一輪、這一輪結束後自己接著開一輪：接成同一件工作，兩段回答都留著。
func TestSteerCarriedIntoHermesFollowUpTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.SteerNextTurn = true
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	atTool := make(chan struct{})
	f.g.OnPrompt = func(turn *hermestest.Turn) {
		turn.Event("message.start", nil)
		if strings.Contains(turn.Text, "改用藍色") {
			turn.Event("message.delta", map[string]any{"text": "好，改成藍色。"})
			turn.Complete("好，改成藍色。", "complete", nil)
			return
		}
		turn.Event("tool.start", map[string]any{"tool_id": "c1", "name": "web_search"})
		<-atTool
		turn.Event("tool.complete", map[string]any{"tool_id": "c1", "name": "web_search", "result": "ok"})
		turn.Event("message.delta", map[string]any{"text": "海報做好了。"})
		turn.Complete("海報做好了。", "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我做海報")
	if err != nil {
		t.Fatal(err)
	}
	waitKind(t, sub, KindToolCall)
	more, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "改用藍色")
	close(atTool)
	if err != nil || !more.Steered {
		t.Fatalf("插話：%+v %v", more, err)
	}
	task := f.wait(t, res.TaskID)
	if task.Verdict != VerdictAnswer {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "海報做好了。") || !strings.Contains(last.Content, "好，改成藍色。") {
		t.Fatalf("兩段回答都要留著：%q", last.Content)
	}
	tasks, _ := f.db.ListTasks(ctx, f.bot.ID, false)
	if len(tasks) != 1 {
		t.Fatalf("只有一件工作：%d", len(tasks))
	}
}
