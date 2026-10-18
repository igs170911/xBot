package limits

import "time"

// 引擎驅動與對話（E02）。

const (
	// HermesReadyTimeout 是連上 Hermes gateway 後等 gateway.ready 的時限。出自 LazyBoy server/rpc.py:25 open()（wait_for(ready, 15)）。
	HermesReadyTimeout = 15 * time.Second

	// HermesCallTimeout 是一次 JSON-RPC 呼叫等回覆的預設時限。出自 LazyBoy server/rpc.py:31 call()（timeout=90）。
	HermesCallTimeout = 90 * time.Second

	// HermesPingInterval、HermesPingTimeout 是 WebSocket 心跳。出自 LazyBoy server/rpc.py:21（ping_interval=15, ping_timeout=30）。
	HermesPingInterval = 15 * time.Second
	HermesPingTimeout  = 30 * time.Second

	// HermesMaxFrameBytes 是單一 WebSocket 訊息的上限。出自 LazyBoy server/rpc.py:21（max_size=16*1024*1024）。
	HermesMaxFrameBytes int64 = 16 << 20

	// HermesMaxIterations 是 Hermes 一輪最多的工具迴圈次數。出自 LazyBoy server/computers.py:213 _prepare()（agent.max_iterations 200）。
	HermesMaxIterations = 200

	// HermesTerminalTimeoutSeconds 是 Hermes 終端機指令的時限（秒）。出自 LazyBoy server/computers.py:202 _prepare()（terminal.timeout 120）。
	HermesTerminalTimeoutSeconds = 120

	// HermesPruneTokens、HermesCompressTokens：舊的大型工具結果超過 48K tokens 時修剪、對話超過 100K 時摘要。
	// 出自 LazyBoy server/computers.py:222–223 _prepare()（compression.proactive_prune_tokens／threshold_tokens）。
	HermesPruneTokens    = 48000
	HermesCompressTokens = 100000

	// StreamPersistInterval 是串流中的回覆最多多久寫回資料庫一次（重新整理頁面時看得到進度）。
	// 出自 LazyBoy server/runtime.py:552 PERSIST_INTERVAL。
	StreamPersistInterval = 500 * time.Millisecond
)

// ACP 驅動（E12：Claude Code、Codex CLI，經 docker exec 跑在 Bot 的電腦裡）。
const (
	// ACPInitTimeout 是開 agent 行程到 initialize 回覆的時限（Node.js 冷啟動加上載入 SDK）。
	// OpenMausBot acp/core.ts 用 INIT_TIMEOUT；XBot 的行程在容器裡，給寬一點。
	ACPInitTimeout = 60 * time.Second

	// ACPSessionTimeout 是 session/new、session/load 的時限（要等 MCP 伺服器都起來）。
	ACPSessionTimeout = 120 * time.Second

	// ACPCallTimeout 是其他 ACP 呼叫（設定模式、模型、取消）的時限。
	ACPCallTimeout = 30 * time.Second

	// ACPMaxLineBytes 是 agent 輸出的一行 JSON-RPC 的上限（同 Hermes 的單一訊息上限）。
	ACPMaxLineBytes = 16 << 20

	// ACPStderrTailBytes 是 agent 行程結束時保留下來寫進紀錄的 stderr 長度。
	ACPStderrTailBytes = 4 << 10

	// ACPToolPayloadBytes 是一個工具呼叫的輸入或輸出寫進事件的上限（截圖的 base64 不整包存）。
	ACPToolPayloadBytes = 64 << 10
)
