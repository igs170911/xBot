package files

import (
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// 有團隊管理權限的 Bot 能看向它報告的 Bot（直屬或間接）電腦裡的檔案，只能看、不能改
// （使用者回報 2026-10-19：主管列下屬的檔案被拒絕）。其他 Bot、下屬看主管，都照舊不行。
func TestManagerReadsItsReportsFiles(t *testing.T) {
	e := setup(t)
	intern, _ := e.db.CreateBot(ctx, store.Bot{Name: "小新", Engine: "hermes"})
	stranger, _ := e.db.CreateBot(ctx, store.Bot{Name: "路人", Engine: "hermes"})
	for _, x := range [][2]string{{e.mate.ID, e.bot.ID}, {intern.ID, e.mate.ID}} {
		if err := e.db.SetBotOrg(ctx, x[0], "", x[1], "owner"); err != nil {
			t.Fatal(err)
		}
	}
	write(t, e.ws(e.bot.ID), "boss.md", "主管的")
	write(t, e.ws(e.mate.ID), "週報.md", "週報")
	write(t, e.ws(intern.ID), "草稿.md", "草稿")
	write(t, e.ws(stranger.ID), "x.md", "x")
	boss := ops.Bot(e.bot.ID, e.bot.Name)

	if err := e.call(t, boss, "xbot.files.read", ReadIn{BotID: e.mate.ID, Path: "週報.md"}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("沒有團隊管理權限：%v", err)
	}
	if err := e.db.SetBotCanManage(ctx, e.bot.ID, true, "owner"); err != nil {
		t.Fatal(err)
	}
	var r ReadOut
	if err := e.call(t, boss, "xbot.files.read", ReadIn{BotID: e.mate.ID, Path: "週報.md"}, &r); err != nil || r.Text != "週報" {
		t.Fatalf("直屬：%+v %v", r, err)
	}
	var l ListOut
	if err := e.call(t, boss, "xbot.files.list", ListIn{BotID: intern.ID}, &l); err != nil || l.BotID != intern.ID || len(l.Entries) != 1 {
		t.Fatalf("間接：%+v %v", l, err)
	}
	if err := e.call(t, boss, "xbot.files.read", ReadIn{BotID: stranger.ID, Path: "x.md"}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("不是下屬：%v", err)
	}
	// 下屬（就算也有權限）不能反過來看主管
	if err := e.db.SetBotCanManage(ctx, e.mate.ID, true, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := e.call(t, ops.Bot(e.mate.ID, e.mate.Name), "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: "boss.md"}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("下屬看主管：%v", err)
	}
	// 只能看：改檔案的操作 Bot 一樣用不到
	for _, op := range []string{"xbot.files.write", "xbot.files.mkdir", "xbot.files.move", "xbot.files.delete", "xbot.files.download"} {
		if err := e.call(t, boss, op, map[string]any{"bot_id": e.mate.ID, "path": "週報.md", "from": "週報.md", "to": "y.md", "content": "x"}, nil); code(err) != ops.CodeForbidden {
			t.Errorf("主管不能用 %s：%v", op, err)
		}
	}
}
