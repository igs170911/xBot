package models

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
)

// OAuth 是訂閱登入（E13 原型）：XBot 服務自己跑登入流程，權杖只存在 Model Proxy。*proxy.Providers 符合。
type OAuth interface {
	StartLogin(ctx context.Context, auth, providerID, label string) (proxy.LoginStart, error)
	CompleteLogin(ctx context.Context, loginID string) (proxy.Provider, error)
}

// OAuthKind 是訂閱登入的種類。
type OAuthKind string

// EnumValues 讓 JSON Schema 列出可用值。
func (OAuthKind) EnumValues() []string { return []string{"openai-codex"} }

// oauthKinds 對應到 Model Proxy 的登入方式與預設的供應商代號。
var oauthKinds = map[OAuthKind]string{"openai-codex": proxy.AuthOAuthCodex}

// OAuthStartIn 是 xbot.models.oauth_start 的輸入。
type OAuthStartIn struct {
	Kind  OAuthKind `json:"kind" required:"true" desc:"openai-codex：ChatGPT 方案（Plus／Pro／Business）"`
	ID    string    `json:"id,omitempty" max:"32" desc:"供應商代號；預設和 kind 同名"`
	Label string    `json:"label,omitempty" max:"100" desc:"顯示名稱"`
}

// OAuthStartOut 是 xbot.models.oauth_start 的輸出。
type OAuthStartOut struct {
	LoginID         string `json:"login_id" desc:"交給 xbot.models.oauth_complete"`
	UserCode        string `json:"user_code" desc:"請使用者在登入頁輸入這組代碼"`
	VerificationURL string `json:"verification_url" desc:"請使用者用自己的瀏覽器打開"`
	IntervalSeconds int    `json:"interval_seconds" desc:"每隔幾秒呼叫一次 oauth_complete"`
	ExpiresAt       int64  `json:"expires_at" desc:"這次登入的期限（毫秒）"`
}

// OAuthCompleteIn 是 xbot.models.oauth_complete 的輸入。
type OAuthCompleteIn struct {
	LoginID      string `json:"login_id" required:"true" max:"64"`
	DefaultModel string `json:"default_model,omitempty" max:"200" desc:"登入成功後順便設成預設模型"`
}

// OAuthCompleteOut 是 xbot.models.oauth_complete 的輸出。
type OAuthCompleteOut struct {
	Status   string    `json:"status" enum:"pending,done" desc:"pending：使用者還沒在瀏覽器完成，稍後再呼叫"`
	Provider *Provider `json:"provider,omitempty"`
	Default  *Default  `json:"default,omitempty"`
}

func registerOAuth(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[OAuthStartIn, OAuthStartOut]{
		Name: "xbot.models.oauth_start", Summary: "用訂閱登入新增模型供應商（例：ChatGPT 方案）",
		Description: "XBot 服務向登入伺服器要一組代碼，你在自己的瀏覽器打開網址、輸入代碼。登入完成後權杖加密存在 Model Proxy，" +
			"不會進 Bot 的電腦。接著每隔 interval_seconds 呼叫 xbot.models.oauth_complete。只有你本人能登入。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe, NoDelegate: true,
		Handle: d.oauthStart,
	})
	ops.Register(reg, ops.Op[OAuthCompleteIn, OAuthCompleteOut]{
		Name: "xbot.models.oauth_complete", Summary: "查訂閱登入是否完成，完成就存成模型供應商",
		Description: "還沒完成時回 status=pending。完成後供應商就設定好了（同代號的舊登入會被換掉），可以用 default_model 設成預設模型。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe, NoDelegate: true,
		Handle: d.oauthComplete,
	})
}

func (d Deps) oauth() (OAuth, error) {
	if err := d.ready(); err != nil {
		return nil, err
	}
	o, ok := d.Providers.(OAuth)
	if !ok {
		return nil, ops.Internal(errors.New("模型代理不支援訂閱登入"))
	}
	return o, nil
}

