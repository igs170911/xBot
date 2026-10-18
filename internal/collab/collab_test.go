package collab

// 從 LazyBoy tests/test_collab.py 翻過來的測試（Bot 之間的傳話、群組、防止失控的限制）。
// 每個測試上面註明原本的名稱；runtime 層的（你的訊息優先、無人在場的提問過期）在 internal/runtime。

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// test_roster_lists_teammates_but_not_the_bot_itself
func TestRosterListsTeammatesButNotTheBotItself(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	mates := must[[]Mate](t)(tm.s.Directory(tm.ctx, chief))
	var names []string
	for _, m := range mates {
		names = append(names, m.Name)
	}
	slices.Sort(names) // 同一毫秒建立的 Bot 順序不固定
	if strings.Join(names, ",") != "Scout,Writer" {
		t.Fatalf("隊友：%v", names)
	}
	roster := must[string](t)(tm.s.RosterText(tm.ctx, chief))
	if !strings.Contains(roster, "Writer（文案）") || !strings.Contains(roster, "message_bot") || strings.Contains(roster, "Chief（") {
		t.Fatalf("名冊：%s", roster)
	}
	if m := must[Mate](t)(tm.s.Resolve(tm.ctx, "@writer", chief)); m.ID != tm.bots["Writer"] {
		t.Fatalf("@writer：%+v", m)
	}
	if _, err := tm.s.Resolve(tm.ctx, "Nobody", chief); err == nil || !strings.Contains(err.Error(), "找不到隊友") {
		t.Fatalf("找不到：%v", err)
	}
}

// test_a_request_wakes_the_recipient_and_its_answer_comes_back
func TestARequestWakesTheRecipientAndItsAnswerComesBack(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	origin := tm.running(chief, nil)
	sent := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫一段新品文案", "request", SendOptions{}))
	if !sent.Sent || !strings.Contains(sent.Note, "自動") {
		t.Fatalf("%+v", sent)
	}
	// 寄件者的對話有「已傳訊息給 Writer」；收件者在自己的對話有一件低優先的工作。
	ev := tm.last(tm.main[chief])
	if ev.Kind != "peer" || peerOf(ev).Event != "message_out" || peerOf(ev).PeerName != "Writer" {
		t.Fatalf("事件：%+v", ev)
	}
	q := tm.queued(writer)
	if len(q) != 1 {
		t.Fatalf("排隊：%+v", q)
	}
	task := q[0]
	m := metaOf(task)
	if m.FromBot != chief || m.Intent != IntentRequest || !task.LowPriority || m.ParentTask != origin || task.Source != runtime.SourceBot {
		t.Fatalf("工作：%+v %+v", task, m)
	}
	if !strings.Contains(task.Input, "不是使用者本人") {
		t.Fatalf("要有資料框架：%s", task.Input)
	}
	shown := tm.last(task.ThreadID)
	if shown.Content != "寫一段新品文案" || shown.Sender != "bot:"+chief || peerOf(shown).Event != "message_in" {
		t.Fatalf("收件者看到的：%+v", shown)
	}
	// Writer 回答：結果以 result 回到 Chief 發出請求的那條對話串。
	tm.start(task)
	tm.finish(task.ID, runtime.VerdictAnswer, "文案在 /workspace/copy.md")
	back := tm.queued(chief)
	if len(back) != 1 {
		t.Fatalf("回覆：%+v", back)
	}
	bm := metaOf(back[0])
	if bm.FromBot != writer || bm.Intent != IntentResult || back[0].ThreadID != tm.main[chief] || bm.Hop != 2 {
		t.Fatalf("回覆：%+v %+v", back[0], bm)
	}
	// 回覆不要求再回覆：Chief 處理完不會再送東西給 Writer。
	tm.start(back[0])
	tm.finish(back[0].ID, runtime.VerdictAnswer, "已整合給使用者")
	if len(tm.queued(writer)) != 0 {
		t.Fatal("result 不該再回覆")
	}
	pair := must[[]store.BotMessage](t)(tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{}))
	if len(pair) != 2 || pair[0].FromName != "Chief" || pair[0].Intent != IntentRequest || pair[1].FromName != "Writer" || pair[1].Intent != IntentResult {
		t.Fatalf("傳話紀錄：%+v", pair)
	}
}

