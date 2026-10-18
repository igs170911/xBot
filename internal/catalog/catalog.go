// Package catalog 把所有核心操作登錄到操作登錄表（PRD §22.3 工具目錄）。
// 服務、指令列、產生前端型別與端對端測試都用同一份登錄，各出口看到的操作才會一樣。
package catalog

import (
	"context"
	"errors"

	"xbot/internal/botops"
	"xbot/internal/bots"
	"xbot/internal/chat"
	"xbot/internal/collab"
	"xbot/internal/delegation"
	"xbot/internal/engines"
	"xbot/internal/files"
	"xbot/internal/hosts"
	"xbot/internal/inbox"
	"xbot/internal/judge"
	"xbot/internal/memory"
	"xbot/internal/models"
	"xbot/internal/ops"
	"xbot/internal/org"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/skills"
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
	Hosts     *hosts.Service   // 主機管理（B12）
	// Teardown 收掉 Bot 的電腦（含資料區）與引擎連線；刪除 Bot 時呼叫。可為 nil。
	Teardown func(ctx context.Context, botID string) error
	// Routines 是定時任務的排程器（C32）；nil 時只登錄定義，run_now 等要排程器的呼叫回 internal。
	Routines *routines.Service
	// Skills、Memory 是技能與記憶（C31）；nil 時只登錄定義。
	Skills *skills.Service
	Memory *memory.Service
	// SoulChanged 把新的 SOUL.md 寫進 Bot 的電腦（C31）。可為 nil。
	SoulChanged func(ctx context.Context, botID string)
	// Collab 是 Bot 之間的傳話與群組（X02）；nil 時只登錄定義。
	Collab *collab.Service
	// Org 是團隊、匯報線、提案與討論（X03）；nil 時只登錄定義。
	Org *org.Service
	// Files 是檔案面板（C33）；nil 時只登錄定義。
	Files *files.Service
	// Judge 是快判（X05）；nil 時只登錄定義。
	Judge *judge.Service
}

// Register 登錄 bots、chat、approvals、inbox、audit、代為核准（agents.*_delegation）、cards、team、collab、rooms、teams、agenda、models、usage、engines、skills、memory、hosts、routines、files、judge（快判）各領域的操作。
func Register(reg *ops.Registry, d Deps) {
	bd := bots.Deps{Teardown: d.Teardown, SoulChanged: d.SoulChanged}
	if d.Collab != nil {
		// 刪除 Bot 時，還在等它回覆的隊友會收到「已經被刪除」（AA-6）。
		teardown, c := d.Teardown, d.Collab
		bd.Teardown = func(ctx context.Context, botID string) error {
			if teardown != nil {
				if err := teardown(ctx, botID); err != nil {
					return err
				}
			}
			c.ForgetBot(context.WithoutCancel(ctx), botID)
			return nil
		}
	}
	if d.Runtime != nil {
		rt := d.Runtime
		bd.Stop = func(ctx context.Context, botID string) { _, _ = rt.Stop(ctx, botID, "") }
		bd.Active = rt.Active
	}
	bots.Register(reg, bd)
	chat.Register(reg, chat.Deps{Runtime: d.Runtime})
	inbox.Register(reg)
	delegation.Register(reg)
	var active func(string) (string, string)
	if d.Runtime != nil {
		active = d.Runtime.Active
	}
	botops.Register(reg, botops.Deps{Active: active})
	collab.Register(reg, d.Collab)
	org.Register(reg, d.Org)
	models.Register(reg, models.Deps{Providers: d.Providers, Lister: d.Lister, Prober: d.Prober})
	usage.Register(reg, usage.Deps{Budgets: d.Budgets})
	engines.Register(reg, d.Engines)
	routines.Register(reg, d.Routines)
	skills.Register(reg, d.Skills)
	memory.Register(reg, d.Memory)
	hosts.Register(reg, d.Hosts)
	files.Register(reg, d.Files)
	judge.Register(reg, d.Judge)
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
