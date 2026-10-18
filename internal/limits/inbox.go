package limits

import "time"

// 待我處理與核准（C12）。

const (
	// QuestionTTL 是無人在場的工作（不是你自己交辦的）裡，Bot 的提問、核准、密碼請求等多久沒回就過期：
	// 核准自動拒絕、提問回「使用者沒有回應，不要猜答案」。你自己交辦的工作不會過期。
	// 出自 LazyBoy server/runtime.py（QUESTION_TTL = 600、unattended()、expire_question()）。
	QuestionTTL = 600 * time.Second

	// UndoWindow 是處理過的卡多久內可以「復原」（退回的核准、略過的卡）。
	// 出自 PRD MCP-10（代為核准「可在 10 分鐘內復原」）；LazyBoy 沒有復原。
	UndoWindow = 10 * time.Minute

	// AnswerTextMax 是回答 Bot 提問的文字長度上限。XBot 新增：和你自己一則訊息一樣（ChatTextMax）。
	AnswerTextMax = ChatTextMax

	// SecretValueMax 是密碼、金鑰、驗證碼這類輸入的長度上限。XBot 新增（LazyBoy 沒有上限；金鑰再長也不會超過 16K）。
	SecretValueMax = 16384

	// StepsPageMax 是 chat.steps 一次最多回幾筆事件。XBot 新增（同 SSE 補送的批次大小）。
	StepsPageMax = SSEBatch
)
