package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// C21：配對預設資源範圍（MCP-8）、代理上限（MCP-12）、撤銷可復原。

func pairWith(t *testing.T, reg *ops.Registry, p *Pairing, in map[string]any) PairResult {
	t.Helper()
	if _, ok := in["scopes"]; !ok {
		in["scopes"] = []string{"read", "operate", "manage"}
	}
	raw, _ := json.Marshal(in)
	res, err := reg.Invoke(context.Background(), ops.Owner("cli"), "xbot.agents.pair_start", raw, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out PairStartOut
	_ = json.Unmarshal(res.Output, &out)
	pr, e := p.Complete(context.Background(), out.Code, "", "t")
	if e != nil {
		t.Fatal(e)
	}
	return pr
}

func TestPairDefaultsToOwnResources(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	// 沒指定：只能碰自己建立的（三種都是空陣列）。
	pr := pairWith(t, reg, p, map[string]any{})
	id, err := a.Identify(ctx, pr.Token, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if id.Resources.Bots == nil || len(id.Resources.Bots) != 0 || id.Resources.Hosts == nil || id.Resources.Teams == nil {
		t.Fatalf("預設只能碰自己建立的：%+v", id.Resources)
	}
	b, err := p.DB.CreateBot(ctx, store.Bot{Name: "別人的"})
	if err != nil {
		t.Fatal(err)
	}
	if id.CanAccess(ops.ResourceBot, b.ID) {
		t.Fatal("不能碰別人建立的 Bot")
	}
	// 明確給 all：不限制。
	all := pairWith(t, reg, p, map[string]any{"resource_scope": "all"})
	aid, _ := a.Identify(ctx, all.Token, "mcp")
	if !aid.CanAccess(ops.ResourceBot, b.ID) {
		t.Fatalf("all 要能碰全部：%+v", aid.Resources)
	}
	// custom：指定的 Bot，沒給的種類只能碰自己建立的。
	cu := pairWith(t, reg, p, map[string]any{"resource_scope": "custom", "resources": map[string]any{"bots": []string{b.ID}}})
	cid, _ := a.Identify(ctx, cu.Token, "mcp")
	if !cid.CanAccess(ops.ResourceBot, b.ID) || cid.Resources.Hosts == nil || len(cid.Resources.Hosts) != 0 {
		t.Fatalf("custom：%+v", cid.Resources)
	}
	// 清單顯示三種設定。
	var list AgentsListOut
	res, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.list", nil, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(res.Output, &list)
	got := map[string]string{}
	for _, v := range list.Agents {
		got[v.ID] = v.ResourceScope
		if v.RatePerMin != limits.AgentRatePerMin || v.Delegation.Enabled || v.Delegation.Never == nil {
			t.Fatalf("清單：%+v", v)
		}
	}
	if got[pr.Agent.ID] != "own" || got[all.Agent.ID] != "all" || got[cu.Agent.ID] != "custom" {
		t.Fatalf("資源範圍：%v", got)
	}
	// 你可以放寬成全部。
	upd, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.update_scopes",
		json.RawMessage(`{"id":"`+pr.Agent.ID+`","scopes":["read"],"resource_scope":"all","daily_calls":200}`), ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var v AgentView
	_ = json.Unmarshal(upd.Output, &v)
	if v.ResourceScope != "all" || v.DailyCalls != 200 || len(v.Scopes) != 1 {
		t.Fatalf("放寬：%+v", v)
	}
}

func TestRevokeAndRestore(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	pr := pairWith(t, reg, p, map[string]any{})
	in := json.RawMessage(`{"id":"` + pr.Agent.ID + `"}`)
	if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.restore", in, ops.Options{}); code(err) != ops.CodeConflict {
		t.Fatalf("沒撤銷不能復原：%v", err)
	}
	if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.revoke", in, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Identify(ctx, pr.Token, "mcp"); err == nil {
		t.Fatal("撤銷後立刻失效")
	}
	// 代理不能幫自己復原。
	agentID := ops.Identity{Kind: ops.KindAgent, ID: pr.Agent.ID, Scopes: []ops.Scope{ops.ScopeAdmin}}
	if _, err := reg.Invoke(ctx, agentID, "xbot.agents.restore", in, ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能復原：%v", err)
	}
	if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.restore", in, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Identify(ctx, pr.Token, "mcp"); err != nil {
		t.Fatalf("復原後可以用：%v", err)
	}
	// 撤銷超過 10 分鐘：不能復原。
	if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.revoke", in, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	timeNow = func() time.Time { return time.Now().Add(11 * time.Minute) }
	t.Cleanup(func() { timeNow = time.Now })
	if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.restore", in, ops.Options{}); code(err) != ops.CodeConflict {
		t.Fatalf("超過 10 分鐘：%v", err)
	}
	for _, name := range []string{"xbot.agents.revoke", "xbot.agents.restore", "xbot.agents.update_scopes"} {
		if info, _ := reg.Get(name); !info.NoDelegate {
			t.Errorf("%s 要永遠由你本人決定", name)
		}
	}
}

