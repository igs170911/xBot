package hermes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/hermes/hermestest"
)

// 上一輪 message.complete 之後 Hermes 還在收尾（running 還沒放開）。這時馬上送下一則：
// 驅動要先等 session.info（running=false）再送，不能讓它被當成插話收下卻不執行。
func TestBackToBackWaitsForIdle(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	g.Settle = 300 * time.Millisecond
	s, err := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := d.Send(ctx, s, engine.Message{Text: "回覆 OK"}); err != nil {
			t.Fatalf("第 %d 輪：%v", i+1, err)
		}
		evs := collect(t, d.Events(s))
		if last := evs[len(evs)-1]; last.Type != engine.EventDone || last.Status != engine.StatusComplete || last.Text != "OK" {
			t.Fatalf("第 %d 輪：%+v", i+1, evs)
		}
	}
	if st := g.Steered(); len(st) != 0 {
		t.Fatalf("不應該有 prompt 被當成插話：%v", st)
	}
	for _, c := range g.Calls() {
		if c.Method == "prompt.submit" && c.Params["queued"] != true {
			t.Fatalf("prompt.submit 應該帶 queued=true：%v", c.Params)
		}
	}
}

// 收尾太久（超過 IdleWait）：照樣送出，但帶 queued=true，Hermes 收完尾自己開這一輪。
func TestBusyTooLongQueues(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	g.Settle = 500 * time.Millisecond
	d.IdleWait = 50 * time.Millisecond
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "第一則"}); err != nil {
		t.Fatal(err)
	}
	collect(t, d.Events(s))
	if err := d.Send(ctx, s, engine.Message{Text: "第二則"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	if last := evs[len(evs)-1]; last.Status != engine.StatusComplete || last.Text != "OK" {
		t.Fatalf("排隊的那一輪：%+v", evs)
	}
	if st := g.Steered(); len(st) != 0 {
		t.Fatalf("不應該被當成插話：%v", st)
	}
}

// Hermes 還是把訊息當成插話（不認得 queued）：Send 回清楚的錯誤，不讓這一輪空等。
func TestSteeredIsAnError(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	g.Settle = time.Second
	g.IgnoreQueued = true
	d.IdleWait = 20 * time.Millisecond
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "第一則"}); err != nil {
		t.Fatal(err)
	}
	collect(t, d.Events(s))
	err := d.Send(ctx, s, engine.Message{Text: "第二則"})
	if err == nil || !strings.Contains(err.Error(), "沒有開始") {
		t.Fatalf("應該回錯誤：%v", err)
	}
}

// 送出後一直等不到 message.start：看門狗以錯誤結束這一輪。
func TestStartWatchdog(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	d.StartTimeout = 100 * time.Millisecond
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	g.OnPrompt = func(*hermestest.Turn) { <-block } // 收下了，但什麼都不送
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	if len(evs) != 2 || evs[0].Type != engine.EventError || !strings.Contains(evs[0].Text, "沒有開始") || evs[1].Status != engine.StatusError {
		t.Fatalf("看門狗：%+v", evs)
	}
}

// 有 message.start 就不會誤判：一輪做得比 StartTimeout 久也沒關係。
func TestStartWatchdogQuietAfterStart(t *testing.T) {
	ctx := context.Background()
	g, d, _ := setup(t)
	d.StartTimeout = 50 * time.Millisecond
	g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		time.Sleep(200 * time.Millisecond)
		tr.Complete("慢慢來", "complete", nil)
	}
	s, _ := d.Start(ctx, engine.StartOptions{BotID: "bot1", ThreadID: "th1"})
	if err := d.Send(ctx, s, engine.Message{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, d.Events(s))
	if len(evs) != 1 || evs[0].Status != engine.StatusComplete || evs[0].Text != "慢慢來" {
		t.Fatalf("事件：%+v", evs)
	}
}
