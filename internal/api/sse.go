package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
)

// events：GET /api/v1/events，前端唯一的一條 SSE（TECH-DESIGN §6）。
//
// 每筆事件是 `id: <事件 id>` 加上 `data: <事件 JSON>`（events.Event：id、kind、bot_id、thread_id、task_id、payload、at）；
// 不存資料庫的即時事件沒有 id 行。斷線重連時瀏覽器會自動帶 Last-Event-ID，也可以用 ?last_event_id= 指定，
// 服務會先補送那之後的事件。都沒有時從現在開始。沒有事件時每 15 秒送一行心跳註解。
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if !id.HasScope(ops.ScopeRead) {
		writeError(w, 0, ops.Forbidden(id.DisplayName()+" 沒有「查看」權限，不能訂閱事件"))
		return
	}
	lastID := int64(-1)
	for _, v := range []string{r.Header.Get("Last-Event-ID"), r.URL.Query().Get("last_event_id")} {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, 0, ops.InvalidInput(ops.FieldError{Field: "last_event_id", Message: "要是事件 id（非負整數）"}))
			return
		}
		lastID = n
		break
	}

	// 先訂閱再回應標頭：用戶端看到連線成功之後發生的事件一定收得到。
	ctx := r.Context()
	ch := events.Stream(ctx, s.bus, s.db, events.ForIdentity(id), lastID, nil)

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // 長連線：不要被伺服器的寫入逾時切斷
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // 叫 nginx 之類的反向代理不要緩衝
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "retry: %d\n: connected\n\n", limits.SSERetry.Milliseconds()); err != nil {
		return
	}
	if rc.Flush() != nil {
		return
	}

	beat := time.NewTicker(s.heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := writeEvent(w, e); err != nil {
				return
			}
			// 一口氣把已經在排隊的事件寫完再 flush（續傳時可能有很多筆）。
			for drained := false; !drained; {
				select {
				case e, ok := <-ch:
					if !ok {
						_ = rc.Flush()
						return
					}
					if err := writeEvent(w, e); err != nil {
						return
					}
				default:
					drained = true
				}
			}
			if rc.Flush() != nil {
				return
			}
			beat.Reset(s.heartbeat)
		}
	}
}

func writeEvent(w http.ResponseWriter, e events.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var sb strings.Builder
	if e.ID > 0 {
		sb.WriteString("id: ")
		sb.WriteString(strconv.FormatInt(e.ID, 10))
		sb.WriteByte('\n')
	}
	sb.WriteString("data: ")
	sb.Write(b) // json.Marshal 不會產生換行
	sb.WriteString("\n\n")
	_, err = w.Write([]byte(sb.String()))
	return err
}
