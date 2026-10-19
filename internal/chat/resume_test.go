package chat

import (
	"testing"

	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// xbot.chat.resume：服務重開時被中斷（也不能再自動接著做）的工作，在對話裡按「接著做」。
func TestResumeInterruptedTask(t *testing.T) {
	e := setup(t)
	th, _ := e.db.CreateThread(bg, store.Thread{BotID: e.bot.ID})
	stale, _ := e.db.CreateTask(bg, store.Task{BotID: e.bot.ID, ThreadID: th.ID, Source: runtime.SourceOwner, State: store.TaskRunning,
		Input: "回覆 OK", Meta: []byte(`{"restart_resumes":1}`)})
	if err := e.rt.RecoverTasks(bg); err != nil {
		t.Fatal(err)
	}
	raw, err := e.call(owner, "xbot.chat.read", map[string]any{"thread_id": th.ID})
	r := must[ReadOut](t, raw, err)
	if len(r.Messages) != 1 || r.Messages[0].Status != "interrupted" || !r.Messages[0].Restart || r.Messages[0].Resumed ||
		r.Messages[0].TaskID != stale.ID {
		t.Fatalf("被中斷的工作要標出來：%+v", r.Messages)
	}

	// 預演、沒有權限的代理
	if _, err := e.call(owner, "xbot.chat.resume", map[string]any{"task_id": stale.ID}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.call(agent(e.other.ID), "xbot.chat.resume", map[string]any{"task_id": stale.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("別的 Bot 的代理：%v", err)
	}

	raw, err = e.call(owner, "xbot.chat.resume", map[string]any{"task_id": stale.ID})
	sent := must[SendOut](t, raw, err)
	if sent.ThreadID != th.ID || sent.TaskID == "" || sent.TaskID == stale.ID || sent.BotID != e.bot.ID {
		t.Fatalf("%+v", sent)
	}
	if w := e.waitFor(t, owner, sent.TaskID); w.Verdict != runtime.VerdictAnswer {
		t.Fatalf("%+v", w)
	}
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": th.ID})
	r = must[ReadOut](t, raw, err)
	if !r.Messages[0].Resumed || r.Messages[1].Sender != "owner" || r.Messages[1].Content != runtime.ResumeAsk {
		t.Fatalf("%+v", r.Messages)
	}
	if _, err := e.call(owner, "xbot.chat.resume", map[string]any{"task_id": stale.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("只能接著做一次：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.resume", map[string]any{"task_id": sent.TaskID}); code(err) != ops.CodeConflict {
		t.Fatalf("做完的不用接著做：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.resume", map[string]any{"task_id": "nope"}); code(err) != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}
}
