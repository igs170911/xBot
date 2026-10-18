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
