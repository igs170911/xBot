package limits

import "time"

// 任務完成判定（X04，PRD §19、§20 用途 2）。
const (
	// JudgeTaskDoneWait 是「任務完成了沒」判斷最多等多久；逾時或出錯退回規則（FJ-2）。
	// XBot 新增（PRD §20.2 用途 2：等待 1 秒）。
	JudgeTaskDoneWait = 1 * time.Second

	// JudgeReasonMax 是判斷原因（一行）最多幾個字。XBot 新增（FJ-1：一句原因）。
	JudgeReasonMax = 120

	// JudgeNoteMax 是「不對？」改正時附註最多幾個字。XBot 新增。
	JudgeNoteMax = 500

	// JudgeAskMax 是「需要你」卡片上引用 Bot 那段話最多幾個字。XBot 新增（同完成通知的 120 字）。
	JudgeAskMax = 300

	// StrandedGoalKey 是比對「同一個背景子任務」時看目標的前幾個字。LazyBoy server/runtime.py:722（goal[:80]）
	StrandedGoalKey = 80
)
