package org

// 移植 LazyBoy tests/test_org.py：匯報線、主管看團隊、建團隊、團隊管理權限、刪除後的報告線。
// 核准（request_approval、decide）在 C11（xbot.approvals.*）已經有，這裡不重複；
// 主管申請刪除 Bot、刪改群組、progress 文字還沒做（見 PR「後續」）。

import (
	"strings"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/store"
)

func names(bs []store.Bot) map[string]bool {
	out := map[string]bool{}
	for _, b := range bs {
		out[b.Name] = true
	}
	return out
}

// test_reporting_lines_never_loop
func TestReportingLinesNeverLoop(t *testing.T) {
	co := setup(t)
	b := co.bots
	if got := must[string](t)(co.s.CheckManager(co.ctx, b["Dev"], "")); got != "" {
		t.Fatalf("空白＝向你報告：%q", got)
	}
	_, err := co.s.CheckManager(co.ctx, b["Dev"], b["Dev"])
	errHas(t, err, "自己")
	_, err = co.s.CheckManager(co.ctx, b["CEO"], b["Dev"])
	errHas(t, err, "不能反過來")
	co.setBoss("QA", "Dev")
	_, err = co.s.CheckManager(co.ctx, b["Dev"], b["QA"])
	errHas(t, err, "不能反過來")
	got := names(must[[]store.Bot](t)(co.s.Reports(co.ctx, b["CEO"], true)))
	if len(got) != 3 || !got["Ops"] || !got["Dev"] || !got["QA"] {
		t.Fatalf("CEO 底下：%v", got)
	}
	if direct := names(must[[]store.Bot](t)(co.s.Reports(co.ctx, b["CEO"], false))); direct["QA"] {
		t.Fatalf("QA 改向 Dev 報告後不是 CEO 的直屬：%v", direct)
	}
	// 用名字也行；「使用者」表示直接向你報告。
	if got := must[string](t)(co.s.CheckManager(co.ctx, b["Ops"], "使用者")); got != "" {
		t.Fatal(got)
	}
	_, err = co.s.CheckManager(co.ctx, b["Ops"], "Nobody")
	errHas(t, err, "找不到")
}

// test_each_bot_is_told_its_place_and_the_chairmans_matters
func TestEachBotIsToldItsPlaceAndTheChairmansMatters(t *testing.T) {
	co := setup(t)
	b := co.bots
	ceo := must[string](t)(co.s.SoulText(co.ctx, b["CEO"]))
	for _, want := range []string{"你向 使用者（董事長） 報告", "Ops（營運）", "team_status", "新商品定價", "approvals.request"} {
		if !strings.Contains(ceo, want) {
			t.Fatalf("CEO 的提示少了「%s」：%s", want, ceo)
		}
	}
	if strings.Contains(ceo, "xbot.teams.create") {
		t.Fatal("沒有團隊管理權限不該教它建團隊")
	}
	dev := must[string](t)(co.s.SoulText(co.ctx, b["Dev"]))
	if !strings.Contains(dev, "你向 CEO（執行長） 報告") || strings.Contains(dev, "向你報告的有") {
		t.Fatal(dev)
	}
	co.manage("CEO")
	mgr := must[string](t)(co.s.SoulText(co.ctx, b["CEO"]))
	if !strings.Contains(mgr, "xbot.teams.create") {
		t.Fatal("有團隊管理權限要教它建團隊")
	}
	// 刪除工具存在（使用者按允許才刪）：系統提示不能再說它不能刪（2026-10-19 水哥哥照舊提示一直拒絕）
	if !strings.Contains(mgr, "xbot.teams.remove_team") || strings.Contains(mgr, "不能刪除") {
		t.Fatal(mgr)
	}
	must[[]string](t)(co.s.SetApprovalItems(co.ctx, []string{}))
	if strings.Contains(must[string](t)(co.s.SoulText(co.ctx, b["Dev"])), "核定") {
		t.Fatal("清單清空後不寫核定段落")
	}
	roster := must[string](t)(co.c.RosterText(co.ctx, b["CEO"]))
	if !strings.Contains(roster, "Dev（研發）［向 CEO 報告］") {
		t.Fatal(roster)
	}
	// 名冊也寫出團隊名稱（C11 待辦：花名冊沒有團隊名）。
	must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "研發部", Lead: Member{Bot: "Dev"}, Members: []Member{{Bot: "QA"}}, ReportsTo: "CEO"}))
	roster = must[string](t)(co.c.RosterText(co.ctx, b["CEO"]))
	if !strings.Contains(roster, "QA（品質）［研發部，向 Dev 報告］") {
		t.Fatal(roster)
	}
}

