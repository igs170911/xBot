package runtime

import (
	"context"

	"xbot/internal/store"
)

// HasWork 回答 Bot 有沒有進行中或排隊中的工作（閒置休眠用，B13 BX-12：有工作的電腦不休眠）。
func (r *Runtime) HasWork(botID string) bool {
	r.mu.Lock()
	busy := r.active[botID] != nil
	r.mu.Unlock()
	if busy {
		return true
	}
	tasks, err := r.o.DB.ListTasks(context.Background(), botID, true)
	if err != nil {
		return true // 讀不到時當作有工作：寧可晚點休眠
	}
	for _, t := range tasks {
		if t.State != store.TaskDone {
			return true
		}
	}
	return false
}

// Quiesce 在電腦休眠、重開或重建前呼叫：Bot 沒有工作時關掉它的引擎連線並回傳 true；有工作時什麼都不做、回傳 false。
// 和收下新工作互斥（dispatchMu），所以之後才來的工作一定會重新接上引擎（需要時重新開機，會等電腦的鎖）。
func (r *Runtime) Quiesce(botID string) bool {
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	if r.HasWork(botID) {
		return false
	}
	if c, ok := r.o.Driver.(interface{ Close(botID string) }); ok {
		c.Close(botID)
	}
	return true
}
