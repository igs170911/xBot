package botshare

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/bots"
	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/skills"
	"xbot/internal/store"
)

var ctx = context.Background()

var owner = ops.Owner("test")

type env struct {
	db  *store.DB
	reg *ops.Registry
	fs  map[string]*boxfs.Mem
	lib *skills.Library
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), fs: map[string]*boxfs.Mem{}, lib: &skills.Library{Dir: t.TempDir()}}
	svc := &skills.Service{Library: e.lib, Locate: func(_ context.Context, botID string, create bool) (boxfs.FS, error) {
		if m, ok := e.fs[botID]; ok {
			return m, nil
		}
		if !create {
			return nil, boxfs.ErrNoComputer
		}
		e.fs[botID] = boxfs.NewMem()
		return e.fs[botID], nil
	}}
	ops.RegisterBuiltin(e.reg)
	bots.Register(e.reg, bots.Deps{})
	skills.Register(e.reg, svc)
	Register(e.reg, Deps{Skills: svc})
	return e
}

func (e *env) call(t *testing.T, op string, in, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(ctx, owner, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return err
}

func (e *env) must(t *testing.T, op string, in, out any) {
	t.Helper()
	if err := e.call(t, op, in, out); err != nil {
		t.Fatalf("%s：%v", op, err)
	}
}

const png = "data:image/png;base64,iVBORw0KGgo="

func TestExportImportRoundTrip(t *testing.T) {
	e := setup(t)
	var src bots.View
	e.must(t, "xbot.bots.create", map[string]any{"name": "小秘", "title": "幕僚長", "description": "每天整理重點", "soul": "說話簡短", "color": "pink", "shape": "star", "eyes": "happy"}, &src)
	e.must(t, "xbot.bots.update", map[string]any{"id": src.ID, "image": png, "approval_mode": "ask"}, nil)
	// 放寬權限要核准：直接寫資料庫模擬已經放寬的 Bot，確認匯入時不會帶過去
	b, _ := e.db.GetBot(ctx, src.ID)
	b.ApprovalMode, b.Engine = "full", "claude-code"
	if _, err := e.db.UpdateBot(ctx, b); err != nil {
		t.Fatal(err)
	}
	e.must(t, "xbot.skills.create", map[string]any{"bot_id": src.ID, "name": "週報彙整", "description": "每週五整理", "instructions": "1. 讀\n2. 寫"}, nil)

	var ex ExportOut
	e.must(t, "xbot.bots.export", map[string]any{"id": src.ID}, &ex)
	if ex.Filename != "小秘.xbot" || ex.Skills != 1 || ex.Bytes != len(ex.Content) {
		t.Fatalf("匯出：%+v", ex)
	}
	for _, secret := range []string{"api_key", "token", "password", "claude-code"} {
		if strings.Contains(ex.Content, secret) {
			t.Fatalf("檔案不該有 %q：%s", secret, ex.Content)
		}
	}
	f, err := Parse(ex.Content)
	if err != nil || f.Bot.Image != png || f.Bot.Eyes != "happy" || f.Settings.ApprovalMode != "full" {
		t.Fatalf("解析：%+v %v", f, err)
	}

	// 預覽：不建立；同名自動改名
	var pv ImportOut
	e.must(t, "xbot.bots.import", map[string]any{"content": ex.Content, "preview": true}, &pv)
	if !pv.Preview || pv.Bot.ID != "" || pv.Bot.Name != "小秘 2" || !pv.Renamed || !pv.CanCreate || pv.Remaining != limits.BotsMax-1 {
		t.Fatalf("預覽：%+v", pv)
	}
	if n, _ := e.db.CountBots(ctx); n != 1 {
		t.Fatalf("預覽不該建立 Bot：%d", n)
	}

	var got ImportOut
	e.must(t, "xbot.bots.import", map[string]any{"content": ex.Content, "filename": "小秘.xbot"}, &got)
	v := got.Bot
	if v.ID == "" || v.Name != "小秘 2" || v.ApprovalMode != bots.ModeAsk || v.Engine != "hermes" || v.Soul != "說話簡短" || v.Shape != "star" || v.Eyes != "happy" || v.Image != png || v.Color != "pink" {
		t.Fatalf("匯入的 Bot：%+v", v)
	}
	if len(got.SkillsAdded) != 1 {
		t.Fatalf("技能要放進技能庫：%+v", got)
	}
	lib, _ := e.lib.List()
	if len(lib) != 1 || lib[0].Enabled || lib[0].Source != "imported" || lib[0].Origin != "小秘.xbot" {
		t.Fatalf("匯入的技能要先停用：%+v", lib)
	}

	// 再匯入一次：技能重複就合併，名字變「小秘 3」
	e.must(t, "xbot.bots.import", map[string]any{"content": ex.Content, "name": "小秘"}, &got)
	if got.Bot.Name != "小秘 3" || len(got.SkillsMerged) != 1 || len(got.SkillsAdded) != 0 {
		t.Fatalf("第二次匯入：%+v", got)
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	e := setup(t)
	cases := map[string]string{
		"not json":     "hello",
		"wrong format": `{"format":"other","version":1,"bot":{"name":"x"}}`,
		"too new":      `{"format":"xbot.bot","version":99,"bot":{"name":"x"}}`,
		"no name":      `{"format":"xbot.bot","version":1,"bot":{"name":" "}}`,
	}
	for name, content := range cases {
		err := e.call(t, "xbot.bots.import", map[string]any{"content": content}, nil)
		var oe *ops.Error
		if !errors.As(err, &oe) || oe.Code != ops.CodeInvalidInput {
			t.Errorf("%s：要拒絕，得到 %v", name, err)
		}
	}
	// 壞掉的頭像、未知的形狀：用預設值，不擋匯入
	var got ImportOut
	e.must(t, "xbot.bots.import", map[string]any{"content": `{"format":"xbot.bot","version":1,"bot":{"name":"阿哲","shape":"triangle","color":"#fff","image":"javascript:alert(1)"},"settings":{"approval_mode":"full"}}`}, &got)
	if got.Bot.Shape != "cursor" || got.Bot.Image != "" || got.Bot.ApprovalMode != "ask" || got.Bot.Color == "#fff" {
		t.Fatalf("要清掉不對的值：%+v", got.Bot)
	}
}

func TestImportQuota(t *testing.T) {
	e := setup(t)
	for i := 0; i < limits.BotsMax; i++ {
		if _, err := e.db.CreateBot(ctx, store.Bot{Name: "b" + string(rune('A'+i%26)) + string(rune('a'+i/26)), ApprovalMode: "ask", Engine: "hermes"}); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"format":"xbot.bot","version":1,"bot":{"name":"多一個"}}`
	var pv ImportOut
	e.must(t, "xbot.bots.import", map[string]any{"content": content, "preview": true}, &pv)
	if pv.CanCreate || pv.Remaining != 0 || pv.Reason == "" {
		t.Fatalf("到上限時預覽要說明：%+v", pv)
	}
	err := e.call(t, "xbot.bots.import", map[string]any{"content": content}, nil)
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeLimit {
		t.Fatalf("到上限不能匯入：%v", err)
	}
}

func TestExportWithoutComputerAndAuditHidesContent(t *testing.T) {
	e := setup(t)
	var src bots.View
	e.must(t, "xbot.bots.create", map[string]any{"name": "阿哲"}, &src)
	var ex ExportOut
	e.must(t, "xbot.bots.export", map[string]any{"id": src.ID, "no_soul": true}, &ex)
	if ex.Skills != 0 || !strings.Contains(ex.Note, "還沒有電腦") {
		t.Fatalf("沒有電腦時：%+v", ex)
	}
	e.must(t, "xbot.bots.update", map[string]any{"id": src.ID, "image": png}, nil)
	e.must(t, "xbot.bots.import", map[string]any{"content": ex.Content}, nil)
	entries, err := e.db.ListAudit(ctx, store.AuditFilter{})
	if err != nil || len(entries) < 2 {
		t.Fatalf("要有稽核紀錄：%d %v", len(entries), err)
	}
	for _, a := range entries {
		if strings.Contains(string(a.Params), "xbot.bot") || strings.Contains(string(a.Params), "iVBOR") {
			t.Fatalf("稽核不該記整份檔案或圖片：%s %s", a.Op, a.Params)
		}
	}
}
