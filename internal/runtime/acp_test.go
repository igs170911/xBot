package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
	"xbot/internal/engine/hermes"
	"xbot/internal/events"
	"xbot/internal/store"
)

// 依 bot.engine 選驅動：claude-code 的 Bot 走 ACP 驅動，不需要設定 Model Proxy 的模型。
func TestChatACPEngine(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	agent := acptest.New()
	var revoked atomic.Bool
	comp := &Computers{DB: db}
	claude := acp.New(acp.Options{Agent: acp.Agents[acp.EngineClaude],
		Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil },
		Authorize: func(context.Context, string, string) error {
			if revoked.Load() {
				return errors.New("這個 Bot 還沒有使用你的 Claude 訂閱")
			}
			return nil
		},
	})
	hermesDrv := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{}, errors.New("不該用到 Hermes")
	}, nil)
	router := engine.NewRouter(hermes.Name, comp.EngineOf, hermesDrv, claude)
	rt := New(Options{DB: db, Driver: router, Bus: events.New(), Models: comp})
	bot, err := db.CreateBot(ctx, store.Bot{Name: "克勞德", Engine: acp.EngineClaude, Model: "sonnet", ApprovalMode: "edits"})
	if err != nil {
		t.Fatal(err)
	}
	wait := func(taskID string) store.Task {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		task, err := rt.Wait(wctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	res, err := rt.Chat(ctx, bot.ID, "", acptest.PromptHello)
	if err != nil {
		t.Fatal(err)
	}
	if task := wait(res.TaskID); task.Verdict != VerdictAnswer {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || msgs[1].Content != "Hello" {
		t.Fatalf("訊息：%+v", msgs)
	}
	th, _ := db.GetThread(ctx, res.ThreadID)
	if th.EngineSession != "sess-1" {
		t.Fatalf("要記下 ACP 的 sessionId 以便接回：%+v", th)
	}
	calls := agent.Calls()
	var sawMode, sawModel bool
	for _, c := range calls {
		sawMode = sawMode || (c.Method == "session/set_mode" && strings.Contains(string(c.Params), "acceptEdits"))
		sawModel = sawModel || (c.Method == "session/set_config_option" && strings.Contains(string(c.Params), "sonnet"))
	}
	if !sawMode || !sawModel {
		t.Fatalf("權限等級 edits → acceptEdits、模型 sonnet：%v", agent.Methods())
	}

	sub := rt.Bus().Subscribe(events.ForBot(bot.ID), 0)
	defer sub.Close()
	waitApproval := func(taskID string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for waiting := false; !waiting; {
			select {
			case e := <-sub.C:
				waiting = e.Kind == KindTaskState && e.TaskID == taskID && strings.Contains(string(e.Payload), store.TaskWaitingApproval)
			case <-deadline:
				t.Fatal("等不到等核准")
			}
		}
	}

	// 權限請求 → 待我處理的卡片 → 允許這次（xbot.chat.respond 經 runtime.Respond → Driver.Respond）→ agent 照做。
	res, err = rt.Chat(ctx, bot.ID, res.ThreadID, acptest.PromptPermission)
	if err != nil {
		t.Fatal(err)
	}
	waitApproval(res.TaskID)
	items, _ := db.ListInbox(ctx, store.InboxOpen)
	if len(items) != 1 {
		t.Fatalf("應該有一張待我處理的卡片：%+v", items)
	}
	if _, err := rt.Respond(ctx, items[0].ID, Answer{Choice: "once"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if task := wait(res.TaskID); task.Verdict != VerdictAnswer {
		t.Fatalf("允許後：%+v", task)
	}
	msgs, _ = db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "allowed:allow") {
		t.Fatalf("允許這次應該對到 allow_once：%+v", last)
	}

	// 不回答（decline）＝拒絕。
	res, err = rt.Chat(ctx, bot.ID, res.ThreadID, acptest.PromptPermission)
	if err != nil {
		t.Fatal(err)
	}
	waitApproval(res.TaskID)
	items, _ = db.ListInbox(ctx, store.InboxOpen)
	if _, err := rt.Respond(ctx, items[0].ID, Answer{Decline: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	wait(res.TaskID)
	msgs, _ = db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "rejected") {
		t.Fatalf("拒絕應該對到 reject_once：%+v", last)
	}

	// 權限請求 → 任務等核准；停止 → cancelled。
	res, err = rt.Chat(ctx, bot.ID, res.ThreadID, acptest.PromptPermission)
	if err != nil {
		t.Fatal(err)
	}
	waitApproval(res.TaskID)
	if task, _ := db.GetTask(ctx, res.TaskID); task.State != store.TaskWaitingApproval {
		t.Fatalf("應該在等核准：%+v", task)
	}
	if ok, err := rt.Stop(ctx, bot.ID, ""); !ok || err != nil {
		t.Fatalf("停止：%v %v", ok, err)
	}
	if task := wait(res.TaskID); task.Verdict != VerdictCancelled {
		t.Fatalf("停止後：%+v", task)
	}

	// 收回同意後，新的對話說明原因。
	revoked.Store(true)
	res, err = rt.Chat(ctx, bot.ID, "", acptest.PromptHello)
	if err != nil {
		t.Fatal(err)
	}
	if task := wait(res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("收回後：%+v", task)
	}
	msgs, _ = db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || !strings.Contains(msgs[1].Content, "訂閱") {
		t.Fatalf("要說明沒有連結訂閱：%+v", msgs)
	}
}