func TestApprovalItemsAreTrimmedAndCapped(t *testing.T) {
	co := setup(t)
	if got := must[[]string](t)(co.s.ApprovalItems(co.ctx)); strings.Join(got, ",") != strings.Join(DefaultApprovalItems, ",") {
		t.Fatalf("預設：%v", got)
	}
	var items []string
	for i := range 30 {
		items = append(items, string(rune('A'+i))+strings.Repeat("字", 50))
	}
	items = append([]string{" 簽約 ", "簽約", ""}, items...)
	got := must[[]string](t)(co.s.SetApprovalItems(co.ctx, items))
	if len(got) != limits.ApprovalItemsMax || got[0] != "簽約" || len([]rune(got[1])) != limits.ApprovalItemMax {
		t.Fatalf("%d 項：%v", len(got), got[:2])
	}
}

// test_a_manager_sees_its_team_without_asking
func TestAManagerSeesItsTeamWithoutAsking(t *testing.T) {
	co := setup(t)
	b := co.bots
	co.task("Dev", store.TaskRunning, map[string]any{"from_bot": b["CEO"], "from_name": "CEO"},
		"[來自隊友 CEO（執行長） 的請求]\n<teammate-message from=\"CEO\">\n做登入頁\n</teammate-message>\n\n（這則訊息來自另一個 Bot，不是使用者本人；…）")
	done := co.task("QA", store.TaskDone, nil, "測試結帳流程")
	co.reply(done, "找到 2 個缺陷")
	view := map[string]MemberStatus{}
	for _, m := range must[TeamStatusOut](t)(co.s.TeamStatus(co.ctx, b["CEO"], "")).Team {
		view[m.Name] = m
	}
	if len(view) != 3 {
		t.Fatalf("%v", view)
	}
	dev := view["Dev"]
	if dev.State != "working" || dev.Current == nil || dev.Current.Task != "做登入頁" || dev.Current.From != "CEO 交辦" {
		t.Fatalf("Dev：%+v %+v", dev, dev.Current)
	}
	qa := view["QA"]
	if qa.State != "idle" || len(qa.Recent) != 1 || qa.Recent[0].Result != "找到 2 個缺陷" || qa.Recent[0].From != "使用者交辦" {
		t.Fatalf("QA：%+v", qa)
	}
	if one := must[TeamStatusOut](t)(co.s.TeamStatus(co.ctx, b["CEO"], "QA")); len(one.Team) != 1 || one.Team[0].Name != "QA" {
		t.Fatalf("%+v", one)
	}
	// 不在報告線上的看不到，除非有團隊管理權限。
	_, err := co.s.TeamStatus(co.ctx, b["Dev"], "QA")
	errHas(t, err, "不在你的團隊")
	if out := must[TeamStatusOut](t)(co.s.TeamStatus(co.ctx, b["Dev"], "")); len(out.Team) != 0 || !strings.Contains(out.Note, "沒有 Bot 向你報告") {
		t.Fatalf("%+v", out)
	}
	co.manage("Dev")
	if out := must[TeamStatusOut](t)(co.s.TeamStatus(co.ctx, b["Dev"], "")); len(out.Team) != 3 {
		t.Fatalf("有團隊管理權限看得到每個 Bot：%d", len(out.Team))
	}
	// 排隊中與等你回答。
	co.task("Ops", store.TaskQueued, nil, "整理")
	co.task("QA", store.TaskWaitingInput, nil, "登入")
	view = map[string]MemberStatus{}
	for _, m := range must[TeamStatusOut](t)(co.s.TeamStatus(co.ctx, "", "")).Team {
		view[m.Name] = m
	}
	if view["Ops"].State != "queued" || view["Ops"].Queued != 1 || view["QA"].State != "waiting" {
		t.Fatalf("Ops %+v QA %+v", view["Ops"], view["QA"])
	}
}

