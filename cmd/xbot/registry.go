package main

import (
	"xbot/internal/auth"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// buildRegistry 登錄所有操作。服務用真的資料庫；指令列只用它產生旗標與說明（db 是 nil，不會執行）。
func buildRegistry(db *store.DB, pairing *auth.Pairing) *ops.Registry {
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, pairing)
	return reg
}
