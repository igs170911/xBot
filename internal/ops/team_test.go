package ops

import (
	"context"
	"testing"

	"xbot/internal/store"
)

type botRefIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot"`
}

type botRefOut struct {
	Reports []string `json:"reports"`
}

// TeamRead：有團隊管理權限的 Bot 能把唯讀操作用在向它報告的 Bot（直屬或間接）上；其他 Bot、其他操作都不放寬。
func TestTeamReadReachesOnlyReports(t *testing.T) {
	env := newEnv(t)
	reg := env.reg
	handle := func(_ context.Context, call *Call, _ botRefIn) (botRefOut, error) {
		return botRefOut{Reports: call.Identity.Reports}, nil
	}
	Register(reg, Op[botRefIn, botRefOut]{Name: "xbot.test.peek", Summary: "看", Scope: ScopeRead, Risk: RiskSafe, ForBot: true, TeamRead: true, Handle: handle})
	Register(reg, Op[botRefIn, botRefOut]{Name: "xbot.test.look", Summary: "看（沒有 TeamRead）", Scope: ScopeRead, Risk: RiskSafe, ForBot: true, Handle: handle})
	mk := func(name, boss string) store.Bot {
		b, err := env.db.CreateBot(ctx, store.Bot{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if boss != "" {
			if err := env.db.SetBotOrg(ctx, b.ID, "", boss, "owner"); err != nil {
				t.Fatal(err)
			}
		}
		return b
	}
	lead := mk("主管", "")
	dev := mk("工程師", lead.ID)
	intern := mk("實習生", dev.ID)
	other := mk("路人", "")
	me := Bot(lead.ID, lead.Name)
	call := func(id Identity, op, bot string) error {
		_, err := reg.Invoke(ctx, id, op, []byte(`{"bot_id":"`+bot+`"}`), Options{})
		return err
	}
	if err := call(me, "xbot.test.peek", dev.ID); err == nil {
		t.Fatal("沒有團隊管理權限不能看下屬")
	}
	if err := env.db.SetBotCanManage(ctx, lead.ID, true, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []store.Bot{dev, intern} {
		if err := call(me, "xbot.test.peek", b.ID); err != nil {
			t.Fatalf("主管看得到 %s：%v", b.Name, err)
		}
	}
	if err := call(me, "xbot.test.peek", other.ID); err == nil {
		t.Fatal("不是下屬的 Bot 看不到")
	}
	if err := call(me, "xbot.test.look", dev.ID); err == nil {
		t.Fatal("沒標 TeamRead 的操作不放寬")
	}
	if err := env.db.SetBotCanManage(ctx, dev.ID, true, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := call(Bot(dev.ID, dev.Name), "xbot.test.peek", lead.ID); err == nil {
		t.Fatal("下屬不能反過來看主管")
	}
	// 自己的資源不用查下屬（Reports 留 nil）
	res, err := reg.Invoke(ctx, me, "xbot.test.peek", []byte(`{"bot_id":"`+lead.ID+`"}`), Options{})
	if err != nil || string(res.Output) != `{"reports":null}` {
		t.Fatalf("%s %v", res.Output, err)
	}
}