// test_a_result_too_long_for_a_message_still_comes_back_as_a_file
func TestAResultTooLongForAMessageStillComesBackAsAFile(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "把完整的調查報告給我", "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)
	report := "調查結果。" + strings.Repeat("細節", 9000)
	tm.finish(task.ID, runtime.VerdictAnswer, report)
	if len(tm.queued(chief)) != 1 {
		t.Fatal("太長的結果也要送回來")
	}
	shown := tm.last(tm.main[chief]).Content
	if !strings.HasPrefix(shown, "調查結果。") || !strings.Contains(shown, "太長放不進訊息") || utf8.RuneCountInString(shown) >= 4000 {
		t.Fatalf("預覽：%d 字", utf8.RuneCountInString(shown))
	}
	p := strings.TrimSuffix(shown[strings.LastIndex(shown, "/workspace/")+len("/workspace/"):], "）")
	if !strings.HasPrefix(p, "from/Writer/result-") {
		t.Fatalf("路徑：%s", p)
	}
	if tm.read(chief, p) != report {
		t.Fatal("檔案要是完整的回覆")
	}
}

// test_silent_answers_are_hidden_and_not_sent_back
func TestSilentAnswersAreHiddenAndNotSentBack(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "有空看一下", "question", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)
	tm.finish(task.ID, runtime.VerdictAnswer, "[silent]")
	if !strings.Contains(string(tm.last(task.ThreadID).Meta), `"silent":true`) {
		t.Fatalf("[silent] 要藏起來：%s", tm.last(task.ThreadID).Meta)
	}
	if len(tm.queued(chief)) != 0 {
		t.Fatal("[silent] 不回覆")
	}
}

// test_an_exchange_stops_after_max_hops
func TestAnExchangeStopsAfterMaxHops(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	tm.running(chief, map[string]any{"from_bot": tm.bots["Writer"], "hop": limits.MaxHops, "chain": "c1", "intent": "request"})
	_, err := tm.s.Send(tm.ctx, chief, "Scout", "再問一次", "request", SendOptions{})
	if err == nil || !strings.Contains(err.Error(), "回報給使用者") {
		t.Fatalf("超過往來次數：%v", err)
	}
	// 交回結果不擋（relay）。
	origin := tm.task(must[string](t)(func() (string, error) { _, id := tm.rt.Active(chief); return id, nil }()))
	if _, err := tm.s.Send(tm.ctx, chief, "Scout", "結果", "", SendOptions{Origin: &origin, Relay: true, intent: IntentResult}); err != nil {
		t.Fatalf("結果回傳不擋：%v", err)
	}
}

// test_stopping_a_bot_stops_the_work_it_handed_off
func TestStoppingABotStopsTheWorkItHandedOff(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	origin := tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫", "request", SendOptions{}))
	child := tm.queued(writer)[0]
	// Writer 把一部分交給 Scout。
	tm.start(child)
	must[SendResult](t)(tm.s.Send(tm.ctx, writer, "Scout", "查資料", "request", SendOptions{}))
	grandchild := tm.queued(scout)[0]
	_ = tm.db.SetTaskState(tm.ctx, origin, store.TaskDone, runtime.VerdictAnswer)
	stopped := tm.s.StopCascade(tm.ctx, []string{metaOf(child).ParentTask})
	if !slices.Contains(stopped, child.ID) || !slices.Contains(stopped, grandchild.ID) {
		t.Fatalf("停止連鎖：%v", stopped)
	}
	if v := tm.task(grandchild.ID).Verdict; v != runtime.VerdictCancelled {
		t.Fatalf("排隊中的子工作要取消：%s", v)
	}
	if st := tm.task(child.ID).State; st != store.TaskStopping && st != store.TaskDone {
		t.Fatalf("進行中的子工作要停：%s", st)
	}
}

