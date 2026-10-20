package limits

import "time"

// Bot 的電腦（B02）。

const (
	// BoxCPUs 是每台 Bot 電腦預設可用的 CPU 數。出自 PRD BX-11 與 LazyBoy server/computers.py:49 RESOURCES（--cpu-quota 20000 / --cpu-period 10000）。
	BoxCPUs = 2

	// BoxCPUPeriod 是 CPU 配額的計算週期。出自 LazyBoy server/computers.py:49（--cpu-period 10000）：
	// Docker 預設 100 ms 的週期會讓忙碌的 Chromium 一開始就用完配額，桌面畫面和人的打字跟著卡住。
	BoxCPUPeriod = 10 * time.Millisecond

	// BoxMemoryBytes 是每台 Bot 電腦的記憶體上限。出自 PRD BX-11 與 LazyBoy server/computers.py:49（--memory 3g）。
	BoxMemoryBytes int64 = 3 << 30

	// BoxShmBytes 是 /dev/shm 大小（Chromium 需要）。出自 PRD BX-11 與 LazyBoy server/computers.py:49（--shm-size 512m）。
	BoxShmBytes int64 = 512 << 20

	// BoxPidsLimit 是容器內最多的行程（含執行緒）數。PRD SEC-3 要求設上限，LazyBoy 沒有設；
	// XBot 取 4096：Chromium＋XFCE＋LibreOffice＋Hermes 平常約數百個執行緒，留足餘裕又擋得住 fork bomb。
	BoxPidsLimit int64 = 4096

	// BoxStopTimeout 是停止電腦時等它自己收尾（存瀏覽器狀態）的時間，逾時才強制結束。
	// 出自 LazyBoy server/computers.py:380（docker stop -t 20）。
	BoxStopTimeout = 20 * time.Second

	// BoxReadyTimeout 是開機後等桌面與 Hermes 就緒的上限。出自 LazyBoy server/computers.py:396 start()（deadline = 75 秒）。
	BoxReadyTimeout = 75 * time.Second

	// BoxReadyPoll 是等待就緒時每次檢查的間隔。出自 LazyBoy server/computers.py:404 start()（asyncio.sleep(.6)）。
	BoxReadyPoll = 600 * time.Millisecond

	// BoxProbeTimeout 是單次就緒檢查（HTTP）的時限。出自 LazyBoy server/computers.py:341 status()（httpx timeout=2）。
	BoxProbeTimeout = 2 * time.Second
)

// Bot 電腦的生命週期與強化（B13）。

const (
	// BoxTmpfsTmpBytes、BoxTmpfsRunBytes、BoxTmpfsVarTmpBytes 是唯讀根檔案系統（SEC-3）之下可寫的 tmpfs 大小。
	// XBot 新增（LazyBoy 的根檔案系統可寫、沒有上限）：/tmp 放桌面的記錄檔、X 的 socket、下載與解壓的暫存，
	// 1 GB 和 BX-14「暫存」清理的量級相當；tmpfs 算在容器的記憶體上限（BX-11 預設 3 GB）裡。
	BoxTmpfsTmpBytes    int64 = 1 << 30
	BoxTmpfsRunBytes    int64 = 64 << 20
	BoxTmpfsVarTmpBytes int64 = 256 << 20

	// BoxIdleCheckInterval 是閒置休眠（BX-12、CL-9）多久檢查一次。XBot 新增；閒置時間本身是 bot.idle_minutes
	// （預設 BoxIdleMinutes，見 store.go）。
	BoxIdleCheckInterval = time.Minute

	// NetguardApplyTimeout 是把網路防護規則（SEC-4）套用到一台主機的上限（寫規則檔＋在 xbot-netguard 裡執行 apply）。XBot 新增。
	NetguardApplyTimeout = 30 * time.Second

	// NetAllowMax 是一個 Bot 的私有網路白名單（SEC-4 allow_private_urls）最多幾筆。XBot 新增。
	NetAllowMax = 32
)

