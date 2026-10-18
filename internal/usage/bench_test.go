package usage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// 效能基準（v1.0，結果記在 docs/perf.md）：10 萬筆用量明細（分散在 90 天、20 個 Bot），看最近 30 天的統計。

const benchRows = 100_000

func BenchmarkUsageSummary(b *testing.B) {
	db, err := store.Open(bg, filepath.Join(b.TempDir(), store.FileName))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	reg := ops.New(ops.Config{DB: db})
	Register(reg, Deps{Budgets: &proxy.Budgets{DB: db, Now: func() time.Time { return now }, Location: time.UTC}})
	var botIDs []string
	for i := range 20 {
		bot, err := db.CreateBot(bg, store.Bot{Name: fmt.Sprintf("Bot%d", i)})
		if err != nil {
			b.Fatal(err)
		}
		botIDs = append(botIDs, bot.ID)
	}
	span := (90 * 24 * time.Hour).Milliseconds()
	err = db.Tx(bg, func(q *store.Queries) error {
		for i := range benchRows {
			u := store.UsageEntry{BotID: botIDs[i%len(botIDs)], Provider: []string{"openai", "anthropic", "oc"}[i%3], Model: fmt.Sprintf("m%d", i%5),
				InputTokens: 1000, OutputTokens: 100, CacheReadTokens: 200, CostMicros: 1234, Unpriced: i%97 == 0,
				CreatedAt: now.UnixMilli() - span + int64(i)*span/benchRows}
			if _, err := q.AddUsage(bg, u); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	run := func(b *testing.B, in RangeIn) {
		raw, _ := json.Marshal(in)
		if _, err := reg.Invoke(bg, ops.Owner("bench"), "xbot.usage.summary", raw, ops.Options{}); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("30d", func(b *testing.B) {
		for b.Loop() {
			run(b, RangeIn{Days: 30})
		}
	})
	b.Run("7d_one_bot", func(b *testing.B) {
		for b.Loop() {
			run(b, RangeIn{Days: 7, BotID: botIDs[3]})
		}
	})
}
