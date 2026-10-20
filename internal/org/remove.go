package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

// 有團隊管理權限的 Bot 也能刪 Bot、刪團隊（使用者決定 2026-10-19：「有管理權限的也要可以執行才對」）。
// 但刪除一律要使用者本人按「允許」（AGENTS.md：刪除 Bot 或資料永遠不能代為核准）：
// 這兩個操作是 confirm 級、NoDelegate，Bot 呼叫時先送一張核准卡；使用者允許後才真的刪，
// 刪的時候用使用者在網頁上刪除的同一個操作（xbot.bots.delete／xbot.teams.delete）。

// RemoveBotIn 是 xbot.teams.remove_bot 的輸入。
type RemoveBotIn struct {
	Bot    string `json:"bot" required:"true" max:"100" desc:"要刪的 Bot（名字或 id）"`
	Reason string `json:"reason,omitempty" max:"500" desc:"為什麼要刪（給使用者看）"`
}

// RemoveTeamIn 是 xbot.teams.remove_team 的輸入。
type RemoveTeamIn struct {
	Team   string `json:"team" required:"true" max:"100" desc:"要刪的團隊（名字或 id）；成員 Bot 不會被刪"`
	Reason string `json:"reason,omitempty" max:"500" desc:"為什麼要刪（給使用者看）"`
}

// RemoveOut 是刪除的結果。
type RemoveOut struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// botTarget 檢查發起的 Bot 有團隊管理權限、要刪的 Bot 存在而且不是自己；送核准卡之前和允許之後都跑。
func botTarget(ctx context.Context, s *Service, call *ops.Call, in RemoveBotIn) (string, string, error) {
	if err := ready(s); err != nil {
		return "", "", err
	}
	if call.Identity.Kind != ops.KindBot {
		return "", "", ops.Forbidden("這是給有團隊管理權限的 Bot 用的；你本人請用 xbot.bots.delete")
	}
	if _, err := s.RequireManager(ctx, call.Identity.ID); err != nil {
		return "", "", err
	}
	r, err := s.roster(ctx)
	if err != nil {
		return "", "", err
	}
	target, err := r.find(in.Bot)
	if err != nil {
		return "", "", err
	}
	if target.ID == call.Identity.ID {
		return "", "", ops.InvalidInput(ops.FieldError{Field: "bot", Message: "不能刪自己"})
	}
	return target.ID, target.Name, nil
}

// teamTarget 檢查發起的 Bot 有團隊管理權限、要刪的團隊存在。
func teamTarget(ctx context.Context, s *Service, call *ops.Call, in RemoveTeamIn) (string, string, error) {
	if err := ready(s); err != nil {
		return "", "", err
	}
	if call.Identity.Kind != ops.KindBot {
		return "", "", ops.Forbidden("這是給有團隊管理權限的 Bot 用的；你本人請用 xbot.teams.delete")
	}
	if _, err := s.RequireManager(ctx, call.Identity.ID); err != nil {
		return "", "", err
	}
	teams, err := s.Teams(ctx)
	if err != nil {
		return "", "", err
	}
	ref := strings.TrimSpace(in.Team)
	for _, t := range teams {
		if t.ID == ref || t.Name == ref {
			return t.ID, t.Name, nil
		}
	}
	return "", "", ops.NotFound("找不到這個團隊：" + ref)
}

