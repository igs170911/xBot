package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/engines"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/proxy"
	"xbot/internal/skills"
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

// AuthSource 回答供應商的登入方式（空白是金鑰；proxy.AuthOAuthCodex 是 ChatGPT 方案）；*proxy.DBStore 符合。
type AuthSource interface {
	ProviderAuth(ctx context.Context, providerID string) (string, error)
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
	// Roster 是 Bot 系統提示裡的隊友段落（X02，collab.RosterText）；nil 時不寫。
	Roster func(ctx context.Context, botID string) (string, error)
	// Apps 回傳 Bot 能用的連接在電腦裡怎麼啟動（C34，apps.Service.Servers）；nil 時不管 mcp_servers 裡的連接。
	Apps func(ctx context.Context, botID string) ([]hermes.AppServer, error)
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

// Prepare 實作 box.PrepareFunc：開本機的 product.sock（遠端主機經 SSH 反向轉發接回來），把 Hermes 設定與 worker-key 寫進 hermes volume，
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
	// XBot 內建技能只放一次（C31，SK-3）；放不進去不擋開機。
	if err := skills.Seed(ctx, c.fs(h, containerID, false)); err != nil {
		slog.Warn("放入內建技能失敗", "bot", botID, "err", err)
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
	team := ""
	if c.Roster != nil {
		if team, err = c.Roster(ctx, botID); err != nil {
			return err
		}
	}
	p := hermes.Profile{
		BotID: botID, Name: b.Name, Title: b.Title, Description: b.Description, Soul: b.Soul, Team: team,
		ProviderID: ref.Provider, Model: ref.Model, Transport: ref.Transport, Effort: b.Effort,
		WorkerKey: string(key), UID: box.BotUID, GID: box.BotGID, ApprovalMode: b.ApprovalMode, MemoryOff: !b.MemoryEnabled,
	}
	if as, ok := c.Providers.(AuthSource); ok {
		auth, err := as.ProviderAuth(ctx, ref.Provider)
		if err != nil {
			return err
		}
		p.NoToolSearch = auth == proxy.AuthOAuthCodex
	}
	if c.Apps != nil {
		apps, err := c.Apps(ctx, botID)
		if err != nil {
			// 讀不到連接不擋開機：這次先不動 mcp_servers 裡的連接。
			slog.Warn("讀不到 Bot 能用的應用程式", "bot", botID, "err", err)
		} else {
			p.ManageApps, p.Apps = true, apps
		}
	}
	return hermes.Provision(ctx, h.Docker(), containerID, p)
}

func (c *Computers) fs(h hosts.Host, containerID string, running bool) *boxfs.Docker {
	return &boxfs.Docker{API: h.Docker(), ID: containerID, UID: box.BotUID, GID: box.BotGID, Running: running}
}

// BotFS 實作 boxfs.Locator：Bot 電腦裡的檔案（技能、記憶，C31）。不開機；create 為 true 且還沒有電腦時先建立（不開機）。
// 要刪檔時（Docker 沒有刪檔的 API）才開機。
func (c *Computers) BotFS(ctx context.Context, botID string, create bool) (boxfs.FS, error) {
	var s box.Status
	var err error
	if _, gerr := c.DB.GetBox(ctx, botID); errors.Is(gerr, store.ErrNotFound) && !create {
		return nil, boxfs.ErrNoComputer
	}
	if create {
		s, err = c.Boxes.Create(ctx, botID)
	} else {
		s, err = c.Boxes.Status(ctx, botID)
	}
	if err != nil {
		return nil, err
	}
	if s.ContainerID == "" {
		return nil, boxfs.ErrNoComputer
	}
	h, err := c.Hosts.Get(s.HostID)
	if err != nil {
		return nil, fmt.Errorf("這個 Bot 的電腦所在的主機連不上：%w", err)
	}
	f := c.fs(h, s.ContainerID, s.Running)
	f.Wake = func(ctx context.Context) (string, error) {
		st, err := c.Boxes.Start(ctx, botID)
		return st.ContainerID, err
	}
	return f, nil
}

// RefreshSoul 在 Bot 的名稱、職稱、介紹或人設改了以後，把新的 SOUL.md 寫進它的電腦（有電腦才寫；沒開機也可以）。
// Hermes 開新的對話時讀它；開機前的 Prepare 也會再寫一次。
func (c *Computers) RefreshSoul(ctx context.Context, botID string) error {
	loc, err := c.Locate(ctx, botID)
	if err != nil || loc.ID == "" {
		return err
	}
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	h, err := c.hostOf(ctx, botID)
	if err != nil {
		return err
	}
	team := ""
	if c.Roster != nil {
		if team, err = c.Roster(ctx, botID); err != nil {
			return err
		}
	}
	return hermes.WriteSoul(ctx, h.Docker(), loc.ID, hermes.Profile{BotID: botID, Name: b.Name, Title: b.Title, Description: b.Description,
		Soul: b.Soul, Team: team, UID: box.BotUID, GID: box.BotGID})
}

func (c *Computers) hostOf(ctx context.Context, botID string) (hosts.Host, error) {
	bx, err := c.DB.GetBox(ctx, botID)
	if err != nil {
		return nil, err
	}
	return c.Hosts.Get(bx.HostID)
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
	if c.Sockets != nil {
		// 本機的 product.sock 一律在服務這邊聽；遠端主機上的那個 socket 由 SSH 反向轉發接回這裡（B11）。
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
	if c.Apps != nil {
		apps, err := c.Apps(context.Background(), botID)
		if err != nil {
			slog.Warn("讀不到 Bot 能用的應用程式", "bot", botID, "err", err)
		}
		for _, a := range apps {
			cmd, args := a.Launch()
			env := []acp.EnvVar{}
			for _, k := range slices.Sorted(maps.Keys(a.Env)) {
				env = append(env, acp.EnvVar{Name: k, Value: a.Env[k]})
			}
			out = append(out, acp.MCPServer{Name: a.Name, Command: cmd, Args: args, Env: env})
		}
	}
	return out
}

// RefreshApps 在 Bot 能用的連接改變後（C34）：有電腦就重寫 config.yaml 的連接與 env 檔（沒開機也可以）。
// 本機程式的祕密 env 檔只在這裡與開機前寫；ACP 引擎在下一個 session 拿到新的清單。
func (c *Computers) RefreshApps(ctx context.Context, botID string) error {
	if c.Apps == nil {
		return nil
	}
	loc, err := c.Locate(ctx, botID)
	if err != nil || loc.ID == "" {
		return err
	}
	apps, err := c.Apps(ctx, botID)
	if err != nil {
		return err
	}
	h, err := c.hostOf(ctx, botID)
	if err != nil {
		return err
	}
	return hermes.WriteApps(ctx, h.Docker(), loc.ID, botID, box.BotUID, box.BotGID, apps)
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

// Docker 找 Bot 的電腦所在主機的 Docker API 與容器（不開機；沒有電腦時 id 空白）。協作層讀寫 /workspace 用（X02）。
func (c *Computers) Docker(ctx context.Context, botID string) (hosts.DockerAPI, string, error) {
	if _, err := c.DB.GetBox(ctx, botID); errors.Is(err, store.ErrNotFound) {
		return nil, "", nil
	}
	s, err := c.Boxes.Status(ctx, botID)
	if err != nil || s.ContainerID == "" {
		return nil, "", err
	}
	h, err := c.Hosts.Get(s.HostID)
	if err != nil {
		return nil, "", fmt.Errorf("這個 Bot 的電腦所在的主機連不上：%w", err)
	}
	return h.Docker(), s.ContainerID, nil
}

// EngineOf 讀 Bot 的引擎（engine.Router 用）。
func (c *Computers) EngineOf(ctx context.Context, botID string) (string, error) {
	b, err := c.DB.GetBot(ctx, botID)
	if err != nil {
		return "", err
	}
	return b.Engine, nil
}
