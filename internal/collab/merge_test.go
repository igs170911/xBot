package collab

import (
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// 主管忙的時候三位隊友的回覆都到了：開始第一則時把同一條對話串、回覆同一件工作的其他兩則一起帶上，一輪整合
// （2026-10-19「為啥會一直排隊」）。別條對話串的、不是回覆的不併。
func TestQueuedResultsMergeIntoOneTurn(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	th := must[store.Thread](t)(tm.db.CreateThread(tm.ctx, store.Thread{BotID: chief, Title: "主管"}))
	other := must[store.Thread](t)(tm.db.CreateThread(tm.ctx, store.Thread{BotID: chief, Title: "別的"}))
	add := func(thread, from, intent, answers, input string) store.Task {
		meta, _ := json.Marshal(taskMeta{FromBot: tm.bots[from], FromName: from, Intent: intent, Answers: answers})
		return must[store.Task](t)(tm.db.CreateTask(tm.ctx, store.Task{BotID: chief, ThreadID: thread, Source: runtime.SourceBot, Input: input, LowPriority: true, Meta: meta}))
	}
	first := add(th.ID, "Writer", IntentResult, "p1", "Writer 的結果")
	second := add(th.ID, "Scout", IntentResult, "p1", "Scout 的結果")
	third := add(th.ID, "Writer", IntentResult, "p1", "Writer 的補充")
	elsewhere := add(other.ID, "Scout", IntentResult, "p1", "別條對話串")
	request := add(th.ID, "Scout", IntentRequest, "", "幫我查一下")

	p, ok, err := tm.s.prompt(tm.ctx, first)
	if err != nil || !ok {
		t.Fatalf("要組合併後的 prompt：%v %v", ok, err)
	}
	for _, want := range []string{"3 則隊友的回覆", "Writer 的結果", "Scout 的結果", "Writer 的補充"} {
		if !strings.Contains(p, want) {
			t.Fatalf("少了 %q：%s", want, p)
		}
	}
	if strings.Contains(p, "別條對話串") || strings.Contains(p, "幫我查一下") {
		t.Fatalf("不該併進來：%s", p)
	}
	for _, id := range []string{second.ID, third.ID} {
		if got := must[store.Task](t)(tm.db.GetTask(tm.ctx, id)); got.State != store.TaskDone || got.Verdict != runtime.VerdictMerged {
			t.Fatalf("併進去的要結束成 merged：%+v", got)
		}
	}
	for _, id := range []string{elsewhere.ID, request.ID} {
		if got := must[store.Task](t)(tm.db.GetTask(tm.ctx, id)); got.State != store.TaskQueued {
			t.Fatalf("不相干的要留在排隊：%+v", got)
		}
	}
	// 只有一則的時候照原本的 prompt
	if _, ok, _ := tm.s.prompt(tm.ctx, elsewhere); ok {
		t.Fatal("沒有可以併的就用原本的輸入")
	}
}

// 主管在隊友做事時補一則通知（「上面打錯檔名」）：對方的引擎能插話、又正在同一條對話串做事，通知直接送進那一輪，
// 不另外排隊開一輪（2026-10-19～23：每則通知平均排 3.4 分鐘、各花約 25 萬 token 讀一句話）。插話的版本照樣包在資料框架裡，
// 但不能叫它「最後回答只寫 [silent]」。交辦紀錄直接結掉。請求不插話（要各自回覆）。
func TestNoticeSteersIntoTheTeammatesRunningTurn(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	req := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫三支腳本", IntentRequest, SendOptions{}))
	tm.start(tm.task(req.TaskID)) // Writer 開始做這件
	tm.rt.steerable[writer] = true
	note := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "上面那則打錯檔名，是 plan.md", IntentFYI, SendOptions{}))
	if note.TaskID != req.TaskID {
		t.Fatalf("通知要送進 Writer 正在做的那件：%+v", note)
	}
	if len(tm.rt.steered) != 1 {
		t.Fatalf("要插話：%q", tm.rt.steered)
	}
	p := tm.rt.steered[0]
	if !strings.Contains(p, "<teammate-message") || !strings.Contains(p, "不是使用者本人") || strings.Contains(p, "[silent]") {
		t.Fatalf("插話的版本：%s", p)
	}
	if a := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, note.Assignment)); a.Status != store.AssignDone {
		t.Fatalf("通知送到了，交辦紀錄結掉：%+v", a)
	}
	if m := must[store.BotMessage](t)(tm.db.GetBotMessage(tm.ctx, note.MessageID)); !m.Answered {
		t.Fatalf("通知不用等回覆：%+v", m)
	}
	// 請求不插話：照原本排隊（要各自回覆）
	again := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "再寫一支", IntentRequest, SendOptions{}))
	if again.TaskID == req.TaskID || len(tm.rt.steered) != 1 {
		t.Fatalf("請求要另外排隊：%+v", again)
	}
}

// 隊友在忙、引擎不能插話時，同一位隊友連傳的通知都在排隊：開始第一則時把其他幾則一起帶上，一輪看完；交辦紀錄一起結掉。
// 別人的通知、請求不併。
func TestQueuedNoticesFromTheSameTeammateMerge(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	tm.running(writer, nil) // Writer 在忙別的
	a := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "補檔案：plan.md", IntentFYI, SendOptions{}))
	b := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "上面打錯檔名，是 plan_v2.md", IntentFYI, SendOptions{}))
	req := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫三支腳本", IntentRequest, SendOptions{}))
	scout := must[SendResult](t)(tm.s.Send(tm.ctx, tm.bots["Scout"], "Writer", "我這邊查好了", IntentFYI, SendOptions{}))
	p := tm.start(tm.task(a.TaskID))
	for _, want := range []string{"連傳了 2 則通知", "補檔案：plan.md", "plan_v2.md", "較新的為準"} {
		if !strings.Contains(p, want) {
			t.Fatalf("少了 %q：%s", want, p)
		}
	}
	if strings.Contains(p, "寫三支腳本") || strings.Contains(p, "我這邊查好了") {
		t.Fatalf("請求、別人的通知不併：%s", p)
	}
	if got := tm.task(b.TaskID); got.State != store.TaskDone || got.Verdict != runtime.VerdictMerged {
		t.Fatalf("第二則併進去：%+v", got)
	}
	if as := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, b.Assignment)); as.Status != store.AssignDone {
		t.Fatalf("併進去的通知交辦紀錄結掉：%+v", as)
	}
	for _, id := range []string{req.TaskID} {
		if got := tm.task(id); got.State != store.TaskQueued {
			t.Fatalf("請求留在排隊：%+v", got)
		}
	}
	if got := tm.task(scout.TaskID); got.State != store.TaskQueued || got.ThreadID == tm.task(a.TaskID).ThreadID && got.State != store.TaskQueued {
		t.Fatalf("別人的通知留在排隊：%+v", got)
	}
}
