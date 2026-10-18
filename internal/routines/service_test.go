package routines

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

var bg = context.Background()

// fakeRT 是假的 runtime：交辦時建立對話串與工作，Finish 結束工作。
type fakeRT struct {
	db *store.DB

	mu      sync.Mutex
	reqs    []runtime.ChatRequest
	fail    error
	active  map[string]string // botID → taskID
	done    map[string]chan struct{}
	onStart func(req runtime.ChatRequest)
}

func newFakeRT(db *store.DB) *fakeRT {
	return &fakeRT{db: db, active: map[string]string{}, done: map[string]chan struct{}{}}
}

func (f *fakeRT) Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	fail, hook := f.fail, f.onStart
	f.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	if fail != nil {
		return runtime.ChatResult{}, fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, busy := f.active[req.BotID]; busy {
		return runtime.ChatResult{}, runtime.ErrBusy
	}
	threadID := req.ThreadID
	if threadID == "" {
		th, err := f.db.CreateThread(ctx, store.Thread{BotID: req.BotID, Title: "t"})
		if err != nil {
			return runtime.ChatResult{}, err
		}
		threadID = th.ID
	} else if _, err := f.db.GetThread(ctx, threadID); err != nil {
		return runtime.ChatResult{}, err
	}
	task, err := f.db.CreateTask(ctx, store.Task{BotID: req.BotID, ThreadID: threadID, Source: req.Source, Input: req.Text})
	if err != nil {
		return runtime.ChatResult{}, err
	}
	f.active[req.BotID] = task.ID
	f.done[task.ID] = make(chan struct{})
	return runtime.ChatResult{ThreadID: threadID, TaskID: task.ID}, nil
}

func (f *fakeRT) Active(botID string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "", f.active[botID]
}

func (f *fakeRT) Wait(ctx context.Context, taskID string) (store.Task, error) {
	f.mu.Lock()
	ch := f.done[taskID]
	f.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return store.Task{}, ctx.Err()
		}
	}
	return f.db.GetTask(ctx, taskID)
}

// Finish 結束一件工作（verdict：answer、failed、cancelled）。
func (f *fakeRT) Finish(t *testing.T, taskID, verdict string) {
	t.Helper()
	if err := f.db.SetTaskState(bg, taskID, store.TaskDone, verdict); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	for b, id := range f.active {
		if id == taskID {
			delete(f.active, b)
		}
	}
	ch := f.done[taskID]
	delete(f.done, taskID)
	f.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (f *fakeRT) calls() []runtime.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.ChatRequest(nil), f.reqs...)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	db    *store.DB
	rt    *fakeRT
	clock *fakeClock
	svc   *Service
	reg   *ops.Registry
	bot   store.Bot
	evs   []store.Event
	evMu  sync.Mutex
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, rt: newFakeRT(db), clock: &fakeClock{t: tue0900}}
	e.svc = New(Options{DB: db, Runtime: e.rt, Now: e.clock.Now, Rand: func(int64) int64 { return 0 },
		Notify: func(ev store.Event) { e.evMu.Lock(); e.evs = append(e.evs, ev); e.evMu.Unlock() }})
	t.Cleanup(e.svc.Close)
	e.reg = ops.New(ops.Config{DB: db})
	Register(e.reg, e.svc)
	if e.bot, err = db.CreateBot(bg, store.Bot{Name: "小秘", Engine: "hermes", IsChief: true}); err != nil {
		t.Fatal(err)
	}
	return e
}

var owner = ops.Owner("test")

func (e *env) call(id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(bg, id, op, raw, o)
	return res.Output, err
}

