package runtime

// X04 任務完成判定：判斷記在任務、回覆與 chat.done 上；自動續做（LazyBoy auto_continue 的測試）；需要你的卡；目前進度。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/judge"
	"xbot/internal/store"
)

// script 讓假 gateway 依收到的 prompt 回覆；tools=true 時先跑一個工具。記下收到的每個 prompt。
type script struct {
	mu      sync.Mutex
	prompts []string
	reply   func(prompt string, n int) (text string, tools bool, status string)
}

func (s *script) on(tr *hermestest.Turn) {
	s.mu.Lock()
	s.prompts = append(s.prompts, tr.Text)
	n := len(s.prompts)
	s.mu.Unlock()
	text, tools, status := s.reply(tr.Text, n)
	tr.Event("message.start", nil)
	if strings.HasPrefix(status, "計畫：") {
		tr.Event("status.update", map[string]any{"text": status})
		status = ""
	}
	if tools {
		tr.Event("tool.start", map[string]any{"tool_id": "t1", "name": "web_search"})
		tr.Event("tool.complete", map[string]any{"tool_id": "t1", "name": "web_search", "result": "ok"})
	}
	tr.Complete(text, "complete", nil)
}

func (s *script) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts...)
}

func judgmentOfTask(t *testing.T, f *fixture, id string) *Judgment {
	t.Helper()
	task, err := f.db.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return TaskJudgment(task)
}

// chain 等一件工作做完，順著自動續做一路等下去，回傳每一件工作的判斷。
func chain(t *testing.T, f *fixture, id string) []*Judgment {
	t.Helper()
	var out []*Judgment
	for id != "" {
		f.wait(t, id)
		j := judgmentOfTask(t, f, id)
		out = append(out, j)
		if j == nil {
			break
		}
		id = j.ContinuedBy
	}
	return out
}

// LazyBoy test_a_reply_that_only_promises_to_look_is_sent_back_to_actually_do_it
func TestAReplyThatOnlyPromisesToLookIsSentBackToActuallyDoIt(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	s := &script{reply: func(p string, _ int) (string, bool, string) {
		if strings.HasPrefix(p, "（系統自動繼續）") {
			return "我查了：三位成員都閒置，沒有人叫醒他們。", true, ""
		}
		return "查一下團隊狀態，看看他們現在在做什麼。", false, ""
	}}
	f.g.OnPrompt = s.on
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 256)
	defer sub.Close()
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "他們有在做嗎")
	if err != nil {
		t.Fatal(err)
	}
	js := chain(t, f, res.TaskID)
	if len(js) != 2 || js[0].Verdict != judge.OnlyPromised || js[0].By != judge.ByRules || js[0].Label != "只說要做" || js[1].Verdict != judge.Done {
		t.Fatalf("判斷：%+v %+v", js[0], js[len(js)-1])
	}
	seen := s.seen()
	if len(seen) != 2 || !strings.Contains(seen[1], "只回了一句要去做的話") {
		t.Fatalf("prompts：%q", seen)
	}
	// 你問進度：附上系統查得的事實（PRD §19.2）。
	if !strings.Contains(seen[0], "系統查得的目前狀況") || !strings.Contains(seen[0], "目前沒有任何工作在執行") {
		t.Fatalf("問進度要附事實：%q", seen[0])
	}
	// 對話裡：你的訊息、Bot 的承諾（判斷在 meta）、「自動繼續：…」那一行、Bot 真的做完的回覆。
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) != 4 || msgs[2].Sender != "system" || msgs[2].Kind != KindActivity || msgs[2].Content != "自動繼續：剛才只說要做，現在真的去做" {
		t.Fatalf("訊息：%+v", msgs)
	}
	var meta struct {
		Judge *Judgment `json:"judge"`
	}
	if err := json.Unmarshal(msgs[1].Meta, &meta); err != nil || meta.Judge == nil || meta.Judge.Verdict != judge.OnlyPromised {
		t.Fatalf("回覆的 meta：%s", msgs[1].Meta)
	}
	// chat.done 帶判斷。
	found := false
	for len(sub.C) > 0 {
		e := <-sub.C
		if e.Kind == KindDone && e.TaskID == res.TaskID {
			var d DonePayload
			_ = json.Unmarshal(e.Payload, &d)
			found = d.Judge != nil && d.Judge.Verdict == judge.OnlyPromised && d.Judge.ContinuedBy != ""
		}
	}
	if !found {
		t.Fatal("chat.done 要帶判斷")
	}
	// 系統叫它接著做的那一輪不發「完成」卡；真的做完的那一輪才發。
	items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID})
	if len(items) != 1 || items[0].Kind != "done" || items[0].TaskID != js[0].ContinuedBy {
		t.Fatalf("通知：%+v", items)
	}
}