func registerRemove(reg *ops.Registry, s *Service) {
	ops.Register(reg, ops.Op[RemoveBotIn, RemoveOut]{
		Name: "xbot.teams.remove_bot", Summary: "刪除一個 Bot（有團隊管理權限的 Bot 用；要使用者按允許）",
		Description: "連同它的電腦、對話、任務一起刪除，無法復原。會先送一張核准卡給使用者，使用者允許後才刪；不能刪自己。" +
			"你本人請用 xbot.bots.delete。",
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true, ForBot: true, ManagerOnly: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RemoveBotIn) error {
			_, _, err := botTarget(ctx, s, call, in)
			return err
		},
		Explain: func(ctx context.Context, call *ops.Call, in RemoveBotIn) (string, error) {
			_, name, err := botTarget(ctx, s, call, in)
			return explain([]string{"Bot：" + name + "（連同它的電腦、檔案、對話）"}, in.Reason), err
		},
		Handle: func(ctx context.Context, call *ops.Call, in RemoveBotIn) (RemoveOut, error) {
			id, name, err := botTarget(ctx, s, call, in)
			if err != nil {
				return RemoveOut{}, err
			}
			out := RemoveOut{Name: name}
			if call.DryRun {
				return out, nil
			}
			// 到這裡代表使用者已經按了允許（confirm 級的操作，Bot 呼叫時先送核准卡）
			raw, _ := json.Marshal(map[string]string{"id": id})
			if _, err := reg.Invoke(ctx, ops.Owner("approved"), "xbot.bots.delete", raw, ops.Options{}); err != nil {
				return out, err
			}
			out.Deleted = true
			return out, nil
		},
	})
	ops.Register(reg, ops.Op[RemoveTeamIn, RemoveOut]{
		Name: "xbot.teams.remove_team", Summary: "刪除一個團隊（有團隊管理權限的 Bot 用；要使用者按允許；Bot 不會被刪）",
		Description: "成員回到未分區，Bot、報告線、群組都保留；團隊記憶會一起刪掉。會先送一張核准卡給使用者，使用者允許後才刪。" +
			"你本人請用 xbot.teams.delete。",
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true, ForBot: true, ManagerOnly: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RemoveTeamIn) error {
			_, _, err := teamTarget(ctx, s, call, in)
			return err
		},
		Explain: func(ctx context.Context, call *ops.Call, in RemoveTeamIn) (string, error) {
			_, name, err := teamTarget(ctx, s, call, in)
			return explain([]string{"團隊：" + name + "（成員 Bot 不會被刪）"}, in.Reason), err
		},
		Handle: func(ctx context.Context, call *ops.Call, in RemoveTeamIn) (RemoveOut, error) {
			id, name, err := teamTarget(ctx, s, call, in)
			if err != nil {
				return RemoveOut{}, err
			}
			out := RemoveOut{Name: name}
			if call.DryRun {
				return out, nil
			}
			raw, _ := json.Marshal(map[string]string{"id": id})
			if _, err := reg.Invoke(ctx, ops.Owner("approved"), "xbot.teams.delete", raw, ops.Options{}); err != nil {
				return out, err
			}
			out.Deleted = true
			return out, nil
		},
	})
	registerRemoveMany(reg, s)
}

// explain 是刪除核准卡上的說明：要刪什麼、為什麼。
func explain(lines []string, reason string) string {
	text := "允許後會刪掉：\n- " + strings.Join(lines, "\n- ")
	if r := strings.TrimSpace(reason); r != "" {
		text += "\n\n原因：" + r
	}
	return text + "\n\n刪了就無法復原。"
}

// RemoveManyIn 是 xbot.teams.remove_many 的輸入。
type RemoveManyIn struct {
	Bots   []string `json:"bots,omitempty" desc:"要刪的 Bot（名字或 id）"`
	Teams  []string `json:"teams,omitempty" desc:"要刪的團隊（名字或 id）；成員 Bot 要一起刪就列在 bots"`
	Groups []string `json:"groups,omitempty" desc:"要刪的群組對話（名稱或 id）"`
	Reason string   `json:"reason,omitempty" max:"500" desc:"為什麼要刪（給使用者看）"`
}

// RemoveManyOut 是 xbot.teams.remove_many 的輸出（使用者允許後）。
type RemoveManyOut struct {
	Bots    []string `json:"bots"`
	Teams   []string `json:"teams"`
	Groups  []string `json:"groups"`
	Gone    []string `json:"gone,omitempty" desc:"核准之後、刪之前已經不在的（略過）"`
	Deleted bool     `json:"deleted"`
}

type batchItem struct{ id, name string }

type batch struct{ bots, teams, groups []batchItem }

func (b batch) lines() []string {
	var out []string
	for _, x := range b.bots {
		out = append(out, "Bot："+x.name)
	}
	for _, x := range b.teams {
		out = append(out, "團隊："+x.name)
	}
	for _, x := range b.groups {
		out = append(out, "群組對話："+x.name)
	}
	return out
}

func itemNames(xs []batchItem) []string {
	out := []string{}
	for _, x := range xs {
		out = append(out, x.name)
	}
	return out
}

