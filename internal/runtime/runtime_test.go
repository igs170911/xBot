package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/store"
)

type fixedModel struct{}

func (fixedModel) ResolveModel(context.Context, store.Bot) (ModelRef, error) {
	return ModelRef{Provider: "openai", Model: "gpt-x"}, nil
}

type fixture struct {
	db  *store.DB
	g   *hermestest.Gateway
	rt  *Runtime
	bot store.Bot
	err error // Connector 要回的錯誤
}

func setup(t *testing.T) *fixture {
	t.Helper()
	return setupWith(t, nil)
}

// setupWith 和 setup 一樣，但可以先改 Options（例：調短看門狗）。
func setupWith(t *testing.T, opt func(*Options)) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, g: hermestest.New("k")}
	t.Cleanup(f.g.Close)
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		if f.err != nil {
			return hermes.Endpoint{}, f.err
		}
		return hermes.Endpoint{Addr: f.g.Addr(), Key: "k"}, nil
	}, nil)
	o := Options{DB: db, Driver: d, Bus: events.New(), Models: fixedModel{}}
	if opt != nil {
		opt(&o)
	}
	f.rt = New(o)
	if f.bot, err = db.CreateBot(ctx, store.Bot{Name: "小幫手"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) wait(t *testing.T, taskID string) store.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task, err := f.rt.Wait(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestChatStreamsAndPersists(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()

	res, err := f.rt.Chat(ctx, f.bot.ID, "", "回覆 OK")
	if err != nil {
		t.Fatal(err)
	}
	task := f.wait(t, res.TaskID)
	if task.State != store.TaskDone || task.Verdict != VerdictAnswer {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || msgs[0].Sender != "owner" || msgs[0].Content != "回覆 OK" || msgs[1].Sender != "bot:"+f.bot.ID || msgs[1].Content != "OK" {
		t.Fatalf("訊息：%+v", msgs)
	}
	th, _ := f.db.GetThread(ctx, res.ThreadID)
	if th.Title != "回覆 OK" || th.EngineSession != "stored-live-1" {
		t.Fatalf("對話串：%+v", th)
	}

	// 匯流排上依序有：兩則訊息、文字增量、用量、done。
	var kinds []string
	var text strings.Builder
	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case e := <-sub.C:
			kinds = append(kinds, e.Kind)
			if e.Kind == KindText {
				var p map[string]string
				_ = json.Unmarshal(e.Payload, &p)
				text.WriteString(p["text"])
			}
			done = e.Kind == KindDone
		case <-timeout:
			t.Fatalf("等不到 done：%v", kinds)
		}
	}
	if text.String() != "OK" || !strings.Contains(strings.Join(kinds, ","), KindUsage) {
		t.Fatalf("事件：%v %q", kinds, text.String())
	}
	// 非串流事件寫進 event 表。
	rows, _ := f.db.ListEvents(ctx, f.bot.ID, 0, 0)
	var stored []string
	for _, r := range rows {
		stored = append(stored, r.Kind)
	}
	got := strings.Join(stored, ",")
	if strings.Contains(got, KindText) || !strings.Contains(got, KindDone) || !strings.Contains(got, KindUsage) {
		t.Fatalf("event 表：%v", stored)
	}

	// 同一條對話串再聊一次：沿用 session。
	res2, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "再一次")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res2.TaskID)
	if n := strings.Count(strings.Join(f.g.Methods(), ","), "session.create"); n != 1 {
		t.Fatalf("應該只開一個 session：%v", f.g.Methods())
	}
}

func TestBusyAndStop(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	started := make(chan struct{})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "做到一半"})
		close(started)
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "做很久的事")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := f.rt.Chat(ctx, f.bot.ID, "", "另一件事"); !errors.Is(err, ErrBusy) {
		t.Fatalf("忙碌中：%v", err)
	}
	if th, task := f.rt.Active(f.bot.ID); th != res.ThreadID || task != res.TaskID {
		t.Fatalf("Active：%s %s", th, task)
	}
	if ok, err := f.rt.Stop(ctx, f.bot.ID, "其他對話串"); ok || err != nil {
		t.Fatalf("別的對話串不該被停：%v %v", ok, err)
	}
	if ok, err := f.rt.Stop(ctx, f.bot.ID, res.ThreadID); !ok || err != nil {
		t.Fatalf("Stop：%v %v", ok, err)
	}
	task := f.wait(t, res.TaskID)
	if task.Verdict != VerdictCancelled {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if msgs[1].Content != "做到一半" {
		t.Fatalf("停止後保留已串流的內容：%+v", msgs[1])
	}
	if th, _ := f.rt.Active(f.bot.ID); th != "" {
		t.Fatal("停止後應該釋放")
	}
	if ok, _ := f.rt.Stop(ctx, f.bot.ID, ""); ok {
		t.Fatal("沒在跑時 Stop 回 false")
	}
}

func TestStartFailureRecorded(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.err = errors.New("找不到 Bot 電腦映像")
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	if err != nil {
		t.Fatal(err)
	}
	task := f.wait(t, res.TaskID)
	if task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 2 || !strings.Contains(msgs[1].Content, "找不到 Bot 電腦映像") {
		t.Fatalf("失敗原因要寫進對話：%+v", msgs)
	}
	// 失敗後可以再聊。
	f.err = nil
	res, err = f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "再試")
	if err != nil {
		t.Fatal(err)
	}
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictAnswer {
		t.Fatalf("再試：%+v", task)
	}
}

func TestModelFailureMidTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "部分"})
		tr.Complete("Error: 401", "error", map[string]any{"error": "金鑰無效"})
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if !strings.HasPrefix(msgs[1].Content, "部分") || !strings.Contains(msgs[1].Content, "Error: 401") {
		t.Fatalf("回覆：%q", msgs[1].Content)
	}
}

func TestDisconnectFailsTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		f.g.DropConnections()
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
}

func TestResolveModel(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	c := &Computers{DB: f.db, Providers: staticProviders{"openai", "local"}}
	if _, err := c.ResolveModel(ctx, store.Bot{}); !errors.Is(err, ErrNoModel) {
		t.Fatalf("沒設定：%v", err)
	}
	if err := f.db.SetSetting(ctx, DefaultModelSetting, ModelRef{Provider: "openai", Model: "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]ModelRef{
		"":                 {Provider: "openai", Model: "gpt-x"},
		"gpt-y":            {Provider: "openai", Model: "gpt-y"},
		"local/qwen3:8b":   {Provider: "local", Model: "qwen3:8b"},
		"anthropic/claude": {Provider: "openai", Model: "anthropic/claude"}, // 沒有 anthropic 這個供應商：整串是模型名
	}
	for in, want := range cases {
		got, err := c.ResolveModel(ctx, store.Bot{Model: in})
		if err != nil || got != want {
			t.Errorf("%q → %+v %v", in, got, err)
		}
	}
}

type staticProviders []string

func (s staticProviders) ProviderIDs(context.Context) ([]string, error) { return s, nil }

// 定時任務（C32）：送給引擎的是 Prompt（含「使用者不在旁邊」的說明），對話裡只顯示 Text；MessageMeta、TaskMeta 記在訊息與工作上。
// 失敗時不發一般的「出錯」卡（排程器自己發 routine_failed）。
func TestStartWithPromptAndMeta(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	res, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "整理新聞", Prompt: "【定時任務】整理新聞", Source: "routine",
		MessageMeta: map[string]any{"routine_id": "r1", "routine_title": "新聞"}, TaskMeta: json.RawMessage(`{"routine_id":"r1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	var sent []string
	for _, c := range f.g.Calls() {
		if c.Method == "prompt.submit" {
			s, _ := c.Params["text"].(string)
			sent = append(sent, s)
		}
	}
	if len(sent) != 1 || sent[0] != "【定時任務】整理新聞" {
		t.Fatalf("送給引擎：%q", sent)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var meta map[string]string
	_ = json.Unmarshal(msgs[0].Meta, &meta)
	if msgs[0].Content != "整理新聞" || meta["routine_id"] != "r1" || meta["task_id"] != res.TaskID {
		t.Fatalf("對話裡：%+v %v", msgs[0], meta)
	}
	task, _ := f.db.GetTask(ctx, res.TaskID)
	if task.Source != "routine" || !strings.Contains(string(task.Meta), `"routine_id":"r1"`) {
		t.Fatalf("工作：%+v", task)
	}
	// 定時任務失敗：不發 error 卡（另一個還沒連線的 Bot，開機失敗）。
	f.err = errors.New("開不了機")
	other, _ := f.db.CreateBot(ctx, store.Bot{Name: "阿寶"})
	res, err = f.rt.Start(ctx, ChatRequest{BotID: other.ID, Text: "再一次", Source: "routine"})
	if err != nil {
		t.Fatal(err)
	}
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("任務：%+v", task)
	}
	if items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{Kinds: []string{"error"}}); len(items) != 0 {
		t.Fatalf("定時任務的失敗由排程器發卡：%+v", items)
	}
}

// 2026-10-20：最長的回覆是工具之間的旁白（「讓我試試…」）。引擎沒給最後的回答時，只取用完工具之後寫的那段。
func TestNarrationBetweenToolsIsNotTheAnswer(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	reply := func(final string) func(t *hermestest.Turn) {
		return func(t *hermestest.Turn) {
			t.Event("message.start", nil)
			t.Event("message.delta", map[string]any{"text": "讓我先查一下價格。"})
			t.Event("tool.start", map[string]any{"tool_id": "c1", "name": "web_search"})
			t.Event("tool.complete", map[string]any{"tool_id": "c1", "name": "web_search", "result": "ok"})
			t.Event("message.delta", map[string]any{"text": "A 店最便宜。"})
			t.Complete(final, "complete", nil)
		}
	}
	f.g.OnPrompt = reply("")
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "比價")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if got := msgs[len(msgs)-1].Content; got != "A 店最便宜。" {
		t.Fatalf("回答：%q", got)
	}
	// 引擎給了最後的回答就照它
	f.g.OnPrompt = reply("A 店最便宜，B 店缺貨。")
	res, err = f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "再查")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, res.TaskID)
	msgs, _ = f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if got := msgs[len(msgs)-1].Content; got != "A 店最便宜，B 店缺貨。" {
		t.Fatalf("回答：%q", got)
	}
}
