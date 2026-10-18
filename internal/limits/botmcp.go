package limits

// Bot 用的 XBot MCP（C11）：Bot 經 product.sock 呼叫的卡片、要求核准、隊友名冊。
// 操作輸入的 struct 標籤（max:"…"）寫的是同一個數字。

const (
	// ApprovalSubjectMax 是 Bot 要求核准時「要核准什麼」的長度上限。出自 LazyBoy server/org.py:236 request_approval（plain(subject, 120)）。
	ApprovalSubjectMax = 120

	// ApprovalDetailsMax 是核准說明的長度上限。出自 LazyBoy server/org.py:244 request_approval（details[:4000]）。
	ApprovalDetailsMax = 4000

	// ApprovalCategoryMax 是核准類別的長度上限。出自 LazyBoy server/org.py:245 request_approval（plain(category, 40)）。
	ApprovalCategoryMax = 40

	// ApprovalPendingMax 是一個 Bot 同時在等核准的件數上限。出自 LazyBoy server/org.py:241 request_approval（len(open_rows) >= 10）。
	ApprovalPendingMax = 10

	// RosterAboutMax 是隊友名冊裡每個 Bot 介紹的長度。出自 LazyBoy server/collab.py:124 directory（plain(persona, 160)）；PRD AA-1。
	RosterAboutMax = 160

	// CardTitleMax、CardBodyMax 是 Bot 發到對話裡的卡片標題與內文上限。XBot 新增（和核准的主旨、說明一樣）。
	CardTitleMax = ApprovalSubjectMax
	CardBodyMax  = ApprovalDetailsMax

	// CardFilesMax 是一張檔案卡最多幾個檔案。同 Bot 傳話的帶檔上限（LazyBoy server/collab.py:62 CARRY_FILES；PRD AA-7）。
	CardFilesMax = CarryFilesMax

	// CardPathMax 是檔案路徑的長度上限。XBot 新增（Linux PATH_MAX 4096 的四分之一，夠用）。
	CardPathMax = 1024
)
