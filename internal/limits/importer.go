package limits

// 從 LazyBoy、OpenMausBot 匯入（V10，PRD ON-5、BT-8）。都是 XBot 新增。

const (
	// ImportDBBytes 是讀舊系統資料庫（LazyBoy product.sqlite3、OpenMausBot 的 JSON 檔）時，單一檔案的大小上限。
	// 資料庫先複製到暫存資料夾再開（不碰原本的檔案），所以要有上限。
	ImportDBBytes = 512 << 20

	// ImportJSONBytes 是 OpenMausBot 單一 JSON 設定檔（bots.json、routines.json…）的大小上限。
	ImportJSONBytes = 64 << 20

	// ImportSkillBytes 是一個技能（SKILL.md 加附帶的檔案）的大小上限；超過的不帶，列在結果裡。
	ImportSkillBytes = 4 << 20

	// ImportSkillsPerBot 是一個 Bot 最多帶幾個技能。
	ImportSkillsPerBot = 100

	// ImportBatchBytes 是把電腦資料寫進新電腦時，一批最多幾個位元組（一批是一次 docker cp）。
	ImportBatchBytes = 32 << 20

	// ImportFileBytes 是電腦資料裡單一檔案的上限；超過的略過並列在結果裡（例：很大的影片）。
	ImportFileBytes = 1 << 30

	// ImportDataBytes 是一個 Bot 的電腦資料（工作資料夾＋家目錄的檔案＋瀏覽器登入）總量上限。
	ImportDataBytes = 20 << 30

	// ImportMemoryFileBytes 是讀舊系統的一份記憶檔的上限。
	ImportMemoryFileBytes = 1 << 20

	// ImportRunsKept 是記住最近幾次匯入的結果（xbot.import.status）。
	ImportRunsKept = 5
)
