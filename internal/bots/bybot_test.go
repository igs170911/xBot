package bots

// Bot 自己建幫手 Bot、改自己的資料（X03、LP-9）。移植 LazyBoy tests/test_collab.py
// test_helper_bots_are_rate_limited_and_announced、test_a_bot_updates_its_own_profile_within_bounds（X02 留給 X03 的兩個）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type botEnv struct {
	*env
	chief  store.Bot
	thread string
}

func setupBots(t *testing.T) *botEnv {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &botEnv{env: &env{db: db, reg: ops.New(ops.Config{DB: db})}}
	ops.RegisterBuiltin(e.reg)
	e.chief, err = db.CreateBot(ctx, store.Bot{Name: "Chief", Title: "總管", TeamID: ""})
	if err != nil {
		t.Fatal(err)
	}
	th, err := db.CreateThread(ctx, store.Thread{BotID: e.chief.ID, Title: "主要"})
	if err != nil {
		t.Fatal(err)
	}
	e.thread = th.ID
	Register(e.reg, Deps{Active: func(id string) (string, string) {
		if id == e.chief.ID {
			return e.thread, ""
		}
		return "", ""
	}})
	return e
}

func (e *botEnv) lastPeer(t *testing.T) (string, map[string]any) {
	t.Helper()
	msgs, err := e.db.ListMessages(ctx, store.ScopeThread, e.thread, 0, 1)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("對話是空的：%v", err)
	}
	var meta struct {
		Peer map[string]any `json:"peer"`
	}
	_ = json.Unmarshal(msgs[0].Meta, &meta)
	return msgs[0].Content, meta.Peer
}

func TestHelperBotsAreRateLimitedAndAnnounced(t *testing.T) {
	e := setupBots(t)
	chief := ops.Bot(e.chief.ID, "Chief")
	var made View
	e.must(t, chief, "xbot.bots.create", CreateIn{Name: "Designer", Title: "設計", Soul: "負責視覺"}, &made)
	helper, err := e.db.GetBot(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if helper.CreatedBy != "bot:"+e.chief.ID || helper.Color == "" || helper.Title != "設計" || helper.Soul != "負責視覺" || helper.ApprovalMode != "ask" {
		t.Fatalf("幫手 Bot：%+v", helper)
	}
	if text, peer := e.lastPeer(t); peer["event"] != "bot_created" || text != "Designer" || peer["peer_id"] != made.ID {
		t.Fatalf("對話裡要記一行：%q %v", text, peer)
	}
	if _, err := e.call(t, chief, "xbot.bots.create", CreateIn{Name: "Designer"}); err == nil || !strings.Contains(err.Error(), "已經有叫") {
		t.Fatalf("同名：%v", err)
	}
	e.must(t, chief, "xbot.bots.create", CreateIn{Name: "Editor"}, nil)
	e.must(t, chief, "xbot.bots.create", CreateIn{Name: "Tester"}, nil)
	_, err = e.call(t, chief, "xbot.bots.create", CreateIn{Name: "Fourth"})
	if code(err) != ops.CodeLimit || !strings.Contains(err.Error(), "今天已經建立") {
		t.Fatalf("一般 Bot 每天 3 個：%v", err)
	}
	// 有團隊管理權限的可以建 20 個。
	if err := e.db.SetBotCanManage(ctx, e.chief.ID, true, "owner"); err != nil {
		t.Fatal(err)
	}
	e.must(t, chief, "xbot.bots.create", CreateIn{Name: "Fourth"}, nil)
	// 一般 Bot 可以建、但不能自己放寬新 Bot 的權限：那要你核准。
	_, err = e.call(t, chief, "xbot.bots.create", CreateIn{Name: "Fifth", ApprovalMode: ModeFull})
	if code(err) != ops.CodePendingApproval {
		t.Fatalf("放寬權限要核准：%v", err)
	}
}

func TestBotCreationRespectsTheRosterCap(t *testing.T) {
	e := setupBots(t)
	for i := 1; i < limits.BotsMax; i++ {
		if _, err := e.db.CreateBot(ctx, store.Bot{Name: "b" + string(rune('A'+i%26)) + strings.Repeat("x", i/26)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.call(t, ops.Bot(e.chief.ID, "Chief"), "xbot.bots.create", CreateIn{Name: "One more"})
	if code(err) != ops.CodeLimit || !strings.Contains(err.Error(), "最多 50") {
		t.Fatalf("全部最多 50 個：%v", err)
	}
	if _, err := e.call(t, ops.Owner("ui"), "xbot.bots.create", CreateIn{Name: "Mine"}); code(err) != ops.CodeLimit {
		t.Fatalf("你本人也受全部 50 個的上限：%v", err)
	}
}

func TestABotUpdatesItsOwnProfileWithinBounds(t *testing.T) {
	e := setupBots(t)
	chief := ops.Bot(e.chief.ID, "Chief")
	var out ProfileOut
	e.must(t, chief, "xbot.bots.update_my_profile", ProfileIn{Title: "總指揮", Shape: "star"}, &out)
	if strings.Join(out.Updated, ",") != "title,shape" {
		t.Fatalf("%+v", out)
	}
	b, _ := e.db.GetBot(ctx, e.chief.ID)
	if v := ViewOf(b); v.Title != "總指揮" || v.Shape != "star" || b.UpdatedBy != "bot:"+e.chief.ID {
		t.Fatalf("%+v", v)
	}
	if text, peer := e.lastPeer(t); peer["event"] != "profile_updated" || !strings.Contains(text, "職稱") {
		t.Fatalf("%q %v", text, peer)
	}
	if _, err := e.call(t, chief, "xbot.bots.update_my_profile", map[string]string{"color": "#ff0000"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("顏色要是可用的顏色：%v", err)
	}
	if _, err := e.call(t, chief, "xbot.bots.update_my_profile", ProfileIn{}); code(err) != ops.CodeInvalidInput || !strings.Contains(err.Error(), "沒有要更新") {
		t.Fatalf("%v", err)
	}
	// 只能改自己：你本人用 xbot.bots.update。
	if _, err := e.call(t, ops.Owner("ui"), "xbot.bots.update_my_profile", ProfileIn{Title: "x"}); code(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
	// 職稱截到 60 字。
	e.must(t, chief, "xbot.bots.update_my_profile", ProfileIn{Title: strings.Repeat("長", 80)}, nil)
	b, _ = e.db.GetBot(ctx, e.chief.ID)
	if n := len([]rune(b.Title)); n != limits.BotProfileTitleMax {
		t.Fatalf("職稱 %d 字", n)
	}
}
