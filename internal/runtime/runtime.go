// Package runtime 是對話的執行層：把使用者的訊息交給 Bot 的引擎、把串流回來的事件存進資料庫並發布到事件匯流排。
//
// E02 是最小版：一個 Bot 同時只跑一輪（第二個訊息回 ErrBusy），沒有佇列、看門狗、自動續跑與任務完成判定
// （之後的任務卡補上，PRD §19、CH-12）。C05 的 chat.send／chat.stop／wait_until_settled 包裝這裡的 Chat、Stop、Wait。
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 發布到匯流排（並寫進 event 表）的事件種類。標 transient 的只發布、不寫資料庫（串流片段，完整內容在 message）。
const (
	KindMessageCreated = "message.created" // payload：store.Message
	KindText           = "chat.text"       // transient；payload：{message_id, text}
	KindThinking       = "chat.thinking"   // transient；payload：{message_id, text}
	KindToolCall       = "chat.tool_call"  // payload：engine.Event
	KindToolResult     = "chat.tool_result"
	KindNeedsApproval  = "chat.needs_approval" // payload：NeedsPayload（engine.Event＋inbox_id、expires_at）
	KindAnswered       = "chat.answered"       // payload：AnsweredPayload（誰怎麼回覆了 Bot 的提問；密碼類不含內容）
	KindUsage          = "chat.usage"
	KindStatus         = "chat.status"
	KindError          = "chat.error"
	KindDone           = "chat.done"  // payload：{message_id, status, verdict, text}
	KindTaskState      = "task.state" // payload：{state, verdict}
)

// 任務結束的判定（task.verdict）。
const (
	VerdictAnswer    = "answer"
	VerdictCancelled = "cancelled"
	VerdictFailed    = "failed"
)

// ErrBusy 表示這個 Bot 正在處理另一則訊息。
var ErrBusy = errors.New("這個 Bot 正在忙，等它做完或先停止")

// Options 是 Runtime 的設定。
type Options struct {
	DB     *store.DB
	Driver engine.Driver
	Bus    *events.Bus
	// Models 決定 Bot 用哪個模型（*Computers 符合）。
	Models interface {
		ResolveModel(ctx context.Context, b store.Bot) (ModelRef, error)
	}
	Logger *slog.Logger
	// QuestionTTL 是無人在場的工作裡提問多久沒回就過期；0 用 limits.QuestionTTL（測試可以調短）。
	QuestionTTL time.Duration
	// Budget 在交辦前檢查花費上限（Bot 與它的模型供應商）；超過時 Chat 回它的錯誤（*proxy.BudgetError）。可為 nil。
	Budget func(ctx context.Context, botID, providerID string) error
}

// Runtime 管理每個 Bot 正在進行的一輪對話。
type Runtime struct {
	o   Options
	log *slog.Logger

	mu        sync.Mutex
	active    map[string]*turn          // botID → 進行中的一輪
	closing   map[string]*turn          // taskID → 已經釋放 Bot、還在收尾的一輪（Wait 要等它寫完）
	consumers map[engine.Session]string // 已經有人在讀事件的 session → botID
}

// New 建立 Runtime。
func New(o Options) *Runtime {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Bus == nil {
		o.Bus = events.New()
	}
	if o.QuestionTTL <= 0 {
		o.QuestionTTL = limits.QuestionTTL
	}
	return &Runtime{o: o, log: o.Logger, active: map[string]*turn{}, closing: map[string]*turn{}, consumers: map[engine.Session]string{}}
}

// Bus 回傳事件匯流排。
func (r *Runtime) Bus() *events.Bus { return r.o.Bus }

// turn 是進行中的一輪。
type turn struct {
	botID, threadID, taskID string
	botName                 string
	source                  string // owner / bot / routine / webhook：不是 owner 的是無人在場的工作，提問會過期
	input                   string
	cancel                  context.CancelFunc
	done                    chan struct{}

	mu        sync.Mutex
	session   engine.Session
	messageID string
	text      strings.Builder
	persisted time.Time
	stopping  bool
	finished  bool
	waiting   bool
	lastErr   string
	pending   *pending // Bot 正在等人回覆的請求（一次一個）

	// askMu 讓「回覆後改回工作中」和「Bot 又問了新問題」不會交錯（否則新問題的「等你」會被蓋掉）。
	askMu sync.Mutex
}

