package routines

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// X05：一句話建定時任務先問快判，整理成規則看得懂的說法；整理得不如原句完整就照規則解析原句。
func TestProposeAsksFastJudgeFirst(t *testing.T) {
	e := setup(t)
	e.clock.Set(at(taipei, 2026, 10, 13, 10, 0))
	reply := "每天早上9點 整理科技新聞給我"
	e.svc.o.Rewrite = func(_ context.Context, text string) (string, bool) { return reply, true }
	p := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "以後早上起床前幫我把科技新聞弄好"})
	if !p.Ready || p.Draft.Prompt != "整理科技新聞給我" || p.Card.When != "每天上午 9:00" ||
		!slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "系統判斷") }) {
		t.Fatalf("快判整理後：%+v", p)
	}
	// 模型整理得比較差（沒有時間）：用規則解析原句。
	reply = "整理新聞"
	if p := get[Proposal](t, e, owner, "xbot.routines.propose", ProposeIn{Text: "每天早上 9 點整理新聞給我"}); !p.Ready || p.Card.When != "每天上午 9:00" || len(p.Notes) != 0 {
		t.Fatalf("退回規則：%+v", p)
	}
}
