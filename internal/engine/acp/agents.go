package acp

import (
	"context"
	"io"
	"slices"
	"strings"
)

// 引擎名稱（bot.engine）。
const (
	EngineClaude   = "claude-code"
	EngineCodex    = "codex"
	EngineGemini   = "gemini"
	EngineOpenCode = "opencode"
	EngineGrok     = "grok"
)

// Engines 是所有 ACP 引擎，依介面上的順序。
var Engines = []string{EngineClaude, EngineCodex, EngineGemini, EngineOpenCode, EngineGrok}

// Agent 是一個支援 ACP 的 CLI：在 Bot 的電腦裡用什麼指令開、要哪些環境變數、權限等級怎麼對到它的模式。
// 指令都由 box/Dockerfile 預先裝好（版本固定）。
type Agent struct {
	Engine string
	// Label 是給人看的名稱。
	Label string
	// Command 是在容器裡執行的指令（stdin／stdout 講 ACP）。
	Command []string
	Env     []string
	// Modes 把 bot.approval_mode 對到 agent 的 session 模式（session/set_mode）；agent 沒有那個模式時不設定。
	Modes map[string]string
	// ModeEnv 是依 bot.approval_mode 加上的環境變數（權限不是用 session 模式、而是開行程時設定的 agent，例：OpenCode）。
	// 權限等級改了，下一次開 session 時（沒有進行中的一輪）重開 agent 行程。
	ModeEnv map[string][]string
	// BlockedPrompts 是不能送給 agent 的斜線指令開頭（例：Grok 的 /always-approve 會關掉所有核准，SEC-2）。
	// 訊息可能來自隊友或網頁內容（SEC-8），一律擋下。
	BlockedPrompts []string
}

// ForMode 回傳開行程用的 Agent：環境變數加上這個權限等級的設定。
func (a Agent) ForMode(mode string) Agent {
	if extra := a.ModeEnv[mode]; len(extra) > 0 {
		a.Env = append(slices.Clone(a.Env), extra...)
	}
	return a
}

// Blocked 回答這段訊息是不是被擋下的斜線指令；是的話回傳那個指令。
func (a Agent) Blocked(text string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, p := range a.BlockedPrompts {
		if strings.HasPrefix(t, p) {
			return p, true
		}
	}
	return "", false
}

// openCodePermission 是 OpenCode 的權限設定（OPENCODE_CONFIG_CONTENT，優先於 Bot 自己寫的設定檔）：
// 指令（bash）在任何等級都要先問（OpenCode 沒有像 Claude Code auto 那樣的自動審查，SEC-2）；編輯與上網依等級放寬。
func openCodePermission(edit, webfetch string) []string {
	return []string{`OPENCODE_CONFIG_CONTENT={"$schema":"https://opencode.ai/config.json","autoupdate":false,"share":"disabled",` +
		`"permission":{"edit":"` + edit + `","bash":"ask","webfetch":"` + webfetch + `","external_directory":"ask","doom_loop":"ask"}}`}
}

// Agents 是 XBot 支援的 ACP 引擎。
//   - Claude Code：@agentclientprotocol/claude-agent-acp（原 Zed 的 claude-code-acp），內含 Claude Agent SDK 與 Claude Code。
//     模式：default（每個動作都問）、acceptEdits、auto（Claude 自己判斷）、bypassPermissions。
//   - Codex CLI：@agentclientprotocol/codex-acp（原 Zed 的 codex-acp），內含 @openai/codex。
//     模式：read-only、workspace-write、agent（自動審查）、agent-full-access。
//   - Gemini CLI（E31）：@google/gemini-cli 本身講 ACP（gemini --acp）。模式：default（每個動作都問）、autoEdit、yolo、plan。
//     登入方式與「不准 YOLO」寫在映像的系統設定 /etc/gemini-cli/settings.json（Bot 改不了）。
//   - OpenCode（E31）：opencode acp。沒有權限模式（session 的 mode 是 build／plan），權限用 OPENCODE_CONFIG_CONTENT 設定。
//     用你的 OpenCode Go／Zen 訂閱（~/.local/share/opencode/auth.json）。
//   - Grok Build（E31）：grok agent stdio（LazyBoy 已經把 grok 裝在映像裡）。ACP 沒有模式，預設每個動作都問；
//     絕不加 --always-approve，也不讓訊息帶 /always-approve。
var Agents = map[string]Agent{
	EngineClaude: {
		Engine: EngineClaude, Label: "Claude Code",
		Command: []string{"claude-agent-acp"},
		// 映像裡的版本是固定的，不讓它自己更新。
		Env: []string{"DISABLE_AUTOUPDATER=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_TELEMETRY=1"},
		// full 也只到 auto：SEC-2 規定危險動作在任何等級都不能自動放行，所以不用 bypassPermissions。
		Modes: map[string]string{"ask": "default", "edits": "acceptEdits", "auto": "auto", "full": "auto"},
	},
	EngineCodex: {
		Engine: EngineCodex, Label: "Codex CLI",
		Command: []string{"codex-acp"},
		// full 也只到 agent（自動審查），不用 agent-full-access（SEC-2）。
		Modes: map[string]string{"ask": "workspace-write", "edits": "workspace-write", "auto": "agent", "full": "agent"},
	},
	EngineGemini: {
		Engine: EngineGemini, Label: "Gemini CLI",
		Command: []string{"gemini", "--acp"},
		// 沒有登入時不要試著開瀏覽器。
		Env: []string{"NO_BROWSER=true"},
		// full 也只到 autoEdit：yolo 會自動允許所有動作（SEC-2；映像的系統設定也關掉了 YOLO）。
		Modes: map[string]string{"ask": "default", "edits": "autoEdit", "auto": "autoEdit", "full": "autoEdit"},
	},
	EngineOpenCode: {
		Engine: EngineOpenCode, Label: "OpenCode",
		Command: []string{"opencode", "acp"},
		Env:     []string{"OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_CLAUDE_CODE=1"},
		ModeEnv: map[string][]string{
			"ask":   openCodePermission("ask", "ask"),
			"edits": openCodePermission("allow", "ask"),
			"auto":  openCodePermission("allow", "allow"),
			"full":  openCodePermission("allow", "allow"),
		},
	},
	EngineGrok: {
		Engine: EngineGrok, Label: "Grok",
		Command:        []string{"grok", "agent", "stdio"},
		BlockedPrompts: []string{"/always-approve"},
	},
}

// IsACP 回答這個引擎是不是由 ACP 驅動。
func IsACP(engine string) bool {
	_, ok := Agents[engine]
	return ok
}

// Process 是一個在 Bot 電腦裡執行的 agent 行程：Read 讀它的 stdout，Write 寫它的 stdin，Close 結束它。
type Process interface {
	io.Reader
	io.Writer
	io.Closer
}

// StderrTail 由 Process 選擇性實作：行程結束時，最後一段 stderr（寫進紀錄方便查錯）。
type StderrTail interface {
	StderrTail() string
}

// Launcher 在 Bot 的電腦裡開一個 agent 行程（必要時先開機）。由 runtime 提供（經 docker exec）。
type Launcher func(ctx context.Context, botID string, a Agent) (Process, error)

// Authorizer 確認 Bot 可以用這個引擎（例：使用者已同意掛載訂閱憑證，EN-2b）；不行時回傳給人看的錯誤。
type Authorizer func(ctx context.Context, botID, engine string) error

// MCPServer 是交給 agent 的一個 stdio MCP 伺服器（ACP session/new 的 mcpServers）。
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []EnvVar `json:"env"`
}

// EnvVar 是 MCP 伺服器的環境變數。
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
