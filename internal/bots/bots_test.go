package bots

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

var ctx = context.Background()

type env struct {
	db       *store.DB
	reg      *ops.Registry
	stopped  []string
	torndown []string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db})}
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, Deps{
		Stop:     func(_ context.Context, id string) { e.stopped = append(e.stopped, id) },
		Teardown: func(_ context.Context, id string) error { e.torndown = append(e.torndown, id); return nil },
	})
	return e
}

func (e *env) call(t *testing.T, id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(ctx, id, op, raw, o)
	return res.Output, err
}

func (e *env) must(t *testing.T, id ops.Identity, op string, in any, out any) {
	t.Helper()
	raw, err := e.call(t, id, op, in)
	if err != nil {
		t.Fatalf("%s：%v", op, err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

var owner = ops.Owner("test")

func agent(id string, bots []string, scopes ...ops.Scope) ops.Identity {
	return ops.Identity{Kind: ops.KindAgent, ID: id, Name: "Agent", Scopes: scopes, Resources: ops.Resources{Bots: bots}, Via: "mcp"}
}

func TestCreateDefaultsAndValidation(t *testing.T) {
	e := setup(t)
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "  小秘 ", "title": "幕僚長"}, &v)
	if v.Name != "小秘" || v.Engine != "hermes" || v.ApprovalMode != ModeAsk || v.Color != "green" || v.Shape != "cursor" || v.Model != "" {
		t.Fatalf("預設值不對：%+v", v)
	}
	var v2 View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "阿哲", "color": "pink", "shape": "star", "model": "openai/gpt-x", "effort": "high"}, &v2)
	if v2.Color != "pink" || v2.Shape != "star" || v2.Model != "openai/gpt-x" || v2.Effort != "high" {
		t.Fatalf("指定值沒用上：%+v", v2)
	}
	b, _ := e.db.GetBot(ctx, v2.ID)
	if b.ApprovalMode != "ask" || string(b.Avatar) != `{"shape":"star"}` {
		t.Fatalf("資料庫：%+v %s", b, b.Avatar)
	}
	for _, bad := range []map[string]any{
		{"name": ""}, {"name": "   "}, {"name": strings.Repeat("字", 101)}, {"name": "x", "color": "black"},
		{"name": "x", "shape": "square"}, {"name": "x", "engine": "gpt"}, {"name": "x", "approval_mode": "yolo"}, {"name": "x", "extra": 1},
	} {
		if _, err := e.call(t, owner, "xbot.bots.create", bad); code(err) != ops.CodeInvalidInput {
			t.Errorf("%v 應該是 invalid_input：%v", bad, err)
		}
	}
}

func TestCreateDryRunAndRequestID(t *testing.T) {
	e := setup(t)
	raw, err := e.call(t, owner, "xbot.bots.create", map[string]any{"name": "預演"}, ops.Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var v View
	_ = json.Unmarshal(raw, &v)
	if v.Name != "預演" || v.ID != "" {
		t.Fatalf("預演輸出：%s", raw)
	}
	if bs, _ := e.db.ListBots(ctx, true); len(bs) != 0 {
		t.Fatal("預演不應該建立 Bot")
	}
	in := map[string]any{"name": "一次"}
	r1, err := e.call(t, owner, "xbot.bots.create", in, ops.Options{RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.call(t, owner, "xbot.bots.create", in, ops.Options{RequestID: "r1"})
	if err != nil || string(r1) != string(r2) {
		t.Fatalf("重送應該回第一次的結果：%s %s %v", r1, r2, err)
	}
	if bs, _ := e.db.ListBots(ctx, true); len(bs) != 1 {
		t.Fatalf("重送不應該再建立：%d", len(bs))
	}
}

func TestLooserApprovalModeNeedsApproval(t *testing.T) {
	e := setup(t)
	a := agent("ag1", nil, ops.ScopeManage, ops.ScopeRead)
	if _, err := e.call(t, a, "xbot.bots.create", map[string]any{"name": "全權", "approval_mode": "full"}); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理建立 full 權限的 Bot 要核准：%v", err)
	}
	var v View
	e.must(t, a, "xbot.bots.create", map[string]any{"name": "乖乖"}, &v)
	if _, err := e.call(t, a, "xbot.bots.update", map[string]any{"id": v.ID, "approval_mode": "auto"}); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理放寬權限要核准：%v", err)
	}
	e.must(t, a, "xbot.bots.update", map[string]any{"id": v.ID, "approval_mode": "ask", "title": "新職稱"}, &v)
	if v.Title != "新職稱" {
		t.Fatalf("%+v", v)
	}
	// 你本人直接生效。
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "approval_mode": "full"}, &v)
	if v.ApprovalMode != ModeFull {
		t.Fatalf("%+v", v)
	}
}

