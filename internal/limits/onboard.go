package limits

import "time"

// 首次設定、頭像圖片、分享與匯入 Bot（W32，PRD ON-*、BT-2、BT-8）。

const (
	// LocalModelProbeTimeout 是首次設定偵測本機模型（LM Studio、Ollama）時，每個網址最多等多久。XBot 新增：
	// 本機服務通常幾毫秒就回，沒開時連線會立刻被拒；等太久會拖慢首次設定的第一個畫面。
	LocalModelProbeTimeout = 800 * time.Millisecond

	// LocalModelListBytes 是本機模型服務回傳的模型清單大小上限。XBot 新增。
	LocalModelListBytes = 1 << 20

	// OnboardImageCheckTimeout 是首次設定檢查 Bot 電腦映像（問 Docker）最多等多久。XBot 新增。
	OnboardImageCheckTimeout = 5 * time.Second

	// LocalModelsMax 是偵測到的本機模型最多列幾個。XBot 新增。
	LocalModelsMax = 20

	// AvatarUploadBytes 是上傳頭像原圖的大小上限（PNG、JPEG、GIF、WebP）。OpenMausBot 的頭像上傳上限 10 MB。
	AvatarUploadBytes = 10 << 20

	// AvatarImageBytes 是存起來的頭像圖片（瀏覽器裁好、縮成 256×256 的 data URL）的長度上限。XBot 新增：
	// 裁好的圖存在 Bot 設定裡，每次列 Bot 都會帶上，所以要小。
	AvatarImageBytes = 400_000

	// AvatarImagePixels 是裁好的頭像邊長（像素）。XBot 新增。
	AvatarImagePixels = 256

	// BotFileBytes 是分享 Bot 的 .xbot 檔大小上限（匯出、匯入都檢查）。XBot 新增：人設＋技能文字＋頭像圖片；
	// 匯入時整份檔案放在一個操作請求裡（APIBodyMax 1 MiB，JSON 跳脫後會變大），所以留餘裕。
	BotFileBytes = 800_000

	// BotFileSkillsMax 是一個 .xbot 檔最多帶幾個技能。XBot 新增。
	BotFileSkillsMax = 50

	// BotFileVersion 是 .xbot 檔的格式版本；比它新的檔案不能匯入（G14「版本太新」）。
	BotFileVersion = 1
)
