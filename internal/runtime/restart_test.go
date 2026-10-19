package runtime

// 服務重開時做到一半的工作（restart.go）：自動接著做一次、再中斷才收掉、對話裡的「接著做」。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

// prompts 記下假 gateway 收到的每一輪，回「好了」。
type prompts struct {
	mu   sync.Mutex
	seen []string
}

func (p *prompts) on(tr *hermestest.Turn) {
	p.mu.Lock()
	p.seen = append(p.seen, tr.Text)
	p.mu.Unlock()
	tr.Complete("好了", "complete", nil)
}

func (p *prompts) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// finishedLog 記下 Hooks.Finished（等它的 Bot 會收到什麼）。
type finishedLog struct {
	mu  sync.Mutex
	got map[string][]string
}

func (l *finishedLog) hook(task store.Task, verdict, _ string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.got == nil {
		l.got = map[string][]string{}
	}
	l.got[task.ID] = append(l.got[task.ID], verdict)
}

func (l *finishedLog) of(id string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got[id]...)
}

func msgMeta(m store.Message) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(m.Meta, &out)
	return out
}

// 隊友交辦的工作做到一半服務重開：同一件工作接著做（交辦紀錄、傳話都還指著它），交辦人收到的是做完的結果，
// 不是「做到一半被中斷」；寫到一半的回覆不再顯示在寫，對話裡多一行「服務剛重開，接著做」。
func TestRestartResumesInterruptedWorkOnce(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	p := &prompts{}
	f.g.OnPrompt = p.on
	log := &finishedLog{}
	f.rt.SetHooks(Hooks{Finished: log.hook})
	th, _ := f.db.CreateThread(ctx, store.Thread{BotID: f.bot.ID})
	stale, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceBot, State: store.TaskRunning, LowPriority: true,
		Input: "整理報告", Meta: []byte(`{"from_bot":"chief","assignment_id":"as1"}`)})
	half, _ := f.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "bot:" + f.bot.ID, Content: "先查了三家",
		Meta: []byte(`{"task_id":"` + stale.ID + `","streaming":true}`)})

	if err := f.rt.RecoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	task := f.wait(t, stale.ID)
	if task.Verdict != VerdictAnswer {
		t.Fatalf("要接著做完：%+v", task)
	}
	got := p.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], resumeNote) || !strings.HasSuffix(got[0], "\n\n整理報告") {
		t.Fatalf("接著做的那一輪：%q", got)
	}
	waitFor(t, "Finished", func() bool { return len(log.of(stale.ID)) > 0 })
	if v := log.of(stale.ID); len(v) != 1 || v[0] != VerdictAnswer {
		t.Fatalf("交辦人只收到做完的結果，不是中斷：%v", v)
	}
	var meta map[string]any
	_ = json.Unmarshal(task.Meta, &meta)
	if meta["restart_resumes"] != float64(1) || meta["from_bot"] != "chief" || meta["assignment_id"] != "as1" {
		t.Fatalf("交辦要保留、記下接著做過：%v", meta)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, th.ID, 0, 0)
	if len(msgs) != 3 {
		t.Fatalf("訊息：%+v", msgs)
	}
	if m := msgMeta(msgs[0]); msgs[0].ID != half.ID || m["streaming"] != nil || m["status"] != nil || m["restart"] != true || msgs[0].Content != "先查了三家" {
		t.Fatalf("寫到一半的回覆：%+v %v", msgs[0], m)
	}
	if m := msgMeta(msgs[1]); msgs[1].Sender != "system" || msgs[1].Kind != KindActivity || msgs[1].Content != ResumeText ||
		m["activity"] != ActivityResume || m["task_id"] != stale.ID {
		t.Fatalf("接著做那一行：%+v", msgs[1])
	}
	if msgs[2].Content != "好了" || msgMeta(msgs[2])["task_id"] != stale.ID {
		t.Fatalf("新的回覆：%+v", msgs[2])
	}

	// 接著做的那一輪又遇到重開：不再接著做，收掉，交辦人收到中斷；對話裡的回覆標成被中斷、可以按「接著做」。
	if err := f.db.SetTaskState(ctx, stale.ID, store.TaskRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.db.UpdateMessage(ctx, msgs[2].ID, "又做了一些", nil, []byte(`{"task_id":"`+stale.ID+`","streaming":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.RecoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ := f.db.GetTask(ctx, stale.ID); task.State != store.TaskDone || task.Verdict != VerdictInterrupted {
		t.Fatalf("第二次要收掉：%+v", task)
	}
	if v := log.of(stale.ID); len(v) != 2 || v[1] != VerdictInterrupted {
		t.Fatalf("%v", v)
	}
	if len(p.all()) != 1 {
		t.Fatalf("不能再接著做：%q", p.all())
	}
	reply, _ := f.db.TaskReply(ctx, th.ID, stale.ID)
	if m := msgMeta(reply); m["status"] != "interrupted" || m["restart"] != true || m["streaming"] != nil {
		t.Fatalf("被中斷的回覆：%v", m)
	}
}

// 還沒寫回覆就被中斷、也不能自動接著做：補一則空的回覆放「接著做」；按了在同一條對話串開一件你交辦的新工作，
// 只能按一次。
func TestResumeInterruptedFromChat(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	p := &prompts{}
	f.g.OnPrompt = p.on
	th, _ := f.db.CreateThread(ctx, store.Thread{BotID: f.bot.ID})
	stale, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceOwner, State: store.TaskWaitingBox,
		Input: resumePrompt("查天氣"), Meta: []byte(`{"restart_resumes":1}`)})
	if err := f.rt.RecoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, th.ID, 0, 0)
	if len(msgs) != 1 || msgs[0].Sender != "bot:"+f.bot.ID || msgs[0].Content != "" {
		t.Fatalf("要補一則放「接著做」：%+v", msgs)
	}
	if m := msgMeta(msgs[0]); m["status"] != "interrupted" || m["restart"] != true || m["task_id"] != stale.ID {
		t.Fatalf("%v", m)
	}

	res, err := f.rt.ResumeInterrupted(ctx, stale.ID, ChatRequest{Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ThreadID != th.ID || res.TaskID == stale.ID {
		t.Fatalf("%+v", res)
	}
	task := f.wait(t, res.TaskID)
	if task.Source != SourceOwner || task.Verdict != VerdictAnswer {
		t.Fatalf("%+v", task)
	}
	if got := p.all(); len(got) != 1 || got[0] != resumeNote+"\n\n查天氣" {
		t.Fatalf("說明只放一次：%q", got)
	}
	old, _ := f.db.GetTask(ctx, stale.ID)
	var meta map[string]any
	_ = json.Unmarshal(old.Meta, &meta)
	if meta["resumed_by"] != res.TaskID {
		t.Fatalf("%v", meta)
	}
	if m := msgMeta(must(f.db.TaskReply(ctx, th.ID, stale.ID))); m["resumed"] != true {
		t.Fatalf("按過了：%v", m)
	}
	msgs, _ = f.db.ListMessages(ctx, store.ScopeThread, th.ID, 0, 0)
	if msgs[1].Sender != "owner" || msgs[1].Content != ResumeAsk {
		t.Fatalf("你的那一則：%+v", msgs[1])
	}
	if _, err := f.rt.ResumeInterrupted(ctx, stale.ID, ChatRequest{}); !errors.Is(err, ErrResumed) {
		t.Fatalf("只能按一次：%v", err)
	}
	if _, err := f.rt.ResumeInterrupted(ctx, res.TaskID, ChatRequest{}); !errors.Is(err, ErrNotInterrupted) {
		t.Fatalf("做完的不用接著做：%v", err)
	}
}

// 群組的一輪不自動接著做、也不放「接著做」；你按了停止的照停止收尾；隊友交辦的按「接著做」會提醒交辦人已收到沒完成。
func TestRestartLeavesRoomTurnsAndStopsAlone(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	p := &prompts{}
	f.g.OnPrompt = p.on
	log := &finishedLog{}
	f.rt.SetHooks(Hooks{Finished: log.hook})
	th, _ := f.db.CreateThread(ctx, store.Thread{BotID: f.bot.ID})
	room, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceRoom, State: store.TaskRunning, Input: "群組"})
	stopping, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceOwner, State: store.TaskStopping, Input: "停下"})
	if err := f.rt.RecoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ := f.db.GetTask(ctx, room.ID); task.Verdict != VerdictInterrupted {
		t.Fatalf("%+v", task)
	}
	if task, _ := f.db.GetTask(ctx, stopping.ID); task.Verdict != VerdictCancelled {
		t.Fatalf("按了停止的照停止收尾：%+v", task)
	}
	if v := log.of(stopping.ID); len(v) != 1 || v[0] != VerdictCancelled {
		t.Fatalf("%v", v)
	}
	if msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, th.ID, 0, 0); len(msgs) != 0 {
		t.Fatalf("不放「接著做」：%+v", msgs)
	}
	if _, err := f.rt.ResumeInterrupted(ctx, room.ID, ChatRequest{}); !errors.Is(err, ErrNotInterrupted) {
		t.Fatalf("群組的一輪：%v", err)
	}
	if len(p.all()) != 0 {
		t.Fatalf("%q", p.all())
	}

	peer, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceBot, State: store.TaskDone, Verdict: VerdictInterrupted,
		Input: "整理報告"})
	res, err := f.rt.ResumeInterrupted(ctx, peer.ID, ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	if got := p.all(); len(got) != 1 || !strings.Contains(got[0], "整理報告") || !strings.HasSuffix(got[0], resumeFromBot) {
		t.Fatalf("%q", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
