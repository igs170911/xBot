package limits

import "time"

// 第一批核心操作（C05）：Bot、對話、核准、待我處理、稽核。
// 操作輸入的 struct 標籤（max:"…"）寫的是同一個數字；internal/catalog 的測試會檢查兩邊一致。

const (
	// BotNameMax 是 Bot 名稱的長度上限（字）。出自 LazyBoy server/main.py:100 update_agent（len(name) > 100 就拒絕）。
	BotNameMax = 100

	// BotTitleMax 是職稱的長度上限。XBot 新增（LazyBoy 沒有職稱上限；和名稱取一樣）。
	BotTitleMax = 100

	// BotDescriptionMax 是描述的長度上限。出自 LazyBoy server/org.py:386（description = plain(persona, 1000)）。
	BotDescriptionMax = 1000

	// BotSoulMax 是 Soul（常駐指示）的長度上限。出自 LazyBoy server/org.py:386（persona[:4000]）。
	BotSoulMax = 4000

	// ChatTextMax 是你自己一則訊息的字元上限（長資料請用檔案）。
	// LazyBoy server/runtime.py:333 _submit（len(text)>64000）；PRD §12 也是 64,000。
	// （X01 更正：原本寫「LazyBoy 沒有上限」並取 100,000。）
	ChatTextMax = 64000

	// ChatWaitMax 是 chat.wait_until_settled、approvals.wait 一次最多等多久。出自任務卡 C05（timeout_s ≤ 300）。
	ChatWaitMax = 300 * time.Second

	// ChatWaitDefault 是沒有給 timeout_s 時等多久。XBot 新增。
	ChatWaitDefault = 60 * time.Second

	// WaitRecheck 是等待類操作在沒收到事件時，多久回資料庫確認一次狀態（事件可能被丟掉，或由別的程序寫入）。XBot 新增。
	WaitRecheck = time.Second

	// ApprovalWaitPoll 是 approvals.wait 檢查核准狀態的間隔。XBot 新增。
	ApprovalWaitPoll = 500 * time.Millisecond

	// PageDefault、PageMax 是列表類操作一頁的預設與最多筆數。XBot 新增。
	PageDefault = 50
	PageMax     = 200

	// ThreadPreviewRunes 是側欄、對話清單每一列最後一句話最多幾個字（同群組預覽）；ThreadPreviewScan 是往回找幾則
	// （最新一則可能是還沒寫字的串流）。XBot 新增。
	ThreadPreviewRunes = 60
	ThreadPreviewScan  = 3
)
