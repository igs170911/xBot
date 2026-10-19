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
	"xbot/internal/engine/hermes"
	"xbot/internal/events"
	"xbot/internal/judge"
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
	KindDone           = "chat.done"  // payload：DonePayload {message_id, status, verdict, text, judge}
	KindTaskState      = "task.state" // payload：{state, verdict}
)

// 任務結束的判定（task.verdict）。
const (
	VerdictAnswer    = "answer"
	VerdictCancelled = "cancelled"
	VerdictFailed    = "failed"
)

// DonePayload 是 chat.done 的內容。Judge 是做完了沒的判斷（X04；停掉的、中斷的沒有）。
type DonePayload struct {
	MessageID string    `json:"message_id"`
	Status    string    `json:"status"`
	Verdict   string    `json:"verdict"`
	Text      string    `json:"text"`
	Judge     *Judgment `json:"judge,omitempty"`
	// Quota 是額度用完卡（EN-7）；這一輪不是因為額度失敗時沒有。
	Quota *QuotaView `json:"quota,omitempty"`
}

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
	// Watchdog 是對話看門狗的時間（CH-12）；0 的欄位用 limits.WatchdogIdle、WatchdogToolIdle、StopGrace、
	// WatchInterval（測試可以調短）。
	Watchdog Watchdog
	// Budget 在交辦前檢查花費上限（Bot 與它的模型供應商）；超過時 Chat 回它的錯誤（*proxy.BudgetError）。可為 nil。
	Budget func(ctx context.Context, botID, providerID string) error
	// Judge 判斷一輪做完了沒（X04，PRD §20 用途 2）；nil 用規則（judge.Rules）。X05：快判模型＋規則兜底（judge.Service.TaskDoneJudge）。
	Judge judge.Judge
	// Namer 是對話串命名的快判（X05，§20 用途 10）：你開新對話串時在背景呼叫，生效時 ok=true，標題換成它回的（≤ 16 字）。可為 nil。
	Namer func(ctx context.Context, text, botID string) (string, bool)
	// Summarize 是一行摘要的快判（X05，§20 用途 8）：完成通知的內文。生效時 ok=true；可為 nil（第一行截斷）。
	Summarize func(ctx context.Context, text, taskID, botID string) (string, bool)
	// Stranded 回答這一輪是不是背景子任務回報後的那一輪，以及失敗後沒人接手的背景子任務（目標文字）。
	// LazyBoy 用 Hermes 的 subagent 追蹤；XBot 還沒有這個來源，nil 時不叫醒（見 X04 待確認）。
	Stranded func(ctx context.Context, task store.Task) (goals []string, background bool)
	// Admit 在開機前排隊等主機（B12：主機離線或滿了，CL-4、CL-6）；wait 收到排隊原因時，任務維持 waiting_box，
	// 並發 task.state（payload 帶 wait）。回傳的 release 在開機完成後呼叫。可為 nil（不排隊）。
	Admit func(ctx context.Context, botID string, wait func(any)) (release func(), err error)
	// QuotaLog 是 Model Proxy 看到的額度類錯誤（EN-7）：Hermes 的一輪失敗時，用它找出是哪個供應商、何時恢復。可為 nil。
	QuotaLog *engine.QuotaLog
	// QuotaCard 回傳額度卡上引擎（或供應商）的名稱，與可以換用的引擎、模型（engines.Service.QuotaCard）。可為 nil（只顯示引擎代號、沒有選項）。
	QuotaCard func(ctx context.Context, botID string, q *engine.QuotaError) (label string, options []engine.SwitchOption)
}

// Watchdog 是對話看門狗的時間（LazyBoy runtime.watch()）。
type Watchdog struct {
	// Idle：一輪多久沒有任何事件就送中斷；Tool：有工具正在執行時的同一個時限。
	Idle, Tool time.Duration
	// Grace：送出中斷（看門狗或按停止）後，等引擎自己結束的時間；過了就直接結束這一輪。
	Grace time.Duration
	// Interval：多久檢查一次。
	Interval time.Duration
}

// StalledText 是看門狗結束一輪時寫進回覆的說明。出自 LazyBoy server/runtime.py STALLED_TEXT。
const StalledText = "Bot 長時間沒有回應，已停止這次工作；檔案與對話都保留。可以再交辦一次或請它繼續。"

