package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Limiter 是代理的上限（MCP-12）：每分鐘呼叫（記憶體裡的滑動視窗）、每日呼叫（數稽核紀錄）、
// 每日花費（它交辦的工作今天花了多少）。HTTP、MCP、指令列都經過登錄表，所以一起受限。
// 只限制外部代理；你本人和 Bot（另有 Bot 自己的花費上限）不受影響。
type Limiter struct {
	DB  *store.DB
	Now func() time.Time // 測試用；空白用 time.Now

	mu   sync.Mutex
	hits map[string][]time.Time // 代理 id → 最近一分鐘內的呼叫時間
}

// NewLimiter 建立代理上限檢查。
func NewLimiter(db *store.DB) *Limiter { return &Limiter{DB: db} }

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Check 是 ops.Limiter：超過上限回 limit 錯誤（HTTP 429）。
func (l *Limiter) Check(ctx context.Context, id ops.Identity, info *ops.Info) *ops.Error {
	if id.Kind != ops.KindAgent {
		return nil
	}
	t, err := l.DB.GetAgentToken(ctx, id.ID)
	if errors.Is(err, store.ErrNotFound) {
		return ops.Forbidden("找不到這個代理的權杖，請重新配對")
	}
	if err != nil {
		return ops.Internal(err)
	}
	now := l.now()
	rate := t.RatePerMin
	if rate <= 0 {
		rate = limits.AgentRatePerMin
	}
	bucket, where := id.ID, ""
	if id.Via == ViaExternal { // 外部入口（Funnel／反向代理）：另一個計數、上限比較嚴（MCP-2）
		bucket, where = "ext:"+id.ID, "經外部入口"
		rate = min(rate, limits.ExternalAgentRatePerMin)
	}
	if !l.allow(bucket, rate, now) {
		e := ops.Limit(fmt.Sprintf("%s %s呼叫太頻繁：每分鐘最多 %d 次，請等一下再試", id.DisplayName(), where, rate))
		e.Retryable = true
		return e
	}
	day := store.DayStart(now)
	if t.DailyCalls > 0 {
		n, err := l.DB.CountAuditSince(ctx, id.Actor(), day)
		if err != nil {
			return ops.Internal(err)
		}
		if n >= t.DailyCalls {
			return ops.Limit(fmt.Sprintf("%s 今天的呼叫已達上限 %d 次；明天再試，或請使用者在「設定 → 已連線的代理」調高", id.DisplayName(), t.DailyCalls))
		}
	}
	// 每日花費只擋會做事的操作；查看類照常可以用（才看得到為什麼被擋）。
	if t.DailySpendMicros > 0 && info.Scope != ops.ScopeRead {
		spent, err := l.DB.AgentSpendSince(ctx, id.Actor(), day)
		if err != nil {
			return ops.Internal(err)
		}
		if spent >= t.DailySpendMicros {
			return ops.Limit(fmt.Sprintf("%s 今天交辦的工作已經花了 US$%.2f，達到每日花費上限 US$%.2f；明天再試，或請使用者調高",
				id.DisplayName(), float64(spent)/1e6, float64(t.DailySpendMicros)/1e6))
		}
	}
	return nil
}

// allow 記下一次呼叫；最近一分鐘內已經有 rate 次就拒絕（被拒的不算）。
func (l *Limiter) allow(agentID string, rate int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-limits.RateWindow)
	for k, ts := range l.hits { // 順便清掉太久沒呼叫的代理
		if len(ts) > 0 && !ts[len(ts)-1].After(cut) {
			delete(l.hits, k)
		}
	}
	ts := l.hits[agentID]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= rate {
		l.hits[agentID] = ts
		return false
	}
	l.hits[agentID] = append(ts, now)
	return true
}
