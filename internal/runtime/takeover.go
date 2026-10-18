package runtime

// B14 接手電腦（PRD BX-8）：使用者按「接手」時 Bot 暫停——正在做的那一輪送出中斷，排隊中的工作先不開始；
// 交還時 Bot 帶著使用者的說明，在同一條對話串接著做原本的工作（LazyBoy ComputerPane 的 handBack：
// 「我剛剛在電腦上操作過，請重新看一下目前的畫面再繼續」）。

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"xbot/internal/store"
)

// 對話裡的系統行（KindActivity）：接手、交還、示範教學（B32）。
const (
	ActivityTakeover = "takeover"
	ActivityHandback = "handback"
	ActivityTeaching = "teaching"
)

// ErrNotPaused 表示這個 Bot 沒有在接手中。
var ErrNotPaused = errors.New("這個 Bot 的電腦沒有在接手中")

// hold 是一次接手：被中斷的那一輪（沒有時欄位空白），交還時接著做。
type hold struct {
	threadID, taskID, source string
	meta                     json.RawMessage // 隊友交辦的：原本的交辦（Hooks.ContinueMeta），結果才會送回隊友
	since                    time.Time
}

// Paused 是 Pause 的結果。
type Paused struct {
	// ThreadID、TaskID 是被中斷的那一輪；Bot 本來沒在做事時是空白。
	ThreadID string `json:"thread_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Since    int64  `json:"since"`
}

// Pause 讓 Bot 暫停（使用者接手電腦）：正在做的那一輪送出中斷（等它結束，最多看門狗的 Grace），
// 之後排隊中或新來的工作都先排著，直到 Resume。已經在暫停中時直接回傳原本的狀態。
func (r *Runtime) Pause(ctx context.Context, botID string) (Paused, error) {
	r.dispatchMu.Lock()
	r.mu.Lock()
	if h := r.held[botID]; h != nil {
		p := Paused{ThreadID: h.threadID, TaskID: h.taskID, Since: h.since.UnixMilli()}
		r.mu.Unlock()
		r.dispatchMu.Unlock()
		return p, nil
	}
	h := &hold{since: time.Now()}
	t := r.active[botID]
	r.held[botID] = h
	r.mu.Unlock()
	r.dispatchMu.Unlock()

	if t != nil {
		t.mu.Lock()
		live := !t.finished && !t.stopping
		if live {
			t.paused = true
		}
		t.mu.Unlock()
		if live {
			source, meta := SourceOwner, json.RawMessage(nil)
			if t.source == SourceBot {
				if task, err := r.o.DB.GetTask(ctx, t.taskID); err == nil {
					if hk := r.hooks(); hk.ContinueMeta != nil {
						if m, ok := hk.ContinueMeta(task); ok {
							source, meta = SourceBot, m
						}
					}
				}
			}
			r.mu.Lock()
			h.threadID, h.taskID, h.source, h.meta = t.threadID, t.taskID, source, meta
			r.mu.Unlock()
			if _, err := r.stopTurn(ctx, t); err != nil {
				r.log.Warn("接手時中斷 Bot 失敗", "bot", botID, "task", t.taskID, "err", err)
			}
			// 等這一輪真的停下來，使用者才不會和 Bot 搶滑鼠；引擎不理中斷時看門狗會在 Grace 後收掉它。
			select {
			case <-t.done:
			case <-ctx.Done():
			case <-time.After(r.o.Watchdog.Grace + time.Second):
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return Paused{ThreadID: h.threadID, TaskID: h.taskID, Since: h.since.UnixMilli()}, nil
}

// IsPaused 回答 Bot 是不是在接手中。
func (r *Runtime) IsPaused(botID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held[botID] != nil
}

// ResumeRequest 是交還時要給 Bot 的話。
type ResumeRequest struct {
	// ThreadID 是 Bot 本來沒在做事時（例：Bot 請你接手後結束了那一輪）要接著做的對話串；空白時不開新的一輪。
	// 有被中斷的那一輪時用它的對話串。
	ThreadID string
	// Text 是對話裡顯示的那一行（系統行 activity=handback）；Prompt 是送給 Bot 的話。Prompt 空白時不開新的一輪。
	Text, Prompt string
}

// Resume 結束接手：有要接著做的對話串時，先開一件接著做的工作（排在最前面），再讓排隊中的工作繼續。
// 回傳接著做的那件工作（沒有時空白）。
func (r *Runtime) Resume(ctx context.Context, botID string, req ResumeRequest) (string, error) {
	r.mu.Lock()
	h := r.held[botID]
	r.mu.Unlock()
	if h == nil {
		return "", ErrNotPaused
	}
	thread, source, meta := h.threadID, h.source, h.meta
	if thread == "" {
		thread, source = req.ThreadID, SourceOwner
	}
	var taskID string
	var startErr error
	if thread != "" && req.Prompt != "" {
		// 還在暫停中：Start 只會把它排進佇列，下面再讓它第一個開始。
		res, err := r.Start(ctx, ChatRequest{BotID: botID, ThreadID: thread, Text: req.Text, Prompt: req.Prompt,
			Sender: "system", Kind: KindActivity, MessageMeta: map[string]any{"activity": ActivityHandback},
			Source: source, TaskMeta: meta, Queue: true})
		if err != nil {
			startErr = err
			r.log.Warn("交還後接著做失敗", "bot", botID, "err", err)
		} else {
			taskID = res.TaskID
		}
	}
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	r.mu.Lock()
	delete(r.held, botID)
	busy := r.active[botID] != nil
	r.mu.Unlock()
	if !busy && taskID != "" {
		if task, err := r.o.DB.GetTask(context.WithoutCancel(ctx), taskID); err == nil && task.State == store.TaskQueued {
			r.begin(task)
			return taskID, nil
		}
	}
	r.next(botID)
	return taskID, startErr
}

// Note 在對話串記一行系統訊息（KindActivity），例：「你接手了電腦，小秘先暫停」。
func (r *Runtime) Note(ctx context.Context, botID, threadID, activity, text string) error {
	meta, _ := json.Marshal(map[string]any{"activity": activity})
	m, err := r.o.DB.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: threadID, Sender: "system", Kind: KindActivity,
		Content: text, Meta: meta})
	if err != nil {
		return err
	}
	r.publish(botID, threadID, "", KindMessageCreated, m, true)
	return nil
}