// test_mentions_match_members_and_everyone
func TestMentionsMatchMembersAndEveryone(t *testing.T) {
	members := []Member{{"1", "Writer"}, {"2", "Write"}, {"3", "小編"}}
	named, everyone := Mentions("@Writer 寫稿，@小編 排版，@everyone 注意時程", members)
	if len(named) != 2 || named[0].ID != "1" || named[1].ID != "3" || !everyone {
		t.Fatalf("%+v %v", named, everyone)
	}
}

// test_group_chat_wakes_the_named_bot_or_everyone
func TestGroupChatWakesTheNamedBotOrEveryone(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	if _, err := tm.s.CreateRoom(tm.ctx, []string{chief}, RoomOptions{}); err == nil || !strings.Contains(err.Error(), "2 到 10") {
		t.Fatalf("人數：%v", err)
	}
	// LazyBoy 沒點名時叫醒所有成員＝回應規則「所有成員都回應」。
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{ResponderRule: store.RuleEveryone}))
	if room.Name != "Chief、Writer、Scout" || !room.AutoName {
		t.Fatalf("名稱：%+v", room)
	}
	woke := must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Writer 寫一段開場", ""))
	if strings.Join(woke.Woke, ",") != "Writer" || len(tm.queued(writer)) != 1 || len(tm.queued(chief)) != 0 {
		t.Fatalf("@Writer：%+v", woke)
	}
	// Writer 還沒開始前再發一則：不會多排一輪，開始時兩則都讀到。
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Writer 再補一句結尾", ""))
	if len(tm.queued(writer)) != 1 {
		t.Fatal("不該多排一輪")
	}
	woke = must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "大家早", ""))
	slices.Sort(woke.Woke)
	if strings.Join(woke.Woke, ",") != "Chief,Scout,Writer" {
		t.Fatalf("大家早：%v", woke.Woke)
	}
	// 群組座位是隱藏的對話串，不是 Bot 自己的對話。
	seat := tm.queued(writer)[0]
	if th := must[store.Thread](t)(tm.db.GetThread(tm.ctx, seat.ThreadID)); th.RoomID != room.ID {
		t.Fatalf("座位：%+v", th)
	}
	if ths := must[[]store.Thread](t)(tm.db.ListThreads(tm.ctx, writer, false)); slices.ContainsFunc(ths, func(th store.Thread) bool { return th.ID == seat.ThreadID }) {
		t.Fatal("座位不能出現在 Bot 的對話串清單")
	}
	prompt := tm.start(seat)
	for _, want := range []string{"寫一段開場", "再補一句結尾", "大家早", "你被點名了"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt 少了 %q：%s", want, prompt)
		}
	}
}

// test_group_answers_are_posted_and_mentions_hand_off
func TestGroupAnswersArePostedAndMentionsHandOff(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{Name: "發表會"}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 規劃發表會", ""))
	turn := tm.queued(chief)[0]
	tm.start(turn)
	tm.finish(turn.ID, runtime.VerdictAnswer, "@Scout 先查三個場地，回報價格")
	log := tm.roomLog(room.ID)
	posted := log[len(log)-1]
	if rm := roomMetaOf(posted); rm.SpeakerName != "Chief" || len(rm.Mentions) != 1 || rm.Mentions[0] != scout || rm.Handoff != 1 {
		t.Fatalf("貼到群組：%+v", rm)
	}
	q := tm.queued(scout)
	if len(q) != 1 {
		t.Fatal("要交棒給 Scout")
	}
	handoff := q[0]
	if hm := metaOf(handoff); !hm.Addressed || hm.Round != 1 || !handoff.LowPriority {
		t.Fatalf("交棒：%+v", hm)
	}
	// [silent] 不貼；接力到 RoomRounds 輪就停。
	tm.start(handoff)
	tm.finish(handoff.ID, runtime.VerdictAnswer, "[silent]")
	if log := tm.roomLog(room.ID); log[len(log)-1].ID != posted.ID {
		t.Fatal("[silent] 不貼到群組")
	}
	tired := tm.running(writer, map[string]any{"room_id": room.ID, "round": limits.RoomRounds})
	tm.finish(tired, runtime.VerdictAnswer, "@Chief 換你")
	if len(tm.queued(chief)) != 0 {
		t.Fatal("到上限不再叫醒別人")
	}
	log = tm.roomLog(room.ID)
	if !roomMetaOf(log[len(log)-2]).Capped || roomMetaOf(log[len(log)-1]).Notice != "capped" {
		t.Fatal("到上限要標記並提示「已經來回 4 輪」")
	}
}

