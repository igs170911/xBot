package runtime

import (
	"context"
	"log/slog"
	"slices"

	"xbot/internal/engine/hermes"
	"xbot/internal/proxy"
)

// 看圖（2026-10-20）：Bot 的主要模型看不懂圖片時，Hermes 先請另一個看得懂的模型描述使用者附的圖片（auxiliary.vision）。

// ModelCatalog 讀供應商探測到的模型與設定；*proxy.DBStore 符合。
type ModelCatalog interface {
	Probe(ctx context.Context, providerID string) (proxy.ProbeResult, bool, error)
	GetProvider(ctx context.Context, id string) (proxy.ProviderRecord, error)
}

// visionAux 挑一個看得懂圖片的模型。先挑確定叫得動的：ChatGPT 方案（訂閱，不另外花錢）、預設模型、備援模型、探測過可以用的；
// 最後才是只在清單上的（例：OpenCode Go 列了 claude-haiku-5-5，但用 chat/completions 叫會回 ModelProtocolUnsupported）。
// 同一層裡便宜的先（描述一張圖不需要大模型）。都沒有時回 nil。
func (c *Computers) visionAux(ctx context.Context) *hermes.AuxModel {
	const (
		fromPlan = iota
		fromDefault
		fromFallback
		fromChecked
		fromListed
	)
	type cand struct {
		ref        ModelRef
		from, rank int
	}
	var cands []cand
	add := func(ref ModelRef, from int) {
		if r := proxy.VisionRank(ref.Model); r >= 0 && ref.Provider != "" {
			cands = append(cands, cand{ref, from, r})
		}
	}
	for key, from := range map[string]int{DefaultModelSetting: fromDefault, FallbackModelSetting: fromFallback} {
		var ref ModelRef
		if ok, err := c.DB.GetSettingInto(ctx, key, &ref); err == nil && ok {
			add(ref, from)
		}
	}
	ids := []string{}
	if c.Providers != nil {
		var err error
		if ids, err = c.Providers.ProviderIDs(ctx); err != nil {
			slog.Warn("讀不到模型供應商，看圖模型只從預設與備援挑", "err", err)
		}
	}
	slices.Sort(ids)
	if cat, ok := c.Providers.(ModelCatalog); ok {
		for _, id := range ids {
			rec, err := cat.GetProvider(ctx, id)
			if err != nil {
				continue
			}
			pr, probed, err := cat.Probe(ctx, id)
			if err != nil {
				continue
			}
			models := pr.Models
			if len(models) == 0 {
				if pre, ok := proxy.PresetByID(rec.Preset); ok {
					models = pre.Models
				} else if rec.Auth == proxy.AuthOAuthCodex {
					models = proxy.CodexModels
				}
			}
			for _, m := range models {
				from := fromListed
				switch {
				case rec.Auth == proxy.AuthOAuthCodex:
					from = fromPlan
				case probed && pr.Checks[m].Status == proxy.CheckOK:
					from = fromChecked
				}
				add(ModelRef{Provider: id, Model: m}, from)
			}
		}
	}
	// 供應商已經不在的（預設模型指到刪掉的供應商）不用。
	cands = slices.DeleteFunc(cands, func(x cand) bool { return len(ids) > 0 && !slices.Contains(ids, x.ref.Provider) })
	if len(cands) == 0 {
		return nil
	}
	slices.SortStableFunc(cands, func(a, b cand) int {
		if a.from != b.from {
			return a.from - b.from
		}
		return a.rank - b.rank
	})
	best := cands[0].ref
	if ts, ok := c.Providers.(TransportSource); ok && best.Transport == "" {
		if t, err := ts.TransportFor(ctx, best.Provider, best.Model); err == nil {
			best.Transport = t
		}
	}
	return &hermes.AuxModel{ProviderID: best.Provider, Model: best.Model, Transport: best.Transport}
}

// VisionRoute 實作 proxy.VisionBinder：主要模型看不懂圖片時，Model Proxy 讓這個 Bot 呼叫 XBot 替它挑的看圖模型（和寫進
// Hermes 設定的 auxiliary.vision 同一個）。主要模型看得懂時回 nil。
func (c *Computers) VisionRoute(ctx context.Context, botID string) (*proxy.Route, error) {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	ref, err := c.ResolveModel(ctx, b)
	if err != nil || proxy.SeesImages(ref.Model) {
		return nil, err
	}
	a := c.visionAux(ctx)
	if a == nil {
		return nil, nil
	}
	return &proxy.Route{Provider: a.ProviderID, Model: a.Model, Transport: a.Transport}, nil
}
