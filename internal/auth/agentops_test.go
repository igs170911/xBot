package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xbot/internal/ops"
)

func setupOps(t *testing.T) (*ops.Registry, *Pairing, *Authenticator) {
	t.Helper()
	p, _ := newPairing(t)
	reg := ops.New(ops.Config{DB: p.DB})
	ops.RegisterBuiltin(reg)
	RegisterAgentOps(reg, p)
	return reg, p, &Authenticator{DB: p.DB, AccessToken: "owner-secret"}
}

func pair(t *testing.T, reg *ops.Registry, p *Pairing, scopes ...string) PairResult {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"name": "Agent", "scopes": scopes})
	res, err := reg.Invoke(context.Background(), ops.Owner("cli"), "xbot.agents.pair_start", in, ops.Options{})
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

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func TestIdentifyOwnerAgentRevoked(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	id, err := a.Identify(ctx, "owner-secret", "mcp")
	if err != nil || id.Kind != ops.KindOwner {
		t.Fatalf("登入碼應該是本人：%v %v", id, err)
	}
	if _, err := a.Identify(ctx, "xbot_nope", "mcp"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("亂給的權杖應該無效：%v", err)
	}
	pr := pair(t, reg, p, "read")
	id, err = a.Identify(ctx, pr.Token, "mcp")
	if err != nil || id.Kind != ops.KindAgent || id.ID != pr.Agent.ID || !id.HasScope(ops.ScopeRead) || id.HasScope(ops.ScopeAdmin) {
		t.Fatalf("代理身分不對：%+v %v", id, err)
	}
	tok, _ := p.DB.GetAgentToken(ctx, pr.Agent.ID)
	if tok.LastUsedAt == 0 {
		t.Fatal("應該記錄最後使用時間")
	}
	in, _ := json.Marshal(map[string]string{"id": pr.Agent.ID})
	if _, err := reg.Invoke(ctx, ops.Owner("cli"), "xbot.agents.revoke", in, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Identify(ctx, pr.Token, "mcp"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("撤銷後應該無效：%v", err)
	}
}

func TestAgentOpsPermissions(t *testing.T) {
	reg, p, a := setupOps(t)
	ctx := context.Background()
	admin := pair(t, reg, p, "admin", "read")
	adminID, err := a.Identify(ctx, admin.Token, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	// 代理就算有 admin 也不能發新權杖或改權限（避免升權）。
	in, _ := json.Marshal(map[string]any{"scopes": []string{"admin"}})
	if _, err := reg.Invoke(ctx, adminID, "xbot.agents.pair_start", in, ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能產生配對碼：%v", err)
	}
	up, _ := json.Marshal(map[string]any{"id": admin.Agent.ID, "scopes": []string{"read", "operate", "manage", "admin"}})
	if _, err := reg.Invoke(ctx, adminID, "xbot.agents.update_scopes", up, ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能改權限：%v", err)
	}
	// 代理撤銷別人要先經過使用者核准（confirm）。
	other := pair(t, reg, p, "read")
	rv, _ := json.Marshal(map[string]string{"id": other.Agent.ID})
	if _, err := reg.Invoke(ctx, adminID, "xbot.agents.revoke", rv, ops.Options{}); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理撤銷應該送去待我處理：%v", err)
	}
	// 只有 read 的代理不能列出代理。
	reader, _ := a.Identify(ctx, other.Token, "mcp")
	if _, err := reg.Invoke(ctx, reader, "xbot.agents.list", nil, ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("read 代理不能列出代理：%v", err)
	}
	// 本人改權限、預演不寫入。
	down, _ := json.Marshal(map[string]any{"id": other.Agent.ID, "scopes": []string{"operate", "read", "read"}, "resources": map[string]any{"bots": []string{"b1"}}})
	res, err := reg.Invoke(ctx, ops.Owner("cli"), "xbot.agents.update_scopes", down, ops.Options{DryRun: true})
	if err != nil || !res.DryRun {
		t.Fatal(err)
	}
	if tok, _ := p.DB.GetAgentToken(ctx, other.Agent.ID); len(tok.Scopes) != 1 {
		t.Fatalf("預演不應該寫入：%v", tok.Scopes)
	}
	if _, err := reg.Invoke(ctx, ops.Owner("cli"), "xbot.agents.update_scopes", down, ops.Options{}); err != nil {
		t.Fatal(err)
	}
	id, _ := a.Identify(ctx, other.Token, "mcp")
	if !id.HasScope(ops.ScopeOperate) || !id.CanAccess(ops.ResourceBot, "b1") || id.CanAccess(ops.ResourceBot, "b2") {
		t.Fatalf("新權限沒生效：%+v", id)
	}
	// 列出：預設不含撤銷的；不會回傳權杖或雜湊（MCP-11）。
	res, err = reg.Invoke(ctx, ops.Owner("cli"), "xbot.agents.list", nil, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var list AgentsListOut
	_ = json.Unmarshal(res.Output, &list)
	if len(list.Agents) != 2 {
		t.Fatalf("應該有 2 個代理：%+v", list.Agents)
	}
	for _, tok := range []string{admin.Token, other.Token, HashToken(admin.Token)} {
		if strings.Contains(string(res.Output), tok) {
			t.Fatal("列表不能含權杖或雜湊")
		}
	}
	if _, err := reg.Invoke(ctx, ops.Owner("cli"), "xbot.agents.revoke", []byte(`{"id":"missing"}`), ops.Options{}); code(err) != ops.CodeNotFound {
		t.Fatalf("撤銷不存在的代理：%v", err)
	}
}
