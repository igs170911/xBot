package collab

// X04：完成判定餵給協作層——隊友交辦的請求只回「我來查一下」不算回答了。

import (
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/judge"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// judged 像 runtime.finish 一樣把判斷寫進 task.meta。
func (tm *team) judged(taskID string, j runtime.Judgment) {
	tm.t.Helper()
	if err := tm.db.PatchTaskMeta(tm.ctx, taskID, map[string]any{"judge": j}); err != nil {
		tm.t.Fatal(err)
	}
}

func TestAPromiseIsNotAnAnswerToATeammate(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "查新品價格", "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)
	msgID := metaOf(task).MessageID
	assignment := metaOf(task).AssignmentID

	// runtime 判成只說要做、叫它接著做：續做的那一輪帶著原本的交辦。
	meta, ok := tm.s.continueMeta(task)
	if !ok {
		t.Fatal("隊友的請求要能續做")
	}
	var cm taskMeta
	_ = json.Unmarshal(meta, &cm)
	if cm.FromBot != chief || cm.MessageID != msgID || cm.AssignmentID != assignment || cm.Intent != IntentRequest {
		t.Fatalf("%+v", cm)
	}
	cont := must[runtime.ChatResult](t)(tm.rt.Start(tm.ctx, runtime.ChatRequest{BotID: writer, ThreadID: task.ThreadID, Text: "自動繼續：剛才只說要做，現在真的去做",
		Source: runtime.SourceBot, LowPriority: true, Queue: true, TaskMeta: meta}))
	tm.judged(task.ID, runtime.Judgment{Verdict: judge.OnlyPromised, ContinuedBy: cont.TaskID})
	tm.finish(task.ID, runtime.VerdictAnswer, "好的，我來查一下價格。")
	// 不回覆、傳話還沒回答、交辦改記在續做的那一輪。
	if len(tm.results(chief)) != 0 {
		t.Fatal("承諾不算回答")
	}
	if m := must[store.BotMessage](t)(tm.db.GetBotMessage(tm.ctx, msgID)); m.Answered || m.TaskID != cont.TaskID {
		t.Fatalf("%+v", m)
	}
	if a := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, assignment)); a.TaskID != cont.TaskID || a.Status == store.AssignDone {
		t.Fatalf("%+v", a)
	}
	// 續做的那一輪真的回答了：送回 Chief。
	next := tm.task(cont.TaskID)
	tm.start(next)
	tm.judged(next.ID, runtime.Judgment{Verdict: judge.Done})
	tm.finish(next.ID, runtime.VerdictAnswer, "A 店 120 元，B 店 99 元。")
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(tm.last(back[0].ThreadID).Content, "B 店 99 元") {
		t.Fatalf("%+v", back)
	}
	if m := must[store.BotMessage](t)(tm.db.GetBotMessage(tm.ctx, msgID)); !m.Answered {
		t.Fatal("回答了")
	}
	if a := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, assignment)); a.Status != store.AssignDone {
		t.Fatalf("%+v", a)
	}
}

// 叫它接著做之後還是只說要做（或不能再續做）：照沒有完成回覆，交辦人不會把一句承諾當成答案。
func TestAPromiseThatIsNeverKeptComesBackAsNotDone(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "查新品價格", "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)
	tm.judged(task.ID, runtime.Judgment{Verdict: judge.OnlyPromised})
	tm.finish(task.ID, runtime.VerdictAnswer, "我來查一下")
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(tm.last(back[0].ThreadID).Content, "沒有完成你的請求") {
		t.Fatalf("%+v", back)
	}
	if a := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, metaOf(task).AssignmentID)); a.Status != store.AssignFailed {
		t.Fatalf("%+v", a)
	}
	// 群組的一輪、不用回覆的通知：不續做。
	if _, ok := tm.s.continueMeta(store.Task{Meta: json.RawMessage(`{"room_id":"r","from_bot":"x","intent":"request"}`)}); ok {
		t.Fatal("群組")
	}
	if _, ok := tm.s.continueMeta(store.Task{Meta: json.RawMessage(`{"from_bot":"x","intent":"fyi"}`)}); ok {
		t.Fatal("fyi")
	}
}