// test_room_transcripts_survive_the_orphan_sweep（XBot 沒有孤兒清理：改成驗證群組訊息留到刪群組為止，連座位一起刪）
func TestRoomTranscriptsLiveAsLongAsTheRoom(t *testing.T) {
	tm := setup(t)
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{tm.bots["Chief"], tm.bots["Writer"]}, RoomOptions{ResponderRule: store.RuleEveryone}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "hello", ""))
	if log := tm.roomLog(room.ID); log[0].Content != "hello" {
		t.Fatalf("%+v", log)
	}
	seat := tm.queued(tm.bots["Writer"])[0]
	if err := tm.s.DeleteRoom(tm.ctx, room.ID); err != nil {
		t.Fatal(err)
	}
	if n := must[int](t)(tm.db.CountMessages(tm.ctx, store.ScopeRoom, room.ID)); n != 0 {
		t.Fatal("刪群組要連訊息一起刪")
	}
	if _, err := tm.db.GetThread(tm.ctx, seat.ThreadID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("刪群組要連座位一起刪")
	}
	if tm.task(seat.ID).Verdict != runtime.VerdictCancelled {
		t.Fatal("刪群組前先停掉成員的工作")
	}
}

// test_a_bot_opens_a_group_chat_and_hands_the_first_step
func TestABotOpensAGroupChatAndHandsTheFirstStep(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	tm.running(chief, nil)
	started := must[StartGroupResult](t)(tm.s.StartGroup(tm.ctx, chief, []string{"Writer", "Scout"}, "發表會", "@Scout 先找三個場地"))
	ids := must[[]string](t)(tm.s.memberIDs(tm.ctx, started.GroupID))
	slices.Sort(ids)
	want := []string{chief, writer, scout}
	slices.Sort(want)
	if !slices.Equal(ids, want) || strings.Join(started.Woke, ",") != "Scout" {
		t.Fatalf("%+v %v", started, ids)
	}
	opening := tm.roomLog(started.GroupID)[0]
	if rm := roomMetaOf(opening); rm.SpeakerName != "Chief" || len(rm.Mentions) != 1 || rm.Mentions[0] != scout {
		t.Fatalf("開場：%+v", rm)
	}
	if len(tm.queued(scout)) != 1 || len(tm.queued(writer)) != 0 {
		t.Fatal("只叫醒 Scout")
	}
	ev := tm.last(tm.main[chief])
	if p := peerOf(ev); p.Event != "group_created" || p.RoomID != started.GroupID {
		t.Fatalf("事件：%+v", p)
	}
	// 之後發言只叫醒點名的；不在群組的 Bot 不能發言。
	posted := must[PostGroupResult](t)(tm.s.PostGroup(tm.ctx, chief, "發表會", "@Writer 場地確定後寫邀請函"))
	if strings.Join(posted.Woke, ",") != "Writer" || len(tm.queued(writer)) != 1 {
		t.Fatalf("%+v", posted)
	}
	outsider := must[store.Bot](t)(tm.db.CreateBot(tm.ctx, store.Bot{Name: "Outsider"}))
	if _, err := tm.s.PostGroup(tm.ctx, outsider.ID, "發表會", "hi"); err == nil || !strings.Contains(err.Error(), "找不到你所在的群組") {
		t.Fatalf("外人：%v", err)
	}
}

