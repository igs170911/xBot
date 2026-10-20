package engines

// xbot.engines.login_*、logout、check：每個 Bot 在自己的電腦裡登入訂閱 CLI（own.go）。只有你本人能做。

import (
	"context"
	"errors"
	"os"
	"strings"

	"xbot/internal/engine/acp"
	"xbot/internal/limits"
	"xbot/internal/ops"
)

// Account 是一個 Bot 的一個訂閱 CLI 帳號：能不能用、怎麼登入、有哪些模型。
type Account struct {
	Engine string `json:"engine"`
	// Label 是 CLI 名稱；Plan 是帳號種類（Claude、ChatGPT、Grok、OpenCode Go、Google）。
	Label string `json:"label"`
	Plan  string `json:"plan"`
	// Login 是怎麼登入：web（到網頁輸入代碼）、code（網頁登入後貼回授權碼）、key（貼金鑰）、host（只能用這台電腦的登入）。
	Login string `json:"login" desc:"web：到網頁輸入代碼；code：網頁登入後貼回授權碼；key：貼金鑰；host：只能用這台電腦的登入"`
	// Ready：這個 Bot 可以用這個帳號（自己登入過，或同意用這台電腦的登入）。
	Ready bool `json:"ready" desc:"這個 Bot 可以用這個帳號"`
	// Source：bot（這個 Bot 自己登入的）或 host（用這台電腦的登入）。
	Source string `json:"source,omitempty" desc:"bot：這個 Bot 自己登入的；host：用這台電腦的登入"`
	At     int64  `json:"at,omitempty"`
	// HostLoggedIn：這台電腦上有這個 CLI 的登入（可以改用它，xbot.engines.link_credentials）。
	HostLoggedIn bool `json:"host_logged_in"`
	// Models、Default：這個帳號能用的模型（登入後確認時、或上次對話時 CLI 列出的）與 CLI 自己的預設。
	Models  []acp.ModelChoice `json:"models,omitempty"`
	Default string            `json:"default,omitempty"`
}

