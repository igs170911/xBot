package models

import (
	"context"
	"errors"
	"strings"
	"time"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
)

// ProbeIn 是 xbot.models.probe 的輸入。
type ProbeIn struct {
	Provider string `json:"provider" required:"true" max:"32" desc:"已設定的供應商代號"`
	Model    string `json:"model,omitempty" max:"200" desc:"要測的模型；不填時測預設模型（預設模型不是這個供應商的，就測範本建議的模型）"`
}

// ProbeOut 是 xbot.models.probe 的輸出。
type ProbeOut struct {
	Provider  string            `json:"provider"`
	Models    []string          `json:"models" desc:"供應商 /models 列出的模型"`
	ListError string            `json:"list_error,omitempty" desc:"列模型失敗的原因"`
	Check     *proxy.ModelCheck `json:"check,omitempty" desc:"這次測的模型能不能用、用哪種呼叫方式"`
	// DefaultUpdated：測的是預設模型、而且找到能用的呼叫方式，已經更新預設模型的呼叫方式。
	DefaultUpdated bool  `json:"default_updated"`
	At             int64 `json:"at"`
}

func registerProbe(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[ProbeIn, ProbeOut]{
		Name: "xbot.models.probe", Summary: "探測模型供應商：列出模型、找出能用的呼叫方式",
		Description: "讀供應商的模型清單，並對一個模型依序試 Chat Completions、Responses、Anthropic Messages（各送一個很小的請求），" +
			"記下能用的那一種（EN-4）。之後 Bot 用這個模型時就照它設定。會花一點點 token。預演不會連到供應商。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: d.probe,
	})
}

func (d Deps) prober() Prober {
	if d.Prober != nil {
		return d.Prober
	}
	return &proxy.Prober{}
}

func (d Deps) probe(ctx context.Context, call *ops.Call, in ProbeIn) (ProbeOut, error) {
	if err := d.ready(); err != nil {
		return ProbeOut{}, err
	}
	p, err := d.Providers.Get(ctx, in.Provider)
	if errors.Is(err, proxy.ErrNotFound) {
		return ProbeOut{}, ops.NotFound("沒有這個供應商：" + in.Provider)
	}
	if err != nil {
		return ProbeOut{}, err
	}
	if p.Auth != "" {
		// 訂閱登入只有 Codex 的 responses 端點，呼叫方式固定，不用試；只更新這個帳號能用的模型。
		out := ProbeOut{Provider: p.ID, Models: []string{}, At: time.Now().UnixMilli()}
		if call.DryRun {
			return out, nil
		}
		out.Models, out.ListError, err = d.listOAuthModels(ctx, p)
		return out, err
	}
	var def runtime.ModelRef
	if _, err := call.DB.GetSettingInto(ctx, runtime.DefaultModelSetting, &def); err != nil {
		return ProbeOut{}, err
	}
	model := strings.TrimSpace(in.Model)
	if model == "" && def.Provider == p.ID {
		model = def.Model
	}
	pre, hasPreset := proxy.PresetByID(p.Preset)
	if model == "" && hasPreset {
		model = pre.SuggestedModel()
	}
	out := ProbeOut{Provider: p.ID, Models: []string{}, At: time.Now().UnixMilli()}
	if call.DryRun {
		return out, nil
	}
	pr := d.prober()
	models, listErr := pr.ListModels(ctx, p)
	out.Models, out.ListError = nonNil(models), listErr

	prev, _, err := d.Lister.Probe(ctx, p.ID)
	if err != nil {
		return ProbeOut{}, err
	}
	res := proxy.ProbeResult{Provider: p.ID, At: out.At, Models: out.Models, ListError: listErr, Checks: prev.Checks}
	if res.Checks == nil {
		res.Checks = map[string]proxy.ModelCheck{}
	}
	if model != "" {
		guess := proxy.RuleTransport(p.ID, p.BaseURL, model)
		if guess == "" && hasPreset {
			guess = pre.TransportFor(model)
		}
		if guess == "" {
			guess = cmpOr(p.Transport, proxy.TransportChat)
		}
		c := pr.CheckModel(ctx, p, model, guess)
		out.Check = &c
		res.Checks[model] = c
		if c.Status == proxy.CheckOK && def.Provider == p.ID && def.Model == model && def.Transport != c.Transport {
			def.Transport = c.Transport
			if err := call.DB.SetSetting(ctx, runtime.DefaultModelSetting, def); err != nil {
				return ProbeOut{}, err
			}
			out.DefaultUpdated = true
		}
	}
	if err := d.Lister.PutProbe(ctx, res); err != nil {
		return ProbeOut{}, err
	}
	return out, nil
}

// Freshener 換新訂閱登入的權杖；*proxy.Providers 符合。
type Freshener interface {
	Fresh(ctx context.Context, pr proxy.Provider, force bool) (proxy.Provider, error)
}

// listOAuthModels 讀訂閱登入（ChatGPT 方案）這個帳號真的能用的模型並記下來（權杖快到期時先換新）。
// 2026-10-20：原本只用寫死的清單（proxy.CodexModels），裡面的 gpt-5.4-mini 這個帳號一用就 400。
func (d Deps) listOAuthModels(ctx context.Context, p proxy.Provider) ([]string, string, error) {
	if f, ok := d.Providers.(Freshener); ok {
		fresh, err := f.Fresh(ctx, p, false)
		if err != nil {
			return []string{}, "登入要換新但失敗了：" + err.Error(), nil
		}
		p = fresh
	}
	models, listErr := d.prober().ListModels(ctx, p)
	prev, _, err := d.Lister.Probe(ctx, p.ID)
	if err != nil {
		return nil, "", err
	}
	if len(models) == 0 && len(prev.Models) > 0 {
		models = prev.Models // 這次讀不到：留著上次的清單
	}
	res := proxy.ProbeResult{Provider: p.ID, At: time.Now().UnixMilli(), Models: nonNil(models), ListError: listErr, Checks: prev.Checks}
	if res.Checks == nil {
		res.Checks = map[string]proxy.ModelCheck{}
	}
	return res.Models, listErr, d.Lister.PutProbe(ctx, res)
}