func get[T any](t *testing.T, e *env, id ops.Identity, op string, in any, opts ...ops.Options) T {
	t.Helper()
	raw, err := e.call(id, op, in, opts...)
	if err != nil {
		t.Fatalf("%s：%v", op, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func message(err error) string {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Message
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

var weekly = Schedule{Kind: KindWeekly, Time: "09:00", Timezone: "Asia/Taipei", Weekdays: []int{1}}

func (e *env) create(t *testing.T, in CreateIn) View {
	t.Helper()
	if in.BotID == "" {
		in.BotID = e.bot.ID
	}
	if in.Title == "" {
		in.Title = "週報"
	}
	if in.Prompt == "" {
		in.Prompt = "整理數據"
	}
	if in.Schedule == nil {
		in.Schedule = &weekly
	}
	out := get[CreateOut](t, e, owner, "xbot.routines.create", in)
	if out.Routine == nil {
		t.Fatalf("沒有建立：%+v", out)
	}
	return *out.Routine
}

func (e *env) routine(t *testing.T, id string) store.Routine {
	t.Helper()
	r, err := e.db.GetRoutine(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) patch(t *testing.T, id string, fn func(r *store.Routine)) {
	t.Helper()
	r := e.routine(t, id)
	fn(&r)
	if _, err := e.db.UpdateRoutine(bg, r); err != nil {
		t.Fatal(err)
	}
}

func (e *env) runs(t *testing.T, id string) []store.RoutineRun {
	t.Helper()
	runs, err := e.db.ListRoutineRuns(bg, id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func (e *env) inbox(t *testing.T, kind string) []store.InboxItem {
	t.Helper()
	items, err := e.db.ListInboxFiltered(bg, store.InboxFilter{Kinds: []string{kind}})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// waitFor 等背景的結果寫回（watch 是 goroutine）。
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("等不到：%s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var past = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

// LazyBoy test_scheduler_restart_catches_up_once：錯過很多次也只補跑一次；
// 交辦後、往後排之前重開，同一次不會再跑。
func TestRestartCatchesUpOnceAndNeverDoubleRuns(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = past })
	e.svc.Tick(bg)
	calls := e.rt.calls()
	if len(calls) != 1 {
		t.Fatalf("補跑一次：%d", len(calls))
	}
	runs := e.runs(t, r.ID)
	if len(runs) != 1 || runs[0].Trigger != store.TriggerCatchup || runs[0].Status != store.RunRunning || runs[0].TaskID == "" {
		t.Fatalf("執行紀錄：%+v", runs)
	}
	got := e.routine(t, r.ID)
	if got.NextRunAt <= e.clock.Now().UnixMilli() || got.LastTaskID != runs[0].TaskID || got.ThreadID == "" {
		t.Fatalf("要排到以後：%+v", got)
	}
	// 模擬「交辦了、還沒往後排」就重開：同一個 next_run_at 又到期。
	e.rt.Finish(t, runs[0].TaskID, runtime.VerdictAnswer)
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = past })
	restarted := New(Options{DB: e.db, Runtime: e.rt, Now: e.clock.Now})
	defer restarted.Close()
	restarted.Tick(bg)
	if n := len(e.rt.calls()); n != 1 {
		t.Fatalf("同一次不能跑兩遍：%d", n)
	}
	if got := e.routine(t, r.ID); got.NextRunAt <= e.clock.Now().UnixMilli() {
		t.Fatalf("重開後要往後排：%+v", got)
	}
	// 再過一天：正常排程（不是補跑）。
	next := e.routine(t, r.ID).NextRunAt
	e.clock.Set(time.UnixMilli(next))
	e.svc.Tick(bg)
	if runs := e.runs(t, r.ID); len(runs) != 2 || runs[0].Trigger != store.TriggerSchedule {
		t.Fatalf("隔天：%+v", runs)
	}
}

// LazyBoy test_failed_admission_backs_off_and_keeps_the_occurrence_key
func TestFailedAdmissionBacksOffAndKeepsTheOccurrence(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	due := e.clock.Now().Add(-time.Second).UnixMilli()
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = due })
	e.rt.fail = errors.New("電腦啟動失敗")
	for range 5 {
		e.svc.Tick(bg)
	}
	if n := len(e.rt.calls()); n != 1 {
		t.Fatalf("退避中不能每次都重試：%d", n)
	}
	got := e.routine(t, r.ID)
	if got.Failures != 1 || got.NextRunAt != due || !strings.Contains(got.LastError, "電腦啟動失敗") ||
		got.RetryAt != e.clock.Now().Add(time.Minute).UnixMilli() {
		t.Fatalf("第一次失敗退避 1 分鐘：%+v", got)
	}
	if runs := e.runs(t, r.ID); len(runs) != 1 || runs[0].Status != store.RunRetrying {
		t.Fatalf("執行紀錄：%+v", runs)
	}
	// 1／5／15／60 分鐘，之後都是 60。
	for i, want := range []time.Duration{5 * time.Minute, 15 * time.Minute, 60 * time.Minute, 60 * time.Minute} {
		e.clock.Set(time.UnixMilli(e.routine(t, r.ID).RetryAt))
		e.svc.Tick(bg)
		got := e.routine(t, r.ID)
		if got.Failures != i+2 || got.RetryAt != e.clock.Now().Add(want).UnixMilli() {
			t.Fatalf("第 %d 次失敗：%+v", i+2, got)
		}
	}
	// 第 4 次失敗（重試 3 次）發一張卡，之後不再發。
	if cards := e.inbox(t, "routine_failed"); len(cards) != 1 || !strings.Contains(cards[0].Body, "已自動重試 3 次") || cards[0].Ref != r.ID {
		t.Fatalf("失敗卡：%+v", cards)
	}
	if len(e.evs) != 1 {
		t.Fatalf("卡片要推給介面：%d", len(e.evs))
	}
	// 恢復後：同一次（同一個去重鍵）交辦成功，清掉退避。
	e.rt.fail = nil
	e.clock.Set(time.UnixMilli(e.routine(t, r.ID).RetryAt))
	e.svc.Tick(bg)
	runs := e.runs(t, r.ID)
	if len(runs) != 1 || runs[0].Status != store.RunRunning || runs[0].Occurrence != "sched:"+itoa(due) {
		t.Fatalf("同一次：%+v", runs)
	}
	got = e.routine(t, r.ID)
	if got.Failures != 0 || got.RetryAt != 0 || got.LastError != "" || got.NextRunAt <= due {
		t.Fatalf("恢復後：%+v", got)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestBusyBotRetriesWithBackoff(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	e.rt.active[e.bot.ID] = "別的工作"
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = e.clock.Now().UnixMilli() })
	e.svc.Tick(bg)
	got := e.routine(t, r.ID)
	if got.Failures != 1 || got.RetryAt == 0 || !strings.Contains(got.LastError, "正在忙") {
		t.Fatalf("Bot 在忙：退避重試：%+v", got)
	}
}

func TestSkipsWhenThePreviousRunIsStillGoing(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindInterval, EveryMinutes: 5, Time: "00:00"}})
	e.clock.Set(time.UnixMilli(r.NextRunAt))
	e.svc.Tick(bg)
	first := e.runs(t, r.ID)[0]
	// 5 分鐘後上一次還在跑：略過這一次，排下一次。
	e.clock.Add(5 * time.Minute)
	e.svc.Tick(bg)
	runs := e.runs(t, r.ID)
	if len(e.rt.calls()) != 1 || len(runs) != 2 || runs[0].Status != store.RunSkipped || !strings.Contains(runs[0].Error, "還在跑") {
		t.Fatalf("上一次還在跑要略過：%+v", runs)
	}
	if got := e.routine(t, r.ID); got.NextRunAt != e.clock.Now().Add(5*time.Minute).UnixMilli() || got.Failures != 0 {
		t.Fatalf("略過後排下一次：%+v", got)
	}
	// 上一次做完：下一次照常跑，結果寫回。
	e.rt.Finish(t, first.TaskID, runtime.VerdictAnswer)
	waitFor(t, "第一次寫回完成", func() bool { r, _ := e.db.GetRoutineRun(bg, first.ID); return r.Status == store.RunDone })
	e.clock.Add(5 * time.Minute)
	e.svc.Tick(bg)
	if len(e.rt.calls()) != 2 {
		t.Fatal("上一次做完就照常跑")
	}
}

