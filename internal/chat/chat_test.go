package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

var bg = context.Background()

type models struct{ err error }

func (m *models) ResolveModel(context.Context, store.Bot) (runtime.ModelRef, error) {
	return runtime.ModelRef{Provider: "openai", Model: "gpt-x"}, m.err
}

type env struct {
	db     *store.DB
	reg    *ops.Registry
	g      *hermestest.Gateway
	rt     *runtime.Runtime
	models *models
	bot    store.Bot
	other  store.Bot
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, g: hermestest.New("k"), models: &models{}}
	t.Cleanup(e.g.Close)
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: e.g.Addr(), Key: "k"}, nil
	}, nil)
	e.rt = runtime.New(runtime.Options{DB: db, Driver: d, Bus: events.New(), Models: e.models})
	e.reg = ops.New(ops.Config{DB: db})
	Register(e.reg, Deps{Runtime: e.rt})
	if e.bot, err = db.CreateBot(bg, store.Bot{Name: "小幫手"}); err != nil {
		t.Fatal(err)
	}
	if e.other, err = db.CreateBot(bg, store.Bot{Name: "別人"}); err != nil {
		t.Fatal(err)
	}
	return e
}

var owner = ops.Owner("test")

func agent(bots ...string) ops.Identity {
	return ops.Identity{Kind: ops.KindAgent, ID: "ag", Name: "Agent", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate},
		Resources: ops.Resources{Bots: bots}, Via: "mcp"}
}

func (e *env) call(id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(bg, id, op, raw, o)
	return res.Output, err
}

func must[T any](t *testing.T, raw json.RawMessage, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func (e *env) waitFor(t *testing.T, id ops.Identity, task string) WaitOut {
	t.Helper()
	raw, err := e.call(id, "xbot.chat.wait_until_settled", map[string]any{"task_id": task, "timeout_s": 10})
	return must[WaitOut](t, raw, err)
}

func TestSendAndWait(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "回覆 OK"})
	sent := must[SendOut](t, raw, err)
	if sent.ThreadID == "" || sent.TaskID == "" || sent.MessageID == "" {
		t.Fatalf("%+v", sent)
	}
	w := e.waitFor(t, owner, sent.TaskID)
	if !w.Settled || w.State != store.TaskDone || w.Verdict != runtime.VerdictAnswer || w.Text != "OK" || w.MessageID == "" || w.NeedsPerson {
		t.Fatalf("%+v", w)
	}
	// 同一條對話串再說一次。
	raw, err = e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "再一次", "thread_id": sent.ThreadID})
	sent2 := must[SendOut](t, raw, err)
	if sent2.ThreadID != sent.ThreadID {
		t.Fatalf("%+v", sent2)
	}
	if w := e.waitFor(t, owner, sent2.TaskID); w.Text != "OK" {
		t.Fatalf("%+v", w)
	}

	// read：由舊到新，分頁。
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID})
	r := must[ReadOut](t, raw, err)
	if len(r.Messages) != 4 || r.HasMore || r.Messages[0].Sender != "owner" || r.Messages[0].Content != "回覆 OK" ||
		r.Messages[1].Content != "OK" || r.Messages[1].TaskID != sent.TaskID || r.Messages[1].Status != "complete" {
		t.Fatalf("%+v", r)
	}
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID, "limit": 3})
	r = must[ReadOut](t, raw, err)
	if len(r.Messages) != 3 || !r.HasMore || r.Messages[0].Content != "OK" {
		t.Fatalf("第一頁：%+v", r)
	}
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID, "limit": 3, "before": r.NextBefore})
	r = must[ReadOut](t, raw, err)
	if len(r.Messages) != 1 || r.HasMore || r.Messages[0].Content != "回覆 OK" {
		t.Fatalf("第二頁：%+v", r)
	}

	raw, err = e.call(owner, "xbot.chat.list_threads", map[string]any{"bot_id": e.bot.ID})
	lt := must[ListThreadsOut](t, raw, err)
	if len(lt.Threads) != 1 || lt.Threads[0].ID != sent.ThreadID || lt.Threads[0].Title != "回覆 OK" {
		t.Fatalf("%+v", lt)
	}
}

