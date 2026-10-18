package ops

import (
	"encoding/json"
	"fmt"
	"slices"

	"xbot/internal/store"
)

// Scope 是操作需要的權限範圍（MCP-7）。範圍是「可疊加」的集合，彼此不互相包含：
// 例如只有 manage 的代理不能呼叫 read 操作。
type Scope string

const (
	ScopeRead    Scope = "read"    // 看
	ScopeOperate Scope = "operate" // 交辦工作、開對話串、定時任務、檔案
	ScopeManage  Scope = "manage"  // 建立／修改 Bot、團隊、群組、技能、記憶
	ScopeAdmin   Scope = "admin"   // 主機、連接應用程式、模型金鑰設定、成員
)

// Scopes 是所有權限範圍，依權限由小到大。
var Scopes = []Scope{ScopeRead, ScopeOperate, ScopeManage, ScopeAdmin}

// EnumValues 讓 JSON Schema 列出所有可用值。
func (Scope) EnumValues() []string { return []string{"read", "operate", "manage", "admin"} }

// Risk 是操作的風險等級（MCP-9）。
type Risk string

const (
	RiskSafe    Risk = "safe"    // 直接做
	RiskConfirm Risk = "confirm" // 要使用者核准才做
)

// EnumValues 讓 JSON Schema 列出所有可用值。
func (Risk) EnumValues() []string { return []string{"safe", "confirm"} }

// Kind 是呼叫者的種類。
type Kind string

const (
	KindOwner Kind = "owner" // 使用者本人（網頁介面、本機 CLI）
	KindAgent Kind = "agent" // 外部代理，用 agent_token（MCP-6）
	KindBot   Kind = "bot"   // Bot 透過 product.sock 呼叫
)

// 資源種類（resource 標籤的值，MCP-8）。
const (
	ResourceBot  = "bot"
	ResourceTeam = "team"
	ResourceHost = "host"
)

// Resources 限制一個身分能碰哪些東西（MCP-8）。某個種類是 nil 表示不限制；空陣列表示一個都不能碰。
// 存在 agent_token.resources，例：{"bots":["…"]}。
type Resources struct {
	Bots  []string `json:"bots"`
	Teams []string `json:"teams"`
	Hosts []string `json:"hosts"`
}

func (r Resources) list(kind string) []string {
	switch kind {
	case ResourceBot:
		return r.Bots
	case ResourceTeam:
		return r.Teams
	case ResourceHost:
		return r.Hosts
	}
	return []string{} // 不認得的種類一律不允許
}

// Identity 是「誰在呼叫」。由出口（HTTP、MCP、CLI、product.sock）驗證後建立，登錄表只相信這個值。
type Identity struct {
	Kind      Kind      `json:"kind"`
	ID        string    `json:"id,omitempty"`   // agent：權杖 id；bot：Bot id；owner：空白
	Name      string    `json:"name,omitempty"` // 顯示用，例：Claude Code
	Scopes    []Scope   `json:"scopes,omitempty"`
	Resources Resources `json:"resources"`
	Via       string    `json:"via,omitempty"` // ui / http / mcp / cli / product（寫進稽核）
}

// Owner 是使用者本人：所有權限、不限資源。
func Owner(via string) Identity {
	return Identity{Kind: KindOwner, Name: "你", Scopes: slices.Clone(Scopes), Via: via}
}

// Agent 由一把有效的代理權杖建立身分。權杖是否撤銷、過期由呼叫端（store.FindAgentToken）先確認。
func Agent(t store.AgentToken, via string) (Identity, error) {
	id := Identity{Kind: KindAgent, ID: t.ID, Name: t.Name, Via: via}
	for _, s := range t.Scopes {
		if !slices.Contains(Scopes, Scope(s)) {
			return Identity{}, fmt.Errorf("權杖 %s 有不認得的權限範圍 %q", t.ID, s)
		}
		id.Scopes = append(id.Scopes, Scope(s))
	}
	if len(t.Resources) > 0 {
		if err := json.Unmarshal(t.Resources, &id.Resources); err != nil {
			return Identity{}, fmt.Errorf("權杖 %s 的資源範圍讀不懂：%w", t.ID, err)
		}
	}
	return id, nil
}

// Bot 是一個 Bot 自己透過 product.sock 呼叫：只能看和交辦，而且只能碰自己（不能碰任何團隊、主機），
// 只能用標了 ForBot 的操作。之後依 Bot 的權限設定（bots.set_permissions）放寬。
func Bot(botID, name string) Identity {
	return Identity{Kind: KindBot, ID: botID, Name: name, Scopes: []Scope{ScopeRead, ScopeOperate},
		Resources: Resources{Bots: []string{botID}, Teams: []string{}, Hosts: []string{}}, Via: "product"}
}

// Actor 是寫進稽核與去重紀錄的呼叫者代號：owner、agent:<權杖 id>、bot:<Bot id>。
func (id Identity) Actor() string {
	if id.Kind == KindOwner {
		return string(KindOwner)
	}
	return string(id.Kind) + ":" + id.ID
}

// DisplayName 是給人看的名字。
func (id Identity) DisplayName() string {
	if id.Name != "" {
		return id.Name
	}
	return id.Actor()
}

// HasScope 回答這個身分有沒有某個權限範圍。
func (id Identity) HasScope(s Scope) bool {
	return id.Kind == KindOwner || slices.Contains(id.Scopes, s)
}

// CanAccess 回答這個身分能不能碰某個資源（kind 是 ResourceBot 等）。列表類操作用它過濾結果。
func (id Identity) CanAccess(kind, resourceID string) bool {
	if id.Kind == KindOwner {
		return true
	}
	allowed := id.Resources.list(kind)
	return allowed == nil || slices.Contains(allowed, resourceID)
}

func (id Identity) valid() bool {
	switch id.Kind {
	case KindOwner:
		return true
	case KindAgent, KindBot:
		return id.ID != ""
	}
	return false
}