// LazyBoy test_a_turn_that_stops_with_its_plan_unfinished_carries_on_by_itself：最多自動續做 2 次。
func TestAutoContinueStopsAfterTwo(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	s := &script{reply: func(string, int) (string, bool, string) {
		return "商品挑好了，Threads 還沒找到。要我繼續嗎？", true, "計畫：\n☑ 挑出 3 個商品\n☐ 找 Threads 新貼文"
	}}
	f.g.OnPrompt = s.on
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "衝分潤")
	if err != nil {
		t.Fatal(err)
	}
	js := chain(t, f, res.TaskID)
	if len(js) != 3 {
		all, _ := f.db.ListTasks(ctx, f.bot.ID, false)
		for _, x := range all {
			t.Logf("%s %s %s %s", x.ID, x.State, x.Verdict, x.Meta)
		}
		for _, j := range js {
			t.Logf("%+v", j)
		}
		t.Fatalf("原本一次＋續做兩次：%+v", js)
	}
	for _, j := range js {
		if j.Verdict != judge.HandedBack {
			t.Fatalf("%+v", j)
		}
	}
	seen := s.seen()
	if !strings.Contains(seen[1], "找 Threads 新貼文") || !strings.Contains(seen[1], "不要問使用者要不要繼續") {
		t.Fatalf("續做的提示：%q", seen[1])
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if msgs[2].Content != "自動繼續：計畫還有 1 項沒完成" {
		t.Fatalf("%+v", msgs[2])
	}
	// 續做的工作記著第幾次與從哪件開始。
	task, _ := f.db.GetTask(ctx, js[1].ContinuedBy)
	var am autoMeta
	_ = json.Unmarshal(task.Meta, &am)
	if am.Depth != 2 || am.Root != res.TaskID || !task.LowPriority || task.Source != SourceOwner {
		t.Fatalf("%+v %s", task, task.Meta)
	}
	// 續做兩次後還是交回來：開一張「交回給你」卡。
	items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen})
	if len(items) != 1 || items[0].Kind != "question" || !strings.Contains(items[0].Title, "交回給你") || items[0].ThreadID != res.ThreadID ||
		!strings.Contains(items[0].Body, "要我繼續") {
		t.Fatalf("卡片：%+v", items)
	}
}

// 要你本人（登入、對外發送前的同意）：不續做，開一張「需要你」卡；同一條對話串不重複開；你回話就收掉。
func TestNeedsPersonOpensACardOnce(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	s := &script{reply: func(p string, _ int) (string, bool, string) {
		if p == "好了" {
			return "發好了，三則都上線。", true, ""
		}
		return "三則留言的草稿寫好了，請你確認內容後我再發。", true, ""
	}}
	f.g.OnPrompt = s.on
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "寫留言")
	if js := chain(t, f, res.TaskID); len(js) != 1 || js[0].Verdict != judge.NeedsPerson || js[0].ContinuedBy != "" {
		t.Fatalf("%+v", js)
	}
	// 同一條對話串又要你一次：還是只有一張。
	res2, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, ThreadID: res.ThreadID, Text: "再寫一則", Source: SourceRoutine})
	chain(t, f, res2.TaskID)
	open, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen})
	if len(open) != 1 || open[0].Kind != "question" || open[0].Title != "小幫手需要你處理" || !strings.Contains(open[0].Body, "請你確認內容後我再發") || open[0].TaskID != "" {
		t.Fatalf("卡片：%+v", open)
	}
	// 你回話：卡收掉。
	res3, _ := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "好了")
	if js := chain(t, f, res3.TaskID); js[0].Verdict != judge.Done {
		t.Fatalf("%+v", js)
	}
	if it, _ := f.db.GetInboxItem(ctx, open[0].ID); it.Status != store.InboxResolved || it.Resolution != ResolutionAnswered {
		t.Fatalf("%+v", it)
	}
}

