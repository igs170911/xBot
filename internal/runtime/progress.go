package runtime

// 目前進度（PRD §19.2，LazyBoy org.progress／progress_text）：系統查得的事實，不是 Bot 自己說的。
// 畫面（目前進度）、MCP（xbot.tasks.progress）和你問「進度如何」時附給 Bot 的是同一份。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"xbot/internal/store"
)

// Progress 是一個 Bot 現在的狀況。
type Progress struct {
	BotID string `json:"bot_id"`
	State string `json:"state" desc:"idle 沒在做事、queued 只有排隊中的、working 工作中、waiting 等你回覆"`
	// Busy：自己還在做，或還在等隊友的結果。false 而交辦的事還沒完成＝卡住了。
	Busy       bool           `json:"busy" desc:"有沒有任何人在為它的工作忙（自己或還在等的隊友）；false 而事情沒完成代表卡住了"`
	Current    *ProgressTask  `json:"current,omitempty" desc:"正在做的那件工作"`
	Queued     int            `json:"queued" desc:"排隊中的工作數"`
	WaitingFor *ProgressAsk   `json:"waiting_for,omitempty" desc:"卡在等你回覆的問題"`
	WaitingOn  []ProgressWait `json:"waiting_on" desc:"傳給隊友、還沒回來的請求"`
	LastError  *ProgressError `json:"last_error,omitempty" desc:"正在做（或最近結束）的那件工作最後一次出錯"`
	Last       *ProgressLast  `json:"last,omitempty" desc:"最近一次結束的工作（不含你停掉的）"`
	// Text 是同一份事實寫成給 Bot 看的幾行字（你問進度時附在訊息後面）。
	Text string `json:"text"`
	Now  int64  `json:"now"`
}

// ProgressTask 是正在做的工作。
type ProgressTask struct {
	TaskID    string   `json:"task_id"`
	ThreadID  string   `json:"thread_id"`
	Title     string   `json:"title" desc:"交辦內容的第一行"`
	From      string   `json:"from" desc:"誰交辦的：你交辦、○○ 交辦、群組對話、定時任務、Webhook"`
	State     string   `json:"state" desc:"running、waiting_input、waiting_approval、waiting_box、stopping"`
	StartedAt int64    `json:"started_at"`
	ElapsedS  float64  `json:"elapsed_s" desc:"開始到現在幾秒"`
	Steps     int      `json:"steps" desc:"做了幾步（呼叫工具的次數）"`
	Step      string   `json:"step,omitempty" desc:"正在執行的工具"`
	Plan      []string `json:"plan,omitempty" desc:"計畫裡還沒做完的項目"`
}

// ProgressAsk 是卡在等你的問題。
type ProgressAsk struct {
	InboxID string `json:"inbox_id"`
	Kind    string `json:"kind"`
	Prompt  string `json:"prompt"`
	Since   int64  `json:"since"`
}

// ProgressWait 是還在等的隊友。
type ProgressWait struct {
	BotID string `json:"bot_id"`
	Bot   string `json:"bot" desc:"隊友的名字"`
	Task  string `json:"task" desc:"請它做的事（開頭）"`
	Since int64  `json:"since"`
	For   string `json:"for,omitempty" desc:"做完要回報給誰（轉派的工作）"`
}

// ProgressError 是最後一次出錯。
type ProgressError struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
	At     int64  `json:"at"`
}

// ProgressLast 是最近一次結束的工作。
type ProgressLast struct {
	TaskID  string    `json:"task_id"`
	Verdict string    `json:"verdict" desc:"answer、failed、interrupted"`
	Judge   *Judgment `json:"judge,omitempty" desc:"做完了沒的判斷"`
	At      int64     `json:"at"`
	Result  string    `json:"result,omitempty" desc:"回覆的開頭"`
}

func sourceLabel(t store.Task) string {
	switch t.Source {
	case SourceBot:
		var m struct {
			FromName string `json:"from_name"`
		}
		_ = json.Unmarshal(t.Meta, &m)
		return firstNonEmpty(m.FromName, "隊友") + " 交辦"
	case SourceRoom:
		return "群組對話"
	case SourceRoutine:
		return "定時任務"
	case SourceWebhook:
		return "Webhook"
	}
	return "你交辦"
}

