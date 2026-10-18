package chat

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// 效能基準（v1.0，結果記在 docs/perf.md）：一條有 1 萬則訊息的對話串，讀最新一頁和往前翻到很前面的一頁。

const benchMessages = 10_000

func benchThread(b *testing.B) (*ops.Registry, store.Thread) {
	b.Helper()
	db, err := store.Open(bg, filepath.Join(b.TempDir(), store.FileName))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	Register(reg, Deps{Runtime: runtime.New(runtime.Options{DB: db, Bus: events.New()})})
	bot, err := db.CreateBot(bg, store.Bot{Name: "小幫手"})
	if err != nil {
		b.Fatal(err)
	}
	th, err := db.CreateThread(bg, store.Thread{BotID: bot.ID, Title: "長對話"})
	if err != nil {
		b.Fatal(err)
	}
	// 另一條對話串也塞一樣多，確認查詢只碰自己那條。
	other, err := db.CreateThread(bg, store.Thread{BotID: bot.ID, Title: "別條"})
	if err != nil {
		b.Fatal(err)
	}
	err = db.Tx(bg, func(q *store.Queries) error {
		for i := range benchMessages {
			sender := "owner"
			if i%2 == 1 {
				sender = "bot:" + bot.ID
			}
			for _, id := range []string{th.ID, other.ID} {
				if _, err := q.AddMessage(bg, store.Message{Scope: store.ScopeThread, ScopeID: id, Sender: sender,
					Content: "這是一則有點長度的訊息，用來量讀取對話的速度。"}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return reg, th
}

func benchRead(b *testing.B, reg *ops.Registry, in ReadIn) ReadOut {
	raw, _ := json.Marshal(in)
	res, err := reg.Invoke(bg, owner, "xbot.chat.read", raw, ops.Options{})
	if err != nil {
		b.Fatal(err)
	}
	var out ReadOut
	if err := json.Unmarshal(res.Output, &out); err != nil {
		b.Fatal(err)
	}
	return out
}

func BenchmarkChatRead(b *testing.B) {
	reg, th := benchThread(b)
	first := benchRead(b, reg, ReadIn{ThreadID: th.ID})
	if len(first.Messages) == 0 || !first.HasMore {
		b.Fatal("沒有讀到訊息")
	}
	b.Run("latest", func(b *testing.B) {
		for b.Loop() {
			benchRead(b, reg, ReadIn{ThreadID: th.ID})
		}
	})
	// 往前翻到最舊的那幾頁：before 很小時，不能從最新一路掃下去。
	oldest := first.Messages[0].Seq - 2*(benchMessages-100)
	b.Run("deep_page", func(b *testing.B) {
		for b.Loop() {
			out := benchRead(b, reg, ReadIn{ThreadID: th.ID, Before: oldest})
			if len(out.Messages) == 0 {
				b.Fatal("沒有讀到訊息")
			}
		}
	})
}
