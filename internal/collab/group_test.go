package collab

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// Bot 改自己所在的群組（使用者回報 2026-10-19：「我沒有改群組名稱的工具」「沒有移除群組成員的功能」）：
// 名稱、公告、回應規則、主責、加減成員、離開；和你在網頁上改群組走同一段程式，群組裡貼一行是誰改了什麼。
func TestBotUpdatesItsGroup(t *testing.T) {
	tm := setup(t)
	reg := ops.New(ops.Config{DB: tm.db})
	Register(reg, tm.s)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "發表會"}))
	bot := ops.Bot(chief, "Chief")
	tm.running(chief, nil)

	if info, _ := reg.Get("xbot.collab.update_group"); !info.ForBot || info.Risk != ops.RiskSafe || info.DynamicRisk {
		t.Fatalf("Bot 用得到、safe：%+v", info)
	}
	if _, err := call[GroupUpdateResult](t, tm, reg, ops.Owner("ui"), "xbot.collab.update_group", map[string]any{"group": room.ID, "name": "x"}); err == nil {
		t.Fatal("你本人用 xbot.rooms.update")
	}
	if _, err := call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": "發表會"}); err == nil ||
		!strings.Contains(err.Error(), "沒有要改的") {
		t.Fatalf("沒給欄位：%v", err)
	}
	// 預演不改
	res := must[ops.Result](t)(reg.Invoke(tm.ctx, bot, "xbot.collab.update_group", []byte(`{"group":"發表會","name":"新名字"}`), ops.Options{DryRun: true}))
	var dry GroupUpdateResult
	if err := json.Unmarshal(res.Output, &dry); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dry.Changed, []string{"名稱"}) || must[store.Room](t)(tm.db.GetRoom(tm.ctx, room.ID)).Name != "發表會" {
		t.Fatalf("預演：%+v", dry)
	}

	out := must[GroupUpdateResult](t)(call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{
		"group": "發表會", "name": "發表會籌備", "bulletin": "  每天 5 點前回報  ", "rule": "lead", "lead": "Writer", "add": []string{"Scout"}}))
	if out.Group != "發表會籌備" || out.Lead != "Writer" || !slices.Equal(out.Members, []string{"Chief", "Writer", "Scout"}) {
		t.Fatalf("%+v", out)
	}
	r := must[store.Room](t)(tm.db.GetRoom(tm.ctx, room.ID))
	if r.Name != "發表會籌備" || r.AutoName || r.Bulletin != "每天 5 點前回報" || r.ResponderRule != store.RuleLead || r.LeadID != writer {
		t.Fatalf("%+v", r)
	}
	log := tm.roomLog(room.ID)
	notice := log[len(log)-1]
	if notice.Sender != "system" || roomMetaOf(notice).Notice != "updated" || !strings.HasPrefix(notice.Content, "Chief ") ||
		!strings.Contains(notice.Content, "Scout") || !strings.Contains(notice.Content, "發表會籌備") {
		t.Fatalf("群組裡要看得到誰改了什麼：%+v", notice)
	}
	if line := tm.last(tm.main[chief]); peerOf(line).Event != "group_updated" || peerOf(line).RoomID != room.ID {
		t.Fatalf("Bot 的對話裡記一行：%+v", line)
	}
	// 主責要是成員；移出不在群組的人回 not_found
	if _, err := call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": room.ID, "lead": "路人"}); err == nil {
		t.Fatal("主責要是成員")
	}
	var oe *ops.Error
	if _, err := call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": room.ID, "remove": []string{"路人"}}); !errors.As(err, &oe) || oe.Code != ops.CodeNotFound {
		t.Fatalf("%v", err)
	}

	// Scout 排隊中的群組工作：被移出時停掉，不會再貼到群組
	if err := tm.s.seat(tm.ctx, r, scout, "", true, 0, true); err != nil {
		t.Fatal(err)
	}
	if len(tm.queued(scout)) != 1 {
		t.Fatal("Scout 要有一輪在排隊")
	}
	out = must[GroupUpdateResult](t)(call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": room.ID, "remove": []string{"scout"}}))
	if !slices.Equal(out.Members, []string{"Chief", "Writer"}) || len(tm.queued(scout)) != 0 {
		t.Fatalf("移出：%+v，Scout 排隊 %d", out, len(tm.queued(scout)))
	}
	// 剩兩位：不能再移出或離開（要刪群組用 remove_group）
	if _, err := call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": room.ID, "leave": true}); err == nil ||
		!strings.Contains(err.Error(), "xbot.collab.remove_group") {
		t.Fatalf("剩兩位不能離開：%v", err)
	}
	if _, err := call[GroupUpdateResult](t, tm, reg, bot, "xbot.collab.update_group", map[string]any{"group": room.ID, "remove": []string{"Writer"}}); err == nil {
		t.Fatal("剩兩位不能再移出")
	}

	// 不在群組的 Bot 改不了；有團隊管理權限的可以（也可以把自己加進去）
	scoutBot := ops.Bot(scout, "Scout")
	if _, err := call[GroupUpdateResult](t, tm, reg, scoutBot, "xbot.collab.update_group", map[string]any{"group": room.ID, "name": "偷改"}); !errors.As(err, &oe) || oe.Code != ops.CodeNotFound {
		t.Fatalf("不在群組：%v", err)
	}
	if err := tm.db.SetBotCanManage(tm.ctx, scout, true, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := call[GroupUpdateResult](t, tm, reg, scoutBot, "xbot.collab.update_group", map[string]any{"group": room.ID, "leave": true}); err == nil {
		t.Fatal("不在群組不能離開")
	}
	out = must[GroupUpdateResult](t)(call[GroupUpdateResult](t, tm, reg, scoutBot, "xbot.collab.update_group", map[string]any{"group": "發表會籌備", "add": []string{"Scout"}}))
	if !slices.Contains(out.Members, "Scout") {
		t.Fatalf("管理者加自己：%+v", out)
	}

	// 離開：正在做的這一輪不停；之後不在成員裡
	active := tm.running(scout, map[string]any{"room_id": room.ID})
	out = must[GroupUpdateResult](t)(call[GroupUpdateResult](t, tm, reg, scoutBot, "xbot.collab.update_group", map[string]any{"group": room.ID, "leave": true}))
	if !out.Left || slices.Contains(out.Members, "Scout") {
		t.Fatalf("離開：%+v", out)
	}
	if st := tm.task(active).State; st != store.TaskRunning {
		t.Fatalf("自己正在做的這一輪不停：%s", st)
	}
}
