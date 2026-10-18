package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

func fastWatchdog(o *Options) {
	o.Watchdog = Watchdog{Idle: 150 * time.Millisecond, Tool: 600 * time.Millisecond, Grace: 200 * time.Millisecond, Interval: 20 * time.Millisecond}
}

// 一輪開始後就沒有任何進度：看門狗送中斷；引擎回 interrupted 時算失敗（不是使用者停止），寫下說明。
func TestWatchdogInterruptsSilentTurn(t *testing.T) {
	ctx := context.Background()
	f := setupWith(t, fastWatchdog)
	interrupted := make(chan struct{})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "做到一半"})
		<-tr.Interrupted()
		close(interrupted)
		tr.Complete("", "interrupted", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "做很久的事")
	if err != nil {
		t.Fatal(err)
	}
	task := f.wait(t, res.TaskID)
	select {
	case <-interrupted:
	default:
		t.Fatal("看門狗應該送出中斷")
	}
	if task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if !strings.HasPrefix(msgs[1].Content, "做到一半") || !strings.Contains(msgs[1].Content, StalledText) {
		t.Fatalf("回覆：%q", msgs[1].Content)
	}
}

// 中斷之後引擎也不回應：過了 Grace 直接結束這一輪，Bot 被釋放。
func TestWatchdogGivesUpAfterGrace(t *testing.T) {
	ctx := context.Background()
	f := setupWith(t, fastWatchdog)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		<-block // 連中斷都不理
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
	if th, _ := f.rt.Active(f.bot.ID); th != "" {
		t.Fatal("應該釋放 Bot")
	}
}

// 按了停止、引擎卻不結束：過了 Grace 以「已停止」結束。
func TestStopGrace(t *testing.T) {
	ctx := context.Background()
	f := setupWith(t, func(o *Options) {
		fastWatchdog(o)
		o.Watchdog.Idle = time.Minute
	})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		close(started)
		<-block
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	<-started
	if ok, err := f.rt.Stop(ctx, f.bot.ID, ""); !ok || err != nil {
		t.Fatalf("Stop：%v %v", ok, err)
	}
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictCancelled {
		t.Fatalf("任務：%+v", task)
	}
}

// 有工具在執行時用比較長的時限：工具跑得比 Idle 久不會被中斷。
func TestWatchdogToolLimit(t *testing.T) {
	ctx := context.Background()
	f := setupWith(t, fastWatchdog)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("tool.start", map[string]any{"tool_id": "t1", "name": "terminal"})
		time.Sleep(350 * time.Millisecond) // 超過 Idle、沒超過 Tool
		tr.Event("tool.complete", map[string]any{"tool_id": "t1", "name": "terminal", "result": "{}"})
		tr.Complete("好了", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "建置")
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictAnswer {
		t.Fatalf("任務：%+v", task)
	}
}
