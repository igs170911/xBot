package limits

import "time"

// 定時任務（PRD §12、RT-*、§19 目標型定時任務；LazyBoy server/scheduling.py）。

const (
	// RoutinesPerBot 是每個 Bot 最多幾個定時任務。LazyBoy server/scheduling.py:26 MAX_ROUTINES
	RoutinesPerBot = 50

	// RoutineIntervalMin、RoutineIntervalMax 是間隔型定時任務的間隔範圍。
	// LazyBoy server/scheduling.py:27 MIN_INTERVAL（5 分鐘）、:28 MAX_INTERVAL（7 天）
	RoutineIntervalMin = 5 * time.Minute
	RoutineIntervalMax = 7 * 24 * time.Hour

	// RoutineJitterMaxMinutes 是隨機延後的上限（分鐘）；後端接受 0～240，間隔型還要小於間隔。
	// LazyBoy server/scheduling.py:31 MAX_JITTER（檢查在 :89、:92）
	RoutineJitterMaxMinutes = 240

	// RoutineHistory 是每個定時任務保留幾筆執行紀錄。LazyBoy server/scheduling.py:32 HISTORY
	RoutineHistory = 20

	// RoutineHookSpacing 是同一個網址觸發最少要隔多久（太密回 429）。LazyBoy server/scheduling.py:34 HOOK_SPACING
	RoutineHookSpacing = 60 * time.Second

	// RoutineHookBytes 是網址觸發的請求內容上限（超過回 413）。LazyBoy server/scheduling.py:35 HOOK_BYTES
	RoutineHookBytes = 64 << 10

	// RoutineHookTextMax 是網址觸發的內容交給 Bot 時保留的字元數。LazyBoy server/scheduling.py:215（payload[:8000]）
	RoutineHookTextMax = 8000

	// GoalRoundsDefault、GoalRoundsMax 是目標型定時任務的輪數預設與上限；用完輪數自動停用。
	// LazyBoy server/scheduling.py:41 GOAL_ROUNDS, MAX_GOAL_ROUNDS
	GoalRoundsDefault = 10
	GoalRoundsMax     = 50

	// GoalDoneWhenMax、GoalSummaryMax 是完成條件與完成摘要的長度。
	// LazyBoy server/scheduling.py:238（done_when[:1000]）、:258（summary[:4000]）
	GoalDoneWhenMax = 1000
	GoalSummaryMax  = 4000

	// RoutineTitleMax 是定時任務標題長度。LazyBoy server/scheduling.py:188（title[:80]）
	RoutineTitleMax = 80

	// RoutineErrorMax 是啟動失敗時保留的錯誤訊息長度。LazyBoy server/scheduling.py:284（str(exc)[:500]）
	RoutineErrorMax = 500

	// RoutineTick 是排程器檢查到期任務的間隔。LazyBoy server/scheduling.py:293（asyncio.sleep(2)）
	RoutineTick = 2 * time.Second
)

// RoutineStartRetryDelays 是定時任務啟動失敗後的退避時間，依失敗次數取用，第 4 次以後都是最後一個。
// （和模型暫時出錯的 ProviderRetryDelays 是兩回事。）LazyBoy server/scheduling.py:24 RETRY_MINUTES（1, 5, 15, 60）
func RoutineStartRetryDelays() []time.Duration {
	return []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 60 * time.Minute}
}

// RoutineJitterChoices 是畫面上「時間飄移」下拉選單的選項（分鐘，另有 0＝不延後）。
// 後端接受 0～RoutineJitterMaxMinutes 的任何整數。LazyBoy web/src/routines.tsx:148
func RoutineJitterChoices() []int {
	return []int{15, 30, 60, 90, 120}
}