// accounts 列出 Bot 每個訂閱 CLI 的帳號。
func (s *Service) accounts(ctx context.Context, botID string) ([]Account, error) {
	own, err := s.owns(ctx, botID)
	if err != nil {
		return nil, err
	}
	l, linked, err := s.Get(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := []Account{}
	for _, e := range acp.Engines {
		sp := Specs[e]
		a := Account{Engine: e, Label: sp.Label, Plan: sp.Plan, Login: LoginKind(e)}
		if o, ok := own[e]; ok && !o.Revoked {
			a.Ready, a.Source, a.At = true, "bot", o.At
		} else if linked && !l.Revoked && l.Engine == e {
			a.Ready, a.Source, a.At = true, "host", l.LinkedAt
		}
		_, herr := os.Stat(s.HostPath(sp))
		a.HostLoggedIn = herr == nil
		m, err := s.engineModels(ctx, e)
		if err != nil {
			return nil, err
		}
		a.Models, a.Default = m.Models, m.Default
		out = append(out, a)
	}
	return out, nil
}

// LoginEngine 是可以在網頁上登入的 CLI。
type LoginEngine string

// EnumValues 讓 JSON Schema 列出可用值。
func (LoginEngine) EnumValues() []string {
	return []string{acp.EngineClaude, acp.EngineCodex, acp.EngineGrok}
}

// LoginStartIn 是 xbot.engines.login_start 的輸入。
type LoginStartIn struct {
	BotID  string      `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Engine LoginEngine `json:"engine" required:"true" desc:"要登入哪個 CLI"`
}

// LoginBotIn 是只帶 Bot 的登入操作的輸入。
type LoginBotIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// LoginCodeIn 是 xbot.engines.login_code 的輸入。
type LoginCodeIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Code  string `json:"code" required:"true" max:"2048" desc:"網頁登入完顯示的授權碼"`
}

// LoginKeyIn 是 xbot.engines.login_key 的輸入（OpenCode）。
type LoginKeyIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	// GoKey、ZenKey：OpenCode Go 訂閱與 OpenCode Zen 的金鑰（opencode.ai 上是兩把不同的金鑰），至少一把。
	GoKey  string `json:"go_key,omitempty" max:"512" desc:"OpenCode Go 訂閱的金鑰"`
	ZenKey string `json:"zen_key,omitempty" max:"512" desc:"OpenCode Zen 的金鑰"`
}

// LogoutIn 是 xbot.engines.logout 與 xbot.engines.check 的輸入。
type LogoutIn struct {
	BotID  string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Engine Engine `json:"engine" required:"true" desc:"哪個 CLI"`
}

// CheckOut 是 xbot.engines.check 的輸出。
type CheckOut struct {
	OK      bool    `json:"ok" desc:"登入有效，CLI 開得起來"`
	Message string  `json:"message,omitempty"`
	Account Account `json:"account"`
}

func registerLogin(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil {
			return ops.Internal(errors.New("引擎服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[LoginStartIn, LoginView]{
		Name: "xbot.engines.login_start", Summary: "讓一個 Bot 在自己的電腦裡登入 Claude Code、Codex 或 Grok（網頁授權）",
		Description: "在這個 Bot 的電腦裡開 CLI 的登入，回傳網址與代碼：到網址登入、輸入代碼（Claude 要把網頁給的授權碼用 xbot.engines.login_code 貼回來）。" +
			"之後用 xbot.engines.login_status 看進度。登入的帳號只給這個 Bot 用，Bot 換成別的引擎時收起來，不會給 Hermes。只有你本人能做。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LoginStartIn) (LoginView, error) {
			if err := need(); err != nil {
				return LoginView{}, err
			}
			if err := ownerOnly(call); err != nil {
				return LoginView{}, err
			}
			b, err := getBot(ctx, call, in.BotID)
			if err != nil {
				return LoginView{}, err
			}
			if _, ok := loginCmds[string(in.Engine)]; !ok {
				return LoginView{}, ops.InvalidInput(ops.FieldError{Field: "engine", Message: "只能在網頁上登入 Claude Code、Codex 或 Grok"})
			}
			if call.DryRun {
				return LoginView{BotID: b.ID, Engine: string(in.Engine), Label: Specs[string(in.Engine)].Label, State: LoginWaiting}, nil
			}
			v, err := svc.StartLogin(ctx, b.ID, string(in.Engine))
			if errors.Is(err, ErrNoExec) {
				return LoginView{}, ops.Conflict(err.Error())
			}
			return v, err
		},
	})
	ops.Register(reg, ops.Op[LoginBotIn, LoginView]{
		Name: "xbot.engines.login_status", Summary: "看 Bot 登入 CLI 的進度",
		Description: "回傳最近一次 xbot.engines.login_start 的狀態：waiting（等你到網頁登入）、checking（確認中）、done、failed、cancelled；沒有登入時 state 是 none。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LoginBotIn) (LoginView, error) {
			if err := need(); err != nil {
				return LoginView{}, err
			}
			if _, err := getBot(ctx, call, in.BotID); err != nil {
				return LoginView{}, err
			}
			v, ok := svc.LoginStatus(in.BotID)
			if !ok {
				return LoginView{BotID: in.BotID, State: "none"}, nil
			}
			return v, nil
		},
	})
	ops.Register(reg, ops.Op[LoginCodeIn, LoginView]{
		Name: "xbot.engines.login_code", Summary: "把網頁登入完的授權碼交給 Bot 電腦裡的登入（Claude Code）",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LoginCodeIn) (LoginView, error) {
			if err := need(); err != nil {
				return LoginView{}, err
			}
			if err := ownerOnly(call); err != nil {
				return LoginView{}, err
			}
			code := strings.TrimSpace(in.Code)
			if code == "" || len(code) > limits.EngineLoginCodeMax || strings.ContainsAny(code, "\r\n") {
				return LoginView{}, ops.InvalidInput(ops.FieldError{Field: "code", Message: "授權碼不對，請整段複製再貼一次"})
			}
			if call.DryRun {
				v, _ := svc.LoginStatus(in.BotID)
				return v, nil
			}
			v, err := svc.SubmitCode(in.BotID, code)
			if err != nil {
				return v, ops.Conflict(err.Error())
			}
			return v, nil
		},
	})
	ops.Register(reg, ops.Op[LoginBotIn, LoginView]{
		Name: "xbot.engines.login_cancel", Summary: "取消 Bot 進行中的 CLI 登入",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LoginBotIn) (LoginView, error) {
			if err := need(); err != nil {
				return LoginView{}, err
			}
			if err := ownerOnly(call); err != nil {
				return LoginView{}, err
			}
			if !call.DryRun {
				svc.CancelLogin(ctx, in.BotID)
			}
			v, ok := svc.LoginStatus(in.BotID)
			if !ok {
				return LoginView{BotID: in.BotID, State: "none"}, nil
			}
			return v, nil
		},
	})
	ops.Register(reg, ops.Op[LoginKeyIn, BotStatus]{
		Name: "xbot.engines.login_key", Summary: "用金鑰讓一個 Bot 登入 OpenCode（OpenCode Go／Zen）",
		Description: "把金鑰寫成這個 Bot 電腦裡 OpenCode 的登入（只給這個 Bot 用；Bot 換成別的引擎時收起來）。至少給一把金鑰。只有你本人能做。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LoginKeyIn) (BotStatus, error) {
			if err := need(); err != nil {
				return BotStatus{}, err
			}
			if err := ownerOnly(call); err != nil {
				return BotStatus{}, err
			}
			b, err := getBot(ctx, call, in.BotID)
			if err != nil {
				return BotStatus{}, err
			}
			keys := map[string]string{"opencode-go": strings.TrimSpace(in.GoKey), "opencode": strings.TrimSpace(in.ZenKey)}
			if keys["opencode-go"] == "" && keys["opencode"] == "" {
				return BotStatus{}, ops.InvalidInput(ops.FieldError{Field: "go_key", Message: "至少要一把金鑰"})
			}
			for f, k := range map[string]string{"go_key": keys["opencode-go"], "zen_key": keys["opencode"]} {
				if strings.ContainsAny(k, " \t\r\n\"") || len(k) > limits.EngineLoginKeyMax {
					return BotStatus{}, ops.InvalidInput(ops.FieldError{Field: f, Message: "金鑰不對，請整段複製再貼一次"})
				}
			}
			if call.DryRun {
				return svc.view(ctx, b)
			}
			if err := svc.SetKeys(ctx, b.ID, keys); err != nil {
				if errors.Is(err, ErrNoExec) {
					return BotStatus{}, ops.Conflict(err.Error())
				}
				// 金鑰已經放好，只是確認失敗：回傳狀態，錯誤寫進紀錄（介面會看到帳號已登入）。
				svc.log().Warn("OpenCode 金鑰放好了，但確認失敗", "bot", b.ID, "err", err)
			}
			return svc.view(ctx, b)
		},
	})
	ops.Register(reg, ops.Op[LogoutIn, BotStatus]{
		Name: "xbot.engines.logout", Summary: "讓一個 Bot 登出它自己登入的 CLI",
		Description: "結束正在跑的 CLI，清掉這個 Bot 電腦裡那個 CLI 的登入（不影響這台電腦和其他 Bot）。只有你本人能做。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LogoutIn) (BotStatus, error) {
			if err := need(); err != nil {
				return BotStatus{}, err
			}
			if err := ownerOnly(call); err != nil {
				return BotStatus{}, err
			}
			b, err := getBot(ctx, call, in.BotID)
			if err != nil {
				return BotStatus{}, err
			}
			if !call.DryRun {
				if _, err := svc.Logout(ctx, b.ID, string(in.Engine)); err != nil {
					return BotStatus{}, err
				}
			}
			return svc.view(ctx, b)
		},
	})
	ops.Register(reg, ops.Op[LogoutIn, CheckOut]{
		Name: "xbot.engines.check", Summary: "確認一個 Bot 的 CLI 登入有效，並更新它能用的模型",
		Description: "開一次 CLI（不送訊息）：開得起來表示登入有效，順便記下這個帳號能用的模型。會開機。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LogoutIn) (CheckOut, error) {
			if err := need(); err != nil {
				return CheckOut{}, err
			}
			b, err := getBot(ctx, call, in.BotID)
			if err != nil {
				return CheckOut{}, err
			}
			out := CheckOut{}
			if !call.DryRun {
				if ready, err := svc.Ready(ctx, b.ID, string(in.Engine)); err != nil {
					return CheckOut{}, err
				} else if !ready {
					out.Message = "這個 Bot 還沒登入 " + label(string(in.Engine))
				} else if err := svc.Probe(ctx, b.ID, string(in.Engine)); err != nil {
					out.Message = err.Error()
				} else {
					out.OK = true
				}
			}
			accs, err := svc.accounts(ctx, b.ID)
			if err != nil {
				return CheckOut{}, err
			}
			for _, a := range accs {
				if a.Engine == string(in.Engine) {
					out.Account = a
				}
			}
			return out, nil
		},
	})
}
