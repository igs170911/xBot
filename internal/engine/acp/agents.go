package acp

import (
	"context"
	"io"
)

// 引擎名稱（bot.engine）。
const (
	EngineClaude = "claude-code"
	EngineCodex  = "codex"
)

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
}

// Agents 是 XBot 支援的 ACP 引擎。
//   - Claude Code：@agentclientprotocol/claude-agent-acp（原 Zed 的 claude-code-acp），內含 Claude Agent SDK 與 Claude Code。
//     模式：default（每個動作都問）、acceptEdits、auto（Claude 自己判斷）、bypassPermissions。
//   - Codex CLI：@agentclientprotocol/codex-acp（原 Zed 的 codex-acp），內含 @openai/codex。
//     模式：read-only、workspace-write、agent（自動審查）、agent-full-access。
var Agents = map[string]Agent{
	EngineClaude: {
		Engine: EngineClaude, Label: "Claude Code",
		Command: []string{"claude-agent-acp"},
		// 映像裡的版本是固定的，不讓它自己更新。
		Env:   []string{"DISABLE_AUTOUPDATER=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_TELEMETRY=1"},
		Modes: map[string]string{"ask": "default", "edits": "acceptEdits", "auto": "auto", "full": "bypassPermissions"},
	},
	EngineCodex: {
		Engine: EngineCodex, Label: "Codex CLI",
		Command: []string{"codex-acp"},
		Modes:   map[string]string{"ask": "workspace-write", "edits": "workspace-write", "auto": "agent", "full": "agent-full-access"},
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
