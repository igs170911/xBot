package org

import (
	"context"
	"encoding/json"
	"strings"

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
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true, ForBot: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RemoveBotIn) error {
			_, _, err := botTarget(ctx, s, call, in)
			return err
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
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true, ForBot: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RemoveTeamIn) error {
			_, _, err := teamTarget(ctx, s, call, in)
			return err
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
}