// LazyBoy test_finished_once_cannot_be_resumed_without_a_new_date
func TestFinishedOnceCannotBeResumedWithoutANewDate(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindOnce, Time: "09:00", Date: "2026-10-15"}})
	e.patch(t, r.ID, func(r *store.Routine) {
		r.Schedule = json.RawMessage(`{"kind":"once","time":"09:00","date":"2020-01-01","timezone":"Asia/Taipei"}`)
		r.NextRunAt = past
	})
	e.svc.Tick(bg)
	got := e.routine(t, r.ID)
	if got.Status != store.RoutineDone || got.NextRunAt != 0 {
		t.Fatalf("單次跑完就結束：%+v", got)
	}
	_, err := e.call(owner, "xbot.routines.resume", IDIn{ID: r.ID})
	if !strings.Contains(message(err), "單次排程的時間已經過了") {
		t.Fatalf("resume：%v", err)
	}
	// 換新日期就重新啟用。
	v := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, Schedule: &Schedule{Kind: KindOnce, Time: "09:00", Date: "2026-10-20"}})
	if v.Status != store.RoutineActive || v.NextRunAt == 0 {
		t.Fatalf("新日期：%+v", v)
	}
}

// LazyBoy test_routine_deleted_during_run_does_not_crash_tick
func TestRoutineDeletedDuringRunDoesNotCrashTick(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = past })
	e.rt.onStart = func(runtime.ChatRequest) { _ = e.db.DeleteRoutine(bg, r.ID) }
	e.svc.Tick(bg)
	if _, err := e.db.GetRoutine(bg, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("刪掉的不能被寫回來：%v", err)
	}
}

