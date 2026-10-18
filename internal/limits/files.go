package limits

import "time"

// 上傳、檔案、教學（LazyBoy server/main.py、files.py、teaching.py）。

const (
	// UploadFilesMax 是你一則訊息最多附幾個檔案。LazyBoy server/main.py:283 UPLOAD_LIMITS["files"]
	UploadFilesMax = 10

	// UploadFileBytes 是一個附檔的大小上限。LazyBoy server/main.py:283 UPLOAD_LIMITS["file_bytes"]
	UploadFileBytes int64 = 50 << 20

	// UploadImageBytes 是一張要給模型看的圖片的大小上限。LazyBoy server/main.py:283 UPLOAD_LIMITS["image_bytes"]
	UploadImageBytes int64 = 20 << 20

	// UploadTotalBytes 是一則訊息附檔合計上限。LazyBoy server/main.py:283 UPLOAD_LIMITS["total_bytes"]
	UploadTotalBytes int64 = 200 << 20

	// AvatarPNGBytes 是上傳頭像圖片的上限。LazyBoy server/main.py:891（8*1024*1024）
	AvatarPNGBytes int64 = 8 << 20

	// FolderEntries 是檔案瀏覽一個資料夾最多列幾項。LazyBoy server/main.py:681 FOLDER_ENTRIES
	FolderEntries = 300

	// FileNameMax 是存檔時檔名（一段路徑）的長度。LazyBoy server/files.py:18 filename（[:180]）
	FileNameMax = 180

	// TeachStepsMax 是一次示範教學最多記錄幾個步驟。LazyBoy server/teaching.py:16 MAX_STEPS
	TeachStepsMax = 300

	// TeachStartTimeout、TeachStopTimeout 是開始與結束示範錄製的時限。
	// LazyBoy server/teaching.py:159 START_TIMEOUT、:160 STOP_TIMEOUT
	TeachStartTimeout = 15 * time.Second
	TeachStopTimeout  = 6 * time.Second

	// TeachGoalMax、TeachNoteMax 是教學目標與註記的長度。LazyBoy server/teaching.py:203（goal[:200]）、:254（text[:500]）
	TeachGoalMax = 200
	TeachNoteMax = 500
)
