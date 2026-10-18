package events

import (
	"encoding/json"

	"xbot/internal/ops"
)

// ForIdentity 是「這個身分看得到哪些事件」：使用者本人看全部；
// 代理與 Bot 只看得到資源範圍內的 Bot 的事件（MCP-8），以及自己發起的全域事件（payload.actor 是自己）。
// 沒有 read 權限的身分什麼都看不到。
func ForIdentity(id ops.Identity) Filter {
	if id.Kind == ops.KindOwner {
		return nil
	}
	if !id.HasScope(ops.ScopeRead) {
		return func(Event) bool { return false }
	}
	actor := id.Actor()
	return func(e Event) bool {
		if e.BotID != "" {
			return id.CanAccess(ops.ResourceBot, e.BotID)
		}
		var p struct {
			Actor string `json:"actor"`
		}
		return json.Unmarshal(e.Payload, &p) == nil && p.Actor == actor
	}
}
