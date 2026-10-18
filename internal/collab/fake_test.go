package collab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"xbot/internal/events"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// fakeRT 是不會真的執行的 runtime（同 LazyBoy 測試的 runtime.closed=True）：工作只會排隊，測試自己推動每一輪。
type fakeRT struct {
	db     *store.DB
	bus    *events.Bus
	hooks  runtime.Hooks
	mu     sync.Mutex
	active map[string]string // botID → taskID
	refuse error
}

func (f *fakeRT) Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error) {
	if f.refuse != nil {
		return runtime.ChatResult{}, f.refuse
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return runtime.ChatResult{}, errors.New("訊息是空的")
	}
	thread := req.ThreadID
	if thread == "" {
		th, err := f.db.CreateThread(ctx, store.Thread{BotID: req.BotID, Title: text})
		if err != nil {
			return runtime.ChatResult{}, err
		}
		thread = th.ID
	}
	source := req.Source
	if source == "" {
		source = runtime.SourceOwner
	}
	prompt := req.Prompt
	if prompt == "" {
		prompt = text
	}
	sender := req.Sender
	if sender == "" {
		sender = "owner"
	}
	task, err := f.db.CreateTask(ctx, store.Task{BotID: req.BotID, ThreadID: thread, Source: source, Input: prompt, LowPriority: req.LowPriority, Meta: req.TaskMeta})
	if err != nil {
		return runtime.ChatResult{}, err
	}
	meta := map[string]any{"task_id": task.ID}
	for k, v := range req.MessageMeta {
		meta[k] = v
	}
	raw, _ := json.Marshal(meta)
	m, err := f.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: thread, Sender: sender, Kind: req.Kind, Content: text, Meta: raw})
	if err != nil {
		return runtime.ChatResult{}, err
	}
	return runtime.ChatResult{ThreadID: thread, TaskID: task.ID, MessageID: m.ID, Queued: true}, nil
}

func (f *fakeRT) Active(botID string) (string, string) {
	f.mu.Lock()
	id := f.active[botID]
	f.mu.Unlock()
	if id == "" {
		return "", ""
	}
	t, err := f.db.GetTask(context.Background(), id)
	if err != nil {
		return "", ""
	}
	return t.ThreadID, t.ID
}

func (f *fakeRT) StopTask(ctx context.Context, id string) (bool, error) {
	t, err := f.db.GetTask(ctx, id)
	if err != nil || t.State == store.TaskDone {
		return false, err
	}
	if t.State == store.TaskQueued {
		if ok, err := f.db.SetTaskCancelledIfQueued(ctx, id); !ok || err != nil {
			return false, err
		}
		t, _ = f.db.GetTask(ctx, id)
		f.hooks.Finished(t, runtime.VerdictCancelled, "")
		return true, nil
	}
	return true, f.db.SetTaskState(ctx, id, store.TaskStopping, "")
}

func (f *fakeRT) SetHooks(h runtime.Hooks) { f.hooks = h }
func (f *fakeRT) Bus() *events.Bus         { return f.bus }

// team 是三個 Bot 的測試環境（LazyBoy tests/test_collab.py team）。
type team struct {
	t     *testing.T
	ctx   context.Context
	db    *store.DB
	rt    *fakeRT
	s     *Service
	files DirFiles
	bots  map[string]string // 名字 → id
	main  map[string]string // id → 自己的主要對話串
}

func setup(t *testing.T) *team {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rt := &fakeRT{db: db, bus: events.New(), active: map[string]string{}}
	tm := &team{t: t, ctx: ctx, db: db, rt: rt, files: DirFiles{Root: filepath.Join(dir, "bots")}, bots: map[string]string{}, main: map[string]string{}}
	tm.s = New(Options{DB: db, Runtime: rt, Files: tm.files})
	for _, b := range [][2]string{{"Chief", "總管"}, {"Writer", "文案"}, {"Scout", "研究"}} {
		bot, err := db.CreateBot(ctx, store.Bot{Name: b[0], Title: b[1], Description: b[0] + " 的專長"})
		if err != nil {
			t.Fatal(err)
		}
		tm.bots[b[0]] = bot.ID
		th, err := db.CreateThread(ctx, store.Thread{BotID: bot.ID, Title: "主要"})
		if err != nil {
			t.Fatal(err)
		}
		tm.main[bot.ID] = th.ID
	}
	return tm
}

// running 假裝 bot 正在做一件你交辦的工作（或帶著 meta 的工作），回傳工作 id（LazyBoy running）。
func (tm *team) running(bot string, meta map[string]any) string {
	tm.t.Helper()
	raw, _ := json.Marshal(meta)
	task, err := tm.db.CreateTask(tm.ctx, store.Task{BotID: bot, ThreadID: tm.main[bot], Source: "owner", State: store.TaskRunning, Input: "x", Meta: raw})
	if err != nil {
		tm.t.Fatal(err)
	}
	tm.rt.mu.Lock()
	tm.rt.active[bot] = task.ID
	tm.rt.mu.Unlock()
	return task.ID
}