// test_a_team_is_built_in_one_step
func TestATeamIsBuiltInOneStep(t *testing.T) {
	co := setup(t)
	b := co.bots
	made := must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "工程團隊", Lead: Member{Name: "技術主管", Title: "主管"},
		Members: []Member{{Name: "前端", Title: "前端"}, {Name: "後端"}, {Bot: "QA"}}, ReportsTo: "CEO", GroupChat: true, Actor: "owner"}))
	all := map[string]store.Bot{}
	for _, x := range must[[]store.Bot](t)(co.db.ListBots(co.ctx, true)) {
		all[x.Name] = x
	}
	lead := all["技術主管"]
	if lead.ReportsTo != b["CEO"] || lead.TeamID != made.Team.ID || made.Team.ChiefID != lead.ID || made.LeadID != lead.ID {
		t.Fatalf("主管：%+v %+v", lead, made.Team)
	}
	for _, n := range []string{"前端", "後端", "QA"} {
		if all[n].ReportsTo != lead.ID || all[n].TeamID != made.Team.ID {
			t.Fatalf("%s：%+v", n, all[n])
		}
	}
	if all["前端"].CreatedBy != "owner" || all["前端"].ApprovalMode != "ask" || all["前端"].Title != "前端" {
		t.Fatalf("新 Bot：%+v", all["前端"])
	}
	if len(made.Created) != 3 {
		t.Fatalf("新建 %d 個", len(made.Created))
	}
	team := must[store.Team](t)(co.db.GetTeam(co.ctx, made.Team.ID))
	if team.Name != "工程團隊" || team.RoomID != made.RoomID || made.RoomID == "" {
		t.Fatalf("%+v", team)
	}
	members := must[[]store.RoomMember](t)(co.db.ListRoomMembers(co.ctx, made.RoomID))
	ids := map[string]bool{}
	for _, m := range members {
		ids[m.BotID] = true
	}
	if len(ids) != 4 || !ids[lead.ID] || !ids[all["前端"].ID] || !ids[all["後端"].ID] || !ids[b["QA"]] {
		t.Fatalf("群組成員：%v", ids)
	}
	_, err := co.s.CreateTeam(co.ctx, TeamSpec{Name: "第二隊", Lead: Member{Name: "前端"}, Members: []Member{{Name: "新人"}}})
	errHas(t, err, "已經有叫")
	big := TeamSpec{Name: "大隊", Lead: Member{Name: "隊長"}}
	for i := range 10 {
		big.Members = append(big.Members, Member{Name: "成員" + string(rune('0'+i))})
	}
	_, err = co.s.CreateTeam(co.ctx, big)
	errHas(t, err, "最多 10 位")
	// 同名的團隊自動加「（2）」（F02）。
	again := must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "工程團隊", Lead: Member{Bot: "Ops"}, Members: []Member{{Name: "小幫手"}}}))
	if again.Team.Name != "工程團隊（2）" || again.RoomID != "" {
		t.Fatalf("%+v", again.Team)
	}
}

