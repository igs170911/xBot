package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOpRequestLifecycle(t *testing.T) {
	db := openTest(t)
	r, claimed, err := db.ClaimOpRequest(ctx, "owner", "r1", "xbot.bots.create", "h1", 60_000)
	if err != nil || !claimed || r.Status != "running" {
		t.Fatalf("第一次要佔到：%+v %v %v", r, claimed, err)
	}
	r, claimed, err = db.ClaimOpRequest(ctx, "owner", "r1", "xbot.bots.create", "h1", 60_000)
	if err != nil || claimed || r.Status != "running" {
		t.Fatalf("處理中不能再佔：%+v %v %v", r, claimed, err)
	}
	// 不同身分同一個 request_id 互不影響。
	if _, c, _ := db.ClaimOpRequest(ctx, "agent:t1", "r1", "xbot.bots.create", "h1", 60_000); !c {
		t.Fatal("不同身分要能佔")
	}
	check(t, db.FinishOpRequest(ctx, "owner", "r1", "ok", json.RawMessage(`{"id":"b1"}`), ""))
	got := must[OpRequest](t)(db.GetOpRequest(ctx, "owner", "r1"))
	if got.Status != "ok" || string(got.Result) != `{"id":"b1"}` {
		t.Fatalf("完成後：%+v", got)
	}
	if _, c, _ := db.ClaimOpRequest(ctx, "owner", "r1", "xbot.bots.create", "h1", 0); c {
		t.Fatal("已完成的不能被接手")
	}
	if err := db.FinishOpRequest(ctx, "owner", "r1", "running", nil, ""); err == nil {
		t.Fatal("只能記 ok 或 pending_approval")
	}
	if err := db.FinishOpRequest(ctx, "owner", "沒有", "ok", nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在：%v", err)
	}
	check(t, db.ReleaseOpRequest(ctx, "owner", "r1"))
	if _, err := db.GetOpRequest(ctx, "owner", "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("釋放後要不見：%v", err)
	}
}

func TestOpRequestStaleTakeover(t *testing.T) {
	db := openTest(t)
	old := nowMS
	t.Cleanup(func() { nowMS = old })
	nowMS = func() int64 { return 1_000 }
	if _, c, err := db.ClaimOpRequest(ctx, "owner", "r1", "op", "h", 500); err != nil || !c {
		t.Fatal(err)
	}
	nowMS = func() int64 { return 1_400 }
	if _, c, _ := db.ClaimOpRequest(ctx, "owner", "r1", "op", "h", 500); c {
		t.Fatal("還沒過期不能接手")
	}
	nowMS = func() int64 { return 2_000 }
	if _, c, _ := db.ClaimOpRequest(ctx, "owner", "r1", "op", "other", 500); c {
		t.Fatal("內容不同不能接手")
	}
	if r, c, _ := db.ClaimOpRequest(ctx, "owner", "r1", "op", "h", 500); !c || r.UpdatedAt != 2_000 {
		t.Fatalf("過期後要能接手：%+v", r)
	}
}

func TestOpRequestApproval(t *testing.T) {
	db := openTest(t)
	a := must[Approval](t)(db.CreateApproval(ctx, Approval{Action: "xbot.bots.delete", Input: json.RawMessage(`{"key":"secret"}`)}))
	got := must[Approval](t)(db.GetApproval(ctx, a.ID))
	if string(got.Input) != `{"key":"secret"}` || string(got.Payload) != "{}" {
		t.Fatalf("原始輸入要存起來：%+v", got)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "secret") {
		t.Fatalf("原始輸入不能出現在 JSON：%s", b)
	}

	if _, _, err := db.StartApprovedOpRequest(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有紀錄：%v", err)
	}
	if _, _, err := db.ClaimOpRequest(ctx, "agent:t1", "approval:"+a.ID, "xbot.bots.delete", "h", 1000); err != nil {
		t.Fatal(err)
	}
	check(t, db.FinishOpRequest(ctx, "agent:t1", "approval:"+a.ID, "pending_approval", nil, a.ID))
	if r := must[OpRequest](t)(db.GetOpRequestByApproval(ctx, a.ID)); r.Status != "pending_approval" {
		t.Fatalf("等待中：%+v", r)
	}
	r, started, err := db.StartApprovedOpRequest(ctx, a.ID)
	if err != nil || !started || r.Status != "running" {
		t.Fatalf("開始執行：%+v %v %v", r, started, err)
	}
	if _, started, _ := db.StartApprovedOpRequest(ctx, a.ID); started {
		t.Fatal("不能開始兩次")
	}
	if _, err := db.GetOpRequestByApproval(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("空白的核准 id 不能找到東西")
	}
}

func TestReadOnly(t *testing.T) {
	db := openTest(t)
	ro := db.ReadOnly()
	check(t, db.SetSetting(ctx, "a", 1))
	if _, err := ro.GetSetting(ctx, "a"); err != nil {
		t.Fatalf("唯讀版本要能讀：%v", err)
	}
	if err := ro.SetSetting(ctx, "b", 1); err == nil {
		t.Fatal("唯讀版本不能寫")
	}
	if err := ro.Tx(ctx, func(q *Queries) error { return q.SetSetting(ctx, "c", 1) }); err == nil {
		t.Fatal("唯讀版本的交易不能寫")
	}
	check(t, ro.Close())
	check(t, db.SetSetting(ctx, "d", 1)) // 關掉唯讀版本不影響原本的
}
