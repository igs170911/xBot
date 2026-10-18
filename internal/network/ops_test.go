package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

func TestOps(t *testing.T) {
	s, _, db := newSvc(t)
	ctx := context.Background()
	reg := ops.New(ops.Config{DB: db})
	Register(reg, s)
	for _, name := range []string{"xbot.network.tailscale_set", "xbot.network.funnel_set"} {
		info, ok := reg.Get(name)
		if !ok || info.Scope != ops.ScopeAdmin || !info.NoDelegate || !info.DynamicRisk || info.Delegable() {
			t.Fatalf("%s 要 admin、開啟要核准、不能代為核准：%+v", name, info)
		}
	}
	call := func(id ops.Identity, name, in string) (Status, *ops.Error) {
		res, err := reg.Invoke(ctx, id, name, json.RawMessage(in), ops.Options{})
		var st Status
		if err == nil {
			_ = json.Unmarshal(res.Output, &st)
		}
		return st, ops.AsError(err)
	}
	owner := ops.Owner("ui")
	// 內建 Tailscale 沒開，不能開 Funnel。
	if _, e := call(owner, "xbot.network.funnel_set", `{"enabled":true}`); e == nil || e.Code != ops.CodeConflict {
		t.Fatalf("要先開 Tailscale：%v", e)
	}
	if _, e := call(owner, "xbot.network.tailscale_set", `{"enabled":true,"hostname":"Bad_Name"}`); e == nil || e.Code != ops.CodeInvalidInput {
		t.Fatalf("名字格式：%v", e)
	}
	if _, e := call(owner, "xbot.network.funnel_set", `{"enabled":false,"allow_ips":["nope"]}`); e == nil || e.Code != ops.CodeInvalidInput {
		t.Fatalf("白名單格式：%v", e)
	}
	st, e := call(owner, "xbot.network.tailscale_set", `{"enabled":true,"auth_key":"tskey-auth-1"}`)
	if e != nil || !st.Tailscale.Enabled || !st.Tailscale.HasAuthKey {
		t.Fatalf("開啟：%+v %v", st.Tailscale, e)
	}
	// 稽核裡的登入金鑰要遮蔽。
	rows, err := db.ListAudit(ctx, store.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rows {
		if strings.Contains(string(a.Params), "tskey") {
			t.Fatalf("稽核不能有金鑰：%s", a.Params)
		}
	}
	// 代理（admin）開 Funnel：送到待我處理，不會直接執行。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "代理", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeAdmin}}
	if _, e := call(agent, "xbot.network.funnel_set", `{"enabled":true}`); e == nil || e.Code != ops.CodePendingApproval {
		t.Fatalf("代理開 Funnel 要等你核准：%v", e)
	}
	if s.settings().Funnel {
		t.Fatal("還沒核准不能開")
	}
	// 關掉不用核准。
	if _, e := call(agent, "xbot.network.funnel_set", `{"enabled":false}`); e != nil {
		t.Fatalf("關閉不用核准：%v", e)
	}
	// 狀態：read 就能看。
	reader := ops.Identity{Kind: ops.KindAgent, ID: "a2", Scopes: []ops.Scope{ops.ScopeRead}}
	if _, e := call(reader, "xbot.network.status", `{}`); e != nil {
		t.Fatal(e)
	}
}
