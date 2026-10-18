package bots

import (
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// CL-1：xbot.bots.update 可以指定 Bot 放在哪台主機；電腦已經建在別台時要搬家，不能直接換。
func TestUpdateHost(t *testing.T) {
	e := setup(t)
	owner := ops.Owner("test")
	local, err := e.db.CreateHost(ctx, store.Host{Name: "這台電腦", Kind: "local"})
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := e.db.CreateHost(ctx, store.Host{Name: "cloud-01", Kind: "ssh", Address: "cloud-01", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	var v View
	e.must(t, owner, "xbot.bots.create", CreateIn{Name: "阿凱"}, &v)
	if v.HostID != "" {
		t.Fatalf("新 Bot 還沒擺放：%q", v.HostID)
	}
	cloudID := cloud.ID
	e.must(t, owner, "xbot.bots.update", UpdateIn{ID: v.ID, HostID: &cloudID}, &v)
	if v.HostID != cloud.ID {
		t.Fatalf("換主機：%q", v.HostID)
	}
	bad := "沒有這台"
	if _, err := e.call(t, owner, "xbot.bots.update", UpdateIn{ID: v.ID, HostID: &bad}); ops.AsError(err).Code != ops.CodeInvalidInput {
		t.Fatalf("不存在的主機：%v", err)
	}
	// 電腦列還在但沒有容器：一起改到新主機。
	if err := e.db.UpsertBox(ctx, store.Box{BotID: v.ID, HostID: cloud.ID, State: "none"}); err != nil {
		t.Fatal(err)
	}
	localID := local.ID
	e.must(t, owner, "xbot.bots.update", UpdateIn{ID: v.ID, HostID: &localID}, &v)
	if b, _ := e.db.GetBox(ctx, v.ID); b.HostID != local.ID || v.HostID != local.ID {
		t.Fatalf("電腦列應該跟著換：%+v %q", b, v.HostID)
	}
	// 電腦已經建好：換主機是搬家（CL-5），先不支援。
	if err := e.db.UpsertBox(ctx, store.Box{BotID: v.ID, HostID: local.ID, ContainerID: "c1", State: "sleeping"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.call(t, owner, "xbot.bots.update", UpdateIn{ID: v.ID, HostID: &cloudID}); ops.AsError(err).Code != ops.CodeConflict {
		t.Fatalf("已有電腦時換主機：%v", err)
	}
	// 清成空白＝自動擺放。
	empty := ""
	e.must(t, owner, "xbot.bots.update", UpdateIn{ID: v.ID, HostID: &empty}, &v)
	if v.HostID != "" {
		t.Fatalf("清空：%q", v.HostID)
	}
}