// ChatResult 是 Chat 的結果：訊息已收下，回覆經事件串流回來。
type ChatResult struct {
	ThreadID  string `json:"thread_id"`
	TaskID    string `json:"task_id"`
	MessageID string `json:"message_id"` // 使用者那則訊息
}

// Chat 把使用者的訊息交給 Bot。threadID 空白時開一條新的對話串。
// 訊息與任務寫好就回傳；開機、送出與串流在背景進行，進度發布在匯流排上，用 Wait 等結果。
func (r *Runtime) Chat(ctx context.Context, botID, threadID, text string) (ChatResult, error) {
	return r.Start(ctx, ChatRequest{BotID: botID, ThreadID: threadID, Text: text})
}

// ChatRequest 是交給 Bot 的一件工作。
type ChatRequest struct {
	BotID, ThreadID, Text string
	// Source 是誰交辦的：owner（預設，你自己）、bot、routine、webhook。不是 owner 的算無人在場，提問會過期（PRD §18）。
	Source string
}

// Start 和 Chat 一樣，但可以指定交辦來源。
func (r *Runtime) Start(ctx context.Context, req ChatRequest) (ChatResult, error) {
	botID, threadID, text := req.BotID, req.ThreadID, strings.TrimSpace(req.Text)
	source := req.Source
	if source == "" {
		source = "owner"
	}
	if text == "" {
		return ChatResult{}, errors.New("訊息是空的")
	}
	b, err := r.o.DB.GetBot(ctx, botID)
	if err != nil {
		return ChatResult{}, err
	}
	if b.Archived {
		return ChatResult{}, errors.New("這個 Bot 已封存")
	}
	ref, err := r.o.Models.ResolveModel(ctx, b)
	if err != nil {
		return ChatResult{}, err
	}
	if r.o.Budget != nil {
		if err := r.o.Budget(ctx, botID, ref.Provider); err != nil {
			return ChatResult{}, err
		}
	}
	var th store.Thread
	if threadID == "" {
		if th, err = r.o.DB.CreateThread(ctx, store.Thread{BotID: botID, Title: title(text)}); err != nil {
			return ChatResult{}, err
		}
	} else {
		if th, err = r.o.DB.GetThread(ctx, threadID); err != nil {
			return ChatResult{}, err
		}
		if th.BotID != botID {
			return ChatResult{}, fmt.Errorf("對話串不屬於這個 Bot：%w", store.ErrNotFound)
		}
	}

	r.mu.Lock()
	if _, busy := r.active[botID]; busy {
		r.mu.Unlock()
		return ChatResult{}, ErrBusy
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t := &turn{botID: botID, threadID: th.ID, botName: b.Name, source: source, input: text, cancel: cancel, done: make(chan struct{})}
	r.active[botID] = t
	r.mu.Unlock()

	var task store.Task
	var msg store.Message
	err = r.o.DB.Tx(ctx, func(q *store.Queries) error {
		var err error
		if task, err = q.CreateTask(ctx, store.Task{BotID: botID, ThreadID: th.ID, Source: source, State: store.TaskQueued, Input: text}); err != nil {
			return err
		}
		meta, _ := json.Marshal(map[string]string{"task_id": task.ID})
		msg, err = q.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "owner", Content: text, Meta: meta})
		return err
	})
	if err != nil {
		r.mu.Lock()
		delete(r.active, botID)
		r.mu.Unlock()
		cancel()
		return ChatResult{}, err
	}
	r.mu.Lock()
	t.taskID = task.ID
	r.mu.Unlock()
	r.emit(t, KindMessageCreated, msg, true)
	go r.run(runCtx, t, th, b.Name, ref)
	return ChatResult{ThreadID: th.ID, TaskID: task.ID, MessageID: msg.ID}, nil
}

