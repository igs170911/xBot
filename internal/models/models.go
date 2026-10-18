// Package models 是模型設定的操作（PRD §6.12、EN-3～EN-5、MCP-11）：
// models.get、models.presets、models.set_provider、models.add_provider、models.update_provider、models.remove_provider、
// models.set_default、models.set_fallback、models.probe、models.pricing、models.set_price。
// 供應商的金鑰只能寫、不能讀：存進 Model Proxy（加密），任何操作都只回「已設定」。
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

// Providers 存取供應商設定；*proxy.Providers 符合。
type Providers interface {
	Put(ctx context.Context, p proxy.Provider) error
	Get(ctx context.Context, id string) (proxy.Provider, error)
	Delete(ctx context.Context, id string) error
}

// Lister 列出已設定的供應商代號、存取探測結果；*proxy.DBStore 符合。
type Lister interface {
	ProviderIDs(ctx context.Context) ([]string, error)
	Probe(ctx context.Context, providerID string) (proxy.ProbeResult, bool, error)
	PutProbe(ctx context.Context, r proxy.ProbeResult) error
}

// Prober 對供應商送探測請求；*proxy.Prober 符合。
type Prober interface {
	ListModels(ctx context.Context, p proxy.Provider) ([]string, string)
	CheckModel(ctx context.Context, p proxy.Provider, model, guess string) proxy.ModelCheck
}

// Deps 是模型操作需要的服務元件；nil 時（指令列只產生說明）呼叫會回 internal。
type Deps struct {
	Providers Providers
	Lister    Lister
	// Prober 預設是 &proxy.Prober{}（真的連到供應商）。
	Prober Prober
}

// Transport 是模型的呼叫方式（E02 runtime.ModelRef.Transport）。
type Transport string

// EnumValues 讓 JSON Schema 列出可用值。
func (Transport) EnumValues() []string {
	return []string{"chat_completions", "codex_responses", "anthropic_messages"}
}

// Provider 是對外的供應商設定（不含金鑰）。
type Provider struct {
	ID        string    `json:"id"`
	Label     string    `json:"label,omitempty"`
	BaseURL   string    `json:"base_url"`
	Transport Transport `json:"transport,omitempty" desc:"預設的呼叫方式；空白是 chat_completions"`
	Preset    string    `json:"preset,omitempty" desc:"從哪個內建範本建立"`
	APIKeySet bool      `json:"api_key_set" desc:"有沒有設定金鑰；金鑰本身永遠不會回傳"`
	Auth      string    `json:"auth,omitempty" desc:"登入方式：空白是金鑰；oauth_codex 是 ChatGPT 方案的訂閱登入（權杖只在服務端）"`
	Models    []string  `json:"models,omitempty" desc:"探測時供應商列出的模型（沒探測過時是範本的常用模型）"`
	ProbedAt  int64     `json:"probed_at,omitempty" desc:"上次探測的時間；0 表示還沒探測"`
}

// Default 是預設模型。
type Default struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Transport Transport `json:"transport,omitempty"`
}

// GetIn 是 xbot.models.get 的輸入。
type GetIn struct{}

// GetOut 是 xbot.models.get 的輸出。
type GetOut struct {
	Providers []Provider `json:"providers"`
	Default   *Default   `json:"default,omitempty" desc:"沒設定預設模型時不出現"`
	Fallback  *Default   `json:"fallback,omitempty" desc:"全域備援模型；沒設定時不出現"`
	Ready     bool       `json:"ready" desc:"預設模型和它的供應商都設好了，新 Bot 可以直接對話"`
}

// SetProviderIn 是 xbot.models.set_provider 的輸入。
type SetProviderIn struct {
	ID           string    `json:"id" required:"true" max:"32" desc:"供應商代號（小寫英文、數字、-），例：openai；Bot 的電腦經 /model/<代號>/v1 使用"`
	BaseURL      string    `json:"base_url" required:"true" max:"500" desc:"OpenAI 相容的服務網址，例：https://api.openai.com/v1"`
	APIKey       string    `json:"api_key,omitempty" secret:"true" max:"4000" desc:"金鑰（只能寫、不能讀）；不填時保留原本的金鑰"`
	DefaultModel string    `json:"default_model,omitempty" max:"200" desc:"順便把這個模型設成預設模型"`
	Transport    Transport `json:"transport,omitempty" desc:"設預設模型時的呼叫方式；預設 chat_completions"`
}

