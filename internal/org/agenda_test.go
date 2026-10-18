package org

// 移植 LazyBoy tests/test_agenda.py：提案（沒意見就是同意）、討論、每週檢討的資料、行事曆。
// 行事曆裡的定時任務與學習時段（C32）、ICS 訂閱（之後的卡）還沒有，那幾個測試沒移植（見 PR「後續」）。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

func (co *company) submit(name string, in SubmitIn) SubmitOut {
	co.t.Helper()
	return must[SubmitOut](co.t)(co.s.Submit(co.ctx, co.bots[name], in))
}

func propose(subject string) SubmitIn { return SubmitIn{Kind: KindPropose, Subject: subject} }

// taipei 是台北時間 days 天後 hour 點，Bot 會寫的樣子，和它代表的 UTC 時間（LazyBoy taipei）。
func (co *company) taipei(days, hour int) (string, time.Time) {
	local := co.clock.Add(time.Duration(days) * 24 * time.Hour).In(co.s.Zone())
	local = time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, co.s.Zone())
	return local.Format("2006-01-02 15:04"), local.UTC()
}

// test_a_proposal_waits_its_days_then_silence_is_consent
func TestAProposalWaitsItsDaysThenSilenceIsConsent(t *testing.T) {
	co := setup(t)
	ceo := co.bots["CEO"]
	co.task("CEO", store.TaskRunning, nil, "整理日報")
	sent := co.submit("CEO", SubmitIn{Kind: KindPropose, Subject: "把日報改成週報", Details: "日報大多沒有新進度", Days: 2})
	if !sent.Sent || !strings.Contains(sent.Note, "視同同意") {
		t.Fatalf("%+v", sent)
	}
	rows := co.pending()
	if len(rows) != 1 || rows[0].Kind != KindPropose || rows[0].State != store.AgendaOpen || rows[0].ThreadID != co.main[ceo] {
		t.Fatalf("%+v", rows)
	}
	row := rows[0]
	if d := time.UnixMilli(row.DueAt).Sub(co.clock); d != 48*time.Hour {
		t.Fatalf("兩天後生效：%v", d)
	}
	if peerEvent(co.last(co.main[ceo])) != "proposal" {
		t.Fatal("Bot 的對話要記一行")
	}
	// 「待我處理」有一張提案卡。
	cards := must[[]store.InboxItem](t)(co.db.ListInboxByRef(co.ctx, row.ID))
	if len(cards) != 1 || cards[0].Kind != "proposal" || cards[0].Status != store.InboxOpen || !strings.Contains(cards[0].Title, "CEO 提案：把日報改成週報") {
		t.Fatalf("%+v", cards)
	}
	if again := co.submit("CEO", propose("把日報改成週報")); again.Sent {
		t.Fatal("同一件不重送")
	}
	_, err := co.s.Submit(co.ctx, ceo, SubmitIn{Kind: KindPropose, Subject: "別的事", Days: 30})
	errHas(t, err, "1 到 14")
	// 還沒到期：什麼都不做。過了生效日：叫 Bot 開始做。
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if len(co.rt.messages()) != 0 {
		t.Fatal("還沒到期")
	}
	co.clock = co.clock.Add(48*time.Hour + time.Minute)
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if a := co.agenda(row.ID); a.State != store.AgendaAuto || len(co.pending()) != 0 {
		t.Fatalf("%+v", a)
	}
	msgs := co.rt.messages()
	if len(msgs) != 1 || msgs[0].BotID != ceo || !strings.HasPrefix(msgs[0].Text, "【視同同意】把日報改成週報") || msgs[0].ThreadID != co.main[ceo] || !msgs[0].Queue {
		t.Fatalf("%+v", msgs)
	}
	if c := must[store.InboxItem](t)(co.db.GetInboxItem(co.ctx, cards[0].ID)); c.Status != store.InboxResolved || c.Resolution != store.AgendaAuto {
		t.Fatalf("卡片結掉：%+v", c)
	}
}