func TestLimiterRate(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	now := time.Now()
	l := &Limiter{DB: p.DB, Now: func() time.Time { return now }}
	reg.SetLimiter(l.Check)
	pr := pairWith(t, reg, p, map[string]any{})
	if err := p.DB.SetAgentTokenLimits(ctx, pr.Agent.ID, 3, 0, 0); err != nil {
		t.Fatal(err)
	}
	id, _ := a.Identify(ctx, pr.Token, "mcp")
	for i := range 3 {
		if _, err := reg.Invoke(ctx, id, "xbot.system.ping", nil, ops.Options{}); err != nil {
			t.Fatalf("第 %d 次：%v", i+1, err)
		}
	}
	_, err := reg.Invoke(ctx, id, "xbot.system.ping", nil, ops.Options{})
	oe := ops.AsError(err)
	if oe == nil || oe.Code != ops.CodeLimit || !oe.Retryable || !strings.Contains(oe.Message, "每分鐘最多 3 次") {
		t.Fatalf("第 4 次要被擋：%v", err)
	}
	if HTTPStatus(oe.Code) != 429 {
		t.Fatalf("HTTP 429：%d", HTTPStatus(oe.Code))
	}
	// 你本人不受限。
	for range 5 {
		if _, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.system.ping", nil, ops.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	// 一分鐘後恢復。
	now = now.Add(61 * time.Second)
	if _, err := reg.Invoke(ctx, id, "xbot.system.ping", nil, ops.Options{}); err != nil {
		t.Fatalf("一分鐘後：%v", err)
	}
}

func TestLimiterDailyCallsAndSpend(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	reg.SetLimiter(NewLimiter(p.DB).Check)
	pr := pairWith(t, reg, p, map[string]any{"daily_calls": 2, "daily_spend_micros": 1_000_000})
	id, _ := a.Identify(ctx, pr.Token, "mcp")
	for range 2 {
		if _, err := reg.Invoke(ctx, id, "xbot.system.ping", nil, ops.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.Invoke(ctx, id, "xbot.system.ping", nil, ops.Options{}); code(err) != ops.CodeLimit {
		t.Fatalf("每日呼叫上限：%v", err)
	}
	// 被擋下的不算今天的呼叫數。
	if n, _ := p.DB.CountAuditSince(ctx, "agent:"+pr.Agent.ID, store.DayStart(time.Now())); n != 2 {
		t.Fatalf("今天 %d 次", n)
	}

	// 每日花費：它交辦的工作今天花了 US$1，會做事的操作被擋，查看的不擋。
	pr2 := pairWith(t, reg, p, map[string]any{"daily_spend_micros": 1_000_000, "scopes": []string{"read", "admin"}})
	id2, _ := a.Identify(ctx, pr2.Token, "mcp")
	b, _ := p.DB.CreateBot(ctx, store.Bot{Name: "b"})
	task, err := p.DB.CreateTask(ctx, store.Task{BotID: b.ID, Source: "owner", Meta: json.RawMessage(`{"actor":"agent:` + pr2.Agent.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.AddUsage(ctx, store.UsageEntry{BotID: b.ID, TaskID: task.ID, CostMicros: 1_000_000}); err != nil {
		t.Fatal(err)
	}
	if spent, _ := p.DB.AgentSpendSince(ctx, "agent:"+pr2.Agent.ID, 0); spent != 1_000_000 {
		t.Fatalf("花費：%d", spent)
	}
	if _, err := reg.Invoke(ctx, id2, "xbot.system.ping", nil, ops.Options{}); err != nil {
		t.Fatalf("查看的不擋：%v", err)
	}
	_, err = reg.Invoke(ctx, id2, "xbot.agents.pair_start", json.RawMessage(`{"scopes":["read"]}`), ops.Options{})
	if oe := ops.AsError(err); oe == nil || oe.Code != ops.CodeLimit || !strings.Contains(oe.Message, "每日花費上限") {
		t.Fatalf("超過每日花費：%v", err)
	}
	// 清單顯示今天的呼叫與花費。
	res, _ := reg.Invoke(ctx, ops.Owner("ui"), "xbot.agents.list", nil, ops.Options{})
	var list AgentsListOut
	_ = json.Unmarshal(res.Output, &list)
	for _, v := range list.Agents {
		if v.ID == pr2.Agent.ID && (v.SpendTodayMicros != 1_000_000 || v.CallsToday < 1 || v.DailySpendMicros != 1_000_000) {
			t.Fatalf("今天的用量：%+v", v)
		}
	}
}
