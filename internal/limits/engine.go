package limits

import "time"

// 引擎驅動與對話（E02）。

const (
	// HermesReadyTimeout 是連上 Hermes gateway 後等 gateway.ready 的時限。出自 LazyBoy server/rpc.py open()（wait_for(ready, 15)）。
	HermesReadyTimeout = 15 * time.Second

	// HermesCallTimeout 是一次 JSON-RPC 呼叫等回覆的預設時限。出自 LazyBoy server/rpc.py call()（timeout=90）。
	HermesCallTimeout = 90 * time.Second

	// HermesPingInterval、HermesPingTimeout 是 WebSocket 心跳。出自 LazyBoy server/rpc.py（ping_interval=15, ping_timeout=30）。
	HermesPingInterval = 15 * time.Second
	HermesPingTimeout  = 30 * time.Second

	// HermesMaxFrameBytes 是單一 WebSocket 訊息的上限。出自 LazyBoy server/rpc.py（max_size=16*1024*1024）。
	HermesMaxFrameBytes int64 = 16 << 20

	// HermesMaxIterations 是 Hermes 一輪最多的工具迴圈次數。出自 LazyBoy server/computers.py _prepare()（agent.max_iterations 200）。
	HermesMaxIterations = 200

	// HermesTerminalTimeoutSeconds 是 Hermes 終端機指令的時限（秒）。出自 LazyBoy server/computers.py _prepare()（terminal.timeout 120）。
	HermesTerminalTimeoutSeconds = 120

	// HermesPruneTokens、HermesCompressTokens：舊的大型工具結果超過 48K tokens 時修剪、對話超過 100K 時摘要。
	// 出自 LazyBoy server/computers.py _prepare()（compression.proactive_prune_tokens／threshold_tokens）。
	HermesPruneTokens    = 48000
	HermesCompressTokens = 100000

	// StreamPersistInterval 是串流中的回覆最多多久寫回資料庫一次（重新整理頁面時看得到進度）。
	// 出自 LazyBoy server/runtime.py PERSIST_INTERVAL。
	StreamPersistInterval = 500 * time.Millisecond
)