func (d Deps) oauthStart(ctx context.Context, call *ops.Call, in OAuthStartIn) (OAuthStartOut, error) {
	if err := ownerOnly(call); err != nil {
		return OAuthStartOut{}, err
	}
	o, err := d.oauth()
	if err != nil {
		return OAuthStartOut{}, err
	}
	auth, ok := oauthKinds[in.Kind]
	if !ok {
		return OAuthStartOut{}, ops.InvalidInput(ops.FieldError{Field: "kind", Message: "不支援這種訂閱登入"})
	}
	id := cmpOr(strings.TrimSpace(in.ID), string(in.Kind))
	if !proxy.ValidProviderID(id) {
		return OAuthStartOut{}, ops.InvalidInput(ops.FieldError{Field: "id", Message: "只能用小寫英文、數字和 -（最多 32 字）"})
	}
	if old, err := d.Providers.Get(ctx, id); err == nil && old.Auth == "" {
		return OAuthStartOut{}, ops.Conflict("已經有用金鑰設定的供應商（" + id + "），請換一個代號")
	} else if err != nil && !errors.Is(err, proxy.ErrNotFound) {
		return OAuthStartOut{}, err
	}
	if call.DryRun {
		return OAuthStartOut{}, nil
	}
	s, err := o.StartLogin(ctx, auth, id, strings.TrimSpace(in.Label))
	if err != nil {
		return OAuthStartOut{}, ops.Errorf(ops.CodeConflict, "%s", err.Error())
	}
	return OAuthStartOut{LoginID: s.LoginID, UserCode: s.UserCode, VerificationURL: s.VerificationURL,
		IntervalSeconds: int(s.Interval.Seconds()), ExpiresAt: s.ExpiresAt.UnixMilli()}, nil
}

func (d Deps) oauthComplete(ctx context.Context, call *ops.Call, in OAuthCompleteIn) (OAuthCompleteOut, error) {
	if err := ownerOnly(call); err != nil {
		return OAuthCompleteOut{}, err
	}
	o, err := d.oauth()
	if err != nil {
		return OAuthCompleteOut{}, err
	}
	if call.DryRun {
		return OAuthCompleteOut{Status: "pending"}, nil
	}
	p, err := o.CompleteLogin(ctx, strings.TrimSpace(in.LoginID))
	if errors.Is(err, proxy.ErrLoginPending) {
		return OAuthCompleteOut{Status: "pending"}, nil
	}
	if errors.Is(err, proxy.ErrLoginNotFound) {
		return OAuthCompleteOut{}, ops.NotFound(err.Error())
	}
	if err != nil {
		return OAuthCompleteOut{}, ops.Errorf(ops.CodeConflict, "登入失敗：%s", err.Error())
	}
	view := viewOf(p)
	out := OAuthCompleteOut{Status: "done", Provider: &view}
	models, listErr, err := d.listOAuthModels(ctx, p)
	if err != nil {
		return OAuthCompleteOut{}, err
	}
	if listErr != "" {
		slog.Warn("讀不到 ChatGPT 方案能用的模型，先用內建清單", "provider", p.ID, "err", listErr)
	}
	m := strings.TrimSpace(in.DefaultModel)
	if m == "" {
		// 還沒有預設模型時，用這個帳號列出的第一個（不然登入完還是「沒有模型可以用」）。
		var cur runtime.ModelRef
		if ok, err := call.DB.GetSettingInto(ctx, runtime.DefaultModelSetting, &cur); err != nil {
			return OAuthCompleteOut{}, err
		} else if !ok || cur.Model == "" {
			if len(models) == 0 {
				models = proxy.CodexModels
			}
			if len(models) > 0 {
				m = models[0]
			}
		}
	}
	if m != "" {
		ref := runtime.ModelRef{Provider: p.ID, Model: m, Transport: p.Transport}
		if err := call.DB.SetSetting(ctx, runtime.DefaultModelSetting, ref); err != nil {
			return OAuthCompleteOut{}, err
		}
		out.Default = &Default{Provider: p.ID, Model: m, Transport: Transport(p.Transport)}
	}
	return out, nil
}