// 你在這之間傳了新訊息、有待核准的動作、群組的一輪、隊友交辦（沒有 ContinueMeta）：不續做。
func TestNoAutoContinue(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	g := newGate()
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		if tr.Text == "等一下" {
			g.onPrompt(tr)
			return
		}
		tr.Complete("我來查一下", "complete", nil)
	}
	// 群組、隊友交辦：不續做。
	for _, src := range []string{SourceRoom, SourceBot} {
		res, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "查價格 " + src, Source: src, LowPriority: true, Queue: true})
		if js := chain(t, f, res.TaskID); len(js) != 1 || js[0].Verdict != judge.OnlyPromised || js[0].ContinuedBy != "" {
			t.Fatalf("%s：%+v", src, js)
		}
	}
	// 群組、隊友的「只說要做」不發卡（結果回到交辦的 Bot 或群組）。
	if items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID}); len(items) != 0 {
		t.Fatalf("%+v", items)
	}
	// 有待核准的動作：不續做。
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "等一下")
	waitFor(t, "開始", func() bool { return len(g.seen()) == 1 })
	if _, err := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "approval", BotID: f.bot.ID, TaskID: res.TaskID, Ref: "ap1", Title: "寄信"}); err != nil {
		t.Fatal(err)
	}
	close(g.ch("等一下"))
	f.wait(t, res.TaskID)
	// gate 回的是「好了：等一下」＝已完成；改用承諾再測一次。
	f.g.OnPrompt = func(tr *hermestest.Turn) { tr.Complete("我來查一下", "complete", nil) }
	res, _ = f.rt.Chat(ctx, f.bot.ID, "", "查一下")
	f.wait(t, res.TaskID)
	if _, err := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "approval", BotID: f.bot.ID, TaskID: res.TaskID, Ref: "ap2", Title: "寄信"}); err != nil {
		t.Fatal(err)
	}
	task, _ := f.db.GetTask(ctx, res.TaskID)
	j := TaskJudgment(task)
	if j == nil || j.Verdict != judge.OnlyPromised || j.ContinuedBy == "" {
		t.Fatalf("沒有待核准時要續做：%+v", j)
	}
	cont, _ := f.db.GetTask(ctx, j.ContinuedBy)
	f.wait(t, cont.ID)
	// 續做的那一輪結束時有待核准的動作（開在它名下）就不再續做。
	if _, err := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "approval", BotID: f.bot.ID, TaskID: cont.ID, Ref: "ap3", Title: "寄信"}); err != nil {
		t.Fatal(err)
	}
	r := f.rt
	in := judge.Input{Outcome: judge.OutcomeAnswer, Text: "我來查一下"}
	tt := &turn{botID: f.bot.ID, threadID: cont.ThreadID, taskID: cont.ID}
	if req := r.continuation(tt, cont, in, judge.Result{Verdict: judge.OnlyPromised}); req != nil {
		t.Fatalf("有待核准：%+v", req)
	}
	// 你的訊息在排隊：不續做（你的話先處理）。
	if _, err := f.db.CreateTask(ctx, store.Task{BotID: f.bot.ID, ThreadID: cont.ThreadID, Source: SourceOwner, Input: "改一下"}); err != nil {
		t.Fatal(err)
	}
	if req := r.continuation(tt, task, in, judge.Result{Verdict: judge.OnlyPromised}); req != nil {
		t.Fatalf("你的訊息在排隊：%+v", req)
	}
}

