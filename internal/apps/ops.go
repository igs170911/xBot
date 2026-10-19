package apps

import (
	"context"
	"errors"

	"xbot/internal/ops"
)

// ── 輸入輸出 ──────────────────────────────────────────

// ListIn 是 xbot.apps.list 的輸入。
type ListIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只列這個 Bot 能用的；Bot 自己呼叫時就是自己"`
}

// ListOut 是 xbot.apps.list 的輸出。
type ListOut struct {
	Connections []Connection `json:"connections"`
}

// IDIn 指定一個連接。
type IDIn struct {
	ID string `json:"id" required:"true" max:"64" desc:"連接 id"`
}

// SigninIn 是 xbot.apps.signin 的輸入。
type SigninIn struct {
	ID           string `json:"id" required:"true" max:"64" desc:"連接 id"`
	RedirectBase string `json:"redirect_base,omitempty" max:"300" desc:"XBot 的網址開頭（登入完成後回到這裡）"`
	SigninOnBot  string `json:"signin_on_bot,omitempty" resource:"bot" max:"64" desc:"把登入頁開在這個 Bot 的電腦上"`
}

// FlowIn 指定一次登入。
type FlowIn struct {
	FlowID string `json:"flow_id" required:"true" max:"64"`
}

// FinishIn 是 xbot.apps.finish_signin 的輸入。
type FinishIn struct {
	URL string `json:"url" required:"true" max:"8000" desc:"登入後停住（打不開）那一頁網址列的整串網址"`
}

// CancelOut 是 xbot.apps.cancel_signin 的輸出。
type CancelOut struct {
	Canceled bool `json:"canceled"`
}

// LoginsIn 是 xbot.logins.list 的輸入。
type LoginsIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64"`
}

// LoginsOut 是 xbot.logins.list 的輸出。
type LoginsOut struct {
	BotID string `json:"bot_id"`
	Sites []Site `json:"sites" desc:"登入的網站與 cookie 數（值不會回傳）"`
}

// ForgetIn 是 xbot.logins.forget 的輸入。
type ForgetIn struct {
	BotID string   `json:"bot_id" required:"true" resource:"bot" max:"64"`
	Sites []string `json:"sites" required:"true" min:"1" max:"50" desc:"要移除登入的網站，例：shopee.tw"`
}

// ForgetOut 是 xbot.logins.forget 的輸出。
type ForgetOut struct {
	Sites   []string `json:"sites"`
	Removed int      `json:"removed" desc:"移除了幾個 cookie"`
}

// UndoShareIn 是 xbot.logins.undo_share 的輸入。
type UndoShareIn struct {
	ShareID string `json:"share_id" required:"true" max:"64"`
}

