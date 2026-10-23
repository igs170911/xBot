package collab

import (
	"context"
	"time"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// 總控「全部停下」是緊急停止按鈕（使用者 2026-10-19：「互相回話之間要有總控可以停止」；2026-10-23：「全部停止就直接停止，
// 不需要用恢復，這像是一個緊急停止按鈕」）：馬上停掉所有 Bot 正在做與排隊的工作，停完就照常，不用按繼續、也不記在任何地方。
// 停的那段時間（加上 limits.StopAllSettle）Bot 不能交辦隊友，隊友交回的結果、群組接力都不叫醒任何 Bot——
// 不然剛好做完的那一輪又會叫醒另一個。你自己對 Bot 說的話不受影響。

// StopAll 停掉所有 Bot 正在做與排隊的工作，回傳停了幾件。
func (s *Service) StopAll(ctx context.Context) (int, error) {
	s.stopping.Add(1)
	defer s.stopping.Add(-1)
	stopped := map[string]bool{}
	sweep := func() (int, error) {
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
				if t.State == store.TaskDone || stopped[t.ID] {
					continue
				}
				if ok, err := s.rt.StopTask(ctx, t.ID); err == nil && ok {
					stopped[t.ID] = true
					n++
				}
			}
		}
		return n, nil
	}
	// 停的時候剛好做完的一輪會晚一點交回結果、排出新的工作：等一下再掃，直到沒有新的（最多 StopAllRounds 次）。
	for range limits.StopAllRounds {
		n, err := sweep()
		if err != nil {
			return len(stopped), err
		}
		if n == 0 {
			break // 這次沒有新的（一開始就沒有在做的工作時，馬上回來）
		}
		time.Sleep(s.settle)
	}
	return len(stopped), nil
}

// halted 回答是不是正在全部停下：這段時間不叫醒任何 Bot。
func (s *Service) halted() bool { return s.stopping.Load() > 0 }

// errHalted 是正在全部停下時 Bot 想交辦隊友收到的錯誤。
func errHalted() error {
	return &Error{Code: CodeLimit, Msg: "使用者剛按了「全部停下」，所有工作都停了。先停下來，等使用者再交代。"}
}
