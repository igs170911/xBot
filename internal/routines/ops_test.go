package routines

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

func agent(id string, botIDs []string) ops.Identity {
	return ops.Identity{Kind: ops.KindAgent, ID: id, Name: "代理" + id, Via: "mcp",
		Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}, Resources: ops.Resources{Bots: botIDs}}
}

func TestProposeReturnsAConfirmCard(t *testing.T) {
	e := setup(t)
	e.clock.Set(at(taipei, 2026, 10, 13, 10, 0))
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "阿寶", Engine: "hermes"})
	p := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "每天早上 9 點整理新聞給我"})
	if !p.Ready || p.Draft.BotID != e.bot.ID || p.BotName != "小秘" || p.Draft.Prompt != "整理新聞給我" || p.Draft.Schedule.Kind != KindDaily {
		t.Fatalf("草稿：%+v", p)
	}
	if p.Card.When != "每天上午 9:00" || p.Card.Next != "明天上午 9:00" || p.Card.Who != "小秘" || p.Card.PostsTo != "和小秘的對話" ||
		p.NextRunAt != at(taipei, 2026, 10, 14, 9, 0).UnixMilli() {
		t.Fatalf("確認卡：%+v", p.Card)
	}
	// 提到名字就指派給它。
	if p := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "平日下午三點請阿寶整理報表"}); p.Draft.BotID != other.ID || p.Card.Next != "今天下午 3:00" {
		t.Fatalf("阿寶：%+v", p)
	}
	// 沒寫時間：不能建立，什麼時候欄要問。
	miss := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "每天整理新聞"})
	if miss.Ready || miss.Card.When != "沒看到時間，要幾點做？" || !slices.Contains(miss.Missing, "time") {
		t.Fatalf("缺時間：%+v", miss)
	}
	// 時間已過的單次。
	gone := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "今天早上八點寄信"})
	if gone.Ready || gone.Problem == "" {
		t.Fatalf("過去的時間：%+v", gone)
	}
	// Bot 自己呼叫：預設是它自己。
	bp := get[Proposal](t, e, ops.Bot(other.ID, other.Name), "xbot.routines.propose", ProposeIn{Text: "每天晚上 8 點寫日記"})
	if bp.Draft.BotID != other.ID {
		t.Fatalf("Bot 自己：%+v", bp)
	}
	// 草稿可以直接交給 create。
	made := get[CreateOut](t, e, owner, "xbot.routines.create", p.Draft)
	if made.Routine == nil || made.Routine.When != "每天上午 9:00" || made.Routine.CreatedBy != "owner" {
		t.Fatalf("建立：%+v", made)
	}
	// 也可以只給一句話。
	byText := get[CreateOut](t, e, owner, "xbot.routines.create", CreateIn{Text: "每 30 分鐘檢查網站"})
	if byText.Routine == nil || byText.Routine.Schedule.EveryMinutes != 30 || byText.Routine.Prompt != "檢查網站" {
		t.Fatalf("一句話：%+v", byText)
	}
	if _, err := e.call(owner, "xbot.routines.create", CreateIn{Text: "每天整理新聞"}); !strings.Contains(message(err), "沒看到時間") {
		t.Fatalf("一句話缺時間：%v", err)
	}
}

