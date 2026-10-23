package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// 只有道謝、收到的話：不叫醒別人、不必傳給隊友（2026-10-22 實測一句話引出 11 則道謝）。
// 「好、可以、是」可能是在回答問題，不算。
func TestIsThanksOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"謝謝！":             true,
		"@Writer 謝謝你 🙏":   true,
		"收到，感謝大家～":        true,
		"不客氣！辛苦了":         true,
		"Thanks, got it.": true,
		"收到了喔":            true,
		"好":               false,
		"可以。":             false,
		"好的，謝謝":           false,
		"收到，我下午三點前交":      false,
		"謝謝 @Writer 的整理，資料很完整":   false,
		"謝謝，請再補一段結論":             false,
		"":                       false,
		"[silent]":               false,
		strings.Repeat("謝謝", 30): false,
	} {
		if got := IsThanksOnly(text); got != want {
			t.Errorf("IsThanksOnly(%q) = %v，要 %v", text, got, want)
		}
	}
}

// 群組裡只有道謝：照樣貼出來，但不叫醒被 @ 的人；有內容的照常交棒。
func TestThanksInAGroupWakesNobody(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "發表會"}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 規劃發表會", ""))
	turn := tm.queued(chief)[0]
	tm.start(turn)
	tm.finish(turn.ID, runtime.VerdictAnswer, "@Writer 謝謝你！🙏")
	log := tm.roomLog(room.ID)
	if got := log[len(log)-1]; got.Content != "@Writer 謝謝你！🙏" {
		t.Fatalf("道謝照樣貼出來：%+v", got)
	}
	if q := tm.queued(writer); len(q) != 0 {
		t.Fatalf("只有道謝不叫醒 Writer：%+v", q)
	}
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 再來", ""))
	turn = tm.queued(chief)[0]
	tm.start(turn)
	tm.finish(turn.ID, runtime.VerdictAnswer, "@Writer 好，請開始寫新聞稿")
	if q := tm.queued(writer); len(q) != 1 {
		t.Fatal("有內容的照常交棒")
	}
	// Bot 用 post_to_group 道謝也一樣。
	tm.running(chief, nil)
	res := must[PostGroupResult](t)(tm.s.PostGroup(tm.ctx, chief, room.ID, "@Writer 收到，感謝"))
	if len(res.Woke) != 0 || !strings.Contains(res.Note, "沒有叫醒") {
		t.Fatalf("%+v", res)
	}
}

// Bot 傳給隊友的只有道謝：不送，告訴它不用傳。
func TestSendRefusesThanksOnly(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	tm.running(chief, nil)
	if _, err := tm.s.Send(tm.ctx, chief, "Writer", "謝謝！辛苦了", IntentRequest, SendOptions{}); err == nil || !strings.Contains(err.Error(), "不用傳") {
		t.Fatalf("要拒絕：%v", err)
	}
	if len(tm.queued(tm.bots["Writer"])) != 0 {
		t.Fatal("不該叫醒 Writer")
	}
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "謝謝，請再補一段結論", IntentRequest, SendOptions{}))
}

