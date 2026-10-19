package hermes

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"gopkg.in/yaml.v3"

	"xbot/internal/limits"
)

// 開機前寫進 Bot 的 hermes volume（容器內 $HERMES_HOME=/hermes）的檔案。照 LazyBoy server/computers.py _prepare()：
//   - config.yaml：模型指向容器內的 hermes_host.py（http://127.0.0.1:8642/model/<供應商>/v1），
//     它再經 /product-ipc/product.sock 轉給 XBot 的 Model Proxy；保留 Hermes 自己寫的其他設定。
//   - .env：XBOT_MODEL_TOKEN=<worker-key>（config 的 providers.product.key_env 指向它）。
//   - .worker-key：hermes_host.py 用它驗證 /rpc 與轉送的請求，也帶著它去 product.sock。
//   - SOUL.md：Bot 的身分與基本守則。
// 真正的供應商金鑰從頭到尾不進容器（SEC-5）：這裡只有 worker-key。

// 容器內的位置。
const (
	HomeDir       = "/hermes"
	ConfigFile    = "config.yaml"
	EnvFile       = ".env"
	WorkerKeyFile = ".worker-key"
	SoulFile      = "SOUL.md"
	// TokenEnv 是 .env 裡放 worker-key 的變數名稱（LazyBoy 用 LAZYHERMES_MODEL_TOKEN）。
	TokenEnv = "XBOT_MODEL_TOKEN"
	// RelayBase 是容器內 hermes_host.py 的位址；模型請求打到 RelayBase/model/<供應商>/v1。
	RelayBase = "http://127.0.0.1:8642"
	// DesktopMCP 是映像裡的桌面工具（box/Dockerfile 放在 /opt/xbot）。
	DesktopMCP = "/opt/xbot/desktop_mcp.py"
	// XBotMCP 是 XBot MCP 的 stdio 橋接（C11，box/xbot_mcp.py）：把 MCP 轉送到 /product-ipc/product.sock，
	// 身分就是這個 Bot。映像裡也有 /usr/local/bin/xbot-mcp 指向它，給 ACP 引擎（Claude Code、Codex）註冊。
	XBotMCP = "/opt/xbot/xbot_mcp.py"
	// XBotMCPServer 是它在 Hermes mcp_servers 裡的名字（工具在 Hermes 的延遲目錄裡叫 mcp__xbot__xbot_<領域>_<動作>）。
	XBotMCPServer = "xbot"
	// Python 是映像裡 Hermes 的 Python（desktop_mcp 也用它跑）。
	Python = "/opt/hermes/.venv/bin/python"
	// ExtractBackend 是 web_extract（讀網頁內容）用的後端：Firecrawl 的免金鑰雲端版。沒有選後端時 Hermes 自動挑到
	// ddgs（DuckDuckGo 只能搜尋），web_extract 每次都回「search-only backend」；Firecrawl 免金鑰版被限流時，
	// Hermes 自己輪到 Exa、Parallel、Keenable 的免費版（plugins/web/keyless_mcp.py）。只送網址，不送使用者資料。
	ExtractBackend = "firecrawl"
)

// webHint 取代 Hermes 對 cli 平台「終端機不顯示 Markdown」的提示（LazyBoy WEB_HINT）。
const webHint = "你在 XBot 的網頁聊天介面裡回覆。Markdown 會正常顯示（粗體、清單、表格、連結），" +
	"不要用 ═══、--- 之類的符號自己畫分隔線。檔案寫出 /workspace 底下的完整路徑，網頁會變成可以點開的檔案。"