func TestSendValidation(t *testing.T) {
	e := setup(t)
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "  "}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("空白訊息：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": "nope", "text": "hi"}); code(err) != ops.CodeNotFound {
		t.Fatalf("沒有這個 Bot：%v", err)
	}
	th, _ := e.db.CreateThread(bg, store.Thread{BotID: e.other.ID})
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi", "thread_id": th.ID}); code(err) != ops.CodeNotFound {
		t.Fatalf("別的 Bot 的對話串：%v", err)
	}
	_ = e.db.SetBotArchived(bg, e.bot.ID, true)
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"}); code(err) != ops.CodeConflict {
		t.Fatalf("封存的 Bot：%v", err)
	}
	_ = e.db.SetBotArchived(bg, e.bot.ID, false)
	e.models.err = runtime.ErrNoModel
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"}); code(err) != ops.CodeConflict {
		t.Fatalf("沒設定模型：%v", err)
	}
}

func TestSendDryRunWritesNothing(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"}, ops.Options{DryRun: true})
	out := must[SendOut](t, raw, err)
	if out.TaskID != "" || out.BotID != e.bot.ID {
		t.Fatalf("%+v", out)
	}
	if ths, _ := e.db.ListThreads(bg, e.bot.ID, true); len(ths) != 0 {
		t.Fatal("預演不應該開對話串")
	}
	if len(e.g.Calls()) != 0 {
		t.Fatal("預演不應該連引擎")
	}
}

func TestResourceScope(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.other.ID, "text": "hi"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, owner, sent.TaskID)

	a := agent(e.bot.ID)
	checks := []struct {
		op string
		in map[string]any
	}{
		{"xbot.chat.send", map[string]any{"bot_id": e.other.ID, "text": "hi"}},
		{"xbot.chat.stop", map[string]any{"bot_id": e.other.ID}},
		{"xbot.chat.list_threads", map[string]any{"bot_id": e.other.ID}},
		{"xbot.chat.read", map[string]any{"thread_id": sent.ThreadID}},
		{"xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 1}},
		// 用自己能碰的 Bot 加上別人的對話串也不行。
		{"xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi", "thread_id": sent.ThreadID}},
	}
	for _, c := range checks {
		if _, err := e.call(a, c.op, c.in); code(err) != ops.CodeForbidden {
			t.Errorf("%s %v 應該 forbidden：%v", c.op, c.in, err)
		}
	}
	// 自己的 Bot 可以。
	raw, err = e.call(a, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"})
	mine := must[SendOut](t, raw, err)
	if w := e.waitFor(t, a, mine.TaskID); w.Text != "OK" {
		t.Fatalf("%+v", w)
	}
	// 只有 read 的代理不能送訊息。
	ro := ops.Identity{Kind: ops.KindAgent, ID: "ro", Scopes: []ops.Scope{ops.ScopeRead}}
	if _, err := e.call(ro, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"}); code(err) != ops.CodeForbidden {
		t.Fatalf("read 不能 send：%v", err)
	}
}

