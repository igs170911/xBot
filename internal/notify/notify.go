// Package notify 是「待我處理」卡片的事件（PRD IN-3、B04、L14）：新的卡出現、卡被處理（允許、回答、過期、復原、已讀）。
// 事件寫進 event 表並經 SSE 推給所有畫面（側欄徽章、通知中心、手機），斷線後可以補送。
package notify

import (
	"encoding/json"

	"xbot/internal/store"
)

// 事件種類。
const (
	KindCreated = "inbox.created" // payload：Payload
	KindUpdated = "inbox.updated" // payload：Payload
)

// 篩選分類（B04、L14 的篩選膠囊）。
const (
	GroupDecide  = "decide"  // 要你決定
	GroupDone    = "done"    // 完成
	GroupMention = "mention" // 提到你
	GroupError   = "error"   // 出錯
)

// Groups 是所有分類，依畫面上的順序。
var Groups = []string{GroupDecide, GroupDone, GroupMention, GroupError}

var groupOf = map[string]string{
	"approval": GroupDecide, "question": GroupDecide, "secret": GroupDecide, "signin": GroupDecide, "takeover": GroupDecide,
	"proposal": GroupDecide, "discussion": GroupDecide, "budget": GroupDecide, "engine_quota": GroupDecide, "lesson": GroupDecide,
	"done": GroupDone, "delegated": GroupDone, "mention": GroupMention, "error": GroupError, "routine_failed": GroupError,
}

// Group 回傳卡片種類屬於哪個分類。
func Group(kind string) string { return groupOf[kind] }

// KindsOf 回傳一個分類包含的卡片種類。
func KindsOf(group string) []string {
	var out []string
	for _, k := range []string{"approval", "question", "secret", "signin", "takeover", "proposal", "discussion", "budget", "engine_quota", "lesson",
		"done", "delegated", "mention", "error", "routine_failed"} {
		if groupOf[k] == group {
			out = append(out, k)
		}
	}
	return out
}

// Payload 是 inbox.* 事件的內容（不含卡片內文；畫面收到後用 xbot.inbox.list／counts 重讀）。
type Payload struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Group      string `json:"group"`
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
	Title      string `json:"title"`
	Read       bool   `json:"read"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

// Event 產生一筆要寫進 event 表的 inbox 事件（帶 Bot、對話串、工作）。
func Event(kind string, it store.InboxItem) store.Event {
	raw, _ := json.Marshal(Payload{ID: it.ID, Kind: it.Kind, Group: Group(it.Kind), Status: it.Status, Resolution: it.Resolution,
		Title: it.Title, Read: it.ReadAt != 0, ExpiresAt: it.ExpiresAt})
	return store.Event{BotID: it.BotID, ThreadID: it.ThreadID, TaskID: it.TaskID, Kind: kind, Payload: raw}
}