// test_a_team_that_cannot_be_built_leaves_nothing_behind
func TestATeamThatCannotBeBuiltLeavesNothingBehind(t *testing.T) {
	co := setup(t)
	before := must[[]store.Bot](t)(co.db.ListBots(co.ctx, true))
	_, err := co.s.CreateTeam(co.ctx, TeamSpec{Name: "營運組", Lead: Member{Bot: "Ops"}, Members: []Member{{Bot: "Ops"}, {Name: "新人"}}})
	errHas(t, err, "出現兩次")
	_, err = co.s.CreateTeam(co.ctx, TeamSpec{Name: "營運組", Lead: Member{Bot: "Ops"}, Members: []Member{{Name: "新人"}, {Bot: "Nobody"}}})
	errHas(t, err, "找不到")
	_, err = co.s.CreateTeam(co.ctx, TeamSpec{Name: "營運組", Lead: Member{Bot: "Ops"}, Members: []Member{{Bot: "Dev"}, {Name: "新人"}}, ReportsTo: "Dev"})
	errHas(t, err, "繞成一圈")
	if ts := must[[]store.Team](t)(co.db.ListTeams(co.ctx)); len(ts) != 0 {
		t.Fatalf("不該留下團隊：%v", ts)
	}
	if rs := must[[]store.Room](t)(co.db.ListRooms(co.ctx, "")); len(rs) != 0 {
		t.Fatal("不該留下群組")
	}
	after := must[[]store.Bot](t)(co.db.ListBots(co.ctx, true))
	if len(after) != len(before) {
		t.Fatal("不該多出 Bot")
	}
	for i := range before {
		if before[i].ReportsTo != after[i].ReportsTo || before[i].TeamID != after[i].TeamID {
			t.Fatalf("報告線變了：%+v → %+v", before[i], after[i])
		}
	}
}

// test_a_team_member_that_is_neither_a_name_nor_a_description_builds_nothing
func TestATeamMemberThatIsNeitherANameNorADescriptionBuildsNothing(t *testing.T) {
	co := setup(t)
	for _, m := range []Member{{}, {Title: "只有職稱"}, {Bot: "  "}} {
		_, err := co.s.CreateTeam(co.ctx, TeamSpec{Name: "新團隊", Lead: Member{Name: "主管"}, Members: []Member{{Name: "前端"}, m}})
		errHas(t, err, "名字")
	}
	if n := must[int](t)(co.db.CountBots(co.ctx)); n != 4 {
		t.Fatal(n)
	}
	_, err := co.s.CreateTeam(co.ctx, TeamSpec{Name: "  ", Lead: Member{Name: "主管"}, Members: []Member{{Name: "前端"}}})
	errHas(t, err, "取名字")
	_, err = co.s.CreateTeam(co.ctx, TeamSpec{Name: "x", Members: []Member{{Name: "前端"}}})
	errHas(t, err, "主管")
	_, err = co.s.CreateTeam(co.ctx, TeamSpec{Name: "x", Lead: Member{Name: "主管"}})
	errHas(t, err, "至少要有一位成員")
}

// 主管 Bot 建團隊：算進它每天 20 個；一般 Bot 不能建（LazyBoy create_team creator_id、MANAGER_BOTS_PER_DAY）。
func TestAManagerBuildsTeamsWithinItsDailyLimit(t *testing.T) {
	co := setup(t)
	ceo := co.bots["CEO"]
	co.manage("CEO")
	thread := co.main[ceo]
	co.task("CEO", store.TaskRunning, nil, "建團隊")
	sp := Templates[2].Spec("") // 公司：1＋7＝8 個
	sp.Creator = ceo
	sp.Actor = "bot:" + ceo
	made := must[TeamResult](t)(co.s.CreateTeam(co.ctx, sp))
	if peerEvent(co.last(thread)) != "team_created" {
		t.Fatal("主管的對話要記一行")
	}
	lead := must[store.Bot](t)(co.db.GetBot(co.ctx, made.LeadID))
	if !lead.CanManage || lead.CreatedBy != "bot:"+ceo {
		t.Fatalf("公司範本的執行長有團隊管理權限：%+v", lead)
	}
	sp = Templates[1].Spec("第二個工程團隊") // 6 個，累計 14
	sp.Creator = ceo
	must[TeamResult](t)(co.s.CreateTeam(co.ctx, sp))
	sp = Templates[1].Spec("第三個工程團隊")
	for i := range sp.Members {
		sp.Members[i].Name += "3"
	}
	sp.Lead.Name += "3"
	sp.Creator = ceo
	_, err := co.s.CreateTeam(co.ctx, sp) // 再 6 個＝20：剛好
	if err != nil {
		t.Fatal(err)
	}
	sp = TeamSpec{Name: "超過", Lead: Member{Name: "x1"}, Members: []Member{{Name: "x2"}}, Creator: ceo}
	_, err = co.s.CreateTeam(co.ctx, sp)
	errHas(t, err, "每天最多建立 20 個 Bot")
}

