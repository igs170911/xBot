package inbox

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"xbot/internal/bots"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 效能基準（v1.0，結果記在 docs/perf.md）：5000 張卡（一半還沒處理、一半已處理），列待我處理第一頁與通知中心。

const benchItems = 5000

func benchInbox(b *testing.B) *ops.Registry {
	b.Helper()
	db, err := store.Open(bg, filepath.Join(b.TempDir(), store.FileName))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	bots.Register(reg, bots.Deps{})
	Register(reg)
	var botIDs []string
	for i := range 10 {
		bot, err := db.CreateBot(bg, store.Bot{Name: fmt.Sprintf("Bot%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		botIDs = append(botIDs, bot.ID)
	}
	kinds := []string{"approval", "question", "done", "error", "mention", "dismissed"}
	err = db.Tx(bg, func(q *store.Queries) error {
		for i := range benchItems {
			k := kinds[i%len(kinds)]
			it := store.InboxItem{Kind: k, BotID: botIDs[i%len(botIDs)], Title: fmt.Sprintf("卡片 %d", i), Ref: fmt.Sprintf("ref-%d", i)}
			if k == "dismissed" {
				it.Kind = "approval"
			}
			it, err := q.CreateInboxItem(bg, it)
			if err != nil {
				return err
			}
			if i%2 == 1 {
				status, res := store.InboxResolved, "once"
				if k == "dismissed" {
					status, res = store.InboxDismissed, "dismissed"
				}
				if err := q.SettleInboxItem(bg, it.ID, status, res, "owner", nil); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return reg
}

func BenchmarkInboxList(b *testing.B) {
	reg := benchInbox(b)
	list := func(b *testing.B, in InboxListIn) {
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(bg, owner, "xbot.inbox.list", raw, ops.Options{})
		if err != nil {
			b.Fatal(err)
		}
		var out InboxListOut
		if err := json.Unmarshal(res.Output, &out); err != nil || len(out.Items) == 0 {
			b.Fatal("沒有讀到卡片", err)
		}
	}
	b.Run("open", func(b *testing.B) {
		for b.Loop() {
			list(b, InboxListIn{Status: "open"})
		}
	})
	b.Run("all_group", func(b *testing.B) {
		for b.Loop() {
			list(b, InboxListIn{Status: "all", Group: "decide", Limit: 50})
		}
	})
	b.Run("counts", func(b *testing.B) {
		for b.Loop() {
			if _, err := reg.Invoke(bg, owner, "xbot.inbox.counts", json.RawMessage(`{}`), ops.Options{}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
