package events

import (
	"context"
	"log/slog"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// FromStore 把 event 表的一筆轉成匯流排事件（帶流水號）。
func FromStore(e store.Event) Event {
	return Event{ID: e.ID, Kind: e.Kind, BotID: e.BotID, ThreadID: e.ThreadID, TaskID: e.TaskID, Payload: e.Payload, At: e.CreatedAt}
}

// Stream 是可以斷線續傳的訂閱（SSE 用）：
//   - lastID 大於等於 0 時，先補送 event 表中 id 大於 lastID 的事件；小於 0 表示從現在開始。
//   - 之後照匯流排的順序即時送出（文字增量和已存事件的先後不會亂）。已存的事件（ID > 0）只會照 id 遞增送出：
//     遇到跳號就先從 event 表補到那一筆，已經送過的不再送；緩衝滿了掉事件時也從 event 表補，
//     所以已存的事件不會漏或重複（同 LazyBoy server/main.py 的事件流）。
//     沒存的即時事件（ID 為 0，例：文字增量）直接轉送，掉了就沒了。
//
// 發布已存事件的一方必須在寫入（交易提交）之後才 Publish。ctx 結束時通道關閉。
func Stream(ctx context.Context, bus *Bus, db *store.DB, filter Filter, lastID int64, log *slog.Logger) <-chan Event {
	if log == nil {
		log = slog.Default()
	}
	sub := bus.Subscribe(nil, 0) // 先訂閱再讀資料庫：之後提交的事件一定會通知到
	out := make(chan Event)
	cursor := lastID
	// 訂閱當下最新的事件 id：補送只補到這裡。之後提交的事件一定會從匯流排送來，照匯流排的順序送出；
	// 如果補送一路讀到最新，後來才存的事件會跑到比它早發布的即時事件（文字增量）前面。
	head, startErr := db.LastEventID(ctx, "")
	if cursor < 0 {
		// 在回傳前決定起點：Stream 回傳之後才提交的事件一定收得到。
		cursor = head
	}
	go func() {
		defer close(out)
		defer sub.Close()
		if startErr != nil {
			if ctx.Err() == nil {
				log.Error("讀取最新事件失敗", "err", startErr)
			}
			return
		}
		send := func(e Event) bool {
			if filter != nil && !filter(e) {
				return true
			}
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		// catchUp 從 event 表讀 cursor 之後、到 upTo 為止的事件（upTo 小於 0 表示讀到最新）。
		catchUp := func(upTo int64) bool {
			for {
				batch, err := db.ListEvents(ctx, "", cursor, limits.SSEBatch)
				if err != nil {
					if ctx.Err() == nil {
						log.Error("讀取事件失敗", "err", err)
					}
					return false
				}
				for _, row := range batch {
					if upTo >= 0 && row.ID > upTo {
						return true // 之後的等匯流排送來，保持和即時事件的先後順序
					}
					cursor = row.ID
					if !send(FromStore(row)) {
						return false
					}
				}
				if len(batch) < limits.SSEBatch {
					return true
				}
			}
		}
		if !catchUp(head) {
			return
		}
		dropped := 0
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				if d := sub.Dropped(); d != dropped {
					dropped = d // 有事件被丟掉：已存的從資料庫補
					if !catchUp(-1) {
						return
					}
				}
				switch {
				case e.ID == 0:
					if !send(e) {
						return
					}
				case e.ID == cursor+1:
					cursor = e.ID
					if !send(e) {
						return
					}
				case e.ID > cursor:
					// 中間有空號（別的事件還沒通知到、或交易回滾留下的號碼）：從資料庫補到這一筆為止。
					if !catchUp(e.ID) {
						return
					}
				}
			}
		}
	}()
	return out
}
