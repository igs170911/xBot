package ops

import (
	"context"
	"errors"

	"xbot/internal/store"
)

// reportsOf 回傳向 botID 報告的所有 Bot（直屬或間接，含封存的）。botID 沒有團隊管理權限時回空的（不是 nil）：
// 主管看下屬的東西（Op.TeamRead）只給有團隊管理權限的 Bot。
func (r *Registry) reportsOf(ctx context.Context, botID string) ([]string, error) {
	out := []string{}
	if r.cfg.DB == nil {
		return out, nil
	}
	me, err := r.cfg.DB.GetBot(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if !me.CanManage {
		return out, nil
	}
	all, err := r.cfg.DB.ListBots(ctx, true)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{botID: true}
	frontier := map[string]bool{botID: true}
	for len(frontier) > 0 {
		next := map[string]bool{}
		for _, b := range all {
			if frontier[b.ReportsTo] && !seen[b.ID] {
				seen[b.ID] = true
				next[b.ID] = true
				out = append(out, b.ID)
			}
		}
		frontier = next
	}
	return out, nil
}
