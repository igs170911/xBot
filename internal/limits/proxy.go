package limits

import "time"

// Model Proxy（E01）。

const (
	// ModelProxyBodyBytes 是容器送進 Model Proxy 的單一請求上限。
	// LazyBoy 沒有設上限；這裡取 64 MB，足夠放一張 20 MB 圖片的 base64（server/main.py:283 UPLOAD_LIMITS image_bytes）加上對話。
	ModelProxyBodyBytes int64 = 64 << 20

	// ModelUsageJSONBytes 是讀用量時單一 JSON 回應（或單一 SSE 行）最多保留的大小；超過就放棄計量。
	// 出自 LazyBoy server/usage.py:12 JSON_LIMIT。
	ModelUsageJSONBytes = 4 << 20

	// ModelConnectTimeout 是連上模型供應商的時限（server/main.py:1264 model_http：connect=15）。
	ModelConnectTimeout = 15 * time.Second

	// ModelResponseHeaderTimeout 是等供應商開始回應的時限（server/main.py:1264 model_http：timeout=300）。
	ModelResponseHeaderTimeout = 300 * time.Second

	// ModelMaxConns、ModelMaxIdleConns、ModelIdleConnTimeout 是到供應商的連線池
	// （server/main.py:1265 model_http：max_connections=100、max_keepalive_connections=32、keepalive_expiry=60）。
	ModelMaxConns        = 100
	ModelMaxIdleConns    = 32
	ModelIdleConnTimeout = 60 * time.Second

	// DefaultGlobalMonthlyMicros 是全部 Bot 合計的預設每月花費上限（百萬分之一美元；20 美元）。XBot 新增（V10）：
	// 新安裝直接套用；舊安裝沒有設全域上限時套用一次並發「待我處理」通知（之後你拿掉就不再自動加回）。
	DefaultGlobalMonthlyMicros int64 = 20_000_000
)

// Model Proxy 完整版（E11）。

const (
	// ModelStreamIdleTimeout 是串流回應兩段資料之間最久等多久；超過就當供應商斷線，結束這次請求。
	// 出自 LazyBoy server/main.py:1264 model_http：httpx.Timeout(300) 的 read 時限（每次讀取之間）。
	ModelStreamIdleTimeout = 300 * time.Second

	// ModelProbeTimeout 是模型探測每個請求的時限（server/model_check.py:123 check_model：timeout=20）。
	ModelProbeTimeout = 20 * time.Second

	// ModelProbeBodyBytes 是探測時讀供應商回應的上限（錯誤訊息只取前 400 字，模型清單可能較長）。XBot 新增。
	ModelProbeBodyBytes = 4 << 20

	// ModelProbeDetailChars 是探測失敗時保留的錯誤訊息長度（server/model_check.py:118–120 _error_text：[:400]）。
	ModelProbeDetailChars = 400

	// ModelProbeMaxModels 是探測時最多記下幾個模型名稱。XBot 新增（避免把上千個模型的清單存進設定）。
	ModelProbeMaxModels = 500

	// UsageExportMaxRows 是匯出 CSV 一次最多幾筆。XBot 新增（約 10 MB）。
	UsageExportMaxRows = 100_000

	// UsageSummaryMaxDays 是用量統計最多看幾天（server/usage.py:106 summary：min(days, 365)）。
	UsageSummaryMaxDays = 365

	// UsageSummaryDefaultDays 是用量統計預設看幾天（server/usage.py:104 summary：days=30）。
	UsageSummaryDefaultDays = 30
)

// 訂閱登入（E13 原型：ChatGPT 方案經 Hermes 的 openai-codex OAuth，權杖只在 Model Proxy）。
const (
	// OAuthRefreshSkew 是存取權杖到期前多久就先換新（Hermes auth_constants CODEX_ACCESS_TOKEN_REFRESH_SKEW_SECONDS=120）。
	OAuthRefreshSkew = 120 * time.Second

	// OAuthHTTPTimeout 是登入與換新權杖每個請求的時限（Hermes HERMES_CODEX_REFRESH_TIMEOUT_SECONDS 預設 20）。
	OAuthHTTPTimeout = 20 * time.Second

	// OAuthLoginTTL 是裝置碼登入最久等多久（Hermes _codex_poll_authorization_code：max_wait 15 分鐘）。
	OAuthLoginTTL = 15 * time.Minute

	// OAuthMinPollInterval 是向登入伺服器查詢結果的最短間隔（Hermes：max(3, interval)）。
	OAuthMinPollInterval = 3 * time.Second

	// OAuthMaxPendingLogins 是同時進行中的登入最多幾個。XBot 新增（避免一直開新登入塞滿記憶體）。
	OAuthMaxPendingLogins = 8

	// OAuthBodyBytes 是讀登入伺服器回應的上限（Hermes _CODEX_AUTH_BODY_MAX_BYTES = 1 MiB）。
	OAuthBodyBytes = 1 << 20
)