// test_only_a_bot_with_team_management_changes_others
func TestOnlyABotWithTeamManagementChangesOthers(t *testing.T) {
	co := setup(t)
	b := co.bots
	_, _, err := co.s.UpdateBot(co.ctx, b["CEO"], "QA", BotUpdate{Title: "測試主管"})
	errHas(t, err, "團隊管理權限")
	co.manage("CEO")
	_, fields := must2(t)(co.s.UpdateBot(co.ctx, b["CEO"], "QA", BotUpdate{Title: "測試主管", ReportsTo: "Dev", Team: "品質小組"}))
	if strings.Join(fields, ",") != "title,reports_to,team" {
		t.Fatal(fields)
	}
	qa := co.bot("QA")
	team := must[store.Team](t)(co.db.GetTeam(co.ctx, qa.TeamID))
	if qa.Title != "測試主管" || qa.ReportsTo != b["Dev"] || team.Name != "品質小組" {
		t.Fatalf("%+v %+v", qa, team)
	}
	_, _, err = co.s.UpdateBot(co.ctx, b["CEO"], "Dev", BotUpdate{ReportsTo: "QA"})
	errHas(t, err, "不能反過來")
	must2(t)(co.s.UpdateBot(co.ctx, b["CEO"], "QA", BotUpdate{ReportsTo: "使用者", Team: "未分區"}))
	if qa := co.bot("QA"); qa.ReportsTo != "" || qa.TeamID != "" {
		t.Fatalf("%+v", qa)
	}
	_, _, err = co.s.UpdateBot(co.ctx, b["CEO"], "QA", BotUpdate{})
	errHas(t, err, "沒有要更新")
}

func must2(t *testing.T) func(string, []string, error) (string, []string) {
	return func(a string, b []string, err error) (string, []string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return a, b
	}
}

// test_a_deleted_managers_team_moves_up（核准在 C11：刪 Bot 時它的核准一起刪掉，外鍵 CASCADE）
func TestADeletedManagersTeamMovesUp(t *testing.T) {
	co := setup(t)
	co.setBoss("QA", "Dev")
	made := must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "研發部", Lead: Member{Bot: "Dev"}, Members: []Member{{Bot: "QA"}}, ReportsTo: "CEO"}))
	if err := co.db.DeleteBot(co.ctx, co.bots["Dev"]); err != nil {
		t.Fatal(err)
	}
	if qa := co.bot("QA"); qa.ReportsTo != co.bots["CEO"] {
		t.Fatalf("QA 改向 Dev 的主管報告：%+v", qa)
	}
	if team := must[store.Team](t)(co.db.GetTeam(co.ctx, made.Team.ID)); team.ChiefID != "" {
		t.Fatalf("主管被刪掉後團隊沒有主管：%+v", team)
	}
}

