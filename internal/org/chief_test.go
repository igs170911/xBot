package org

import (
	"strings"
	"testing"
)

// 大總管不加入任何團隊、也不當主管（2026-10-21 使用者：「每次請他建立團隊，他就變成那個團隊的主管，這樣不對」；
// 線上紀錄：大總管建團隊時把自己填成主管）。團隊可以向它回報。
func TestTheChiefNeverJoinsATeam(t *testing.T) {
	co := setup(t)
	co.manage("CEO")
	ceo := co.bot("CEO")
	ceo.IsChief = true
	if _, err := co.db.UpdateBot(co.ctx, ceo); err != nil {
		t.Fatal(err)
	}
	refuse := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "大總管") {
			t.Fatalf("%s：要擋下並說明大總管不加入團隊，得到 %v", what, err)
		}
	}

	// 建團隊：大總管當主管或成員都不行，什麼都不留
	_, err := co.s.CreateTeam(co.ctx, TeamSpec{Name: "內容組", Lead: Member{Bot: "CEO"}, Members: []Member{{Name: "阿筆"}}, Creator: co.bots["CEO"]})
	refuse("當主管", err)
	_, err = co.s.CreateTeam(co.ctx, TeamSpec{Name: "內容組", Lead: Member{Bot: "Dev"}, Members: []Member{{Bot: "CEO"}}})
	refuse("當成員", err)
	if teams, _ := co.db.ListTeams(co.ctx); len(teams) != 0 {
		t.Fatalf("擋下時不建團隊：%+v", teams)
	}

	// 另外找主管、向大總管回報：可以，大總管還是不在團隊裡
	res := must[TeamResult](t)(co.s.CreateTeam(co.ctx, TeamSpec{Name: "內容組", Lead: Member{Name: "主編"}, Members: []Member{{Name: "阿筆"}}, ReportsTo: "CEO", Creator: co.bots["CEO"]}))
	if co.bot("CEO").TeamID != "" {
		t.Fatalf("大總管不在團隊裡：%+v", co.bot("CEO"))
	}
	if b, _ := co.db.GetBot(co.ctx, res.LeadID); b.ReportsTo != co.bots["CEO"] {
		t.Fatalf("主管向大總管回報：%+v", b)
	}

	// 改團隊加人、改 Bot 的團隊：一樣擋
	_, err = co.s.UpdateTeam(co.ctx, res.Team.ID, TeamUpdate{Add: []string{co.bots["CEO"]}, Actor: "owner"})
	refuse("加進團隊", err)
	_, _, err = co.s.UpdateBot(co.ctx, co.bots["CEO"], "CEO", BotUpdate{Team: "內容組"})
	refuse("改自己的團隊", err)
	if co.bot("CEO").TeamID != "" {
		t.Fatal("大總管還是不在團隊裡")
	}

	// 守則裡告訴它
	soul := must[string](t)(co.s.SoulText(co.ctx, co.bots["CEO"]))
	if !strings.Contains(soul, "不加入任何團隊") {
		t.Fatalf("大總管的守則要寫：%s", soul)
	}
}
