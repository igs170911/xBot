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

	// FilesScanMax 是「全部檔案」一次最多列幾個檔案（略過隱藏檔與套件資料夾）。LazyBoy server/runtime.py:1185 scan_files（500）
	FilesScanMax = 500

	// FilesFolderScanMax 是瀏覽一個資料夾時最多讀幾項（之後分頁，每頁 FolderEntries）。XBot 新增。
	FilesFolderScanMax = 10000

	// FilesWalkScanMax 是找「全部檔案」時最多看幾個檔案（再挑出前 FilesScanMax 個）。XBot 新增。
	FilesWalkScanMax = 20000

	// FilesListOutputBytes 是列檔案時從 Bot 電腦讀回的清單大小上限。XBot 新增。
	FilesListOutputBytes int64 = 8 << 20

	// FilePathDepth 是檔案路徑最多幾層（檢查連結要一層一層看）。XBot 新增。
	FilePathDepth = 32

	// FilePathMax 是檔案路徑的長度（同 CardPathMax）。XBot 新增。
	FilePathMax = 1024

	// FilePreviewBytes、FilePreviewBytesMax 是文字預覽預設與最多讀幾個位元組。XBot 新增。
	FilePreviewBytes    int64 = 256 << 10
	FilePreviewBytesMax int64 = 1 << 20

	// FilePreviewRows 是 CSV、Excel 預覽最多幾列。XBot 新增（E07「大檔只預覽前 1,000 列」）。
	FilePreviewRows = 1000

	// FilePreviewColumns 是表格預覽最多幾欄。XBot 新增。
	FilePreviewColumns = 100

	// FileSheetBytes 是 Excel（.xlsx）檔案超過多大就不預覽，只給下載。XBot 新增。
	FileSheetBytes int64 = 20 << 20

	// FileSheetXMLBytes 是預覽 Excel 時解壓縮後最多讀幾個位元組（擋壓縮炸彈）。XBot 新增。
	FileSheetXMLBytes int64 = 64 << 20

	// ThumbSize 是縮圖的長邊（像素）。XBot 新增。
	ThumbSize = 320

	// ThumbPixelsMax 是做縮圖的原圖最多幾個像素（擋解壓縮炸彈）。XBot 新增。
	ThumbPixelsMax = 50_000_000

	// FileWriteTextMax 是 xbot.files.write 一次寫入的文字大小。XBot 新增（同 API 請求上限以內）。
	FileWriteTextMax = 512 << 10

	// ArchiveEntries 是壓縮檔預覽最多列幾項。LazyBoy server/previews.py:20 ARCHIVE_ENTRIES
	ArchiveEntries = 2000

	// ArchiveCountLimit 是壓縮檔預覽最多數到幾項（擋大量小檔的炸彈）。LazyBoy server/previews.py:22 ARCHIVE_COUNT_LIMIT
	ArchiveCountLimit = 100_000

	// TeachStepsMax 是一次示範教學最多記錄幾個步驟。LazyBoy server/teaching.py:16 MAX_STEPS
	TeachStepsMax = 300

	// TeachStartTimeout、TeachStopTimeout 是開始與結束示範錄製的時限。
	// LazyBoy server/teaching.py:159 START_TIMEOUT、:160 STOP_TIMEOUT
	TeachStartTimeout = 15 * time.Second
	TeachStopTimeout  = 6 * time.Second

	// TeachGoalMax、TeachNoteMax 是教學目標與註記的長度。LazyBoy server/teaching.py:203（goal[:200]）、:254（text[:500]）
	TeachGoalMax = 200
	TeachNoteMax = 500

	// TeachPoll 是示範中多久看一次錄到的新步驟（即時步驟清單）。LazyBoy server/teaching.py watch() asyncio.sleep(1)
	TeachPoll = 1 * time.Second

	// TeachSubmitMerge：點擊或按鍵之後這麼短的時間內送出表單，算同一步。LazyBoy server/teaching.py normalize() gap < 1500
	TeachSubmitMerge = 1500 * time.Millisecond
	// TeachNavigateMerge：連續的換頁（轉址）在這段時間內算同一步。LazyBoy server/teaching.py normalize() gap < 2500
	TeachNavigateMerge = 2500 * time.Millisecond
	// TeachTabMerge：開新分頁後這段時間內的換頁算「在新分頁前往」。LazyBoy server/teaching.py normalize() gap < 4000
	TeachTabMerge = 4000 * time.Millisecond
	// TeachCauseWindow：點擊、送出之後這段時間內的換頁算那一步的結果。LazyBoy server/teaching.py normalize() < 5000
	TeachCauseWindow = 5000 * time.Millisecond

	// TeachKeyShots 是一個學成的技能最多附幾張截圖。LazyBoy server/teaching.py key_shots(limit=8)
	TeachKeyShots = 8
	// TeachShotBytes 是一張示範截圖最多讀多大（介面縮圖、存進技能）。XBot 新增（錄製器的 JPEG 品質 60，一張約 100–300 KB）。
	TeachShotBytes = 2 << 20
	// TeachRowsBytes 是錄製紀錄（browser.jsonl＋desktop.jsonl）最多讀多大。XBot 新增（300 步遠低於此）。
	TeachRowsBytes = 8 << 20
)