// test_a_retried_message_is_delivered_once
func TestARetriedMessageIsDeliveredOnce(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	first := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫三則貼文", "", SendOptions{RequestID: "tool-call-1"}))
	again := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫三則貼文", "", SendOptions{RequestID: "tool-call-1"}))
	if !first.Sent || !again.Replayed {
		t.Fatalf("%+v %+v", first, again)
	}
	if n := len(must[[]store.BotMessage](t)(tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{}))); n != 1 {
		t.Fatalf("傳話 %d 則", n)
	}
	outs := 0
	for _, m := range tm.transcript(tm.main[chief]) {
		if peerOf(m).Event == "message_out" {
			outs++
		}
	}
	if outs != 1 || len(tm.queued(writer)) != 1 {
		t.Fatalf("事件 %d、排隊 %d", outs, len(tm.queued(writer)))
	}
}

// test_two_bots_may_use_the_same_request_id
func TestTwoBotsMayUseTheSameRequestID(t *testing.T) {
	tm := setup(t)
	chief, scout, writer := tm.bots["Chief"], tm.bots["Scout"], tm.bots["Writer"]
	tm.running(chief, nil)
	tm.running(scout, nil)
	first := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "寫三則貼文", "", SendOptions{RequestID: "task-1"}))
	second := must[SendResult](t)(tm.s.Send(tm.ctx, scout, "Writer", "寫三則貼文", "", SendOptions{RequestID: "task-1"}))
	if !first.Sent || !second.Sent || second.Replayed {
		t.Fatalf("%+v %+v", first, second)
	}
	if len(tm.queued(writer)) != 2 || len(must[[]store.BotMessage](t)(tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{}))) != 2 {
		t.Fatal("兩個寄件者是兩則")
	}
	if again := must[SendResult](t)(tm.s.Send(tm.ctx, scout, "Writer", "寫三則貼文", "", SendOptions{RequestID: "task-1"})); !again.Replayed {
		t.Fatal("同一個寄件者重送要擋")
	}
}

// test_clearing_a_group_name_goes_back_to_naming_it_after_its_members
func TestClearingAGroupNameGoesBackToNamingItAfterItsMembers(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "發表會"}))
	if room.AutoName {
		t.Fatal("有名字就不是自動命名")
	}
	empty := ""
	room = must[store.Room](t)(tm.s.UpdateRoom(tm.ctx, room.ID, RoomUpdate{Name: &empty, MemberIDs: []string{chief, writer, scout}}))
	if room.Name != "Chief、Writer、Scout" || !room.AutoName {
		t.Fatalf("%+v", room)
	}
	if r := must[store.Room](t)(tm.s.UpdateRoom(tm.ctx, room.ID, RoomUpdate{MemberIDs: []string{chief, scout}})); r.Name != "Chief、Scout" {
		t.Fatalf("%+v", r)
	}
}

// test_the_primary_bot_is_named_in_every_teammate_list
func TestThePrimaryBotIsNamedInEveryTeammateList(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	b := must[store.Bot](t)(tm.db.GetBot(tm.ctx, chief))
	b.IsChief = true
	must[store.Bot](t)(tm.db.UpdateBot(tm.ctx, b))
	roster := must[string](t)(tm.s.RosterText(tm.ctx, writer))
	if !strings.Contains(roster, "Chief（總管）［主要 Bot］") || !strings.Contains(roster, "Chief 是使用者的主要 Bot") {
		t.Fatalf("%s", roster)
	}
	own := must[string](t)(tm.s.RosterText(tm.ctx, chief))
	if line := strings.Split(strings.Split(own, "## 隊友與協作")[1], "\n")[2]; strings.Contains(line, "主要 Bot") {
		t.Fatalf("自己的名冊第一位隊友：%s", line)
	}
}

