// Package acp 是 ACP 驅動（E12、PRD EN-2）：在 Bot 的電腦裡（經 docker exec）開 Claude Code、Codex CLI 的 ACP adapter，
// 用 Agent Client Protocol（stdio 上的 JSON-RPC 2.0）開 session、送訊息、串流事件、取消、回覆權限請求，
// 把事件正規化成 engine.Event。XBot 不自己寫 agent harness：工具都經 MCP（desktop_mcp、XBot MCP，EN-2c）。
package acp
