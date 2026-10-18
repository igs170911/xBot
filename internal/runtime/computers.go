package runtime

import (
	"context"
	"errors"
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
func (c *Computers) ResolveModel(ctx context.Context, b store.Bot) (ModelRef, error) {
	var def ModelRef
	if _, err := c.DB.GetSettingInto(ctx, DefaultModelSetting, &def); err != nil {
		return def, err
	}
	m := strings.TrimSpace(b.Model)
	if m == "" {
		if def.Provider == "" || def.Model == "" {
			return def, ErrNoModel
		}
		return def, nil
	}
	if p, rest, ok := strings.Cut(m, "/"); ok && rest != "" && c.Providers != nil {
		ids, err := c.Providers.ProviderIDs(ctx)
		if err != nil {
			return def, err
		}
		for _, id := range ids {
			if id == p {
				ref := ModelRef{Provider: p, Model: rest}
				if def.Provider == p {
					ref.Transport = def.Transport
				}
				return ref, nil
			}
		}
	}
	if def.Provider == "" {
		return def, ErrNoModel
	}
	return ModelRef{Provider: def.Provider, Model: m, Transport: def.Transport}, nil
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