// Runtime 管理每個 Bot 正在進行的一輪對話。
type Runtime struct {
	o   Options
	log *slog.Logger

	// dispatchMu 讓「收下一件工作」和「Bot 空下來開始下一件」不會交錯（排隊、優先順序，LP-1）。
	dispatchMu sync.Mutex

	hookMu sync.RWMutex
	hook   Hooks

	mu        sync.Mutex
	active    map[string]*turn          // botID → 進行中的一輪
	held      map[string]*hold          // botID → 使用者接手電腦中（B14）：不開始新的工作
	closing   map[string]*turn          // taskID → 已經釋放 Bot、還在收尾的一輪（Wait 要等它寫完）
	consumers map[engine.Session]string // 已經有人在讀事件的 session → botID

	// stallNudged：同一個失敗的背景子任務已經叫 Bot 回去處理幾次（botID＋目標前 80 字，LazyBoy stall_nudged）。
	stallMu     sync.Mutex
	stallNudged map[string]int

	// resumeMu 讓同一件被中斷的工作按兩次「接著做」只開一件（ResumeInterrupted）。
	resumeMu sync.Mutex
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
	w := &o.Watchdog
	for _, d := range []struct {
		v   *time.Duration
		def time.Duration
	}{{&w.Idle, limits.WatchdogIdle}, {&w.Tool, limits.WatchdogToolIdle}, {&w.Grace, limits.StopGrace}, {&w.Interval, limits.WatchInterval}} {
		if *d.v <= 0 {
			*d.v = d.def
		}
	}
	return &Runtime{o: o, log: o.Logger, active: map[string]*turn{}, held: map[string]*hold{}, closing: map[string]*turn{}, consumers: map[engine.Session]string{},
		stallNudged: map[string]int{}}
}

// Bus 回傳事件匯流排。
func (r *Runtime) Bus() *events.Bus { return r.o.Bus }

// Hooks 讓協作層（internal/collab，X02）接上 runtime。每個欄位都可以是 nil。
type Hooks struct {
	// Prompt 在一件工作真的開始時組出要送給引擎的文字（例：群組座位輪到時才讀新的群組訊息，LazyBoy room_prompt）。
	// ok=false 時用 task.Input；回錯誤時這一輪以失敗結束。
	Prompt func(ctx context.Context, task store.Task) (prompt string, ok bool, err error)
	// Finished 在一件工作結束後呼叫（含排隊中被取消的）：把回覆送回等它的 Bot、貼到群組（LazyBoy collab.finished）。
	// 呼叫時 Bot 已經空下來，可以再交辦工作。
	Finished func(task store.Task, verdict, text string)
	// Stopped 在 Stop 停下一個 Bot 的工作後呼叫（停止連鎖，LP-6）。
	Stopped func(botID string, taskIDs []string)
	// Silent 回答這一輪的回覆要不要藏起來（隊友交辦、群組裡回 [silent] 的；群組已刪除的）。
	Silent func(taskID, verdict, text string) bool
	// ContinueMeta：隊友交辦的工作只回了「我來查一下」時，續做的那一輪要帶的 task.meta（原本的交辦，結果才會送回隊友）。
	// ok=false 時不續做（X04）。
	ContinueMeta func(task store.Task) (meta json.RawMessage, ok bool)
}

// SetHooks 接上協作層。
func (r *Runtime) SetHooks(h Hooks) {
	r.hookMu.Lock()
	r.hook = h
	r.hookMu.Unlock()
}

func (r *Runtime) hooks() Hooks {
	r.hookMu.RLock()
	defer r.hookMu.RUnlock()
	return r.hook
}

// attended：你自己交辦的，或你在群組發言叫醒的（不是低優先的群組座位）。其餘是無人在場的工作（LazyBoy unattended）。
func (t *turn) attended() bool {
	return t.source == SourceOwner || (t.source == SourceRoom && !t.lowPriority)
}

// turn 是進行中的一輪。
type turn struct {
	botID, threadID, taskID string
	botName                 string
	source                  string // owner / bot / room / routine / webhook：不是 owner 的是無人在場的工作，提問會過期
	lowPriority             bool   // Bot 交辦、群組接力（你的訊息會排在它後面第一個）
	input                   string
	orig                    string // 交辦時的原文（額度用完後可以重送的那一則）
	cancel                  context.CancelFunc
	done                    chan struct{}

	mu        sync.Mutex
	session   engine.Session
	messageID string
	text      strings.Builder
	persisted time.Time
	stopping  bool
	paused    bool // 使用者接手電腦而中斷（B14）：交還時接著做
	finished  bool
	waiting   bool
	lastErr   string
	pending   *pending // Bot 正在等人回覆的請求（一次一個）
	usedTools bool     // 這一輪呼叫過工具（判斷「只說要做」用）
	// engine、provider：這一輪用的引擎與 Model Proxy 供應商；started：開始送出的時間；quota：引擎報的額度錯誤（EN-7）。
	engine, provider string
	started          time.Time
	quota            *engine.QuotaError
	plan             []string // Bot 的計畫裡還沒做完的項目（最新的一份）

	// 看門狗：最後一次收到事件的時間、正在執行的工具、看門狗送出中斷的時間、按停止的時間。
	seen      time.Time
	openTools map[string]bool
	stalled   time.Time
	stopAt    time.Time

	// askMu 讓「回覆後改回工作中」和「Bot 又問了新問題」不會交錯（否則新問題的「等你」會被蓋掉）。
	askMu sync.Mutex
}