// start 讓排隊中的工作開始（runtime 會先組 prompt），回傳送給引擎的文字。
func (tm *team) start(task store.Task) string {
	tm.t.Helper()
	if err := tm.db.SetTaskState(tm.ctx, task.ID, store.TaskRunning, ""); err != nil {
		tm.t.Fatal(err)
	}
	tm.rt.mu.Lock()
	tm.rt.active[task.BotID] = task.ID
	tm.rt.mu.Unlock()
	p, ok, err := tm.s.prompt(tm.ctx, task)
	if err != nil {
		tm.t.Fatal(err)
	}
	if !ok {
		p = task.Input
	}
	return p
}

// finish 結束一輪（同 runtime.finish：寫回覆、標記 [silent]、呼叫協作層）。
func (tm *team) finish(taskID, verdict, text string) {
	tm.t.Helper()
	task := tm.task(taskID)
	meta := map[string]any{"task_id": taskID}
	if tm.s.silent(taskID, verdict, text) {
		meta["silent"] = true
	}
	raw, _ := json.Marshal(meta)
	if _, err := tm.db.AddMessage(tm.ctx, store.Message{Scope: store.ScopeThread, ScopeID: task.ThreadID, Sender: "bot:" + task.BotID, Content: text, Meta: raw}); err != nil {
		tm.t.Fatal(err)
	}
	if err := tm.db.SetTaskState(tm.ctx, taskID, store.TaskDone, verdict); err != nil {
		tm.t.Fatal(err)
	}
	tm.rt.mu.Lock()
	if tm.rt.active[task.BotID] == taskID {
		delete(tm.rt.active, task.BotID)
	}
	tm.rt.mu.Unlock()
	tm.s.finished(tm.task(taskID), verdict, text)
}

func (tm *team) task(id string) store.Task {
	tm.t.Helper()
	t, err := tm.db.GetTask(tm.ctx, id)
	if err != nil {
		tm.t.Fatal(err)
	}
	return t
}

func (tm *team) queued(bot string) []store.Task {
	tm.t.Helper()
	tasks, err := tm.db.ListTasks(tm.ctx, bot, true)
	if err != nil {
		tm.t.Fatal(err)
	}
	var out []store.Task
	for _, t := range tasks {
		if t.State == store.TaskQueued {
			out = append(out, t)
		}
	}
	return out
}

// results 是 bot 收到的「回覆」工作（intent=result）。
func (tm *team) results(bot string) []store.Task {
	tm.t.Helper()
	tasks, err := tm.db.ListTasks(tm.ctx, bot, false)
	if err != nil {
		tm.t.Fatal(err)
	}
	var out []store.Task
	for _, t := range tasks {
		if metaOf(t).Intent == IntentResult {
			out = append(out, t)
		}
	}
	return out
}

func (tm *team) transcript(thread string) []store.Message {
	tm.t.Helper()
	msgs, err := tm.db.ListMessages(tm.ctx, store.ScopeThread, thread, 0, 0)
	if err != nil {
		tm.t.Fatal(err)
	}
	return msgs
}

func (tm *team) last(thread string) store.Message {
	tm.t.Helper()
	msgs := tm.transcript(thread)
	if len(msgs) == 0 {
		tm.t.Fatal("對話串是空的")
	}
	return msgs[len(msgs)-1]
}

func (tm *team) roomLog(roomID string) []store.Message {
	tm.t.Helper()
	msgs, err := tm.db.ListMessages(tm.ctx, store.ScopeRoom, roomID, 0, 0)
	if err != nil {
		tm.t.Fatal(err)
	}
	return msgs
}

func (tm *team) write(bot, rel, content string) string {
	tm.t.Helper()
	p := filepath.Join(tm.files.Dir(bot), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tm.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tm.t.Fatal(err)
	}
	return p
}

func (tm *team) read(bot, rel string) string {
	tm.t.Helper()
	b, err := os.ReadFile(filepath.Join(tm.files.Dir(bot), filepath.FromSlash(rel)))
	if err != nil {
		tm.t.Fatal(err)
	}
	return string(b)
}

func peerOf(m store.Message) PeerEvent {
	var meta struct {
		Peer PeerEvent `json:"peer"`
	}
	_ = json.Unmarshal(m.Meta, &meta)
	return meta.Peer
}

func roomMetaOf(m store.Message) roomMeta {
	var rm roomMeta
	_ = json.Unmarshal(m.Meta, &rm)
	return rm
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}
