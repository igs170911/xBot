package store

import (
	"errors"
	"testing"
)

func TestMemoryVersionsKeepOnlyTheLatest(t *testing.T) {
	db := openTest(t)
	b := newBot(t, db, "小秘")
	other := newBot(t, db, "阿寶")
	for i, s := range []string{"a", "b", "c", "d"} {
		must[MemoryVersion](t)(db.AddMemoryVersion(ctx, MemoryVersion{BotID: b.ID, Target: "memory", Before: "", After: s, Actor: "owner", CreatedAt: int64(i + 1)}, 3))
	}
	must[MemoryVersion](t)(db.AddMemoryVersion(ctx, MemoryVersion{BotID: b.ID, Target: "user", After: "u", ThreadID: "th1", CreatedAt: 9}, 3))
	must[MemoryVersion](t)(db.AddMemoryVersion(ctx, MemoryVersion{BotID: other.ID, Target: "memory", After: "x"}, 3))

	mem := must[[]MemoryVersion](t)(db.ListMemoryVersions(ctx, b.ID, "memory"))
	if len(mem) != 3 || mem[0].After != "d" || mem[2].After != "b" {
		t.Fatalf("只留最新 3 筆，新的在前：%+v", mem)
	}
	all := must[[]MemoryVersion](t)(db.ListMemoryVersions(ctx, b.ID, ""))
	if len(all) != 4 || all[0].Target != "user" || all[0].ThreadID != "th1" || all[1].ThreadID != "" {
		t.Fatalf("兩份都列：%+v", all)
	}
	got := must[MemoryVersion](t)(db.GetMemoryVersion(ctx, mem[1].ID))
	if got.After != "c" || got.Actor != "owner" {
		t.Fatalf("讀回來：%+v", got)
	}
	if _, err := db.GetMemoryVersion(ctx, "沒有"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	check(t, db.DeleteBot(ctx, b.ID))
	if left := must[[]MemoryVersion](t)(db.ListMemoryVersions(ctx, b.ID, "")); len(left) != 0 {
		t.Fatal("刪除 Bot 時一起刪")
	}
}