// ChatResult 是 Chat 的結果：訊息已收下，回覆經事件串流回來。
type ChatResult struct {
	ThreadID  string `json:"thread_id"`
	TaskID    string `json:"task_id"`
	MessageID string `json:"message_id"` // 使用者那則訊息
	// Queued：Bot 正在做別的事，這件工作在排隊（做完前面的才開始；你的訊息排在 Bot 交辦的前面，LP-1）。
	Queued bool `json:"queued,omitempty"`
}

// Chat 把使用者的訊息交給 Bot。threadID 空白時開一條新的對話串。
// 訊息與任務寫好就回傳；開機、送出與串流在背景進行，進度發布在匯流排上，用 Wait 等結果。
func (r *Runtime) Chat(ctx context.Context, botID, threadID, text string) (ChatResult, error) {
	return r.Start(ctx, ChatRequest{BotID: botID, ThreadID: threadID, Text: text})
}

// 任務來源（task.source）。
const (
	SourceOwner   = "owner"
	SourceBot     = "bot"  // 隊友傳來的工作（X02）
	SourceRoom    = "room" // 群組座位的一輪（X02）
	SourceRoutine = "routine"
	SourceWebhook = "webhook"
)

// ChatRequest 是交給 Bot 的一件工作。
type ChatRequest struct {
	BotID, ThreadID, Text string
	// Source 是誰交辦的：owner（預設，你自己）、bot、room、routine、webhook。
	// 不是 owner 的算無人在場（你在群組發言叫醒的除外），提問會過期（PRD §18）。
	Source string
	// Prompt 是送給引擎的文字；空白時用 Text。Text 是對話裡顯示的那一則（例：隊友傳話的原文，Prompt 是加了資料框架的版本）。
	Prompt string
	// Sender、Kind 是對話裡那則訊息的發送者與種類；空白時是 owner、text。
	Sender, Kind string
	// MessageMeta 會併進那則訊息的 meta（task_id 之外的欄位，例：peer）。
	MessageMeta map[string]any
	// TaskMeta 是任務的 meta（例：隊友傳話的 hop、chain，群組的 room_id、round）。
	TaskMeta json.RawMessage
	// LowPriority：Bot 交辦、群組接力是低優先，排在你的訊息後面（LP-1）。
	LowPriority bool
	// Queue：Bot 正在忙時排隊，不回 ErrBusy。你的訊息遇到 Bot 在做低優先的工作時一律排隊（排在第一個）。
	Queue bool
	// Title 是開新對話串時的標題；空白時用 Text 開頭。
	Title string
	// Actor 是誰送出這則訊息（owner、agent:<權杖 id>）；ActorName 是顯示名稱、Via 是從哪裡來（mcp、http…）。
	// 代理送的記在訊息與工作的 meta 裡：介面標「由 ○○（MCP）傳送」（MCP-13），每日花費算到代理頭上（MCP-12）。
	Actor, ActorName, Via string
}

