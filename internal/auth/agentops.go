package auth

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"xbot/internal/delegation"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// RegisterAgentOps 登錄「已連線的代理」相關操作（MCP-6）：pair_start、list、revoke、restore、update_scopes。
// 代為核准授權（set_delegation、get_delegation）在 internal/delegation。
// 換權杖（pair_complete）不是操作：代理那時還沒有身分，走 POST /api/v1/agents.pair（Pairing.Handler）。
func RegisterAgentOps(r *ops.Registry, p *Pairing) {
	ops.Register(r, ops.Op[PairStartIn, PairStartOut]{
		Name:    "xbot.agents.pair_start",
		Summary: "產生一個配對碼，讓外部代理換到權杖",
		Description: "配對碼一次性、5 分鐘內有效。把配對碼交給代理（Claude Code、Codex…），" +
			"代理呼叫 POST " + PairPath + " 換到權杖，之後用 Authorization: Bearer <權杖> 連 /mcp。只有你本人能產生配對碼。",
		Scope: ops.ScopeAdmin,
		Risk:  ops.RiskSafe,
		Handle: func(_ context.Context, call *ops.Call, in PairStartIn) (PairStartOut, error) {
			if err := ownerOnly(call, "產生配對碼"); err != nil {
				return PairStartOut{}, err
			}
			res, err := resolveResources(in.ResourceScope, in.Resources, true)
			if err != nil {
				return PairStartOut{}, err
			}
			out := PairStartOut{Name: in.Name, Scopes: dedupScopes(in.Scopes), Resources: res, ResourceScope: scopeOf(res),
				TokenDays: in.TokenDays, DailyCalls: in.DailyCalls, DailySpendMicros: in.DailySpendMicros, PairPath: PairPath}
			if call.DryRun {
				out.ExpiresAt = time.Now().Add(limits.PairCodeTTL).UnixMilli()
				return out, nil
			}
			code, exp, err := p.Start(PairGrant{Name: in.Name, Scopes: out.Scopes, Resources: res, TokenDays: in.TokenDays,
				DailyCalls: in.DailyCalls, DailySpendMicros: in.DailySpendMicros})
			if err != nil {
				return PairStartOut{}, err
			}
			out.Code, out.ExpiresAt = code, exp.UnixMilli()
			return out, nil
		},
	})
	ops.Register(r, ops.Op[AgentsListIn, AgentsListOut]{
		Name:    "xbot.agents.list",
		Summary: "列出已連線的代理",
		Scope:   ops.ScopeAdmin,
		Risk:    ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in AgentsListIn) (AgentsListOut, error) {
			ts, err := call.DB.ListAgentTokens(ctx)
			if err != nil {
				return AgentsListOut{}, err
			}
			out := AgentsListOut{Agents: []AgentView{}}
			now := time.Now().UnixMilli()
			for _, t := range ts {
				v := viewOf(t, now)
				if v.Status != "active" && !in.IncludeRevoked {
					continue
				}
				if err := withUsage(ctx, call.DB, &v); err != nil {
					return out, err
				}
				out.Agents = append(out.Agents, v)
			}
			return out, nil
		},
	})
	ops.Register(r, ops.Op[AgentIDIn, AgentView]{
		Name:        "xbot.agents.revoke",
		Summary:     "撤銷一個代理的權杖",
		Description: "撤銷後這個代理的下一個請求就會被拒絕；10 分鐘內你可以復原（xbot.agents.restore），之後要重新配對才能再連線。",
		Scope:       ops.ScopeAdmin,
		Risk:        ops.RiskConfirm,
		NoDelegate:  true, // 改代理的權限：永遠要你本人
		Handle: func(ctx context.Context, call *ops.Call, in AgentIDIn) (AgentView, error) {
			if !call.DryRun {
				if err := call.DB.RevokeAgentToken(ctx, in.ID); err != nil {
					return AgentView{}, notFoundAgent(err)
				}
			}
			t, err := call.DB.GetAgentToken(ctx, in.ID)
			if err != nil {
				return AgentView{}, notFoundAgent(err)
			}
			v := viewOf(t, time.Now().UnixMilli())
			if call.DryRun {
				v.Status = "revoked"
			}
			return v, nil
		},
	})
	ops.Register(r, ops.Op[AgentIDIn, AgentView]{
		Name:        "xbot.agents.restore",
		Summary:     "復原剛撤銷的代理",
		Description: "撤銷後 10 分鐘內可以復原，權杖恢復原本的權限。超過就要重新配對。只有你本人能復原。",
		Scope:       ops.ScopeAdmin,
		Risk:        ops.RiskSafe,
		NoDelegate:  true,
		Handle: func(ctx context.Context, call *ops.Call, in AgentIDIn) (AgentView, error) {
			if err := ownerOnly(call, "復原代理"); err != nil {
				return AgentView{}, err
			}
			t, err := call.DB.GetAgentToken(ctx, in.ID)
			if err != nil {
				return AgentView{}, notFoundAgent(err)
			}
			now := timeNow()
			if t.RevokedAt == 0 {
				return AgentView{}, ops.Conflict("這個代理沒有被撤銷")
			}
			if now.UnixMilli()-t.RevokedAt > limits.AgentRestoreWindow.Milliseconds() {
				return AgentView{}, ops.Conflict("撤銷已經超過 10 分鐘，不能復原了；請重新配對")
			}
			if !call.DryRun {
				if err := call.DB.RestoreAgentToken(ctx, in.ID); err != nil {
					return AgentView{}, notFoundAgent(err)
				}
			}
			t.RevokedAt = 0
			v := viewOf(t, now.UnixMilli())
			return v, withUsage(ctx, call.DB, &v)
		},
	})
	ops.Register(r, ops.Op[UpdateScopesIn, AgentView]{
		Name:    "xbot.agents.update_scopes",
		Summary: "改一個代理的權限範圍、資源範圍與上限",
		Description: "scopes 整組取代原本的權限；resource_scope／resources 有給才改資源範圍；daily_calls、daily_spend_micros 有給才改上限。" +
			"只有你本人能改。",
		Scope:      ops.ScopeAdmin,
		Risk:       ops.RiskSafe,
		NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateScopesIn) (AgentView, error) {
			if err := ownerOnly(call, "改代理的權限"); err != nil {
				return AgentView{}, err
			}
			t, err := call.DB.GetAgentToken(ctx, in.ID)
			if err != nil {
				return AgentView{}, notFoundAgent(err)
			}
			scopes := dedupScopes(in.Scopes)
			var res json.RawMessage
			if in.Resources != nil || in.ResourceScope != "" {
				r, err := resolveResources(in.ResourceScope, in.Resources, false)
				if err != nil {
					return AgentView{}, err
				}
				if res, err = json.Marshal(r); err != nil {
					return AgentView{}, err
				}
			}
			if in.DailyCalls != nil {
				t.DailyCalls = *in.DailyCalls
			}
			if in.DailySpendMicros != nil {
				t.DailySpendMicros = *in.DailySpendMicros
			}
			if call.DryRun {
				t.Scopes = toStrings(scopes)
				if res != nil {
					t.Resources = res
				}
				return viewOf(t, time.Now().UnixMilli()), nil
			}
			err = call.DB.Tx(ctx, func(q *store.Queries) error {
				if err := q.UpdateAgentTokenScopes(ctx, in.ID, toStrings(scopes), res); err != nil {
					return err
				}
				return q.SetAgentTokenLimits(ctx, in.ID, t.RatePerMin, t.DailyCalls, t.DailySpendMicros)
			})
			if err != nil {
				return AgentView{}, notFoundAgent(err)
			}
			t, err = call.DB.GetAgentToken(ctx, in.ID)
			if err != nil {
				return AgentView{}, err
			}
			v := viewOf(t, time.Now().UnixMilli())
			return v, withUsage(ctx, call.DB, &v)
		},
	})
}

