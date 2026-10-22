package limits

import (
	"slices"
	"testing"
	"time"
)

// 這些數字是 LazyBoy DEV 程式值，也寫在 docs/limits.md 和 PRD §12、§18。
// 改這裡的數字前，先確認 LazyBoy 的程式真的改了，並同步更新 docs/limits.md。
func TestCollabLimitsMatchLazyBoy(t *testing.T) {
	ints := []struct {
		name      string
		got, want int64
	}{
		{"BotMessageMax", BotMessageMax, 12000},
		{"ResultPreview", ResultPreview, 3000},
		{"CarryFilesMax", CarryFilesMax, 10},
		{"CarryBytesMax", CarryBytesMax, 50 * 1024 * 1024},
		{"RoomMembersMin", RoomMembersMin, 2},
		{"RoomMembersMax", RoomMembersMax, 10},
		{"RoomBacklog", RoomBacklog, 20},
		{"TeamMax", TeamMax, 10},
		{"ApprovalPendingMax", ApprovalPendingMax, 10},
		{"ApprovalItemsMax", ApprovalItemsMax, 20},
		{"ApprovalItemMax", ApprovalItemMax, 40},
		{"ProposalDaysMax", ProposalDaysMax, 14},
		{"StallNudgesMax", StallNudgesMax, 2},
		{"PromiseMaxChars", PromiseMaxChars, 70},
		{"ChatTextMax", ChatTextMax, 64000},
		{"MemoryCharsDefault", MemoryCharsDefault, 2200},
		{"MemoryUserCharsDefault", MemoryUserCharsDefault, 1375},
		{"MemoryEntryMax", MemoryEntryMax, 2000},
		{"SkillLibraryResults", SkillLibraryResults, 8},
		{"SkillLibraryText", SkillLibraryText, 60000},
		{"RoutinesPerBot", RoutinesPerBot, 50},
		{"RoutineJitterMaxMinutes", RoutineJitterMaxMinutes, 240},
		{"GoalRoundsDefault", GoalRoundsDefault, 10},
		{"GoalRoundsMax", GoalRoundsMax, 50},
		{"UploadFilesMax", UploadFilesMax, 10},
		{"UploadFileBytes", UploadFileBytes, 50 * 1024 * 1024},
		{"CardFilesMax", CardFilesMax, 10},
		{"BoxCPUs", BoxCPUs, 2},
		{"BoxMemoryBytes", BoxMemoryBytes, 3 * 1024 * 1024 * 1024},
		{"BoxShmBytes", BoxShmBytes, 512 * 1024 * 1024},
		{"CleanupBrowserCacheBytes", CleanupBrowserCacheBytes, 200 * 1024 * 1024},
		{"CleanupLiveCacheChecks", CleanupLiveCacheChecks, 10},
	}
	for _, c := range ints {
		if c.got != c.want {
			t.Errorf("%s = %d，LazyBoy 是 %d", c.name, c.got, c.want)
		}
	}

	durations := []struct {
		name      string
		got, want time.Duration
	}{
		{"BotCreateWindow", BotCreateWindow, 24 * time.Hour},
		{"VetoMemory", VetoMemory, 14 * 24 * time.Hour},
		{"WatchdogIdle", WatchdogIdle, 15 * time.Minute},
		{"WatchdogToolIdle", WatchdogToolIdle, 40 * time.Minute},
		{"StopGrace", StopGrace, 60 * time.Second},
		{"QuestionTTL", QuestionTTL, 10 * time.Minute},
		{"RoutineIntervalMin", RoutineIntervalMin, 5 * time.Minute},
		{"RoutineHookSpacing", RoutineHookSpacing, 60 * time.Second},
		{"BoxReadyTimeout", BoxReadyTimeout, 75 * time.Second},
		{"CleanupScratchAge", CleanupScratchAge, 3 * 24 * time.Hour},
		{"CleanupUploadAge", CleanupUploadAge, 30 * 24 * time.Hour},
		{"CleanupFirstDelay", CleanupFirstDelay, 120 * time.Second},
		{"CleanupEvery", CleanupEvery, 24 * time.Hour},
		{"CleanupLiveCacheTimeout", CleanupLiveCacheTimeout, 30 * time.Second},
		{"CleanupLiveCachePoll", CleanupLiveCachePoll, time.Second},
	}
	for _, c := range durations {
		if c.got != c.want {
			t.Errorf("%s = %s，LazyBoy 是 %s", c.name, c.got, c.want)
		}
	}

	if got, want := ProviderRetryDelays(), []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}; !slices.Equal(got, want) {
		t.Errorf("ProviderRetryDelays = %v，LazyBoy 是 %v", got, want)
	}
	if got, want := RoutineStartRetryDelays(), []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}; !slices.Equal(got, want) {
		t.Errorf("RoutineStartRetryDelays = %v，LazyBoy 是 %v", got, want)
	}
	if got, want := RoutineJitterChoices(), []int{15, 30, 60, 90, 120}; !slices.Equal(got, want) {
		t.Errorf("RoutineJitterChoices = %v，LazyBoy 是 %v", got, want)
	}
}

