package org

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xbot/internal/bots"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type opsEnv struct {
	*company
	reg *ops.Registry
}

func setupOps(t *testing.T) *opsEnv {
	co := setup(t)
	reg := ops.New(ops.Config{DB: co.db})
	ops.RegisterBuiltin(reg)
	bots.Register(reg, bots.Deps{Active: co.rt.Active})
	Register(reg, co.s)
	return &opsEnv{company: co, reg: reg}
}

func (e *opsEnv) call(id ops.Identity, op string, in any, out any) error {
	e.t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(e.ctx, id, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			e.t.Fatal(err)
		}
	}
	return err
}

func codeOf(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func agentWith(scopes ...ops.Scope) ops.Identity {
	return ops.Identity{Kind: ops.KindAgent, ID: "ag", Name: "Agent", Scopes: scopes, Via: "mcp"}
}

func TestTeamOpsPermissions(t *testing.T) {
	e := setupOps(t)
	owner, ceo := ops.Owner("ui"), ops.Bot(e.bots["CEO"], "CEO")
	in := CreateTeamIn{Template: "marketing", Name: "行銷部"}
	// 沒有團隊管理權限的 Bot 不能建團隊。
	if err := e.call(ceo, "xbot.teams.create", in, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	e.manage("CEO")
	var made CreateTeamOut
	if err := e.call(ceo, "xbot.teams.create", in, &made); err != nil {
		t.Fatal(err)
	}
	if made.Team.Name != "行銷部" || len(made.Created) != 5 || made.RoomID == "" || made.Team.Template != "marketing" {
		t.Fatalf("%+v", made)
	}
	lead := must[store.Bot](t)(e.db.GetBot(e.ctx, made.LeadID))
	if lead.CreatedBy != "bot:"+e.bots["CEO"] {
		t.Fatalf("記在主管名下：%+v", lead)
	}
	// Bot 不能用你本人的團隊操作。
	var list TeamsListOut
	if err := e.call(ceo, "xbot.teams.list", struct{}{}, &list); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	if err := e.call(owner, "xbot.teams.list", struct{}{}, &list); err != nil || len(list.Teams) != 1 || len(list.Teams[0].MemberIDs) != 5 {
		t.Fatalf("%+v %v", list, err)
	}
	if list.Teams[0].MemberIDs[0] != made.LeadID {
		t.Fatal("主管排第一")
	}
	// 刪團隊：代理要你核准；你本人直接刪。
	ag := agentWith(ops.ScopeManage, ops.ScopeRead)
	if err := e.call(ag, "xbot.teams.delete", TeamIDIn{ID: made.Team.ID}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("%v", err)
	}
	// 只能碰某些團隊的代理碰不到這個團隊。
	limited := ag
	limited.Resources = ops.Resources{Teams: []string{}}
	name := "x"
	if err := e.call(limited, "xbot.teams.update", UpdateTeamIn{ID: made.Team.ID, Name: &name}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	var del DeleteTeamOut
	if err := e.call(owner, "xbot.teams.delete", TeamIDIn{ID: made.Team.ID}, &del); err != nil || !del.Deleted || del.Members != 5 {
		t.Fatalf("%+v %v", del, err)
	}
	// 「只有你能決定的事」：代理改要你核准。
	if err := e.call(ag, "xbot.teams.set_approval_items", SetApprovalItemsIn{Items: []string{}}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("%v", err)
	}
	// 報告線
	var rt ReportsToOut
	if err := e.call(owner, "xbot.teams.set_reports_to", ReportsToIn{BotID: e.bots["CEO"], ReportsTo: e.bots["Dev"]}, &rt); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("繞成一圈：%v", err)
	}
	// 主管 Bot 調整別人
	var upd UpdateBotOut
	if err := e.call(ceo, "xbot.teams.update_bot", UpdateBotIn{Bot: "QA", Title: "品管"}, &upd); err != nil || upd.Updated != "QA" {
		t.Fatalf("%+v %v", upd, err)
	}
	if err := e.call(owner, "xbot.teams.update_bot", UpdateBotIn{Bot: "QA", Title: "x"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

func TestAgendaOpsPermissions(t *testing.T) {
	e := setupOps(t)
	owner, dev := ops.Owner("ui"), ops.Bot(e.bots["Dev"], "Dev")
	if err := e.call(owner, "xbot.agenda.propose", ProposeIn{Subject: "x"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("提案是 Bot 送的：%v", err)
	}
	var sent SubmitOut
	if err := e.call(dev, "xbot.agenda.propose", ProposeIn{Subject: "改成週報", Days: 2}, &sent); err != nil || !sent.Sent {
		t.Fatalf("%+v %v", sent, err)
	}
	var disc SubmitOut
	if err := e.call(dev, "xbot.agenda.request_discussion", DiscussIn{Subject: "選路線"}, &disc); err != nil || !disc.Sent {
		t.Fatalf("%+v %v", disc, err)
	}
	// Bot 和代理都不能替你回覆。
	if err := e.call(dev, "xbot.agenda.decide", DecideIn{ID: sent.AgendaID, Decision: "accept"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	if err := e.call(agentWith(ops.ScopeOperate), "xbot.agenda.decide", DecideIn{ID: sent.AgendaID, Decision: "accept"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	var list ListOut
	if err := e.call(owner, "xbot.agenda.list", AgendaListIn{}, &list); err != nil || len(list.Pending) != 2 || list.Pending[0].BotName != "Dev" {
		t.Fatalf("%+v %v", list, err)
	}
	var item Item
	if err := e.call(owner, "xbot.agenda.decide", DecideIn{ID: sent.AgendaID, Decision: "veto", Note: "不要"}, &item); err != nil ||
		item.State != store.AgendaVetoed || item.BotName != "Dev" {
		t.Fatalf("%+v %v", item, err)
	}
	// 主管看團隊、看檢討（Bot 用自己的角度）。
	var st TeamStatusOut
	if err := e.call(ops.Bot(e.bots["CEO"], "CEO"), "xbot.teams.team_status", TeamStatusIn{}, &st); err != nil || len(st.Team) != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	var rv ReviewOut
	if err := e.call(owner, "xbot.teams.work_review", ReviewIn{}, &rv); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("你本人要指定誰：%v", err)
	}
	if err := e.call(dev, "xbot.teams.work_review", ReviewIn{}, &rv); err != nil || rv.Me.Name != "Dev" {
		t.Fatalf("%+v %v", rv, err)
	}
	var sg SuggestOut
	if err := e.call(owner, "xbot.teams.suggest", SuggestIn{Sentence: "要做 App"}, &sg); err != nil || sg.Template.Key != "engineering" || !sg.Matched {
		t.Fatalf("%+v %v", sg, err)
	}
}

// 有團隊管理權限的 Bot 也能刪 Bot、刪團隊（使用者決定 2026-10-19），但一定要使用者按允許才真的刪。
func TestManagerRemovesBotOnlyAfterOwnerApproves(t *testing.T) {
	e := setupOps(t)
	ceo := ops.Bot(e.bots["CEO"], "CEO")
	// 沒有團隊管理權限：不能發起
	if err := e.call(ceo, "xbot.teams.remove_bot", RemoveBotIn{Bot: "Dev"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("沒權限：%v", err)
	}
	e.manage("CEO")
	// 不合格的請求在送核准卡之前就擋下（不打擾使用者）
	if err := e.call(ceo, "xbot.teams.remove_bot", RemoveBotIn{Bot: "CEO"}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("不能刪自己：%v", err)
	}
	if err := e.call(ceo, "xbot.teams.remove_bot", RemoveBotIn{Bot: "不存在"}, nil); codeOf(err) != ops.CodeNotFound {
		t.Fatalf("找不到：%v", err)
	}
	if pending, _ := e.db.ListApprovals(e.ctx, "pending"); len(pending) != 0 {
		t.Fatalf("不合格的請求不能送核准卡：%d", len(pending))
	}
	if err := e.call(ceo, "xbot.teams.remove_bot", RemoveBotIn{Bot: "Dev", Reason: "重建"}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("要先核准：%v", err)
	}
	if _, err := e.db.GetBot(e.ctx, e.bots["Dev"]); err != nil {
		t.Fatal("還沒核准不能刪")
	}
	pending, _ := e.db.ListApprovals(e.ctx, "pending")
	var id string
	for _, a := range pending {
		if a.Action == "xbot.teams.remove_bot" && strings.Contains(string(a.Input), "Dev") {
			id = a.ID
			if a.BotID != e.bots["CEO"] {
				t.Fatalf("核准卡算在發起的 Bot 頭上（才會出現在它的對話裡）：%q", a.BotID)
			}
		}
	}
	if id == "" {
		t.Fatalf("找不到核准：%+v", pending)
	}
	if err := e.db.DecideApproval(e.ctx, id, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.ExecuteApproved(e.ctx, id); err != nil {
		t.Fatalf("允許後執行：%v", err)
	}
	if _, err := e.db.GetBot(e.ctx, e.bots["Dev"]); err == nil {
		t.Fatal("允許後要刪掉")
	}
}

// 同一批刪除只送一張核准卡，卡上寫出每一個要刪的名字；允許一次就全刪（使用者決定 2026-10-19）。
func TestManagerRemovesManyWithOneApproval(t *testing.T) {
	e := setupOps(t)
	ceo := ops.Bot(e.bots["CEO"], "CEO")
	e.manage("CEO")
	var made CreateTeamOut
	if err := e.call(ops.Owner("ui"), "xbot.teams.create", CreateTeamIn{Template: "marketing", Name: "行銷部"}, &made); err != nil {
		t.Fatal(err)
	}
	// 有一個找不到就整批不送
	if err := e.call(ceo, "xbot.teams.remove_many", RemoveManyIn{Bots: []string{"Dev", "不存在"}}, nil); codeOf(err) != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}
	in := RemoveManyIn{Bots: []string{"Dev", "QA", "Dev"}, Teams: []string{"行銷部"}, Reason: "重建"}
	if err := e.call(ceo, "xbot.teams.remove_many", in, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("要先核准：%v", err)
	}
	pending, _ := e.db.ListApprovals(e.ctx, "pending")
	if len(pending) != 1 {
		t.Fatalf("同一批只要一張核准卡：%d", len(pending))
	}
	cards, _ := e.db.ListInbox(e.ctx, "open")
	body := ""
	for _, c := range cards {
		if c.Kind == "approval" {
			body = c.Body
		}
	}
	for _, want := range []string{"Bot：Dev", "Bot：QA", "團隊：行銷部", "原因：重建"} {
		if !strings.Contains(body, want) {
			t.Fatalf("卡上要寫出 %q：%s", want, body)
		}
	}
	if strings.Count(body, "Bot：Dev") != 1 {
		t.Fatalf("重複的只列一次：%s", body)
	}
	if err := e.db.DecideApproval(e.ctx, pending[0].ID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.ExecuteApproved(e.ctx, pending[0].ID); err != nil {
		t.Fatalf("允許後執行：%v", err)
	}
	for _, n := range []string{"Dev", "QA"} {
		if _, err := e.db.GetBot(e.ctx, e.bots[n]); err == nil {
			t.Fatalf("%s 要刪掉", n)
		}
	}
	if teams := must[[]store.Team](t)(e.db.ListTeams(e.ctx)); len(teams) != 0 {
		t.Fatalf("團隊要刪掉：%+v", teams)
	}
}
