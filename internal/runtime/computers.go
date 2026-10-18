package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"xbot/internal/box"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/engines"
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

// Computers 把 Bot 的電腦準備好給引擎用：開機前寫 Hermes 設定、開 product.sock、放（或清掉）訂閱 CLI 的憑證；
// 開機後回傳 Hermes 的位址，或在電腦裡開 ACP agent 行程。
// 它的 Prepare 給 box.Manager（Options.Prepare），Connect 給 Hermes 驅動，LaunchACP、MCPServers 給 ACP 驅動。
type Computers struct {
	DB        *store.DB
	Boxes     *box.Manager
	Keys      *proxy.WorkerKeys
	Sockets   *ipc.Server
	Providers ProviderLister
	// Hosts 依 host.id 找主機（ACP 驅動要用那台主機的 Docker API 開行程）。
	Hosts box.Hosts
	// Creds 管訂閱 CLI 的憑證（EN-2b）；nil 時不放憑證。
	Creds *engines.Service
	// XBotMCP 是給 ACP 引擎的 XBot MCP（經 product.sock，C11）；還沒有時是 nil，只給 desktop_mcp。
	XBotMCP *acp.MCPServer
}

// ResolveModel 決定 Bot 要用的模型：bot.model 寫成「<供應商>/<模型>」且供應商已設定時用它；
// 只寫模型時用預設供應商；空白時用 setting 的 model.default。
// 呼叫方式：就是預設模型且有設定時用預設的；否則看探測結果、範本與供應商設定（TransportSource）。
//
// ACP 引擎（Claude Code、Codex CLI）不走 Model Proxy：bot.model 原樣交給 agent（空白＝agent 自己的預設）。
func (c *Computers) ResolveModel(ctx context.Context, b store.Bot) (ModelRef, error) {
	if acp.IsACP(b.Engine) {
		return ModelRef{Model: strings.TrimSpace(b.Model)}, nil
	}
	return c.resolveHermes(ctx, b)
}

func (c *Computers) resolveHermes(ctx context.Context, b store.Bot) (ModelRef, error) {
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

// Prepare 實作 box.PrepareFunc：開 product.sock（本機主機），把 Hermes 設定與 worker-key 寫進 hermes volume，
// 並依同意紀錄放入（或清掉）訂閱 CLI 的憑證。ACP 引擎的 Bot 沒有設定模型時不寫 Hermes 設定（用不到）。
func (c *Computers) Prepare(ctx context.Context, h hosts.Host, botID, containerID string) error {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if c.Creds != nil {
		if err := c.Creds.Prepare(ctx, engines.Container{API: h.Docker(), ID: containerID}, b); err != nil {
			return err
		}
	}
	ref, err := c.resolveHermes(ctx, b)
	if err != nil {
		if acp.IsACP(b.Engine) && errors.Is(err, ErrNoModel) {
			return c.ensureSocket(h, botID)
		}
		return err
	}
	key, err := c.Keys.Issue(ctx, botID)
	if err != nil {
		return err
	}
	if err := c.ensureSocket(h, botID); err != nil {
		return err
	}
	return hermes.Provision(ctx, h.Docker(), containerID, hermes.Profile{
		BotID: botID, Name: b.Name, Title: b.Title, Description: b.Description, Soul: b.Soul,
		ProviderID: ref.Provider, Model: ref.Model, Transport: ref.Transport, Effort: b.Effort,
		WorkerKey: string(key), UID: box.BotUID, GID: box.BotGID, ApprovalMode: b.ApprovalMode,
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

func (c *Computers) ensureSocket(h hosts.Host, botID string) error {
	if h.Kind() == "local" && c.Sockets != nil {
		// 遠端主機的 product.sock 由 SSH 反向轉發提供（B11）。
		return c.Sockets.Ensure(botID)
	}
	return nil
}

// LaunchACP 實作 acp.Launcher：開機（含 Prepare）、等桌面就緒，在 Bot 的電腦裡以 Bot 使用者開 agent 行程。
func (c *Computers) LaunchACP(ctx context.Context, botID string, a acp.Agent) (acp.Process, error) {
	if _, err := c.Boxes.Start(ctx, botID); err != nil {
		return nil, err
	}
	s, err := c.Boxes.WaitReady(ctx, botID)
	if err != nil {
		return nil, err
	}
	if s.ContainerID == "" {
		return nil, errors.New("找不到 Bot 的電腦")
	}
	h, err := c.Hosts.Get(s.HostID)
	if err != nil {
		return nil, err
	}
	return acp.Exec(ctx, h.Docker(), acp.ExecSpec{
		ContainerID: s.ContainerID,
		User:        strconv.Itoa(box.BotUID) + ":" + strconv.Itoa(box.BotGID),
		Home:        box.HomeDir,
	}, a)
}

// MCPServers 回傳交給 ACP 引擎的 MCP 伺服器：Bot 電腦裡的 desktop_mcp（和 Hermes 同一個指令，EN-2c），
// 有 XBot MCP 時一併給。
func (c *Computers) MCPServers(botID string) []acp.MCPServer {
	out := []acp.MCPServer{{Name: "computer", Command: hermes.Python, Args: []string{hermes.DesktopMCP}, Env: []acp.EnvVar{}}}
	if c.XBotMCP != nil {
		x := *c.XBotMCP
		x.Env = slices.Clone(x.Env)
		// 沒設定模型的 ACP Bot 不會寫 Hermes 設定（也就沒有 /hermes/.worker-key）：直接把這個 Bot 的 worker-key 交給橋接。
		if c.Keys != nil {
			if key, err := c.Keys.Issue(context.Background(), botID); err == nil {
				x.Env = append(x.Env, acp.EnvVar{Name: "XBOT_WORKER_KEY", Value: string(key)})
			}
		}
		out = append(out, x)
	}
	return out
}

// XBotMCPServer 是給 ACP 引擎的 XBot MCP（C11）：Bot 電腦裡的 stdio 橋接 box/xbot_mcp.py，經 product.sock 以這個 Bot 的身分呼叫。
// 和 Hermes 設定裡的 mcp_servers.xbot 是同一個指令。
func XBotMCPServer() *acp.MCPServer {
	return &acp.MCPServer{Name: hermes.XBotMCPServer, Command: hermes.Python, Args: []string{hermes.XBotMCP},
		Env: []acp.EnvVar{{Name: "HERMES_HOME", Value: hermes.HomeDir}, {Name: "XBOT_PRODUCT_SOCK", Value: box.IPCDir + "/" + ipc.SocketName}}}
}

// Locate 實作 engines.Service.Locate：找 Bot 的電腦（不開機；沒有電腦時 ID 空白）。
func (c *Computers) Locate(ctx context.Context, botID string) (engines.Container, error) {
	if _, err := c.DB.GetBox(ctx, botID); errors.Is(err, store.ErrNotFound) {
		return engines.Container{}, nil
	}
	s, err := c.Boxes.Status(ctx, botID)
	if err != nil {
		return engines.Container{}, err
	}
	if s.ContainerID == "" {
		return engines.Container{}, nil
	}
	h, err := c.Hosts.Get(s.HostID)
	if err != nil {
		return engines.Container{}, fmt.Errorf("這個 Bot 的電腦所在的主機連不上：%w", err)
	}
	return engines.Container{API: h.Docker(), ID: s.ContainerID, Running: s.Running}, nil
}

// EngineOf 讀 Bot 的引擎（engine.Router 用）。
func (c *Computers) EngineOf(ctx context.Context, botID string) (string, error) {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return "", err
	}
	return b.Engine, nil
}