// RT-2：Bot 提議 → 你在待我處理確認後才建立。
func TestBotProposesOwnerConfirms(t *testing.T) {
	e := setup(t)
	bot := ops.Bot(e.bot.ID, e.bot.Name)
	out := get[CreateOut](t, e, bot, "xbot.routines.create", CreateIn{Text: "每天早上 9 點整理新聞給我"})
	if !out.Proposed || out.ApprovalID == "" || out.Routine != nil {
		t.Fatalf("要先送確認：%+v", out)
	}
	if rs, _ := e.db.ListRoutines(bg, ""); len(rs) != 0 {
		t.Fatal("確認前不能建立")
	}
	cards := e.inbox(t, "approval")
	if len(cards) != 1 || !strings.Contains(cards[0].Title, "小秘 想新增定時任務") || !strings.Contains(cards[0].Body, "每天上午 9:00") ||
		!strings.Contains(cards[0].Body, "整理新聞給我") {
		t.Fatalf("確認卡：%+v", cards)
	}
	if err := e.db.DecideApproval(bg, out.ApprovalID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := e.reg.ExecuteApproved(bg, out.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	var done CreateOut
	_ = json.Unmarshal(res.Output, &done)
	if done.Routine == nil || done.Routine.CreatedBy != "bot:"+e.bot.ID || done.Routine.Schedule.Time != "09:00" || done.Routine.Status != store.RoutineActive {
		t.Fatalf("確認後建立：%+v", done)
	}
	// 代理也一樣要確認；預演不寫任何東西。
	a := agent("a1", nil)
	dry := get[CreateOut](t, e, a, "xbot.routines.create", CreateIn{BotID: e.bot.ID, Prompt: "x", Schedule: &weekly}, ops.Options{DryRun: true})
	if !dry.Proposed || dry.ApprovalID != "" {
		t.Fatalf("預演：%+v", dry)
	}
	if len(e.inbox(t, "approval")) != 1 {
		t.Fatal("預演不能送卡")
	}
	if out := get[CreateOut](t, e, a, "xbot.routines.create", CreateIn{BotID: e.bot.ID, Prompt: "x", Schedule: &weekly}); !out.Proposed {
		t.Fatalf("代理：%+v", out)
	}
	// Bot 不能幫別的 Bot 建、也看不到別的 Bot 的定時任務。
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "阿寶", Engine: "hermes"})
	if _, err := e.call(bot, "xbot.routines.create", CreateIn{BotID: other.ID, Prompt: "x", Schedule: &weekly}); code(err) != ops.CodeForbidden {
		t.Fatalf("別的 Bot：%v", err)
	}
	e.bot = other
	theirs := e.create(t, CreateIn{})
	if l := get[ListOut](t, e, bot, "xbot.routines.list", ListIn{}); len(l.Routines) != 1 || l.Routines[0].BotID == other.ID {
		t.Fatalf("Bot 只看自己的：%+v", l)
	}
	if _, err := e.call(bot, "xbot.routines.get", IDIn{ID: theirs.ID}); code(err) != ops.CodeNotFound {
		t.Fatalf("別人的：%v", err)
	}
	if _, err := e.call(bot, "xbot.routines.pause", IDIn{ID: done.Routine.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("Bot 不能暫停（沒開放給 Bot）：%v", err)
	}
	if _, err := e.call(agent("a2", []string{other.ID}), "xbot.routines.pause", IDIn{ID: done.Routine.ID}); code(err) != ops.CodeNotFound {
		t.Fatalf("代理碰不到的 Bot：%v", err)
	}
}

func TestCreateLimitsAndValidation(t *testing.T) {
	e := setup(t)
	for range limits.RoutinesPerBot {
		if _, err := e.db.CreateRoutine(bg, store.Routine{BotID: e.bot.ID, Title: "x", Prompt: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.call(owner, "xbot.routines.create", CreateIn{BotID: e.bot.ID, Prompt: "x", Schedule: &weekly}); code(err) != ops.CodeLimit {
		t.Fatalf("每個 Bot 最多 50 個：%v", err)
	}
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "阿寶", Engine: "hermes"})
	for _, c := range []struct {
		in    CreateIn
		field string
	}{
		{CreateIn{BotID: other.ID, Schedule: &weekly}, "prompt"},
		{CreateIn{BotID: other.ID, Prompt: "x", Schedule: &Schedule{Kind: KindInterval, EveryMinutes: 3}}, "schedule.every_minutes"},
		{CreateIn{BotID: other.ID, Prompt: "x", Schedule: &Schedule{Kind: KindInterval, EveryMinutes: 30}, JitterMinutes: 30}, "jitter_minutes"},
		{CreateIn{BotID: other.ID, Prompt: "x", Schedule: &Schedule{Kind: KindOnce, Date: "2020-01-01", Time: "09:00"}}, "schedule.date"},
		{CreateIn{BotID: other.ID, Prompt: "x", Schedule: &Schedule{Kind: KindDaily, Time: "09:00", EndDate: "2020-01-01"}}, "schedule.end_date"},
		{CreateIn{BotID: other.ID, Prompt: "x", Schedule: &Schedule{Kind: KindDaily, Time: "25:00"}}, "schedule.time"},
		{CreateIn{Prompt: "x", Schedule: &weekly}, "bot_id"},
	} {
		_, err := e.call(owner, "xbot.routines.create", c.in)
		var oe *ops.Error
		if code(err) != ops.CodeInvalidInput || !asOps(err, &oe) || len(oe.Fields) == 0 || oe.Fields[0].Field != c.field {
			t.Errorf("%+v：%v，要 %s", c.in, err, c.field)
		}
	}
	if _, err := e.call(owner, "xbot.routines.create", CreateIn{BotID: "nope", Prompt: "x", Schedule: &weekly}); code(err) != ops.CodeNotFound {
		t.Fatalf("沒有這個 Bot：%v", err)
	}
	// 隨機延後：下一次落在排定時間之後 0～N 分鐘（假亂數取最大）。
	e.svc.o.Rand = func(n int64) int64 { return n - 1 }
	v := e.create(t, CreateIn{BotID: other.ID, Schedule: &Schedule{Kind: KindDaily, Time: "20:00"}, JitterMinutes: 30})
	if v.NextRunAt != at(taipei, 2026, 10, 13, 20, 30).UnixMilli() || v.JitterMinutes != 30 {
		t.Fatalf("隨機延後：%+v", v)
	}
}

func asOps(err error, oe **ops.Error) bool {
	e, ok := err.(*ops.Error)
	*oe = e
	return ok
}

// LazyBoy test_partial_update_keeps_other_fields_and_paused_state
func TestPartialUpdateKeepsOtherFieldsAndPausedState(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{})
	get[View](t, e, owner, "xbot.routines.pause", IDIn{ID: r.ID})
	empty := ""
	updated := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, Schedule: &Schedule{Kind: KindWeekly, Weekdays: []int{1}, Time: "10:30"}})
	if updated.Title != "週報" || updated.Prompt != "整理數據" || updated.Schedule.Time != "10:30" || !slices.Equal(updated.Schedule.Weekdays, []int{1}) ||
		updated.Status != store.RoutinePaused {
		t.Fatalf("部分更新：%+v", updated)
	}
	renamed := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, Title: ptr("新名稱"), ThreadID: &empty})
	if renamed.Title != "新名稱" || renamed.Schedule.Kind != KindWeekly || renamed.Schedule.Time != "10:30" {
		t.Fatalf("改名：%+v", renamed)
	}
	// 繼續：重新排下一次（週一 10:30）。
	resumed := get[View](t, e, owner, "xbot.routines.resume", IDIn{ID: r.ID})
	if resumed.Status != store.RoutineActive || resumed.NextRunAt != at(taipei, 2026, 10, 19, 10, 30).UnixMilli() {
		t.Fatalf("繼續：%+v", resumed)
	}
	// 修改會清掉退避。
	e.patch(t, r.ID, func(r *store.Routine) { r.Failures, r.RetryAt, r.LastError = 3, 1, "x" })
	if v := get[View](t, e, owner, "xbot.routines.update", UpdateIn{ID: r.ID, JitterMinutes: ptr(15)}); v.Failures != 0 || v.RetryAt != 0 || v.LastError != "" {
		t.Fatalf("清掉退避：%+v", v)
	}
	if _, err := e.call(owner, "xbot.routines.update", UpdateIn{ID: r.ID, ThreadID: ptr("nope")}); code(err) != ops.CodeNotFound {
		t.Fatalf("別的對話串：%v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// LazyBoy test_list_shows_last_run_and_history_is_newest_first
func TestListAndLogs(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{})
	first := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: r.ID})
	if _, err := e.call(owner, "xbot.routines.run_now", IDIn{ID: r.ID}); code(err) != ops.CodeConflict || !strings.Contains(message(err), "正在忙") {
		t.Fatalf("Bot 在忙：%v", err)
	}
	e.rt.Finish(t, first.Run.TaskID, runtime.VerdictAnswer)
	waitFor(t, "寫回完成", func() bool { r, _ := e.db.GetRoutineRun(bg, first.Run.ID); return r.Status == store.RunDone })
	second := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: r.ID})
	l := get[ListOut](t, e, owner, "xbot.routines.list", ListIn{})
	if len(l.Routines) != 1 || l.Routines[0].LastTaskID != second.Run.TaskID || !l.Routines[0].Running || l.Routines[0].When != "每週一上午 9:00" {
		t.Fatalf("清單：%+v", l)
	}
	logs := get[LogsOut](t, e, owner, "xbot.routines.logs", LogsIn{ID: r.ID})
	if len(logs.Runs) != 3 || logs.Runs[0].ID != second.Run.ID || logs.Runs[1].Status != store.RunFailed || logs.Runs[2].Status != store.RunDone {
		t.Fatalf("新到舊：%+v", logs.Runs)
	}
	// 只保留最近 20 筆。
	for range limits.RoutineHistory + 5 {
		e.rt.Finish(t, e.routine(t, r.ID).LastTaskID, runtime.VerdictAnswer)
		get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: r.ID})
	}
	if runs := e.runs(t, r.ID); len(runs) != limits.RoutineHistory {
		t.Fatalf("保留 %d 筆：%d", limits.RoutineHistory, len(runs))
	}
}