// test_a_proposal_that_came_due_while_nobody_could_look_gets_one_more_day
func TestAProposalThatCameDueWhileNobodyCouldLookGetsOneMoreDay(t *testing.T) {
	co := setup(t)
	id := co.submit("CEO", propose("把日報改成週報")).AgendaID
	co.setDue(id, co.clock.Add(-5*time.Hour))
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	after := co.agenda(id)
	if after.State != store.AgendaOpen || !after.Grace || len(co.rt.messages()) != 0 {
		t.Fatalf("%+v", after)
	}
	if d := time.UnixMilli(after.DueAt).Sub(co.clock); d != limits.ProposalGraceExtra {
		t.Fatalf("再一天：%v", d)
	}
	co.setDue(id, co.clock.Add(-5*time.Hour))
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if a := co.agenda(id); a.State != store.AgendaAuto || len(co.rt.messages()) != 1 {
		t.Fatalf("%+v", a)
	}
}

// test_money_and_promises_cannot_be_approved_by_silence_and_a_veto_is_remembered
func TestMoneyAndPromisesCannotBeApprovedBySilenceAndAVetoIsRemembered(t *testing.T) {
	co := setup(t)
	ceo := co.bots["CEO"]
	_, err := co.s.Submit(co.ctx, ceo, propose("新增廣告支出 NT$3,000"))
	errHas(t, err, "approvals.request")
	_, err = co.s.Submit(co.ctx, ceo, SubmitIn{Kind: KindPropose, Subject: "調整做法", Details: "下週簽約並上架新商品"})
	errHas(t, err, "approvals.request")
	// 你自己加的事項也算。
	must[[]string](t)(co.s.SetApprovalItems(co.ctx, []string{"換供應商"}))
	_, err = co.s.Submit(co.ctx, ceo, propose("下個月換供應商"))
	errHas(t, err, "換供應商")
	first := co.submit("CEO", propose("把日報改成週報")).AgendaID
	must[store.Agenda](t)(co.s.Decide(co.ctx, first, DecideVeto, "不要", ""))
	_, err = co.s.Submit(co.ctx, ceo, propose("把日報改成週報"))
	errHas(t, err, "否決過")
	if !co.submit("CEO", propose("把日報改成雙週報")).Sent {
		t.Fatal("換個做法可以再提")
	}
	// 14 天後可以再提。
	co.clock = co.clock.Add(limits.VetoMemory + time.Hour)
	if !co.submit("CEO", propose("把日報改成週報")).Sent {
		t.Fatal("14 天後可以再提")
	}
}

// test_a_tick_does_not_override_an_answer_given_meanwhile_and_keeps_what_it_could_not_deliver
func TestATickDoesNotOverrideAnAnswerGivenMeanwhileAndKeepsWhatItCouldNotDeliver(t *testing.T) {
	co := setup(t)
	first := co.submit("CEO", propose("第一件")).AgendaID
	second := co.submit("CEO", propose("第二件")).AgendaID
	// 兩件依時間先後處理：第一件先到期。
	co.setDue(first, co.clock.Add(-2*time.Minute))
	co.setDue(second, co.clock.Add(-time.Minute))
	// 送第一件的時候，你否決了第二件。
	co.rt.onStart = func(req runtime.ChatRequest) {
		if strings.Contains(req.Text, "第一件") {
			if err := co.db.SettleAgenda(co.ctx, second, pending, store.AgendaVetoed, "", co.clock.UnixMilli()); err != nil {
				t.Error(err)
			}
		}
	}
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	co.rt.onStart = nil
	if co.agenda(second).State != store.AgendaVetoed || co.agenda(first).State != store.AgendaAuto || len(co.rt.messages()) != 1 {
		t.Fatalf("第二件 %s，送了 %d 則", co.agenda(second).State, len(co.rt.messages()))
	}
	// 送不到 Bot 的留著下次再試。
	third := co.submit("CEO", propose("第三件")).AgendaID
	co.clock = co.clock.Add(3*24*time.Hour + time.Minute)
	co.rt.refuse = errBusy
	if err := co.s.Tick(co.ctx); err == nil {
		t.Fatal("送不到要回錯誤")
	}
	if a := co.agenda(third); a.State != store.AgendaOpen || a.DecidedAt != 0 {
		t.Fatalf("%+v", a)
	}
	co.rt.refuse = nil
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if co.agenda(third).State != store.AgendaAuto {
		t.Fatal("下次再試就送到了")
	}
}