// Register 登錄 xbot.apps.*、xbot.logins.* 操作。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	registerRequestShare(reg)
	need := func() error {
		if s == nil {
			return ops.Internal(errors.New("連接應用程式的服務沒有啟動"))
		}
		return nil
	}
	if s != nil {
		s.notify = reg.Notify
	}
	ops.Register(reg, ops.Op[CatalogIn, CatalogOut]{
		Name: "xbot.apps.catalog", Summary: "列出可以連接的應用程式",
		Description: "常用的在前；每個應用程式有驗證方式（none：不用登入；api_key：要貼金鑰；oauth2：要登入）與目前狀態。清單外的服務用 xbot.apps.add_mcp_server。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, _ *ops.Call, in CatalogIn) (CatalogOut, error) {
			if err := need(); err != nil {
				return CatalogOut{}, err
			}
			return s.catalog(ctx, in)
		},
	})
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.apps.list", Summary: "列出已連接的應用程式",
		Description: "每個連接的狀態（可以使用、需要登入、授權過期…）、誰能用、工具。不會回傳任何權杖或金鑰（只有末四碼）。Bot 自己呼叫時只看得到自己能用的。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			bot := in.BotID
			if call.Identity.Kind == ops.KindBot {
				bot = call.Identity.ID
			}
			cs, err := s.list(ctx, bot)
			return ListOut{Connections: cs}, err
		},
	})
	ops.Register(reg, ops.Op[IDIn, Connection]{
		Name: "xbot.apps.get", Summary: "看一個連接的詳細",
		Description: "帳號、狀態、誰能用、工具、最近使用（哪個 Bot 用了哪個工具）。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in IDIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			return s.get(ctx, in.ID)
		},
	})
	ops.Register(reg, ops.Op[ConnectIn, ConnectOut]{
		Name: "xbot.apps.connect", Summary: "連接一個應用程式",
		Description: "目錄裡的應用程式：不用登入的直接連好；要金鑰的帶 api_key；要登入的回傳 signin.auth_url，請使用者打開登入，" +
			"用 xbot.apps.signin_status 查結果。預設所有 Bot 都能用、工具全開。權杖由 XBot 保管，不會進 Bot 的電腦。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ConnectIn) (ConnectOut, error) {
			if err := need(); err != nil {
				return ConnectOut{}, err
			}
			return s.connect(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[SigninIn, ConnectOut]{
		Name: "xbot.apps.signin", Summary: "重新登入一個連接",
		Description: "授權過期或需要登入時用：回傳新的 signin.auth_url。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SigninIn) (ConnectOut, error) {
			if err := need(); err != nil {
				return ConnectOut{}, err
			}
			a, err := s.load(ctx, in.ID)
			if err != nil {
				return ConnectOut{}, err
			}
			if AuthKind(a.Auth) != AuthOAuth2 {
				return ConnectOut{}, ops.InvalidInput(ops.FieldError{Field: "id", Message: "這個連接不用登入"})
			}
			if call.DryRun {
				c, err := s.get(ctx, a.ID)
				return ConnectOut{Connection: c}, err
			}
			return s.afterSave(ctx, a, nil, in.RedirectBase, "", "", in.SigninOnBot)
		},
	})
	ops.Register(reg, ops.Op[FlowIn, SigninResult]{
		Name: "xbot.apps.signin_status", Summary: "查一次登入的結果",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in FlowIn) (SigninResult, error) {
			if err := need(); err != nil {
				return SigninResult{}, err
			}
			return s.signinStatus(ctx, in.FlowID)
		},
	})
	ops.Register(reg, ops.Op[FinishIn, SigninResult]{
		Name: "xbot.apps.finish_signin", Summary: "用貼上的網址完成登入",
		Description: "在別的裝置或 Bot 的電腦登入時，最後會停在打不開的頁面（正常）；把網址列整串貼上就能完成。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in FinishIn) (SigninResult, error) {
			if err := need(); err != nil {
				return SigninResult{}, err
			}
			if call.DryRun {
				return SigninResult{Status: "pending"}, nil
			}
			return s.finishURL(ctx, in.URL)
		},
	})
	ops.Register(reg, ops.Op[FlowIn, CancelOut]{
		Name: "xbot.apps.cancel_signin", Summary: "取消一次登入",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, call *ops.Call, in FlowIn) (CancelOut, error) {
			if err := need(); err != nil {
				return CancelOut{}, err
			}
			if call.DryRun {
				return CancelOut{}, nil
			}
			return CancelOut{Canceled: s.cancel(in.FlowID)}, nil
		},
	})
	ops.Register(reg, ops.Op[IDIn, Connection]{
		Name: "xbot.apps.disconnect", Summary: "中斷一個連接",
		Description: "刪掉這個帳號的權杖與設定，所有 Bot 都不能再用。要你本人核准。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			return s.disconnect(ctx, call, in.ID)
		},
	})
	ops.Register(reg, ops.Op[GrantIn, Connection]{
		Name: "xbot.apps.grant_to_bot", Summary: "讓一個 Bot 可以用這個連接",
		Description: "或用 all_bots 改成所有 Bot 都能用。Hermes 的 Bot 立刻重新載入（正在工作時下一個對話生效）；Claude Code、Codex 下一個對話生效。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in GrantIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			return s.grant(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[RevokeIn, Connection]{
		Name: "xbot.apps.revoke_from_bot", Summary: "不再讓一個 Bot 用這個連接",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in RevokeIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			return s.revoke(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[ServerIn, ConnectOut]{
		Name: "xbot.apps.add_mcp_server", Summary: "加一個自訂 MCP 服務",
		Description: "清單外的服務：遠端網址（Streamable HTTP，XBot 代為轉送，Token 不進 Bot 的電腦）或在 Bot 電腦裡執行的本機程式（環境變數只寫進有授權的 Bot）。" +
			"只加你信任的服務。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ServerIn) (ConnectOut, error) {
			if err := need(); err != nil {
				return ConnectOut{}, err
			}
			return s.addServer(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, Connection]{
		Name: "xbot.apps.update", Summary: "修改一個連接",
		Description: "名稱、啟用、關掉哪些工具；自訂連接還可以改網址、指令、Token、標頭、環境變數。變更立刻生效。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			return s.update(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[IDIn, Connection]{
		Name: "xbot.apps.test", Summary: "測試一個連接",
		Description: "XBot 連一次遠端、列出工具（本機程式要在 Bot 的電腦裡執行，第一次使用時才知道）。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (Connection, error) {
			if err := need(); err != nil {
				return Connection{}, err
			}
			if call.DryRun {
				return s.get(ctx, in.ID)
			}
			return s.test(ctx, in.ID)
		},
	})
	ops.Register(reg, ops.Op[RequestIn, RequestOut]{
		Name: "xbot.apps.request", Summary: "請使用者連接一個應用程式",
		Description: "你需要某個應用程式（例：Notion）但還不能用時呼叫：在「待我處理」出一張卡，使用者連接並授權給你之後，下一個對話就有它的工具。" +
			"不要自己去登入或要密碼。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in RequestIn) (RequestOut, error) {
			if err := need(); err != nil {
				return RequestOut{}, err
			}
			return s.request(ctx, call, in)
		},
	})

	// ── 瀏覽器登入（I05、E09）──
	ops.Register(reg, ops.Op[LoginsIn, LoginsOut]{
		Name: "xbot.logins.list", Summary: "列出 Bot 的瀏覽器登入了哪些網站",
		Description: "只有網站名稱與 cookie 數，不會回傳登入資料本身。電腦沒開機時會先開機。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in LoginsIn) (LoginsOut, error) {
			if err := need(); err != nil {
				return LoginsOut{}, err
			}
			sites, err := s.sites(ctx, in.BotID)
			return LoginsOut{BotID: in.BotID, Sites: sites}, err
		},
	})
	ops.Register(reg, ops.Op[ForgetIn, ForgetOut]{
		Name: "xbot.logins.forget", Summary: "移除 Bot 瀏覽器裡某些網站的登入",
		Description: "Bot 下次打開這些網站要重新登入。要你本人核准。",
		Scope:       ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in ForgetIn) (ForgetOut, error) {
			if err := need(); err != nil {
				return ForgetOut{}, err
			}
			sites, err := cleanSites(in.Sites)
			if err != nil {
				return ForgetOut{}, err
			}
			if call.DryRun {
				return ForgetOut{Sites: sites}, nil
			}
			n, err := s.forget(ctx, in.BotID, sites)
			return ForgetOut{Sites: sites, Removed: n}, err
		},
	})
	ops.Register(reg, ops.Op[ShareIn, ShareOut]{
		Name: "xbot.logins.share", Summary: "把一個 Bot 的網站登入分享給其他 Bot",
		Description: "複製選定網站的登入狀態（cookies），對方打開同一個網站就不用再登入；對方原本的登入會被取代。密碼不會被看到。" +
			"要你本人核准；10 分鐘內可以復原。",
		Scope: ops.ScopeManage, Risk: ops.RiskConfirm,
		Revert: func(ctx context.Context, _ *ops.Call, _ ShareIn, out ShareOut) error {
			if s == nil || out.ShareID == "" {
				return nil
			}
			_, err := s.undoShare(ctx, out.ShareID)
			return err
		},
		Handle: func(ctx context.Context, call *ops.Call, in ShareIn) (ShareOut, error) {
			if err := need(); err != nil {
				return ShareOut{}, err
			}
			return s.share(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UndoShareIn, ShareOut]{
		Name: "xbot.logins.undo_share", Summary: "復原一次分享登入",
		Description: "把對方的登入換回分享前的樣子（分享後 10 分鐘內）。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UndoShareIn) (ShareOut, error) {
			if err := need(); err != nil {
				return ShareOut{}, err
			}
			if call.DryRun {
				return ShareOut{ShareID: in.ShareID}, nil
			}
			return s.undoShare(ctx, in.ShareID)
		},
	})
}
