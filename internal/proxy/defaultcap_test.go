package proxy

import (
	"context"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/store"
)

func inboxItems(t *testing.T, db *store.DB) []store.InboxItem {
	t.Helper()
	items, err := db.ListInboxFiltered(context.Background(), store.InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// 新安裝：直接套用預設月上限、不發卡；只做一次（之後拿掉上限也不會再加回）。
func TestDefaultCapNewInstall(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	b := &Budgets{DB: db}
	applied, err := b.EnsureDefaultCap(ctx)
	if err != nil || !applied {
		t.Fatalf("新安裝要套用：%v %v", applied, err)
	}
	if c, _ := b.GlobalCaps(ctx); c.MonthlyMicros != limits.DefaultGlobalMonthlyMicros || limits.DefaultGlobalMonthlyMicros != 20_000_000 {
		t.Fatalf("全域上限：%+v", c)
	}
	if n := len(inboxItems(t, db)); n != 0 {
		t.Fatalf("新安裝不發卡：%d 張", n)
	}
	// 使用者拿掉上限 → 重啟後不再自動加回。
	_ = db.SetSetting(ctx, GlobalCapsSetting, Caps{})
	if applied, err := b.EnsureDefaultCap(ctx); err != nil || applied {
		t.Fatalf("只做一次：%v %v", applied, err)
	}
	if c, _ := b.GlobalCaps(ctx); c.MonthlyMicros != 0 {
		t.Fatalf("拿掉的上限被加回：%+v", c)
	}
}

// 舊安裝、沒有上限：套用並發一張通知卡（只發一次）。
func TestDefaultCapExistingInstallNotifies(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := db.CreateBot(ctx, store.Bot{Name: "阿哲"}); err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	b := &Budgets{DB: db, Notify: func(e store.Event) { events = append(events, e) }}
	if applied, err := b.EnsureDefaultCap(ctx); err != nil || !applied {
		t.Fatalf("要套用：%v %v", applied, err)
	}
	items := inboxItems(t, db)
	if len(items) != 1 || items[0].Kind != "budget" || items[0].Ref != DefaultCapRef || len(events) != 1 {
		t.Fatalf("通知卡：%+v 事件 %d", items, len(events))
	}
	if _, err := b.EnsureDefaultCap(ctx); err != nil || len(inboxItems(t, db)) != 1 {
		t.Fatalf("卡只發一次：%v", err)
	}
}

// 舊安裝、已經有月上限：完全不動。
func TestDefaultCapKeepsExistingCap(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	_, _ = db.CreateBot(ctx, store.Bot{Name: "阿哲"})
	_ = db.SetSetting(ctx, GlobalCapsSetting, Caps{MonthlyMicros: 5_000_000})
	b := &Budgets{DB: db}
	if applied, err := b.EnsureDefaultCap(ctx); err != nil || applied {
		t.Fatalf("已有上限不套用：%v %v", applied, err)
	}
	if c, _ := b.GlobalCaps(ctx); c.MonthlyMicros != 5_000_000 {
		t.Fatalf("上限被改：%+v", c)
	}
	if n := len(inboxItems(t, db)); n != 0 {
		t.Fatalf("不發卡：%d", n)
	}
}