// test_the_user_can_agree_early_or_veto_with_a_reason
func TestTheUserCanAgreeEarlyOrVetoWithAReason(t *testing.T) {
	co := setup(t)
	first := co.submit("CEO", propose("每天多發一篇貼文")).AgendaID
	second := co.submit("CEO", propose("改用新的資料來源")).AgendaID
	if a := must[store.Agenda](t)(co.s.Decide(co.ctx, first, DecideAccept, "", "")); a.State != store.AgendaAccepted {
		t.Fatal(a.State)
	}
	msgs := co.rt.messages()
	if !strings.Contains(msgs[len(msgs)-1].Text, "【董事長回覆】同意：每天多發一篇貼文") || msgs[len(msgs)-1].Sender != "owner" {
		t.Fatalf("%+v", msgs[len(msgs)-1])
	}
	vetoed := must[store.Agenda](t)(co.s.Decide(co.ctx, second, DecideVeto, "來源不可靠", ""))
	if vetoed.State != store.AgendaVetoed || vetoed.Note != "來源不可靠" {
		t.Fatalf("%+v", vetoed)
	}
	last := co.rt.messages()[len(co.rt.messages())-1].Text
	if !strings.Contains(last, "否決：改用新的資料來源") || !strings.Contains(last, "來源不可靠") {
		t.Fatal(last)
	}
	_, err := co.s.Decide(co.ctx, first, DecideVeto, "", "")
	errHas(t, err, "處理過")
	_, err = co.s.Decide(co.ctx, co.submit("CEO", propose("第三件")).AgendaID, DecideAnswer, "x", "")
	errHas(t, err, "同意或否決")
	// 卡片跟著結掉。
	if c := must[[]store.InboxItem](t)(co.db.ListInboxByRef(co.ctx, second)); c[0].Status != store.InboxResolved || c[0].Resolution != store.AgendaVetoed {
		t.Fatalf("%+v", c[0])
	}
}

// 送不到 Bot 時你的回覆保持未處理，可以再給一次（LazyBoy decide 的 except）。
func TestADecisionThatCouldNotBeDeliveredStaysOpen(t *testing.T) {
	co := setup(t)
	id := co.submit("CEO", propose("每天多發一篇貼文")).AgendaID
	co.rt.refuse = errBusy
	if _, err := co.s.Decide(co.ctx, id, DecideAccept, "", ""); err == nil {
		t.Fatal("送不到要回錯誤")
	}
	if a := co.agenda(id); a.State != store.AgendaOpen {
		t.Fatal(a.State)
	}
	if c := must[[]store.InboxItem](t)(co.db.ListInboxByRef(co.ctx, id)); c[0].Status != store.InboxOpen {
		t.Fatal("卡片還開著")
	}
	co.rt.refuse = nil
	must[store.Agenda](t)(co.s.Decide(co.ctx, id, DecideAccept, "", ""))
}

// test_a_discussion_gets_a_time_and_wakes_the_bot_when_it_comes
func TestADiscussionGetsATimeAndWakesTheBotWhenItComes(t *testing.T) {
	co := setup(t)
	dev := co.bots["Dev"]
	written, instant := co.taipei(5, 15)
	sent := co.submit("Dev", SubmitIn{Kind: KindDiscuss, Subject: "下季要先做哪個功能", Details: "A 或 B…", When: written})
	row := co.agenda(sent.AgendaID)
	if row.DueAt != instant.UnixMilli() {
		t.Fatalf("%v != %v", time.UnixMilli(row.DueAt), instant)
	}
	if c := must[[]store.InboxItem](t)(co.db.ListInboxByRef(co.ctx, row.ID)); c[0].Kind != "discussion" {
		t.Fatal(c[0].Kind)
	}
	far, _ := co.taipei(40, 10)
	_, err := co.s.Submit(co.ctx, dev, SubmitIn{Kind: KindDiscuss, Subject: "太遠", When: far})
	errHas(t, err, "30 天")
	_, err = co.s.Submit(co.ctx, dev, SubmitIn{Kind: KindDiscuss, Subject: "太快", When: co.clock.Format(time.RFC3339)})
	errHas(t, err, "30 分鐘")
	_, err = co.s.Submit(co.ctx, dev, SubmitIn{Kind: KindDiscuss, Subject: "亂寫", When: "下週"})
	errHas(t, err, "時間格式")
	// 改時間：還是 open、不送任何東西；Bot 只需要時間到時知道。
	written, instant = co.taipei(6, 10)
	moved := must[store.Agenda](t)(co.s.Decide(co.ctx, row.ID, DecideAccept, "", written))
	if moved.State != store.AgendaOpen || moved.DueAt != instant.UnixMilli() || len(co.rt.messages()) != 0 {
		t.Fatalf("%+v", moved)
	}
	co.clock = instant.Add(time.Minute)
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	held := co.agenda(row.ID)
	if held.State != store.AgendaHeld || len(co.pending()) != 1 {
		t.Fatalf("%+v", held)
	}
	msgs := co.rt.messages()
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "【討論時間到】下季要先做哪個功能") || !strings.Contains(msgs[0].Text, "問他的想法") {
		t.Fatalf("%+v", msgs)
	}
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if len(co.rt.messages()) != 1 {
		t.Fatal("時間到了的討論不重複提醒")
	}
	answered := must[store.Agenda](t)(co.s.Decide(co.ctx, row.ID, DecideAnswer, "先做 A，B 下季再說", ""))
	last := co.rt.messages()[1].Text
	if answered.State != store.AgendaAnswered || !strings.Contains(last, "先做 A") || !strings.Contains(last, "不用另外安排討論") {
		t.Fatalf("%+v %s", answered, last)
	}
}