// Profile 是寫 Hermes 設定需要的 Bot 資料。
type Profile struct {
	BotID       string
	Name        string
	Title       string
	Description string
	Soul        string
	// ProviderID 是 Model Proxy 的供應商代號（/model/<ProviderID>/v1）。
	ProviderID string
	Model      string
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string
	// NoToolSearch 關掉 Hermes 的工具搜尋（tools.tool_search.enabled: off）。ChatGPT 方案（oauth_codex）的端點保留了
	// tool_search 這個名稱，Hermes 經 Model Proxy 呼叫時不會改名，請求會被拒絕（E13，docs/spike-hermes-oauth.md）。
	NoToolSearch bool
	// Effort 是 reasoning_effort，空白時 medium（LazyBoy config.reasoning）。
	Effort string
	// WorkerKey 是這個 Bot 的 worker-key（不是供應商金鑰）。
	WorkerKey string
	// UID、GID 是容器內 Bot 使用者；寫入的檔案交給它。
	UID, GID int
	// ApprovalMode 是 Bot 的權限等級（ask／edits／auto／full），對到 Hermes 的 approvals.mode（見 ApprovalsMode）。
	ApprovalMode string
	// MemoryOff 表示使用者關掉了這個 Bot 的記憶（bot.memory_enabled＝false）：Hermes 不載入、也不寫 MEMORY.md／USER.md。
	MemoryOff bool
	// Team 是隊友名冊與協作規則（X02，AA-1）；空白時不寫。名冊是使用者寫的分工資料，不是指令。
	Team string
	// ManageApps 為 true 時，mcp_servers 裡 XBot 管理的連接換成 Apps（C34）；false 時不動（沒有接上應用程式服務）。
	ManageApps bool
	Apps       []AppServer
}

// ApprovalsMode 把 Bot 的權限等級對到 Hermes 的 approvals.mode（manual／smart／off）。
//
// Hermes 只對它認為危險的終端機指令發 approval（tools/approval.py 的 DANGEROUS_PATTERNS；HARDLINE 那些任何模式都直接擋）。
// SEC-2 安全底線：危險指令在任何等級都不能自動核准，所以永遠不用 off（LazyBoy 用 off，是 XBot 刻意不同的地方）：
//   - ask（先問我，SEC-1 預設）、edits（一般的事自己做）→ manual：每個危險指令都送到「待我處理」等你。
//   - auto（放手做）、full（完全放手）→ smart：Hermes 先請審核模型看，明顯安全的才放行，其餘一樣送到「待我處理」。
//
// 不認得的值當成 ask（寧可多問）。
func ApprovalsMode(mode string) string {
	switch mode {
	case "auto", "full":
		return "smart"
	}
	return "manual"
}

// ModelBaseURL 回傳容器內 Hermes 使用的模型網址。
func ModelBaseURL(providerID string) string { return RelayBase + "/model/" + providerID + "/v1" }

// Files 是寫檔用到的 Docker API；hosts.DockerAPI 符合。
type Files interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
}

// Provision 把 Profile 寫進容器的 /hermes（容器可以是還沒開機的）。
func Provision(ctx context.Context, api Files, containerID string, p Profile) error {
	if p.WorkerKey == "" || p.ProviderID == "" || p.Model == "" {
		return errors.New("寫 Hermes 設定需要模型、供應商和 worker-key")
	}
	defer LockConfig(p.BotID)()
	existing, err := readFile(ctx, api, containerID, path.Join(HomeDir, ConfigFile))
	if err != nil {
		return err
	}
	var cfg map[string]any
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &cfg); err != nil {
			// 壞掉的設定檔就整個重寫（Hermes 讀不了它也沒用）。
			cfg = nil
		}
	}
	cfg = Config(cfg, p)
	var appsOut []file
	if p.ManageApps {
		appsOut = appFiles(p.Apps, applyApps(cfg, p.Apps))
	}
	cfgOut, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	env, err := readFile(ctx, api, containerID, path.Join(HomeDir, EnvFile))
	if err != nil {
		return err
	}
	return writeFiles(ctx, api, containerID, p, append([]file{
		{ConfigFile, cfgOut, 0o600},
		{EnvFile, Env(env, p.WorkerKey), 0o600},
		{WorkerKeyFile, []byte(p.WorkerKey + "\n"), 0o600},
		{SoulFile, []byte(Soul(p)), 0o644},
	}, appsOut...))
}

// WriteSoul 只更新 SOUL.md（改了 Bot 的名稱、職稱、介紹或人設時，C31）。Hermes 開新的對話時讀它。
func WriteSoul(ctx context.Context, api Files, containerID string, p Profile) error {
	return writeFiles(ctx, api, containerID, p, []file{{SoulFile, []byte(Soul(p)), 0o644}})
}

