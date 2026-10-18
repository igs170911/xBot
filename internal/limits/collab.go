package limits

import "time"

// Bot 之間交談的限制（X01，給 X02 傳話與群組、X03 團隊與議程、X04 完成判定用）。
// 全部是 LazyBoy DEV 的程式值；註解寫出處「檔案:行號 名稱」。PRD §12、§18 和程式不一致時以程式為準，
// 對照表見 docs/limits.md。

// 傳話（PRD §18、AA-*、LP-*）。
const (
	// MaxHops 是同一條 Bot 往來最多幾次；超過就拒絕並要 Bot 回報使用者。結果回傳（relay）不擋。
	// LazyBoy server/collab.py:30 MAX_HOPS
	MaxHops = 6

	// BotMessageMax 是 Bot 一則傳話（也是群組一則訊息）的字元上限。Bot 主動送的超過就拒絕；
	// 交回的結果超過就存成檔案、訊息只帶 ResultPreview 字預覽。
	// LazyBoy server/collab.py:64 MESSAGE_MAX
	BotMessageMax = 12000

	// ResultPreview 是太長的結果存成檔案後，訊息裡保留的開頭字數。
	// LazyBoy server/collab.py:64 RESULT_PREVIEW
	ResultPreview = 3000

	// CarryFilesMax 是一則傳話最多帶幾個檔案（訊息裡的 /workspace 路徑）；超過的不複製、不報錯。
	// LazyBoy server/collab.py:62 CARRY_FILES
	CarryFilesMax = 10

	// CarryBytesMax 是一則傳話帶的檔案合計大小上限；會超過的那個檔案略過不複製。
	// LazyBoy server/collab.py:62 CARRY_BYTES
	CarryBytesMax int64 = 50 << 20

	// SilentTailMax 是 "[silent]" 後面最多還能多幾個字，仍算「不發言」。
	// LazyBoy server/collab.py:56 is_silent（len(SILENT) + 40）
	SilentTailMax = 40
)

// 群組（PRD §18、RM-*、LP-3）。
const (
	// RoomMembersMin、RoomMembersMax 是群組 Bot 人數（不含使用者）。
	// LazyBoy server/collab.py:40 ROOM_MIN, ROOM_MAX（模組說明寫 2 到 6，以程式 10 為準）
	RoomMembersMin = 2
	RoomMembersMax = 10

	// RoomRounds 是使用者每發言一次，Bot 互相喚醒最多幾輪；超過時訊息照發、不再喚醒別人。失敗的回合也中止接力。
	// LazyBoy server/collab.py:41 ROOM_ROUNDS（用在 :495 room_answer、:550 post_group）
	RoomRounds = 4

	// RoomBacklog 是成員第一次輪到（還沒有 seen 紀錄）時，從群組帶入的最近幾則訊息。
	// LazyBoy server/collab.py:468 room_prompt（len(transcript) - 20）
	RoomBacklog = 20

	// RoomNameMax 是群組名稱長度。LazyBoy server/collab.py:379 create_room（plain(name, 60)）
	RoomNameMax = 60

	// RoomBulletinMax 是群組公告（共同指示，RM-7）長度。XBot 新增（同 AgendaDetailsMax）。
	RoomBulletinMax = 4000

	// RoomReadMax 是讀群組訊息一次最多幾則（xbot.collab.room_read）。XBot 新增。
	RoomReadMax = 200
)

// 工作鏈預算與往來紀錄（X02；XBot 新增，LazyBoy 沒有）。
const (
	// ChainBudgetTokens 是一條 Bot 往來的工作鏈（同一個 chain 的所有工作）最多用掉多少 token（輸入＋輸出）。
	// 超過就不再傳話，並發一張「花費」卡給你（PRD LP-8、BL-3）。PRD 沒有定數字：XBot 先訂 200 萬，待確認。
	ChainBudgetTokens int64 = 2_000_000

	// PeerHistoryMax 是讀兩個 Bot 之間往來紀錄一次最多幾則（xbot.collab.peer_history，RM-12、C13）。XBot 新增。
	PeerHistoryMax = 200
)