func TestOwnerEditsAndDeletesTeams(t *testing.T) {
	co := setup(t)
	b := co.bots
	made := must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "研發部", Lead: Member{Bot: "Dev"}, Members: []Member{{Bot: "QA"}}}))
	id := made.Team.ID
	// 加入 Ops：改向主管 Dev 報告。
	name, about := "研發中心", "做產品"
	team := must[store.Team](t)(co.s.UpdateTeam(co.ctx, id, TeamUpdate{Name: &name, About: &about, Add: []string{b["Ops"]}, Memory: []string{" 週五發版 ", ""}}))
	if team.Name != "研發中心" || team.About != "做產品" || strings.Join(team.Memory, ",") != "週五發版" {
		t.Fatalf("%+v", team)
	}
	if ops := co.bot("Ops"); ops.TeamID != id || ops.ReportsTo != b["Dev"] {
		t.Fatalf("%+v", ops)
	}
	// 換主管：向舊主管報告的改向新主管；新主管接手舊主管的上級。
	chief := b["QA"]
	must[store.Team](t)(co.s.UpdateTeam(co.ctx, id, TeamUpdate{ChiefID: &chief}))
	if qa, ops, dev := co.bot("QA"), co.bot("Ops"), co.bot("Dev"); qa.ReportsTo != "" || ops.ReportsTo != chief || dev.ReportsTo != chief {
		t.Fatalf("QA→%q Ops→%q Dev→%q", qa.ReportsTo, ops.ReportsTo, dev.ReportsTo)
	}
	// 主管要是成員。
	ceo := b["CEO"]
	_, err := co.s.UpdateTeam(co.ctx, id, TeamUpdate{ChiefID: &ceo})
	errHas(t, err, "要是這個團隊的成員")
	// 移出成員：回到未分區，原本向主管報告的改成直接向你報告。
	must[store.Team](t)(co.s.UpdateTeam(co.ctx, id, TeamUpdate{Remove: []string{b["Ops"]}}))
	if ops := co.bot("Ops"); ops.TeamID != "" || ops.ReportsTo != "" {
		t.Fatalf("%+v", ops)
	}
	// 刪團隊：Bot 和報告線都留著。
	n := must[int](t)(co.s.DeleteTeam(co.ctx, id))
	if n != 2 {
		t.Fatal(n)
	}
	if dev := co.bot("Dev"); dev.TeamID != "" || dev.ReportsTo != chief {
		t.Fatalf("%+v", dev)
	}
	if _, err := co.s.DeleteTeam(co.ctx, id); err == nil {
		t.Fatal("刪過的團隊找不到")
	}
}

func TestSetReportsToRejectsLoops(t *testing.T) {
	co := setup(t)
	b := co.bots
	got := must[store.Bot](t)(co.s.SetReportsTo(co.ctx, b["QA"], b["Dev"], "owner"))
	if got.ReportsTo != b["Dev"] || got.UpdatedBy != "owner" {
		t.Fatalf("%+v", got)
	}
	_, err := co.s.SetReportsTo(co.ctx, b["Dev"], b["QA"], "owner")
	errHas(t, err, "不能反過來")
	got = must[store.Bot](t)(co.s.SetReportsTo(co.ctx, b["QA"], "", "owner"))
	if got.ReportsTo != "" {
		t.Fatal(got.ReportsTo)
	}
}

func TestSuggestPicksATemplateFromASentence(t *testing.T) {
	for sentence, want := range map[string]string{
		"幫我經營 IG 社群和廣告投放": "marketing",
		"做一個網站，前端後端都要":    "engineering",
		"我想開一間公司，賣自己的商品":  "company",
		"隨便": "marketing",
	} {
		got, ok := Suggest(sentence)
		if got.Key != want || ok != (sentence != "隨便") {
			t.Errorf("%s → %s（%v）", sentence, got.Key, ok)
		}
	}
	for _, tpl := range Templates {
		if n := len(tpl.Members) + 1; n > limits.TeamMax || n > 8 {
			t.Errorf("%s 有 %d 位：範本最多 8 位（F02）", tpl.Key, n)
		}
	}
}
