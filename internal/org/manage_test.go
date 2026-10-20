package org

import (
	"context"
	"slices"
	"strings"
	"testing"

	"xbot/internal/ops"
)

// 建團隊順便打開主管的團隊管理權限時要通知（它看得到的 XBot MCP 工具變了，要請引擎重新載入）。
func TestCreateTeamLeadManageNotifies(t *testing.T) {
	co := setup(t)
	var changed []string
	co.s.o.ManageChanged = func(_ context.Context, id string) { changed = append(changed, id) }
	must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "品質組", Lead: Member{Bot: "QA"}, Members: []Member{{Name: "新人"}}}))
	if len(changed) != 0 {
		t.Fatalf("沒開權限不用通知：%v", changed)
	}
	must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "研發部", Lead: Member{Bot: "Dev"}, Members: []Member{{Name: "前端"}}, LeadManage: true}))
	if !slices.Equal(changed, []string{co.bots["Dev"]}) || !co.bot("Dev").CanManage {
		t.Fatalf("主管拿到權限要通知：%v %+v", changed, co.bot("Dev"))
	}
}

// 團隊管理權限只有使用者能給：Bot 建團隊時帶 lead_manage，主管不會直接拿到權限（審查 2026-10-21：
// 原本有管理權限的 Bot 能這樣不經核准讓另一個 Bot 也有管理權限）。使用者自己建時照給。
func TestBotCannotGrantManageThroughCreateTeam(t *testing.T) {
	e := setupOps(t)
	e.manage("CEO")
	ceo, owner := ops.Bot(e.bots["CEO"], "CEO"), ops.Owner("ui")
	yes := true
	lead := Member{Bot: "Dev"}
	var made CreateTeamOut
	if err := e.call(ceo, "xbot.teams.create", CreateTeamIn{Name: "研發部", Lead: &lead, Members: []Member{{Bot: "QA"}}, LeadManage: &yes}, &made); err != nil {
		t.Fatal(err)
	}
	if e.bot("Dev").CanManage || !strings.Contains(made.Note, "xbot.teams.update_bot") {
		t.Fatalf("Bot 不能直接給：%+v %q", e.bot("Dev"), made.Note)
	}
	lead = Member{Bot: "QA"}
	if err := e.call(owner, "xbot.teams.create", CreateTeamIn{Name: "品質部", Lead: &lead, Members: []Member{{Name: "新人"}}, LeadManage: &yes}, &made); err != nil {
		t.Fatal(err)
	}
	if !e.bot("QA").CanManage {
		t.Fatal("使用者自己建的照給")
	}
}
