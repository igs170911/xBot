package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Router 依 Bot 的引擎（bot.engine）把呼叫交給對應的驅動；runtime 只看到一個 Driver（E12）。
type Router struct {
	def      string
	engineOf func(ctx context.Context, botID string) (string, error)
	drivers  map[string]Driver

	mu       sync.Mutex
	sessions map[Session]*routed
}

type routed struct {
	d  Driver
	ch <-chan Event
}

var (
	_ Driver  = (*Router)(nil)
	_ Steerer = (*Router)(nil)
)

// ErrUnknownEngine 表示 Bot 設定的引擎沒有驅動。
var ErrUnknownEngine = errors.New("不支援這個引擎")

// NewRouter 建立 Router。def 是 bot.engine 空白時用的引擎；engineOf 讀 Bot 的引擎。
func NewRouter(def string, engineOf func(ctx context.Context, botID string) (string, error), drivers ...Driver) *Router {
	r := &Router{def: def, engineOf: engineOf, drivers: map[string]Driver{}, sessions: map[Session]*routed{}}
	for _, d := range drivers {
		r.drivers[d.Name()] = d
	}
	return r
}

// Name 實作 Driver。
func (r *Router) Name() string { return "router" }

// Driver 回傳某個引擎的驅動。
func (r *Router) Driver(name string) (Driver, bool) {
	d, ok := r.drivers[name]
	return d, ok
}

// Start 實作 Driver：照 Bot 目前的引擎開 session。
func (r *Router) Start(ctx context.Context, o StartOptions) (Session, error) {
	name, err := r.engineOf(ctx, o.BotID)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = r.def
	}
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("%w：%s", ErrUnknownEngine, name)
	}
	s, err := d.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if _, ok := r.sessions[s]; !ok {
		r.sessions[s] = &routed{d: d}
	}
	r.mu.Unlock()
	return s, nil
}

func (r *Router) route(s Session) (*routed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.sessions[s]
	if !ok {
		return nil, ErrDisconnected
	}
	return rt, nil
}

// Send 實作 Driver。
func (r *Router) Send(ctx context.Context, s Session, m Message) error {
	rt, err := r.route(s)
	if err != nil {
		return err
	}
	return rt.d.Send(ctx, s, m)
}

// Interrupt 實作 Driver。
func (r *Router) Interrupt(ctx context.Context, s Session) error {
	rt, err := r.route(s)
	if err != nil {
		return err
	}
	return rt.d.Interrupt(ctx, s)
}

// Respond 實作 Driver。
func (r *Router) Respond(ctx context.Context, s Session, requestID string, a Answer) error {
	rt, err := r.route(s)
	if err != nil {
		return err
	}
	return rt.d.Respond(ctx, s, requestID, a)
}

// Steer 實作 Steerer：交給這個 session 的驅動；驅動不能插話就回 false。
func (r *Router) Steer(ctx context.Context, s Session, m Message) (bool, error) {
	rt, err := r.route(s)
	if err != nil {
		return false, err
	}
	st, ok := rt.d.(Steerer)
	if !ok {
		return false, nil
	}
	return st.Steer(ctx, s, m)
}

// Events 實作 Driver：轉送驅動的事件；驅動關掉事件之後忘掉這個 session。
func (r *Router) Events(s Session) <-chan Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.sessions[s]
	if !ok {
		ch := make(chan Event)
		close(ch)
		return ch
	}
	if rt.ch == nil {
		in := rt.d.Events(s)
		out := make(chan Event, 64)
		rt.ch = out
		go func() {
			for e := range in {
				out <- e
			}
			r.mu.Lock()
			if r.sessions[s] == rt {
				delete(r.sessions, s)
			}
			r.mu.Unlock()
			close(out)
		}()
	}
	return rt.ch
}

// Close 關掉一個 Bot 在所有引擎上的連線（電腦停止、刪除、收回憑證時）。
func (r *Router) Close(botID string) {
	for _, d := range r.drivers {
		if c, ok := d.(interface{ Close(string) }); ok {
			c.Close(botID)
		}
	}
}