// test_group_attachments_are_pointed_to_on_each_members_computer
func TestGroupAttachmentsArePointedToOnEachMembersComputer(t *testing.T) {
	text := "看這份 [報價單.pdf](/api/agents/0b1e7f3a-1111-2222-3333-444455556666/files/content?path=uploads/k1/%E5%A0%B1%E5%83%B9%E5%96%AE.pdf)"
	if got := AttachmentsIn(text); got != "看這份 ［附件：報價單.pdf，在你的電腦：/workspace/uploads/k1/報價單.pdf］" {
		t.Fatal(got)
	}
	if AttachmentsIn("沒有附件") != "沒有附件" {
		t.Fatal("沒有附件不變")
	}
}

// test_files_named_in_a_message_travel_to_the_recipient
func TestFilesNamedInAMessageTravelToTheRecipient(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.write(chief, "reports/plan.md", "# plan")
	tm.running(chief, nil)
	sent := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "請看 /workspace/reports/plan.md，還有不存在的 /workspace/nope.txt。", "request", SendOptions{}))
	if tm.read(writer, "from/Chief/reports/plan.md") != "# plan" || sent.Files != 1 {
		t.Fatal("檔案要複製到對方電腦")
	}
	task := tm.queued(writer)[0]
	if !strings.Contains(task.Input, "/workspace/from/Chief/reports/plan.md") || !strings.Contains(task.Input, "/workspace/nope.txt") {
		t.Fatalf("%s", task.Input)
	}
	// 寄件者自己的對話保留它電腦上的路徑。
	if !strings.Contains(tm.last(tm.main[chief]).Content, "請看 /workspace/reports/plan.md") {
		t.Fatal("寄件者的路徑不變")
	}
}

// test_a_file_that_came_from_the_recipient_points_back_at_its_own_copy
func TestAFileThatCameFromTheRecipientPointsBackAtItsOwnCopy(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.write(writer, "poc/run.py", "print(1)")
	// Chief 先前拿到 Writer 的檔案、放在同樣的位置；原樣送回去，然後改過再送。
	tm.write(chief, "poc/run.py", "print(1)")
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "修一下 /workspace/poc/run.py", "request", SendOptions{}))
	if !strings.Contains(tm.queued(writer)[0].Input, "修一下 /workspace/poc/run.py") {
		t.Fatal("內容相同時指向收件者自己的檔案")
	}
	if _, err := tm.files.Stat(tm.ctx, writer, "from/Chief/poc/run.py"); err == nil {
		t.Fatal("不該複製")
	}
	// Writer 改了 Chief 寄來、放在 /workspace/from/Chief/ 的那份：新版到 Chief 的 from/Writer/。
	tm.write(writer, "from/Chief/notes.md", "v2")
	tm.write(chief, "notes.md", "v1")
	tm.running(writer, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, writer, "Chief", "改好了 /workspace/from/Chief/notes.md", "fyi", SendOptions{}))
	if tm.read(chief, "from/Writer/notes.md") != "v2" || tm.read(chief, "notes.md") != "v1" {
		t.Fatal("改過的版本放到 from/Writer/，原本的不動")
	}
}

// test_a_file_received_from_one_teammate_can_be_passed_on_to_another
func TestAFileReceivedFromOneTeammateCanBePassedOnToAnother(t *testing.T) {
	tm := setup(t)
	chief, scout := tm.bots["Chief"], tm.bots["Scout"]
	tm.write(chief, "from/Writer/poc/run.py", "fixed")
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Scout", "請驗證 /workspace/from/Writer/poc/run.py", "request", SendOptions{}))
	goal := tm.queued(scout)[0].Input
	if tm.read(scout, "from/Chief/from/Writer/poc/run.py") != "fixed" || !strings.Contains(goal, "/workspace/from/Chief/from/Writer/poc/run.py") {
		t.Fatalf("%s", goal)
	}
}

