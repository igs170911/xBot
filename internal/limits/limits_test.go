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
		{"MaxHops", MaxHops, 6},
		{"BotMessageMax", BotMessageMax, 12000},
		{"ResultPreview", ResultPreview, 3000},
		{"CarryFilesMax", CarryFilesMax, 10},
		{"CarryBytesMax", CarryBytesMax, 50 * 1024 * 1024},
		{"RoomMembersMin", RoomMembersMin, 2},
		{"RoomMembersMax", RoomMembersMax, 10},
		{"RoomRounds", RoomRounds, 4},
		{"RoomBacklog", RoomBacklog, 20},
		{"BotCreatesPerDay", BotCreatesPerDay, 3},
		{"ManagerBotCreatesPerDay", ManagerBotCreatesPerDay, 20},
		{"BotsMax", BotsMax, 50},
		{"TeamMax", TeamMax, 10},
		{"ApprovalPendingMax", ApprovalPendingMax, 10},
		{"ApprovalItemsMax", ApprovalItemsMax, 20},
		{"ApprovalItemMax", ApprovalItemMax, 40},
		{"AgendaOpenMax", AgendaOpenMax, 3},
		{"ProposalDaysMax", ProposalDaysMax, 14},
		{"AutoContinueMax", AutoContinueMax, 2},
		{"StallNudgesMax", StallNudgesMax, 2},
		{"PromiseMaxChars", PromiseMaxChars, 70},
		{"ChatTextMax", ChatTextMax, 64000},
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
	if BotCreatesPerDay > ManagerBotCreatesPerDay || ManagerBotCreatesPerDay > BotsMax {
		t.Error("建立 Bot 上限應為 一般 ≤ 管理者 ≤ 總數")
	}
	if len(ProviderRetryDelays()) != 3 {
		t.Error("暫時性錯誤應重試 3 次")
	}
}
