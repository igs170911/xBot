package catalog_test

import (
	"context"
	"path/filepath"
	"testing"

	"xbot/internal/catalog"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func TestTeardownSkipsBotsWithoutComputer(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b, _ := db.CreateBot(ctx, store.Bot{Name: "x"})
	var closed, removed []string
	td := catalog.Teardown(db, func(id string) { closed = append(closed, id) }, func(_ context.Context, id string) error {
		removed = append(removed, id)
		return nil
	})
	if err := td(ctx, b.ID); err != nil || len(closed) != 1 || len(removed) != 0 {
		t.Fatalf("沒開過電腦不碰 Docker：%v %v %v", err, closed, removed)
	}
	if err := db.UpsertBox(ctx, store.Box{BotID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := td(ctx, b.ID); err != nil || len(removed) != 1 {
		t.Fatalf("有電腦要刪：%v %v", err, removed)
	}
}

func TestRegisterWithoutDeps(t *testing.T) {
	// 指令列與 gen ts 只登錄定義：不能 panic，而且每個核心操作都在。
	reg := ops.New(ops.Config{})
	catalog.Register(reg, catalog.Deps{})
	for _, name := range []string{
		"xbot.bots.list", "xbot.bots.get", "xbot.bots.create", "xbot.bots.update", "xbot.bots.archive", "xbot.bots.delete",
		"xbot.chat.send", "xbot.chat.stop", "xbot.chat.read", "xbot.chat.list_threads", "xbot.chat.wait_until_settled",
		"xbot.approvals.list", "xbot.approvals.wait", "xbot.approvals.decide", "xbot.inbox.list", "xbot.audit.list",
		"xbot.models.get", "xbot.models.set_provider", "xbot.models.set_default",
	} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("少了 %s", name)
		}
	}
}
