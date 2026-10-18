package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"

	"xbot/internal/box"
	"xbot/internal/engine/hermes"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

// DefaultModelSetting 是預設模型在 setting 表的 key，值為 ModelRef 的 JSON。
const DefaultModelSetting = "model.default"

// FallbackModelSetting 是全域備援模型在 setting 表的 key（ModelRef 的 JSON）。
// 主要模型回 429／5xx 或連不上、而且還沒送出任何位元組時，Model Proxy 改用它重試一次（EN-5）。
// Bot 自己的 fallback 欄位（「<供應商>/<模型>」）優先。
const FallbackModelSetting = "model.fallback"

// ModelRef 指定一個模型：哪個供應商（Model Proxy 的代號）的哪個模型。
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string `json:"transport,omitempty"`
}

// ErrNoModel 表示還沒設定模型。
var ErrNoModel = errors.New("還沒設定模型：請先設定模型供應商與預設模型")

// ProviderLister 列出已設定的供應商；*proxy.DBStore 符合。
type ProviderLister interface {
	ProviderIDs(ctx context.Context) ([]string, error)
}

// TransportSource 決定模型的呼叫方式（探測結果、範本、供應商設定）；*proxy.DBStore 符合。
// Providers 有實作它時，ResolveModel 用它補上呼叫方式。
type TransportSource interface {
	TransportFor(ctx context.Context, providerID, model string) (string, error)
}

// Computers 把 Bot 的電腦準備好給引擎用：開機前寫 Hermes 設定、開 product.sock；開機後回傳 Hermes 的位址。
// 它的 Prepare 給 box.Manager（Options.Prepare），Connect 給 Hermes 驅動。
type Computers struct {
	DB        *store.DB
	Boxes     *box.Manager
	Keys      *proxy.WorkerKeys
	Sockets   *ipc.Server
	Providers ProviderLister
}

// ResolveModel 決定 Bot 要用的模型：bot.model 寫成「<供應商>/<模型>」且供應商已設定時用它；
// 只寫模型時用預設供應商；空白時用 setting 的 model.default。
// 呼叫方式：就是預設模型且有設定時用預設的；否則看探測結果、範本與供應商設定（TransportSource）。
func (c *Computers) ResolveModel(ctx context.Context, b store.Bot) (ModelRef, error) {
	var def ModelRef
	if _, err := c.DB.GetSettingInto(ctx, DefaultModelSetting, &def); err != nil {
		return def, err
	}
	ref, err := c.resolve(ctx, def, b.Model)
	if err != nil {
		return ref, err
	}
	if ref.Provider == def.Provider && ref.Model == def.Model && def.Transport != "" {
		ref.Transport = def.Transport
		return ref, nil
	}
	if ts, ok := c.Providers.(TransportSource); ok {
		t, err := ts.TransportFor(ctx, ref.Provider, ref.Model)
		if err != nil {
			return ref, err
		}
		if t != "" {
			ref.Transport = t
		}
	}
	return ref, nil
}

func (c *Computers) resolve(ctx context.Context, def ModelRef, model string) (ModelRef, error) {
	m := strings.TrimSpace(model)
	if m == "" {
		if def.Provider == "" || def.Model == "" {
			return def, ErrNoModel
		}
		return def, nil
	}
	if ref, ok, err := c.splitModel(ctx, m); err != nil || ok {
		if ok && def.Provider == ref.Provider {
			ref.Transport = def.Transport
		}
		return ref, err
	}
	if def.Provider == "" {
		return def, ErrNoModel
	}
	return ModelRef{Provider: def.Provider, Model: m, Transport: def.Transport}, nil
}

// splitModel 把「<供應商>/<模型>」拆開；供應商沒設定時 ok 為 false（整串當模型名）。
func (c *Computers) splitModel(ctx context.Context, m string) (ModelRef, bool, error) {
	p, rest, ok := strings.Cut(m, "/")
	if !ok || rest == "" || c.Providers == nil {
		return ModelRef{}, false, nil
	}
	ids, err := c.Providers.ProviderIDs(ctx)
	if err != nil {
		return ModelRef{}, false, err
	}
	if !slices.Contains(ids, p) {
		return ModelRef{}, false, nil
	}
	return ModelRef{Provider: p, Model: rest}, true, nil
}