// timeNow 是現在時間（測試可以換，檢查復原時限）。
var timeNow = time.Now

// ownerOnly：發權杖、改權限只有使用者本人能做，避免代理幫自己或別人升權。
func ownerOnly(call *ops.Call, what string) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能" + what)
	}
	return nil
}

func notFoundAgent(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return ops.NotFound("找不到這個代理")
	}
	return err
}

func dedupScopes(in []ops.Scope) []ops.Scope {
	var out []ops.Scope
	for _, s := range ops.Scopes { // 依權限由小到大排好
		if slices.Contains(in, s) {
			out = append(out, s)
		}
	}
	return out
}

func toStrings(in []ops.Scope) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = string(s)
	}
	return out
}

// 資源範圍的三種設定（MCP-8）。
const (
	ResourceScopeOwn    = "own"    // 只能碰自己建立的（預設）
	ResourceScopeAll    = "all"    // 全部
	ResourceScopeCustom = "custom" // 指定的 Bot／團隊／主機，加上自己建立的
)

// resolveResources 把 resource_scope＋resources 換成要存的資源範圍。
//   - own：三種都是空陣列，只能碰自己建立的（bot.created_by，見 ops.Registry.withOwned）。
//   - all：都不限制。
//   - custom：照 resources，沒給的種類當成空陣列（同樣只能碰自己建立的）。
//   - 空白：給了 resources 就照原樣（舊的用法，沒給的種類不限制）；都沒給時配對預設 own（isNew），修改時不變（回 nil）。
func resolveResources(scope string, res *ops.Resources, isNew bool) (*ops.Resources, error) {
	own := &ops.Resources{Bots: []string{}, Teams: []string{}, Hosts: []string{}}
	switch scope {
	case ResourceScopeOwn:
		return own, nil
	case ResourceScopeAll:
		return &ops.Resources{}, nil
	case ResourceScopeCustom:
		if res == nil {
			return own, nil
		}
		c := *res
		for _, l := range []*[]string{&c.Bots, &c.Teams, &c.Hosts} {
			if *l == nil {
				*l = []string{}
			}
		}
		return &c, nil
	case "":
		if res != nil {
			return res, nil
		}
		if isNew {
			return own, nil
		}
		return nil, nil
	}
	return nil, ops.InvalidInput(ops.FieldError{Field: "resource_scope", Message: "要是 own、all 或 custom"})
}

