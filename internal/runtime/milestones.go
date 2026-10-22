package runtime

import (
	"strings"
	"xbot/internal/engine"
)

// Milestone 定義重要的工作里程碑，這些才需要推送進度更新給使用者。
// 不是每個工具呼叫都值得打斷使用者，只有以下情況才算里程碑：
// - 長時間執行的操作（瀏覽器、網路請求、檔案操作）
// - 需要外部資源的操作（MCP 工具、訊息傳送）
// - 背景工作的開始與結束
// - 計畫階段的完成

// isMilestone 判斷一個工具呼叫是否為重要里程碑，值得推送進度更新。
func isMilestone(e engine.Event) bool {
	if e.Type != engine.EventToolCall {
		return false
	}
	if e.Tool == nil {
		return false
	}

	// 背景工作、子任務：一定是里程碑
	name := e.Tool.Name
	if strings.HasPrefix(name, "背景工作：") || strings.Contains(name, "subagent") || strings.Contains(name, "Task") {
		return true
	}

	// 長時間操作：瀏覽器、網路、建置
	longRunning := []string{
		"browser_", "web_", "http_", "fetch",
		"docker_", "build", "compile",
		"deploy", "install", "download", "upload",
	}
	for _, prefix := range longRunning {
		if strings.HasPrefix(name, prefix) || strings.Contains(name, prefix) {
			return true
		}
	}

	// 外部通訊：MCP、訊息傳送、檔案共享
	external := []string{
		"mcp_", "message_", "send_",
		"email", "slack", "github",
		"file_share", "collab_",
	}
	for _, keyword := range external {
		if strings.Contains(name, keyword) {
			return true
		}
	}

	// 計畫與決策：表示工作進入新階段
	if e.Tool.Context != "" {
		ctx := strings.ToLower(e.Tool.Context)
		planning := []string{
			"計畫", "plan", "開始", "start",
			"完成", "done", "結束", "finish",
			"階段", "phase", "步驟", "step",
		}
		for _, keyword := range planning {
			if strings.Contains(ctx, keyword) {
				return true
			}
		}
	}

	// 其他工具呼叫（檔案讀寫、終端機）不是里程碑，不打斷使用者
	return false
}

// shouldNotifyToolResult 判斷工具結果是否需要通知（只通知里程碑工具的結果）。
func shouldNotifyToolResult(e engine.Event, lastMilestone string) bool {
	if e.Type != engine.EventToolResult {
		return false
	}
	if e.Tool == nil || e.Tool.ID == "" {
		return false
	}
	// 只有里程碑工具的結果才通知
	return e.Tool.ID == lastMilestone
}
