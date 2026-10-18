package org

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/collab"
	"xbot/internal/events"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// fakeRT 是不會真的執行的 runtime（同 LazyBoy 測試的 runtime.closed=True）：記下送給 Bot 的每一段話。
type fakeRT struct {
	db     *store.DB
	bus    *events.Bus
	mu     sync.Mutex
	active map[string]string // botID → taskID
	sent   []runtime.ChatRequest
	refuse error
	// onStart 在記下之後呼叫（測試用來模擬「送的同時你回覆了另一件」）。
	onStart func(req runtime.ChatRequest)
}

func (f *fakeRT) Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error) {
	if f.refuse != nil {
		return runtime.ChatResult{}, f.refuse
	}
	f.mu.Lock()
	f.sent = append(f.sent, req)
	hook := f.onStart
	f.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	return runtime.ChatResult{ThreadID: req.ThreadID, Queued: true}, nil
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

func (f *fakeRT) StopTask(context.Context, string) (bool, error) { return false, nil }
func (f *fakeRT) SetHooks(runtime.Hooks)                         {}
func (f *fakeRT) Bus() *events.Bus                               { return f.bus }

func (f *fakeRT) messages() []runtime.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.ChatRequest{}, f.sent...)
}

// company 是 LazyBoy tests/test_org.py 的 company：CEO 帶 Ops、Dev、QA。
type company struct {
	t     *testing.T
	ctx   context.Context
	db    *store.DB
	rt    *fakeRT
	c     *collab.Service
	s     *Service
	clock time.Time
	bots  map[string]string // 名字 → id
	main  map[string]string // id → 主要對話串
}

func setup(t *testing.T) *company {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rt := &fakeRT{db: db, bus: events.New(), active: map[string]string{}}
	co := &company{t: t, ctx: ctx, db: db, rt: rt, clock: time.UnixMilli(time.Now().UnixMilli()), bots: map[string]string{}, main: map[string]string{}}
	co.c = collab.New(collab.Options{DB: db, Runtime: rt})
	co.s = New(Options{DB: db, Runtime: rt, Collab: co.c, Now: func() time.Time { return co.clock }})
	for _, b := range [][2]string{{"CEO", "執行長"}, {"Ops", "營運"}, {"Dev", "研發"}, {"QA", "品質"}} {
		bot, err := db.CreateBot(ctx, store.Bot{Name: b[0], Title: b[1]})
		if err != nil {
			t.Fatal(err)
		}
		co.bots[b[0]] = bot.ID
		th, err := db.CreateThread(ctx, store.Thread{BotID: bot.ID, Title: "主要"})
		if err != nil {
			t.Fatal(err)
		}
		co.main[bot.ID] = th.ID
	}
	for _, n := range []string{"Ops", "Dev", "QA"} {
		co.setBoss(n, "CEO")
	}
	return co
}

func (co *company) setBoss(name, boss string) {
	co.t.Helper()
	b := co.bot(name)
	if err := co.db.SetBotOrg(co.ctx, b.ID, b.TeamID, co.bots[boss], "owner"); err != nil {
		co.t.Fatal(err)
	}
}

func (co *company) bot(name string) store.Bot {
	co.t.Helper()
	b, err := co.db.GetBot(co.ctx, co.bots[name])
	if err != nil {
		co.t.Fatal(err)
	}
	return b
}

func (co *company) manage(name string) {
	co.t.Helper()
	if err := co.db.SetBotCanManage(co.ctx, co.bots[name], true, "owner"); err != nil {
		co.t.Fatal(err)
	}
}

// task 新增一件工作；running 時當成 Bot 正在做的事（LazyBoy task）。
func (co *company) task(name, state string, meta map[string]any, input string) store.Task {
	co.t.Helper()
	id := co.bots[name]
	raw, _ := json.Marshal(meta)
	source := runtime.SourceOwner
	if meta["from_bot"] != nil {
		source = runtime.SourceBot
	}
	if meta["room_id"] != nil {
		source = runtime.SourceRoom
	}
	t, err := co.db.CreateTask(co.ctx, store.Task{BotID: id, ThreadID: co.main[id], Source: source, State: store.TaskQueued, Input: input, Meta: raw})
	if err != nil {
		co.t.Fatal(err)
	}
	if state != store.TaskQueued {
		verdict := ""
		if state == store.TaskDone {
			verdict = runtime.VerdictAnswer
		}
		if err := co.db.SetTaskState(co.ctx, t.ID, store.TaskRunning, ""); err != nil {
			co.t.Fatal(err)
		}
		if err := co.db.SetTaskState(co.ctx, t.ID, state, verdict); err != nil {
			co.t.Fatal(err)
		}
	}
	if state == store.TaskRunning {
		co.rt.mu.Lock()
		co.rt.active[id] = t.ID
		co.rt.mu.Unlock()
	}
	t, _ = co.db.GetTask(co.ctx, t.ID)
	return t
}

// reply 寫下 Bot 對一件工作的回覆。
func (co *company) reply(t store.Task, text string) {
	co.t.Helper()
	meta, _ := json.Marshal(map[string]string{"task_id": t.ID})
	if _, err := co.db.AddMessage(co.ctx, store.Message{Scope: store.ScopeThread, ScopeID: t.ThreadID, Sender: "bot:" + t.BotID, Content: text, Meta: meta}); err != nil {
		co.t.Fatal(err)
	}
}

func (co *company) last(thread string) store.Message {
	co.t.Helper()
	msgs, err := co.db.ListMessages(co.ctx, store.ScopeThread, thread, 0, 1)
	if err != nil || len(msgs) == 0 {
		co.t.Fatalf("對話是空的：%v", err)
	}
	return msgs[0]
}

func peerEvent(m store.Message) string {
	var meta struct {
		Peer struct {
			Event string `json:"event"`
		} `json:"peer"`
	}
	_ = json.Unmarshal(m.Meta, &meta)
	return meta.Peer.Event
}

func (co *company) agenda(id string) store.Agenda {
	co.t.Helper()
	a, err := co.db.GetAgenda(co.ctx, id)
	if err != nil {
		co.t.Fatal(err)
	}
	return a
}

func (co *company) pending() []store.Agenda {
	co.t.Helper()
	rows, err := co.db.ListAgenda(co.ctx, store.AgendaFilter{States: pending})
	if err != nil {
		co.t.Fatal(err)
	}
	return rows
}

func (co *company) setDue(id string, at time.Time) {
	co.t.Helper()
	a := co.agenda(id)
	if err := co.db.MoveAgenda(co.ctx, id, at.UnixMilli(), a.Grace); err != nil {
		co.t.Fatal(err)
	}
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

func errHas(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("錯誤要包含「%s」：%v", want, err)
	}
}

var errBusy = errors.New("busy")