// test_a_discussion_answered_in_text_or_declined_needs_no_meeting
func TestADiscussionAnsweredInTextOrDeclinedNeedsNoMeeting(t *testing.T) {
	co := setup(t)
	row := co.submit("Dev", SubmitIn{Kind: KindDiscuss, Subject: "要不要換模型"})
	due := time.UnixMilli(row.DueAt)
	if due.Before(co.clock.Add(12*time.Hour)) || due.In(co.s.Zone()).Weekday() == time.Saturday || due.In(co.s.Zone()).Weekday() == time.Sunday ||
		due.In(co.s.Zone()).Hour() != limits.DiscussDefaultHour {
		t.Fatalf("預設是隔天以後的平日晚上 6 點：%v", due.In(co.s.Zone()))
	}
	_, err := co.s.Decide(co.ctx, row.AgendaID, DecideAnswer, "", "")
	errHas(t, err, "寫下你的回覆")
	declined := must[store.Agenda](t)(co.s.Decide(co.ctx, row.AgendaID, DecideDecline, "", ""))
	last := co.rt.messages()[0].Text
	if declined.State != store.AgendaDeclined || !strings.Contains(last, "不需要討論") || !strings.Contains(last, "照你的判斷做") {
		t.Fatalf("%+v %s", declined, last)
	}
	_, err = co.s.Decide(co.ctx, co.submit("Dev", SubmitIn{Kind: KindDiscuss, Subject: "x"}).AgendaID, DecideVeto, "", "")
	errHas(t, err, "保留時間、直接回覆或取消")
}

// test_a_bot_merges_rather_than_piling_up_and_a_deleted_bot_takes_its_items_along
func TestABotMergesRatherThanPilingUpAndADeletedBotTakesItsItemsAlong(t *testing.T) {
	co := setup(t)
	for _, s := range []string{"事情 0", "事情 1"} {
		co.submit("Dev", propose(s))
	}
	co.submit("Dev", SubmitIn{Kind: KindDiscuss, Subject: "事情 2"}) // 提案＋討論合計
	_, err := co.s.Submit(co.ctx, co.bots["Dev"], propose("第四件"))
	errHas(t, err, "合併")
	co.submit("CEO", propose("主管的事")) // 每個 Bot 各算
	if err := co.db.DeleteBot(co.ctx, co.bots["Dev"]); err != nil {
		t.Fatal(err)
	}
	rows := co.pending()
	if len(rows) != 1 || rows[0].Subject != "主管的事" {
		t.Fatalf("%+v", rows)
	}
}