// SetProviderOut 是 xbot.models.set_provider 的輸出。
type SetProviderOut struct {
	Provider Provider `json:"provider"`
	Default  *Default `json:"default,omitempty"`
}

// SetDefaultIn 是 xbot.models.set_default 的輸入。
type SetDefaultIn struct {
	Provider  string    `json:"provider" required:"true" max:"32" desc:"已設定的供應商代號"`
	Model     string    `json:"model" required:"true" max:"200" desc:"模型名稱，例：gpt-4.1-mini"`
	Transport Transport `json:"transport,omitempty" desc:"預設 chat_completions"`
}

// Register 登錄模型設定的操作。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[GetIn, GetOut]{
		Name: "xbot.models.get", Summary: "看模型設定（供應商與預設模型，不含金鑰）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.get,
	})
	ops.Register(reg, ops.Op[SetProviderIn, SetProviderOut]{
		Name: "xbot.models.set_provider", Summary: "設定一個模型供應商（網址與金鑰）",
		Description: "金鑰加密存在服務端的 Model Proxy，不會進 Bot 的電腦，也不會再回傳（MCP-11）。" +
			"可以同時用 default_model 設定預設模型。只有你本人能設定。在指令列建議用 --json - 從標準輸入給金鑰，避免留在指令紀錄裡。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.setProvider,
	})
	ops.Register(reg, ops.Op[SetDefaultIn, Default]{
		Name: "xbot.models.set_default", Summary: "設定預設模型",
		Description: "沒有指定模型的 Bot 用這個模型。改了之後，新的對話串就會用新的模型。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.setDefault,
	})
	registerProviders(reg, d)
	registerProbe(reg, d)
	registerOAuth(reg, d)
	registerPricing(reg)
}

// IsClaudeOAuth 判斷金鑰是不是 Claude 訂閱（Claude Code／claude.ai 登入）的 OAuth 權杖：
// access token 是 sk-ant-oat…、refresh token 是 sk-ant-ort…；一般 API 金鑰是 sk-ant-api…。
func IsClaudeOAuth(key string) bool {
	return strings.HasPrefix(key, "sk-ant-oat") || strings.HasPrefix(key, "sk-ant-ort")
}

func (d Deps) ready() error {
	if d.Providers == nil || d.Lister == nil {
		return ops.Internal(errors.New("模型代理（Model Proxy）沒有啟動"))
	}
	return nil
}

func (d Deps) provider(ctx context.Context, id string) (Provider, bool, error) {
	p, err := d.Providers.Get(ctx, id)
	if errors.Is(err, proxy.ErrNotFound) {
		return Provider{ID: id}, false, nil
	}
	if err != nil {
		return Provider{}, false, err
	}
	out := viewOf(p)
	probe, ok, err := d.Lister.Probe(ctx, id)
	if err != nil {
		return Provider{}, false, err
	}
	if ok {
		out.ProbedAt = probe.At
		out.Models = probe.Models
	}
	if len(out.Models) == 0 {
		if pre, ok := proxy.PresetByID(p.Preset); ok {
			out.Models = pre.Models
		} else if p.Auth == proxy.AuthOAuthCodex {
			out.Models = proxy.CodexModels
		}
	}
	return out, true, nil
}

func viewOf(p proxy.Provider) Provider {
	return Provider{ID: p.ID, Label: p.Label, BaseURL: p.BaseURL, Transport: Transport(p.Transport), Preset: p.Preset, APIKeySet: p.APIKey != "",
		Auth: p.Auth}
}

func (d Deps) get(ctx context.Context, call *ops.Call, _ GetIn) (GetOut, error) {
	if err := d.ready(); err != nil {
		return GetOut{}, err
	}
	ids, err := d.Lister.ProviderIDs(ctx)
	if err != nil {
		return GetOut{}, err
	}
	slices.Sort(ids)
	out := GetOut{Providers: []Provider{}}
	for _, id := range ids {
		p, ok, err := d.provider(ctx, id)
		if err != nil {
			return GetOut{}, err
		}
		if ok {
			out.Providers = append(out.Providers, p)
		}
	}
	var def runtime.ModelRef
	ok, err := call.DB.GetSettingInto(ctx, runtime.DefaultModelSetting, &def)
	if err != nil {
		return GetOut{}, err
	}
	if ok && def.Model != "" {
		out.Default = &Default{Provider: def.Provider, Model: def.Model, Transport: Transport(def.Transport)}
		out.Ready = slices.Contains(ids, def.Provider)
	}
	var fb runtime.ModelRef
	if ok, err := call.DB.GetSettingInto(ctx, runtime.FallbackModelSetting, &fb); err != nil {
		return GetOut{}, err
	} else if ok && fb.Model != "" {
		out.Fallback = &Default{Provider: fb.Provider, Model: fb.Model, Transport: Transport(fb.Transport)}
	}
	return out, nil
}

