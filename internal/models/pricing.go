package models

import (
	"context"
	"maps"
	"slices"
	"strings"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// PriceRow 是價目表的一列（美元／每百萬 token）。
type PriceRow struct {
	Model      string  `json:"model" desc:"模型名稱，或「<供應商>/<模型>」只套用在那個供應商"`
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read" desc:"快取命中；0 表示照輸入價"`
	CacheWrite float64 `json:"cache_write" desc:"寫入快取；0 表示照輸入價"`
	Source     string  `json:"source" enum:"custom,default" desc:"custom：你設定的；default：內建"`
}

// PricingIn 是 xbot.models.pricing 的輸入。
type PricingIn struct{}

// PricingOut 是 xbot.models.pricing 的輸出。
type PricingOut struct {
	Prices   []PriceRow            `json:"prices" desc:"你設定的在前，內建的在後"`
	Unpriced []store.UnpricedModel `json:"unpriced" desc:"用過、但沒有價格的模型（花費記成 0），請補上價格"`
}

// SetPriceIn 是 xbot.models.set_price 的輸入。
type SetPriceIn struct {
	Model      string  `json:"model" required:"true" max:"200" desc:"模型名稱，或「<供應商>/<模型>」"`
	Input      float64 `json:"input,omitempty" min:"0" max:"10000" desc:"每百萬輸入 token 的美元"`
	Output     float64 `json:"output,omitempty" min:"0" max:"10000" desc:"每百萬輸出 token 的美元"`
	CacheRead  float64 `json:"cache_read,omitempty" min:"0" max:"10000"`
	CacheWrite float64 `json:"cache_write,omitempty" min:"0" max:"10000"`
	Remove     bool    `json:"remove,omitempty" desc:"刪掉你設定的價格，改回內建（或沒有價格）"`
}

func registerPricing(reg *ops.Registry) {
	ops.Register(reg, ops.Op[PricingIn, PricingOut]{
		Name: "xbot.models.pricing", Summary: "看模型價目表",
		Description: "用量的花費照這張表算（供應商自己回報花費時用回報的）。不在表上的模型花費記成 0，列在 unpriced。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: pricing,
	})
	ops.Register(reg, ops.Op[SetPriceIn, PriceRow]{
		Name: "xbot.models.set_price", Summary: "設定一個模型的價格",
		Description: "價格會影響花費上限的計算（把價格調低等於放寬上限），所以要你核准，也不能代為核准。之後的用量才會照新價格算。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: setPrice,
	})
}

func pricing(ctx context.Context, call *ops.Call, _ PricingIn) (PricingOut, error) {
	custom, err := (&proxy.Pricing{DB: call.DB}).Custom(ctx)
	if err != nil {
		return PricingOut{}, err
	}
	out := PricingOut{Prices: []PriceRow{}}
	for _, k := range slices.Sorted(maps.Keys(custom)) {
		out.Prices = append(out.Prices, row(k, custom[k], proxy.PriceSourceCustom))
	}
	defaults := proxy.DefaultPrices()
	for _, k := range slices.Sorted(maps.Keys(defaults)) {
		out.Prices = append(out.Prices, row(k, defaults[k], proxy.PriceSourceDefault))
	}
	// 只列「現在」仍查不到價格的（補了價格之後，舊的紀錄不會再出現）。
	unpriced, err := call.DB.UnpricedModels(ctx, store.UsageFilter{})
	if err != nil {
		return PricingOut{}, err
	}
	out.Unpriced = []store.UnpricedModel{}
	for _, u := range unpriced {
		if _, _, ok := proxy.LookupPrice(custom, u.Provider, u.Model); !ok {
			out.Unpriced = append(out.Unpriced, u)
		}
	}
	return out, nil
}

func row(model string, p proxy.Price, src string) PriceRow {
	return PriceRow{Model: model, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite, Source: src}
}

func setPrice(ctx context.Context, call *ops.Call, in SetPriceIn) (PriceRow, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return PriceRow{}, ops.InvalidInput(ops.FieldError{Field: "model", Message: "不能是空的"})
	}
	p := proxy.Price{Input: in.Input, Output: in.Output, CacheRead: in.CacheRead, CacheWrite: in.CacheWrite}
	if !proxy.ValidPrice(p) {
		return PriceRow{}, ops.InvalidInput(ops.FieldError{Field: "input", Message: "價格要是 0 以上的數字"})
	}
	custom, err := (&proxy.Pricing{DB: call.DB}).Custom(ctx)
	if err != nil {
		return PriceRow{}, err
	}
	out := row(model, p, proxy.PriceSourceCustom)
	if in.Remove {
		delete(custom, model)
		if d, src, ok := proxy.LookupPrice(custom, "", model); ok {
			out = row(model, d, src)
		} else {
			out = PriceRow{Model: model, Source: proxy.PriceSourceDefault}
		}
	} else {
		custom[model] = p
	}
	if call.DryRun {
		return out, nil
	}
	return out, call.DB.SetSetting(ctx, proxy.PricingSetting, custom)
}