func TestUpdatePartial(t *testing.T) {
	e := setup(t)
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "小編", "description": "社群", "model": "m1"}, &v)
	_ = e.db.Tx(ctx, func(q *store.Queries) error { // 頭像有其他欄位也要保留
		b, _ := q.GetBot(ctx, v.ID)
		b.Avatar = json.RawMessage(`{"shape":"blob","img":"x.png"}`)
		_, err := q.UpdateBot(ctx, b)
		return err
	})
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "shape": "drop", "model": ""}, &v)
	if v.Name != "小編" || v.Description != "社群" || v.Shape != "drop" || v.Model != "" {
		t.Fatalf("%+v", v)
	}
	b, _ := e.db.GetBot(ctx, v.ID)
	if !strings.Contains(string(b.Avatar), `"img":"x.png"`) {
		t.Fatalf("頭像其他欄位被洗掉：%s", b.Avatar)
	}
	if _, err := e.call(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "name": ""}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("名稱不能改成空的：%v", err)
	}
	if _, err := e.call(t, owner, "xbot.bots.update", map[string]any{"id": "nope", "title": "x"}); code(err) != ops.CodeNotFound {
		t.Fatalf("不存在：%v", err)
	}
	// 預演不寫。
	if _, err := e.call(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "title": "預演"}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.db.GetBot(ctx, v.ID); b.Title == "預演" {
		t.Fatal("預演不應該寫入")
	}
}

