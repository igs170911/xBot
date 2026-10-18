package events

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"xbot/internal/store"
)

// 效能基準（v1.0，結果記在 docs/perf.md）：
//   - Fanout：50 個 SSE 訂閱者，每個事件寫進 event 表再發布，等 50 個都收到；concurrent 是 4 個 goroutine 同時發布（會跳號、從資料庫補）。
//   - Rate100：50 個訂閱者、每秒 100 個事件，量「發布 → 訂閱者收到」的延遲（PRD §8：SSE 事件延遲 ≤ 200 ms）。
//   - Replay：斷線續傳，從 event 表補送 1 萬筆。

const benchSubs = 50

func benchDB(b *testing.B) *store.DB {
	b.Helper()
	db, err := store.Open(context.Background(), filepath.Join(b.TempDir(), store.FileName))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

var benchPayload = json.RawMessage(`{"op":"xbot.bots.update","actor":"owner","status":"ok"}`)

// subscribers 開 n 個訂閱者，每收到一筆就呼叫 got（帶收到的時間）。回傳停止函式。
func subscribers(ctx context.Context, bus *Bus, db *store.DB, n int, got func(Event, time.Time)) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range n {
		ch := Stream(ctx, bus, db, nil, -1, nil)
		wg.Go(func() {
			for e := range ch {
				// 和 SSE 一樣每筆都編成 JSON
				if _, err := json.Marshal(e); err != nil {
					panic(err)
				}
				got(e, time.Now())
			}
		})
	}
	return func() { cancel(); wg.Wait() }
}

func publish(b *testing.B, bus *Bus, db *store.DB) Event {
	row, err := db.AppendEvent(context.Background(), store.Event{Kind: "op", Payload: benchPayload})
	if err != nil {
		b.Error(err)
		return Event{}
	}
	e := FromStore(row)
	bus.Publish(e)
	return e
}

func BenchmarkFanout(b *testing.B) {
	for _, pubs := range []int{1, 4} {
		name := map[int]string{1: "sequential", 4: "concurrent"}[pubs]
		b.Run(name, func(b *testing.B) {
			bus, db := New(), benchDB(b)
			var mu sync.Mutex
			received := 0
			cond := sync.NewCond(&mu)
			stop := subscribers(context.Background(), bus, db, benchSubs, func(Event, time.Time) {
				mu.Lock()
				received++
				cond.Broadcast()
				mu.Unlock()
			})
			defer stop()
			want := 0
			for b.Loop() {
				var wg sync.WaitGroup
				for range pubs {
					wg.Go(func() { publish(b, bus, db) })
				}
				wg.Wait()
				want += pubs * benchSubs
				mu.Lock()
				for received < want {
					cond.Wait()
				}
				mu.Unlock()
			}
			b.ReportMetric(float64(pubs), "events/op")
		})
	}
}

func BenchmarkRate100(b *testing.B) {
	bus, db := New(), benchDB(b)
	var mu sync.Mutex
	sent := map[int64]time.Time{}
	var lat []time.Duration
	stop := subscribers(context.Background(), bus, db, benchSubs, func(e Event, at time.Time) {
		mu.Lock()
		if t, ok := sent[e.ID]; ok {
			lat = append(lat, at.Sub(t))
		}
		mu.Unlock()
	})
	defer stop()
	tick := time.NewTicker(10 * time.Millisecond) // 每秒 100 個
	defer tick.Stop()
	for b.Loop() {
		<-tick.C
		row, err := db.AppendEvent(context.Background(), store.Event{Kind: "op", Payload: benchPayload})
		if err != nil {
			b.Fatal(err)
		}
		mu.Lock()
		sent[row.ID] = time.Now()
		mu.Unlock()
		bus.Publish(FromStore(row))
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(lat) == 0 {
		b.Fatal("沒有收到事件")
	}
	slices.Sort(lat)
	b.ReportMetric(float64(lat[len(lat)/2].Microseconds()), "p50-µs")
	b.ReportMetric(float64(lat[len(lat)*99/100].Microseconds()), "p99-µs")
	b.ReportMetric(float64(lat[len(lat)-1].Microseconds()), "max-µs")
}

func BenchmarkReplay(b *testing.B) {
	const n = 10_000
	bus, db := New(), benchDB(b)
	err := db.Tx(context.Background(), func(q *store.Queries) error {
		for range n {
			if _, err := q.AppendEvent(context.Background(), store.Event{Kind: "op", Payload: benchPayload}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		ch := Stream(ctx, bus, db, nil, 0, nil)
		for got := 0; got < n; got++ {
			if _, ok := <-ch; !ok {
				b.Fatal("通道提早關了")
			}
		}
		cancel()
		for range ch {
		}
	}
	b.ReportMetric(n, "events/op")
}

// 每個 SSE 連線一開始都要讀最新事件 id。
func BenchmarkLastEventID(b *testing.B) {
	db := benchDB(b)
	err := db.Tx(context.Background(), func(q *store.Queries) error {
		for range 10_000 {
			if _, err := q.AppendEvent(context.Background(), store.Event{Kind: "op", Payload: benchPayload}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := db.LastEventID(context.Background(), ""); err != nil {
			b.Fatal(err)
		}
	}
}
