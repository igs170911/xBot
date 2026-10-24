package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/judge"
	"xbot/internal/store"
)

// 2026-10-24：「要不要你處理」原本從回覆找關鍵字猜。「你想要幾人團隊？」一個關鍵字都沒有，判成已完成、沒有卡。
// Bot 用 xbot.chat.wait_for_user 自己說在等你：判成「需要你」，卡片寫它那一句、帶它的選項，對話裡的判斷也帶選項；你回話卡就收掉。
func TestBotSaysItIsWaitingForYou(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	const reply = "3 人版是主管兼文案、研究兼驗證、資料整理；5 人版每個角色各一位。你想要幾人？"
	wait := true
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		if strings.Contains(tr.Text, "3 人版") {
			tr.Complete("好，照 3 人版建。", "complete", nil)
			return
		}
		if wait {
			if peer, ok := f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "要建 3 人版還是 5 人版？", Choices: []string{"3 人版", "5 人版"}}); peer || !ok {
				t.Errorf("WaitForUser：peer=%v ok=%v", peer, ok)
			}
		}
		tr.Complete(reply, "complete", nil)
	}
	// 沒說在等你：關鍵字猜不到，判成已完成。
	wait = false
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "先跟我討論，行銷團隊要幾個人")
	if js := chain(t, f, res.TaskID); js[0].Verdict != judge.Done {
		t.Fatalf("沒說在等你：%+v", js[0])
	}
	// 說了：需要你、帶選項、開卡。
	wait = true
	res, _ = f.rt.Chat(ctx, f.bot.ID, "", "先跟我討論，行銷團隊要幾個人")
	js := chain(t, f, res.TaskID)
	if len(js) != 1 || js[0].Verdict != judge.NeedsPerson || js[0].ContinuedBy != "" || strings.Join(js[0].Choices, "|") != "3 人版|5 人版" {
		t.Fatalf("說了在等你：%+v", js)
	}
	task, _ := f.db.GetTask(ctx, res.TaskID)
	var meta struct {
		Waiting *WaitAsk `json:"waiting"`
	}
	if _ = json.Unmarshal(task.Meta, &meta); meta.Waiting == nil || meta.Waiting.Ask != "要建 3 人版還是 5 人版？" {
		t.Fatalf("task.meta.waiting：%s", task.Meta)
	}
	if in, ok, _ := ReplayInput(ctx, f.db, task); !ok || in.Waiting != "要建 3 人版還是 5 人版？" {
		t.Fatalf("重播要讀得到：%+v", in)
	}
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	var mm struct {
		Judge *Judgment `json:"judge"`
	}
	_ = json.Unmarshal(msgs[len(msgs)-1].Meta, &mm)
	if mm.Judge == nil || len(mm.Judge.Choices) != 2 {
		t.Fatalf("回覆的判斷要帶選項：%s", msgs[len(msgs)-1].Meta)
	}
	open, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen})
	if len(open) != 1 || open[0].Kind != "question" || open[0].Body != "要建 3 人版還是 5 人版？" || open[0].ThreadID != res.ThreadID {
		t.Fatalf("卡片：%+v", open)
	}
	var p struct {
		Choices []string `json:"choices"`
	}
	if _ = json.Unmarshal(open[0].Payload, &p); len(p.Choices) != 2 {
		t.Fatalf("卡片的選項：%s", open[0].Payload)
	}
	// 你點了選項（就是在那條對話回話）：卡收掉，下一輪沒說在等你就是做完了。
	res2, _ := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "3 人版")
	if js := chain(t, f, res2.TaskID); js[0].Verdict != judge.Done || len(js[0].Choices) != 0 {
		t.Fatalf("回話之後：%+v", js[0])
	}
	if it, _ := f.db.GetInboxItem(ctx, open[0].ID); it.Status != store.InboxResolved {
		t.Fatalf("卡要收掉：%+v", it)
	}
}

// 你已經交代了，它卻用 wait_for_user 問「要我建團隊嗎？」：照樣叫它自己做完（10/21 連續四次「要我建團隊嗎」）。
func TestWaitingToAskPermissionForWhatYouAskedIsSentBack(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		if strings.HasPrefix(tr.Text, "幫我建") {
			f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "名單整理好了，要我建團隊嗎？"})
			tr.Complete("名單整理好了，要我建團隊嗎？", "complete", nil)
			return
		}
		tr.Complete("團隊建好了。", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "幫我建一個三人的行銷團隊")
	js := chain(t, f, res.TaskID)
	if len(js) != 2 || js[0].Verdict != judge.HandedBack || js[0].ContinuedBy == "" || js[1].Verdict != judge.Done {
		t.Fatalf("%+v", js)
	}
	if open, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen}); len(open) != 0 {
		t.Fatalf("不開卡：%+v", open)
	}
}