// 在群組裡交辦出去的結果回來了：整合後貼到群組（不然你只能自己去問，2026-10-21「我不問你都不講?」）。
// 又交辦出去的那一輪不貼、[silent] 不貼；貼出來的不叫醒任何人。
func TestDelegatedResultsFromAGroupArePostedBack(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{Name: "行銷團隊"}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 分工下去做", ""))
	turn := tm.queued(chief)[0]
	tm.start(turn)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫一版文案", IntentRequest, SendOptions{}))
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Scout", "查三個競品", IntentRequest, SendOptions{}))
	tm.finish(turn.ID, runtime.VerdictAnswer, "已經分給 Writer 和 Scout。")
	before := len(tm.roomLog(room.ID))

	// Writer 交回：Chief 整合後的回答貼到群組，不叫醒任何人。
	w := tm.queued(writer)[0]
	tm.start(w)
	tm.finish(w.ID, runtime.VerdictAnswer, "文案 v1 在 /workspace/a.md")
	got := tm.results(chief)
	if len(got) != 1 {
		t.Fatalf("Chief 要收到 Writer 的回覆：%+v", got)
	}
	prompt := tm.start(got[0])
	if !strings.Contains(prompt, "會貼到群組") || !strings.Contains(prompt, "行銷團隊") {
		t.Fatalf("要告訴 Chief 這次的回答會貼到群組：%s", prompt)
	}
	tm.finish(got[0].ID, runtime.VerdictAnswer, "@Writer 文案 v1 到了；Scout 的競品還在查。")
	log := tm.roomLog(room.ID)
	if len(log) != before+1 || log[len(log)-1].Content != "@Writer 文案 v1 到了；Scout 的競品還在查。" || roomMetaOf(log[len(log)-1]).SpeakerName != "Chief" {
		t.Fatalf("要貼到群組：%+v", log[before:])
	}
	if len(tm.queued(writer)) != 0 {
		t.Fatal("貼回群組不叫醒別人")
	}

	// Scout 交回：Chief 這一輪又交辦給 Writer，不貼；Writer 再交回時才貼。[silent] 不貼。
	sc := tm.queued(scout)[0]
	tm.start(sc)
	tm.finish(sc.ID, runtime.VerdictAnswer, "競品三個在 /workspace/b.md")
	r2 := tm.results(chief)[1]
	tm.start(r2)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "照競品改第二版", IntentRequest, SendOptions{}))
	tm.finish(r2.ID, runtime.VerdictAnswer, "已請 Writer 改第二版。")
	if len(tm.roomLog(room.ID)) != before+1 {
		t.Fatal("又交辦出去的那一輪不貼")
	}
	w2 := tm.queued(writer)[0]
	tm.start(w2)
	tm.finish(w2.ID, runtime.VerdictAnswer, "第二版在 /workspace/a2.md")
	r3 := tm.results(chief)[2]
	tm.start(r3)
	tm.finish(r3.ID, runtime.VerdictAnswer, "[silent]")
	if len(tm.roomLog(room.ID)) != before+1 {
		t.Fatal("[silent] 不貼")
	}
}

// 在一對一對話裡交辦的：結果本來就在你看得到的對話裡，不貼到任何群組。
func TestDelegatedResultsFromAChatStayInTheChat(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "發表會"}))
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫一版文案", IntentRequest, SendOptions{}))
	w := tm.queued(writer)[0]
	tm.start(w)
	tm.finish(w.ID, runtime.VerdictAnswer, "文案 v1 在 /workspace/a.md")
	r := tm.results(chief)[0]
	if p := tm.start(r); strings.Contains(p, "貼到群組") {
		t.Fatalf("%s", p)
	}
	tm.finish(r.ID, runtime.VerdictAnswer, "文案到了。")
	if log := tm.roomLog(room.ID); len(log) != 0 {
		t.Fatalf("不貼到群組：%+v", log)
	}
}