// 互相有關的數字：預覽要比上限短、畫面選項不能超過後端上限、看門狗工具時限比一般長。
func TestLimitsConsistent(t *testing.T) {
	if ResultPreview >= BotMessageMax {
		t.Errorf("ResultPreview %d 應小於 BotMessageMax %d", ResultPreview, BotMessageMax)
	}
	for _, m := range RoutineJitterChoices() {
		if m <= 0 || m > RoutineJitterMaxMinutes {
			t.Errorf("隨機延後選項 %d 超出 1～%d", m, RoutineJitterMaxMinutes)
		}
	}
	if WatchdogToolIdle <= WatchdogIdle {
		t.Error("WatchdogToolIdle 應比 WatchdogIdle 長")
	}
	if RoomMembersMax < TeamMax {
		t.Error("整個團隊（TeamMax）要能放進一個群組（RoomMembersMax）")
	}
	if BotCreatesPerDay() > ManagerBotCreatesPerDay() || ManagerBotCreatesPerDay() > BotsMax() {
		t.Error("建立 Bot 上限應為 一般 ≤ 管理者 ≤ 總數")
	}
	if len(ProviderRetryDelays()) != 3 {
		t.Error("暫時性錯誤應重試 3 次")
	}
	// Hermes 省 token（2026-10-19，和 LazyBoy 刻意不同，見 docs/limits.md §10）：摘要門檻要低於 Hermes 自己的比例門檻
	// （視窗小於 512K 時是 75%），摘要才真的會發生；修剪比摘要早；子代理的迴圈比主代理少。
	if HermesCompressTokens >= HermesContextLength*3/4 || HermesPruneTokens >= HermesCompressTokens {
		t.Errorf("摘要門檻 %d 要介於修剪 %d 和視窗 %d 的 75%% 之間", HermesCompressTokens, HermesPruneTokens, HermesContextLength)
	}
	if HermesReviewInputTokens >= HermesContextLength || HermesDelegationMaxIterations >= HermesMaxIterations || HermesSkillNudgeInterval <= 10 {
		t.Error("背景檢討的預算要小於視窗、子代理迴圈要少於主代理、技能檢討要比 Hermes 預設（10）少跑")
	}
}

// 可以調的限制：預設比 LazyBoy 寬（使用者決定 2026-10-19），而且都在允許範圍內。
func TestDefaultTunables(t *testing.T) {
	d := DefaultTunables
	if d.MaxHops != 6 || d.RoomRounds != 10 || d.BotCreatesPerDay != 10 || d.ManagerBotCreatesPerDay != 50 || d.BotsMax != 200 || d.AutoContinueMax != 5 || d.AgendaOpenMax != 10 {
		t.Fatalf("%+v", d)
	}
	vals := map[string]int{"max_hops": d.MaxHops, "room_rounds": d.RoomRounds, "bot_creates_per_day": d.BotCreatesPerDay,
		"manager_bot_creates_per_day": d.ManagerBotCreatesPerDay, "bots_max": d.BotsMax, "auto_continue_max": d.AutoContinueMax, "agenda_open_max": d.AgendaOpenMax}
	if len(vals) != len(TunableRange) {
		t.Fatalf("每個限制都要有範圍：%v", TunableRange)
	}
	for k, v := range vals {
		r := TunableRange[k]
		if v < r[0] || v > r[1] {
			t.Errorf("%s=%d 不在 %v", k, v, r)
		}
	}
	if CurrentTunables() != d || MaxHops() != 6 {
		t.Fatalf("一開始就是預設值")
	}
}
