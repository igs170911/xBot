package models

import (
	"context"
	"errors"
	"slices"
	"strings"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
)

// PresetView 是一個內建的供應商範本。
type PresetView struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	BaseURL   string    `json:"base_url"`
	Transport Transport `json:"transport"`
	SignupURL string    `json:"signup_url,omitempty" desc:"申請金鑰的網址"`
	Local     bool      `json:"local" desc:"本機模型（LM Studio 等），不需要金鑰"`
	Models    []string  `json:"models" desc:"常用模型"`
	// DefaultModel 是建議先用的模型。
	DefaultModel  string `json:"default_model,omitempty" desc:"建議先用的模型"`
	SessionHeader string `json:"session_header,omitempty" desc:"供應商要求的對話識別標頭；Model Proxy 會自動帶上對話串 id"`
	Configured    bool   `json:"configured" desc:"已經用這個代號設定過"`
}

// PresetsIn 是 xbot.models.presets 的輸入。
type PresetsIn struct{}

// PresetsOut 是 xbot.models.presets 的輸出。
type PresetsOut struct {
	Presets []PresetView `json:"presets"`
}

// AddProviderIn 是 xbot.models.add_provider 的輸入。
type AddProviderIn struct {
	Preset       string    `json:"preset,omitempty" max:"32" desc:"內建範本代號（xbot.models.presets），例：openai-api；選了就只要再給金鑰"`
	ID           string    `json:"id,omitempty" max:"32" desc:"供應商代號（小寫英文、數字、-）；用範本時預設和範本同名"`
	Label        string    `json:"label,omitempty" max:"100" desc:"顯示名稱"`
	BaseURL      string    `json:"base_url,omitempty" max:"500" desc:"服務網址；用範本時可不填"`
	Transport    Transport `json:"transport,omitempty" desc:"預設的呼叫方式；用範本時可不填"`
	APIKey       string    `json:"api_key,omitempty" secret:"true" max:"4000" desc:"金鑰（只能寫、不能讀）"`
	DefaultModel string    `json:"default_model,omitempty" max:"200" desc:"順便把這個模型設成預設模型"`
}

// UpdateProviderIn 是 xbot.models.update_provider 的輸入：有給的欄位才改。
type UpdateProviderIn struct {
	ID        string     `json:"id" required:"true" max:"32"`
	Label     *string    `json:"label,omitempty" max:"100"`
	BaseURL   *string    `json:"base_url,omitempty" max:"500"`
	Transport *Transport `json:"transport,omitempty"`
	APIKey    *string    `json:"api_key,omitempty" secret:"true" max:"4000" desc:"新金鑰；空字串表示清掉金鑰（本機模型用）"`
}

// RemoveProviderIn 是 xbot.models.remove_provider 的輸入。
type RemoveProviderIn struct {
	ID string `json:"id" required:"true" max:"32"`
}

// RemoveProviderOut 是 xbot.models.remove_provider 的輸出。
type RemoveProviderOut struct {
	ID              string   `json:"id"`
	Removed         bool     `json:"removed" desc:"預演時是 false"`
	AffectedBots    []string `json:"affected_bots" desc:"模型或備援模型指定了這個供應商的 Bot（之後會改用預設模型）"`
	ClearedFallback bool     `json:"cleared_fallback" desc:"全域備援模型用的是這個供應商，一起清掉"`
}

// SetFallbackIn 是 xbot.models.set_fallback 的輸入。兩個都空白表示不用備援。
type SetFallbackIn struct {
	Provider  string    `json:"provider,omitempty" max:"32" desc:"已設定的供應商代號"`
	Model     string    `json:"model,omitempty" max:"200"`
	Transport Transport `json:"transport,omitempty" desc:"不填時依探測結果或供應商設定"`
}

// SetFallbackOut 是 xbot.models.set_fallback 的輸出。
type SetFallbackOut struct {
	Fallback *Default `json:"fallback,omitempty" desc:"清掉時不出現"`
}