// scopeOf 從存好的資源範圍反推畫面上的選項。
func scopeOf(r *ops.Resources) string {
	if r == nil || (r.Bots == nil && r.Teams == nil && r.Hosts == nil) {
		return ResourceScopeAll
	}
	if len(r.Bots) == 0 && len(r.Teams) == 0 && len(r.Hosts) == 0 && r.Bots != nil && r.Teams != nil && r.Hosts != nil {
		return ResourceScopeOwn
	}
	return ResourceScopeCustom
}

// withUsage 補上今天的呼叫數、花費與代為核准授權（agents.list、J12 每一列）。
func withUsage(ctx context.Context, db *store.DB, v *AgentView) error {
	now := time.Now()
	day := store.DayStart(now)
	var err error
	if v.CallsToday, err = db.CountAuditSince(ctx, "agent:"+v.ID, day); err != nil {
		return err
	}
	if v.SpendTodayMicros, err = db.AgentSpendSince(ctx, "agent:"+v.ID, day); err != nil {
		return err
	}
	v.Delegation, err = delegation.Get(ctx, db, v.ID)
	return err
}

func viewOf(t store.AgentToken, now int64) AgentView {
	v := AgentView{ID: t.ID, Name: t.Name, LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt, Status: "active",
		RatePerMin: t.RatePerMin, DailyCalls: t.DailyCalls, DailySpendMicros: t.DailySpendMicros}
	if v.RatePerMin == 0 {
		v.RatePerMin = limits.AgentRatePerMin
	}
	for _, s := range t.Scopes {
		v.Scopes = append(v.Scopes, ops.Scope(s))
	}
	if v.Scopes == nil {
		v.Scopes = []ops.Scope{}
	}
	if len(t.Resources) > 0 {
		_ = json.Unmarshal(t.Resources, &v.Resources)
	}
	v.ResourceScope = scopeOf(&v.Resources)
	v.Delegation = delegation.View{AgentID: t.ID, Categories: []ops.Category{}, Never: delegation.Never}
	switch {
	case t.RevokedAt != 0:
		v.Status = "revoked"
	case t.ExpiresAt != 0 && t.ExpiresAt <= now:
		v.Status = "expired"
	}
	return v
}

// PairStartIn 是 xbot.agents.pair_start 的輸入。
type PairStartIn struct {
	Name   string      `json:"name,omitempty" desc:"代理的名字，例：Claude Code；空白時用代理換權杖時報的名字" max:"100"`
	Scopes []ops.Scope `json:"scopes" required:"true" min:"1" desc:"給這個代理的權限範圍（可疊加）：read、operate、manage、admin"`
	// ResourceScope 決定能碰哪些東西（MCP-8）；不填時預設 own（只能碰自己建立的）。
	ResourceScope string         `json:"resource_scope,omitempty" enum:"own,all,custom" desc:"own：只能碰自己建立的（預設）；all：全部；custom：resources 指定的，加上自己建立的"`
	Resources     *ops.Resources `json:"resources,omitempty" desc:"resource_scope=custom 時指定哪些 Bot／團隊／主機"`
	TokenDays     int            `json:"token_days,omitempty" min:"0" max:"3650" desc:"權杖有效天數；0 或不填表示不過期（可隨時撤銷）"`
	// 上限（MCP-12）；0 或不填表示不另外限制（每分鐘呼叫一律有預設上限）。
	DailyCalls       int   `json:"daily_calls,omitempty" min:"0" max:"100000" desc:"每天最多呼叫幾次；0 表示不限制"`
	DailySpendMicros int64 `json:"daily_spend_micros,omitempty" min:"0" max:"1000000000" desc:"它交辦的工作每天最多花多少（美元百萬分之一）；0 表示不另外限制"`
}

