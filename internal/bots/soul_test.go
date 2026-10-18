package bots

import (
	"context"
	"path/filepath"
	"testing"

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
