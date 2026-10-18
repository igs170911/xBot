// Package catalog 把所有核心操作登錄到操作登錄表（PRD §22.3 工具目錄）。
// 服務、指令列、產生前端型別與端對端測試都用同一份登錄，各出口看到的操作才會一樣。
package catalog

import (
	"context"
	"errors"

	"xbot/internal/bots"
	"xbot/internal/chat"
	"xbot/internal/engines"
	"xbot/internal/inbox"
	"xbot/internal/models"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
	"xbot/internal/usage"
)

// Deps 是核心操作需要的服務元件。全部是 nil 時只登錄定義（指令列、gen ts），呼叫會回 internal。
type Deps struct {
	Runtime   chat.Runtime     // *runtime.Runtime
	Providers models.Providers // *proxy.Providers
	Lister    models.Lister    // *proxy.DBStore
	Prober    models.Prober    // 模型探測；nil 時用 &proxy.Prober{}
	Budgets   *proxy.Budgets   // 花費上限（usage 操作）
	Engines   *engines.Service // 訂閱 CLI 的憑證（EN-2b）
	// Teardown 收掉 Bot 的電腦（含資料區）與引擎連線；刪除 Bot 時呼叫。可為 nil。
	Teardown func(ctx context.Context, botID string) error
}

// Register 登錄 bots、chat、approvals、inbox、audit、models、usage、engines 各領域的操作。
func Register(reg *ops.Registry, d Deps) {
	bd := bots.Deps{Teardown: d.Teardown}
	if d.Runtime != nil {
		rt := d.Runtime
		bd.Stop = func(ctx context.Context, botID string) { _, _ = rt.Stop(ctx, botID, "") }
	}
	bots.Register(reg, bd)
	chat.Register(reg, chat.Deps{Runtime: d.Runtime})
	inbox.Register(reg)
	models.Register(reg, models.Deps{Providers: d.Providers, Lister: d.Lister, Prober: d.Prober})
	usage.Register(reg, usage.Deps{Budgets: d.Budgets})
	engines.Register(reg, d.Engines)
}

// Teardown 產生刪除 Bot 時用的收尾函式：先關引擎連線，再刪電腦與資料區（remove）。
// 從來沒開過電腦的 Bot（box 表沒有資料）不碰 Docker。
func Teardown(db *store.DB, closeEngine func(botID string), remove func(ctx context.Context, botID string) error) func(ctx context.Context, botID string) error {
	return func(ctx context.Context, botID string) error {
		if closeEngine != nil {
			closeEngine(botID)
		}
		if _, err := db.GetBox(ctx, botID); errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		return remove(ctx, botID)
	}
}
