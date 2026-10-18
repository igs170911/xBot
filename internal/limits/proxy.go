package limits

import "time"

// Model Proxy（E01）。

const (
	// ModelProxyBodyBytes 是容器送進 Model Proxy 的單一請求上限。
	// LazyBoy 沒有設上限；這裡取 64 MB，足夠放一張 20 MB 圖片的 base64（server/main.py UPLOAD_LIMITS image_bytes）加上對話。
	ModelProxyBodyBytes int64 = 64 << 20

	// ModelUsageJSONBytes 是讀用量時單一 JSON 回應（或單一 SSE 行）最多保留的大小；超過就放棄計量。
	// 出自 LazyBoy server/usage.py JSON_LIMIT。
	ModelUsageJSONBytes = 4 << 20

	// ModelConnectTimeout 是連上模型供應商的時限（server/main.py model_http：connect=15）。
	ModelConnectTimeout = 15 * time.Second

	// ModelResponseHeaderTimeout 是等供應商開始回應的時限（server/main.py model_http：timeout=300）。
	ModelResponseHeaderTimeout = 300 * time.Second

	// ModelMaxConns、ModelMaxIdleConns、ModelIdleConnTimeout 是到供應商的連線池
	// （server/main.py model_http：max_connections=100、max_keepalive_connections=32、keepalive_expiry=60）。
	ModelMaxConns        = 100
	ModelMaxIdleConns    = 32
	ModelIdleConnTimeout = 60 * time.Second
)

// Model Proxy 完整版（E11）。

const (
	// ModelStreamIdleTimeout 是串流回應兩段資料之間最久等多久；超過就當供應商斷線，結束這次請求。
	// 出自 LazyBoy server/main.py model_http：httpx.Timeout(300) 的 read 時限（每次讀取之間）。
	ModelStreamIdleTimeout = 300 * time.Second

	// ModelProbeTimeout 是模型探測每個請求的時限（server/model_check.py check_model：timeout=20）。
	ModelProbeTimeout = 20 * time.Second

	// ModelProbeBodyBytes 是探測時讀供應商回應的上限（錯誤訊息只取前 400 字，模型清單可能較長）。XBot 新增。
	ModelProbeBodyBytes = 4 << 20

	// ModelProbeDetailChars 是探測失敗時保留的錯誤訊息長度（server/model_check.py _error_text：[:400]）。
	ModelProbeDetailChars = 400

	// ModelProbeMaxModels 是探測時最多記下幾個模型名稱。XBot 新增（避免把上千個模型的清單存進設定）。
	ModelProbeMaxModels = 500

	// UsageExportMaxRows 是匯出 CSV 一次最多幾筆。XBot 新增（約 10 MB）。
	UsageExportMaxRows = 100_000

	// UsageSummaryMaxDays 是用量統計最多看幾天（server/usage.py summary：min(days, 365)）。
	UsageSummaryMaxDays = 365

	// UsageSummaryDefaultDays 是用量統計預設看幾天（server/usage.py summary：days=30）。
	UsageSummaryDefaultDays = 30
)
