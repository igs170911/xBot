package runtime

// X05 快判接到 runtime 的兩個用途：對話串命名（§20 用途 10）、完成通知的一行摘要（用途 8）。

import (
	"context"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

func TestFastJudgeNamesThreadAndSummarizesNotice(t *testing.T) {
	ctx := context.Background()
	named := make(chan struct{}, 1)
	f := setupWith(t, func(o *Options) {
		o.Namer = func(_ context.Context, text, botID string) (string, bool) {
			defer func() { named <- struct{}{} }()
			return "台北東京機票比價", text != "" && botID != ""
		}
		o.Summarize = func(_ context.Context, text, taskID, botID string) (string, bool) {
			return "查好了：週二最便宜", text != "" && taskID != "" && botID != ""
		}
	})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("我查好了。最便宜是週二，3,280 元。\n\n細節如下…", "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我查這週台北飛東京最便宜的是哪一天，要含稅價")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	select {
	case <-named:
	case <-time.After(5 * time.Second):
		t.Fatal("沒有請快判命名")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		th, _ := f.db.GetThread(ctx, res.ThreadID)
		items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID})
		if th.Title == "台北東京機票比價" && len(items) == 1 && items[0].Body == "查好了：週二最便宜" && items[0].Kind == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("標題 %q、通知 %+v", th.Title, items)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 在既有對話串裡說話不命名。
	res2, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "再查一下回程")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res2.TaskID)
	select {
	case <-named:
		t.Fatal("既有對話串不用再命名")
	case <-time.After(200 * time.Millisecond):
	}
}