// Start 和 Chat 一樣，但可以指定交辦來源、優先順序與排隊。
func (r *Runtime) Start(ctx context.Context, req ChatRequest) (ChatResult, error) {
	botID, threadID, text := req.BotID, req.ThreadID, strings.TrimSpace(req.Text)
	source := req.Source
	if source == "" {
		source = SourceOwner
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
		t := req.Title
		if t == "" {
			t = title(text)
		}
		if th, err = r.o.DB.CreateThread(ctx, store.Thread{BotID: botID, Title: t}); err != nil {
			return ChatResult{}, err
		}
		if req.Title == "" && source == SourceOwner && r.o.Namer != nil {
			go r.nameThread(th.ID, botID, text, t)
		}
	} else {
		if th, err = r.o.DB.GetThread(ctx, threadID); err != nil {
			return ChatResult{}, err
		}
		if th.BotID != botID {
			return ChatResult{}, fmt.Errorf("對話串不屬於這個 Bot：%w", store.ErrNotFound)
		}
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = text
	}
	sender, kind := req.Sender, req.Kind
	if sender == "" {
		sender = "owner"
	}

	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	r.mu.Lock()
	cur := r.active[botID]
	r.mu.Unlock()
	if cur != nil && !req.Queue && (source != SourceOwner || !cur.lowPriority) {
		return ChatResult{}, ErrBusy
	}
	var task store.Task
	var msg store.Message
	err = r.o.DB.Tx(ctx, func(q *store.Queries) error {
		var err error
		taskMeta := req.TaskMeta
		if req.Actor != "" && req.Actor != "owner" {
			m := map[string]any{}
			_ = json.Unmarshal(taskMeta, &m)
			m["actor"] = req.Actor
			taskMeta, _ = json.Marshal(m)
		}
		if task, err = q.CreateTask(ctx, store.Task{BotID: botID, ThreadID: th.ID, Source: source, State: store.TaskQueued, Input: prompt,
			LowPriority: req.LowPriority, Meta: taskMeta}); err != nil {
			return err
		}
		meta := map[string]any{}
		for k, v := range req.MessageMeta {
			meta[k] = v
		}
		meta["task_id"] = task.ID
		if req.Actor != "" && req.Actor != "owner" {
			meta["actor"], meta["actor_name"], meta["via"] = req.Actor, req.ActorName, req.Via
		}
		rawMeta, _ := json.Marshal(meta)
		msg, err = q.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: sender, Kind: kind, Content: text, Meta: rawMeta})
		return err
	})
	if err != nil {
		return ChatResult{}, err
	}
	r.publish(botID, th.ID, task.ID, KindMessageCreated, msg, true)
	if source == SourceOwner && kind != KindActivity {
		r.closeJudgeCards(context.WithoutCancel(ctx), botID, th.ID) // 你回話了：「需要你」卡收掉（X04）
	}
	started := r.next(botID)
	if started != task.ID {
		r.publish(botID, th.ID, task.ID, KindTaskState, map[string]string{"state": store.TaskQueued}, true)
	}
	return ChatResult{ThreadID: th.ID, TaskID: task.ID, MessageID: msg.ID, Queued: started != task.ID}, nil
}

// next 在 Bot 空下來時開始排隊中的下一件工作：你的訊息優先，其餘依先來後到（LazyBoy next_task）。
// 呼叫時要拿著 dispatchMu。回傳開始的任務 id（沒有開始任何工作時是空白）。
func (r *Runtime) next(botID string) string {
	r.mu.Lock()
	busy := r.active[botID] != nil || r.held[botID] != nil
	r.mu.Unlock()
	if busy {
		return ""
	}
	tasks, err := r.o.DB.ListTasks(context.Background(), botID, true)
	if err != nil {
		r.log.Warn("讀排隊中的工作失敗", "bot", botID, "err", err)
		return ""
	}
	var pick *store.Task
	for i := range tasks {
		t := &tasks[i]
		if t.State != store.TaskQueued {
			continue
		}
		if pick == nil {
			pick = t
		}
		if !t.LowPriority {
			pick = t
			break
		}
	}
	if pick == nil {
		return ""
	}
	r.begin(*pick)
	return pick.ID
}

// kick 在 Bot 空下來時開始下一件排隊中的工作。
func (r *Runtime) kick(botID string) {
	r.dispatchMu.Lock()
	defer r.dispatchMu.Unlock()
	r.next(botID)
}