func TestBusyStopAndTimeout(t *testing.T) {
	e := setup(t)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "處理中"})
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "做很久的事"})
	sent := must[SendOut](t, raw, err)

	// 逾時：settled=false，還在做。
	raw, err = e.call(owner, "xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 1})
	w := must[WaitOut](t, raw, err)
	if w.Settled || w.State == store.TaskDone || w.WaitedSeconds < 0.9 {
		t.Fatalf("應該逾時：%+v", w)
	}
	// 忙碌：conflict 而且可以重試；預演也看得出來。
	_, err = e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "插隊"})
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeConflict || !oe.Retryable {
		t.Fatalf("忙碌：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "插隊"}, ops.Options{DryRun: true}); code(err) != ops.CodeConflict {
		t.Fatalf("預演忙碌：%v", err)
	}
	// stop 預演不會真的停。
	raw, err = e.call(owner, "xbot.chat.stop", map[string]any{"bot_id": e.bot.ID}, ops.Options{DryRun: true})
	if s := must[StopOut](t, raw, err); !s.Stopped || s.TaskID != sent.TaskID {
		t.Fatalf("%+v", s)
	}
	raw, err = e.call(owner, "xbot.chat.stop", map[string]any{"bot_id": e.bot.ID, "thread_id": "other"})
	if s := must[StopOut](t, raw, err); s.Stopped {
		t.Fatal("別的對話串不應該停到")
	}
	raw, err = e.call(owner, "xbot.chat.stop", map[string]any{"bot_id": e.bot.ID})
	if s := must[StopOut](t, raw, err); !s.Stopped || s.TaskID != sent.TaskID {
		t.Fatalf("%+v", s)
	}
	w = e.waitFor(t, owner, sent.TaskID)
	if !w.Settled || w.Verdict != runtime.VerdictCancelled || w.Text != "處理中" {
		t.Fatalf("停止後：%+v", w)
	}
	raw, err = e.call(owner, "xbot.chat.stop", map[string]any{"bot_id": e.bot.ID})
	if s := must[StopOut](t, raw, err); s.Stopped {
		t.Fatal("沒在做事時 stopped=false")
	}
}

func TestWaitReturnsWhenBotNeedsPerson(t *testing.T) {
	e := setup(t)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Request("approval", map[string]any{"command": "rm -rf /workspace/tmp", "choices": []string{"once", "deny"}})
		// 真的 Bot 會停在這裡等人回答；不要馬上結束，否則等待者可能先看到「完成」。
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "清理暫存"})
	sent := must[SendOut](t, raw, err)
	t.Cleanup(func() { _, _ = e.rt.Stop(bg, e.bot.ID, "") })
	w := e.waitFor(t, owner, sent.TaskID)
	if !w.Settled || !w.NeedsPerson || w.State != store.TaskWaitingApproval || w.Request == nil ||
		w.Request.Kind != "approval" || w.Request.Prompt != "rm -rf /workspace/tmp" {
		t.Fatalf("%+v %+v", w, w.Request)
	}
}

func TestWaitRespectsCancellation(t *testing.T) {
	e := setup(t)
	e.g.OnPrompt = func(tr *hermestest.Turn) { <-tr.Interrupted(); tr.Complete("", "interrupted", nil) }
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "等很久"})
	sent := must[SendOut](t, raw, err)
	t.Cleanup(func() { _, _ = e.rt.Stop(bg, e.bot.ID, "") })

	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		in, _ := json.Marshal(map[string]any{"task_id": sent.TaskID, "timeout_s": 300})
		_, err := e.reg.Invoke(ctx, owner, "xbot.chat.wait_until_settled", in, ops.Options{})
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil || time.Since(start) > 5*time.Second {
			t.Fatalf("取消後應該馬上回：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消後沒有回來")
	}
	if _, err := e.call(owner, "xbot.chat.wait_until_settled", map[string]any{"task_id": "nope"}); code(err) != ops.CodeNotFound {
		t.Fatalf("沒有這件工作：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.wait_until_settled", map[string]any{"task_id": sent.TaskID, "timeout_s": 301}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("超過 300 秒：%v", err)
	}
}

func TestNoRuntime(t *testing.T) {
	db, _ := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	defer db.Close()
	reg := ops.New(ops.Config{DB: db})
	Register(reg, Deps{})
	b, _ := db.CreateBot(bg, store.Bot{Name: "x"})
	in := fmt.Sprintf(`{"bot_id":%q,"text":"hi"}`, b.ID)
	if _, err := reg.Invoke(bg, owner, "xbot.chat.send", json.RawMessage(in), ops.Options{}); code(err) != ops.CodeInternal {
		t.Fatalf("%v", err)
	}
}