func registerProviders(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[PresetsIn, PresetsOut]{
		Name: "xbot.models.presets", Summary: "列出內建的模型供應商範本",
		Description: "選一個範本（名稱、網址、呼叫方式都填好了），用 xbot.models.add_provider 加上金鑰就能用。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.presets,
	})
	ops.Register(reg, ops.Op[AddProviderIn, SetProviderOut]{
		Name: "xbot.models.add_provider", Summary: "新增一個模型供應商",
		Description: "可以從內建範本（preset）建立，只要再貼金鑰。金鑰加密存在 Model Proxy，不會進 Bot 的電腦，也不會再回傳。只有你本人能新增。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.addProvider,
	})
	ops.Register(reg, ops.Op[UpdateProviderIn, Provider]{
		Name: "xbot.models.update_provider", Summary: "修改一個模型供應商",
		Description: "只改有給的欄位；不給金鑰就保留原本的。只有你本人能修改。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.updateProvider,
	})
	ops.Register(reg, ops.Op[RemoveProviderIn, RemoveProviderOut]{
		Name: "xbot.models.remove_provider", Summary: "移除一個模型供應商（連同金鑰）",
		Description: "預設模型用的供應商不能移除，先換預設模型。用到它的 Bot 之後會改用預設模型。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: d.removeProvider,
	})
	ops.Register(reg, ops.Op[SetFallbackIn, SetFallbackOut]{
		Name: "xbot.models.set_fallback", Summary: "設定備援模型",
		Description: "主要模型回 429／5xx 或連不上、而且還沒開始回應時，改用備援模型重試一次。Bot 自己的備援（bots.update fallback）優先。兩個欄位都空白表示不用備援。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.setFallback,
	})
}

func (d Deps) presets(ctx context.Context, _ *ops.Call, _ PresetsIn) (PresetsOut, error) {
	if err := d.ready(); err != nil {
		return PresetsOut{}, err
	}
	ids, err := d.Lister.ProviderIDs(ctx)
	if err != nil {
		return PresetsOut{}, err
	}
	out := PresetsOut{Presets: []PresetView{}}
	for _, p := range proxy.Presets() {
		out.Presets = append(out.Presets, PresetView{ID: p.ID, Label: p.Label, BaseURL: p.BaseURL, Transport: Transport(p.Transport),
			SignupURL: p.SignupURL, Local: p.Local, Models: nonNil(p.Models), DefaultModel: p.SuggestedModel(),
			SessionHeader: p.SessionHeader, Configured: slices.Contains(ids, p.ID)})
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func ownerOnly(call *ops.Call) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能設定模型供應商的金鑰")
	}
	return nil
}

