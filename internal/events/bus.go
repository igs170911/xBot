// Package events 是服務內的事件匯流排：runtime 發布，SSE（C03）、MCP 資源通知與等待操作訂閱。
package events

import (
	"encoding/json"
	"sync"
)

// Event 是匯流排上的一個事件。
type Event struct {
	// ID 是 event 表的流水號（SSE 斷線續傳用）；沒有寫進資料庫的串流片段（例：文字增量）為 0。
	ID       int64           `json:"id,omitempty"`
	Kind     string          `json:"kind"`
	BotID    string          `json:"bot_id,omitempty"`
	ThreadID string          `json:"thread_id,omitempty"`
	TaskID   string          `json:"task_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	At       int64           `json:"at"`
}

// Filter 決定訂閱者要哪些事件；nil 表示全部。
type Filter func(Event) bool

// Bus 是簡單的發布／訂閱。Publish 不會被慢的訂閱者卡住：訂閱者的緩衝滿了就丟掉那個事件，
// 並把 Subscription.Dropped 加一（SSE 端應在掉事件後重新讀資料庫補齊）。
type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
}

// New 建立匯流排。
func New() *Bus { return &Bus{subs: map[*Subscription]struct{}{}} }

// Subscription 是一個訂閱。
type Subscription struct {
	C       <-chan Event
	c       chan Event
	filter  Filter
	bus     *Bus
	mu      sync.Mutex
	dropped int
	closed  bool
}

// DefaultBuffer 是訂閱的預設緩衝大小。
const DefaultBuffer = 1024

// Subscribe 訂閱事件；buffer <= 0 時用 DefaultBuffer。用完要 Close。
func (b *Bus) Subscribe(filter Filter, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	c := make(chan Event, buffer)
	s := &Subscription{C: c, c: c, filter: filter, bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish 把事件送給所有符合條件的訂閱者。
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		s.mu.Lock()
		if !s.closed {
			select {
			case s.c <- e:
			default:
				s.dropped++
			}
		}
		s.mu.Unlock()
	}
}

// Dropped 回傳因為緩衝滿而丟掉的事件數。
func (s *Subscription) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Close 取消訂閱並關閉 C。
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.c)
	}
	s.mu.Unlock()
}

// ForBot 只要某個 Bot 的事件。
func ForBot(botID string) Filter { return func(e Event) bool { return e.BotID == botID } }

// ForTask 只要某個任務的事件。
func ForTask(taskID string) Filter { return func(e Event) bool { return e.TaskID == taskID } }