// 隊友交辦的只說要做：有 ContinueMeta 時續做，續做的那一輪帶著原本的交辦。
func TestTeammatePromiseContinuesWithItsAssignment(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		if strings.HasPrefix(tr.Text, "（系統自動繼續）") {
			tr.Complete("價格：A 店 120 元。", "complete", nil)
			return
		}
		tr.Complete("好的，我來查一下價格。", "complete", nil)
	}
	var mu sync.Mutex
	var prompts []string
	f.rt.SetHooks(Hooks{ContinueMeta: func(task store.Task) (json.RawMessage, bool) {
		return json.RawMessage(`{"from_bot":"b2","from_name":"小研究","intent":"request","message_id":"m1"}`), true
	}, Prompt: func(_ context.Context, task store.Task) (string, bool, error) {
		mu.Lock()
		prompts = append(prompts, task.Input)
		mu.Unlock()
		return "", false, nil
	}})
	res, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "幫我查價格", Source: SourceBot, LowPriority: true, Queue: true,
		TaskMeta: json.RawMessage(`{"from_bot":"b2","intent":"request"}`)})
	js := chain(t, f, res.TaskID)
	if len(js) != 2 || js[0].Verdict != judge.OnlyPromised || js[1].Verdict != judge.Done {
		t.Fatalf("%+v", js)
	}
	cont, _ := f.db.GetTask(ctx, js[0].ContinuedBy)
	var m map[string]any
	_ = json.Unmarshal(cont.Meta, &m)
	if cont.Source != SourceBot || m["from_bot"] != "b2" || m["message_id"] != "m1" || m["auto_continue"] != float64(1) {
		t.Fatalf("%+v %s", cont, cont.Meta)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || !strings.Contains(prompts[1], "小研究") {
		t.Fatalf("%q", prompts)
	}
}

// LazyBoy test_a_bot_whose_last_helper_failed_and_was_never_resent_is_woken：同一個背景子任務最多叫回去 2 次。
func TestStrandedHelperNudgedAtMostTwice(t *testing.T) {
	ctx := context.Background()
	f := setupWith(t, func(o *Options) {
		// 背景子任務回報後的那一輪才看；叫回去的那一輪（自動續做）不是。
		o.Stranded = func(_ context.Context, task store.Task) ([]string, bool) {
			if task.Input != "背景工作完成，回報結果" {
				return nil, false
			}
			return []string{"研究其他台灣平台"}, true
		}
	})
	var mu sync.Mutex
	var prompts []string
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		mu.Lock()
		prompts = append(prompts, tr.Text)
		mu.Unlock()
		tr.Complete("聯盟網結果已到；只剩下其他平台的子任務失敗，尚未重派。", "complete", nil)
	}
	for i := 0; i < 3; i++ {
		res, err := f.rt.Chat(ctx, f.bot.ID, "", "背景工作完成，回報結果")
		if err != nil {
			t.Fatal(err)
		}
		chain(t, f, res.TaskID)
	}
	mu.Lock()
	defer mu.Unlock()
	var nudges int
	for _, p := range prompts {
		if strings.Contains(p, "研究其他台灣平台") && strings.Contains(p, "message_bot") {
			nudges++
		}
	}
	if nudges != 2 {
		t.Fatalf("叫回去 %d 次：%q", nudges, prompts)
	}
}

// 用了工具、真的回答：已完成；出錯：卡住；你停掉的：不判斷。
func TestJudgmentOfOrdinaryTurns(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "回覆 OK")
	if js := chain(t, f, res.TaskID); js[0].Verdict != judge.Done {
		t.Fatalf("%+v", js)
	}
	f.g.OnPrompt = func(tr *hermestest.Turn) { tr.Complete("Error: 401", "error", map[string]any{"error": "金鑰無效"}) }
	res, _ = f.rt.Chat(ctx, f.bot.ID, "", "嗨")
	if js := chain(t, f, res.TaskID); js[0].Verdict != judge.Stuck || js[0].ContinuedBy != "" {
		t.Fatalf("%+v", js)
	}
	started := make(chan struct{})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		close(started)
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	res, _ = f.rt.Chat(ctx, f.bot.ID, "", "做很久")
	<-started
	_, _ = f.rt.Stop(ctx, f.bot.ID, "")
	if js := chain(t, f, res.TaskID); js[0] != nil {
		t.Fatalf("停掉的不判斷：%+v", js[0])
	}
}

