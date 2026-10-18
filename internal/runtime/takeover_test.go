package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/store"
)

// B14：接手時正在做的那一輪中斷、排隊的工作先不開始；交還時帶著說明在同一條對話串接著做，排在最前面。
func TestPauseAndResume(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	started := make(chan struct{}, 4)
	var mu sync.Mutex
	var prompts []string
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		mu.Lock()
		prompts = append(prompts, tr.Text)
		first := len(prompts) == 1
		mu.Unlock()
		if first {
			tr.Event("message.start", nil)
			tr.Event("message.delta", map[string]any{"text": "填到第 3 家"})
			started <- struct{}{}
			<-tr.Interrupted()
			tr.Complete("", "interrupted", nil)
			return
		}
		hermestest.ReplyOK(tr)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "比較三家價格")
	if err != nil {
		t.Fatal(err)
	}
	<-started

	p, err := f.rt.Pause(ctx, f.bot.ID)
	if err != nil || p.ThreadID != res.ThreadID || p.TaskID != res.TaskID {
		t.Fatalf("Pause：%+v %v", p, err)
	}
	if task, _ := f.db.GetTask(ctx, res.TaskID); task.State != store.TaskDone || task.Verdict != VerdictCancelled {
		t.Fatalf("Pause 要等那一輪停下：%+v", task)
	}
	if !f.rt.IsPaused(f.bot.ID) {
		t.Fatal("應該在暫停中")
	}
	// 再按一次接手：狀態不變。
	if again, _ := f.rt.Pause(ctx, f.bot.ID); again.TaskID != res.TaskID {
		t.Fatalf("重複接手：%+v", again)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var meta map[string]any
	_ = json.Unmarshal(msgs[1].Meta, &meta)
	if msgs[1].Content != "填到第 3 家" || meta["paused"] != true {
		t.Fatalf("中斷的回覆標 paused：%+v %v", msgs[1], meta)
	}

	// 接手中你又傳了一則訊息：排隊，不開始。
	queued, err := f.rt.Chat(ctx, f.bot.ID, "", "順便查運費")
	if err != nil || !queued.Queued {
		t.Fatalf("接手中要排隊：%+v %v", queued, err)
	}
	time.Sleep(100 * time.Millisecond)
	if th, _ := f.rt.Active(f.bot.ID); th != "" {
		t.Fatal("接手中不能開始新工作")
	}

	id, err := f.rt.Resume(ctx, f.bot.ID, ResumeRequest{Text: "已交還給小幫手：輸入了驗證碼", Prompt: "使用者已接手並完成：輸入了驗證碼"})
	if err != nil || id == "" {
		t.Fatalf("Resume：%q %v", id, err)
	}
	task := f.wait(t, id)
	if task.ThreadID != res.ThreadID || task.Source != SourceOwner {
		t.Fatalf("接著做的工作在原本的對話串：%+v", task)
	}
	f.wait(t, queued.TaskID)
	mu.Lock()
	got := strings.Join(prompts, "|")
	mu.Unlock()
	if !strings.HasPrefix(got, "比較三家價格|使用者已接手並完成：輸入了驗證碼|") {
		t.Fatalf("交還後先接著做，再做排隊的：%s", got)
	}
	msgs, _ = f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var line *store.Message
	for i := range msgs {
		if msgs[i].Kind == KindActivity {
			line = &msgs[i]
		}
	}
	if line == nil || line.Content != "已交還給小幫手：輸入了驗證碼" || !strings.Contains(string(line.Meta), ActivityHandback) {
		t.Fatalf("對話裡有交還的系統行：%+v", msgs)
	}
	if f.rt.IsPaused(f.bot.ID) {
		t.Fatal("交還後不再暫停")
	}
	if _, err := f.rt.Resume(ctx, f.bot.ID, ResumeRequest{}); !errors.Is(err, ErrNotPaused) {
		t.Fatalf("沒在接手：%v", err)
	}
}

// Bot 沒在做事時接手：交還時在指定的對話串（Bot 請你接手的那條）接著做；沒有對話串就只是恢復。
func TestPauseIdleResumeOnThread(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "登入網站")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	if p, err := f.rt.Pause(ctx, f.bot.ID); err != nil || p.TaskID != "" {
		t.Fatalf("Pause：%+v %v", p, err)
	}
	if err := f.rt.Note(ctx, f.bot.ID, res.ThreadID, ActivityTakeover, "你接手了電腦"); err != nil {
		t.Fatal(err)
	}
	id, err := f.rt.Resume(ctx, f.bot.ID, ResumeRequest{ThreadID: res.ThreadID, Text: "已交還", Prompt: "繼續"})
	if err != nil || id == "" {
		t.Fatalf("Resume：%q %v", id, err)
	}
	if task := f.wait(t, id); task.ThreadID != res.ThreadID {
		t.Fatalf("工作：%+v", task)
	}
	// 沒有對話串：只恢復，不開新工作。
	if _, err := f.rt.Pause(ctx, f.bot.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := f.rt.Resume(ctx, f.bot.ID, ResumeRequest{Prompt: "繼續"}); err != nil || id != "" {
		t.Fatalf("沒有對話串：%q %v", id, err)
	}
}
