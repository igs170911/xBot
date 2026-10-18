package runtime

// X02：排隊與優先順序（你的訊息排在 Bot 交辦前面，LP-1）、停止與停止連鎖的 Hooks、服務重開後接續。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

// gate 讓假 gateway 的每一輪等測試放行，並記下收到 prompt 的順序。
type gate struct {
	mu      sync.Mutex
	order   []string
	release map[string]chan struct{}
}

func newGate() *gate { return &gate{release: map[string]chan struct{}{}} }

func (g *gate) ch(text string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.release[text] == nil {
		g.release[text] = make(chan struct{})
	}
	return g.release[text]
}

func (g *gate) onPrompt(tr *hermestest.Turn) {
	g.mu.Lock()
	g.order = append(g.order, tr.Text)
	g.mu.Unlock()
	select {
	case <-g.ch(tr.Text):
		tr.Complete("好了："+tr.Text, "complete", nil)
	case <-tr.Interrupted():
		tr.Complete("", "interrupted", nil)
	}
}

func (g *gate) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.order...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等不到：" + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// LazyBoy test_the_users_messages_go_before_bot_messages：Bot 在做隊友交辦時，你的訊息排在其他排隊的交辦前面。
func TestTheUsersMessagesGoBeforeBotMessages(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	g := newGate()
	f.g.OnPrompt = g.onPrompt
	a, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "隊友交辦一", Source: SourceBot, LowPriority: true, Queue: true})
	if err != nil || a.Queued {
		t.Fatalf("%+v %v", a, err)
	}
	waitFor(t, "第一件開始", func() bool { return len(g.seen()) == 1 })
	b, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "隊友交辦二", Source: SourceBot, LowPriority: true, Queue: true})
	if err != nil || !b.Queued {
		t.Fatalf("%+v %v", b, err)
	}
	// 你的訊息：Bot 在做低優先的事，不回「正在忙」，排在第一個。
	c, err := f.rt.Chat(ctx, f.bot.ID, "", "使用者自己的問題")
	if err != nil || !c.Queued {
		t.Fatalf("你的訊息要排隊：%+v %v", c, err)
	}
	if busy, low := f.rt.Busy(f.bot.ID); !busy || !low {
		t.Fatal("Busy")
	}
	close(g.ch("隊友交辦一"))
	waitFor(t, "第二件開始", func() bool { return len(g.seen()) == 2 })
	if got := g.seen()[1]; got != "使用者自己的問題" {
		t.Fatalf("下一件應該是你的訊息：%v", g.seen())
	}
	// 你自己的工作在做時，你再傳一則還是回「正在忙」。
	if _, err := f.rt.Chat(ctx, f.bot.ID, "", "又一則"); !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	close(g.ch("使用者自己的問題"))
	close(g.ch("隊友交辦二"))
	for _, id := range []string{a.TaskID, b.TaskID, c.TaskID} {
		if task := f.wait(t, id); task.Verdict != VerdictAnswer {
			t.Fatalf("%+v", task)
		}
	}
	if strings.Join(g.seen(), ",") != "隊友交辦一,使用者自己的問題,隊友交辦二" {
		t.Fatalf("順序：%v", g.seen())
	}
}

func TestStopCancelsQueuedWorkAndCallsHooks(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	g := newGate()
	f.g.OnPrompt = g.onPrompt
	var mu sync.Mutex
	var stopped []string
	finished := map[string]string{}
	f.rt.SetHooks(Hooks{
		Stopped: func(_ string, ids []string) { mu.Lock(); stopped = append(stopped, ids...); mu.Unlock() },
		Finished: func(task store.Task, verdict, _ string) {
			mu.Lock()
			finished[task.ID] = verdict
			mu.Unlock()
		},
	})
	a, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "一", Source: SourceBot, LowPriority: true, Queue: true})
	waitFor(t, "開始", func() bool { return len(g.seen()) == 1 })
	b, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "二", Source: SourceBot, LowPriority: true, Queue: true})
	c, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "三", Source: SourceBot, LowPriority: true, Queue: true})
	// 只停一件排隊中的。
	if ok, err := f.rt.StopTask(ctx, b.TaskID); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if task, _ := f.db.GetTask(ctx, b.TaskID); task.Verdict != VerdictCancelled {
		t.Fatalf("%+v", task)
	}
	// 停 Bot：正在做的和排隊中的都停，Hooks.Stopped 拿到兩件。
	if ok, err := f.rt.Stop(ctx, f.bot.ID, ""); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	f.wait(t, a.TaskID)
	waitFor(t, "Finished", func() bool { mu.Lock(); defer mu.Unlock(); return len(finished) == 3 })
	mu.Lock()
	defer mu.Unlock()
	if len(stopped) != 2 || finished[a.TaskID] != VerdictCancelled || finished[b.TaskID] != VerdictCancelled || finished[c.TaskID] != VerdictCancelled {
		t.Fatalf("stopped=%v finished=%v", stopped, finished)
	}
	if len(g.seen()) != 1 {
		t.Fatalf("停掉的不該開始：%v", g.seen())
	}
}

func TestRecoverTasksInterruptsStaleWorkAndStartsTheQueue(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	th, _ := f.db.CreateThread(ctx, store.Thread{BotID: f.bot.ID})
	stale, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceBot, State: store.TaskRunning, Input: "做到一半"})
	queued, _ := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: th.ID, Source: SourceBot, State: store.TaskQueued, Input: "排隊中", LowPriority: true})
	var got []string
	f.rt.SetHooks(Hooks{Finished: func(task store.Task, verdict, _ string) { got = append(got, task.ID+":"+verdict) }})
	if err := f.rt.RecoverTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if task, _ := f.db.GetTask(ctx, stale.ID); task.State != store.TaskDone || task.Verdict != VerdictInterrupted {
		t.Fatalf("%+v", task)
	}
	if task := f.wait(t, queued.ID); task.Verdict != VerdictAnswer {
		t.Fatalf("排隊中的要開始：%+v", task)
	}
	if len(got) < 1 || got[0] != stale.ID+":"+VerdictInterrupted {
		t.Fatalf("%v", got)
	}
}

func TestRoomTurnsYouStartedAreAttended(t *testing.T) {
	for _, c := range []struct {
		source string
		low    bool
		want   bool
	}{{SourceOwner, false, true}, {SourceRoom, false, true}, {SourceRoom, true, false}, {SourceBot, true, false}, {SourceRoutine, false, false}} {
		if got := (&turn{source: c.source, lowPriority: c.low}).attended(); got != c.want {
			t.Fatalf("%+v：%v", c, got)
		}
	}
}

func TestPromptHookAndSilentReply(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	var prompt string
	var mu sync.Mutex
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		mu.Lock()
		prompt = tr.Text
		mu.Unlock()
		tr.Complete("[silent]", "complete", nil)
	}
	f.rt.SetHooks(Hooks{
		Prompt: func(_ context.Context, task store.Task) (string, bool, error) {
			return "組好的：" + task.Input, true, nil
		},
		Silent: func(_, verdict, text string) bool { return text == "[silent]" },
	})
	res, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "群組有新訊息", Source: SourceRoom, LowPriority: true, Queue: true, Sender: "system"})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	mu.Lock()
	defer mu.Unlock()
	if prompt != "組好的：群組有新訊息" {
		t.Fatalf("%q", prompt)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || msgs[0].Sender != "system" || !strings.Contains(string(msgs[1].Meta), `"silent":true`) {
		t.Fatalf("%+v", msgs)
	}
}