func TestDeleteNeedsYou(t *testing.T) {
	e := setup(t)
	r := e.create(t, CreateIn{})
	a := agent("a1", nil)
	if _, err := e.call(a, "xbot.routines.delete", IDIn{ID: r.ID}); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理刪除要你核准：%v", err)
	}
	info, _ := e.reg.Get("xbot.routines.delete")
	if info.Risk != ops.RiskConfirm || !info.NoDelegate {
		t.Fatalf("不能代為核准：%+v", info)
	}
	dry := get[DeleteOut](t, e, owner, "xbot.routines.delete", IDIn{ID: r.ID}, ops.Options{DryRun: true})
	if dry.Deleted || dry.Title != "週報" {
		t.Fatalf("預演：%+v", dry)
	}
	get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: r.ID})
	if out := get[DeleteOut](t, e, owner, "xbot.routines.delete", IDIn{ID: r.ID}); !out.Deleted {
		t.Fatalf("刪除：%+v", out)
	}
	if runs := e.runs(t, r.ID); len(runs) != 0 {
		t.Fatal("執行紀錄一起刪")
	}
}

func TestCalendar(t *testing.T) {
	e := setup(t)
	// 週二 09:00；這週（週一～週日）。
	weekStart := at(taipei, 2026, 10, 12, 0, 0)
	weekEnd := weekStart.AddDate(0, 0, 7)
	daily := e.create(t, CreateIn{Title: "日報", Schedule: &Schedule{Kind: KindDaily, Time: "18:00"}, JitterMinutes: 30})
	e.create(t, CreateIn{Title: "週報", Schedule: &Schedule{Kind: KindWeekly, Time: "09:00", Weekdays: []int{1, 5}}})
	busy := e.create(t, CreateIn{Title: "巡站", Schedule: &Schedule{Kind: KindInterval, EveryMinutes: 5}})
	paused := e.create(t, CreateIn{Title: "暫停的", Schedule: &Schedule{Kind: KindDaily, Time: "10:00"}})
	get[View](t, e, owner, "xbot.routines.pause", IDIn{ID: paused.ID})
	run := get[RunOut](t, e, owner, "xbot.routines.run_now", IDIn{ID: daily.ID})
	e.rt.Finish(t, run.Run.TaskID, runtime.VerdictFailed)
	waitFor(t, "寫回失敗", func() bool { r, _ := e.db.GetRoutineRun(bg, run.Run.ID); return r.Status == store.RunFailed })
	cal := get[CalendarOut](t, e, owner, "xbot.routines.calendar", CalendarIn{From: weekStart.UnixMilli(), To: weekEnd.UnixMilli()})
	count := map[string]int{}
	var failed, firstDaily *CalendarItem
	for i, it := range cal.Items {
		count[it.Title]++
		if it.Status == store.RunFailed {
			failed = &cal.Items[i]
		}
		if it.Title == "日報" && it.Status == "scheduled" && firstDaily == nil {
			firstDaily = &cal.Items[i]
		}
		if i > 0 && cal.Items[i-1].At > it.At {
			t.Fatal("要依時間排序")
		}
	}
	// 日報：今天～週日 6 次排程＋1 次失敗紀錄；週報：週五 1 次（週一已過）；巡站最多 100 格；暫停的不列。
	if count["日報"] != 7 || count["週報"] != 1 || count["巡站"] != limits.RoutineCalendarPerRoutine || count["暫停的"] != 0 || !cal.Truncated {
		t.Fatalf("行事曆：%v truncated=%v", count, cal.Truncated)
	}
	if failed == nil || failed.RoutineID != daily.ID || failed.RunID != run.Run.ID {
		t.Fatalf("失敗的紀錄：%+v", failed)
	}
	if firstDaily.At != at(taipei, 2026, 10, 13, 18, 0).UnixMilli() {
		t.Fatalf("隨機延後的以排定時間顯示：%s", time.UnixMilli(firstDaily.At).In(taipei))
	}
	// 依 Bot 篩選、範圍上限。
	other, _ := e.db.CreateBot(bg, store.Bot{Name: "阿寶", Engine: "hermes"})
	if c := get[CalendarOut](t, e, owner, "xbot.routines.calendar", CalendarIn{From: weekStart.UnixMilli(), To: weekEnd.UnixMilli(), BotID: other.ID}); len(c.Items) != 0 {
		t.Fatalf("依 Bot：%+v", c.Items)
	}
	if _, err := e.call(owner, "xbot.routines.calendar", CalendarIn{From: 0, To: weekEnd.UnixMilli()}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("太長：%v", err)
	}
	_ = busy
}