// batchTarget 檢查發起的 Bot 有團隊管理權限、每一個要刪的都找得到；重複的只算一次。
// gone 不是 nil 時（使用者已經允許、要開始刪了）找不到的不算錯：記進 gone 略過，其他照刪
// （審查 2026-10-21：核准前手動刪掉其中一個，按允許後整批都刪不了，重試也一樣）。
func batchTarget(ctx context.Context, s *Service, call *ops.Call, in RemoveManyIn, gone *[]string) (batch, error) {
	var b batch
	n := len(in.Bots) + len(in.Teams) + len(in.Groups)
	if n == 0 {
		return b, ops.InvalidInput(ops.FieldError{Field: "bots", Message: "至少要列一個要刪的 Bot、團隊或群組"})
	}
	if n > limits.RemoveBatchMax {
		return b, ops.InvalidInput(ops.FieldError{Field: "bots", Message: fmt.Sprintf("一次最多刪 %d 個", limits.RemoveBatchMax)})
	}
	if err := ready(s); err != nil {
		return b, err
	}
	if call.Identity.Kind != ops.KindBot {
		return b, ops.Forbidden("這是給有團隊管理權限的 Bot 用的；你本人請用 xbot.bots.delete／xbot.teams.delete／xbot.rooms.delete")
	}
	if _, err := s.RequireManager(ctx, call.Identity.ID); err != nil {
		return b, err
	}
	seen := map[string]bool{}
	missing := func(ref string, err error) bool {
		if gone != nil && err != nil && ops.AsError(err).Code == ops.CodeNotFound {
			*gone = append(*gone, strings.TrimSpace(ref))
			return true
		}
		return false
	}
	for _, ref := range in.Bots {
		id, name, err := botTarget(ctx, s, call, RemoveBotIn{Bot: ref})
		if missing(ref, err) {
			continue
		}
		if err != nil {
			return b, err
		}
		if !seen[id] {
			seen[id] = true
			b.bots = append(b.bots, batchItem{id, name})
		}
	}
	for _, ref := range in.Teams {
		id, name, err := teamTarget(ctx, s, call, RemoveTeamIn{Team: ref})
		if missing(ref, err) {
			continue
		}
		if err != nil {
			return b, err
		}
		if !seen[id] {
			seen[id] = true
			b.teams = append(b.teams, batchItem{id, name})
		}
	}
	if len(in.Groups) > 0 {
		rooms, err := call.DB.ListRooms(ctx, "")
		if err != nil {
			return b, err
		}
		for _, ref := range in.Groups {
			ref = strings.TrimSpace(ref)
			found := false
			for _, r := range rooms {
				if r.ID == ref || r.Name == ref {
					found = true
					if !seen[r.ID] {
						seen[r.ID] = true
						b.groups = append(b.groups, batchItem{r.ID, r.Name})
					}
					break
				}
			}
			if err := ops.NotFound("找不到這個群組：" + ref); !found && !missing(ref, err) {
				return b, err
			}
		}
	}
	return b, nil
}

// registerRemoveMany：一次刪好幾個，只要使用者允許一次（使用者決定 2026-10-19：「同一批的刪除只批准一次就全刪」）。
func registerRemoveMany(reg *ops.Registry, s *Service) {
	ops.Register(reg, ops.Op[RemoveManyIn, RemoveManyOut]{
		Name: "xbot.teams.remove_many", Summary: "一次刪除好幾個 Bot、團隊、群組（有團隊管理權限的 Bot 用；使用者允許一次就全刪）",
		Description: "同一批要刪的列在一起，只送一張核准卡；使用者允許後依序刪群組、Bot、團隊。刪 Bot 會連同它的電腦、對話、任務，無法復原。" +
			"要刪兩個以上時用這個，不要一個一個送。你本人請用 xbot.bots.delete／xbot.teams.delete／xbot.rooms.delete。",
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true, ForBot: true, ManagerOnly: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RemoveManyIn) error {
			_, err := batchTarget(ctx, s, call, in, nil)
			return err
		},
		Explain: func(ctx context.Context, call *ops.Call, in RemoveManyIn) (string, error) {
			b, err := batchTarget(ctx, s, call, in, nil)
			return explain(b.lines(), in.Reason), err
		},
		Handle: func(ctx context.Context, call *ops.Call, in RemoveManyIn) (RemoveManyOut, error) {
			var gone []string
			b, err := batchTarget(ctx, s, call, in, &gone)
			if err != nil {
				return RemoveManyOut{}, err
			}
			out := RemoveManyOut{Bots: itemNames(b.bots), Teams: itemNames(b.teams), Groups: itemNames(b.groups), Gone: gone}
			if call.DryRun {
				return out, nil
			}
			// 到這裡代表使用者已經按了允許；用使用者在網頁上刪除的同一組操作
			run := func(op string, in map[string]string) error {
				raw, _ := json.Marshal(in)
				_, err := reg.Invoke(ctx, ops.Owner("approved"), op, raw, ops.Options{})
				return err
			}
			for _, g := range b.groups {
				if err := run("xbot.rooms.delete", map[string]string{"room_id": g.id}); err != nil && ops.AsError(err).Code != ops.CodeNotFound {
					return out, err
				}
			}
			for _, x := range b.bots {
				if err := run("xbot.bots.delete", map[string]string{"id": x.id}); err != nil && ops.AsError(err).Code != ops.CodeNotFound {
					return out, err
				}
			}
			for _, t := range b.teams {
				if err := run("xbot.teams.delete", map[string]string{"id": t.id}); err != nil && ops.AsError(err).Code != ops.CodeNotFound {
					return out, err
				}
			}
			out.Deleted = true
			return out, nil
		},
	})
}
