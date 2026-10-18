package limits

import "time"

// 內建 Tailscale（tsnet）與外部入口（Funnel／反向代理，C35，PRD §17、MCP-2）。全部是 XBot 新增。

const (
	// TailnetHostname 是服務在 tailnet 上的預設名字：https://xbot.<tailnet>.ts.net（PRD TS-1）。
	TailnetHostname = "xbot"

	// TailnetHTTPSPort 是 tailnet（以及 Funnel）上提供 HTTPS 的埠。Funnel 只開放 443、8443、10000；用 443 網址不用帶埠。
	TailnetHTTPSPort = 443

	// TailscalePoll 是內建 Tailscale 狀態的檢查間隔（等登入、看連線、看憑證）。
	TailscalePoll = 2 * time.Second

	// TailscaleCertTimeout 是第一次向 Tailscale 拿 HTTPS 憑證最多等多久。
	TailscaleCertTimeout = 60 * time.Second

	// WhoIsCacheTTL 是 tailnet 來源 IP → Tailscale 使用者的快取時間（登入畫面的提示用）。
	WhoIsCacheTTL = 5 * time.Minute

	// ExternalRequestsPerMin 是外部入口（Funnel／反向代理）每個來源 IP 每分鐘最多幾個請求，在驗證權杖之前就擋。
	ExternalRequestsPerMin = 120

	// ExternalAgentRatePerMin 是代理經外部入口每分鐘最多呼叫幾次操作（另一個計數，比 tailnet 內的 AgentRatePerMin 嚴；
	// 代理自己的上限更低時用它自己的）。PRD MCP-2「速率上限較嚴」。
	ExternalAgentRatePerMin = 20

	// ExternalIPTrackMax 是外部入口限速最多記住幾個來源 IP，超過時先清掉過期的。
	ExternalIPTrackMax = 10000

	// AllowIPsMax 是外部入口 IP 白名單最多幾筆。
	AllowIPsMax = 50

	// TailnetHostnameMax 是 tailnet 名字的長度上限（DNS label 63）。
	TailnetHostnameMax = 63
)
