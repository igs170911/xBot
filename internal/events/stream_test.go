package events

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/store"
)

func newDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// appendEvent 寫進 event 表再發布（同 runtime 的做法）。
func appendEvent(t *testing.T, b *Bus, db *store.DB, kind string) Event {
	t.Helper()
	row, err := db.AppendEvent(context.Background(), store.Event{Kind: kind})
	if err != nil {
		t.Error(err)
		return Event{}
	}
	e := FromStore(row)
	b.Publish(e)
	return e
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("通道關掉了")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("等不到事件")
	}
	return Event{}
}

func TestStreamFromNowAndResume(t *testing.T) {
	b, db := New(), newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := appendEvent(t, b, db, "old")
	ch := Stream(ctx, b, db, nil, -1, nil)
	e1 := appendEvent(t, b, db, "new")
	if got := recv(t, ch); got.ID != e1.ID {
		t.Fatalf("從現在開始不該補送舊事件：%+v", got)
	}
	// 即時事件（不存）照匯流排順序送出。
	b.Publish(Event{Kind: "delta"})
	e2 := appendEvent(t, b, db, "done")
	if got := recv(t, ch); got.ID != 0 || got.Kind != "delta" {
		t.Fatalf("即時事件：%+v", got)
	}
	if got := recv(t, ch); got.ID != e2.ID {
		t.Fatalf("即時事件之後的已存事件：%+v", got)
	}
	// 從 old 之後續傳。
	ch2 := Stream(ctx, b, db, nil, old.ID, nil)
	if got := recv(t, ch2); got.ID != e1.ID {
		t.Fatalf("續傳：%+v", got)
	}
	cancel()
	for range ch {
	}
}

// 跳號時（某筆事件寫了但沒通知、或通知比較晚到）從資料庫補，不漏也不重複。
func TestStreamFillsGaps(t *testing.T) {
	b, db := New(), newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Stream(ctx, b, db, nil, 0, nil)
	silent, err := db.AppendEvent(ctx, store.Event{Kind: "silent"}) // 寫了但還沒 Publish
	if err != nil {
		t.Fatal(err)
	}
	next := appendEvent(t, b, db, "next")
	b.Publish(FromStore(silent)) // 晚到的通知：已經補送過，不再送
	last := appendEvent(t, b, db, "last")
	for _, want := range []int64{silent.ID, next.ID, last.ID} {
		if got := recv(t, ch); got.ID != want {
			t.Fatalf("應該是 %d，拿到 %+v", want, got)
		}
	}
}

// 很多個寫入者同時寫，訂閱者照 id 順序一筆不漏地收到。
func TestStreamConcurrentWritersInOrder(t *testing.T) {
	b, db := New(), newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Stream(ctx, b, db, nil, 0, nil)
	const writers, each = 8, 25
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range each {
				appendEvent(t, b, db, "x")
			}
		})
	}
	for i := int64(1); i <= writers*each; i++ {
		if got := recv(t, ch); got.ID != i {
			t.Fatalf("應該是 %d，拿到 %d", i, got.ID)
		}
	}
	wg.Wait()
}

func TestForIdentity(t *testing.T) {
	agent := ops.Identity{Kind: ops.KindAgent, ID: "t1", Scopes: []ops.Scope{ops.ScopeRead}, Resources: ops.Resources{Bots: []string{"b1"}}}
	f := ForIdentity(agent)
	cases := map[string]struct {
		e    Event
		want bool
	}{
		"範圍內的 Bot":   {Event{BotID: "b1"}, true},
		"範圍外的 Bot":   {Event{BotID: "b2"}, false},
		"別人的全域事件":    {Event{Payload: json.RawMessage(`{"actor":"owner"}`)}, false},
		"自己的全域事件":    {Event{Payload: json.RawMessage(`{"actor":"agent:t1"}`)}, true},
		"payload 壞掉": {Event{Payload: json.RawMessage(`x`)}, false},
	}
	for name, c := range cases {
		if f(c.e) != c.want {
			t.Errorf("%s：應該是 %v", name, c.want)
		}
	}
	if ForIdentity(ops.Owner("ui")) != nil {
		t.Error("使用者本人看全部")
	}
	agent.Scopes = []ops.Scope{ops.ScopeManage}
	if ForIdentity(agent)(Event{BotID: "b1"}) {
		t.Error("沒有 read 權限什麼都看不到")
	}
}
