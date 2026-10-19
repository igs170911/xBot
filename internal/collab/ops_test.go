package collab

// 從 LazyBoy tests/test_continuation_audit.py test_http_group_chats_and_bot_messages 翻過來：經操作（HTTP、MCP 共用）
// 開群組、發言、讀成員狀態、改名、Bot 用自己的身分傳話、讀兩個 Bot 的往來、刪群組。

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

func call[T any](t *testing.T, tm *team, reg *ops.Registry, id ops.Identity, name string, in any) (T, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	var out T
	res, err := reg.Invoke(tm.ctx, id, name, raw, ops.Options{})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestOpsGroupChatsAndBotMessages(t *testing.T) {
	tm := setup(t)
	reg := ops.New(ops.Config{DB: tm.db})
	Register(reg, tm.s)
	owner := ops.Owner("ui")
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	if _, err := call[RoomView](t, tm, reg, owner, "xbot.rooms.create", map[string]any{"member_ids": []string{chief}}); err == nil {
		t.Fatal("一個 Bot 不能開群組")
	}
	room := must[RoomView](t)(call[RoomView](t, tm, reg, owner, "xbot.rooms.create", map[string]any{"member_ids": []string{chief, writer}}))
	if room.Name != "Chief、Writer" || len(room.Members) != 2 || room.ResponderRule != store.RuleAuto {
		t.Fatalf("%+v", room)
	}
	posted := must[PostResult](t)(call[PostResult](t, tm, reg, owner, "xbot.rooms.post", map[string]any{"room_id": room.ID, "text": "@Writer 寫一段介紹"}))
	if strings.Join(posted.Woke, ",") != "Writer" {
		t.Fatalf("%+v", posted)
	}
	read := must[RoomReadOut](t)(call[RoomReadOut](t, tm, reg, owner, "xbot.collab.room_read", map[string]any{"room": room.ID}))
	if read.Messages[0].Content != "@Writer 寫一段介紹" {
		t.Fatalf("%+v", read.Messages)
	}
	seats := map[string]string{}
	for _, m := range read.Room.Members {
		seats[m.Name] = m.Seat
	}
	if seats["Chief"] != "idle" || seats["Writer"] != "queued" {
		t.Fatalf("座位：%v", seats)
	}
	list := must[RoomListOut](t)(call[RoomListOut](t, tm, reg, owner, "xbot.rooms.list", struct{}{}))
	if len(list.Rooms) != 1 || !strings.HasPrefix(list.Rooms[0].Preview, "你：") {
		t.Fatalf("%+v", list)
	}
	// 座位不是 Bot 自己的對話串。
	if ths := must[[]store.Thread](t)(tm.db.ListThreads(tm.ctx, writer, false)); len(ths) != 1 {
		t.Fatalf("Writer 的對話串：%d", len(ths))
	}
	name := "發表會小組"
	renamed := must[RoomView](t)(call[RoomView](t, tm, reg, owner, "xbot.rooms.update", map[string]any{"room_id": room.ID, "name": name}))
	if renamed.Name != name || renamed.AutoName {
		t.Fatalf("%+v", renamed)
	}
	// Bot 自己的工具：用自己的身分傳話，不能冒用別人；group 只能讀自己在的。
	bot := ops.Bot(chief, "Chief")
	tm.running(chief, nil)
	sent := must[SendResult](t)(call[SendResult](t, tm, reg, bot, "xbot.collab.message_bot", map[string]any{"bot": "Writer", "text": "幫忙", "intent": "question"}))
	if !sent.Sent || sent.To != "Writer" {
		t.Fatalf("%+v", sent)
	}
	if _, err := call[SendResult](t, tm, reg, bot, "xbot.collab.message_bot", map[string]any{"bot": "Writer", "text": "x", "from_bot": writer}); err == nil {
		t.Fatal("Bot 不能用別人的名義")
	}
	if _, err := call[SendResult](t, tm, reg, owner, "xbot.collab.message_bot", map[string]any{"bot": "Writer", "text": "x"}); err == nil {
		t.Fatal("你本人要指定 from_bot")
	}
	pair := must[PeerHistoryOut](t)(call[PeerHistoryOut](t, tm, reg, owner, "xbot.collab.peer_history", map[string]any{"from_bot": chief, "bot": writer}))
	if len(pair.Messages) != 1 || pair.Messages[0].Text != "幫忙" || pair.HopsUsed != 1 || pair.MaxHops != 6 || !pair.Open {
		t.Fatalf("%+v", pair)
	}
	if _, err := call[RoomReadOut](t, tm, reg, ops.Bot(tm.bots["Scout"], "Scout"), "xbot.collab.room_read", map[string]any{"room": room.ID}); err == nil {
		t.Fatal("不在群組的 Bot 不能讀")
	}
	if r := must[RoomReadOut](t)(call[RoomReadOut](t, tm, reg, bot, "xbot.collab.room_read", map[string]any{"room": name})); r.Room.ID != room.ID {
		t.Fatal("Bot 可以用名稱找自己的群組")
	}
	stopped := must[StopChainOut](t)(call[StopChainOut](t, tm, reg, owner, "xbot.collab.stop_chain", map[string]any{"chain_id": pair.ChainID}))
	if stopped.Stopped != 1 {
		t.Fatalf("%+v", stopped)
	}
	if out := must[RoomDeleteOut](t)(call[RoomDeleteOut](t, tm, reg, owner, "xbot.rooms.delete", map[string]any{"room_id": room.ID})); !out.Deleted {
		t.Fatal("刪除")
	}
	if list := must[RoomListOut](t)(call[RoomListOut](t, tm, reg, owner, "xbot.rooms.list", struct{}{})); len(list.Rooms) != 0 {
		t.Fatalf("%+v", list)
	}
}

// Bot 也能請使用者刪它所在的群組（使用者決定 2026-10-19），但一定要使用者按允許才真的刪。
func TestBotRemovesGroupOnlyAfterOwnerApproves(t *testing.T) {
	tm := setup(t)
	reg := ops.New(ops.Config{DB: tm.db})
	Register(reg, tm.s)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[RoomView](t)(call[RoomView](t, tm, reg, ops.Owner("ui"), "xbot.rooms.create", map[string]any{"member_ids": []string{chief, writer}, "name": "發表會"}))
	scout := ops.Bot(tm.bots["Scout"], "Scout")
	// 不在群組的 Bot：在送核准卡之前就擋下
	if _, err := call[RemoveGroupOut](t, tm, reg, scout, "xbot.collab.remove_group", map[string]any{"room": "發表會"}); err == nil {
		t.Fatal("不在群組的 Bot 不能刪")
	}
	if pending, _ := tm.db.ListApprovals(tm.ctx, "pending"); len(pending) != 0 {
		t.Fatalf("不合格的請求不能送核准卡：%d", len(pending))
	}
	bot := ops.Bot(chief, "Chief")
	var oe *ops.Error
	if _, err := call[RemoveGroupOut](t, tm, reg, bot, "xbot.collab.remove_group", map[string]any{"room": "發表會", "reason": "做完了"}); !errors.As(err, &oe) || oe.Code != ops.CodePendingApproval {
		t.Fatalf("要先核准：%v", err)
	}
	if _, err := tm.db.GetRoom(tm.ctx, room.ID); err != nil {
		t.Fatal("還沒核准不能刪")
	}
	pending, _ := tm.db.ListApprovals(tm.ctx, "pending")
	if len(pending) != 1 || pending[0].Action != "xbot.collab.remove_group" || pending[0].BotID != chief {
		t.Fatalf("%+v", pending)
	}
	if err := tm.db.DecideApproval(tm.ctx, pending[0].ID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ExecuteApproved(tm.ctx, pending[0].ID); err != nil {
		t.Fatalf("允許後執行：%v", err)
	}
	if _, err := tm.db.GetRoom(tm.ctx, room.ID); err == nil {
		t.Fatal("允許後要刪掉")
	}
}

// 刪 Bot 時，剩不到 2 位成員的群組一起刪；還有 2 位以上的留著（2026-10-19：Bot 都刪了，同名群組還留著）。
func TestPruneRoomsWhenBotGoes(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]
	pair := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{Name: "兩人"}))
	trio := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{Name: "三人"}))
	gone := must[[]string](t)(tm.s.PruneRooms(tm.ctx, writer))
	if len(gone) != 1 || gone[0] != "兩人" {
		t.Fatalf("%v", gone)
	}
	if _, err := tm.db.GetRoom(tm.ctx, pair.ID); err == nil {
		t.Fatal("剩一位的群組要刪")
	}
	if _, err := tm.db.GetRoom(tm.ctx, trio.ID); err != nil {
		t.Fatal("還有兩位的群組要留著")
	}
	// 服務啟動時的掃描：都夠人數就不動
	if gone := must[[]string](t)(tm.s.PruneRooms(tm.ctx, "")); len(gone) != 0 {
		t.Fatalf("%v", gone)
	}
}