func (d Deps) addProvider(ctx context.Context, call *ops.Call, in AddProviderIn) (SetProviderOut, error) {
	if err := ownerOnly(call); err != nil {
		return SetProviderOut{}, err
	}
	if err := d.ready(); err != nil {
		return SetProviderOut{}, err
	}
	p := proxy.Provider{ID: strings.TrimSpace(in.ID), Label: strings.TrimSpace(in.Label), BaseURL: in.BaseURL,
		Transport: string(in.Transport), APIKey: proxy.Secret(strings.TrimSpace(in.APIKey))}
	if in.Preset != "" {
		pre, ok := proxy.PresetByID(in.Preset)
		if !ok {
			return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "preset", Message: "沒有這個範本（看 xbot.models.presets）"})
		}
		p.Preset = pre.ID
		p.ID = cmpOr(p.ID, pre.ID)
		p.Label = cmpOr(p.Label, pre.Label)
		p.BaseURL = cmpOr(strings.TrimSpace(p.BaseURL), pre.BaseURL)
		p.Transport = cmpOr(p.Transport, pre.Transport)
		if p.APIKey == "" && !pre.Local {
			return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "api_key", Message: "這個供應商需要金鑰"})
		}
	}
	if !proxy.ValidProviderID(p.ID) {
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "id", Message: "只能用小寫英文、數字和 -（最多 32 字）"})
	}
	base, err := proxy.NormalizeBaseURL(p.BaseURL)
	if err != nil {
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "base_url", Message: err.Error()})
	}
	p.BaseURL = base
	if _, err := d.Providers.Get(ctx, p.ID); err == nil {
		return SetProviderOut{}, ops.Conflict("已經有這個供應商（" + p.ID + "），要改請用 xbot.models.update_provider")
	} else if !errors.Is(err, proxy.ErrNotFound) {
		return SetProviderOut{}, err
	}
	out := SetProviderOut{Provider: viewOf(p)}
	var def *runtime.ModelRef
	if m := strings.TrimSpace(in.DefaultModel); m != "" {
		t := p.Transport
		if pre, ok := proxy.PresetByID(p.Preset); ok {
			t = pre.TransportFor(m)
		}
		def = &runtime.ModelRef{Provider: p.ID, Model: m, Transport: t}
		out.Default = &Default{Provider: p.ID, Model: m, Transport: Transport(t)}
	}
	if call.DryRun {
		return out, nil
	}
	if err := d.Providers.Put(ctx, p); err != nil {
		return SetProviderOut{}, err
	}
	if def != nil {
		if err := call.DB.SetSetting(ctx, runtime.DefaultModelSetting, def); err != nil {
			return SetProviderOut{}, err
		}
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (d Deps) updateProvider(ctx context.Context, call *ops.Call, in UpdateProviderIn) (Provider, error) {
	if err := ownerOnly(call); err != nil {
		return Provider{}, err
	}
	if err := d.ready(); err != nil {
		return Provider{}, err
	}
	p, err := d.Providers.Get(ctx, in.ID)
	if errors.Is(err, proxy.ErrNotFound) {
		return Provider{}, ops.NotFound("沒有這個供應商：" + in.ID)
	}
	if err != nil {
		return Provider{}, err
	}
	if in.Label != nil {
		p.Label = strings.TrimSpace(*in.Label)
	}
	if in.BaseURL != nil {
		base, err := proxy.NormalizeBaseURL(*in.BaseURL)
		if err != nil {
			return Provider{}, ops.InvalidInput(ops.FieldError{Field: "base_url", Message: err.Error()})
		}
		p.BaseURL = base
	}
	if in.Transport != nil {
		p.Transport = string(*in.Transport)
	}
	if in.APIKey != nil {
		p.APIKey = proxy.Secret(strings.TrimSpace(*in.APIKey))
	}
	if call.DryRun {
		return viewOf(p), nil
	}
	if err := d.Providers.Put(ctx, p); err != nil {
		return Provider{}, err
	}
	return viewOf(p), nil
}

func (d Deps) removeProvider(ctx context.Context, call *ops.Call, in RemoveProviderIn) (RemoveProviderOut, error) {
	if err := d.ready(); err != nil {
		return RemoveProviderOut{}, err
	}
	if _, err := d.Providers.Get(ctx, in.ID); errors.Is(err, proxy.ErrNotFound) {
		return RemoveProviderOut{}, ops.NotFound("沒有這個供應商：" + in.ID)
	} else if err != nil {
		return RemoveProviderOut{}, err
	}
	var def, fb runtime.ModelRef
	if _, err := call.DB.GetSettingInto(ctx, runtime.DefaultModelSetting, &def); err != nil {
		return RemoveProviderOut{}, err
	}
	if def.Provider == in.ID {
		return RemoveProviderOut{}, ops.Conflict("預設模型用的是這個供應商，先用 xbot.models.set_default 換一個")
	}
	if _, err := call.DB.GetSettingInto(ctx, runtime.FallbackModelSetting, &fb); err != nil {
		return RemoveProviderOut{}, err
	}
	out := RemoveProviderOut{ID: in.ID, AffectedBots: []string{}, ClearedFallback: fb.Provider == in.ID}
	bots, err := call.DB.ListBots(ctx, true)
	if err != nil {
		return RemoveProviderOut{}, err
	}
	for _, b := range bots {
		uses := strings.HasPrefix(b.Model, in.ID+"/")
		for _, f := range b.Fallback {
			uses = uses || strings.HasPrefix(f, in.ID+"/")
		}
		if uses {
			out.AffectedBots = append(out.AffectedBots, b.ID)
		}
	}
	if call.DryRun {
		return out, nil
	}
	if err := d.Providers.Delete(ctx, in.ID); err != nil {
		return RemoveProviderOut{}, err
	}
	if out.ClearedFallback {
		if err := call.DB.DeleteSetting(ctx, runtime.FallbackModelSetting); err != nil {
			return RemoveProviderOut{}, err
		}
	}
	out.Removed = true
	return out, nil
}

func (d Deps) setFallback(ctx context.Context, call *ops.Call, in SetFallbackIn) (SetFallbackOut, error) {
	if err := d.ready(); err != nil {
		return SetFallbackOut{}, err
	}
	provider, model := strings.TrimSpace(in.Provider), strings.TrimSpace(in.Model)
	if provider == "" && model == "" {
		if call.DryRun {
			return SetFallbackOut{}, nil
		}
		return SetFallbackOut{}, call.DB.DeleteSetting(ctx, runtime.FallbackModelSetting)
	}
	if model == "" {
		return SetFallbackOut{}, ops.InvalidInput(ops.FieldError{Field: "model", Message: "不能是空的"})
	}
	ids, err := d.Lister.ProviderIDs(ctx)
	if err != nil {
		return SetFallbackOut{}, err
	}
	if !slices.Contains(ids, provider) {
		return SetFallbackOut{}, ops.InvalidInput(ops.FieldError{Field: "provider", Message: "還沒設定這個供應商"})
	}
	ref := runtime.ModelRef{Provider: provider, Model: model, Transport: string(in.Transport)}
	out := SetFallbackOut{Fallback: &Default{Provider: provider, Model: model, Transport: in.Transport}}
	if call.DryRun {
		return out, nil
	}
	return out, call.DB.SetSetting(ctx, runtime.FallbackModelSetting, ref)
}