// test_the_review_shows_what_went_wrong_for_the_bot_and_its_team
func TestTheReviewShowsWhatWentWrongForTheBotAndItsTeam(t *testing.T) {
	co := setup(t)
	co.setBoss("Ops", "")
	co.setBoss("QA", "")
	login := co.task("Dev", store.TaskDone, nil, "做登入頁")
	toolEvent := func(task store.Task, name string, ok bool, summary string) {
		raw, _ := json.Marshal(map[string]any{"type": "tool_result", "tool": map[string]any{"id": name, "name": name, "ok": ok, "summary": summary}})
		if _, err := co.db.AppendEvent(co.ctx, store.Event{BotID: task.BotID, ThreadID: task.ThreadID, TaskID: task.ID, Kind: runtime.KindToolResult, Payload: raw}); err != nil {
			t.Fatal(err)
		}
	}
	toolEvent(login, "browser_navigate", false, "逾時")
	toolEvent(login, "terminal", true, "")
	co.task("Dev", store.TaskDone, nil, "寫測試")
	weekly := co.task("CEO", store.TaskDone, nil, "整理週報")
	if err := co.db.SetTaskState(co.ctx, weekly.ID, store.TaskDone, runtime.VerdictFailed); err != nil {
		t.Fatal(err)
	}
	co.task("CEO", store.TaskDone, nil, "舊的事")
	co.clock = co.clock.Add(time.Hour)
	view := must[ReviewOut](t)(co.s.Review(co.ctx, co.bots["CEO"], 7))
	if view.Me.Tasks != 2 || view.Me.WithFailures != 1 || view.Me.Failures[0].Verdict != runtime.VerdictFailed {
		t.Fatalf("%+v", view.Me)
	}
	if len(view.Team) != 1 {
		t.Fatalf("%+v", view.Team)
	}
	dev := view.Team[0]
	if dev.Name != "Dev" || dev.Tasks != 2 || dev.WithFailures != 1 || strings.Join(dev.Failures[0].FailedTools, ",") != "browser_navigate" ||
		strings.Join(dev.Failures[0].Errors, ",") != "逾時" {
		t.Fatalf("%+v", dev)
	}
	// 20 天前的不算。
	co.clock = co.clock.Add(20 * 24 * time.Hour)
	if v := must[ReviewOut](t)(co.s.Review(co.ctx, co.bots["CEO"], 7)); v.Me.Tasks != 0 {
		t.Fatal(v.Me.Tasks)
	}
	if v := must[ReviewOut](t)(co.s.Review(co.ctx, co.bots["Dev"], 0)); len(v.Team) != 0 || v.Days != limits.ReviewDaysDefault {
		t.Fatalf("%+v", v)
	}
}

// test_the_calendar_lists_routines_learning_time_and_what_waits_for_the_user（定時任務與學習時段等 C32）
func TestTheCalendarListsWhatWaitsForTheUser(t *testing.T) {
	co := setup(t)
	co.submit("CEO", SubmitIn{Kind: KindPropose, Subject: "改成週報", Days: 3})
	when, _ := co.taipei(2, 15)
	co.submit("Dev", SubmitIn{Kind: KindDiscuss, Subject: "選路線", When: when})
	co.submit("Dev", SubmitIn{Kind: KindPropose, Subject: "下個月的事", Days: 14})
	// 待你核定（C11 的核准）列在旁邊，不算進 3 件。
	if _, err := co.db.CreateApproval(co.ctx, store.Approval{BotID: co.bots["Dev"], Action: "xbot.approvals.request", RequestedBy: "bot:" + co.bots["Dev"],
		Payload: json.RawMessage(`{"summary":"請使用者核准一件事","params":{"subject":"簽約"}}`)}); err != nil {
		t.Fatal(err)
	}
	week := must[ListOut](t)(co.s.List(co.ctx, 7, ""))
	if len(week.Pending) != 3 || len(week.Calendar) != 2 || week.OpenMax != limits.AgendaOpenMax {
		t.Fatalf("%+v", week)
	}
	if week.Calendar[0].Title != "選路線" || week.Calendar[1].Title != "改成週報" || week.Calendar[0].BotName != "Dev" {
		t.Fatalf("依時間先後：%+v", week.Calendar)
	}
	if len(week.Approvals) != 1 || week.Approvals[0].Subject != "簽約" {
		t.Fatalf("%+v", week.Approvals)
	}
	must[store.Agenda](t)(co.s.Decide(co.ctx, week.Pending[0].ID, DecideDecline, "", ""))
	week = must[ListOut](t)(co.s.List(co.ctx, 7, co.bots["Dev"]))
	if len(week.Pending) != 1 || len(week.Decided) != 1 || week.Decided[0].State != store.AgendaDeclined {
		t.Fatalf("%+v", week)
	}
}

