package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"xbot/internal/events"
	"xbot/internal/store"
)

// B12：開機前排隊等主機（主機離線／滿了）。等待期間任務是 waiting_box，task.state 帶著原因；
// 放行後照常開機，開機完成後釋放名額。
func TestAdmitQueuesBeforeBoot(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	gate := make(chan struct{})
	released := make(chan struct{}, 1)
	f.rt.o.Admit = func(ctx context.Context, botID string, wait func(any)) (func(), error) {
		wait(map[string]any{"reason": "host_full", "host_name": "cloud-01", "position": 1})
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return func() { released <- struct{}{} }, nil
	}
	sub := f.rt.Bus().Subscribe(events.ForBot(f.bot.ID), 0)
	defer sub.Close()
	res, err := f.rt.Chat(ctx, f.bot.ID, "", "回覆 OK")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for waiting := false; !waiting; {
		select {
		case e := <-sub.C:
			var p struct {
				State string         `json:"state"`
				Wait  map[string]any `json:"wait"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			waiting = e.Kind == KindTaskState && p.State == store.TaskWaitingBox && p.Wait["reason"] == "host_full"
		case <-deadline:
			t.Fatal("沒有收到排隊原因")
		}
	}
	close(gate)
	if task := f.wait(t, res.TaskID); task.State != store.TaskDone || task.Verdict != VerdictAnswer {
		t.Fatalf("放行後應該照常完成：%+v", task)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("開機後沒有釋放名額")
	}

	// 排隊中按停止：以 cancelled 結束，不會開機。
	f.rt.o.Admit = func(ctx context.Context, botID string, wait func(any)) (func(), error) {
		wait(map[string]any{"reason": "host_offline"})
		<-ctx.Done()
		return nil, ctx.Err()
	}
	res, err = f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "再一次")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if ok, err := f.rt.Stop(ctx, f.bot.ID, ""); !ok || err != nil {
		t.Fatalf("停止：%v %v", ok, err)
	}
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictCancelled {
		t.Fatalf("排隊中停止：%+v", task)
	}

	// 排隊失敗（例：主機被移除）：任務失敗並寫出原因。
	f.rt.o.Admit = func(context.Context, string, func(any)) (func(), error) {
		return nil, errors.New("找不到這台主機")
	}
	res, err = f.rt.Chat(ctx, f.bot.ID, res.ThreadID, "第三次")
	if err != nil {
		t.Fatal(err)
	}
	if task := f.wait(t, res.TaskID); task.Verdict != VerdictFailed {
		t.Fatalf("排隊失敗：%+v", task)
	}
}