// run 在背景完成一輪：開（或接回）引擎 session（必要時開機）、送出訊息；之後由 consume 處理事件直到 done。
func (r *Runtime) run(ctx context.Context, t *turn, th store.Thread, botName string, ref ModelRef) {
	r.setState(t, store.TaskWaitingBox)
	title := botName
	if th.Title != "" {
		title = botName + " · " + th.Title
	}
	s, err := r.o.Driver.Start(ctx, engine.StartOptions{BotID: t.botID, ThreadID: t.threadID, Resume: th.EngineSession, Title: title, Model: ref.Model})
	if err != nil {
		r.finishErr(t, err)
		return
	}
	if id := s.ResumeID(); id != "" && id != th.EngineSession {
		if err := r.o.DB.SetThreadEngineSession(context.WithoutCancel(ctx), t.threadID, id); err != nil {
			r.log.Warn("記錄引擎 session 失敗", "thread", t.threadID, "err", err)
		}
	}
	r.consume(s, t.botID)

	meta, _ := json.Marshal(map[string]any{"task_id": t.taskID, "streaming": true})
	msg, err := r.o.DB.AddMessage(context.WithoutCancel(ctx), store.Message{Scope: store.ScopeThread, ScopeID: t.threadID, Sender: "bot:" + t.botID, Meta: meta})
	if err != nil {
		r.finishErr(t, err)
		return
	}
	t.mu.Lock()
	t.session, t.messageID = s, msg.ID
	stopping := t.stopping
	t.mu.Unlock()
	r.emit(t, KindMessageCreated, msg, true)
	if stopping {
		r.finish(t, engine.StatusInterrupted, "")
		return
	}
	r.setState(t, store.TaskRunning)
	if err := r.o.Driver.Send(ctx, s, engine.Message{Text: t.input}); err != nil {
		r.finishErr(t, err)
		return
	}
	// 送出後 resume id 可能才定下來（Hermes 第一個 prompt 才建資料列）。
	if id := s.ResumeID(); id != "" && id != th.EngineSession {
		_ = r.o.DB.SetThreadEngineSession(context.WithoutCancel(ctx), t.threadID, id)
	}
}

// consume 為一個 session 開一個讀事件的 goroutine（每個 session 只開一次）。
func (r *Runtime) consume(s engine.Session, botID string) {
	r.mu.Lock()
	if _, ok := r.consumers[s]; ok {
		r.mu.Unlock()
		return
	}
	r.consumers[s] = botID
	r.mu.Unlock()
	go func() {
		for e := range r.o.Driver.Events(s) {
			r.handle(s, botID, e)
		}
		r.mu.Lock()
		delete(r.consumers, s)
		r.mu.Unlock()
	}()
}

// handle 把一個引擎事件交給它所屬的那一輪。沒有進行中的一輪（例：背景工作的回報）就略過。
func (r *Runtime) handle(s engine.Session, botID string, e engine.Event) {
	r.mu.Lock()
	t := r.active[botID]
	r.mu.Unlock()
	if t == nil {
		return
	}
	t.mu.Lock()
	mine := t.session == s && !t.finished
	msgID := t.messageID
	t.mu.Unlock()
	if !mine {
		return
	}
	if e.Type != engine.EventNeedsApproval && e.Type != engine.EventRequestClosed {
		t.mu.Lock()
		wasWaiting := t.waiting
		t.waiting = false
		t.mu.Unlock()
		if wasWaiting && e.Type != engine.EventDone && e.Type != engine.EventError {
			r.setState(t, store.TaskRunning)
		}
	}
	switch e.Type {
	case engine.EventText:
		t.mu.Lock()
		t.text.WriteString(e.Text)
		content := t.text.String()
		save := time.Since(t.persisted) >= limits.StreamPersistInterval
		if save {
			t.persisted = time.Now()
		}
		t.mu.Unlock()
		r.emit(t, KindText, map[string]string{"message_id": msgID, "text": e.Text}, false)
		if save {
			meta, _ := json.Marshal(map[string]any{"task_id": t.taskID, "streaming": true})
			if err := r.o.DB.UpdateMessage(context.Background(), msgID, content, nil, meta); err != nil {
				r.log.Warn("寫入串流中的回覆失敗", "message", msgID, "err", err)
			}
		}
	case engine.EventThinking:
		r.emit(t, KindThinking, map[string]string{"message_id": msgID, "text": e.Text}, false)
	case engine.EventToolCall:
		r.emit(t, KindToolCall, e, true)
	case engine.EventToolResult:
		r.emit(t, KindToolResult, e, true)
	case engine.EventUsage:
		r.emit(t, KindUsage, e, true)
	case engine.EventStatus:
		r.emit(t, KindStatus, e, true)
	case engine.EventNeedsApproval:
		t.mu.Lock()
		t.waiting = true
		t.mu.Unlock()
		state := store.TaskWaitingInput
		if e.Request != nil && e.Request.Kind == "approval" {
			state = store.TaskWaitingApproval
		}
		// 先開卡、記下 Bot 在問什麼，再改狀態：看到「等你」的人一定查得到問題內容，也能就地回覆。
		t.askMu.Lock()
		item := r.openCard(t, e.Request)
		r.emit(t, KindNeedsApproval, NeedsPayload{Event: e, InboxID: item.ID, ExpiresAt: item.ExpiresAt}, true)
		r.setState(t, state)
		t.askMu.Unlock()
	case engine.EventRequestClosed:
		if e.Request != nil {
			r.closeRequest(t, e.Request.ID)
		}
	case engine.EventError:
		t.mu.Lock()
		t.lastErr = e.Text
		t.mu.Unlock()
		r.emit(t, KindError, e, true)
		if e.Fatal {
			r.finish(t, engine.StatusError, e.Text)
		}
	case engine.EventDone:
		r.finish(t, e.Status, e.Text)
	}
}

