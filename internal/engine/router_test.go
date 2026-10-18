package engine_test

import (
	"context"
	"errors"
	"testing"

	"xbot/internal/engine"
)

type stubSession string

func (s stubSession) ID() string       { return string(s) }
func (s stubSession) ResumeID() string { return string(s) }

type stubDriver struct {
	name   string
	ch     chan engine.Event
	sent   []string
	closed []string
}

func (d *stubDriver) Name() string { return d.name }
func (d *stubDriver) Start(_ context.Context, o engine.StartOptions) (engine.Session, error) {
	return stubSession(d.name + ":" + o.ThreadID), nil
}
func (d *stubDriver) Send(_ context.Context, _ engine.Session, m engine.Message) error {
	d.sent = append(d.sent, m.Text)
	return nil
}
func (d *stubDriver) Interrupt(context.Context, engine.Session) error { return nil }
func (d *stubDriver) Respond(context.Context, engine.Session, string, engine.Answer) error {
	return nil
}
func (d *stubDriver) Events(engine.Session) <-chan engine.Event { return d.ch }
func (d *stubDriver) Close(botID string)                        { d.closed = append(d.closed, botID) }

func TestRouter(t *testing.T) {
	h := &stubDriver{name: "hermes", ch: make(chan engine.Event, 1)}
	c := &stubDriver{name: "claude-code", ch: make(chan engine.Event, 1)}
	engines := map[string]string{"b1": "", "b2": "claude-code", "b3": "gemini"}
	r := engine.NewRouter("hermes", func(_ context.Context, botID string) (string, error) { return engines[botID], nil }, h, c)

	s1, err := r.Start(context.Background(), engine.StartOptions{BotID: "b1", ThreadID: "t"})
	if err != nil || s1.ID() != "hermes:t" {
		t.Fatalf("預設引擎 = %v %v", s1, err)
	}
	s2, err := r.Start(context.Background(), engine.StartOptions{BotID: "b2", ThreadID: "t"})
	if err != nil || s2.ID() != "claude-code:t" {
		t.Fatalf("claude-code = %v %v", s2, err)
	}
	if _, err := r.Start(context.Background(), engine.StartOptions{BotID: "b3"}); !errors.Is(err, engine.ErrUnknownEngine) {
		t.Errorf("不支援的引擎 = %v", err)
	}
	_ = r.Send(context.Background(), s2, engine.Message{Text: "hi"})
	if len(c.sent) != 1 || len(h.sent) != 0 {
		t.Errorf("送到錯的驅動：hermes=%v claude=%v", h.sent, c.sent)
	}
	c.ch <- engine.Event{Type: engine.EventText, Text: "x"}
	close(c.ch)
	ev := r.Events(s2)
	if e := <-ev; e.Text != "x" {
		t.Errorf("事件 = %+v", e)
	}
	if _, ok := <-ev; ok {
		t.Error("驅動關掉事件後應該關閉")
	}
	if err := r.Send(context.Background(), s2, engine.Message{Text: "again"}); !errors.Is(err, engine.ErrDisconnected) {
		t.Errorf("事件關閉後 = %v", err)
	}
	r.Close("b2")
	if len(h.closed) != 1 || len(c.closed) != 1 {
		t.Errorf("Close 應該交給每個驅動：%v %v", h.closed, c.closed)
	}
}