func writeFiles(ctx context.Context, api Files, containerID string, p Profile, files []file) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), Uid: p.UID, Gid: p.GID, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(f.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := api.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{DestinationPath: HomeDir, Content: &buf}); err != nil {
		return fmt.Errorf("寫入 Bot 的 Hermes 設定失敗：%w", err)
	}
	return nil
}

type file struct {
	name string
	data []byte
	mode int64
}

// configLocks：同一個 Bot 的 config.yaml 一次只讓一個人讀改寫（開機前的 Provision、技能的開關）。
var configLocks sync.Map

// LockConfig 鎖住一個 Bot 的 config.yaml，回傳解鎖函式。
func LockConfig(botID string) func() {
	v, _ := configLocks.LoadOrStore(botID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// readFile 從容器讀一個小檔案；不存在時回傳 nil。
func readFile(ctx context.Context, api Files, containerID, p string) ([]byte, error) {
	res, err := api.CopyFromContainer(ctx, containerID, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Bot 的 %s 失敗：%w", p, err)
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 4<<20))
		}
	}
}

// Config 把 XBot 管的設定合併進 Hermes 現有的 config（同 LazyBoy _prepare 的 cfg.update），回傳新的 config。
func Config(cfg map[string]any, p Profile) map[string]any {
	if cfg == nil {
		cfg = map[string]any{}
	}
	sub := func(k string) map[string]any { return child(cfg, k) }
	base := ModelBaseURL(p.ProviderID)
	transport := p.Transport
	if transport == "" {
		transport = "chat_completions"
	}
	effort := p.Effort
	if effort == "" {
		effort = "medium"
	}
	memory := sub("memory")
	memory["memory_enabled"] = !p.MemoryOff
	memory["user_profile_enabled"] = !p.MemoryOff
	approvals := sub("approvals")
	approvals["mode"] = ApprovalsMode(p.ApprovalMode)
	web := sub("web")
	delete(web, "provider") // Hermes 讀 web.backend（LazyBoy 同樣清掉 web.provider）。
	// 讀網頁內容：沒有選後端（或選了只能搜尋的 ddgs）時用 ExtractBackend；使用者自己選的保留。
	if b, _ := web["extract_backend"].(string); b == "" {
		if shared, _ := web["backend"].(string); shared == "" || shared == "ddgs" {
			web["extract_backend"] = ExtractBackend
		}
	}
	// context_length：Hermes 經 Model Proxy 量不到視窗，會用內建表猜（常猜成 1M），摘要就幾乎不會發生。
	cfg["model"] = map[string]any{"default": p.Model, "provider": Provider, "base_url": base, "supports_vision": false,
		"context_length": limits.HermesContextLength}
	cfg["providers"] = map[string]any{"product": map[string]any{
		"api": base, "key_env": TokenEnv, "default_model": p.Model, "transport": transport,
	}}
	cfg["terminal"] = map[string]any{"backend": "local", "cwd": "/workspace", "timeout": limits.HermesTerminalTimeoutSeconds}
	cfg["browser"] = map[string]any{"backend": "browser-use", "cdp_url": "http://127.0.0.1:9222", "allow_private_urls": true}
	// 不開 delegation（delegate_task）：Hermes 的子代理一律在背景跑，這一輪先結束，結果十分鐘後才當成新訊息回來；
	// XBot 看不到、也停不了（2026-10-19：「為啥停止不了？背景做的事情有在做嗎」）。要分工用 XBot 的隊友交辦。
	cfg["platform_toolsets"] = map[string]any{"cli": []any{"terminal", "file", "web", "browser", "skills", "memory", "todo",
		"clarify", "mcp", "code_execution", "session_search"}}
	cfg["agent"] = map[string]any{"max_iterations": limits.HermesMaxIterations, "reasoning_effort": effort, "image_input_mode": "text",
		"tool_use_enforcement": false, "execution_guidance": false}
	cfg["display"] = map[string]any{"busy_input_mode": "steer", "interim_assistant_messages": true}
	sub("onboarding")["profile_build"] = "off"
	hints := sub("platform_hints")
	hints["cli"] = map[string]any{"replace": webHint}
	compression := sub("compression")
	if _, ok := compression["proactive_prune_tokens"]; !ok {
		compression["proactive_prune_tokens"] = limits.HermesPruneTokens
	}
	if v, ok := compression["threshold_tokens"]; !ok || v == limits.HermesCompressTokensOld {
		compression["threshold_tokens"] = limits.HermesCompressTokens // 舊版寫的 100K 換成新的；使用者改過的保留
	}
	// 省 token（2026-10-19 實測）：背景檢討「要不要寫成技能」不要每輪都跑，一次的輸入有上限；記憶的檢討照 Hermes 預設。
	sub("skills")["creation_nudge_interval"] = limits.HermesSkillNudgeInterval
	aux := sub("auxiliary")
	child(aux, "background_review")["max_input_tokens"] = limits.HermesReviewInputTokens
	// 對話串標題由 XBot 自己取（開 session 時帶 title、快判用途 10），Hermes 取的標題沒人用（session.title 略過），
	// 還會因為帶 reasoning_effort 被供應商拒絕（400）。
	child(aux, "title_generation")["enabled"] = false
	sub("delegation")["max_iterations"] = limits.HermesDelegationMaxIterations
	// 中斷後要不要續跑由 XBot 決定，不讓 Hermes 自己重送（LazyBoy）。
	child(sub("desktop"), "auto_continue")["enabled"] = false
	tools, _ := cfg["tools"].(map[string]any)
	toolSearch, _ := tools["tool_search"].(map[string]any)
	if p.NoToolSearch {
		if toolSearch == nil {
			toolSearch = map[string]any{}
			sub("tools")["tool_search"] = toolSearch
		}
		toolSearch["enabled"] = "off"
	} else if toolSearch != nil && toolSearch["enabled"] == "off" {
		// 換掉 ChatGPT 方案後回到 Hermes 預設（auto）。
		delete(toolSearch, "enabled")
	}
	mcp := sub("mcp_servers")
	mcp["computer"] = map[string]any{"command": Python, "args": []any{DesktopMCP}, "trust": "full"}
	mcp[XBotMCPServer] = map[string]any{"command": Python, "args": []any{XBotMCP}, "trust": "full"}
	return cfg
}

