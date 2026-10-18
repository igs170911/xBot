package events

import (
	"context"
	"testing"

	"xbot/internal/store"
)

// W02 待確認：斷線續傳補送的事件也要帶 thread_id（畫面靠它把事件放回對的對話串）。
func TestReplayKeepsThreadID(t *testing.T) {
	b, db := New(), newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bot, err := db.CreateBot(ctx, store.Bot{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	th, err := db.CreateThread(ctx, store.Thread{BotID: bot.ID})
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.AppendEvent(ctx, store.Event{BotID: bot.ID, ThreadID: th.ID, TaskID: "t1", Kind: "chat.tool_call"})
	if err != nil {
		t.Fatal(err)
	}
	ch := Stream(ctx, b, db, nil, 0, nil)
	if got := recv(t, ch); got.ID != row.ID || got.ThreadID != th.ID || got.TaskID != "t1" {
		t.Fatalf("補送的事件：%+v", got)
	}
}