// 目前進度（PRD §19.2）：正在做什麼、做了幾步、排隊幾件、等你什麼、等哪個隊友、最後的錯誤、最近完成。
func TestProgress(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	p, err := f.rt.Progress(ctx, f.bot.ID, "")
	if err != nil || p.State != "idle" || p.Busy || p.Current != nil || !strings.Contains(p.Text, "目前沒有任何人在工作") {
		t.Fatalf("%+v %v", p, err)
	}
	asked := make(chan struct{})
	release := make(chan struct{})
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		if tr.Text != "比三家價格\n細節略" {
			hermestest.ReplyOK(tr)
			return
		}
		tr.Event("message.start", nil)
		tr.Event("tool.start", map[string]any{"tool_id": "a", "name": "web_search"})
		tr.Event("tool.complete", map[string]any{"tool_id": "a", "name": "web_search", "result": "ok"})
		tr.Event("tool.start", map[string]any{"tool_id": "b", "name": "browser"})
		close(asked)
		<-release
		tr.Event("tool.complete", map[string]any{"tool_id": "b", "name": "browser", "result": "ok"})
		tr.Complete("比完了。", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "比三家價格\n細節略")
	<-asked
	// 它交給隊友、還沒回來的請求。
	other, _ := f.db.CreateBot(ctx, store.Bot{Name: "小研究"})
	if _, err := f.db.AddBotMessage(ctx, store.BotMessage{From: f.bot.ID, To: other.ID, ToName: "小研究", Intent: "request", Text: "查 A 店", OriginTaskID: res.TaskID}); err != nil {
		t.Fatal(err)
	}
	queued, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "隊友的事", Source: SourceBot, LowPriority: true, Queue: true})
	waitFor(t, "兩步", func() bool {
		p, _ = f.rt.Progress(ctx, f.bot.ID, "")
		return p.Current != nil && p.Current.Steps == 2
	})
	if p.State != "working" || !p.Busy || p.Queued != 1 || p.Current.Title != "比三家價格" || p.Current.From != "你交辦" || p.Current.Step != "browser" ||
		len(p.WaitingOn) != 1 || p.WaitingOn[0].Bot != "小研究" {
		t.Fatalf("%+v %+v", p, p.Current)
	}
	if !strings.Contains(p.Text, "正在做「比三家價格」") || !strings.Contains(p.Text, "另有 1 件排隊") || !strings.Contains(p.Text, "小研究") || !strings.Contains(p.Text, "還有人在工作") {
		t.Fatal(p.Text)
	}
	close(release)
	f.wait(t, res.TaskID)
	f.wait(t, queued.TaskID)
	p, _ = f.rt.Progress(ctx, f.bot.ID, "")
	if p.Last == nil || p.Last.Judge == nil || p.Current != nil {
		t.Fatalf("%+v", p)
	}
}

// 自動繼續用完還是沒做完：請老師看一次（Unfinished，學到一件事）；隊友、群組交辦的不請。
func TestUnfinishedAfterAutoContinueGivesUp(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var got []string
	f := setupWith(t, func(o *Options) {
		o.Unfinished = func(_ context.Context, taskID, why string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, taskID+"|"+why)
		}
	})
	f.g.OnPrompt = func(tr *hermestest.Turn) { tr.Complete("我來查一下", "complete", nil) }
	res, _ := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "查價格", Source: SourceBot, LowPriority: true, Queue: true})
	f.wait(t, res.TaskID)
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "查價格")
	if err != nil {
		t.Fatal(err)
	}
	js := chain(t, f, res.TaskID)
	lastID := res.TaskID
	for _, j := range js[:len(js)-1] {
		lastID = j.ContinuedBy
	}
	last := js[len(js)-1]
	waitFor(t, "請老師看", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if len(js) < 2 || !strings.HasPrefix(got[0], lastID+"|") || last.Verdict != judge.OnlyPromised || last.ContinuedBy != "" {
		t.Fatalf("要是最後一輪：%v %+v", got, js)
	}
}