// LazyBoy test_routine_edited_during_run_keeps_new_settings
func TestRoutineEditedDuringRunKeepsNewSettings(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = past })
	e.rt.onStart = func(runtime.ChatRequest) {
		e.patch(t, r.ID, func(r *store.Routine) { r.Status = store.RoutinePaused; r.Title = "改過了" })
	}
	e.svc.Tick(bg)
	got := e.routine(t, r.ID)
	if got.Status != store.RoutinePaused || got.Title != "改過了" || got.LastTaskID == "" {
		t.Fatalf("交辦途中的修改要保留：%+v", got)
	}
}

// LazyBoy test_routine_without_frequency_advances_after_run
func TestRoutineWithoutKindAdvancesAfterRun(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{})
	e.patch(t, r.ID, func(r *store.Routine) {
		r.Schedule = json.RawMessage(`{"time":"09:00","timezone":"Asia/Taipei"}`)
		r.NextRunAt = past
	})
	e.svc.Tick(bg)
	if got := e.routine(t, r.ID); got.NextRunAt <= e.clock.Now().UnixMilli() || got.Status != store.RoutineActive {
		t.Fatalf("沒有 kind 的當每天：%+v", got)
	}
}

// LazyBoy test_routine_run_tells_the_bot_its_instructions_are_the_permission、
// test_routine_messages_are_labelled_in_the_transcript
func TestRunFramesThePromptButShowsOnlyTheInstructions(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Title: "Threads 海巡", Prompt: "每天回 3 則求推薦貼文", Schedule: &Schedule{Kind: KindDaily, Time: "20:00"}})
	e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = past })
	e.svc.Tick(bg)
	req := e.rt.calls()[0]
	if req.Text != "每天回 3 則求推薦貼文" || req.Source != "routine" {
		t.Fatalf("對話裡只顯示指示：%+v", req)
	}
	if !strings.Contains(req.Prompt, "定時任務「Threads 海巡」時間到了") || !strings.Contains(req.Prompt, "就是使用者的授權") ||
		!strings.HasSuffix(req.Prompt, req.Text) {
		t.Fatalf("送給引擎的要說明使用者不在：%s", req.Prompt)
	}
	if req.MessageMeta["routine_id"] != r.ID || req.MessageMeta["routine_title"] != "Threads 海巡" || req.MessageMeta["trigger"] != store.TriggerCatchup {
		t.Fatalf("訊息要標示定時任務：%+v", req.MessageMeta)
	}
	// 結果貼到同一條對話串。
	e.rt.Finish(t, e.routine(t, r.ID).LastTaskID, runtime.VerdictAnswer)
	thread := e.routine(t, r.ID).ThreadID
	get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: r.ID})
	if calls := e.rt.calls(); calls[1].ThreadID != thread || calls[1].MessageMeta["trigger"] != store.TriggerManual {
		t.Fatalf("同一條對話串：%+v", calls[1])
	}
}