// test_handing_work_on_defers_the_answer_until_the_teammates_results_are_in
func TestHandingWorkOnDefersTheAnswerUntilTheTeammatesResultsAreIn(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	// Chief 問 Writer；Writer 交給 Scout 後說「已派工」：這句還不能送回 Chief。
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "修好 POC", "request", SendOptions{}))
	request := tm.queued(writer)[0]
	tm.start(request)
	must[SendResult](t)(tm.s.Send(tm.ctx, writer, "Scout", "請你修", "request", SendOptions{}))
	tm.s.finished(tm.task(request.ID), runtime.VerdictAnswer, "已派工給 Scout")
	if len(tm.results(chief)) != 0 {
		t.Fatal("「已派工」不是答案")
	}
	if d := metaOf(tm.task(request.ID)).Deferred; d == nil || d.To != chief {
		t.Fatalf("延後回覆：%+v", d)
	}
	asg := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, metaOf(request).AssignmentID))
	if asg.Status != store.AssignDeferred {
		t.Fatalf("交辦：%+v", asg)
	}
	// Scout 回覆；Writer 處理這個結果的那一輪才是 Chief 拿到的答案。
	scoutTask := tm.queued(scout)[0]
	tm.s.finished(scoutTask, runtime.VerdictAnswer, "修好了")
	rs := tm.results(writer)
	if len(rs) != 1 || metaOf(rs[0]).Answers != request.ID || !strings.Contains(rs[0].Input, "自動送回給 Chief") {
		t.Fatalf("%+v", rs)
	}
	tm.s.finished(rs[0], runtime.VerdictAnswer, "整合後：POC 修好且驗過")
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(back[0].Input, "整合後：POC 修好且驗過") {
		t.Fatalf("%+v", back)
	}
	if asg := must[store.Assignment](t)(tm.db.GetAssignment(tm.ctx, metaOf(request).AssignmentID)); asg.Status != store.AssignDone {
		t.Fatalf("交辦：%+v", asg)
	}
}

// test_a_teammate_that_answers_before_the_handing_on_turn_ends_does_not_lose_the_real_answer
func TestATeammateThatAnswersBeforeTheHandingOnTurnEndsDoesNotLoseTheRealAnswer(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "修好 POC", "request", SendOptions{}))
	request := tm.queued(writer)[0]
	tm.start(request)
	must[SendResult](t)(tm.s.Send(tm.ctx, writer, "Scout", "請你修", "request", SendOptions{}))
	// Scout 很快：它的答案排在 Writer 還在進行的那一輪後面。
	tm.s.finished(tm.queued(scout)[0], runtime.VerdictAnswer, "修好了")
	if !tm.s.answeredLater(tm.ctx, writer, request.ID) {
		t.Fatal("結果要排在後面")
	}
	tm.s.finished(tm.task(request.ID), runtime.VerdictAnswer, "已派工給 Scout")
	if len(tm.results(chief)) != 0 {
		t.Fatal("不能先送「已派工」")
	}
	if d := metaOf(tm.task(request.ID)).Deferred; d == nil || d.To != chief {
		t.Fatalf("%+v", d)
	}
	rs := tm.results(writer)
	tm.s.finished(rs[0], runtime.VerdictAnswer, "整合後：修好了")
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(back[0].Input, "整合後：修好了") {
		t.Fatalf("%+v", back)
	}
}

// test_a_request_cut_off_by_a_dropped_connection_is_reported_but_a_user_stop_is_not
func TestARequestCutOffByADroppedConnectionIsReportedButAUserStopIsNot(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "整理報告", "request", SendOptions{}))
	tm.s.finished(tm.queued(writer)[0], runtime.VerdictCancelled, "")
	if len(tm.results(chief)) != 0 {
		t.Fatal("你停下的不回報（LazyBoy；BL-4 待補）")
	}
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "再整理一次", "request", SendOptions{}))
	second := tm.queued(writer)
	tm.s.finished(second[len(second)-1], runtime.VerdictInterrupted, "")
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(back[0].Input, "被中斷") || !strings.Contains(back[0].Input, "沒有完成") {
		t.Fatalf("%+v", back)
	}
}