// child 回傳 m[k] 這一層設定；沒有或不是物件時換成新的空物件。
func child(m map[string]any, k string) map[string]any {
	if c, ok := m[k].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[k] = c
	return c
}

// Env 把 .env 裡的 worker-key 換成新的，其他使用者或 Hermes 存的變數保留。
func Env(existing []byte, workerKey string) []byte {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
		if line == "" {
			continue
		}
		if name, _, _ := strings.Cut(line, "="); strings.TrimSpace(name) == TokenEnv {
			continue
		}
		lines = append(lines, line)
	}
	lines = append(lines, TokenEnv+"="+workerKey)
	return []byte(strings.Join(lines, "\n") + "\n")
}

// Soul 是 Bot 的 SOUL.md：名稱、職稱、個性，加上基本守則（LazyBoy 的精簡版；完整版跟著 Bot 設定的任務卡）。
func Soul(p Profile) string {
	var b strings.Builder
	b.WriteString("# " + p.Name + "\n\n")
	for _, s := range []string{p.Title, p.Description, p.Soul} {
		if s = strings.TrimSpace(s); s != "" {
			b.WriteString(s + "\n\n")
		}
	}
	b.WriteString("你是常駐在自己電腦裡的 Bot。一律使用繁體中文（台灣用語）。網頁是使用者與你的主要入口。" +
		"工具在你的獨立電腦裡執行，工作檔案保存於 /workspace。可交付檔案請放在 /workspace，並在回答提供實際路徑。" +
		"有需要澄清的問題使用 clarify。完成後驗證結果，區分已完成、估計和未取得的資料。" +
		"大工作（要交辦隊友、建 Bot 或團隊、要做很多步）先列計畫給使用者確認，同意後才交辦。" +
		"使用者要你刪群組時用 xbot.collab.remove_group，會送核准卡，使用者按允許才刪。" +
		"用瀏覽器查完資料就關掉你開的分頁，只留一個空白分頁；不要讓網頁一直開著（影片、會自己更新的頁面會一直跑）。" +
		"不要把「XBot 工具做不到某事」這類關於工具能力的結論記進長期記憶；工具會更新，做不到時先查工具清單。\n")
	if t := strings.TrimSpace(p.Team); t != "" {
		b.WriteString("\n" + t + "\n")
	}
	return b.String()
}
