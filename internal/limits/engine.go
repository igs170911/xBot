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

	// HermesIdleWait 是送出新訊息前，最多等 Hermes 把這個 session 的上一輪收完尾（session.info running=false）多久。
	// Hermes 在 message.complete 之後還要跑 /loop 檢查、寫標題、發布控制快照，跑完才放開 running（tui_gateway/prompt_turn.py
	// run_body 的 finally）；這段期間送出的 prompt 會被當成插話（busy_input_mode=steer）收下卻不會開新的一輪。
	// 平常不到 2 秒；這是 XBot 自己的數字（LazyBoy 兩輪之間有路由與設定呼叫，碰不到這個時間差）。逾時就以 queued 送出，
	// 讓 Hermes 收完尾再自己接著跑。
	HermesIdleWait = 30 * time.Second

	// HermesStartTimeout 是 prompt.submit 回 streaming 之後，最多等 message.start 多久。Hermes 會先等 agent 建好
	// （MCP 探索、模型資料），上限是 agent.build_wait_timeout，預設 600 秒（tui_gateway/server.py _agent_build_wait_cap）；
	// 再加 30 秒餘裕。超過就判定這一輪沒有開始，回報錯誤，不會永遠等下去。
	HermesStartTimeout = 630 * time.Second

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

// 對話看門狗（CH-12）。出自 LazyBoy server/runtime.py Runtime.watch()。
const (
	// TurnIdleLimit 是一輪多久沒有任何進度（事件）就判定卡住、送出中斷。出自 IDLE_LIMIT（15*60）。
	TurnIdleLimit = 15 * time.Minute

	// TurnToolIdleLimit 是有工具正在執行（長的建置、下載）時的同一個時限。出自 TOOL_IDLE_LIMIT（40*60）。
	TurnToolIdleLimit = 40 * time.Minute

	// TurnStopGrace 是送出中斷（看門狗或使用者按停止）後，等引擎自己結束這一輪的時間；過了就直接結束。出自 STOP_GRACE（60）。
	TurnStopGrace = 60 * time.Second

	// TurnWatchInterval 是看門狗多久檢查一次。出自 WATCH_INTERVAL（5）。
	TurnWatchInterval = 5 * time.Second
)