// begin 讓一件排隊中的工作成為 Bot 正在進行的一輪，在背景開始。呼叫時要拿著 dispatchMu。
func (r *Runtime) begin(task store.Task) {
	runCtx, cancel := context.WithCancel(context.Background())
	t := &turn{botID: task.BotID, threadID: task.ThreadID, taskID: task.ID, source: task.Source, lowPriority: task.LowPriority,
		input: task.Input, orig: task.Input, cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.active[task.BotID] = t
	r.mu.Unlock()
	go r.run(runCtx, t)
}

// run 在背景完成一輪：讀 Bot 與模型、開（或接回）引擎 session（必要時開機）、送出訊息；之後由 consume 處理事件直到 done。
func (r *Runtime) run(ctx context.Context, t *turn) {
	b, err := r.o.DB.GetBot(ctx, t.botID)
	if err == nil && b.Archived {
		err = errors.New("這個 Bot 已封存")
	}
	if err != nil {
		r.finishErr(t, err)
		return
	}
	t.mu.Lock()
	t.botName = b.Name
	t.mu.Unlock()
	th, err := r.o.DB.GetThread(ctx, t.threadID)
	if err != nil {
		r.finishErr(t, err)
		return
	}
	ref, err := r.o.Models.ResolveModel(ctx, b)
	if err == nil && r.o.Budget != nil {
		err = r.o.Budget(ctx, t.botID, ref.Provider)
	}
	if err != nil {
		r.finishErr(t, err)
		return
	}
	t.mu.Lock()
	t.engine, t.provider, t.started = firstNonEmpty(b.Engine, hermes.Name), ref.Provider, time.Now()
	t.mu.Unlock()
	if h := r.hooks(); h.Prompt != nil {
		task, err := r.o.DB.GetTask(ctx, t.taskID)
		if err == nil {
			var p string
			var ok bool
			if p, ok, err = h.Prompt(ctx, task); ok && err == nil {
				t.mu.Lock()
				t.input = p
				t.mu.Unlock()
			}
		}
		if err != nil {
			r.finishErr(t, err)
			return
		}
	}
	// 你問「進度如何」：附上系統查得的事實，Bot 照事實回答，不會只說「我去查」（PRD §19.2，LazyBoy _prompt）。
	t.mu.Lock()
	input := t.input
	t.mu.Unlock()
	if t.source == SourceOwner && judge.IsProgressQuestion(strings.TrimSpace(input)) {
		if p, err := r.Progress(ctx, t.botID, t.taskID); err == nil {
			t.mu.Lock()
			t.input = input + "\n\n" + p.Text
			t.mu.Unlock()
		}
	}
	r.setState(t, store.TaskWaitingBox)
	if r.o.Admit != nil {
		release, err := r.o.Admit(ctx, t.botID, func(w any) {
			// 還在等電腦（主機離線／滿了）：狀態仍是 waiting_box，介面依 wait 顯示「主機離線」或「等電腦空出來」。
			r.emit(t, KindTaskState, map[string]any{"state": store.TaskWaitingBox, "wait": w}, true)
		})
		if err != nil {
			r.finishErr(t, err)
			return
		}
		defer release()
	}
	title := b.Name
	if th.Title != "" {
		title = b.Name + " · " + th.Title
	}
	s, err := r.o.Driver.Start(ctx, engine.StartOptions{BotID: t.botID, ThreadID: t.threadID, Resume: th.EngineSession, Title: title, Model: ref.Model, Mode: b.ApprovalMode})
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
	input = t.input
	t.mu.Unlock()
	r.emit(t, KindMessageCreated, msg, true)
	if stopping {
		r.finish(t, engine.StatusInterrupted, "")
		return
	}
	r.setState(t, store.TaskRunning)
	t.mu.Lock()
	t.seen = time.Now()
	t.mu.Unlock()
	// 送出不跟著這一輪的 ctx：引擎可能在回覆 prompt.submit 之前就把這一輪做完（finish 會取消 ctx），
	// 取消寫到一半的 WebSocket 會把整條連線關掉，下一輪（例：自動續做）就斷線了。停止走 Interrupt。
	if err := r.o.Driver.Send(context.WithoutCancel(ctx), s, engine.Message{Text: input}); err != nil {
		r.finishErr(t, err)
		return
	}
	go r.watch(t, s)
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
	if mine {
		t.seen = time.Now()
		if e.Tool != nil && e.Tool.ID != "" {
			switch e.Type {
			case engine.EventToolCall:
				t.usedTools = true
				if t.openTools == nil {
					t.openTools = map[string]bool{}
				}
				t.openTools[e.Tool.ID] = true
			case engine.EventToolResult:
				delete(t.openTools, e.Tool.ID)
			}
		}
	}
	t.mu.Unlock()
	if !mine {
		return
	}
	// 用量、狀態只是記帳，不代表 Bot 又開始動了：問題還在等你時不能把狀態改回 running
	// （2026-10-19：Hermes 提問後緊接著送 usage，畫面上的提問卡就被收掉，只剩待我處理）。
	if e.Type != engine.EventNeedsApproval && e.Type != engine.EventRequestClosed && e.Type != engine.EventUsage && e.Type != engine.EventStatus {
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
		if left, ok := planLeft(e.Text); ok {
			t.mu.Lock()
			t.plan = left
			t.mu.Unlock()
		}
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
		if e.Quota != nil {
			t.quota = e.Quota
		}
		t.mu.Unlock()
		r.emit(t, KindError, e, true)
		if e.Fatal {
			r.finish(t, engine.StatusError, e.Text)
		}
	case engine.EventDone:
		t.mu.Lock()
		stalled := !t.stalled.IsZero() && !t.stopping
		t.mu.Unlock()
		if stalled {
			// 是看門狗中斷了沒有進度的一輪：算失敗，不是使用者按停止（LazyBoy 同）。
			r.emit(t, KindError, engine.Event{Type: engine.EventError, Text: StalledText}, true)
			r.finish(t, engine.StatusError, StalledText)
			return
		}
		if e.Quota != nil {
			t.mu.Lock()
			t.quota = e.Quota
			t.mu.Unlock()
		}
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
	paused := t.paused
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
	var quotaView *QuotaView
	switch verdict {
	case VerdictAnswer, VerdictCancelled:
		if final == "" {
			final = streamed
		}
	case VerdictFailed:
		if final == "" {
			final = lastErr
		}
		// 額度用完、限流、餘額不足、登入過期（EN-7）：說清楚，開一張換引擎的卡。
		if q := r.quotaOf(t, final); q != nil && text != StalledText {
			quota, said := r.quotaCard(t, q)
			quotaView = &quota
			final = said
		}
		if streamed != "" && final != streamed {
			final = strings.TrimSpace(streamed + "\n\n" + final)
		}
	}
	ctx := context.Background()
	// 判斷做完了沒（X04）：停掉的、中斷的不判斷。
	task, terr := r.o.DB.GetTask(ctx, t.taskID)
	var in judge.Input
	var res *judge.Result
	if terr == nil {
		in, res = r.judgeTurn(t, task, verdict, final, status == engine.StatusError && text == StalledText)
	}
	var judged *Judgment
	if res != nil {
		j := judgmentOf(*res)
		judged = &j
	}
	if msgID != "" {
		fields := map[string]any{"task_id": t.taskID, "status": status}
		if paused && verdict == VerdictCancelled {
			fields["paused"] = true // 你接手了電腦：這一輪先停，交還時接著做（B14）
		}
		if h := r.hooks(); h.Silent != nil && h.Silent(t.taskID, verdict, final) {
			fields["silent"] = true // 隊友、群組裡回 [silent] 的：不顯示（LazyBoy finish）
		}
		if judged != nil {
			fields["judge"] = judged
		}
		if quotaView != nil {
			fields["quota"] = quotaView
		}
		meta, _ := json.Marshal(fields)
		if err := r.o.DB.UpdateMessage(ctx, msgID, final, nil, meta); err != nil {
			r.log.Warn("寫入回覆失敗", "message", msgID, "err", err)
		}
	} else if final != "" {
		// 還沒開始就失敗：把原因寫成 Bot 的一則訊息，對話裡看得到。
		fields := map[string]any{"task_id": t.taskID, "status": status}
		if judged != nil {
			fields["judge"] = judged
		}
		if quotaView != nil {
			fields["quota"] = quotaView
		}
		meta, _ := json.Marshal(fields)
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
	// 只說要做、丟回來、計畫沒做完：叫 Bot 自己接著做（排在這一輪後面；LazyBoy auto_continue）。
	if res != nil {
		if req := r.continuation(t, task, in, *res); req != nil {
			judged.ContinuedBy = r.continueTurn(t, in, req)
		}
		if err := r.o.DB.PatchTaskMeta(ctx, t.taskID, map[string]any{"judge": judged}); err != nil {
			r.log.Warn("寫入完成判定失敗", "task", t.taskID, "err", err)
		}
	}
	if quotaView == nil {
		r.notifyDone(t, verdict, final, res, judged) // 額度卡已經是通知
	}
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
	r.emit(t, KindDone, DonePayload{MessageID: msgID, Status: status, Verdict: verdict, Text: final, Judge: judged, Quota: quotaView}, true)
	r.finished(t.taskID, verdict, final)
	t.cancel()
	close(t.done)
	r.mu.Lock()
	delete(r.closing, t.taskID)
	r.mu.Unlock()
	r.kick(t.botID)
}

// finished 呼叫協作層的 Finished（回覆送回、貼到群組）。
func (r *Runtime) finished(taskID, verdict, text string) {
	h := r.hooks()
	if h.Finished == nil {
		return
	}
	task, err := r.o.DB.GetTask(context.Background(), taskID)
	if err != nil {
		r.log.Warn("讀結束的工作失敗", "task", taskID, "err", err)
		return
	}
	h.Finished(task, verdict, text)
}

func (r *Runtime) setState(t *turn, state string) {
	if err := r.o.DB.SetTaskState(context.Background(), t.taskID, state, ""); err != nil {
		r.log.Warn("寫入任務狀態失敗", "task", t.taskID, "err", err)
	}
	r.emit(t, KindTaskState, map[string]string{"state": state}, true)
}

// emit 發布一個事件；persist 時先寫進 event 表，帶著流水號發布。
func (r *Runtime) emit(t *turn, kind string, payload any, persist bool) {
	r.publish(t.botID, t.threadID, t.taskID, kind, payload, persist)
}

// publish 發布一個 Bot 的事件；persist 時先寫進 event 表，帶著流水號發布。
func (r *Runtime) publish(botID, threadID, taskID, kind string, payload any, persist bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		r.log.Error("事件無法轉成 JSON", "kind", kind, "err", err)
		return
	}
	e := events.Event{Kind: kind, BotID: botID, ThreadID: threadID, TaskID: taskID, Payload: raw, At: time.Now().UnixMilli()}
	if persist {
		row, err := r.o.DB.AppendEvent(context.Background(), store.Event{BotID: botID, ThreadID: threadID, TaskID: taskID, Kind: kind, Payload: raw})
		if err != nil {
			r.log.Warn("寫入事件失敗", "kind", kind, "err", err)
		} else {
			e.ID, e.At = row.ID, row.CreatedAt
		}
	}
	r.o.Bus.Publish(e)
}

// Stop 停止 Bot 的工作（threadID 不是空白時，只停那條對話串的）：正在進行的一輪，以及排隊中的工作
// （LazyBoy stop）。沒有東西可停時回傳 false。停止後仍會收到 verdict=cancelled 的 chat.done。
// 停下的工作派給其他 Bot 的子工作也會一起停（停止連鎖，LP-6，由 Hooks.Stopped 處理）。
func (r *Runtime) Stop(ctx context.Context, botID, threadID string) (bool, error) {
	var stopped []string
	tasks, err := r.o.DB.ListTasks(ctx, botID, true)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.State == store.TaskQueued && (threadID == "" || task.ThreadID == threadID) && r.cancelQueued(task) {
			stopped = append(stopped, task.ID)
		}
	}
	r.mu.Lock()
	t := r.active[botID]
	r.mu.Unlock()
	var stopErr error
	if t != nil && (threadID == "" || t.threadID == threadID) {
		var ok bool
		if ok, stopErr = r.stopTurn(ctx, t); ok {
			stopped = append(stopped, t.taskID)
		}
	}
	if h := r.hooks(); h.Stopped != nil && len(stopped) > 0 {
		h.Stopped(botID, stopped)
	}
	return len(stopped) > 0, stopErr
}

// StopTask 只停一件工作（隊友交辦的、群組座位的一輪），不碰 Bot 的其他工作（LazyBoy stop_task）。
func (r *Runtime) StopTask(ctx context.Context, taskID string) (bool, error) {
	task, err := r.o.DB.GetTask(ctx, taskID)
	if err != nil {
		return false, err
	}
	if task.State == store.TaskDone {
		return false, nil
	}
	if task.State == store.TaskQueued && r.cancelQueued(task) {
		return true, nil
	}
	r.mu.Lock()
	t := r.active[task.BotID]
	r.mu.Unlock()
	if t == nil || t.taskID != taskID {
		return false, nil
	}
	return r.stopTurn(ctx, t)
}

// cancelQueued 取消一件還在排隊的工作；已經開始了就不動。
func (r *Runtime) cancelQueued(task store.Task) bool {
	r.dispatchMu.Lock()
	ok, err := r.o.DB.SetTaskCancelledIfQueued(context.Background(), task.ID)
	r.dispatchMu.Unlock()
	if err != nil {
		r.log.Warn("取消排隊中的工作失敗", "task", task.ID, "err", err)
		return false
	}
	if !ok {
		return false
	}
	r.publish(task.BotID, task.ThreadID, task.ID, KindTaskState, map[string]string{"state": store.TaskDone, "verdict": VerdictCancelled}, true)
	r.publish(task.BotID, task.ThreadID, task.ID, KindDone, map[string]string{"status": engine.StatusInterrupted, "verdict": VerdictCancelled}, true)
	r.finished(task.ID, VerdictCancelled, "")
	return true
}

// stopTurn 停止進行中的一輪。
func (r *Runtime) stopTurn(ctx context.Context, t *turn) (bool, error) {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return false, nil
	}
	already := t.stopping
	t.stopping = true
	if !already {
		t.stopAt = time.Now()
	}
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

// Busy 回答 Bot 是不是正在做事；lowPriority 表示正在做的是 Bot 交辦或群組接力（你的訊息會排在它後面第一個）。
func (r *Runtime) Busy(botID string) (busy, lowPriority bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.active[botID]; t != nil {
		return true, t.lowPriority
	}
	return false, false
}

// VerdictInterrupted 是服務重開時還沒做完、也沒有自動接著做的工作（LazyBoy interrupted；見 restart.go）：
// 等它的 Bot 會收到「做到一半被中斷」，對話裡有「接著做」。
const VerdictInterrupted = "interrupted"

// RecoverTasks 在服務啟動時（接上 Hooks 之後）處理上次沒做完的工作，並開始排隊中的工作（AA-10）：
// 你的、隊友交辦的接著做（同一件工作重新排隊，restart.go）；其餘收掉。
func (r *Runtime) RecoverTasks(ctx context.Context) error {
	tasks, err := r.o.DB.ListOpenTasks(ctx)
	if err != nil {
		return err
	}
	bots := map[string]bool{}
	for _, task := range tasks {
		r.mu.Lock()
		running := r.active[task.BotID] != nil && r.active[task.BotID].taskID == task.ID
		r.mu.Unlock()
		if running {
			continue
		}
		if task.State != store.TaskQueued && !r.requeue(ctx, task) {
			verdict := VerdictInterrupted
			if task.State == store.TaskStopping {
				verdict = VerdictCancelled // 你按了停止、還沒停好服務就重開了：照停止收尾
			}
			if err := r.o.DB.SetTaskState(ctx, task.ID, store.TaskDone, verdict); err != nil {
				return err
			}
			r.closeReply(ctx, task, verdict)
			r.publish(task.BotID, task.ThreadID, task.ID, KindTaskState, map[string]string{"state": store.TaskDone, "verdict": verdict}, true)
			r.finished(task.ID, verdict, "")
		}
		bots[task.BotID] = true
	}
	for id := range bots {
		r.kick(id)
	}
	return nil
}

// watch 是一輪的看門狗（CH-12，照 LazyBoy runtime.watch()）：太久沒有任何事件（有工具在跑時給長一點）就送中斷；
// 送出中斷或按了停止之後，引擎過了 Grace 還沒結束這一輪，就直接結束，不會永遠卡著。等人回覆的時間不算。
func (r *Runtime) watch(t *turn, s engine.Session) {
	w := r.o.Watchdog
	tick := time.NewTicker(w.Interval)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tick.C:
		}
		now := time.Now()
		t.mu.Lock()
		if t.finished {
			t.mu.Unlock()
			return
		}
		if t.waiting {
			t.seen = now
			t.mu.Unlock()
			continue
		}
		if t.stopping || !t.stalled.IsZero() {
			since := t.stalled
			if t.stopping && !t.stopAt.IsZero() && (since.IsZero() || t.stopAt.Before(since)) {
				since = t.stopAt
			}
			stopping := t.stopping
			t.mu.Unlock()
			if now.Sub(since) < w.Grace {
				continue
			}
			if stopping {
				r.log.Warn("停止後引擎沒有結束這一輪，直接結束", "bot", t.botID, "task", t.taskID)
				r.finish(t, engine.StatusInterrupted, "")
			} else {
				r.log.Warn("中斷後引擎沒有結束這一輪，直接結束", "bot", t.botID, "task", t.taskID)
				r.emit(t, KindError, engine.Event{Type: engine.EventError, Text: StalledText}, true)
				r.finish(t, engine.StatusError, StalledText)
			}
			return
		}
		limit := w.Idle
		if len(t.openTools) > 0 {
			limit = w.Tool
		}
		idle := now.Sub(t.seen)
		if idle < limit {
			t.mu.Unlock()
			continue
		}
		t.stalled = now
		t.mu.Unlock()
		r.log.Warn("這一輪太久沒有進度，送出中斷", "bot", t.botID, "task", t.taskID, "idle", idle.Round(time.Second))
		r.emit(t, KindStatus, engine.Event{Type: engine.EventStatus, Text: "Bot 太久沒有回應，正在停止這次工作"}, true)
		ctx, cancel := context.WithTimeout(context.Background(), w.Grace)
		if err := r.o.Driver.Interrupt(ctx, s); err != nil {
			r.log.Warn("看門狗中斷失敗", "bot", t.botID, "task", t.taskID, "err", err)
		}
		cancel()
	}
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