// test_times_and_folding（ICS 折行還沒有）
func TestTimes(t *testing.T) {
	s := New(Options{})
	got := must[time.Time](t)(s.ParseWhen("2026-10-20 15:00"))
	if got.Format(time.RFC3339) != "2026-10-20T07:00:00Z" {
		t.Fatal(got)
	}
	got = must[time.Time](t)(s.ParseWhen("2026-10-20T15:00:00+09:00"))
	if got.Format(time.RFC3339) != "2026-10-20T06:00:00Z" {
		t.Fatal(got)
	}
	if got := must[time.Time](t)(s.ParseWhen("2026/10/20 15:00")); got.Format(time.RFC3339) != "2026-10-20T07:00:00Z" {
		t.Fatal(got)
	}
	if got := must[time.Time](t)(s.ParseWhen("")); !got.IsZero() {
		t.Fatal(got)
	}
	friday := time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC) // 台北週五 10:00
	if got := s.DefaultDiscussionTime(friday); got.Format(time.RFC3339) != "2026-10-19T10:00:00Z" {
		t.Fatalf("下週一台北 18:00：%v", got)
	}
}

// test_moving_a_discussion_keeps_its_limits_and_wakes_the_bot_again_at_the_new_time
func TestMovingADiscussionKeepsItsLimitsAndWakesTheBotAgainAtTheNewTime(t *testing.T) {
	co := setup(t)
	written, _ := co.taipei(5, 15)
	key := co.submit("CEO", SubmitIn{Kind: KindDiscuss, Subject: "選路線", When: written}).AgendaID
	soon := co.clock.Add(5 * time.Minute).In(co.s.Zone()).Format("2006-01-02 15:04")
	_, err := co.s.Decide(co.ctx, key, DecideAccept, "", soon)
	errHas(t, err, "30 分鐘")
	_, err = co.s.Decide(co.ctx, key, DecideAccept, "", "2099-01-01 10:00")
	errHas(t, err, "30 天")
	co.setDue(key, co.clock.Add(-2*time.Minute))
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if co.agenda(key).State != store.AgendaHeld {
		t.Fatal("時間到")
	}
	// 時間到之後你改了時間：重新排，到新的時間再叫醒一次。
	later, instant := co.taipei(2, 10)
	moved := must[store.Agenda](t)(co.s.Decide(co.ctx, key, DecideAccept, "", later))
	if moved.State != store.AgendaOpen || moved.DueAt != instant.UnixMilli() {
		t.Fatalf("%+v", moved)
	}
	co.clock = instant.Add(time.Minute)
	must[struct{}](t)(struct{}{}, co.s.Tick(co.ctx))
	if n := len(co.rt.messages()); n != 2 {
		t.Fatalf("叫醒兩次：%d", n)
	}
}

// 群組座位的一輪送出的提案：你的回覆改送到 Bot 自己的新對話（LazyBoy test_an_approval_from_a_group_chat_turn…，提案版）。
func TestAProposalFromAGroupTurnIsAnsweredOutsideTheGroup(t *testing.T) {
	co := setup(t)
	seat := must[store.Thread](t)(co.db.CreateThread(co.ctx, store.Thread{BotID: co.bots["Dev"], Title: "群組"}))
	raw, _ := json.Marshal(map[string]string{"room_id": "room-1"})
	task := must[store.Task](t)(co.db.CreateTask(co.ctx, store.Task{BotID: co.bots["Dev"], ThreadID: seat.ID, Source: runtime.SourceRoom, State: store.TaskRunning, Input: "x", Meta: raw}))
	co.rt.active[co.bots["Dev"]] = task.ID
	id := co.submit("Dev", propose("換個排版")).AgendaID
	if a := co.agenda(id); a.ThreadID != "" || a.TaskID != task.ID {
		t.Fatalf("%+v", a)
	}
	must[store.Agenda](t)(co.s.Decide(co.ctx, id, DecideAccept, "", ""))
	if m := co.rt.messages()[0]; m.ThreadID != "" || m.Title != "提案：換個排版" {
		t.Fatalf("%+v", m)
	}
}
