package limits

import "time"

// 遠端主機的 SSH 連線（B11）。LazyBoy 沒有遠端主機，以下數字是 XBot 自訂，依 docs/spike-ssh-forward.md 的實測選定。

const (
	// SSHDialTimeout 是建立 TCP 連線加上 SSH 握手的時限。
	SSHDialTimeout = 15 * time.Second

	// SSHKeepaliveInterval、SSHKeepaliveTimeout：每 10 秒送一次 keepalive@openssh.com，
	// 15 秒內沒回應就當成斷線並重連。用來抓「TCP 沒斷但對方已經沒反應」（網路中斷、主機凍結）。
	SSHKeepaliveInterval = 10 * time.Second
	SSHKeepaliveTimeout  = 15 * time.Second

	// SSHReconnectMin、SSHReconnectMax 是斷線後重連的退避時間：斷線後先立刻重試一次，
	// 再從 0.5 秒開始每次加倍，最多 15 秒。
	SSHReconnectMin = 500 * time.Millisecond
	SSHReconnectMax = 15 * time.Second

	// SSHStableAfter：連線維持超過這麼久才算穩定，斷掉時立刻重試、退避歸零。
	SSHStableAfter = 2 * time.Second

	// SSHWaitOnline 是主機正在重連時，一個 Docker API 呼叫或轉發最多等多久，逾時就回報「主機離線」。
	SSHWaitOnline = 10 * time.Second
)

// SSHRemoteDataDir 是遠端主機上 XBot 的資料目錄（PRD §16.3：/var/lib/xbot/bots/<id>/…），由 xbot 帳號擁有。
const SSHRemoteDataDir = "/var/lib/xbot"

// 主機管理（B12）。LazyBoy 沒有遠端主機，以下是 XBot 自訂。
const (
	// HostMinDockerMajor：遠端主機的 Docker 至少要 26 版（PLAN §2 make doctor 同一個門檻）。
	HostMinDockerMajor = 26
	// HostMinDiskBytes：Docker 資料目錄至少要有 10 GB 可用（電腦映像約 3 GB，加上每個 Bot 的資料）。
	HostMinDiskBytes = 10 << 30
	// HostDefaultMaxRunning 是新增主機時「同時開機上限」的預設值（J11 步驟 3）。
	HostDefaultMaxRunning = 3
	// HostMaxRunningCap 是同時開機上限能設的最大值。
	HostMaxRunningCap = 64
	// HostWatchInterval 是檢查主機連線狀態、寫回 host.status 的間隔。
	HostWatchInterval = time.Second
	// HostLocalPingInterval 是檢查本機 Docker 還在不在的間隔。
	HostLocalPingInterval = 30 * time.Second
	// HostQueuePoll：工作在排隊等電腦（主機離線或滿了）時，多久重新看一次。
	HostQueuePoll = 2 * time.Second
	// HostImageProgressEvery：載入電腦映像時，進度事件最多多久發一次。
	HostImageProgressEvery = 500 * time.Millisecond
	// HostCheckTimeout 是一次主機檢查（不含載入映像）的時限。
	HostCheckTimeout = 60 * time.Second
	// HostCheckGrace：檢查一台從來沒連上過的主機時，連不上（例：sshd 剛重新載入）最多再試這麼久才回報。
	HostCheckGrace = 3 * time.Second
	// HostImageLoadTimeout 是把電腦映像傳到一台主機的時限。
	HostImageLoadTimeout = 2 * time.Hour
)

// HostSSHUser 是遠端主機上 XBot 用的帳號（安裝指令會建立）。
const HostSSHUser = "xbot"
