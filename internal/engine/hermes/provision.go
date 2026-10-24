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
	"xbot/internal/replystyle"
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
	// SearchBackend 是 web_search 用的後端：Exa 的免金鑰版（mcp.exa.ai，不用金鑰、不送使用者資料）。沒有選後端時 Hermes
	// 自動挑到 ddgs（DuckDuckGo）：常被限流，結果只有標題和一行摘要。Exa 回原文段落（2026-10-21 實測台灣的醫療問題 1.2 秒，
	// 回長庚、台大的原文），被限流時 Hermes 自己輪到 Parallel、Firecrawl、Keenable 的免費版。使用者要求串 Exa（2026-10-21）。
	SearchBackend = "exa"
)

// AuxModel 是 Hermes 輔助工作（例：看圖）用的另一個模型，經 Model Proxy 呼叫。
type AuxModel struct {
	ProviderID, Model string
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string
}

// blindHint 加在 webHint 後面：沒有看得懂圖片的模型時，照實說，不要猜。
const blindHint = "你看不到圖片內容。使用者附了圖片時直接說你看不到，請他用文字描述，或到 Bot 設定換一個看得懂圖片的模型；" +
	"不要去看自己的桌面或用工具猜圖片裡有什麼。"

// webHint 取代 Hermes 對 cli 平台「終端機不顯示 Markdown」的提示（LazyBoy WEB_HINT）。
const webHint = "你在 XBot 的網頁聊天介面裡回覆。Markdown 會正常顯示（清單、表格、連結；標題和粗體照回話風格不用），" +
	"不要用 ═══、--- 之類的符號自己畫分隔線。檔案寫出 /workspace 底下的完整路徑，網頁會變成可以點開的檔案。"

