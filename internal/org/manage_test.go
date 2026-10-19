package org

import (
	"context"
	"slices"
	"testing"
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