// finishErr 結束一輪：開始前就失敗了（開不了機、送不出去）。
func (r *Runtime) finishErr(t *turn, err error) {
	t.mu.Lock()
	stopping := t.stopping
	t.mu.Unlock()
	if stopping && errors.Is(err, context.Canceled) {
		r.finish(t, engine.StatusInterrupted, "")
		return
	}
	r.log.Warn("這一輪沒有完成", "bot", t.botID, "task", t.taskID, "err", err)
	r.emit(t, KindError, engine.Event{Type: engine.EventError, Text: err.Error()}, true)
	r.finish(t, engine.StatusError, "工作未完成："+err.Error())
}

// finish 寫下最後的回覆、結束任務、發布 done，並釋放這個 Bot。只會做一次。
func (r *Runtime) finish(t *turn, status, text string) {
	t.askMu.Lock() // 和 resume 互斥：回覆前改回「工作中」不會蓋掉「已完成」
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		t.askMu.Unlock()
		return
	}
	t.finished = true
	t.askMu.Unlock()
	p := t.pending
	t.pending = nil
	streamed := t.text.String()
	msgID := t.messageID
	stopping := t.stopping
	lastErr := t.lastErr
	t.mu.Unlock()

	verdict := VerdictAnswer
	switch {
	case status == engine.StatusInterrupted || (stopping && status != engine.StatusComplete):
		verdict, status = VerdictCancelled, engine.StatusInterrupted
	case status == engine.StatusError:
		verdict = VerdictFailed
	}
	final := text
	switch verdict {
	case VerdictAnswer, VerdictCancelled:
		if final == "" {
			final = streamed
		}
	case VerdictFailed:
		if final == "" {
			final = lastErr
		}
		if streamed != "" && final != streamed {
			final = strings.TrimSpace(streamed + "\n\n" + final)
		}
	}
	ctx := context.Background()
	if msgID != "" {
		meta, _ := json.Marshal(map[string]any{"task_id": t.taskID, "status": status})
		if err := r.o.DB.UpdateMessage(ctx, msgID, final, nil, meta); err != nil {
			r.log.Warn("寫入回覆失敗", "message", msgID, "err", err)
		}
	} else if final != "" {
		// 還沒開始就失敗：把原因寫成 Bot 的一則訊息，對話裡看得到。
		meta, _ := json.Marshal(map[string]any{"task_id": t.taskID, "status": status})
		if m, err := r.o.DB.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: t.threadID, Sender: "bot:" + t.botID, Content: final, Meta: meta}); err == nil {
			msgID = m.ID
			r.emit(t, KindMessageCreated, m, true)
		}
	}
	if p != nil {
		// 工作結束了，Bot 不再等這個回覆。
		p.stop()
		r.settle(t, p, store.InboxDismissed, ResolutionGone, "system", nil, "")
	}
	r.notifyDone(t, verdict, final)
	// 先釋放這個 Bot 再寫 done：看到任務結束的人（wait_until_settled）馬上交辦下一件不會撞到「正在忙」。
	r.mu.Lock()
	if r.active[t.botID] == t {
		delete(r.active, t.botID)
	}
	r.closing[t.taskID] = t
	r.mu.Unlock()
	if err := r.o.DB.SetTaskState(ctx, t.taskID, store.TaskDone, verdict); err != nil {
		r.log.Warn("寫入任務狀態失敗", "task", t.taskID, "err", err)
	}
	r.emit(t, KindTaskState, map[string]string{"state": store.TaskDone, "verdict": verdict}, true)
	r.emit(t, KindDone, map[string]string{"message_id": msgID, "status": status, "verdict": verdict, "text": final}, true)
	t.cancel()
	close(t.done)
	r.mu.Lock()
	delete(r.closing, t.taskID)
	r.mu.Unlock()
}

