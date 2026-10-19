package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// waitCard 等到這件工作有一張 open 的提問卡。
func (f *fixture) waitCard(t *testing.T, taskID string) store.InboxItem {
	t.Helper()
	for i := 0; i < 200; i++ {
		items, _ := f.db.ListInboxFiltered(context.Background(), store.InboxFilter{TaskID: taskID, Status: store.InboxOpen})
		if len(items) > 0 {
			return items[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等不到提問卡")
	return store.InboxItem{}
}

func TestQuestionCardEventsAndAnswer(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		res := tr.Request("vault.save_login", map[string]any{"prompt": "晨光記帳的登入", "site": "chenguang.tw"})
		tr.Complete(string(res), "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "整理發票")
	if err != nil {
		t.Fatal(err)
	}
	card := f.waitCard(t, res.TaskID)
	if card.Kind != "secret" || !strings.Contains(string(card.Payload), `"name":"identifier"`) || card.ThreadID != res.ThreadID {
		t.Fatalf("帳號＋密碼卡：%+v", card)
	}
	if _, err := f.rt.Respond(ctx, card.ID, Answer{Values: map[string]string{"identifier": "ops@x.tw"}}, "owner"); err == nil {
		t.Fatal("密碼沒填要失敗")
	}
	got, err := f.rt.Respond(ctx, card.ID, Answer{Values: map[string]string{"identifier": "ops@x.tw", "password": "pw-123"}}, "owner")
	if err != nil || got.Status != store.InboxResolved || got.Resolution != ResolutionProvided {
		t.Fatalf("%+v %v", got, err)
	}
	if strings.Contains(string(got.Payload), "pw-123") {
		t.Fatal("卡片不能有密碼")
	}
	task := f.wait(t, res.TaskID)
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if task.Verdict != VerdictAnswer || !strings.Contains(msgs[1].Content, `{\"identifier\":\"ops@x.tw\",\"password\":\"pw-123\"}`) {
		t.Fatalf("Bot 要拿到帳號密碼 JSON：%q", msgs[1].Content)
	}
	if _, err := f.rt.Respond(ctx, card.ID, Answer{Value: "x"}, "owner"); !errors.Is(err, ErrAnswered) {
		t.Fatalf("回過了：%v", err)
	}

	// 匯流排上：inbox.created → chat.needs_approval（帶 inbox_id）→ inbox.updated → chat.answered（不含密碼）。
	var seen []string
	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case e := <-sub.C:
			switch e.Kind {
			case notify.KindCreated, notify.KindUpdated:
				if e.ThreadID != res.ThreadID || e.ID == 0 {
					t.Fatalf("inbox 事件要存下來並帶對話串：%+v", e)
				}
				var p notify.Payload
				_ = json.Unmarshal(e.Payload, &p)
				seen = append(seen, e.Kind+":"+p.Kind+":"+p.Group)
			case KindNeedsApproval:
				var p NeedsPayload
				_ = json.Unmarshal(e.Payload, &p)
				if p.InboxID != card.ID || p.Request == nil {
					t.Fatalf("needs_approval：%s", e.Payload)
				}
				seen = append(seen, e.Kind)
			case KindAnswered:
				if strings.Contains(string(e.Payload), "pw-123") || !strings.Contains(string(e.Payload), `"text":"已提供"`) {
					t.Fatalf("answered：%s", e.Payload)
				}
				seen = append(seen, e.Kind)
			case KindDone:
				done = true
			}
		case <-timeout:
			t.Fatalf("等不到 done：%v", seen)
		}
	}
	// 回覆送出後引擎可能馬上做完：chat.answered 和完成通知誰先都可以。
	order := strings.Join(seen, ",")
	want := "inbox.created:secret:decide,chat.needs_approval,inbox.updated:secret:decide,"
	if !strings.HasPrefix(order, want) || !strings.Contains(order, "chat.answered") || !strings.Contains(order, "inbox.created:done:done") || len(seen) != 5 {
		t.Fatalf("事件順序：\n%v\n想要開頭\n%v", order, want)
	}
}

// 無人在場的提問過期：Bot 收到「使用者沒有回應，不要猜答案」，卡片變成已過期。
func TestUnattendedQuestionExpires(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.rt.o.QuestionTTL = 200 * time.Millisecond
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		res := tr.Request("clarify", map[string]any{"questions": []map[string]any{{"qid": "q1", "question": "要不要退訂？", "choices": []string{"要", "不要"}}}})
		tr.Complete(string(res), "complete", nil)
	}
	res, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "整理收件匣", Source: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	card := f.waitCard(t, res.TaskID)
	if card.ExpiresAt == 0 || card.Kind != "question" || !strings.Contains(string(card.Payload), `"choices":["要","不要"]`) {
		t.Fatalf("%+v", card)
	}
	task := f.wait(t, res.TaskID)
	if task.Source != "bot" {
		t.Fatalf("%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if !strings.Contains(msgs[1].Content, "10 分鐘內沒有回應") {
		t.Fatalf("Bot 要收到沒回應：%q", msgs[1].Content)
	}
	got, _ := f.db.GetInboxItem(ctx, card.ID)
	if got.Status != store.InboxExpired || got.Resolution != ResolutionExpired {
		t.Fatalf("%+v", got)
	}
	ev, err := f.db.LatestTaskEvent(ctx, f.bot.ID, res.TaskID, KindAnswered)
	if err != nil || !strings.Contains(string(ev.Payload), "提問已過期") {
		t.Fatalf("對話要記下過期：%s %v", ev.Payload, err)
	}
}

// Hermes 自己取消請求（request.cancel）：卡片收掉。服務重開：留下的提問卡收掉。
func TestRequestCanceledAndRecover(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		go tr.Request("approval", map[string]any{"command": "ls", "choices": []string{"once", "deny"}})
		time.Sleep(200 * time.Millisecond)
		tr.Event("request.cancel", map[string]any{"id": "srq-1"})
		time.Sleep(200 * time.Millisecond)
		tr.Complete("好", "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "看一下")
	if err != nil {
		t.Fatal(err)
	}
	card := f.waitCard(t, res.TaskID)
	f.wait(t, res.TaskID)
	if got, _ := f.db.GetInboxItem(ctx, card.ID); got.Status != store.InboxDismissed || got.Resolution != ResolutionCanceled {
		t.Fatalf("取消後：%+v", got)
	}

	stale, _ := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "question", BotID: f.bot.ID, TaskID: res.TaskID, ThreadID: res.ThreadID})
	other, _ := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "budget", BotID: f.bot.ID})
	if err := f.rt.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetInboxItem(ctx, stale.ID); got.Status != store.InboxDismissed || got.Resolution != ResolutionGone {
		t.Fatalf("重開後：%+v", got)
	}
	if got, _ := f.db.GetInboxItem(ctx, other.ID); got.Status != store.InboxOpen {
		t.Fatalf("別種卡不動：%+v", got)
	}
	if _, err := f.rt.Respond(ctx, other.ID, Answer{Text: "x"}, "owner"); !errors.Is(err, ErrNotQuestion) {
		t.Fatalf("%v", err)
	}
}

