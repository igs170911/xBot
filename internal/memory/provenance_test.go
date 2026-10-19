package memory

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"xbot/internal/boxfs"
	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/store"
)

// 2026-10-19：Bot 自己用記憶工具記的事，要不等你改過一次就列進最近的變更，並記下在哪條對話串、什麼時候記的；
// 連續記兩次是兩筆，不會等打開記憶頁才併成一筆。
func TestBotOwnMemoryWritesAreRecordedWithThread(t *testing.T) {
	e := setup(t)
	th, err := e.db.CreateThread(ctx, store.Thread{BotID: e.bot.ID, Title: "整理客戶名單"})
	if err != nil {
		t.Fatal(err)
	}
	m := boxfs.NewMem()
	e.fs[e.bot.ID] = m
	svc := &Service{DB: e.db, Locate: func(context.Context, string, bool) (boxfs.FS, error) { return m, nil }}
	m.Files[Dir+"/MEMORY.md"] = []byte("xbot 工具沒有刪除 Bot 的能力")
	if err := svc.Notice(ctx, e.bot.ID, th.ID, 1000); err != nil {
		t.Fatal(err)
	}
	m.Files[Dir+"/MEMORY.md"] = []byte("xbot 工具沒有刪除 Bot 的能力\n§\n客戶 A 用月結")
	if err := svc.Notice(ctx, e.bot.ID, th.ID, 2000); err != nil {
		t.Fatal(err)
	}
	var h HistoryOut
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID}, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Changes) != 2 {
		t.Fatalf("兩次各一筆：%+v", h.Changes)
	}
	first, second := h.Changes[1], h.Changes[0]
	if first.Actor != "bot:"+e.bot.ID || !slices.Equal(first.Added, []string{"xbot 工具沒有刪除 Bot 的能力"}) || len(first.Removed) != 0 ||
		first.CreatedAt != 1000 || first.ThreadID != th.ID || first.ThreadTitle != "整理客戶名單" || first.Undoable {
		t.Fatalf("第一筆從空白算起：%+v", first)
	}
	if !slices.Equal(second.Added, []string{"客戶 A 用月結"}) || second.CreatedAt != 2000 || second.ThreadID != th.ID || !second.Undoable {
		t.Fatalf("%+v", second)
	}
	// 你拿掉 Bot 記錯的那條：你的修改沒有對話串。
	if err := e.call(t, owner, "xbot.memory.write", WriteIn{BotID: e.bot.ID, Target: TargetMemory, Entries: []string{"客戶 A 用月結"}}, nil); err != nil {
		t.Fatal(err)
	}
	h = HistoryOut{} // json.Unmarshal 會沿用舊的元素，省略的欄位不會清掉
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID}, &h); err != nil || len(h.Changes) != 3 ||
		h.Changes[0].Actor != "owner" || h.Changes[0].ThreadID != "" || !slices.Equal(h.Changes[0].Removed, []string{"xbot 工具沒有刪除 Bot 的能力"}) {
		t.Fatalf("%+v %v", h.Changes, err)
	}
	// 對話串刪掉了：不給標題，也不給連結。
	if err := e.db.DeleteThread(ctx, th.ID); err != nil {
		t.Fatal(err)
	}
	h = HistoryOut{}
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID}, &h); err != nil || h.Changes[2].ThreadID != "" || h.Changes[2].ThreadTitle != "" {
		t.Fatalf("%+v %v", h.Changes, err)
	}
}

// 從來沒有紀錄、Bot 在電腦裡已經記了東西：打開記憶頁時也記成 Bot 新增的一筆，不再看不到。
func TestFirstReadRecordsWhatTheBotAlreadyWrote(t *testing.T) {
	e := setup(t)
	m := boxfs.NewMem()
	e.fs[e.bot.ID] = m
	m.Files[Dir+"/USER.md"] = []byte("喜歡簡短回答")
	var h HistoryOut
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID}, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Changes) != 1 || h.Changes[0].Target != TargetUser || h.Changes[0].Actor != "bot:"+e.bot.ID ||
		!slices.Equal(h.Changes[0].Added, []string{"喜歡簡短回答"}) || h.Changes[0].ThreadID != "" {
		t.Fatalf("%+v", h.Changes)
	}
	// 只調整順序：也記一筆，新增、拿掉都是空的。
	m.Files[Dir+"/USER.md"] = []byte("喜歡簡短回答\n§\n住在台北")
	if err := e.call(t, owner, "xbot.memory.read", BotIn{BotID: e.bot.ID}, nil); err != nil {
		t.Fatal(err)
	}
	m.Files[Dir+"/USER.md"] = []byte("住在台北\n§\n喜歡簡短回答")
	h = HistoryOut{}
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID, Target: TargetUser}, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Changes) != 3 || len(h.Changes[0].Added) != 0 || len(h.Changes[0].Removed) != 0 || !h.Changes[0].Undoable {
		t.Fatalf("%+v", h.Changes)
	}
}

