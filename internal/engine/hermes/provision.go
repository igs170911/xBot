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
const webHint = "你在 XBot 的網頁聊天介面裡回覆。Markdown 會正常顯示（粗體、清單、表格、連結），" +
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
	ReplyBrief    ReplyStyle = "brief"    // 簡短（預設）：像傳訊息，一段一兩句，最多 4 段
	ReplyNormal   ReplyStyle = "normal"   // 一般：一段兩三句，最多 6 段
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
func replyStyleText(s ReplyStyle) string {
	size := "每段一兩句話，大約 40 個字以內，一次最多 4 段。"
	switch s {
	case ReplyNormal:
		size = "每段兩三句話，一次最多 6 段。"
	case ReplyDetailed:
		size = "需要的話可以完整說明，但每段只講一件事，先講結果再講細節。"
	}
	return replyStyleHead + "\n" +
		"回給使用者本人時，像在通訊軟體裡傳訊息給老闆：短、白話、一段一件事。段落之間空一行，每一段會變成一個對話泡泡。" + size + "\n" +
		"第一段直接講結果：做好了沒、東西在哪（實際檔案路徑或連結）。接著只講使用者需要知道的：沒做完、沒確認到、有風險，或你替他做了什麼判斷。" +
		"要使用者決定或回答的事，一題自己一段，一句話就能回答，有選項就寫出選項。" +
		"值得做的下一步，挑最有價值的一件用一句話講，可以連結他的其他專案；沒有就不講。上面這些沒有的就省略，不要寫「無」。\n" +
		"不要寫開場白、不要重述問題、不要客套。不要描述你的過程或用了哪些工具，工具之間也不要寫旁白，使用者看得到工作步驟。" +
		"不要用 # 標題、** 粗體、1. 2. 編號或項目符號，除非使用者要清單；表格只在比較三項以上時用。不要寫內部欄位名、工具名或英文術語。" +
		"放不放檔案，看使用者之後會不會再用：只看一次就好的（結果、狀態、8 項以內的清單、5 列以內的表格、短程式碼、連結）直接寫在對話裡；" +
		"會留著、會改、會分享的成品（報告、試算表、文件、要執行的程式），或一個畫面看不完的，存成 /workspace 裡的檔案，聊天裡只講重點和路徑。" +
		"但使用者明說要在對話裡看的（例：「整理成表格給我」），一個畫面放得下就直接放在對話裡，就算也存成了檔案。" +
		"中途抓的網頁、下載的原始資料這類過程檔放 /workspace/tmp/，過幾天會自動清掉。使用者說要詳細時才展開。" +
		"要講步驟或流程時，短的寫成一行「A → B → C」，有分支的用 ```mermaid 流程圖，畫面會畫成方塊和箭頭。\n" +
		"照這個長度和樣子寫（三段就是三個泡泡）：\n\n" +
		"比價表做好了，在 /workspace/比價.xlsx。\n\n" +
		"A 店最便宜，比我們低 8%。B 店缺貨，沒算進去。\n\n" +
		"要照 A 店調價嗎？\n\n" +
		"閒聊或簡單問題就一句話回答。給隊友的訊息（最後回答會送回給隊友時）不用這樣分段，照協作規則寫完整、精簡。"
}

// replyReminder 放在 SOUL.md 最後再提醒一次（長的系統提示，結尾的提醒最有用）。
func replyReminder(s ReplyStyle) string {
	if s == ReplyDetailed {
		return "（回話提醒：先講結果，一段一件事，段落之間空一行。）"
	}
	return "（回話提醒：回給使用者時像傳訊息一樣短，先講結果，一段一個泡泡，不寫過程。）"
}

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
	b.WriteString(baseRulesStart + "一律使用繁體中文（台灣用語）。網頁是使用者與你的主要入口。" +
		"工具在你的獨立電腦裡執行，工作檔案保存於 /workspace。可交付檔案請放在 /workspace，並在回答提供實際路徑。" +
		"有需要澄清的問題使用 clarify。完成後驗證結果；是估計的、沒查到的要說，一句帶過就好。" +
		"要把工作交給隊友 Bot（xbot.collab.message_bot）、建 Bot 或團隊之前，先列計畫給使用者確認，同意後才做。" +
		"子代理（delegate_task）不是隊友，開之前不用問使用者、不要用 clarify 確認計畫。但子代理很花 token、也比較慢：" +
		"一般工作和幾個子題都自己做；只有使用者要你同時做、或很多個（8 個以上）彼此獨立又各要查很多資料的子題，才開子代理" +
		"（這一輪裡做完才回來；子代理不知道這段對話，需要的都寫進 context）。不要開子代理檢查自己的成果。" +
		"其他使用者交代的事就直接做到完成：不要停下來問「要開始嗎」，也不要寫「接下來我去做某某」就結束這一輪，說要做的同一輪就做完。" +
		"範圍不清楚但能合理假設時，先照假設做，把假設寫在回覆裡；只有花錢、對外發送、刪除，或真的沒辦法開始時才停下來問。" +
		"網站要登入、被擋（驗證碼、滑動驗證）或查不到時，先換別的路（搜尋引擎、比價網站、其他賣場或官方頁面）。" +
		"換了幾個來源還是缺資料，就先用查得到的把成品做出來、該存的檔照樣存，缺的寫「未取得」；交付之後再說哪些要使用者登入才補得到。" +
		"你所在的群組要改名稱、公告、主責、加減成員，或你要離開時，用 xbot.collab.update_group；" +
		"使用者要你刪群組時用 xbot.collab.remove_group，會送核准卡，使用者按允許才刪。" +
		"要核准的動作會回 status=pending_approval 和 approval_id：表示核准卡已送出、還沒執行，用 xbot.approvals.wait 等結果，不要重送；幾張核准卡可以接著送。" +
		"用瀏覽器查完資料就關掉你開的分頁，只留一個空白分頁；不要讓網頁一直開著（影片、會自己更新的頁面會一直跑）。" +
		"不要把「XBot 工具做不到某事」這類關於工具能力的結論記進長期記憶；工具會更新，做不到時先查工具清單。\n")
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
