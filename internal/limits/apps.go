package limits

import "time"

// 連接應用程式與登入（C34，PRD SK-5、SK-6、BX-10、SEC-5）。

const (
	// AppOAuthTimeout 是一次應用程式登入（OAuth）最多等多久。LazyBoy server/connectors.py OAUTH_TIMEOUT（15 分鐘）。
	AppOAuthTimeout = 15 * time.Minute

	// AppOAuthPending 是同時進行中的登入最多幾個（XBot 新增：防止一直開新的登入把記憶體塞滿）。
	AppOAuthPending = 32

	// AppTokenRefreshSkew 是存取權杖到期前多久就先換新（XBot 新增）。
	AppTokenRefreshSkew = 60 * time.Second

	// AppHTTPTimeout 是 XBot 呼叫遠端 MCP 與授權伺服器一次最多等多久。I04 的「等待 60 秒」。
	AppHTTPTimeout = 60 * time.Second

	// AppResultChars 是一次工具結果交給 Bot 的字數上限（I02、I04「單次結果上限 25,000 字」）。
	AppResultChars = 25_000

	// AppBodyBytes 是轉送遠端 MCP 一次請求或回應的大小上限（XBot 新增）。
	AppBodyBytes int64 = 8 << 20

	// AppUsesKept 是每個連接留幾筆「最近使用」（I02，XBot 新增）。
	AppUsesKept = 50

	// AppConnectionsMax 是最多幾個連接（含自訂 MCP；XBot 新增）。
	AppConnectionsMax = 200

	// AppToolsMax 是一個連接記下幾個工具名稱（LazyBoy set_tools 的 500）。
	AppToolsMax = 500

	// AppEnvMax 是自訂本機程式最多幾個環境變數、標頭最多幾個（XBot 新增）。
	AppEnvMax = 32

	// LoginShareUndo 是分享登入後多久內可以復原（E09；同代為核准的 10 分鐘）。
	LoginShareUndo = 10 * time.Minute

	// LoginScriptTimeout 是在 Bot 的瀏覽器讀寫登入（cookies）一次最多等多久。LazyBoy server/logins.py 的 40 秒。
	LoginScriptTimeout = 40 * time.Second

	// LoginCookiesBytes 是讀一個 Bot 瀏覽器全部 cookies 的大小上限（XBot 新增）。
	LoginCookiesBytes int64 = 16 << 20

	// LoginSitesMax 是一次分享或移除最多幾個網站（XBot 新增）。
	LoginSitesMax = 50
)