// Progress 查一個 Bot 現在的狀況。skipTask 是不算進去的工作（你問進度的那一則本身）。
func (r *Runtime) Progress(ctx context.Context, botID, skipTask string) (Progress, error) {
	now := time.Now()
	out := Progress{BotID: botID, State: "idle", WaitingOn: []ProgressWait{}, Now: now.UnixMilli()}
	open, err := r.o.DB.ListTasks(ctx, botID, true)
	if err != nil {
		return out, err
	}
	var cur *store.Task
	for i := range open {
		t := &open[i]
		if t.ID == skipTask {
			continue
		}
		if t.State == store.TaskQueued {
			out.Queued++
		} else if cur == nil {
			cur = t
		}
	}
	switch {
	case cur != nil && (cur.State == store.TaskWaitingInput || cur.State == store.TaskWaitingApproval):
		out.State = "waiting"
	case cur != nil:
		out.State = "working"
	case out.Queued > 0:
		out.State = "queued"
	}
	if cur != nil {
		p := &ProgressTask{TaskID: cur.ID, ThreadID: cur.ThreadID, Title: clip(firstLine(cur.Input), 60), From: sourceLabel(*cur), State: cur.State,
			StartedAt: cur.StartedAt}
		since := cur.StartedAt
		if since == 0 {
			since = cur.CreatedAt
		}
		p.ElapsedS = now.Sub(time.UnixMilli(since)).Round(time.Second).Seconds()
		if evs, err := r.o.DB.ListThreadEvents(ctx, cur.ThreadID, cur.ID, []string{KindToolCall, KindToolResult}, 0, 0); err == nil {
			running := map[string]string{}
			var order []string
			for _, e := range evs {
				var ev struct {
					Tool *struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"tool"`
				}
				_ = json.Unmarshal(e.Payload, &ev)
				if e.Kind == KindToolCall {
					p.Steps++
				}
				if ev.Tool == nil || ev.Tool.ID == "" {
					continue
				}
				if e.Kind == KindToolCall {
					running[ev.Tool.ID] = ev.Tool.Name
					order = append(order, ev.Tool.ID)
				} else {
					delete(running, ev.Tool.ID)
				}
			}
			for i := len(order) - 1; i >= 0; i-- {
				if name, ok := running[order[i]]; ok {
					p.Step = name
					break
				}
			}
		}
		r.mu.Lock()
		if t := r.active[botID]; t != nil && t.taskID == cur.ID {
			t.mu.Lock()
			p.Plan = append([]string(nil), t.plan...)
			t.mu.Unlock()
		}
		r.mu.Unlock()
		out.Current = p
		if out.State == "waiting" {
			if items, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, TaskID: cur.ID,
				Kinds: []string{"approval", "question", "secret"}}); err == nil && len(items) > 0 {
				it := items[len(items)-1]
				out.WaitingFor = &ProgressAsk{InboxID: it.ID, Kind: it.Kind, Prompt: clip(firstNonEmpty(it.Body, it.Title), 200), Since: it.CreatedAt}
			}
		}
		if e, err := r.o.DB.LatestTaskEvent(ctx, botID, cur.ID, KindError); err == nil {
			out.LastError = errorOf(e)
		}
	}
	// 交給隊友、還沒回來的（轉派出去的工作結束了，還欠著答案，LazyBoy progress waits）。
	waiters := open
	if deferred, err := r.o.DB.ListTasksWithMeta(ctx, botID, "$.deferred_reply"); err == nil {
		waiters = append(waiters, deferred...)
	}
	seen := map[string]bool{}
	for _, t := range waiters {
		if seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		msgs, err := r.o.DB.ListBotMessages(ctx, store.BotMessageFilter{OriginTaskID: t.ID, Intents: []string{"request", "question"}, Unanswered: true})
		if err != nil {
			continue
		}
		var owed struct {
			Deferred *struct {
				To string `json:"to"`
			} `json:"deferred_reply"`
		}
		_ = json.Unmarshal(t.Meta, &owed)
		forName := ""
		if owed.Deferred != nil && owed.Deferred.To != "" {
			if b, err := r.o.DB.GetBot(ctx, owed.Deferred.To); err == nil {
				forName = b.Name
			}
		}
		for _, m := range msgs {
			out.WaitingOn = append(out.WaitingOn, ProgressWait{BotID: m.To, Bot: m.ToName, Task: clip(firstLine(m.Text), 160), Since: m.CreatedAt, For: forName})
		}
	}
	if last, err := r.o.DB.LatestDoneTask(ctx, botID, VerdictCancelled); err == nil {
		l := &ProgressLast{TaskID: last.ID, Verdict: last.Verdict, Judge: TaskJudgment(last), At: last.FinishedAt}
		if e, err := r.o.DB.LatestTaskEvent(ctx, botID, last.ID, KindDone); err == nil {
			var d DonePayload
			if json.Unmarshal(e.Payload, &d) == nil {
				l.Result = clip(firstLine(d.Text), 240)
			}
		}
		out.Last = l
		if out.LastError == nil && last.Verdict == VerdictFailed {
			if e, err := r.o.DB.LatestTaskEvent(ctx, botID, last.ID, KindError); err == nil {
				out.LastError = errorOf(e)
			}
		}
	}
	out.Busy = out.State != "idle" || len(out.WaitingOn) > 0
	out.Text = progressText(out, now)
	return out, nil
}

func errorOf(e store.Event) *ProgressError {
	var ev struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(e.Payload, &ev)
	if ev.Text == "" {
		return nil
	}
	return &ProgressError{TaskID: e.TaskID, Text: clip(ev.Text, 300), At: e.CreatedAt}
}

// ago 是「剛剛／N 分鐘前／N 小時前／N 天前」（LazyBoy progress_text ago）。
func ago(ms int64, now time.Time) string {
	if ms <= 0 {
		return "不知道多久前"
	}
	s := now.Sub(time.UnixMilli(ms)).Seconds()
	switch {
	case s < 90:
		return "剛剛"
	case s < 5400:
		return fmt.Sprintf("%d 分鐘前", int(s/60))
	case s < 172800:
		return fmt.Sprintf("%d 小時前", int(s/3600))
	}
	return fmt.Sprintf("%d 天前", int(s/86400))
}

// progressText 把事實寫成幾行字給 Bot（LazyBoy org.progress_text；XBot 還沒有團隊成員與背景子任務的資料，這兩段先不寫）。
func progressText(p Progress, now time.Time) string {
	lines := []string{"【系統查得的目前狀況，是事實；回答時直接用，不要再說「我去查」】"}
	me := "你自己："
	if c := p.Current; c != nil {
		since := c.StartedAt
		me += fmt.Sprintf("正在做「%s」（%s，%s開始）", c.Title, c.From, ago(since, now))
		if c.Steps > 0 {
			me += fmt.Sprintf("，做了 %d 步", c.Steps)
		}
		if c.Step != "" {
			me += "，正在執行 " + c.Step
		}
	} else {
		me += "目前沒有任何工作在執行"
	}
	if p.Queued > 0 {
		me += fmt.Sprintf("，另有 %d 件排隊", p.Queued)
	}
	if p.WaitingFor != nil {
		me += "，卡在等使用者：" + p.WaitingFor.Prompt
	}
	lines = append(lines, me)
	if len(p.WaitingOn) > 0 {
		var ws []string
		for _, w := range p.WaitingOn {
			s := fmt.Sprintf("%s（%s交辦：%s）", w.Bot, ago(w.Since, now), clip(w.Task, 40))
			if w.For != "" {
				s += "，做完要回報給 " + w.For
			}
			ws = append(ws, s)
		}
		lines = append(lines, "你在等的隊友："+strings.Join(ws, "；"))
	} else {
		lines = append(lines, "你在等的隊友：沒有")
	}
	if p.LastError != nil {
		lines = append(lines, fmt.Sprintf("最後一次出錯：%s（%s）", clip(firstLine(p.LastError.Text), 120), ago(p.LastError.At, now)))
	}
	if p.Last != nil {
		line := "你最近一次完成工作：" + ago(p.Last.At, now)
		if p.Last.Judge != nil {
			line += "，系統判斷：" + p.Last.Judge.Label
		}
		lines = append(lines, line)
	}
	if p.Busy {
		lines = append(lines, "整件事還有人在工作。")
	} else {
		lines = append(lines, "整件事目前沒有任何人在工作。如果交辦的事還沒完成，代表卡住了：不要只說在等，現在就用 message_bot 追問或重新派工，或說明需要使用者做什麼。")
	}
	return strings.Join(lines, "\n")
}