func (r *Runtime) setState(t *turn, state string) {
	if err := r.o.DB.SetTaskState(context.Background(), t.taskID, state, ""); err != nil {
		r.log.Warn("寫入任務狀態失敗", "task", t.taskID, "err", err)
	}
	r.emit(t, KindTaskState, map[string]string{"state": state}, true)
}

// emit 發布一個事件；persist 時先寫進 event 表，帶著流水號發布。
func (r *Runtime) emit(t *turn, kind string, payload any, persist bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		r.log.Error("事件無法轉成 JSON", "kind", kind, "err", err)
		return
	}
	e := events.Event{Kind: kind, BotID: t.botID, ThreadID: t.threadID, TaskID: t.taskID, Payload: raw, At: time.Now().UnixMilli()}
	if persist {
		row, err := r.o.DB.AppendEvent(context.Background(), store.Event{BotID: t.botID, ThreadID: t.threadID, TaskID: t.taskID, Kind: kind, Payload: raw})
		if err != nil {
			r.log.Warn("寫入事件失敗", "kind", kind, "err", err)
		} else {
			e.ID, e.At = row.ID, row.CreatedAt
		}
	}
	r.o.Bus.Publish(e)
}

// Stop 停止 Bot 正在進行的一輪（threadID 不是空白時，只停那條對話串的）。沒有在進行時回傳 false。
// 停止後仍會收到 verdict=cancelled 的 chat.done。
func (r *Runtime) Stop(ctx context.Context, botID, threadID string) (bool, error) {
	r.mu.Lock()
	t := r.active[botID]
	r.mu.Unlock()
	if t == nil || (threadID != "" && t.threadID != threadID) {
		return false, nil
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return false, nil
	}
	already := t.stopping
	t.stopping = true
	s := t.session
	t.mu.Unlock()
	if !already {
		r.setState(t, store.TaskStopping)
	}
	if s == nil {
		// 還在開機或開 session：取消它，run 會以 cancelled 收尾。
		t.cancel()
		return true, nil
	}
	if err := r.o.Driver.Interrupt(ctx, s); err != nil {
		if errors.Is(err, engine.ErrDisconnected) {
			r.finish(t, engine.StatusInterrupted, "")
			return true, nil
		}
		return true, err
	}
	return true, nil
}

// Active 回傳 Bot 正在進行的對話串與任務（沒有時是空字串）；Model Proxy 用它把用量記到任務上。
func (r *Runtime) Active(botID string) (threadID, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.active[botID]; t != nil {
		return t.threadID, t.taskID
	}
	return "", ""
}

// Wait 等任務結束（或 ctx 結束），回傳任務的最後狀態。
func (r *Runtime) Wait(ctx context.Context, taskID string) (store.Task, error) {
	r.mu.Lock()
	var done chan struct{}
	for _, t := range r.active {
		if t.taskID == taskID {
			done = t.done
		}
	}
	if t := r.closing[taskID]; t != nil {
		done = t.done
	}
	r.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return store.Task{}, ctx.Err()
		}
	}
	return r.o.DB.GetTask(ctx, taskID)
}

// title 用訊息開頭當新對話串的標題。
func title(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const limit = 40
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}