// 群組的一輪被模型或連線的暫時錯誤打斷：不把錯誤貼到群組，自動重排一次；再失敗才用白話說。
func TestATransientFailureInAGroupIsRetriedOnce(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "發表會"}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 規劃發表會", ""))
	before := len(tm.roomLog(room.ID))
	turn := tm.queued(chief)[0]
	tm.start(turn)
	tm.finish(turn.ID, runtime.VerdictFailed, "HTTP 429: Upstream request failed: Endpoint is unavailable.")
	if len(tm.roomLog(room.ID)) != before {
		t.Fatal("暫時的錯誤不貼到群組")
	}
	q := tm.queued(chief)
	if len(q) != 1 || !metaOf(q[0]).Retried || metaOf(q[0]).Cause != metaOf(turn).Cause {
		t.Fatalf("要自動重排一輪：%+v", q)
	}
	if p := tm.start(q[0]); !strings.Contains(p, "規劃發表會") {
		t.Fatalf("重排的那一輪要從原本的地方讀起：%s", p)
	}
	tm.finish(q[0].ID, runtime.VerdictFailed, "HTTP 429: Upstream request failed: Endpoint is unavailable.")
	log := tm.roomLog(room.ID)
	if len(log) != before+1 || !strings.HasPrefix(log[len(log)-1].Content, "（這次沒有完成：模型或連線暫時出錯，自動再試也沒成功）") {
		t.Fatalf("再失敗才貼，用白話說：%+v", log[before:])
	}
	if len(tm.queued(chief)) != 0 {
		t.Fatal("只重排一次")
	}
	// 不是暫時的錯誤：照原本貼出來，不重排。
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 再試", ""))
	turn = tm.queued(chief)[0]
	tm.start(turn)
	tm.finish(turn.ID, runtime.VerdictFailed, "找不到檔案 /workspace/x.md")
	log = tm.roomLog(room.ID)
	if got := log[len(log)-1].Content; got != "（這次沒有完成）找不到檔案 /workspace/x.md" || len(tm.queued(chief)) != 0 {
		t.Fatalf("%q", got)
	}
}

// 隊友交辦的工作被暫時的錯誤打斷、系統自動再做一次（runtime 記在 judge.continued_by）：
// 不把錯誤送回交辦人，交辦與傳話改記在再做的那一輪，那一輪的回答才送回去。
func TestATeammatesRetryCarriesTheAnswerBack(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	sent := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫一版文案", IntentRequest, SendOptions{}))
	w := tm.queued(writer)[0]
	tm.start(w)
	retry, err := tm.db.CreateTask(tm.ctx, store.Task{BotID: writer, ThreadID: w.ThreadID, Source: runtime.SourceBot, Input: "（系統自動繼續）", Meta: w.Meta})
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.db.PatchTaskMeta(tm.ctx, w.ID, map[string]any{"judge": map[string]any{"verdict": "failed", "continued_by": retry.ID}}); err != nil {
		t.Fatal(err)
	}
	tm.finish(w.ID, runtime.VerdictFailed, "HTTP 429: Upstream request failed")
	if got := tm.results(chief); len(got) != 0 {
		t.Fatalf("不把錯誤送回交辦人：%+v", got)
	}
	msg := must[store.BotMessage](t)(tm.db.GetBotMessage(tm.ctx, sent.MessageID))
	if msg.TaskID != retry.ID || msg.Answered {
		t.Fatalf("傳話改記在再做的那一輪：%+v", msg)
	}
	tm.start(retry)
	tm.finish(retry.ID, runtime.VerdictAnswer, "文案 v1 在 /workspace/a.md")
	if got := tm.results(chief); len(got) != 1 || !strings.Contains(got[0].Input, "文案 v1") {
		t.Fatalf("再做的那一輪的回答送回去：%+v", got)
	}
}

// 訊息裡提到的檔案在寄件者電腦找不到：當下告訴寄件者哪些沒帶過去（不然對方回「我這邊是空的」，又多來回幾次）。
// 「/workspace/from/<寄件者>/…」是對方那邊的位置，不算找不到。
func TestSendTellsTheSenderWhichFilesWereNotCarried(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	tm.write(chief, "plan.md", "計畫")
	tm.running(chief, nil)
	res := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer",
		"先讀 /workspace/plan.md、/workspace/plan_v2.md。到你那邊會在 /workspace/from/Chief/plan.md", IntentRequest, SendOptions{}))
	if res.Files != 1 || len(res.NotCarried) != 1 || res.NotCarried[0] != "/workspace/plan_v2.md" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Note, "/workspace/plan_v2.md") || !strings.Contains(res.Note, "再傳一則補上") {
		t.Fatalf("要告訴寄件者：%s", res.Note)
	}
	ok := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "只讀 /workspace/plan.md", IntentFYI, SendOptions{}))
	if len(ok.NotCarried) != 0 || strings.Contains(ok.Note, "注意") {
		t.Fatalf("都帶到了就不提：%+v", ok)
	}
}
