package main

import (
	"xbot/internal/auth"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// buildRegistry 登錄所有操作。服務用真的資料庫；指令列與產生前端型別（gen ts）只用它的定義（db 是 nil，不會執行）。
// 之後的卡（C05…）把自己的操作加在這裡，HTTP、MCP、CLI、前端型別就都有了。
// notify 在事件寫進 event 表之後呼叫（SSE 用）；可以是 nil。
func buildRegistry(db *store.DB, pairing *auth.Pairing, notify func(store.Event)) *ops.Registry {
	reg := ops.New(ops.Config{DB: db, Notify: notify})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, pairing)
	return reg
}