func TestListGetArchiveAndResourceScope(t *testing.T) {
	e := setup(t)
	var a, b View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "A"}, &a)
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "B"}, &b)
	limited := agent("ag2", []string{a.ID}, ops.ScopeRead, ops.ScopeManage)

	var list ListOut
	e.must(t, limited, "xbot.bots.list", map[string]any{}, &list)
	if len(list.Bots) != 1 || list.Bots[0].ID != a.ID {
		t.Fatalf("受限代理只看得到 A：%+v", list)
	}
	if _, err := e.call(t, limited, "xbot.bots.get", map[string]any{"id": b.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("不能讀 B：%v", err)
	}
	if _, err := e.call(t, limited, "xbot.bots.archive", map[string]any{"id": b.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("不能封存 B：%v", err)
	}
	readOnly := agent("ag3", nil, ops.ScopeRead)
	if _, err := e.call(t, readOnly, "xbot.bots.create", map[string]any{"name": "C"}); code(err) != ops.CodeForbidden {
		t.Fatalf("只有 read 不能建立：%v", err)
	}

	var got View
	e.must(t, limited, "xbot.bots.archive", map[string]any{"id": a.ID}, &got)
	if !got.Archived || len(e.stopped) != 1 {
		t.Fatalf("封存：%+v %v", got, e.stopped)
	}
	e.must(t, owner, "xbot.bots.list", map[string]any{}, &list)
	if len(list.Bots) != 1 || list.Bots[0].ID != b.ID {
		t.Fatalf("封存的不列：%+v", list)
	}
	e.must(t, owner, "xbot.bots.list", map[string]any{"include_archived": true}, &list)
	if len(list.Bots) != 2 {
		t.Fatalf("%+v", list)
	}
	e.must(t, owner, "xbot.bots.archive", map[string]any{"id": a.ID, "archived": false}, &got)
	if got.Archived {
		t.Fatal("取消封存")
	}
}

func TestRestrictedAgentGetsBotsItCreates(t *testing.T) {
	e := setup(t)
	tok, err := e.db.CreateAgentToken(ctx, store.AgentToken{Name: "受限", TokenHash: "h", Scopes: []string{"read", "manage"},
		Resources: json.RawMessage(`{"bots":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ops.Agent(tok, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	var v View
	e.must(t, id, "xbot.bots.create", map[string]any{"name": "我的"}, &v)
	tok, _ = e.db.GetAgentToken(ctx, tok.ID)
	id, _ = ops.Agent(tok, "mcp")
	if !id.CanAccess(ops.ResourceBot, v.ID) {
		t.Fatalf("建立者應該能碰自己建立的 Bot：%s", tok.Resources)
	}
	e.must(t, id, "xbot.bots.get", map[string]any{"id": v.ID}, nil)

	// 不受限的代理不需要改。
	free, _ := e.db.CreateAgentToken(ctx, store.AgentToken{Name: "不限", TokenHash: "h2", Scopes: []string{"manage"}})
	fid, _ := ops.Agent(free, "mcp")
	e.must(t, fid, "xbot.bots.create", map[string]any{"name": "他的"}, nil)
	free, _ = e.db.GetAgentToken(ctx, free.ID)
	if string(free.Resources) != "{}" {
		t.Fatalf("不受限的權杖不應該被改：%s", free.Resources)
	}
}

func TestDeleteNeedsApprovalAndCannotBeDelegated(t *testing.T) {
	e := setup(t)
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "要刪的"}, &v)
	if _, err := e.db.CreateThread(ctx, store.Thread{BotID: v.ID, Title: "t"}); err != nil {
		t.Fatal(err)
	}
	info, _ := e.reg.Get("xbot.bots.delete")
	if info.Risk != ops.RiskConfirm || !info.NoDelegate {
		t.Fatalf("刪除要 confirm 且不能代為核准：%+v", info)
	}
	a := agent("ag4", nil, ops.ScopeManage)
	_, err := e.call(t, a, "xbot.bots.delete", map[string]any{"id": v.ID})
	if code(err) != ops.CodePendingApproval {
		t.Fatalf("代理刪除要核准：%v", err)
	}
	if _, err := e.db.GetBot(ctx, v.ID); err != nil {
		t.Fatal("還沒核准不能刪")
	}
	// 預演：說明會刪幾條對話串，不刪。
	raw, err := e.call(t, owner, "xbot.bots.delete", map[string]any{"id": v.ID}, ops.Options{DryRun: true})
	var out DeleteOut
	_ = json.Unmarshal(raw, &out)
	if err != nil || out.Deleted || out.Threads != 1 {
		t.Fatalf("預演：%s %v", raw, err)
	}
	// 你本人直接刪：先停、收掉電腦、再刪資料。
	e.must(t, owner, "xbot.bots.delete", map[string]any{"id": v.ID}, &out)
	if !out.Deleted || len(e.torndown) != 1 || len(e.stopped) != 1 {
		t.Fatalf("%+v %v %v", out, e.torndown, e.stopped)
	}
	if _, err := e.db.GetBot(ctx, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("應該刪掉了")
	}
}

func TestSchemaLimitsMatchLimitsPackage(t *testing.T) {
	// 名稱等長度上限寫在 struct 標籤；這裡確認和 internal/limits 一致（見 limits/ops.go）。
	checkMax(t, "xbot.bots.create", map[string]int{"name": limits.BotNameMax, "title": limits.BotTitleMax, "description": limits.BotDescriptionMax, "soul": limits.BotSoulMax})
}

func checkMax(t *testing.T, op string, want map[string]int) {
	t.Helper()
	reg := ops.New(ops.Config{})
	Register(reg, Deps{})
	info, _ := reg.Get(op)
	for f, n := range want {
		p := info.Input.Property(f)
		if p == nil || p.MaxLength == nil || *p.MaxLength != n {
			t.Errorf("%s.%s 的上限應該是 %d", op, f, n)
		}
	}
}