// 搬家（CL-5，B31）與「從家裡出去」（CL-7）。都是 XBot 新增。
const (
	// BoxMoveKeep：搬家成功後，原主機上的資料（home／workspace／hermes／cred volume）再留多久才刪（回復用）。
	BoxMoveKeep = 24 * time.Hour
	// BoxMoveCleanupEvery：多久檢查一次有沒有過了保留期的舊資料要刪。
	BoxMoveCleanupEvery = 10 * time.Minute
	// BoxMoveHelperTimeout：搬家時在主機上量資料大小、算檢查碼的輔助容器最多跑多久。
	BoxMoveHelperTimeout = 30 * time.Minute
	// BoxMoveTimeout：一個 Bot 搬家（不含把電腦映像傳到新主機）的上限。
	BoxMoveTimeout = 6 * time.Hour
	// BoxMoveProgressEvery：搬家進度事件最多多久發一次。
	BoxMoveProgressEvery = 500 * time.Millisecond
	// BackupKeepTimeout：還原備份前，在主機上把 Bot 電腦原本的資料複製一份保留，最多等多久。
	BackupKeepTimeout = 6 * time.Hour
	// BackupHostWait：xbot backup／restore 等一台遠端主機連上的上限；連不上就略過那台並在結果裡說明。
	BackupHostWait = 20 * time.Second
	// BoxMoveHistory：搬家紀錄（進度面板、xbot.computers.move_status）最多回傳幾筆。
	BoxMoveHistory = 20
	// BoxMoveManifestBytes：新主機上算出的檔案清單與檢查碼最大多少（約 100 萬個檔案）。
	BoxMoveManifestBytes int64 = 256 << 20

	// EgressImage 是「從家裡出去」每台主機上的 Tailscale 小容器的映像（固定版本）。
	EgressImage = "tailscale/tailscale:v1.88.3"
	// EgressProxyPort 是 Tailscale 小容器在 xbot-box 網路上提供的 HTTP proxy 埠。
	EgressProxyPort = 1055
	// EgressMemoryBytes 是 Tailscale 小容器的記憶體上限。
	EgressMemoryBytes int64 = 256 << 20
	// EgressStatusTimeout 是查 Tailscale 小容器狀態（tailscale status）的時限。
	EgressStatusTimeout = 10 * time.Second
	// EgressPullTimeout 是在主機上拉 Tailscale 映像的時限。
	EgressPullTimeout = 10 * time.Minute
)

// 例行清理（BX-14）：讓 Bot 的電腦不會被會自己長回來的東西塞滿。出自 LazyBoy server/cleanup.py。
const (
	// CleanupScratchAge：引擎的暫存（/hermes/cache/scratch）多久沒動就刪。出自 server/cleanup.py SCRATCH_DAYS（3 天）。
	// XBot 的 /workspace/tmp（Bot 放中間檔的地方）也用這個時間。
	CleanupScratchAge = 3 * 24 * time.Hour
	// CleanupUploadAge：你傳的附件（/workspace/uploads）放多久就刪。出自 server/cleanup.py UPLOAD_DAYS（30 天）。
	CleanupUploadAge = 30 * 24 * time.Hour
	// CleanupBrowserCacheBytes：瀏覽器快取超過多少才清。出自 server/cleanup.py BROWSER_CACHE_LIMIT（200 MB）。
	CleanupBrowserCacheBytes int64 = 200 << 20
	// CleanupFirstDelay、CleanupEvery：服務開始後多久做第一次、之後多久一次。出自 server/cleanup.py daily()（120 秒、每天）。
	CleanupFirstDelay = 2 * time.Minute
	CleanupEvery      = 24 * time.Hour
	// CleanupLiveCacheTimeout：請開著的瀏覽器清快取的指令最多跑多久。出自 server/cleanup.py clear_browser_cache()（timeout=30）。
	CleanupLiveCacheTimeout = 30 * time.Second
	// CleanupLiveCacheChecks、CleanupLiveCachePoll：瀏覽器收到後在背景清空快取，最多再看幾次、每次隔多久。
	// 出自 server/cleanup.py clear_browser_cache()（10 次、每秒）。
	CleanupLiveCacheChecks = 10
	CleanupLiveCachePoll   = time.Second
	// CleanupBotTimeout：一個 Bot 的清理或量用量（含輔助容器）最多多久。XBot 新增：LazyBoy 直接讀寫主機上的資料夾，
	// XBot 的資料在 volume 裡（可能在遠端主機），沒開機時要開一個輔助容器。
	CleanupBotTimeout = 10 * time.Minute
	// CleanupHelperTmpBytes：清理用的輔助容器的 /tmp 大小（只放一個時間標記檔）。XBot 新增。
	CleanupHelperTmpBytes int64 = 1 << 20
)