// Bot 建立 Bot（PRD §18、LP-9）。
const (
	// BotCreatesPerDay 是一般 Bot 每 24 小時（滾動）最多建立幾個 Bot。
	// LazyBoy server/collab.py:32 BOTS_PER_DAY
	BotCreatesPerDay = 3

	// ManagerBotCreatesPerDay 是有團隊管理權限的 Bot 每 24 小時最多建立幾個（建團隊也算在內）。
	// LazyBoy server/collab.py:34 MANAGER_BOTS_PER_DAY（org.py:319 create_team 也用它）
	ManagerBotCreatesPerDay = 20

	// BotCreateWindow 是計算「每天」建立數的滾動時間窗。LazyBoy server/collab.py:564 create_bot（timedelta(days=1)）
	BotCreateWindow = 24 * time.Hour

	// BotsMax 是全部 Bot 的上限（Bot 建 Bot、建團隊時檢查）。LazyBoy server/collab.py:35 MAX_BOTS
	BotsMax = 50

	// BotCreateNameMax 是 Bot 建立的新 Bot 名稱長度（使用者自己建的上限是 BotNameMax）。
	// LazyBoy server/collab.py:568 create_bot（plain(name, 40)）
	BotCreateNameMax = 40

	// BotProfileTitleMax 是 Bot 建立或改自己職稱時的長度。LazyBoy server/collab.py:572、:583（plain(title, 60)）
	BotProfileTitleMax = 60
)

// 團隊與核定（PRD §18、TM-*）。
const (
	// TeamMax 是一個團隊（含主管）最多幾位。LazyBoy server/org.py:32 TEAM_MAX
	TeamMax = 10

	// TeamNameMax 是團隊名稱長度。LazyBoy server/org.py:305 create_team（plain(name, 40)）
	TeamNameMax = 40

	// ApprovalItemsMax、ApprovalItemMax 是「只有你能核准」清單最多幾項、每項幾字。
	// LazyBoy server/org.py:230 set_approval_items（plain(item, 40)…[:20]）
	ApprovalItemsMax = 20
	ApprovalItemMax  = 40

	// ApprovalNoteMax 是你核准或退回時附的備註長度。LazyBoy server/org.py:264 decide（note[:2000]）
	ApprovalNoteMax = 2000

	// DeleteReasonMax 是主管申請刪除 Bot 時的原因長度。LazyBoy server/org.py:414 request_delete_bot（plain(reason, 500)）
	DeleteReasonMax = 500

	// TeamRecentDays 是團隊狀態看最近幾天的工作。LazyBoy server/org.py:33 RECENT_DAYS
	TeamRecentDays = 7
)

// 提案與討論（PRD §18、TM-5；LazyBoy agenda.py）。
const (
	// AgendaOpenMax 是一個 Bot 同時在等你的提案＋討論合計件數。LazyBoy server/agenda.py:28 MAX_OPEN
	AgendaOpenMax = 3

	// ProposalDaysMin、ProposalDaysMax、ProposalDaysDefault 是提案「幾天後沒意見就生效」的範圍與預設。
	// LazyBoy server/agenda.py:29 MIN_DAYS, MAX_DAYS；:87 submit（days=3）
	ProposalDaysMin     = 1
	ProposalDaysMax     = 14
	ProposalDaysDefault = 3

	// VetoMemory 是被否決的提案多久內不能原樣再提。LazyBoy server/agenda.py:37 VETO_MEMORY
	VetoMemory = 14 * 24 * time.Hour

	// DiscussLeadMin 是討論時間最早要在幾分鐘之後。LazyBoy server/agenda.py:111、:149（timedelta(minutes=30)）
	DiscussLeadMin = 30 * time.Minute

	// DiscussAheadMax 是討論最多排到幾天後。LazyBoy server/agenda.py:30 DISCUSS_MAX_DAYS
	DiscussAheadMax = 30 * 24 * time.Hour

	// DiscussDefaultHour 是沒給時間時，討論排在隔天幾點（台北時間）。LazyBoy server/agenda.py:38 DISCUSS_HOUR
	DiscussDefaultHour = 18

	// ProposalGrace、ProposalGraceExtra：提案到期時已經過了 ProposalGrace 還沒人看到（服務關著），就再延 ProposalGraceExtra。
	// LazyBoy server/agenda.py:32 GRACE、:33 GRACE_EXTRA
	ProposalGrace      = 30 * time.Minute
	ProposalGraceExtra = 24 * time.Hour

	// AgendaDetailsMax、AgendaNoteMax 是提案說明與你回覆的備註長度。
	// LazyBoy server/agenda.py:116（details[:4000]）、:138（note[:2000]）
	AgendaDetailsMax = 4000
	AgendaNoteMax    = 2000

	// ReviewDaysDefault、ReviewDaysMax 是每週檢討看最近幾天。LazyBoy server/agenda.py:44 RECENT_DAYS、:205（min(30, …)）
	ReviewDaysDefault = 7
	ReviewDaysMax     = 30

	// CalendarDenseInterval 是間隔短於幾分鐘的定時任務在行事曆上一天只顯示一次。LazyBoy server/agenda.py:42 DENSE_INTERVAL
	CalendarDenseInterval = 180 * time.Minute

	// CalendarWeekItems 是行事曆一週最多列幾項。LazyBoy server/agenda.py:43 WEEK_ITEMS
	CalendarWeekItems = 120
)

