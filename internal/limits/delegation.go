package limits

import "time"

// 代為核准與代理上限（C21，PRD MCP-8～MCP-12）。
// 操作輸入的 struct 標籤（max:"…"）寫的是同一個數字；internal/delegation 與 internal/auth 的測試會檢查兩邊一致。

const (
	// AgentRatePerMin 是代理每分鐘最多呼叫幾次（沒有另外設定時）。XBot 新增：PRD MCP-12 只說「呼叫速率上限」、
	// LazyBoy 沒有對應的值；取每秒 1 次的平均，夠代理正常操作，又擋得住失控的迴圈。外部入口（C35）會更嚴。
	AgentRatePerMin = 60

	// AgentRatePerMinMax 是可以設定的每分鐘呼叫上限。XBot 新增。
	AgentRatePerMinMax = 600

	// AgentDailyCallsMax 是可以設定的每日呼叫上限。XBot 新增（design/J12 範例是 200～500 次）。
	AgentDailyCallsMax = 100000

	// AgentDailySpendMaxMicros 是可以設定的每日花費上限（美元百萬分之一）：US$1,000。XBot 新增。
	AgentDailySpendMaxMicros = 1_000_000_000

	// DelegationDailyCountMax 是代為核准每天最多可以設幾次。XBot 新增（design/J12 範例是 5 次）。
	DelegationDailyCountMax = 100

	// DelegationDailyAmountMaxMicros 是代為核准每天最多可以核准多少錢（美元百萬分之一）：US$1,000。
	// XBot 新增：PRD MCP-10 寫「小額花費」，再多就該你本人決定。
	DelegationDailyAmountMaxMicros = 1_000_000_000

	// DelegationMaxDays 是代為核准授權最長幾天。出自 design/J12（有效期限 1／7／30 天）。
	DelegationMaxDays = 30

	// AgentRestoreWindow 是撤銷代理後多久內可以按「復原」。出自 design/J12（撤銷可復原），沿用 UndoWindow。
	AgentRestoreWindow = UndoWindow

	// RateWindow 是速率上限的計算區間。
	RateWindow = time.Minute
)