// test_deleting_a_bot_tells_the_ones_still_waiting_on_it
func TestDeletingABotTellsTheOnesStillWaitingOnIt(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	origin := tm.running(chief, nil)
	sent := must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "整理報告", "request", SendOptions{}))
	if len(tm.s.waitingOn(tm.ctx, origin)) == 0 {
		t.Fatal("要在等")
	}
	tm.s.ForgetBot(tm.ctx, writer)
	if m := must[store.BotMessage](t)(tm.db.GetBotMessage(tm.ctx, sent.MessageID)); !m.Answered || len(tm.s.waitingOn(tm.ctx, origin)) != 0 {
		t.Fatal("不用再等了")
	}
	back := tm.results(chief)
	if len(back) != 1 || !strings.Contains(back[0].Input, "已經被刪除") {
		t.Fatalf("%+v", back)
	}
}

// test_a_message_that_could_not_be_handed_over_is_not_left_waiting
func TestAMessageThatCouldNotBeHandedOverIsNotLeftWaiting(t *testing.T) {
	tm := setup(t)
	origin := tm.running(tm.bots["Chief"], nil)
	tm.rt.refuse = errors.New("gone")
	if _, err := tm.s.Send(tm.ctx, tm.bots["Chief"], "Writer", "整理報告", "request", SendOptions{}); err == nil {
		t.Fatal("要回錯誤")
	}
	if len(tm.s.waitingOn(tm.ctx, origin)) != 0 {
		t.Fatal("交不出去的不能一直等")
	}
}

// test_mentions_need_the_whole_name
func TestMentionsNeedTheWholeName(t *testing.T) {
	members := []Member{{"w", "王"}, {"b", "bob"}, {"l", "王小明"}}
	ids := func(text string) string {
		named, _ := Mentions(text, members)
		var out []string
		for _, m := range named {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	if ids("@王小明 你好") != "l" || ids("@王，你好 @bob") != "w,b" || ids("寄到 x@bobby.com") != "" || ids("@bobby") != "" {
		t.Fatal(ids("@王小明 你好"), ids("@王，你好 @bob"), ids("寄到 x@bobby.com"), ids("@bobby"))
	}
}

// test_posting_to_a_group_from_inside_it_continues_the_round_count_and_stops_at_the_limit
func TestPostingToAGroupFromInsideItContinuesTheRoundCountAndStopsAtTheLimit(t *testing.T) {
	tm := setup(t)
	chief := tm.bots["Chief"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, tm.bots["Writer"]}, RoomOptions{Name: "群"}))
	for _, c := range []struct {
		round int
		wakes bool
	}{{0, true}, {limits.RoomRounds - 1, true}, {limits.RoomRounds, false}} {
		tm.running(chief, map[string]any{"room_id": room.ID, "round": c.round})
		res := must[PostGroupResult](t)(tm.s.PostGroup(tm.ctx, chief, "群", "@Writer 接著做"))
		if (len(res.Woke) > 0) != c.wakes {
			t.Fatalf("第 %d 輪：%+v", c.round, res)
		}
		if !c.wakes && !strings.Contains(res.Note, "不再喚醒") {
			t.Fatalf("%+v", res)
		}
	}
	// 在群組外（Bot 自己的對話）發言重新算。
	tm.running(chief, nil)
	if res := must[PostGroupResult](t)(tm.s.PostGroup(tm.ctx, chief, "群", "@Writer 看一下")); strings.Join(res.Woke, ",") != "Writer" {
		t.Fatalf("%+v", res)
	}
}

// test_reply_for_a_deleted_group_chat_stays_silent（LazyBoy tests/test_runtime.py）
func TestReplyForADeletedGroupChatStaysSilent(t *testing.T) {
	tm := setup(t)
	task := tm.running(tm.bots["Chief"], map[string]any{"room_id": "gone"})
	if !tm.s.silent(task, runtime.VerdictCancelled, "已停止") {
		t.Fatal("群組刪掉了，回覆也不顯示")
	}
	tm.finish(task, runtime.VerdictCancelled, "已停止")
}
