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

	// JudgeReplayDays、JudgeReplayTasks：重播快判（xbot.judge.replay）預設看最近幾天、最多幾件工作；JudgeReplayCases 是最多列出幾件。XBot 新增。
	JudgeReplayDays  = 7
	JudgeReplayTasks = 1000
	JudgeReplayCases = 50

	// JudgeAskMax 是「需要你」卡片上引用 Bot 那段話最多幾個字。XBot 新增（同完成通知的 120 字）。
	JudgeAskMax = 300

	// StrandedGoalKey 是比對「同一個背景子任務」時看目標的前幾個字。LazyBoy server/runtime.py:722（goal[:80]）
	StrandedGoalKey = 80
)

// 快判模型（X05，PRD §20、FASTJUDGE.md）。等待上限照 PRD §20.2 表；11–15 是 XBot 建議值（P06）。
const (
	// JudgeShadowWait 是影子模式在背景等模型的上限：不擋任何人，等久一點才記得到慢一點的模型怎麼判。XBot 自訂。
	JudgeShadowWait = 10 * time.Second
	// JudgeWaitRoute 是用途 1 對話分流的等待上限。PRD §20.2：0.3 秒。
	JudgeWaitRoute = 300 * time.Millisecond
	// JudgeWaitApproval 是用途 3 要不要找你核定。PRD §20.2：0.5 秒。
	JudgeWaitApproval = 500 * time.Millisecond
	// JudgeWaitProgress 是用途 4 是不是在問進度。PRD §20.2：0.3 秒。
	JudgeWaitProgress = 300 * time.Millisecond
	// JudgeWaitError 是用途 5 錯誤分類。PRD §20.2：0.5 秒。
	JudgeWaitError = 500 * time.Millisecond
	// JudgeWaitSkillDup 是用途 6 重複技能（背景）。PRD §20.2：1 秒。
	JudgeWaitSkillDup = 1 * time.Second
	// JudgeWaitResponder 是用途 7 交給哪個 Bot。PRD §20.2：1.5 秒（OpenMausBot ROOM_ROUTING_TIMEOUT_MS）。
	JudgeWaitResponder = 1500 * time.Millisecond
	// JudgeWaitSummary 是用途 8 一行摘要（背景）。PRD §20.2：2 秒。
	JudgeWaitSummary = 2 * time.Second
	// JudgeWaitBotSuggest 是用途 9 一句話建立 Bot。PRD §20.2：2 秒。
	JudgeWaitBotSuggest = 2 * time.Second
	// JudgeWaitTitle 是用途 10 命名與寒暄。PRD §20.2：1 秒。
	JudgeWaitTitle = 1 * time.Second
	// JudgeWaitPreCheck 是用途 11–14「先判斷」每一項。XBot 建議值（C15、P06：0.5 秒）。
	JudgeWaitPreCheck = 500 * time.Millisecond
	// JudgeWaitPeerReply 是用途 15 隊友訊息要不要回。XBot 建議值（P06：1 秒）。
	JudgeWaitPeerReply = 1 * time.Second
	// JudgeWaitRoutine 是一句話建定時任務（C32 解析器的快判）。XBot 新增，同用途 9：2 秒。
	JudgeWaitRoutine = 2 * time.Second
	// JudgeWaitTeach 是示範整理成技能（B32）的快判等待上限。XBot 新增，同用途 9：2 秒。
	JudgeWaitTeach = 2 * time.Second

	// JudgeMinP 是第一層（Jev）選擇題採用的機率門檻；低於它照規則。OpenMausBot ROOM_ROUTING_MIN_PROBABILITY = 0.6。
	JudgeMinP = 0.6
	// JudgeDailyCalls 是快判每天最多呼叫幾次模型（預設）；到了全部退回規則並通知一次。PRD FJ-6：300。
	JudgeDailyCalls = 300
	// JudgeDailyCallsMax 是每日上限最多能設多少。XBot 新增。
	JudgeDailyCallsMax = 100000
	// JudgeTier2DailyCalls 是第二層（OpenCode Go 短文字）每天最多幾次（預設）。J10、P05：100。
	JudgeTier2DailyCalls = 100
	// JudgeLiveAgreement 是影子模式與人工改正（沒有改正時與規則）一致率到多少才建議生效。PRD FJ-5、J10：95%。
	JudgeLiveAgreement = 0.95
	// JudgeLiveMinAnswers 是影子模式至少要有幾次合格的回答，才會建議生效。XBot 新增。
	JudgeLiveMinAnswers = 20
	// JudgeInputMax 是送給快判的輸入最多幾個字（FJ-1：輸入控制在幾百 token 內）。PRD §20.2 最長的是用途 8：1,500 字。
	JudgeInputMax = 1500
	// JudgeRouteInputMax、JudgeTaskDoneInputMax、JudgeApprovalInputMax、JudgeErrorInputMax 是各用途的輸入上限。PRD §20.2。
	JudgeRouteInputMax    = 500
	JudgeTaskDoneInputMax = 800
	JudgeApprovalInputMax = 300
	JudgeErrorInputMax    = 400
	// JudgeSummaryMax 是一行摘要最多幾個字。PRD §20.2 用途 8：30 字。
	JudgeSummaryMax = 30
	// JudgeTitleMax 是對話串標題最多幾個字。PRD §20.2 用途 10：16 字。
	JudgeTitleMax = 16
	// JudgeTitleRuleChars 是規則命名取前幾個字。PRD §20.2 用途 10：前 20 字。
	JudgeTitleRuleChars = 20
	// JudgeWhyMax 是快判「一句話原因」最多幾個字。XBot 新增（FJ-1）。
	JudgeWhyMax = 80
	// JudgeChoiceMaxTokens、JudgeTextMaxTokens 是一次快判最多回幾個 token（輸出固定 JSON）。XBot 新增。
	// 第二層的 flash 模型可能先用掉推理 token（opencode_test：deepseek-v4-flash 會吃光 max_tokens），所以給多一點。
	JudgeChoiceMaxTokens = 120
	JudgeTextMaxTokens   = 600
	// JudgeReplyBytes 是讀模型回應最多幾個位元組。XBot 新增。
	JudgeReplyBytes = 64 << 10
	// JudgeCostSmallTokens 是用途 14「小」的上限（2 萬 token）。PRD §20 用途 14。
	JudgeCostSmallTokens = 20000
	// JudgeStatsDays 是快判統計看最近幾天。XBot 新增。
	JudgeStatsDays = 7
	// JudgeRunsKeepDays 是快判紀錄（judge_run）保留幾天。XBot 新增。
	JudgeRunsKeepDays = 30
)

// 從失敗學到的事（internal/lessons，harness2 的做法：老師模型看沒做完的那次，寫一條守則，你同意才加）。XBot 新增。
const (
	// LessonsMax 是一個 Bot（或「所有 Bot」）最多留幾條學到的事；多了要先刪舊的（守則太長，每次對話都要多花 token）。
	LessonsMax = 12
	// LessonTextMax 是一條學到的事最多幾個字。
	LessonTextMax = 80
	// LessonsPerDay 是一個 Bot 一天最多提議幾次（避免一直跳卡）。
	LessonsPerDay = 3
	// LessonSteps 是交給老師看的步驟最多幾個。
	LessonSteps = 40
	// LessonTeacherTimeout 是請老師看一次最多等多久。
	LessonTeacherTimeout = 120 * time.Second
	// SkillFailMin 是一個技能最近沒做完幾次以上、而且一半以上沒做完，老師就改提議修技能。
	SkillFailMin = 2
	// SkillStatsDays 是技能成效統計看最近幾天。
	SkillStatsDays = 30
)
