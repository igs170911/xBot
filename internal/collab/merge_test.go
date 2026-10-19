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
