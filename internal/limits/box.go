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