// Wait 等任務結束（或 ctx 結束），回傳任務的最後狀態。排隊中的工作會等它開始、做完。
// 不在進行中、也還沒結束的工作（例：服務重開前留下的）會一直等到 ctx 結束。
func (r *Runtime) Wait(ctx context.Context, taskID string) (store.Task, error) {
	for {
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
			return r.o.DB.GetTask(ctx, taskID)
		}
		task, err := r.o.DB.GetTask(ctx, taskID)
		if err != nil || task.State == store.TaskDone {
			return task, err
		}
		// 排隊中，或剛好在上面查完之後才開始（還不在 active 裡就已經不是 queued）：再查一次。
		select {
		case <-ctx.Done():
			return store.Task{}, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// nameThread 在背景請快判幫新對話串取標題（§20 用途 10）；標題還是原本的（你沒改過）才換。
func (r *Runtime) nameThread(threadID, botID, text, was string) {
	ctx, cancel := context.WithTimeout(context.Background(), limits.JudgeWaitTitle+time.Second)
	defer cancel()
	name, ok := r.o.Namer(ctx, text, botID)
	name = strings.TrimSpace(name)
	if !ok || name == "" || name == was {
		return
	}
	th, err := r.o.DB.GetThread(ctx, threadID)
	if err != nil || th.Title != was {
		return
	}
	th.Title = name
	if err := r.o.DB.UpdateThread(ctx, th); err != nil {
		r.log.Warn("快判命名對話串失敗", "thread", threadID, "err", err)
	}
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
