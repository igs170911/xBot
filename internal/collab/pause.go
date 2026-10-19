package collab

import (
	"context"

	"xbot/internal/store"
)

// 總控「全部停下」（使用者決定 2026-10-19：「互相回話之間要有總控可以停止」）：
// 停掉所有 Bot 正在做與排隊的工作，並暫停 Bot 之間的往來——暫停時不能交辦隊友，
// 隊友交回的結果、群組接力、停下通知都不再叫醒任何 Bot（否則停下一個又會叫醒另一個）。
// 你自己對 Bot 說的話不受影響。按「繼續」恢復。存在 setting（collab.paused），重開服務也還在。

// PausedKey 是暫停狀態在 setting 表的 key。
const PausedKey = "collab.paused"

// AllPaused 回答 Bot 之間的往來是不是暫停中。
func (s *Service) AllPaused(ctx context.Context) bool {
	var p bool
	if _, err := s.db.GetSettingInto(ctx, PausedKey, &p); err != nil {
		return false
	}
	return p
}

// PauseAll 暫停或恢復 Bot 之間的往來；暫停時順便停掉所有 Bot 正在做與排隊的工作，回傳停了幾件。
func (s *Service) PauseAll(ctx context.Context, paused bool) (int, error) {
	if err := s.db.SetSetting(ctx, PausedKey, paused); err != nil {
		return 0, err
	}
	if !paused {
		return 0, nil
	}
	bots, err := s.db.ListBots(ctx, true)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bots {
		tasks, err := s.db.ListTasks(ctx, b.ID, true)
		if err != nil {
			return n, err
		}
		for _, t := range tasks {
			if t.State == store.TaskDone {
				continue
			}
			if ok, err := s.rt.StopTask(ctx, t.ID); err == nil && ok {
				n++
			}
		}
	}
	return n, nil
}

// errPaused 是暫停中 Bot 想交辦隊友時收到的錯誤。
func errPaused() error {
	return &Error{Code: CodeLimit, Msg: "使用者按了「全部停下」，Bot 之間的往來暫停中。先停下來，把目前結果回報給使用者，等使用者說繼續。"}
}
