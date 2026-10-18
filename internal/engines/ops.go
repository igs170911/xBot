package engines

import (
	"context"
	"errors"
	"os"

	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Engine 是可以連結訂閱的引擎。
type Engine string

// EnumValues 讓 JSON Schema 列出可用值。
func (Engine) EnumValues() []string { return []string{acp.EngineClaude, acp.EngineCodex} }

// LinkIn 是 xbot.engines.link_credentials 的輸入。
type LinkIn struct {
	BotID  string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Engine Engine `json:"engine" required:"true" desc:"要用哪個訂閱 CLI；要和 Bot 目前的引擎相同"`
}

// UnlinkIn 是 xbot.engines.unlink_credentials 的輸入。
type UnlinkIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// StatusIn 是 xbot.engines.status 的輸入。
type StatusIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot；不填列出全部"`
}

// BotStatus 是一個 Bot 的引擎與訂閱連結狀態（不含任何憑證內容）。
type BotStatus struct {
	BotID  string `json:"bot_id"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
	// EngineLabel 是引擎給人看的名稱。
	EngineLabel string `json:"engine_label"`
	// NeedsLink：這個引擎要連結你的訂閱才能用。
	NeedsLink bool `json:"needs_link" desc:"這個引擎要連結你的訂閱才能用"`
	Linked    bool `json:"linked" desc:"已同意，Bot 正在用你的訂閱"`
	// LinkedEngine、LinkedAt：同意的是哪個引擎、什麼時候（毫秒）。引擎和 Bot 現在的不同時，憑證不會放進電腦。
	LinkedEngine string `json:"linked_engine,omitempty"`
	LinkedAt     int64  `json:"linked_at,omitempty"`
	// Pending：已收回，但 Bot 電腦裡的憑證還沒清掉（電腦連不上），下次開機前會清。
	Pending bool `json:"pending_clear,omitempty" desc:"已收回，憑證等 Bot 的電腦連得上時清掉"`
	// Marker 是介面上的標示，例：「這個 Bot 用你的 Claude 訂閱」。沒有連結時空白。
	Marker string `json:"marker,omitempty"`
}

// HostLogin 是這台電腦上某個 CLI 有沒有登入。
type HostLogin struct {
	Engine   string `json:"engine"`
	Label    string `json:"label"`
	Plan     string `json:"plan"`
	LoggedIn bool   `json:"logged_in" desc:"這台電腦上找得到它的登入"`
	Login    string `json:"login" desc:"沒有登入時，在這台電腦上執行的指令"`
}

// StatusOut 是 xbot.engines.status 的輸出。
type StatusOut struct {
	Bots []BotStatus `json:"bots"`
	Host []HostLogin `json:"host" desc:"這台電腦上各訂閱 CLI 的登入狀態"`
}

// Register 登錄 xbot.engines.* 操作。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	ops.Register(reg, ops.Op[StatusIn, StatusOut]{
		Name: "xbot.engines.status", Summary: "看每個 Bot 用哪個引擎、有沒有用你的訂閱",
		Description: "只回答有沒有連結，不會回傳任何憑證內容。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StatusIn) (StatusOut, error) {
			if svc == nil {
				return StatusOut{}, ops.Internal(errors.New("引擎服務沒有啟動"))
			}
			return svc.status(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[LinkIn, BotStatus]{
		Name: "xbot.engines.link_credentials", Summary: "讓一個 Bot 使用你在這台電腦上的訂閱登入（Claude Code／Codex CLI）",
		Description: "你同意後，把這台電腦上該 CLI 的登入憑證唯讀放進這個 Bot 的電腦（只有這個 Bot），介面會標示「這個 Bot 用你的訂閱」。" +
			"Bot 的引擎要先設成同一個 CLI。可以隨時用 xbot.engines.unlink_credentials 收回。只有你本人能做。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LinkIn) (BotStatus, error) {
			if svc == nil {
				return BotStatus{}, ops.Internal(errors.New("引擎服務沒有啟動"))
			}
			return svc.link(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UnlinkIn, BotStatus]{
		Name: "xbot.engines.unlink_credentials", Summary: "收回一個 Bot 使用你訂閱登入的同意",
		Description: "立刻停止這個 Bot 使用你的訂閱：結束它正在跑的 CLI，清掉它電腦裡的憑證。只有你本人能做。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UnlinkIn) (BotStatus, error) {
			if svc == nil {
				return BotStatus{}, ops.Internal(errors.New("引擎服務沒有啟動"))
			}
			return svc.unlink(ctx, call, in)
		},
	})
}

func ownerOnly(call *ops.Call) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能決定 Bot 能不能用你的訂閱")
	}
	return nil
}

func getBot(ctx context.Context, call *ops.Call, id string) (store.Bot, error) {
	b, err := call.DB.GetBot(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return b, ops.NotFound("找不到這個 Bot")
	}
	return b, err
}

func label(engine string) string {
	if a, ok := acp.Agents[engine]; ok {
		return a.Label
	}
	if engine == hermes.Name {
		return "Hermes"
	}
	return engine
}

func (s *Service) view(ctx context.Context, b store.Bot) (BotStatus, error) {
	engine := b.Engine
	if engine == "" {
		engine = hermes.Name
	}
	_, needs := Specs[engine]
	v := BotStatus{BotID: b.ID, Name: b.Name, Engine: engine, EngineLabel: label(engine), NeedsLink: needs}
	l, ok, err := s.Get(ctx, b.ID)
	if err != nil || !ok {
		return v, err
	}
	if l.Revoked {
		v.Pending = true
		return v, nil
	}
	v.LinkedEngine, v.LinkedAt = l.Engine, l.LinkedAt
	if l.Engine == engine {
		v.Linked = true
		v.Marker = Specs[engine].Marker()
	}
	return v, nil
}

func (s *Service) status(ctx context.Context, call *ops.Call, in StatusIn) (StatusOut, error) {
	var bots []store.Bot
	if in.BotID != "" {
		b, err := getBot(ctx, call, in.BotID)
		if err != nil {
			return StatusOut{}, err
		}
		bots = []store.Bot{b}
	} else {
		all, err := call.DB.ListBots(ctx, false)
		if err != nil {
			return StatusOut{}, err
		}
		for _, b := range all {
			if call.Identity.CanAccess(ops.ResourceBot, b.ID) {
				bots = append(bots, b)
			}
		}
	}
	out := StatusOut{Bots: []BotStatus{}, Host: []HostLogin{}}
	for _, b := range bots {
		v, err := s.view(ctx, b)
		if err != nil {
			return StatusOut{}, err
		}
		out.Bots = append(out.Bots, v)
	}
	for _, e := range (Engine("")).EnumValues() {
		sp := Specs[e]
		_, err := os.Stat(s.HostPath(sp))
		out.Host = append(out.Host, HostLogin{Engine: e, Label: sp.Label, Plan: sp.Plan, LoggedIn: err == nil, Login: sp.Login})
	}
	return out, nil
}

func (s *Service) link(ctx context.Context, call *ops.Call, in LinkIn) (BotStatus, error) {
	if err := ownerOnly(call); err != nil {
		return BotStatus{}, err
	}
	b, err := getBot(ctx, call, in.BotID)
	if err != nil {
		return BotStatus{}, err
	}
	sp, ok := Specs[string(in.Engine)]
	if !ok {
		return BotStatus{}, ops.InvalidInput(ops.FieldError{Field: "engine", Message: "只能連結 Claude Code 或 Codex CLI 的訂閱"})
	}
	if b.Engine != sp.Engine {
		// EN-2a：Claude 訂閱只經 Claude Code 本身使用，Hermes 的 Bot 不能拿到這份憑證。
		return BotStatus{}, ops.InvalidInput(ops.FieldError{Field: "engine",
			Message: "這個 Bot 的引擎是 " + label(b.Engine) + "，要先把引擎改成 " + sp.Label + "（xbot.bots.update）才能用你的 " + sp.Plan + " 訂閱"})
	}
	if _, err := s.ReadHost(sp); err != nil {
		if errors.Is(err, ErrNoHostLogin) {
			return BotStatus{}, ops.Conflict(err.Error())
		}
		return BotStatus{}, err
	}
	if call.DryRun {
		return BotStatus{BotID: b.ID, Name: b.Name, Engine: b.Engine, EngineLabel: sp.Label, NeedsLink: true, Linked: true,
			LinkedEngine: sp.Engine, Marker: sp.Marker()}, nil
	}
	if _, err := s.Link(ctx, b.ID, sp.Engine); err != nil {
		return BotStatus{}, err
	}
	return s.view(ctx, b)
}

func (s *Service) unlink(ctx context.Context, call *ops.Call, in UnlinkIn) (BotStatus, error) {
	if err := ownerOnly(call); err != nil {
		return BotStatus{}, err
	}
	b, err := getBot(ctx, call, in.BotID)
	if err != nil {
		return BotStatus{}, err
	}
	if call.DryRun {
		v, err := s.view(ctx, b)
		v.Linked, v.Marker, v.LinkedEngine, v.LinkedAt = false, "", "", 0
		return v, err
	}
	if _, err := s.Unlink(ctx, b.ID); err != nil {
		return BotStatus{}, err
	}
	return s.view(ctx, b)
}
