package box

import (
	"context"
	"errors"
	"fmt"
	"time"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// 閒置休眠（BX-12、CL-9）：電腦開著、Bot 沒有工作（沒有進行中或排隊中的任務）、也沒有人在看畫面，
// 超過 bot.idle_minutes（預設 limits.BoxIdleMinutes＝30 分鐘）就 docker stop（瀏覽器先存狀態，見 box/start-desktop.sh）。
// 本機與遠端主機都一樣。之後有工作時照常開機（runtime → 引擎驅動 → Computers.Connect／LaunchACP → Start），
// 這就是「有工作自動叫醒」；排隊中的工作輪到時也走同一條路。
//
// 「最後使用時間」是 box.last_active_at：開機、Touch（看畫面時定期呼叫，B14）、以及每次檢查時 Bot 還有工作，都會更新。

// ErrBusy 表示 Bot 正在工作，現在不能休眠、重開或重建（先停止工作）。
var ErrBusy = errors.New("這個 Bot 正在工作，先停止它的工作再試")

// Activity 回答 Bot 的工作狀態；runtime 提供（*runtime.Runtime 的 HasWork、Quiesce）。
type Activity interface {
	// HasWork：有進行中或排隊中的工作。
	HasWork(botID string) bool
	// Quiesce：沒有工作時關掉 Bot 的引擎連線並回傳 true（之後的工作會重新接上、需要時重新開機）；有工作時回傳 false。
	Quiesce(botID string) bool
}

// Hold 表示有人正在看這台電腦的畫面（noVNC，B14）：看的期間不休眠。回傳的函式在離開時呼叫。
func (m *Manager) Hold(botID string) (release func()) {
	m.mu.Lock()
	m.holds[botID]++
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if m.holds[botID]--; m.holds[botID] <= 0 {
			delete(m.holds, botID)
		}
		m.mu.Unlock()
		_ = m.Touch(context.Background(), botID) // 從現在開始重新計算閒置時間
	}
}

func (m *Manager) held(botID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holds[botID] > 0
}

// IdleFor 回傳 Bot 的閒置休眠時間（bot.idle_minutes；沒設時用預設值）。
func IdleFor(b store.Bot) time.Duration {
	n := b.IdleMinutes
	if n <= 0 {
		n = limits.BoxIdleMinutes
	}
	return time.Duration(n) * time.Minute
}

// SleepIdle 檢查一次所有開著的電腦，讓閒置超過時間的休眠。回傳這次休眠的 Bot。
func (m *Manager) SleepIdle(ctx context.Context, act Activity, now time.Time) ([]string, error) {
	rows, err := m.db.ListBoxes(ctx, "")
	if err != nil {
		return nil, err
	}
	var slept []string
	var errs []error
	for _, r := range rows {
		if r.State != StateRunning {
			continue
		}
		if m.held(r.BotID) || (act != nil && act.HasWork(r.BotID)) {
			_ = m.Touch(ctx, r.BotID)
			continue
		}
		ok, err := m.sleepIfIdle(ctx, r.BotID, act, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", r.BotID, err))
			continue
		}
		if ok {
			slept = append(slept, r.BotID)
		}
	}
	return slept, errors.Join(errs...)
}

// sleepIfIdle 在 Bot 的鎖裡再確認一次（期間可能有人 Touch 或交辦工作），真的閒置才停。
func (m *Manager) sleepIfIdle(ctx context.Context, botID string, act Activity, now time.Time) (bool, error) {
	defer m.lock(botID)()
	b, h, err := m.load(ctx, botID)
	if err != nil {
		return false, err
	}
	bot, err := m.db.GetBot(ctx, botID)
	if err != nil {
		return false, err
	}
	last := b.LastActiveAt
	if last == 0 {
		last = b.UpdatedAt
	}
	if b.State != StateRunning || now.Sub(time.UnixMilli(last)) < IdleFor(bot) || m.held(botID) {
		return false, nil
	}
	// 關掉引擎連線要和「收下新工作」互斥（runtime 的 dispatch 鎖）：之後才來的工作會重新接上，並因為要開機而等這把鎖。
	if act != nil && !act.Quiesce(botID) {
		return false, nil
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return false, err
	}
	if running(c) {
		if err := stop(ctx, h, c.ID); err != nil {
			return false, m.fail(ctx, b, err)
		}
		if c, err = inspect(ctx, h, botID); err != nil {
			return false, err
		}
	}
	m.log.Info("電腦閒置休眠", "bot", botID, "idle_minutes", int(IdleFor(bot)/time.Minute))
	_, err = m.sync(ctx, b, h, c)
	return err == nil, err
}

// RunIdle 每 limits.BoxIdleCheckInterval 檢查一次閒置的電腦，直到 ctx 結束。
func (m *Manager) RunIdle(ctx context.Context, act Activity) {
	t := time.NewTicker(limits.BoxIdleCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if slept, err := m.SleepIdle(ctx, act, now); err != nil && ctx.Err() == nil {
				m.log.Warn("閒置休眠檢查失敗", "err", err, "slept", slept)
			}
		}
	}
}

// Sleep 讓電腦休眠（xbot.computers.sleep）：Bot 有工作時回 ErrBusy。
func (m *Manager) Sleep(ctx context.Context, botID string, act Activity) (Status, error) {
	if act != nil && !act.Quiesce(botID) {
		return Status{}, ErrBusy
	}
	return m.Stop(ctx, botID)
}

// Restart 重開電腦（xbot.computers.restart）：Bot 有工作時回 ErrBusy。重開後不等桌面就緒。
func (m *Manager) Restart(ctx context.Context, botID string, act Activity) (Status, error) {
	if act != nil && !act.Quiesce(botID) {
		return Status{}, ErrBusy
	}
	if _, err := m.Stop(ctx, botID); err != nil {
		return Status{}, err
	}
	return m.Start(ctx, botID)
}
