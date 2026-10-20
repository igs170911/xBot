package main

import (
	"context"
	"net/http"
	"slices"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/hermes"
	"xbot/internal/engines"
	"xbot/internal/lessons"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// EN-7 的接線：Model Proxy 看到的供應商錯誤記進 QuotaLog；額度卡的選項列出 Hermes 能換用的模型。

// providerError 把供應商的錯誤回應分類，額度類的記下來（runtime 收掉失敗的一輪時用）。
func providerError(log *engine.QuotaLog) func(botID, provider string, status int, h http.Header, body []byte) {
	return func(botID, provider string, status int, h http.Header, body []byte) {
		if q := engine.ClassifyHTTP(status, h, body, time.Now()); q != nil {
			q.Engine, q.Provider = hermes.Name, provider
			log.Record(botID, q)
		}
	}
}

// modelChoices 列出 Hermes 可以換用的模型：預設模型、全域備援，再來是每個供應商建議的模型。
func modelChoices(db *store.DB, ps *proxy.DBStore, providers *proxy.Providers) func(ctx context.Context) ([]engines.ModelChoice, error) {
	return func(ctx context.Context) ([]engines.ModelChoice, error) {
		ids, err := ps.ProviderIDs(ctx)
		if err != nil {
			return nil, err
		}
		slices.Sort(ids)
		labels := map[string]string{}
		suggested := map[string]string{}
		for _, id := range ids {
			p, err := providers.Get(ctx, id)
			if err != nil {
				continue
			}
			labels[id] = p.Label
			pre, ok := proxy.PresetByID(p.Preset)
			if ok && labels[id] == "" {
				labels[id] = pre.Label
			}
			switch {
			case ok && pre.DefaultModel != "":
				suggested[id] = pre.DefaultModel
			case ok && len(pre.Models) > 0:
				suggested[id] = pre.Models[0]
			case p.Auth == proxy.AuthOAuthCodex && len(proxy.CodexModels) > 0:
				suggested[id] = proxy.CodexModels[0]
			default:
				if r, ok, _ := ps.Probe(ctx, id); ok && len(r.Models) > 0 {
					suggested[id] = r.Models[0]
				}
			}
		}
		var out []engines.ModelChoice
		add := func(provider, model string) {
			if model == "" || !slices.Contains(ids, provider) {
				return
			}
			out = append(out, engines.ModelChoice{Provider: provider, Model: model, ProviderLabel: labels[provider]})
		}
		for _, key := range []string{runtime.DefaultModelSetting, runtime.FallbackModelSetting} {
			var ref runtime.ModelRef
			if ok, err := db.GetSettingInto(ctx, key, &ref); err == nil && ok {
				add(ref.Provider, ref.Model)
			}
		}
		for _, id := range ids {
			add(id, suggested[id])
		}
		return out, nil
	}
}

// teacherModel 挑「學到的事」的老師模型：有 ChatGPT 方案時用它最好的模型（月費方案，不另外花錢；
// 清單照帳號列的優先順序），沒有時用預設模型。
func teacherModel(db *store.DB, ps *proxy.DBStore) func(ctx context.Context) (lessons.Model, bool) {
	return func(ctx context.Context) (lessons.Model, bool) {
		ids, err := ps.ProviderIDs(ctx)
		if err != nil {
			return lessons.Model{}, false
		}
		slices.Sort(ids)
		for _, id := range ids {
			rec, err := ps.GetProvider(ctx, id)
			if err != nil || rec.Auth != proxy.AuthOAuthCodex {
				continue
			}
			models := proxy.CodexModels
			if pr, ok, err := ps.Probe(ctx, id); err == nil && ok && len(pr.Models) > 0 {
				models = pr.Models
			}
			if len(models) > 0 {
				return lessons.Model{Provider: id, Model: models[0], Transport: proxy.TransportResponses}, true
			}
		}
		var def runtime.ModelRef
		if ok, err := db.GetSettingInto(ctx, runtime.DefaultModelSetting, &def); err != nil || !ok || def.Provider == "" || def.Model == "" {
			return lessons.Model{}, false
		}
		if def.Transport == "" {
			def.Transport, _ = ps.TransportFor(ctx, def.Provider, def.Model)
		}
		return lessons.Model{Provider: def.Provider, Model: def.Model, Transport: def.Transport}, true
	}
}