// Bot 用 xbot.memory.add 記的：記下它正在做事的那條對話串。
func TestAddRecordsActiveThread(t *testing.T) {
	e := setup(t)
	th, err := e.db.CreateThread(ctx, store.Thread{BotID: e.bot.ID, Title: "週報"})
	if err != nil {
		t.Fatal(err)
	}
	m := boxfs.NewMem()
	svc := &Service{DB: e.db, Active: func(string) (string, string) { return th.ID, "task1" },
		Locate: func(context.Context, string, bool) (boxfs.FS, error) { return m, nil }}
	if _, err := svc.Add(ctx, e.db, e.bot.ID, TargetMemory, "週一開會", "bot:"+e.bot.ID); err != nil {
		t.Fatal(err)
	}
	h, err := svc.History(ctx, e.db, e.bot.ID, "")
	if err != nil || len(h.Changes) != 1 || h.Changes[0].ThreadID != th.ID || h.Changes[0].ThreadTitle != "週報" {
		t.Fatalf("%+v %v", h.Changes, err)
	}
}

// Watch：只有記憶工具成功跑完才去讀記憶檔，記下那個事件的對話串與時間。
func TestWatchRecordsMemoryToolResults(t *testing.T) {
	e := setup(t)
	var mu sync.Mutex
	files := map[string]*boxfs.Mem{}
	svc := &Service{DB: e.db, Locate: func(_ context.Context, botID string, _ bool) (boxfs.FS, error) {
		mu.Lock()
		defer mu.Unlock()
		if m, ok := files[botID]; ok {
			return m, nil
		}
		return nil, boxfs.ErrNoComputer
	}}
	m := boxfs.NewMem()
	m.Files[Dir+"/MEMORY.md"] = []byte("週一開會")
	mu.Lock()
	files[e.bot.ID] = m
	mu.Unlock()

	bus := events.New()
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		svc.Watch(wctx, bus, "chat.tool_result")
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	tool := func(name string, ok bool) json.RawMessage {
		raw, _ := json.Marshal(engine.Event{Type: engine.EventToolResult, Tool: &engine.ToolEvent{ID: "t", Name: name, OK: ok}})
		return raw
	}
	// Watch 在背景訂閱：一輪一輪送，直到記下來。別的工具、失敗的記憶工具、工具開始都不算，所以一定是 th1 那筆。
	deadline := time.Now().Add(5 * time.Second)
	for {
		bus.Publish(events.Event{Kind: "chat.tool_result", BotID: "nobody", Payload: tool(ToolName, true)}) // 沒有電腦：不做事
		bus.Publish(events.Event{Kind: "chat.tool_result", BotID: e.bot.ID, ThreadID: "th-other", Payload: tool("terminal", true), At: 10})
		bus.Publish(events.Event{Kind: "chat.tool_result", BotID: e.bot.ID, ThreadID: "th-fail", Payload: tool(ToolName, false), At: 20})
		bus.Publish(events.Event{Kind: "chat.tool_call", BotID: e.bot.ID, ThreadID: "th-call", Payload: tool(ToolName, true), At: 30})
		bus.Publish(events.Event{Kind: "chat.tool_result", BotID: e.bot.ID, ThreadID: "th1", Payload: tool(ToolName, true), At: 40})
		vs, err := e.db.ListMemoryVersions(ctx, e.bot.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) > 0 {
			if len(vs) != 1 || vs[0].ThreadID != "th1" || vs[0].CreatedAt != 40 || vs[0].After != "週一開會" || vs[0].Actor != "bot:"+e.bot.ID {
				t.Fatalf("%+v", vs)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("沒有記下 Bot 的記憶變更")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