func TestFailedRunPostsACardUnlessTurnedOff(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	off := false
	quiet := e.create(t, CreateIn{Title: "安靜", Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}, NotifyOnFailure: &off})
	for _, id := range []string{r.ID, quiet.ID} {
		out := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: id})
		if out.Run.Status != store.RunRunning || out.Run.Trigger != store.TriggerManual {
			t.Fatalf("手動：%+v", out)
		}
		e.rt.Finish(t, out.Run.TaskID, runtime.VerdictFailed)
		waitFor(t, "寫回失敗", func() bool { run, _ := e.db.GetRoutineRun(bg, out.Run.ID); return run.Status == store.RunFailed })
	}
	cards := e.inbox(t, "routine_failed")
	if len(cards) != 1 || cards[0].Ref != r.ID || cards[0].Status != store.InboxOpen || !strings.Contains(cards[0].Title, "週報") {
		t.Fatalf("失敗卡：%+v", cards)
	}
	var p map[string]any
	_ = json.Unmarshal(cards[0].Payload, &p)
	if p["routine_id"] != r.ID || p["run_id"] == "" {
		t.Fatalf("卡片要帶定時任務（重跑一次用）：%v", p)
	}
}

func TestReconcileAfterRestart(t *testing.T) {
	e := setup(t)
	a := e.create(t, CreateIn{Title: "A", Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	ra := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: a.ID}).Run
	// 服務關掉：等結果的背景工作停了，工作在關機時做完（或沒做完）。
	e.svc.Close()
	if err := e.db.SetTaskState(bg, ra.TaskID, store.TaskDone, runtime.VerdictAnswer); err != nil {
		t.Fatal(err)
	}
	b := e.bot
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "阿寶", Engine: "hermes"})
	e.bot = other
	bRoutine := e.create(t, CreateIn{Title: "B", Schedule: &Schedule{Kind: KindDaily, Time: "09:00"}})
	e.bot = b
	rb := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: bRoutine.ID}).Run
	// 重開：runtime 是新的，B 的工作不在跑了。
	rt := newFakeRT(e.db)
	svc := New(Options{DB: e.db, Runtime: rt, Now: e.clock.Now})
	defer svc.Close()
	svc.Reconcile(bg)
	if got, _ := e.db.GetRoutineRun(bg, ra.ID); got.Status != store.RunDone {
		t.Fatalf("關機時做完的寫回完成：%+v", got)
	}
	if got, _ := e.db.GetRoutineRun(bg, rb.ID); got.Status != store.RunFailed || !strings.Contains(got.Error, "重新啟動") {
		t.Fatalf("重開時中斷的標失敗：%+v", got)
	}
	if len(e.inbox(t, "routine_failed")) != 0 {
		t.Fatal("重開不是定時任務的問題，不發卡")
	}
}