// 檔案卡（C11 cards.post）：chat.read 帶出卡片內容，畫面才能把檔案畫成可以預覽、下載的列（C33）。
func TestMessageViewCarriesCard(t *testing.T) {
	m := messageView(store.Message{ID: "m1", Kind: "card", Content: "【檔案】報告", Meta: []byte(
		`{"task_id":"t1","card":{"type":"file","title":"報告好了","files":[{"path":"/workspace/報告.pdf","name":"報告.pdf"}]}}`)})
	if m.Card == nil || m.Card.Type != "file" || len(m.Card.Files) != 1 || m.Card.Files[0].Path != "/workspace/報告.pdf" || m.TaskID != "t1" {
		t.Fatalf("%+v", m)
	}
	if messageView(store.Message{ID: "m2", Kind: "text"}).Card != nil {
		t.Fatal("一般訊息沒有卡片")
	}
}

func TestDeleteThread(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, owner, sent.TaskID)

	// 代理要等你核准（刪資料是 confirm 級）。
	if _, err := e.call(agent(e.bot.ID), "xbot.chat.delete_thread", map[string]any{"thread_id": sent.ThreadID}); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理刪對話串要核准：%v", err)
	}
	if _, err := e.db.GetThread(bg, sent.ThreadID); err != nil {
		t.Fatalf("還沒核准不能刪：%v", err)
	}
	raw, err = e.call(owner, "xbot.chat.delete_thread", map[string]any{"thread_id": sent.ThreadID})
	if out := must[DeleteThreadOut](t, raw, err); !out.Deleted || out.BotID != e.bot.ID {
		t.Fatalf("%+v", out)
	}
	if _, err := e.db.GetThread(bg, sent.ThreadID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("對話串應該刪掉了：%v", err)
	}
	if msgs, _ := e.db.ListMessages(bg, store.ScopeThread, sent.ThreadID, 0, 10); len(msgs) != 0 {
		t.Fatalf("訊息應該一起刪掉：%d", len(msgs))
	}
	if _, err := e.call(owner, "xbot.chat.delete_thread", map[string]any{"thread_id": sent.ThreadID}); code(err) != ops.CodeNotFound {
		t.Fatalf("再刪一次是 not_found：%v", err)
	}
	// 群組座位不能刪。
	seat, _ := e.db.CreateThread(bg, store.Thread{BotID: e.bot.ID, RoomID: "r1"})
	if _, err := e.call(owner, "xbot.chat.delete_thread", map[string]any{"thread_id": seat.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("座位不能刪：%v", err)
	}
}

// chat.read 帶上每件工作現在的狀態：重新整理後畫面以它為準，不會把做完的工作顯示成「排隊中」。
func TestReadCarriesTaskStates(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, owner, sent.TaskID)
	raw, err = e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID})
	out := must[ReadOut](t, raw, err)
	if out.Tasks[sent.TaskID] != "done" {
		t.Fatalf("工作狀態：%v", out.Tasks)
	}
}

// 2026-10-20：點進聊完的對話再出來，清單的藍點還在——未讀從來沒有歸零。xbot.chat.mark_read 讓畫面看過就歸零。
func TestMarkReadClearsUnread(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "hi"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, owner, sent.TaskID)
	if th, _ := e.db.GetThread(bg, sent.ThreadID); th.Unread == 0 {
		t.Fatal("Bot 回了以後應該有未讀")
	}
	if _, err := e.call(agent(e.bot.ID), "xbot.chat.mark_read", map[string]any{"thread_id": sent.ThreadID}); code(err) != ops.CodeForbidden {
		t.Fatalf("已讀是你的狀態，代理不能標：%v", err)
	}
	raw, err = e.call(owner, "xbot.chat.mark_read", map[string]any{"thread_id": sent.ThreadID})
	if out := must[MarkReadOut](t, raw, err); !out.Marked {
		t.Fatalf("%+v", out)
	}
	if th, _ := e.db.GetThread(bg, sent.ThreadID); th.Unread != 0 {
		t.Fatalf("未讀 %d", th.Unread)
	}
}
