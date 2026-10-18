package limits

import "time"

// MCP 出口與代理配對（C04）。

const (
	// PairCodeTTL 是配對碼的有效時間。出自 PRD MCP-6／§17（一次性、5 分鐘）；OpenMausBot 的配對碼也是 5 分鐘。
	PairCodeTTL = 5 * time.Minute

	// PairCodeLen 是配對碼的長度（不含分隔線）。XBot 新增：任務卡要求 6～8 碼，取 8 碼。
	// 字母表 32 個字（去掉 0/O/1/I 這類容易看錯的字），32^8 ≈ 1.1 兆種，加上下面的鎖定，猜不到。
	PairCodeLen = 8

	// PairMaxFailures、PairLockout：同一個來源連續換錯配對碼這麼多次就鎖住一段時間。
	// 出自 OpenMausBot 配對的程式值（錯誤 10 次鎖 60 秒；文件寫 5 次 10 分鐘，以程式為準）。
	PairMaxFailures = 10
	PairLockout     = 60 * time.Second

	// PairGlobalMaxFailures 是所有來源合計的失敗上限（同樣鎖 PairLockout）。XBot 新增：
	// 經 Tailscale serve 或反向代理進來的請求來源都是 127.0.0.1，分散猜碼也要擋。
	PairGlobalMaxFailures = 50

	// PairMaxPending 是同時有效（還沒用、還沒過期）的配對碼上限。XBot 新增。
	PairMaxPending = 20

	// AgentTokenTouchEvery 是記錄代理權杖「最後使用時間」的最小間隔，避免每個請求都寫資料庫。XBot 新增。
	AgentTokenTouchEvery = time.Minute

	// MCPSessionIdle 是 Streamable HTTP 的 MCP 連線閒置多久後關閉。XBot 新增。
	MCPSessionIdle = 30 * time.Minute

	// MCPBodyBytes 是單一 MCP 請求的大小上限。XBot 新增（檔案上傳不走 MCP 參數）。
	MCPBodyBytes int64 = 8 << 20
)
