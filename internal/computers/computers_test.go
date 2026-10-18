package computers

import (
	"errors"
	"testing"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func TestOpsDefinitions(t *testing.T) {
	reg := ops.New(ops.Config{})
	Register(reg, nil)
	want := map[string]struct {
		scope      ops.Scope
		risk       ops.Risk
		noDelegate bool
	}{
		"xbot.computers.status":      {ops.ScopeRead, ops.RiskSafe, false},
		"xbot.computers.wake":        {ops.ScopeOperate, ops.RiskSafe, false},
		"xbot.computers.sleep":       {ops.ScopeOperate, ops.RiskSafe, false},
		"xbot.computers.restart":     {ops.ScopeOperate, ops.RiskSafe, false},
		"xbot.computers.rebuild":     {ops.ScopeManage, ops.RiskSafe, false}, // 保留資料，不用核准
		"xbot.computers.purge":       {ops.ScopeManage, ops.RiskConfirm, true},
		"xbot.computers.set_network": {ops.ScopeAdmin, ops.RiskConfirm, true},
		"xbot.computers.update":      {ops.ScopeAdmin, ops.RiskSafe, false},
	}
	for name, w := range want {
		info, ok := reg.Get(name)
		if !ok {
			t.Errorf("沒有 %s", name)
			continue
		}
		if info.Scope != w.scope || info.Risk != w.risk || info.NoDelegate != w.noDelegate || info.ForBot {
			t.Errorf("%s：scope=%s risk=%s noDelegate=%v forBot=%v", name, info.Scope, info.Risk, info.NoDelegate, info.ForBot)
		}
	}
	if info, _ := reg.Get("xbot.computers.set_network"); !info.DynamicRisk {
		t.Error("全部關掉白名單不用核准（RiskOf）")
	}
}

func TestOpErr(t *testing.T) {
	cases := map[error]ops.Code{
		store.ErrNotFound:                   ops.CodeNotFound,
		box.ErrBusy:                         ops.CodeConflict,
		ErrUpdateRunning:                    ops.CodeConflict,
		hosts.ErrHostOffline:                ops.CodeConflict,
		hosts.ErrUnknownHost:                ops.CodeConflict,
		&box.NetError{Err: errors.New("x")}: ops.CodeInternal,
	}
	for err, code := range cases {
		if got := ops.AsError(opErr(err)); got == nil || got.Code != code {
			t.Errorf("%v → %+v，想要 %s", err, got, code)
		}
	}
	if opErr(nil) != nil {
		t.Error("nil")
	}
}

func TestProgressClone(t *testing.T) {
	p := Progress{Hosts: []HostUpdate{{HostID: "h", Bots: []BotUpdate{{BotID: "b", State: StatePending}}}}}
	c := clone(p)
	c.Hosts[0].Bots[0].State = StateRebuilt
	c.Hosts[0].State = StateDone
	if p.Hosts[0].Bots[0].State != StatePending || p.Hosts[0].State != "" {
		t.Fatal("進度快照不能和進行中的共用")
	}
}
