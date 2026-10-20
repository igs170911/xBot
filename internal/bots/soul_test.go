package bots

import (
	"context"
	"path/filepath"
	"testing"

	"xbot/internal/engine/hermes"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func TestSoulChangedAndMemoryToggle(t *testing.T) {
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	var changed []string
	Register(reg, Deps{SoulChanged: func(_ context.Context, id string) { changed = append(changed, id) }})
	e := &env{db: db, reg: reg}
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "小秘"}, &v)
	if !v.MemoryEnabled {
		t.Fatal("新 Bot 的記憶預設打開")
	}
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "color": "red", "memory_enabled": false}, &v)
	if len(changed) != 0 || v.MemoryEnabled {
		t.Fatalf("沒改人設不用寫 SOUL.md：%v %+v", changed, v)
	}
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "soul": "說話簡短"}, &v)
	if len(changed) != 1 || changed[0] != v.ID {
		t.Fatalf("改了人設要寫 SOUL.md：%v", changed)
	}
}

// 2026-10-20：回話風格（設定 › 一般）所有 Bot 共用；改了每個 Bot 的 SOUL.md 都重寫。
func TestReplyStyleIsSharedAndRewritesEverySoul(t *testing.T) {
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	var changed []string
	Register(reg, Deps{SoulChanged: func(_ context.Context, id string) { changed = append(changed, id) }})
	e := &env{db: db, reg: reg}
	var a, b View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "小秘"}, &a)
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "阿哲"}, &b)
	var v ReplyStyleView
	e.must(t, owner, "xbot.bots.reply_style_get", map[string]any{}, &v)
	if v.Style != hermes.ReplyBrief {
		t.Fatalf("預設簡短：%+v", v)
	}
	if _, err := e.call(t, owner, "xbot.bots.reply_style_set", map[string]any{"style": "loud"}); err == nil {
		t.Fatal("不認得的風格要擋")
	}
	e.must(t, owner, "xbot.bots.reply_style_set", map[string]any{"style": "detailed"}, &v)
	e.must(t, owner, "xbot.bots.reply_style_get", map[string]any{}, &v)
	if v.Style != hermes.ReplyDetailed || len(changed) != 2 {
		t.Fatalf("%+v，重寫了 %v", v, changed)
	}
	if _, err := e.call(t, ops.Bot(a.ID, "小秘"), "xbot.bots.reply_style_set", map[string]any{"style": "brief"}); err == nil {
		t.Fatal("Bot 不能改回話風格")
	}
}

// 2026-10-20：側欄最上面是大總管（主要 Bot），原本只有匯入時才設得到。bots.update primary：同時只有一個，每個 Bot 的名冊重寫。
func TestSetPrimaryKeepsOneChief(t *testing.T) {
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	var changed []string
	Register(reg, Deps{SoulChanged: func(_ context.Context, id string) { changed = append(changed, id) }})
	e := &env{db: db, reg: reg}
	var a, b View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "水哥哥"}, &a)
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "研究員"}, &b)
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": a.ID, "primary": true}, &a)
	if !a.Primary || len(changed) != 2 {
		t.Fatalf("%+v，重寫 %v", a, changed)
	}
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": b.ID, "primary": true}, &b)
	e.must(t, owner, "xbot.bots.get", map[string]any{"id": a.ID}, &a)
	if !b.Primary || a.Primary {
		t.Fatalf("同時只有一個大總管：%v %v", a.Primary, b.Primary)
	}
}
