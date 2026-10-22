package limits

import "time"

// 引擎驅動與對話（E02）。

// InterimNarrationMax：ACP 引擎在用工具前寫的文字不超過這麼多字（「我先看一下…」）時，最後的回答只取用完工具後的那段；
// 比這長就整段保留（可能是先寫完答案再存檔）。XBot 自訂。
const InterimNarrationMax = 200

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

	// HermesPruneTokens：舊的大型工具結果超過 48K tokens 時修剪。出自 LazyBoy server/computers.py:222 _prepare()
	// （compression.proactive_prune_tokens）。
	HermesPruneTokens = 48000

	// HermesCompressTokens 是對話超過多少 tokens 時摘要（compression.threshold_tokens）。
	// 和 LazyBoy 不同：LazyBoy server/computers.py:223 是 100K。實測（2026-10-19，水哥哥一天 20.9M 輸入 tokens）每次請求
	// 都停在 86–100K，幾乎沒有摘要過；降到 72K 讓摘要真的發生，每次請求少送約 20K。
	HermesCompressTokens = 72000
	// HermesCompressTokensOld 是舊版寫進 config.yaml 的值（LazyBoy 的 100K）；看到它就換成 HermesCompressTokens，
	// 使用者自己改過的其他值保留。
	HermesCompressTokensOld = 100000

	// HermesSearchSnippetRunes 是 web_search 每一筆摘要最多幾個字（容器裡的 hermes_host.py 讀 config.yaml 的 xbot_search_snippet_max）。
	// XBot 新增（LazyBoy 沒有）：ddgs 有些結果把整頁內文當摘要（2026-10-20 實測 1,102 筆：中位數 259 字、p90 1,666、最長 28K），
	// 之後每一步都跟著重送；Hermes 的 web_search 本來就只該回標題、網址、摘要，內文用 web_extract 讀。截到 500 字，搜尋結果少 33%。
	HermesSearchSnippetRunes = 500

	// HermesContextLength 是告訴 Hermes 的模型視窗大小（model.context_length）。Hermes 經 Model Proxy 量不到視窗，
	// 會用自己內建的表（deepseek-v4-flash 當成 1M），lean 模式摘要後留下的尾巴是視窗的 2.5%（上限 25K）。
	// 定成 200K：尾巴變 10K（下限），背景檢討的預算也跟著變小。XBot 新增（LazyBoy 沒有設定）。
	HermesContextLength = 200000

	// HermesSkillNudgeInterval 是 Hermes 每幾次工具迴圈才在背景檢討「要不要寫成技能」（skills.creation_nudge_interval）。
	// Hermes 預設 10：幾乎每一輪做完都會跑一次，每次把整段對話重送 4–7 遍（實測 158K–553K tokens，一天多花約 27%）。
	// XBot 新增（LazyBoy 沒有設定）。
	HermesSkillNudgeInterval = 40

	// HermesReviewInputTokens 是一次背景檢討最多送多少輸入 tokens（auxiliary.background_review.max_input_tokens，
	// 所有請求加總；到了就停）。Hermes 預設是視窗的 75%（上限 600K）。XBot 新增。
	HermesReviewInputTokens = 150000

	// HermesDelegationMaxIterations 是 Hermes 子代理（delegate_task）一次最多幾次工具迴圈（delegation.max_iterations）。
	// Hermes 預設 250，比主代理的 HermesMaxIterations 還多；子代理只做一小段，60 次夠用。XBot 新增。
	HermesDelegationMaxIterations = 60

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

	// ACPBackgroundWait 是 session/prompt 回來後，最多等 agent 的背景工作（Grok 的 workflow、背景子代理）做完並回報多久；
	// 到了就先收尾這一輪。XBot 新增（2026-10-21：Grok 的深度研究 workflow 跑了 2 分鐘，四個子代理各 15 到 45 秒）。
	// 比 WatchdogToolIdle 短：背景工作在工作步驟裡是一個進行中的步驟，看門狗用有工具在跑的時限。
	ACPBackgroundWait = 30 * time.Minute

	// ACPBackgroundSettle 是背景工作都做完、agent 也沒在跑時，再等它開始回報的時間（Grok 做完 workflow 同一毫秒就開始回報）。
	ACPBackgroundSettle = 15 * time.Second

	// ACPDenyResumeMax 是一輪裡使用者拒絕動作後，最多請 agent 換方法接著做幾次（Grok 一拒絕就整輪取消）。XBot 新增。
	ACPDenyResumeMax = 3

	// ACPMaxSessionsPerBox 是一個 Bot 同時最多幾個 ACP session（超過時關掉最久沒用的）。XBot 新增（2026-10-22）：
	// 每個 session 會開 desktop_mcp.py 與其他 MCP 子行程（合計約 90 MB），不關的話累積到 31–35 個就超過 3 GB 記憶體上限、OOM。
	ACPMaxSessionsPerBox = 5

	// ACPSessionIdleTimeout 是 session 閒置多久自動關閉。XBot 新增（2026-10-22）。
	ACPSessionIdleTimeout = 5 * time.Minute
)

// Bot 自己登入訂閱 CLI（2026-10-20，xbot.engines.login_start）。XBot 自己的數字。
const (
	// EngineLoginTTL 是一次登入最久等多久（Codex、Grok 的裝置碼 15 分鐘到期；同 OAuthLoginTTL）。
	EngineLoginTTL = 15 * time.Minute

	// EngineLoginURLWait 是開始登入後，等 CLI 印出登入網址最久多久（開機、CLI 冷啟動）。
	EngineLoginURLWait = 90 * time.Second

	// EngineLoginOutputBytes 是登入指令的輸出最多留多少（找網址與代碼、失敗時給人看最後幾行）。
	EngineLoginOutputBytes = 16 << 10

	// EngineLoginKeyMax 是貼上的金鑰最長幾個字元（OpenCode）。
	EngineLoginKeyMax = 512

	// EngineLoginCodeMax 是貼回來的授權碼最長幾個字元（Claude 的授權碼約 100 字）。
	EngineLoginCodeMax = 2048
)

// 引擎額度用完（E31，PRD EN-7）。XBot 自己的數字。
const (
	// QuotaMessageMax 是額度卡上保留的原始錯誤長度（字）。
	QuotaMessageMax = 300

	// QuotaRecent 是 Model Proxy 看到的額度錯誤多久之內，還算是這一輪失敗的原因。
	QuotaRecent = 15 * time.Minute

	// QuotaProbeBytes 是 Model Proxy 為了分類錯誤，最多留下供應商錯誤回應的前幾個位元組。
	QuotaProbeBytes = 8 << 10

	// QuotaChoicesMax 是額度用完卡上最多列幾個換引擎、換模型的選項（不含「等額度恢復」）。
	QuotaChoicesMax = 6
)