// LazyBoy test_a_goal_routine_tracks_its_rounds_and_ends_itself_with_a_report
func TestGoalRoutineTracksRoundsAndEndsItself(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{Title: "練查證方法", Prompt: "練習交叉查證", Schedule: &Schedule{Kind: KindDaily, Time: "09:00"},
		Goal: &GoalIn{DoneWhen: "連續兩輪沒有新的錯誤", MaxRounds: 3}})
	if r.Goal == nil || r.Goal.MaxRounds != 3 || r.Goal.Rounds != 0 || r.Goal.ProgressFile != "/workspace/goals/"+r.ID[:8]+".md" {
		t.Fatalf("目標：%+v", r.Goal)
	}
	for round := 1; round <= 3; round++ {
		e.patch(t, r.ID, func(r *store.Routine) { r.NextRunAt = e.clock.Now().UnixMilli() })
		e.svc.Tick(bg)
		req := e.rt.calls()[round-1]
		if !strings.Contains(req.Prompt, "第 "+itoa(int64(round))+" 輪，最多 3 輪") || !strings.Contains(req.Prompt, r.Goal.ProgressFile) {
			t.Fatalf("第 %d 輪的說明：%s", round, req.Prompt)
		}
		if round == 3 && !strings.Contains(req.Prompt, "這是最後一輪") {
			t.Fatal("最後一輪要說")
		}
		e.rt.Finish(t, e.routine(t, r.ID).LastTaskID, runtime.VerdictAnswer)
		e.clock.Add(24 * time.Hour)
	}
	got := e.routine(t, r.ID)
	if g := GoalOf(got); g.Rounds != 3 || got.Status != store.RoutineDone {
		t.Fatalf("用完輪數自動停用：%+v %+v", g, got)
	}
	if _, err := e.call(owner, "xbot.routines.resume", IDIn{ID: r.ID}); !strings.Contains(message(err), "已經結束") {
		t.Fatalf("resume：%v", err)
	}
	// Bot 用 complete 收尾：要寫成果。
	bot := ops.Bot(e.bot.ID, e.bot.Name)
	if _, err := e.call(bot, "xbot.routines.complete", CompleteIn{ID: r.ID, Summary: " "}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("沒寫成果：%v", err)
	}
	done := get[View](t, e, bot, "xbot.routines.complete", CompleteIn{ID: r.ID, Summary: "查證改成兩個獨立來源，已寫進技能"})
	if done.Goal.CompletedAt == 0 || done.Goal.Summary == "" || done.Status != store.RoutineDone {
		t.Fatalf("完成：%+v", done)
	}
	msgs, _ := e.db.ListMessages(bg, store.ScopeThread, done.ThreadID, 0, 100)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "查證改成兩個獨立來源") || last.Kind != "card" {
		t.Fatalf("在對話裡說一聲：%+v", last)
	}
	if _, err := e.call(owner, "xbot.routines.run_now", IDIn{ID: r.ID}); !strings.Contains(message(err), "已經完成") {
		t.Fatalf("完成的目標不能再跑：%v", err)
	}
	// 換新的完成條件：從頭開始。
	again := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, Goal: &GoalIn{DoneWhen: "三輪沒有錯誤"}})
	if again.Goal.Rounds != 0 || again.Goal.CompletedAt != 0 || again.Status != store.RoutineActive || again.Goal.MaxRounds != 3 {
		t.Fatalf("新的完成條件：%+v", again)
	}
	// 一般定時任務不能 complete；拿掉目標。
	plain := e.create(t, CreateIn{Title: "日報", Prompt: "寫日報", Schedule: &Schedule{Kind: KindDaily, Time: "18:00"}})
	if _, err := e.call(owner, "xbot.routines.complete", CompleteIn{ID: plain.ID, Summary: "x"}); !strings.Contains(message(err), "沒有設定目標") {
		t.Fatalf("一般的：%v", err)
	}
	cleared := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, ClearGoal: true})
	if cleared.Goal != nil {
		t.Fatal("拿掉目標")
	}
	if _, err := e.call(owner, "xbot.routines.create", CreateIn{BotID: e.bot.ID, Prompt: "x", Schedule: &weekly, Goal: &GoalIn{DoneWhen: "y", MaxRounds: 51}}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("最多 50 輪：%v", err)
	}
}