// Hermes 一直重複同一個工具、自己停下這一輪時回的英文固定訊息：對話裡換成中文，判成卡住，自己換做法接著做。
func TestHermesLoopHaltContinuesInChinese(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	s := &script{reply: func(_ string, n int) (string, bool, string) {
		if n == 1 {
			return "I stopped retrying because I kept running web_search 50 times without making progress. The last result above shows what blocked it. Tell me how you'd like to proceed, or send `continue` and I'll try a different approach.", true, ""
		}
		return "做好了，表格存在 /workspace/bench/trash-bins.md。", true, ""
	}}
	f.g.OnPrompt = s.on
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我比價存成表格")
	if err != nil {
		t.Fatal(err)
	}
	js := chain(t, f, res.TaskID)
	if len(js) != 2 || js[0].Verdict != judge.Stuck || js[0].ContinuedBy == "" || js[1].Verdict != judge.Done {
		t.Fatalf("%+v", js)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if got := msgs[1].Content; got != "我一直重複用 web_search（50 次）都沒有新進展，先停下來。" {
		t.Fatalf("回覆：%q", got)
	}
	if seen := s.seen(); len(seen) < 2 || !strings.Contains(seen[1], "不要再送同樣的呼叫") {
		t.Fatalf("續做的提示：%q", seen)
	}
}

// 模型或連線的暫時錯誤（2026-10-19 水哥哥：「Model generated invalid tool call」）：自動再做一次，回覆裡用白話說；
// 只試一次，再失敗就停。額度、登入這類不試（quota 測試另外驗）。
func TestTransientErrorIsRetriedOnce(t *testing.T) {
	ctx := context.Background()
	for _, again := range []bool{false, true} {
		f := setup(t)
		var mu sync.Mutex
		var prompts []string
		f.g.OnPrompt = func(tr *hermestest.Turn) {
			mu.Lock()
			prompts = append(prompts, tr.Text)
			n := len(prompts)
			mu.Unlock()
			tr.Event("message.start", nil)
			if n == 1 || again {
				// Hermes 失敗時的 text 是寫到一半的回覆加上英文說明，錯誤本身在 error。
				tr.Event("message.delta", map[string]any{"text": "我先查一下資料，"})
				tr.Complete("我先查一下資料，\n\nThe model provider failed. Send /retry, or switch models with /model.", "error",
					map[string]any{"error": "Model generated invalid tool call: tool_call"})
				return
			}
			tr.Complete("刪好了：兩個團隊、三個群組。", "complete", nil)
		}
		res, err := f.rt.Chat(ctx, f.bot.ID, "", "幫我把團隊跟群組都刪掉")
		if err != nil {
			t.Fatal(err)
		}
		js := chain(t, f, res.TaskID)
		first, _ := f.db.GetTask(ctx, res.TaskID)
		if first.Verdict != VerdictFailed || len(js) != 2 || js[0].ContinuedBy == "" {
			t.Fatalf("again=%v：第一輪 %+v，判斷 %+v", again, first, js)
		}
		msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
		var said []string
		for _, m := range msgs {
			said = append(said, m.Content)
		}
		all := strings.Join(said, "\n")
		if !strings.Contains(all, "我先查一下資料，\n\n剛才被模型或連線的暫時錯誤打斷了，我自動再試一次。（Model generated invalid tool call") ||
			strings.Contains(all, "Send /retry") {
			t.Fatalf("要用白話說會再試，引用的是錯誤本身：%q", said)
		}
		// 自動再試時不發「工作沒有完成」的卡。
		if items, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{Kinds: []string{"error"}, TaskID: res.TaskID}); len(items) != 0 {
			t.Fatalf("不該有沒完成的卡：%+v", items)
		}
		mu.Lock()
		got := append([]string(nil), prompts...)
		mu.Unlock()
		if len(got) != 2 || !strings.HasPrefix(got[1], "（系統自動繼續）剛才這一輪被模型或連線的暫時錯誤打斷了") {
			t.Fatalf("again=%v：送出的 %q", again, got)
		}
		second, _ := f.db.GetTask(ctx, js[0].ContinuedBy)
		want := VerdictAnswer
		if again {
			want = VerdictFailed // 再失敗就停，不再試第三次
		}
		if second.Verdict != want || (js[1] != nil && js[1].ContinuedBy != "") {
			t.Fatalf("again=%v：第二輪 %+v %+v", again, second, js[1])
		}
	}
}

// 不是暫時的錯誤（例：工具真的失敗、模型拒絕）不自動再試。
func TestOtherErrorsAreNotRetried(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Complete("", "error", map[string]any{"error": "找不到這個檔案"})
	}
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "打開報告")
	if err != nil {
		t.Fatal(err)
	}
	if js := chain(t, f, res.TaskID); len(js) != 1 || (js[0] != nil && js[0].ContinuedBy != "") {
		t.Fatalf("%+v", js)
	}
}
