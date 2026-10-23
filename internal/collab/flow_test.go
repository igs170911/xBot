package collab

import (
	"fmt"
	"strings"
	"testing"

	"xbot/internal/limits"
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

// 在群組裡交辦出去的結果回來了（2026-10-21「我不問你都不講?」；一小時 14 則進度、要你決定的事第 60 分鐘才說）：
// 還在等別人時，只有「需要你決定：」才先貼；全部回來才一起回報。又交辦出去的那一輪不貼、[silent] 不貼；貼出來的不叫醒任何人。
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
	posts := func() []store.Message { return tm.roomLog(room.ID)[before:] }

	// Writer 先交回：還在等 Scout。一般進度不貼；「需要你決定：」先貼。
	w := tm.queued(writer)[0]
	tm.start(w)
	tm.finish(w.ID, runtime.VerdictAnswer, "文案 v1 在 /workspace/a.md")
	r1 := tm.results(chief)[0]
	if p := tm.start(r1); !strings.Contains(p, "還在等：Scout") || !strings.Contains(p, "需要你決定：") {
		t.Fatalf("要告訴 Chief 還在等誰、什麼時候才貼：%s", p)
	}
	tm.finish(r1.ID, runtime.VerdictAnswer, "文案 v1 到了，Scout 還在查。")
	if got := posts(); len(got) != 0 {
		t.Fatalf("還在等別人的進度不貼：%+v", got)
	}
	w2 := tm.queued(writer)
	if len(w2) != 0 {
		t.Fatal("不叫醒任何人")
	}

	// Scout 交回：Chief 這一輪又交辦給 Writer、但有要你決定的事：先貼。
	sc := tm.queued(scout)[0]
	tm.start(sc)
	tm.finish(sc.ID, runtime.VerdictAnswer, "競品三個在 /workspace/b.md")
	r2 := tm.results(chief)[1]
	if p := tm.start(r2); !strings.Contains(p, "都回來了") || !strings.Contains(p, "會貼到群組") {
		t.Fatalf("都回來了要說會貼：%s", p)
	}
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "照競品改第二版", IntentRequest, SendOptions{}))
	tm.finish(r2.ID, runtime.VerdictAnswer, "需要你決定：定價走 A 案（1,990）還是 B 案（2,490）？我建議 A。\n\n文案已請 Writer 照競品改第二版。")
	if got := posts(); len(got) != 1 || !strings.HasPrefix(got[0].Content, "需要你決定：") || roomMetaOf(got[0]).SpeakerName != "Chief" {
		t.Fatalf("要你決定的事先貼：%+v", got)
	}

	// Writer 交回第二版：全部回來了，整合後的回答貼到群組；[silent] 不貼。
	w3 := tm.queued(writer)[0]
	tm.start(w3)
	tm.finish(w3.ID, runtime.VerdictAnswer, "第二版在 /workspace/a2.md")
	r3 := tm.results(chief)[2]
	tm.start(r3)
	tm.finish(r3.ID, runtime.VerdictAnswer, "第二版文案和競品比較都好了：/workspace/a2.md、/workspace/b.md。")
	if got := posts(); len(got) != 2 || !strings.Contains(got[1].Content, "第二版文案") {
		t.Fatalf("全部回來才一起回報：%+v", got)
	}
	if len(tm.queued(writer))+len(tm.queued(scout)) != 0 {
		t.Fatal("貼回群組不叫醒別人")
	}
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Scout", "再查一個", IntentRequest, SendOptions{Origin: &r3}))
	sc2 := tm.queued(scout)[0]
	tm.start(sc2)
	tm.finish(sc2.ID, runtime.VerdictAnswer, "第四個競品在 /workspace/c.md")
	r4 := tm.results(chief)[3]
	tm.start(r4)
	tm.finish(r4.ID, runtime.VerdictAnswer, "[silent]")
	if got := posts(); len(got) != 2 {
		t.Fatalf("[silent] 不貼：%+v", got)
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

// 同一件工作第三次請同一位隊友做事：照送，但提醒能用就採用、意見一次寫齊（2026-10-21 文案 52 分鐘改了六版）。
func TestRepeatedRequestsToTheSameTeammateGetANudge(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	origin := tm.running(chief, nil)
	o := tm.task(origin)
	for i := 1; i <= limits.PeerRepeatNote; i++ {
		res := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", fmt.Sprintf("改第 %d 版", i), IntentRequest, SendOptions{Origin: &o}))
		nudged := strings.Contains(res.Note, "一次寫齊")
		if nudged != (i >= limits.PeerRepeatNote) {
			t.Fatalf("第 %d 次：%s", i, res.Note)
		}
	}
	other := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Scout", "查一下", IntentRequest, SendOptions{Origin: &o}))
	if strings.Contains(other.Note, "一次寫齊") {
		t.Fatal("別的隊友不算")
	}
}

// 主管這一輪已經傳話交辦給研究，群組裡又 @研究「你負責第一棒」：只是告訴大家誰做什麼，不再叫醒研究做一次（2026-10-23 實測）。
func TestGroupMentionDoesNotWakeSomeoneAlreadyAsked(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{Name: "行銷團隊"}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 分工下去做", ""))
	turn := tm.queued(chief)[0]
	tm.start(turn)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Scout", "查三個競品", IntentRequest, SendOptions{}))
	tm.finish(turn.ID, runtime.VerdictAnswer, "@Scout 你負責第一棒：查三個競品。@Writer 等研究回來再寫。")
	if q := tm.queued(scout); len(q) != 1 || metaOf(q[0]).RoomID != "" {
		t.Fatalf("Scout 只有傳話那一件，不再被群組叫醒：%+v", q)
	}
	if q := tm.queued(writer); len(q) != 1 || metaOf(q[0]).RoomID != room.ID {
		t.Fatalf("沒有交辦過的 Writer 照常叫醒：%+v", q)
	}
}
