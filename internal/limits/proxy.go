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