// 回覆時 Bot 已經不在等了（例：服務重開過）：回 ErrRequestGone，卡片收掉。
func TestRespondWhenBotGone(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	b, _ := f.db.CreateThread(ctx, store.Thread{BotID: f.bot.ID})
	card, _ := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "approval", BotID: f.bot.ID, TaskID: "old", ThreadID: b.ID})
	if _, err := f.rt.Respond(ctx, card.ID, Answer{Choice: "once"}, "owner"); !errors.Is(err, ErrRequestGone) {
		t.Fatalf("%v", err)
	}
	if got, _ := f.db.GetInboxItem(ctx, card.ID); got.Status != store.InboxDismissed {
		t.Fatalf("%+v", got)
	}
}

// 2026-10-19 實際遇到：Hermes 提問之後緊接著送用量（session.usage），狀態被改回 running，
// 對話裡的提問卡跟著收掉、只剩待我處理。用量只是記帳：問題沒回之前要一直是「等你回答」。
func TestUsageAfterQuestionKeepsWaiting(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		go func() {
			time.Sleep(100 * time.Millisecond)
			tr.Event("session.usage", map[string]any{"usage": map[string]any{"model": "m", "input": 10, "output": 2, "calls": 1}})
		}()
		res := tr.Request("clarify", map[string]any{"question": "團隊要做什麼？"})
		tr.Complete(string(res), "complete", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我建團隊")
	if err != nil {
		t.Fatal(err)
	}
	card := f.waitCard(t, res.TaskID)
	time.Sleep(400 * time.Millisecond) // 等用量送到
	if task, _ := f.db.GetTask(ctx, res.TaskID); task.State != store.TaskWaitingInput {
		t.Fatalf("用量送到後還要是 waiting_input：%s", task.State)
	}
	if _, err := f.rt.Respond(ctx, card.ID, Answer{Text: "內容團隊"}, "owner"); err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
}