// 執行中的工作：看門狗、重試、自動續做（PRD §12、§19；給 X04）。
const (
	// WatchdogIdle 是一輪工作完全沒有動靜多久就打斷（停止並回「Bot 長時間沒有回應…」）。
	// LazyBoy server/runtime.py:553 IDLE_LIMIT
	WatchdogIdle = 15 * time.Minute

	// WatchdogToolIdle 是有工具正在執行（長時間編譯、下載）時的沒動靜上限。LazyBoy server/runtime.py:554 TOOL_IDLE_LIMIT
	WatchdogToolIdle = 40 * time.Minute

	// StopGrace 是打斷或你按停止之後，等引擎自己收尾的時間；逾時就直接結束這一輪。LazyBoy server/runtime.py:555 STOP_GRACE
	StopGrace = 60 * time.Second

	// WatchInterval 是看門狗檢查的間隔（也是檢查提問是否過期的間隔）。LazyBoy server/runtime.py:556 WATCH_INTERVAL
	WatchInterval = 5 * time.Second

	// AutoContinueMax 是同一個請求最多自動續做幾次（只承諾沒做、計畫沒做完、把工作丟回來、背景子任務失敗）。
	// LazyBoy server/runtime.py:696 AUTO_CONTINUE
	AutoContinueMax = 2

	// StallNudgesMax 是同一個失敗的背景子任務最多被叫回去處理幾次。LazyBoy server/runtime.py:709 STALL_NUDGES
	StallNudgesMax = 2

	// StrandedWindow 是找「失敗後沒人接手的背景子任務」時只看最近多久的工作。LazyBoy server/runtime.py:715（timedelta(hours=3)）
	StrandedWindow = 3 * time.Hour

	// PromiseMaxChars 是「只說要做卻沒做」判斷的回覆長度上限（還要像承諾、整輪沒用工具）。
	// LazyBoy server/runtime.py:736 auto_continue（len(content) <= 70）
	PromiseMaxChars = 70

	// HandBackTailChars 是沒有找到「交回給你」的段落時，判斷要不要人時看回覆的最後幾個字。
	// LazyBoy server/runtime.py:747 auto_continue（text[-300:]）
	HandBackTailChars = 300

	// ProgressQuestionMax 是你問「進度如何」時，多短的訊息才當成問進度。LazyBoy server/runtime.py:631（len(text) <= 60）
	ProgressQuestionMax = 60

	// StreamDeltaInterval 是串流文字合併成一筆事件的間隔。LazyBoy server/runtime.py:942 DELTA_INTERVAL
	StreamDeltaInterval = 60 * time.Millisecond

	// TaskStepsKept、TaskPlanMax、TaskWorkersKept 是一輪工作保留的工具步驟、計畫項目、背景子任務筆數。
	// LazyBoy server/runtime.py:879（del steps[:-60]）、:850（[:30]）、:913（del workers[:-12]）
	TaskStepsKept   = 60
	TaskPlanMax     = 30
	TaskWorkersKept = 12

	// FailureDetailMax 是工作失敗時保留的錯誤訊息長度。LazyBoy server/runtime.py:545（str(exc)[:1500]）
	FailureDetailMax = 1500
)

// ProviderRetryDelays 是模型供應商暫時出錯（限流、429、5xx、逾時、連線）時自動重試的等待時間，依序第 1、2、3 次。
// Bot 交辦、群組、教學、背景工作不重試；你中間說過話就不重試。
// LazyBoy server/runtime.py:670 RETRY_DELAYS（120, 600, 1800 秒）
func ProviderRetryDelays() []time.Duration {
	return []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
}