// PairStartOut 是 xbot.agents.pair_start 的輸出。
type PairStartOut struct {
	Code             string         `json:"code,omitempty" desc:"一次性配對碼（XXXX-XXXX）；預演時是空白"`
	ExpiresAt        int64          `json:"expires_at" desc:"配對碼到期時間（Unix 毫秒）"`
	PairPath         string         `json:"pair_path" desc:"代理換權杖的端點：POST {code, name}"`
	Name             string         `json:"name,omitempty"`
	Scopes           []ops.Scope    `json:"scopes"`
	ResourceScope    string         `json:"resource_scope" enum:"own,all,custom"`
	Resources        *ops.Resources `json:"resources,omitempty"`
	TokenDays        int            `json:"token_days,omitempty"`
	DailyCalls       int            `json:"daily_calls,omitempty"`
	DailySpendMicros int64          `json:"daily_spend_micros,omitempty"`
}

// AgentsListIn 是 xbot.agents.list 的輸入。
type AgentsListIn struct {
	IncludeRevoked bool `json:"include_revoked,omitempty" desc:"也列出已撤銷、已過期的代理"`
}

// AgentsListOut 是 xbot.agents.list 的輸出。
type AgentsListOut struct {
	Agents []AgentView `json:"agents"`
}

// AgentView 是一個已連線的代理（不含權杖，MCP-11）。
type AgentView struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Status    string        `json:"status" enum:"active,revoked,expired"`
	Scopes    []ops.Scope   `json:"scopes"`
	Resources ops.Resources `json:"resources"`
	// ResourceScope：own（只能碰自己建立的）、all（全部）、custom（resources 指定的＋自己建立的）。
	ResourceScope string `json:"resource_scope" enum:"own,all,custom"`
	LastUsedAt    int64  `json:"last_used_at" desc:"最後使用時間（Unix 毫秒；0 表示還沒用過）"`
	ExpiresAt     int64  `json:"expires_at" desc:"0 表示不過期"`
	RevokedAt     int64  `json:"revoked_at" desc:"0 表示有效"`
	CreatedAt     int64  `json:"created_at"`
	// 上限與今天的用量（MCP-12）。
	RatePerMin       int   `json:"rate_per_min" desc:"每分鐘最多呼叫幾次"`
	DailyCalls       int   `json:"daily_calls" desc:"每天最多呼叫幾次；0 表示不限制"`
	DailySpendMicros int64 `json:"daily_spend_micros" desc:"每天最多花多少（美元百萬分之一）；0 表示不另外限制"`
	CallsToday       int   `json:"calls_today"`
	SpendTodayMicros int64 `json:"spend_today_micros"`
	// Delegation 是代為核准授權（MCP-10）；enabled=false 表示沒有授權。
	Delegation delegation.View `json:"delegation"`
}

// AgentIDIn 指定一個代理。
type AgentIDIn struct {
	ID string `json:"id" required:"true" desc:"代理 id（xbot.agents.list 的 id）" max:"64"`
}

// UpdateScopesIn 是 xbot.agents.update_scopes 的輸入。
type UpdateScopesIn struct {
	ID            string         `json:"id" required:"true" desc:"代理 id" max:"64"`
	Scopes        []ops.Scope    `json:"scopes" required:"true" desc:"新的權限範圍（整組取代）；空陣列表示什麼都不能做"`
	ResourceScope string         `json:"resource_scope,omitempty" enum:"own,all,custom" desc:"own：只能碰自己建立的；all：全部；custom：resources 指定的＋自己建立的；不填表示不變（除非給了 resources）"`
	Resources     *ops.Resources `json:"resources,omitempty" desc:"新的資源範圍；不填表示不變"`
	// 上限（MCP-12）；不填表示不變，0 表示不限制。
	DailyCalls       *int   `json:"daily_calls,omitempty" min:"0" max:"100000" desc:"每天最多呼叫幾次；0 表示不限制"`
	DailySpendMicros *int64 `json:"daily_spend_micros,omitempty" min:"0" max:"1000000000" desc:"每天最多花多少（美元百萬分之一）；0 表示不另外限制"`
}
