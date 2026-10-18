package events

import "testing"

func TestBus(t *testing.T) {
	b := New()
	all := b.Subscribe(nil, 0)
	one := b.Subscribe(ForBot("b1"), 1)
	b.Publish(Event{Kind: "x", BotID: "b1"})
	b.Publish(Event{Kind: "y", BotID: "b2"})
	b.Publish(Event{Kind: "z", BotID: "b1"}) // one 的緩衝只有 1：丟掉

	if e := <-all.C; e.Kind != "x" {
		t.Fatalf("第一個：%+v", e)
	}
	if e := <-all.C; e.Kind != "y" {
		t.Fatalf("第二個：%+v", e)
	}
	if e := <-one.C; e.Kind != "x" {
		t.Fatalf("過濾：%+v", e)
	}
	if one.Dropped() != 1 {
		t.Fatalf("丟掉的數量：%d", one.Dropped())
	}
	one.Close()
	one.Close()
	if _, ok := <-one.C; ok {
		t.Fatal("Close 後應該關閉")
	}
	b.Publish(Event{Kind: "after", BotID: "b1"}) // 關掉的訂閱不會被寫入
	if e := <-all.C; e.Kind != "z" {
		t.Fatalf("all：%+v", e)
	}
	if ForTask("t")(Event{TaskID: "u"}) {
		t.Fatal("ForTask")
	}
}
