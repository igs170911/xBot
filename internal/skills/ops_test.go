package skills

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/boxfs"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type env struct {
	db  *store.DB
	reg *ops.Registry
	fs  map[string]*boxfs.Mem
	bot store.Bot
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), fs: map[string]*boxfs.Mem{}}
	ops.RegisterBuiltin(e.reg)
	e.bot, _ = db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes", MemoryEnabled: true})
	Register(e.reg, &Service{Library: &Library{Dir: t.TempDir()}, Locate: func(_ context.Context, botID string, create bool) (boxfs.FS, error) {
		if m, ok := e.fs[botID]; ok {
			return m, nil
		}
		if !create {
			return nil, boxfs.ErrNoComputer
		}
		e.fs[botID] = boxfs.NewMem()
		return e.fs[botID], nil
	}})
	return e
}

func (e *env) call(t *testing.T, id ops.Identity, op string, in, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(ctx, id, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return err
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

var owner = ops.Owner("test")

func TestBotSkillOps(t *testing.T) {
	e := setup(t)
	var list ListOut
	if err := e.call(t, owner, "xbot.skills.list", ListIn{BotID: e.bot.ID}, &list); err != nil || list.Computer || list.Note == "" {
		t.Fatalf("還沒有電腦：%+v %v", list, err)
	}
	var s LibSkill
	if err := e.call(t, owner, "xbot.skills.create", CreateIn{BotID: e.bot.ID, Name: "週報彙整", Description: "每週一彙整", Instructions: "1. 讀營收"}, &s); err != nil {
		t.Fatal(err)
	}
	if s.Place != PlaceList || s.Source != "custom" {
		t.Fatalf("%+v", s)
	}
	if err := e.call(t, owner, "xbot.skills.create", CreateIn{BotID: e.bot.ID, Name: "週報彙整", Description: "x", Instructions: "y"}, nil); code(err) != ops.CodeConflict {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.skills.enable", EnableIn{BotID: e.bot.ID, ID: s.ID, Place: PlaceLibrary}, &s); err != nil || s.Place != PlaceLibrary {
		t.Fatal(err)
	}
	// Bot 找技能庫裡的技能，再載入步驟。
	me := ops.Bot(e.bot.ID, "小秘")
	if err := e.call(t, me, "xbot.skills.list", ListIn{Place: PlaceLibrary, Query: "週報"}, &list); err != nil || len(list.Skills) != 1 || list.Skills[0].Instructions != "" {
		t.Fatalf("Bot 只看到名稱與用途：%+v %v", list, err)
	}
	if err := e.call(t, me, "xbot.skills.get", GetIn{ID: "週報彙整"}, &s); err != nil || s.Instructions != "1. 讀營收" {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.skills.disable", IDIn{BotID: e.bot.ID, ID: s.ID}, &s); err != nil || s.Enabled {
		t.Fatal(err)
	}
	if err := e.call(t, me, "xbot.skills.get", GetIn{ID: s.ID}, nil); code(err) != ops.CodeConflict {
		t.Fatal("停用的技能 Bot 不能載入")
	}
	if err := e.call(t, me, "xbot.skills.create", CreateIn{Name: "x", Description: "x", Instructions: "x"}, nil); err == nil {
		t.Fatal("Bot 不能直接新增技能（要提議）")
	}
	name := "週報"
	if err := e.call(t, owner, "xbot.skills.update", UpdateIn{BotID: e.bot.ID, ID: s.ID, Name: &name}, &s); err != nil || s.Name != "週報" || s.Place != PlaceOff {
		t.Fatalf("%+v %v", s, err)
	}
	// 代理刪除要核准；你本人直接刪。
	ag := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "Agent", Scopes: ops.Scopes, Via: "mcp"}
	if err := e.call(t, ag, "xbot.skills.delete", IDIn{BotID: e.bot.ID, ID: s.ID}, nil); code(err) != ops.CodePendingApproval {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.skills.delete", IDIn{BotID: e.bot.ID, ID: s.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.skills.get", GetIn{BotID: e.bot.ID, ID: s.ID}, nil); code(err) != ops.CodeNotFound {
		t.Fatal(err)
	}
}

func TestImportAndInstall(t *testing.T) {
	e := setup(t)
	md := "---\nname: shopee\ndescription: 比價\n---\n\n```bash\ncurl x\n```\n"
	var s LibSkill
	if err := e.call(t, owner, "xbot.skills.import", ImportIn{Filename: "SKILL.md", Content: "# no frontmatter"}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("格式不對要說明：%v", err)
	}
	if err := e.call(t, owner, "xbot.skills.import", ImportIn{Filename: "SKILL.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte(md))}, &s); err != nil {
		t.Fatal(err)
	}
	if s.Enabled || s.Review == nil || s.Review.Commands[0] != "curl x" {
		t.Fatalf("匯入預設停用並列出指令：%+v", s)
	}
	var got LibSkill
	if err := e.call(t, owner, "xbot.skills.install_to_bot", InstallIn{ID: s.ID, BotID: e.bot.ID}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Place != PlaceOff || got.Source != "imported" {
		t.Fatalf("停用的技能裝上去也是停用：%+v", got)
	}
	if err := e.call(t, owner, "xbot.skills.enable", EnableIn{ID: s.ID}, &s); err != nil || !s.Enabled {
		t.Fatal(err)
	}
	var list ListOut
	if err := e.call(t, owner, "xbot.skills.list", ListIn{}, &list); err != nil || len(list.Skills) != 1 || list.Counts["list"] != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	// 匯入到 Bot：一樣停用。
	if err := e.call(t, owner, "xbot.skills.import", ImportIn{BotID: e.bot.ID, Filename: "a.md", Content: strings.Replace(md, "shopee", "other", 1)}, &got); err != nil || got.Place != PlaceOff {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestProposeGoesToApproval(t *testing.T) {
	e := setup(t)
	me := ops.Bot(e.bot.ID, "小秘")
	var out ProposeOut
	in := ProposeIn{Name: "deploy-site", Description: "部署網站時", Instructions: "1. build\n2. push", Reason: "做了 12 步"}
	if err := e.call(t, me, "xbot.skills.propose", in, &out); err != nil || out.Status != "pending" || out.ApprovalID == "" {
		t.Fatalf("%+v %v", out, err)
	}
	var again ProposeOut
	if err := e.call(t, me, "xbot.skills.propose", in, &again); err != nil || again.ApprovalID != out.ApprovalID {
		t.Fatal("同名不重送")
	}
	if len(e.fs) != 0 {
		t.Fatal("核准前不能寫")
	}
	if err := e.db.DecideApproval(ctx, out.ApprovalID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := e.reg.ExecuteApproved(ctx, out.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(res.Output, &out)
	if out.Status != "approved" || out.Skill == nil || out.Skill.Source != "proposed" {
		t.Fatalf("%+v", out)
	}
	if err := e.call(t, owner, "xbot.skills.propose", in, nil); code(err) != ops.CodeForbidden {
		t.Fatal(err)
	}
}