// Profile 是寫 Hermes 設定需要的 Bot 資料。
type Profile struct {
	BotID       string
	Name        string
	Title       string
	Description string
	Soul        string
	// ReplyStyle 是回話風格（空白＝簡短）。
	ReplyStyle ReplyStyle
	// ProviderID 是 Model Proxy 的供應商代號（/model/<ProviderID>/v1）。
	ProviderID string
	Model      string
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string
	// NoToolSearch 關掉 Hermes 的工具搜尋（tools.tool_search.enabled: off）。ChatGPT 方案（oauth_codex）的端點保留了
	// tool_search 這個名稱，Hermes 經 Model Proxy 呼叫時不會改名，請求會被拒絕（E13，docs/spike-hermes-oauth.md）。
	NoToolSearch bool
	// Vision：主要模型看得懂圖片（proxy.SeesImages）：使用者附的圖片直接給它（image_input_mode native），也開 vision 工具。
	Vision bool
	// VisionAux 是主要模型看不懂圖片時，先描述圖片用的模型（auxiliary.vision，經 Model Proxy）；nil 表示沒有看得懂圖片的模型，
	// Bot 會直接說看不到圖片（2026-10-20：原本一律當看不懂又沒有工具，Bot 收到截圖會去看自己的桌面、回答別的東西）。
	VisionAux *AuxModel
	// Lessons 是學到的事（internal/lessons，你同意過的守則），寫在基本守則後面。
	Lessons []string
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
	// 讀網頁內容：沒有選後端（或選了只能搜尋的 ddgs）時用 ExtractBackend；搜尋：沒有選後端時用 SearchBackend。使用者自己選的保留。
	shared, _ := web["backend"].(string)
	if b, _ := web["extract_backend"].(string); b == "" && (shared == "" || shared == "ddgs") {
		web["extract_backend"] = ExtractBackend
	}
	if b, _ := web["search_backend"].(string); b == "" && shared == "" {
		web["search_backend"] = SearchBackend
	}
	// context_length：Hermes 經 Model Proxy 量不到視窗，會用內建表猜（常猜成 1M），摘要就幾乎不會發生。
	cfg["model"] = map[string]any{"default": p.Model, "provider": Provider, "base_url": base, "supports_vision": p.Vision,
		"context_length": limits.HermesContextLength}
	cfg["providers"] = map[string]any{"product": map[string]any{
		"api": base, "key_env": TokenEnv, "default_model": p.Model, "transport": transport,
	}}
	cfg["terminal"] = map[string]any{"backend": "local", "cwd": "/workspace", "timeout": limits.HermesTerminalTimeoutSeconds}
	cfg["browser"] = map[string]any{"backend": "browser-use", "cdp_url": "http://127.0.0.1:9222", "allow_private_urls": true}
	// delegation（delegate_task，2026-10-20 開回來）：容器裡的 hermes_host.py 告訴 Hermes「這個對話收不到之後才送來的結果」，
	// Hermes 就照它對無狀態頻道的做法，在同一個工具呼叫裡等子代理做完才回來（這一輪等著、停止會連子代理一起停）。
	// 2026-10-19 關掉是因為子代理一律在背景跑，這一輪先結束、結果十分鐘後才當成新訊息回來，XBot 看不到也停不了。
	toolsets := []any{"terminal", "file", "web", "browser", "skills", "memory", "todo", "clarify", "mcp", "code_execution", "session_search", "delegation"}
	// 看圖（LazyBoy 一樣只在看得懂時開）：主要模型看得懂就直接給它圖片；看不懂但有別的模型看得懂，先請那個模型描述。
	imageMode := "text"
	if p.Vision || p.VisionAux != nil {
		toolsets = append(toolsets, "vision")
	}
	if p.Vision {
		imageMode = "native"
	}
	cfg["platform_toolsets"] = map[string]any{"cli": toolsets}
	cfg["agent"] = map[string]any{"max_iterations": limits.HermesMaxIterations, "reasoning_effort": effort, "image_input_mode": imageMode,
		"tool_use_enforcement": false, "execution_guidance": false}
	cfg["display"] = map[string]any{"busy_input_mode": "steer", "interim_assistant_messages": true}
	sub("onboarding")["profile_build"] = "off"
	hints := sub("platform_hints")
	hint := webHint
	if !p.Vision && p.VisionAux == nil {
		hint += blindHint
	}
	hints["cli"] = map[string]any{"replace": hint}
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
	if a := p.VisionAux; a != nil && !p.Vision {
		v := map[string]any{"base_url": ModelBaseURL(a.ProviderID), "model": a.Model, "key_env": TokenEnv}
		if a.Transport != "" {
			v["api_mode"] = a.Transport
		}
		aux["vision"] = v
	} else {
		delete(aux, "vision") // 主要模型自己看，或沒有看得懂的模型
	}
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
	cfg[SearchSnippetKey] = limits.HermesSearchSnippetRunes
	mcp := sub("mcp_servers")
	// 桌面工具只有 desktop_observe／desktop_act；它的 FastMCP 照樣宣告 resources、prompts，Hermes 就多列 4 個用不到的附屬工具
	// （list/read_resource、list/get_prompt，每次請求約 1.2K 字元）。
	mcp["computer"] = map[string]any{"command": Python, "args": []any{DesktopMCP}, "trust": "full",
		"tools": map[string]any{"resources": false, "prompts": false}}
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

// baseRulesStart 是基本守則的第一句：Bot 把自己整份 SOUL.md 當人設存回去時，從這裡切掉（PersonaOnly）。
const baseRulesStart = "你是常駐在自己電腦裡的 Bot。"

// generatedHeads 是 SOUL.md 裡系統產生的段落標題（回覆格式、隊友名冊、組織與回報…），不屬於人設。
var generatedHeads = []string{replyStyleHead, "## 回覆使用者的格式", "## 隊友與協作", "## 組織與回報", "## 少打擾使用者", "## 需要董事長核定的事"}

// PersonaOnly 只留人設本身：拿掉系統產生的基本守則、回覆格式、隊友與組織段落（2026-10-20：Bot 用 xbot.teams.update_bot
// 把自己整份 SOUL.md 存成人設，開場和 SOUL.md 都變成兩份守則）。
func PersonaOnly(s string) string {
	cut := len(s)
	for _, m := range append([]string{baseRulesStart}, generatedHeads...) {
		if i := strings.Index(s, m); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(s[:cut])
}

// squash 拿掉所有空白，比對介紹是不是人設的開頭（介紹會被壓成一行、截短）。
func squash(s string) string { return strings.Join(strings.Fields(s), "") }

// ReplyStyle 是 Bot 回話給使用者的風格（設定 › 一般 › 回話風格，所有 Bot 共用；存在 ReplyStyleSetting）。
// 2026-10-20 使用者：回覆太長看不下去，要像人傳訊息一樣短、分段傳。原本的四段報告格式（結論／要注意／需要你回答／
// 建議下一步）會把回覆拉長，改成聊天口吻：重點還是那幾件事，但沒有就不講、不用小標。
type ReplyStyle string

const (
	ReplyBrief    ReplyStyle = "brief"    // 簡短（預設）：簡單問題三到五句；其他照 replystyle.Length
	ReplyNormal   ReplyStyle = "normal"   // 一般：簡單問題兩到四段
	ReplyDetailed ReplyStyle = "detailed" // 詳細：可以完整說明，還是先講結果、分段
)

// ReplyStyleSetting 是設定表裡回話風格的鍵。
const ReplyStyleSetting = "bots.reply_style"

// EnumValues 讓 JSON Schema 列出可用值。
func (ReplyStyle) EnumValues() []string { return []string{"brief", "normal", "detailed"} }

// Valid 回報是不是認得的風格。
func (s ReplyStyle) Valid() bool { return s == ReplyBrief || s == ReplyNormal || s == ReplyDetailed }

// replyStyleHead 是回話風格段落的標題（PersonaOnly 靠它切掉）。
const replyStyleHead = "## 回話風格"

// replyStyleText 是 SOUL.md 的回話風格段落。寫成白話、不用清單和粗體：提示的格式會被模型照抄到回覆裡。
// 2026-10-21 使用者：講話不夠自然。原本「每段 40 字以內、最多 4 段」加上固定三泡泡的範例，模型每次都寫成一句一段的電報；
// 改成照一般聊天助理的樣子：口語、句子接得順、長度跟著問題走，短和分段照舊（2026-10-20 的決定）。
// 2026-10-23：長短與先後改照 replystyle（唯一來源）：看使用者要拿這則訊息做什麼決定長短，要他決定的事不埋在最後，
// 不一步一步問「要我…嗎」（原本「值得做的下一步可以順口提一件」加上範例的結尾問句，教會它每一步都停下來問）。
func replyStyleText(s ReplyStyle) string {
	return replyStyleHead + "\n" +
		"回給使用者本人時，像一個懂行、講話自然的朋友在通訊軟體裡回訊息：口語、有溫度，句子之間接得順，" +
		"「因為」「不過」「所以」這些連接詞照常用，不要寫成電報、公文或條列報告。" +
		"段落之間空一行，每一段會變成一個對話泡泡，一段講一件事。\n" +
		"先回應他真正要的：問問題就直接給答案和理由；交代的工作就先說做好了沒、東西在哪（實際檔案路徑或連結）。" +
		"接著只補他需要知道的：沒做完、沒查到、有風險，或你替他做了什麼判斷；沒有就不講，不要每次都套同一個格式，也不要寫「無」。\n" +
		"不寫開場白、不重述問題、不客套收尾。不描述你的過程或用了哪些工具，工具之間也不要寫旁白，使用者看得到工作步驟。" +
		"不要用 # 標題和 ** 粗體，聊天訊息不需要；清單只在三項以上的並列項目或步驟時用，兩件事的比較寫成兩句話就好，表格只在比較三項以上時用。" +
		"不寫內部欄位名、工具名或英文術語，除非使用者自己這樣說。" +
		"放不放檔案，看使用者之後會不會再用：只看一次就好的（結果、狀態、8 項以內的清單、5 列以內的表格、短程式碼、連結）直接寫在對話裡；" +
		"會留著、會改、會分享的成品（報告、試算表、文件、要執行的程式），或一個畫面看不完的，存成 /workspace 裡的檔案，聊天裡只講重點和路徑。" +
		"但使用者明說要在對話裡看的（例：「整理成表格給我」），一個畫面放得下就直接放在對話裡，就算也存成了檔案。" +
		"中途抓的網頁、下載的原始資料這類過程檔放 /workspace/tmp/，過幾天會自動清掉。" +
		// 2026-10-24 使用者：「可以做網頁跟 dashboard 在對話視窗裡顯示」：.html 會在對話裡直接打開（沙盒，只准連 https）。
		"要做網頁、儀表板、互動圖表時，寫成一個 .html 檔存在 /workspace（1 MB 以內）：CSS、JavaScript 和資料都寫在同一個檔裡，" +
		"圖表可以用 https 的 CDN（例：https://cdn.jsdelivr.net/npm/chart.js），不要拆成好幾個檔，也不要連本機、內網的網址或用 fetch 讀 /workspace 的檔案（網頁讀不到）；" +
		"回覆給路徑，使用者在對話裡就會看到網頁。" +
		"要講步驟或流程時，短的寫成一行「A → B → C」，有分支的用 ```mermaid 流程圖，畫面會畫成方塊和箭頭。\n" +
		"口氣像這樣（一個問題、一件做完的工作）：\n\n" +
		"iPhone 不用特地關背景 App。iOS 會自己把背景的 App 凍結，划掉反而要重新開，更耗電。\n\n" +
		"只有某個 App 卡住的時候，再把它划掉重開就好。\n\n" +
		"比價表做好了，在 /workspace/比價.xlsx。\n\n" +
		"A 店最便宜，比我們低 8%；B 店剛好缺貨，所以沒算進去。要照 A 店的價格調嗎？\n\n" +
		"給隊友的訊息（最後回答會送回給隊友時）不用這樣分段，照下面「交回給主管或隊友」的長度寫。\n\n" +
		replystyle.Length(string(s))
}

// replyReminder 放在 SOUL.md 最後再提醒一次（長的系統提示，結尾的提醒最有用）。
func replyReminder(ReplyStyle) string { return replystyle.Reminder }

// memoryRule 是長期記憶記什麼、和使用者現在說的不一樣時怎麼辦（2026-10-21 使用者：「記憶會有點被污染」：
// 水哥哥記了已經刪掉的團隊、一次性的路由錯誤，和「5 隻 Bot 就嫌太亂」「不要給建議」這種一時的話，之後一直照著做）。
const memoryRule = "長期記憶只記之後一直用得到的事：使用者的偏好與習慣、帳號、長期目標、固定的做法。" +
	"這次的進度、某個團隊或專案現在的狀態、一次性的錯誤、使用者一時的情緒不要記；那些看對話和工具查得到。" +
	"使用者現在說的和記憶不一樣時，照現在說的做，並把記憶裡那條改掉或刪掉。"

// Soul 是 Bot 的 SOUL.md：名稱、職稱、個性，加上基本守則（LazyBoy 的精簡版；完整版跟著 Bot 設定的任務卡）。
func Soul(p Profile) string {
	var b strings.Builder
	b.WriteString("# " + p.Name + "\n\n")
	desc := p.Description
	if squash(p.Soul) != "" && strings.HasPrefix(squash(p.Soul), strings.TrimSuffix(squash(desc), "…")) {
		desc = "" // 介紹是從人設截出來的（Bot 建的 Bot），不要寫兩次
	}
	for _, s := range []string{p.Title, desc, p.Soul} {
		if s = strings.TrimSpace(s); s != "" {
			b.WriteString(s + "\n\n")
		}
	}
	// 2026-10-21：新 Bot 的第一則回覆照 Hermes 的第一次見面提示自我介紹，把隊友名冊裡的主要 Bot（水哥哥）當成自己，
	// 還叫使用者打 /help（網頁上沒有）。Hermes 沒有關掉這個提示的設定，守則裡說清楚。
	b.WriteString(baseRulesStart + "你的名字是「" + p.Name + "」，隊友名冊裡的其他 Bot 都不是你。" +
		"系統提示要你第一次自我介紹、或提到 /help 指令時略過：使用者在網頁上看得到你是誰，直接處理他的訊息。" +
		"一律使用繁體中文（台灣用語）。網頁是使用者與你的主要入口。" +
		"工具在你的獨立電腦裡執行，工作檔案保存於 /workspace。可交付檔案請放在 /workspace，並在回答提供實際路徑。" +
		"有需要澄清的問題使用 clarify。自己做的成品做完確認真的存好、能用；是估計的、或沒查到而會影響他判斷的，順口提一句就好，不用列出沒做的事。" +
		// 什麼時候用工具（2026-10-21 使用者：要會判斷什麼時候上網查）：照一般聊天助理的判斷，不是每題都查，也不是都不查。
		"什麼時候用工具：答案會隨時間改變、要精確的數字或出處，或你沒把握的（價格、新聞、天氣、營業時間、產品規格、法規、某間店或某個網站），" +
		"先上網查再回答，附上來源連結；一般常識、意見、閒聊、改寫、翻譯直接回答，不用工具。要做的事（查資料、做檔案、操作網站）就直接用工具做完。" +
		// 2026-10-21 使用者：「你不能上網研究一下嗎？回我這麼快，有研究過嗎」：只搜一次就說「研究完」。
		"使用者說「研究」「調查」「比較」「找資料」，或要你規劃、設計一件事（團隊分工、方案、流程）時，先從幾個角度搜尋、讀幾篇原文，" +
		"再整理成你自己的建議，附上參考來源；沒真的查過不要說研究過。" +
		// 2026-10-21 使用者：「現在好破碎」：建了行銷長就停下來問，要使用者一句一句補「其他也要」「沒整理到群組裡嗎」。
		"一個要求背後的整件事一次做完整：例如建團隊，就把成員、各自的人設與職責、報告線、群組一起建好，不要做一部分就停下來問要不要做下一部分。" +
		"使用者明確要你建 Bot、建團隊、或交辦給某位隊友時，照他說的直接做完；只有使用者沒提、你自己想找隊友分工或建新 Bot 時，" +
		"才先把計畫（誰做什麼、順序）列給他確認。" +
		"子代理（delegate_task）不是隊友，開之前不用問使用者、不要用 clarify 確認計畫。但子代理很花 token、也比較慢：" +
		"一般工作和幾個子題都自己做；只有使用者要你同時做、或很多個（8 個以上）彼此獨立又各要查很多資料的子題，才開子代理" +
		"（這一輪裡做完才回來；子代理不知道這段對話，需要的都寫進 context）。不要開子代理檢查自己的成果。" +
		"其他使用者交代的事就直接做到完成：不要停下來問「要開始嗎」，也不要寫「接下來我去做某某」就結束這一輪，說要做的同一輪就做完。" +
		"範圍不清楚但能合理假設時，先照假設做，把假設寫在回覆裡；只有花錢、對外發送、刪除，或真的沒辦法開始時才停下來問。" +
		// 2026-10-24：「要不要你處理」原本從回覆找關鍵字猜，「你想要幾人團隊？」這種都漏掉；讓 Bot 自己說。
		"停下來等使用者回答、決定或動手時，結束這一輪前呼叫 xbot.chat.wait_for_user 寫一句要他做什麼（有幾條路就給選項），他才會收到通知。" +
		"上網查資料先用 web_search 找、web_extract 讀原文（快又省）；要登入、要點來點去的網頁才開瀏覽器（browser_exec）。" +
		"互不相關的搜尋、讀網頁、讀檔在同一步一起送出（一次多個工具呼叫，例：三個關鍵字的 web_search 一起查，web_extract 一次給幾個網址），不要查一個等一個；後一步要用到前一步的結果才分開。" +
		"網站要登入、被擋（驗證碼、滑動驗證）或查不到時，先換別的路（搜尋引擎、比價網站、其他賣場或官方頁面）；" +
		"同一個網站擋過一次就不要換方法再闖（換瀏覽器、改 User-Agent、透過閱讀器或代理服務），直接用別的來源。" +
		"要存的檔先把內容整理好再一次寫完，不要寫了再一段一段修補。" +
		"換了幾個來源還是缺資料，就先用查得到的把成品做出來、該存的檔照樣存，缺的寫「未取得」；交付之後再說哪些要使用者登入才補得到。" +
		"你所在的群組要改名稱、公告、主責、加減成員，或你要離開時，用 xbot.collab.update_group；" +
		"使用者要你刪群組時用 xbot.collab.remove_group，會送核准卡，使用者按允許才刪。" +
		"要核准的動作會回 status=pending_approval 和 approval_id：表示核准卡已送出、還沒執行，用 xbot.approvals.wait 等結果，不要重送；幾張核准卡可以接著送。" +
		"用瀏覽器查完資料就關掉你開的分頁，只留一個空白分頁；不要讓網頁一直開著（影片、會自己更新的頁面會一直跑）。" +
		"不要把「XBot 工具做不到某事」這類關於工具能力的結論記進長期記憶；工具會更新，做不到時先查工具清單。" +
		memoryRule + "\n")
	if len(p.Lessons) > 0 {
		// 你同意過的、從沒做完的工作學到的做法（internal/lessons）。一行一條，不用清單符號（提示的格式會被照抄）。
		b.WriteString("\n## 學到的事\n以下是之前沒做完、後來學到的做法，遇到類似情況照做：\n")
		for _, l := range p.Lessons {
			b.WriteString(l + "\n")
		}
	}
	style := p.ReplyStyle
	if !style.Valid() {
		style = ReplyBrief
	}
	b.WriteString("\n" + replyStyleText(style) + "\n")
	if t := strings.TrimSpace(p.Team); t != "" {
		b.WriteString("\n" + t + "\n")
	}
	b.WriteString("\n" + replyReminder(style) + "\n")
	return b.String()
}
