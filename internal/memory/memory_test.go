package memory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"xbot/internal/boxfs"
	"xbot/internal/ops"
	"xbot/internal/store"
)

var ctx = context.Background()

func write(t *testing.T, m *boxfs.Mem, target Target, items, previous []string) {
	t.Helper()
	if _, _, err := Write(ctx, m, target, items, previous); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, m *boxfs.Mem) State {
	t.Helper()
	s, err := Read(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// LazyBoy tests/test_cleanup.py test_memory_lists_round_trip_within_hermes_limits
func TestMemoryListsRoundTripWithinHermesLimits(t *testing.T) {
	m := boxfs.NewMem()
	m.Files[Dir+"/MEMORY.md"] = []byte("first fact\n§\nsecond fact")
	s := read(t, m)
	if !slices.Equal(s.Memory.Entries, []string{"first fact", "second fact"}) || s.Memory.Limit != 2200 {
		t.Fatalf("%+v", s.Memory)
	}
	if len(s.User.Entries) != 0 || s.User.Limit != 1375 {
		t.Fatalf("%+v", s.User)
	}
	write(t, m, TargetMemory, []string{"second fact", "  ", "has § inside"}, nil)
	if got := read(t, m).Memory.Entries; !slices.Equal(got, []string{"second fact", "has   inside"}) {
		t.Fatalf("%q", got)
	}
	if string(m.Files[Dir+"/MEMORY.md"]) != "second fact\n§\nhas   inside" {
		t.Fatalf("%q", m.Files[Dir+"/MEMORY.md"])
	}
	_, _, err := Write(ctx, m, TargetUser, []string{strings.Repeat("x", 1000), strings.Repeat("y", 500)}, nil)
	var inv *InvalidError
	if !errors.As(err, &inv) || !strings.Contains(inv.Msg, "上限") {
		t.Fatal(err)
	}
	m.Files["/hermes/config.yaml"] = []byte("memory:\n  user_char_limit: 5000\n")
	write(t, m, TargetUser, []string{strings.Repeat("x", 1000), strings.Repeat("y", 500)}, nil)
	if u := read(t, m).User; u.Used != 1503 || u.Limit != 5000 {
		t.Fatalf("%+v", u)
	}
	// 字數用字（不是位元組）算，同 Python len()。
	write(t, m, TargetUser, []string{"喜歡簡短回答"}, nil)
	if u := read(t, m).User; u.Used != 6 {
		t.Fatalf("%d", u.Used)
	}
	if _, _, err := Write(ctx, m, TargetUser, []string{strings.Repeat("長", 2001)}, nil); err == nil {
		t.Fatal("一條最多 2000 字")
	}
}

// LazyBoy tests/test_search_reactions.py test_memory_edit_never_erases_what_the_bot_wrote_meanwhile
func TestMemoryEditNeverErasesWhatTheBotWroteMeanwhile(t *testing.T) {
	m := boxfs.NewMem()
	write(t, m, TargetUser, []string{"喜歡簡短回答"}, nil)
	loaded := read(t, m).User.Entries
	write(t, m, TargetUser, append(slices.Clone(loaded), "住在台北"), loaded) // Bot 學到新的事
	if _, _, err := Write(ctx, m, TargetUser, []string{"喜歡詳細回答"}, loaded); !errors.Is(err, ErrChanged) {
		t.Fatalf("網頁還拿著舊的清單：%v", err)
	}
	if got := read(t, m).User.Entries; !slices.Equal(got, []string{"喜歡簡短回答", "住在台北"}) {
		t.Fatal(got)
	}
	// 已經超過上限的清單仍然可以縮小。
	m.Files["/hermes/config.yaml"] = []byte("memory:\n  user_char_limit: 10\n")
	over := read(t, m).User.Entries
	write(t, m, TargetUser, over[:1], over)
	if got := read(t, m).User.Entries; !slices.Equal(got, []string{"喜歡簡短回答"}) {
		t.Fatal(got)
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"API 金鑰 sk-abcdefghijklmnop1234 放這":         "API 金鑰 *** 放這",
		"密碼：hunter2，記得換":                            "密碼：***，記得換",
		"api_key=abc123 用在測試":                       "api_key=*** 用在測試",
		"GitHub ghp_abcdefghijklmnopqrstuvwxyz0123": "GitHub ***",
		"喜歡簡短回答":                                    "喜歡簡短回答",
	} {
		if got := Redact(in); got != want {
			t.Errorf("%q → %q，想要 %q", in, got, want)
		}
	}
}

// ── 服務與操作 ──────────────────────────────────────

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
	e.bot, err = db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes", MemoryEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	Register(e.reg, &Service{DB: db, Locate: func(_ context.Context, botID string, create bool) (boxfs.FS, error) {
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

func TestWriteKeepsVersionsAndUndo(t *testing.T) {
	e := setup(t)
	var out ReadOut
	if err := e.call(t, owner, "xbot.memory.read", BotIn{BotID: e.bot.ID}, &out); err != nil || out.Computer || out.Memory.Limit != 2200 || !out.Enabled {
		t.Fatalf("還沒有電腦：%+v %v", out, err)
	}
	if err := e.call(t, owner, "xbot.memory.write", WriteIn{BotID: e.bot.ID, Target: TargetMemory, Entries: []string{"客戶 A 用月結", "密碼：abc123"}}, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Memory.Entries, []string{"客戶 A 用月結", "密碼：***"}) || !out.Computer {
		t.Fatalf("寫入前遮蔽密碼：%+v", out.Memory)
	}
	// Bot 自己在電腦裡改了：以檔案為準，記成一筆變更。
	m := e.fs[e.bot.ID]
	m.Files[Dir+"/MEMORY.md"] = []byte("客戶 A 用月結\n§\n密碼：***\n§\n週一開會")
	var h HistoryOut
	if err := e.call(t, owner, "xbot.memory.history", HistoryIn{BotID: e.bot.ID}, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Changes) != 2 || h.Changes[0].Actor != "bot:"+e.bot.ID || !slices.Equal(h.Changes[0].Added, []string{"週一開會"}) ||
		!h.Changes[0].Undoable || h.Changes[1].Undoable || h.Changes[1].Actor != "owner" {
		t.Fatalf("%+v", h.Changes)
	}
	// 舊的那筆不能復原（之後又改過）。
	if err := e.call(t, owner, "xbot.memory.restore", RestoreIn{BotID: e.bot.ID, ChangeID: h.Changes[1].ID}, nil); code(err) != ops.CodeConflict {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.memory.restore", RestoreIn{BotID: e.bot.ID, ChangeID: h.Changes[0].ID}, &out); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(out.Memory.Entries, "週一開會") {
		t.Fatal("復原後拿掉 Bot 加的那條")
	}
	// 帶 previous 但不一致：conflict。
	err := e.call(t, owner, "xbot.memory.write", WriteIn{BotID: e.bot.ID, Target: TargetMemory, Entries: []string{"x"}, Previous: []string{"舊的"}}, nil)
	if code(err) != ops.CodeConflict {
		t.Fatal(err)
	}
	err = e.call(t, owner, "xbot.memory.write", WriteIn{BotID: e.bot.ID, Target: TargetUser, Entries: []string{strings.Repeat("x", 1400)}}, nil)
	if code(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
}

func TestBotAddsItsOwnMemoryWithinLimits(t *testing.T) {
	e := setup(t)
	other, _ := e.db.CreateBot(ctx, store.Bot{Name: "阿寶", Engine: "hermes", MemoryEnabled: true})
	me := ops.Bot(e.bot.ID, "小秘")
	var out ReadOut
	if err := e.call(t, me, "xbot.memory.add", AddIn{Target: TargetUser, Content: "喜歡簡短回答"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := e.call(t, me, "xbot.memory.add", AddIn{Target: TargetUser, Content: "喜歡簡短回答"}, &out); err != nil || len(out.User.Entries) != 1 {
		t.Fatalf("重複的不加：%+v %v", out.User, err)
	}
	if err := e.call(t, me, "xbot.memory.read", BotIn{}, &out); err != nil || out.User.Entries[0] != "喜歡簡短回答" {
		t.Fatal(err)
	}
	if err := e.call(t, me, "xbot.memory.read", BotIn{BotID: other.ID}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("不能讀別人的記憶：%v", err)
	}
	if err := e.call(t, me, "xbot.memory.write", WriteIn{BotID: e.bot.ID, Target: TargetUser, Entries: []string{}}, nil); err == nil {
		t.Fatal("Bot 不能整份換掉（manage）")
	}
	for i := 0; i < 2; i++ {
		_ = e.call(t, me, "xbot.memory.add", AddIn{Target: TargetUser, Content: strings.Repeat("長", 700)}, nil)
	}
	if err := e.call(t, me, "xbot.memory.add", AddIn{Target: TargetUser, Content: strings.Repeat("滿", 700)}, nil); code(err) != ops.CodeLimit {
		t.Fatalf("滿了要回 limit：%v", err)
	}
	if err := e.call(t, owner, "xbot.memory.add", AddIn{Target: TargetUser, Content: "x"}, nil); code(err) != ops.CodeForbidden {
		t.Fatal(err)
	}
	b := e.bot
	b.MemoryEnabled = false
	if _, err := e.db.UpdateBot(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := e.call(t, me, "xbot.memory.add", AddIn{Target: TargetMemory, Content: "x"}, nil); code(err) != ops.CodeConflict {
		t.Fatal("記憶關掉時不能寫")
	}
}