// 它說在等你、你在它還在做的時候就回了（插話）：等的就是這則，這一輪結束不算在等你。
func TestWaitClearedWhenYouReplyMidTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "要建 3 人版還是 5 人版？"})
		tr.Event("tool.start", map[string]any{"tool_id": "c1", "name": "write_file"})
		<-tr.Steers()
		tr.Event("tool.complete", map[string]any{"tool_id": "c1", "name": "write_file", "result": "ok"})
		tr.Complete("好，照 3 人版建。", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "先跟我討論，行銷團隊要幾個人")
	waitKind(t, sub, KindToolCall)
	if more, err := f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "3 人版"); err != nil || !more.Steered {
		t.Fatalf("插話：%+v %v", more, err)
	}
	if js := chain(t, f, res.TaskID); js[0].Verdict != judge.Done {
		t.Fatalf("%+v", js[0])
	}
	task, _ := f.db.GetTask(ctx, res.TaskID)
	if strings.Contains(string(task.Meta), "waiting") {
		t.Fatalf("task.meta.waiting 要清掉：%s", task.Meta)
	}
}

// 隊友交辦的一輪說在等使用者：系統不開卡（交辦的隊友會轉告）；沒有進行中的一輪時 ok=false。
func TestWaitForUserInATeammateTurn(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	if _, ok := f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "要哪個？"}); ok {
		t.Fatal("沒有進行中的一輪")
	}
	var peer bool
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		peer, _ = f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "要使用者登入 Threads", Choices: []string{"好"}})
		tr.Complete("要使用者登入 Threads 才查得到。", "complete", nil)
	}
	res, err := f.rt.Start(ctx, ChatRequest{BotID: f.bot.ID, Text: "查 Threads 上的熱門貼文", Source: SourceBot, Sender: "bot:x"})
	if err != nil {
		t.Fatal(err)
	}
	j := chain(t, f, res.TaskID)[0]
	if !peer || j.Verdict != judge.NeedsPerson || len(j.Choices) != 0 {
		t.Fatalf("peer=%v %+v", peer, j)
	}
	if open, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen}); len(open) != 0 {
		t.Fatalf("隊友交辦的不開卡：%+v", open)
	}
}

// deepseek 把選項和推薦寫在呼叫 wait_for_user 的同一步，工具回來後只補一句「你點一個就好」：兩段都是回答
// （照工具切開的話，前面不到 200 字的那段會被當成旁白丟掉，你只看到最後那句）。真的工具前的旁白照樣不算。
func TestTextAroundWaitForUserIsOneAnswer(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("message.delta", map[string]any{"text": "我先看一下。"})
		tr.Event("tool.start", map[string]any{"tool_id": "c1", "name": "web_search"})
		tr.Event("tool.complete", map[string]any{"tool_id": "c1", "name": "web_search", "result": "ok"})
		tr.Event("message.delta", map[string]any{"text": "三個名字：小格、阿限、米格。我推小格。"})
		tr.Event("tool.start", map[string]any{"tool_id": "w1", "name": "mcp__xbot__xbot_chat_wait_for_user"})
		f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "IG Bot 要叫哪個名字？", Choices: []string{"小格", "阿限", "米格"}})
		tr.Event("tool.complete", map[string]any{"tool_id": "w1", "name": "mcp__xbot__xbot_chat_wait_for_user", "result": "ok"})
		tr.Event("message.delta", map[string]any{"text": "你點一個就好。"})
		tr.Complete("你點一個就好。", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "幫我想三個 IG Bot 的名字讓我挑")
	f.wait(t, res.TaskID)
	msgs, _ := f.db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if got := msgs[len(msgs)-1].Content; got != "三個名字：小格、阿限、米格。我推小格。\n\n你點一個就好。" {
		t.Fatalf("回覆：%q", got)
	}
}

// 2026-10-24 蝦皮註冊實測：Bot 用「請你接手電腦」開了接手卡，回覆裡講「只有你本人能做」又被猜成需要你，多一張卡。
// 已經有要你動手的卡（核准、接手、連接應用程式）就不再從回覆猜一張；Bot 自己說在等你的照開（那是它另外要問的事）。
func TestNoGuessedCardNextToATakeoverButExplicitWaitStillOpens(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	explicit := false
	f.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		if _, err := f.db.CreateInboxItem(ctx, store.InboxItem{Kind: "takeover", BotID: f.bot.ID, Title: "小幫手需要你接手電腦"}); err != nil {
			t.Error(err)
		}
		if explicit {
			f.rt.WaitForUser(ctx, f.bot.ID, WaitAsk{Ask: "註冊好之後賣場要叫什麼名字？"})
		}
		tr.Complete("註冊卡在手機簡訊驗證碼，這關只有你本人能做，請你接手填一下。", "complete", nil)
	}
	res, _ := f.rt.Chat(ctx, f.bot.ID, "", "幫我把蝦皮賣場註冊起來")
	if js := chain(t, f, res.TaskID); js[0].Verdict != judge.NeedsPerson {
		t.Fatalf("%+v", js[0])
	}
	open, _ := f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen, Kinds: []string{"question"}})
	if len(open) != 0 {
		t.Fatalf("有接手卡就不再猜一張：%+v", open)
	}
	explicit = true
	res, _ = f.rt.Chat(ctx, f.bot.ID, "", "幫我把蝦皮賣場註冊起來")
	chain(t, f, res.TaskID)
	open, _ = f.db.ListInboxFiltered(ctx, store.InboxFilter{BotID: f.bot.ID, Status: store.InboxOpen, Kinds: []string{"question"}})
	if len(open) != 1 || open[0].Body != "註冊好之後賣場要叫什麼名字？" {
		t.Fatalf("它自己說在等你的照開：%+v", open)
	}
}
