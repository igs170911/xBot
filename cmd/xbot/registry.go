package main

import (
	"context"
	"log/slog"

	"xbot/internal/auth"
	"xbot/internal/catalog"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// buildRegistry 登錄所有操作。服務用真的資料庫與 st；指令列與產生前端型別（gen ts）只用它的定義
// （db、st 是 nil，不會執行）。之後的卡把自己的操作加在 internal/catalog，HTTP、MCP、CLI、前端型別就都有了。
// notify 在事件寫進 event 表之後呼叫（SSE 用）；可以是 nil。
func buildRegistry(db *store.DB, pairing *auth.Pairing, notify func(store.Event), st *stack) *ops.Registry {
	reg := ops.New(ops.Config{DB: db, Notify: notify})
	if db != nil {
		reg.SetLimiter(auth.NewLimiter(db).Check) // 代理的呼叫速率、每日呼叫與花費上限（MCP-12）
	}
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, pairing)
	catalog.Register(reg, coreDeps(st))
	return reg
}

// coreDeps 把服務元件交給核心操作；st 是 nil 時只登錄定義。
func coreDeps(st *stack) catalog.Deps {
	if st == nil {
		return catalog.Deps{}
	}
	return catalog.Deps{
		Runtime:   st.runtime,
		Engines:   st.creds,
		Providers: st.providers,
		Lister:    st.pstore,
		Budgets:   st.budgets,
		Hosts:     st.hosts,
		Collab:    st.collab,
		Skills:    st.skills,
		Memory:    st.memory,
		SoulChanged: func(ctx context.Context, botID string) {
			if err := st.comp.RefreshSoul(ctx, botID); err != nil {
				slog.Warn("更新 Bot 電腦裡的 SOUL.md 失敗，下次開機前會再寫", "bot", botID, "err", err)
			}
		},
		Teardown: catalog.Teardown(st.db, st.driver.Close, func(ctx context.Context, botID string) error {
			_, err := st.boxes.Remove(ctx, botID, true)
			return err
		}),
	}
}
