package runtime

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/store"
)

// B13：閒置休眠看 HasWork；休眠、重開、重建前 Quiesce 關掉引擎連線，之後的工作會重新連上（必要時開機）。
func TestHasWorkAndQuiesce(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	g := hermestest.New("k")
	t.Cleanup(g.Close)
	var connects atomic.Int32
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		connects.Add(1)
		return hermes.Endpoint{Addr: g.Addr(), Key: "k"}, nil
	}, nil)
	rt := New(Options{DB: db, Driver: d, Bus: events.New(), Models: fixedModel{}})
	bot, err := db.CreateBot(ctx, store.Bot{Name: "小幫手"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, g: g, rt: rt, bot: bot}

	if rt.HasWork(bot.ID) || !rt.Quiesce(bot.ID) {
		t.Fatal("沒有工作時可以休眠")
	}

	// 做事中：有工作，不能 Quiesce。
	started, release := make(chan struct{}), make(chan struct{})
	g.OnPrompt = func(tr *hermestest.Turn) {
		close(started)
		<-release
		tr.Complete("好了", "complete", nil)
	}
	res, err := rt.Chat(ctx, bot.ID, "", "做事")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !rt.HasWork(bot.ID) || rt.Quiesce(bot.ID) {
		t.Fatal("做事中不能休眠")
	}
	// 排隊中的工作也算。
	res2, err := rt.Start(ctx, ChatRequest{BotID: bot.ID, ThreadID: res.ThreadID, Text: "下一件", Queue: true})
	if err != nil || !res2.Queued {
		t.Fatalf("排隊：%+v %v", res2, err)
	}
	g.OnPrompt = func(tr *hermestest.Turn) { tr.Complete("也好了", "complete", nil) }
	close(release)
	f.wait(t, res.TaskID)
	f.wait(t, res2.TaskID)
	if rt.HasWork(bot.ID) {
		t.Fatal("做完了")
	}
	before := connects.Load()

	// Quiesce：關掉引擎連線；下一件工作重新連（電腦休眠了的話就是重新開機）。
	if !rt.Quiesce(bot.ID) {
		t.Fatal("沒有工作時可以休眠")
	}
	res3, err := rt.Chat(ctx, bot.ID, res.ThreadID, "再一件")
	if err != nil {
		t.Fatal(err)
	}
	if task := f.wait(t, res3.TaskID); task.Verdict != VerdictAnswer {
		t.Fatalf("重新連上後：%+v", task)
	}
	if connects.Load() != before+1 {
		t.Fatalf("Quiesce 後要重新連上引擎：%d → %d", before, connects.Load())
	}
}