// Binding 實作 proxy.Binder：Bot 只能用它的模型所屬的供應商（E01 待確認 #1）；
// 備援模型是 Bot 的 fallback 第一項（「<供應商>/<模型>」），沒有時用全域的 model.fallback。
func (c *Computers) Binding(ctx context.Context, botID string) (proxy.Binding, error) {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return proxy.Binding{}, err
	}
	ref, err := c.ResolveModel(ctx, b)
	if err != nil {
		return proxy.Binding{}, err
	}
	out := proxy.Binding{Provider: ref.Provider}
	fb, ok, err := c.fallback(ctx, b)
	if err != nil || !ok {
		return out, err
	}
	if fb.Transport == "" {
		if ts, ok := c.Providers.(TransportSource); ok {
			if fb.Transport, err = ts.TransportFor(ctx, fb.Provider, fb.Model); err != nil {
				return out, err
			}
		}
	}
	out.Fallback = &proxy.Route{Provider: fb.Provider, Model: fb.Model, Transport: fb.Transport}
	return out, nil
}

func (c *Computers) fallback(ctx context.Context, b store.Bot) (ModelRef, bool, error) {
	for _, f := range b.Fallback {
		ref, ok, err := c.splitModel(ctx, strings.TrimSpace(f))
		if err != nil || ok {
			return ref, ok, err
		}
	}
	var def ModelRef
	if _, err := c.DB.GetSettingInto(ctx, FallbackModelSetting, &def); err != nil {
		return def, false, err
	}
	if def.Provider == "" || def.Model == "" {
		return def, false, nil
	}
	if c.Providers != nil {
		ids, err := c.Providers.ProviderIDs(ctx)
		if err != nil || !slices.Contains(ids, def.Provider) {
			return def, false, err
		}
	}
	return def, true, nil
}

// Prepare 實作 box.PrepareFunc：開 product.sock（本機主機），把 Hermes 設定與 worker-key 寫進 hermes volume。
func (c *Computers) Prepare(ctx context.Context, h hosts.Host, botID, containerID string) error {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	ref, err := c.ResolveModel(ctx, b)
	if err != nil {
		return err
	}
	key, err := c.Keys.Issue(ctx, botID)
	if err != nil {
		return err
	}
	if h.Kind() == "local" && c.Sockets != nil {
		// 遠端主機的 product.sock 由 SSH 反向轉發提供（B11）。
		if err := c.Sockets.Ensure(botID); err != nil {
			return err
		}
	}
	return hermes.Provision(ctx, h.Docker(), containerID, hermes.Profile{
		BotID: botID, Name: b.Name, Title: b.Title, Description: b.Description, Soul: b.Soul,
		ProviderID: ref.Provider, Model: ref.Model, Transport: ref.Transport, Effort: b.Effort,
		WorkerKey: string(key), UID: box.BotUID, GID: box.BotGID,
	})
}

// Connect 實作 hermes.Connector：開機（含 Prepare）、等桌面與 Hermes 就緒，回傳 Hermes 的位址與 worker-key。
func (c *Computers) Connect(ctx context.Context, botID string) (hermes.Endpoint, error) {
	if _, err := c.Boxes.Start(ctx, botID); err != nil {
		return hermes.Endpoint{}, err
	}
	s, err := c.Boxes.WaitReady(ctx, botID)
	if err != nil {
		return hermes.Endpoint{}, err
	}
	if s.HermesAddr == "" {
		return hermes.Endpoint{}, errors.New("電腦沒有發布 Hermes 的埠")
	}
	key, err := c.Keys.Issue(ctx, botID)
	if err != nil {
		return hermes.Endpoint{}, err
	}
	return hermes.Endpoint{Addr: s.HermesAddr, Key: string(key)}, nil
}