func (d Deps) setProvider(ctx context.Context, call *ops.Call, in SetProviderIn) (SetProviderOut, error) {
	if call.Identity.Kind != ops.KindOwner {
		return SetProviderOut{}, ops.Forbidden("只有你本人能設定模型供應商的金鑰")
	}
	if err := d.ready(); err != nil {
		return SetProviderOut{}, err
	}
	id := strings.TrimSpace(in.ID)
	if !proxy.ValidProviderID(id) {
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "id", Message: "只能用小寫英文、數字和 -（最多 32 字）"})
	}
	base, err := proxy.NormalizeBaseURL(in.BaseURL)
	if err != nil {
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "base_url", Message: err.Error()})
	}
	old, err := d.Providers.Get(ctx, id)
	if err != nil && !errors.Is(err, proxy.ErrNotFound) {
		return SetProviderOut{}, err
	}
	if old.Auth != "" {
		return SetProviderOut{}, ops.Conflict("這個供應商用訂閱登入，要換帳號請用 xbot.models.oauth_start")
	}
	key := proxy.Secret(strings.TrimSpace(in.APIKey))
	if IsClaudeOAuth(string(key)) {
		// EN-2a：Claude 訂閱只經 Claude Code 本身使用，不讓 Hermes（經 Model Proxy）借用它的登入權杖。
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "api_key",
			Message: "這是 Claude 訂閱的登入權杖，不能給 Hermes 用。要用 Claude 訂閱，請把 Bot 的引擎改成 Claude Code，再同意連結訂閱（xbot.engines.link_credentials）；或改用 Anthropic API 金鑰"})
	}
	if key == "" {
		// 沒給金鑰：保留原本的（只改網址）。
		key = old.APIKey
	}
	next := proxy.Provider{ID: id, BaseURL: base, APIKey: key, Label: old.Label, Transport: old.Transport, Preset: old.Preset}
	out := SetProviderOut{Provider: viewOf(next)}
	var def *runtime.ModelRef
	if m := strings.TrimSpace(in.DefaultModel); m != "" {
		def = &runtime.ModelRef{Provider: id, Model: m, Transport: string(in.Transport)}
		out.Default = &Default{Provider: id, Model: m, Transport: in.Transport}
	} else if in.Transport != "" {
		return SetProviderOut{}, ops.InvalidInput(ops.FieldError{Field: "transport", Message: "要和 default_model 一起給"})
	}
	if call.DryRun {
		return out, nil
	}
	if err := d.Providers.Put(ctx, next); err != nil {
		return SetProviderOut{}, err
	}
	if def != nil {
		if err := call.DB.SetSetting(ctx, runtime.DefaultModelSetting, def); err != nil {
			return SetProviderOut{}, err
		}
	}
	return out, nil
}

func (d Deps) setDefault(ctx context.Context, call *ops.Call, in SetDefaultIn) (Default, error) {
	if err := d.ready(); err != nil {
		return Default{}, err
	}
	ids, err := d.Lister.ProviderIDs(ctx)
	if err != nil {
		return Default{}, err
	}
	if !slices.Contains(ids, in.Provider) {
		return Default{}, ops.InvalidInput(ops.FieldError{Field: "provider", Message: "還沒設定這個供應商（先用 xbot.models.set_provider）"})
	}
	out := Default{Provider: in.Provider, Model: strings.TrimSpace(in.Model), Transport: in.Transport}
	if out.Model == "" {
		return Default{}, ops.InvalidInput(ops.FieldError{Field: "model", Message: "不能是空的"})
	}
	if call.DryRun {
		return out, nil
	}
	err = call.DB.SetSetting(ctx, runtime.DefaultModelSetting, runtime.ModelRef{Provider: out.Provider, Model: out.Model, Transport: string(out.Transport)})
	return out, err
}
