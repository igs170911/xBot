// Package chat 是對話的操作（PRD §6.1 CH-*、§22.3 chat）：send、stop、read、list_threads、wait_until_settled，
// 以及回覆 Bot 的提問（respond、ask_again，C12）和讀回工作步驟（steps）。
// 真正的執行在 internal/runtime；這裡負責權限、資源範圍、輸入檢查與把結果整理給代理看。
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// Runtime 是對話執行層；*runtime.Runtime 符合。
type Runtime interface {
	Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error)
	Stop(ctx context.Context, botID, threadID string) (bool, error)
	Active(botID string) (threadID, taskID string)
	Busy(botID string) (busy, lowPriority bool)
	Bus() *events.Bus
	Respond(ctx context.Context, itemID string, a runtime.Answer, by string) (store.InboxItem, error)
	AskAgain(ctx context.Context, itemID string) (runtime.ChatResult, store.InboxItem, error)
}

// Deps 是對話操作需要的服務元件。Runtime 是 nil 時（指令列只產生說明）呼叫會回 internal。
type Deps struct {
	Runtime Runtime
}

// ── 輸入輸出 ──────────────────────────────────────────

// SendIn 是 xbot.chat.send 的輸入。
type SendIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"要對哪個 Bot 說"`
	Text     string `json:"text" required:"true" max:"64000" desc:"訊息內容"`
	ThreadID string `json:"thread_id,omitempty" max:"64" desc:"接續的對話串；不填開一條新的"`
}

// SendOut 是 xbot.chat.send 的輸出。訊息收下就回傳；用 xbot.chat.wait_until_settled 等回覆。
type SendOut struct {
	BotID     string `json:"bot_id"`
	ThreadID  string `json:"thread_id" desc:"預演且沒給 thread_id 時是空白（會開新的對話串）"`
	TaskID    string `json:"task_id" desc:"這件工作的 id，交給 wait_until_settled"`
	MessageID string `json:"message_id" desc:"你這則訊息的 id"`
	Queued    bool   `json:"queued,omitempty" desc:"Bot 正在做隊友交辦或群組的事，你的訊息排在第一個，做完那件就開始"`
}

// StopIn 是 xbot.chat.stop 的輸入。
type StopIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	ThreadID string `json:"thread_id,omitempty" max:"64" desc:"只停這條對話串的工作；不填停 Bot 正在做的"`
}

// StopOut 是 xbot.chat.stop 的輸出。
type StopOut struct {
	Stopped bool   `json:"stopped" desc:"false 表示本來就沒有在做事（預演時表示會不會停到東西）"`
	TaskID  string `json:"task_id,omitempty"`
}

// ReadIn 是 xbot.chat.read 的輸入。
type ReadIn struct {
	ThreadID string `json:"thread_id" required:"true" max:"64"`
	Before   int64  `json:"before,omitempty" min:"0" desc:"往前翻頁：只讀 seq 比它小的訊息（上一頁的 next_before）"`
	Limit    int    `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾則；預設 50"`
}

// Message 是對外的一則訊息。
type Message struct {
	ID      string `json:"id"`
	Seq     int64  `json:"seq"`
	Sender  string `json:"sender" desc:"owner（你）、bot:<id>、system"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	TaskID  string `json:"task_id,omitempty"`
	// Actor／ActorName：代理透過 MCP 等出口送的訊息，記下是哪個代理（MCP-13）；你自己送的是空白。
	Actor     string `json:"actor,omitempty" desc:"代理送的訊息：agent:<id>"`
	ActorName string `json:"actor_name,omitempty" desc:"代理的名字，例：Claude Code"`
	Via       string `json:"via,omitempty" desc:"從哪裡送的：mcp、http、cli"`
	Streaming bool   `json:"streaming,omitempty" desc:"Bot 還在寫這則回覆"`
	Status    string `json:"status,omitempty" desc:"Bot 回覆的結果：complete、interrupted、error"`
	// Silent：隊友交辦或群組裡 Bot 回 [silent]（沒有要補充），畫面不顯示。
	Silent bool `json:"silent,omitempty"`
	// Peer：kind=peer 時這一行是什麼（已傳訊息給…、來自…的訊息、開了群組…，X02）。
	Peer      *PeerLine `json:"peer,omitempty"`
	CreatedAt int64     `json:"created_at"`
	UpdatedAt int64     `json:"updated_at"`
}

// ReadOut 是 xbot.chat.read 的輸出。
type ReadOut struct {
	Thread     Thread    `json:"thread"`
	Messages   []Message `json:"messages" desc:"由舊到新"`
	HasMore    bool      `json:"has_more" desc:"還有更早的訊息"`
	NextBefore int64     `json:"next_before,omitempty" desc:"讀更早一頁時帶的 before"`
}

// Thread 是對外的對話串。
type Thread struct {
	ID            string `json:"id"`
	BotID         string `json:"bot_id"`
	Title         string `json:"title"`
	Pinned        bool   `json:"pinned"`
	Archived      bool   `json:"archived"`
	Unread        int    `json:"unread"`
	LastMessageAt int64  `json:"last_message_at"`
	CreatedAt     int64  `json:"created_at"`
}

// ListThreadsIn 是 xbot.chat.list_threads 的輸入。
type ListThreadsIn struct {
	BotID           string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾條；預設 50"`
}

// ListThreadsOut 是 xbot.chat.list_threads 的輸出。
type ListThreadsOut struct {
	Threads []Thread `json:"threads" desc:"釘選的在前，其餘新到舊"`
}

// WaitIn 是 xbot.chat.wait_until_settled 的輸入。
type WaitIn struct {
	TaskID   string `json:"task_id" required:"true" max:"64" desc:"xbot.chat.send 回的 task_id"`
	TimeoutS int    `json:"timeout_s,omitempty" min:"1" max:"300" desc:"最多等幾秒；預設 60"`
}

// Request 是 Bot 在等人回覆的內容。
type Request struct {
	Kind    string   `json:"kind" desc:"approval（核准）、clarify（補充問題）、secret（密碼、驗證碼）…"`
	Prompt  string   `json:"prompt"`
	Choices []string `json:"choices,omitempty"`
	InboxID string   `json:"inbox_id,omitempty" desc:"這個問題的待我處理卡；用 xbot.chat.respond 回覆"`
}

// WaitOut 是 xbot.chat.wait_until_settled 的輸出。
type WaitOut struct {
	TaskID   string `json:"task_id"`
	BotID    string `json:"bot_id"`
	ThreadID string `json:"thread_id"`
	State    string `json:"state" desc:"任務狀態：queued、running、waiting_input、waiting_approval、waiting_box、stopping、done"`
	Verdict  string `json:"verdict,omitempty" desc:"done 時的結果：answer、cancelled、failed"`
	// Settled 為 false 表示等到逾時了，Bot 還在做；可以再呼叫一次。
	Settled       bool     `json:"settled" desc:"true：完成了，或在等人；false：等到逾時，還在做"`
	NeedsPerson   bool     `json:"needs_person" desc:"Bot 在等人回覆（核准、補充問題）"`
	Request       *Request `json:"request,omitempty" desc:"needs_person 時 Bot 問的內容"`
	Text          string   `json:"text" desc:"Bot 這次的回覆（目前為止）"`
	MessageID     string   `json:"message_id,omitempty" desc:"Bot 回覆那則訊息的 id"`
	WaitedSeconds float64  `json:"waited_s"`
}

// RespondIn 是 xbot.chat.respond 的輸入。
type RespondIn struct {
	InboxID string            `json:"inbox_id,omitempty" max:"64" desc:"待我處理卡的 id（chat.needs_approval 事件或 wait_until_settled 的 request.inbox_id）"`
	TaskID  string            `json:"task_id,omitempty" max:"64" desc:"或：這件工作目前在等的那個問題"`
	Choice  string            `json:"choice,omitempty" enum:"once,always,deny" desc:"核准卡：once 允許這次、always 一律允許（卡片有給才行）、deny 拒絕"`
	Text    string            `json:"text,omitempty" max:"64000" desc:"提問的回答（點選項就送選項的文字）"`
	Answers []string          `json:"answers,omitempty" max:"20" desc:"多個問題時逐題的回答（依題目順序）"`
	Value   string            `json:"value,omitempty" max:"16384" secret:"true" desc:"密碼、金鑰、驗證碼；只交給 Bot 的電腦，不寫進紀錄"`
	Values  map[string]string `json:"values,omitempty" secret:"true" desc:"多欄位的密碼類輸入（例：identifier、password）"`
	Decline bool              `json:"decline,omitempty" desc:"不回答：核准＝拒絕、提問＝略過、密碼＝不提供"`
}

// RespondOut 是 xbot.chat.respond 的輸出。
type RespondOut struct {
	InboxID    string `json:"inbox_id"`
	TaskID     string `json:"task_id"`
	ThreadID   string `json:"thread_id"`
	Status     string `json:"status" desc:"卡片狀態：resolved（已交給 Bot）；預演時是 open"`
	Resolution string `json:"resolution,omitempty" desc:"once、always、deny、answered、provided、declined"`
}

// AskAgainIn 是 xbot.chat.ask_again 的輸入。
type AskAgainIn struct {
	InboxID string `json:"inbox_id" required:"true" max:"64" desc:"過期的那張提問卡"`
}

// StepsIn 是 xbot.chat.steps 的輸入。
type StepsIn struct {
	ThreadID string `json:"thread_id" required:"true" max:"64"`
	TaskID   string `json:"task_id,omitempty" max:"64" desc:"只讀這件工作的"`
	After    int64  `json:"after,omitempty" min:"0" desc:"只讀 id 比它大的（上一頁的 next_after）"`
	Limit    int    `json:"limit,omitempty" min:"1" max:"500" desc:"最多幾筆；預設 500"`
}

// StepEvent 是一筆工作步驟事件（和 SSE 的事件同一個格式）。
type StepEvent struct {
	ID       int64           `json:"id"`
	Kind     string          `json:"kind" desc:"chat.tool_call、chat.tool_result、chat.needs_approval、chat.answered、chat.error、task.state"`
	BotID    string          `json:"bot_id,omitempty"`
	ThreadID string          `json:"thread_id,omitempty"`
	TaskID   string          `json:"task_id,omitempty"`
	Payload  json.RawMessage `json:"payload"`
	At       int64           `json:"at"`
}

// StepsOut 是 xbot.chat.steps 的輸出。
type StepsOut struct {
	Events    []StepEvent `json:"events" desc:"由舊到新"`
	HasMore   bool        `json:"has_more"`
	NextAfter int64       `json:"next_after,omitempty"`
}

// stepKinds 是 chat.steps 回的事件種類：重畫工作步驟需要的，不含訊息（chat.read 有）和用量。
var stepKinds = []string{runtime.KindToolCall, runtime.KindToolResult, runtime.KindNeedsApproval, runtime.KindAnswered,
	runtime.KindError, runtime.KindTaskState}

// Register 登錄對話操作。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[SendIn, SendOut]{
		Name: "xbot.chat.send", Summary: "對一個 Bot 說話（交辦一件事）",
		Description: "訊息收下就回傳 thread_id、task_id；Bot 在背景做事（第一次會先開機），用 xbot.chat.wait_until_settled 等回覆。" +
			"Bot 正在忙你交辦的另一件事時回 conflict（retryable），等它做完或先 xbot.chat.stop；" +
			"正在做隊友交辦或群組的事時，你的訊息排在第一個（queued=true），做完那件就開始（LP-1）。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.send,
	})
	ops.Register(reg, ops.Op[StopIn, StopOut]{
		Name: "xbot.chat.stop", Summary: "停止 Bot 正在做的事",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.stop,
	})
	ops.Register(reg, ops.Op[ReadIn, ReadOut]{
		Name: "xbot.chat.read", Summary: "讀一條對話串的訊息",
		Description: "預設回最新的 50 則（由舊到新）；has_more 時用 next_before 往前翻。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: read,
	})
	ops.Register(reg, ops.Op[ListThreadsIn, ListThreadsOut]{
		Name: "xbot.chat.list_threads", Summary: "列出和一個 Bot 的對話串",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: listThreads,
	})
	ops.Register(reg, ops.Op[RespondIn, RespondOut]{
		Name: "xbot.chat.respond", Summary: "回覆 Bot 的提問（允許指令、回答問題、提供密碼）",
		Description: "Bot 在等人時（needs_person）用它回覆；對話裡和待我處理用的是同一個操作。inbox_id 或 task_id 擇一。" +
			"核准卡用 choice（once／always／deny）；提問用 text（多題用 answers）；密碼類用 value（帳號＋密碼用 values），" +
			"值只交給 Bot 的電腦，不寫進紀錄。decline=true 表示不回答（拒絕、略過、不提供）。目前只有你本人能回覆。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.respond,
	})
	ops.Register(reg, ops.Op[AskAgainIn, SendOut]{
		Name: "xbot.chat.ask_again", Summary: "請 Bot 把過期的問題再問一次",
		Description: "無人在場的提問 10 分鐘沒回會過期；之後用它在原本的對話串請 Bot 再問一次。Bot 正在忙時回 conflict（retryable）。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.askAgain,
	})
	ops.Register(reg, ops.Op[StepsIn, StepsOut]{
		Name: "xbot.chat.steps", Summary: "讀一條對話串的工作步驟（工具、提問、回覆、狀態）",
		Description: "重新整理後用它把工作步驟補回來（chat.read 只有訊息）。事件和 SSE 的格式一樣；has_more 時用 next_after 往後讀。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: steps,
	})
	ops.Register(reg, ops.Op[WaitIn, WaitOut]{
		Name: "xbot.chat.wait_until_settled", Summary: "等 Bot 做完（或需要人）再回傳它的回覆",
		Description: "最多等 timeout_s 秒（上限 300）。settled=true 表示做完了，或在等人回覆（needs_person）；" +
			"settled=false 表示逾時、還在做，可以再呼叫一次。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: d.wait,
	})
}

func (d Deps) runtime() (Runtime, error) {
	if d.Runtime == nil {
		return nil, ops.Internal(errors.New("對話執行層沒有啟動"))
	}
	return d.Runtime, nil
}

// thread 讀對話串並確認呼叫者能碰它的 Bot。
func thread(ctx context.Context, call *ops.Call, id string) (store.Thread, error) {
	th, err := call.DB.GetThread(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return th, ops.NotFound("找不到這條對話串")
	}
	if err != nil {
		return th, err
	}
	if !call.Identity.CanAccess(ops.ResourceBot, th.BotID) {
		return th, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這條對話串的 Bot")
	}
	return th, nil
}

func (d Deps) send(ctx context.Context, call *ops.Call, in SendIn) (SendOut, error) {
	rt, err := d.runtime()
	if err != nil {
		return SendOut{}, err
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return SendOut{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: "不能是空的"})
	}
	b, err := call.DB.GetBot(ctx, in.BotID)
	if errors.Is(err, store.ErrNotFound) {
		return SendOut{}, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return SendOut{}, err
	}
	if b.Archived {
		return SendOut{}, ops.Conflict("這個 Bot 已封存，先取消封存（xbot.bots.archive --archived=false）才能對話")
	}
	if in.ThreadID != "" {
		th, err := thread(ctx, call, in.ThreadID)
		if err != nil {
			return SendOut{}, err
		}
		if th.BotID != b.ID {
			return SendOut{}, ops.NotFound("這條對話串不屬於這個 Bot")
		}
	}
	if call.DryRun {
		if isBusy, low := rt.Busy(b.ID); isBusy && !low {
			return SendOut{}, busy()
		}
		return SendOut{BotID: b.ID, ThreadID: in.ThreadID}, nil
	}
	id := call.Identity
	res, err := rt.Start(ctx, runtime.ChatRequest{BotID: b.ID, ThreadID: in.ThreadID, Text: text,
		Actor: id.Actor(), ActorName: id.DisplayName(), Via: id.Via})
	var over *proxy.BudgetError
	switch {
	case errors.Is(err, runtime.ErrBusy):
		return SendOut{}, busy()
	case errors.As(err, &over):
		return SendOut{}, ops.Budget(over.Error())
	case errors.Is(err, runtime.ErrNoModel):
		return SendOut{}, ops.Conflict("還沒設定模型：請先用 xbot.models.set_provider 設定模型供應商與預設模型")
	case err != nil:
		return SendOut{}, err
	}
	return SendOut{BotID: b.ID, ThreadID: res.ThreadID, TaskID: res.TaskID, MessageID: res.MessageID, Queued: res.Queued}, nil
}

func busy() *ops.Error {
	e := ops.Conflict("這個 Bot 正在忙另一件事：等它做完（xbot.chat.wait_until_settled），或先停止（xbot.chat.stop）")
	e.Retryable = true
	return e
}

func (d Deps) stop(ctx context.Context, call *ops.Call, in StopIn) (StopOut, error) {
	rt, err := d.runtime()
	if err != nil {
		return StopOut{}, err
	}
	if _, err := call.DB.GetBot(ctx, in.BotID); errors.Is(err, store.ErrNotFound) {
		return StopOut{}, ops.NotFound("找不到這個 Bot")
	} else if err != nil {
		return StopOut{}, err
	}
	activeThread, task := rt.Active(in.BotID)
	if call.DryRun {
		would := task != "" && (in.ThreadID == "" || in.ThreadID == activeThread)
		if !would {
			task = ""
		}
		return StopOut{Stopped: would, TaskID: task}, nil
	}
	stopped, err := rt.Stop(ctx, in.BotID, in.ThreadID)
	if err != nil {
		return StopOut{}, err
	}
	if !stopped {
		task = ""
	}
	return StopOut{Stopped: stopped, TaskID: task}, nil
}

func read(ctx context.Context, call *ops.Call, in ReadIn) (ReadOut, error) {
	th, err := thread(ctx, call, in.ThreadID)
	if err != nil {
		return ReadOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = limits.PageDefault
	}
	// 多讀一則，判斷前面還有沒有。
	msgs, err := call.DB.ListMessages(ctx, store.ScopeThread, th.ID, in.Before, limit+1)
	if err != nil {
		return ReadOut{}, err
	}
	out := ReadOut{Thread: threadView(th), Messages: []Message{}}
	if len(msgs) > limit {
		msgs = msgs[1:]
		out.HasMore = true
	}
	for _, m := range msgs {
		out.Messages = append(out.Messages, messageView(m))
	}
	if out.HasMore {
		out.NextBefore = msgs[0].Seq
	}
	return out, nil
}

func listThreads(ctx context.Context, call *ops.Call, in ListThreadsIn) (ListThreadsOut, error) {
	if _, err := call.DB.GetBot(ctx, in.BotID); errors.Is(err, store.ErrNotFound) {
		return ListThreadsOut{}, ops.NotFound("找不到這個 Bot")
	} else if err != nil {
		return ListThreadsOut{}, err
	}
	ths, err := call.DB.ListThreads(ctx, in.BotID, in.IncludeArchived)
	if err != nil {
		return ListThreadsOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = limits.PageDefault
	}
	out := ListThreadsOut{Threads: []Thread{}}
	for i, th := range ths {
		if i == limit {
			break
		}
		out.Threads = append(out.Threads, threadView(th))
	}
	return out, nil
}

func threadView(t store.Thread) Thread {
	return Thread{ID: t.ID, BotID: t.BotID, Title: t.Title, Pinned: t.Pinned, Archived: t.Archived, Unread: t.Unread,
		LastMessageAt: t.LastMessageAt, CreatedAt: t.CreatedAt}
}

// PeerLine 是對話裡和隊友往來的那一行（C13、C14）。
type PeerLine struct {
	Event     string `json:"event" desc:"message_out（已傳訊息給）、message_in（來自…的訊息）、group_created、group_post"`
	PeerID    string `json:"peer_id,omitempty"`
	PeerName  string `json:"peer_name,omitempty"`
	Intent    string `json:"intent,omitempty" desc:"request、question、result、fyi"`
	MessageID string `json:"message_id,omitempty"`
	Hop       int    `json:"hop,omitempty" desc:"這條往來的第幾次"`
	Chain     string `json:"chain,omitempty" desc:"往來的 id；用 xbot.collab.peer_history 讀整條"`
	RoomID    string `json:"room_id,omitempty"`
	Files     int    `json:"files,omitempty" desc:"帶了幾個檔案"`
}

type messageMeta struct {
	TaskID    string    `json:"task_id"`
	Actor     string    `json:"actor"`
	ActorName string    `json:"actor_name"`
	Via       string    `json:"via"`
	Streaming bool      `json:"streaming"`
	Status    string    `json:"status"`
	Silent    bool      `json:"silent"`
	Peer      *PeerLine `json:"peer"`
}

func messageView(m store.Message) Message {
	var meta messageMeta
	_ = json.Unmarshal(m.Meta, &meta)
	return Message{ID: m.ID, Seq: m.Seq, Sender: m.Sender, Kind: m.Kind, Content: m.Content, TaskID: meta.TaskID,
		Actor: meta.Actor, ActorName: meta.ActorName, Via: meta.Via, Streaming: meta.Streaming, Status: meta.Status, Silent: meta.Silent, Peer: meta.Peer,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

// settled：做完了，或在等人。
func settled(state string) bool {
	return state == store.TaskDone || state == store.TaskWaitingInput || state == store.TaskWaitingApproval
}

func (d Deps) wait(ctx context.Context, call *ops.Call, in WaitIn) (WaitOut, error) {
	start := time.Now()
	task, err := call.DB.GetTask(ctx, in.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return WaitOut{}, ops.NotFound("找不到這件工作")
	}
	if err != nil {
		return WaitOut{}, err
	}
	if !call.Identity.CanAccess(ops.ResourceBot, task.BotID) {
		return WaitOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這件工作的 Bot")
	}
	timeout := limits.ChatWaitDefault
	if in.TimeoutS > 0 {
		timeout = min(time.Duration(in.TimeoutS)*time.Second, limits.ChatWaitMax)
	}
	if !settled(task.State) && !call.DryRun {
		rt, err := d.runtime()
		if err != nil {
			return WaitOut{}, err
		}
		// 先訂閱再讀一次資料庫，避免漏掉剛好在中間發生的變化。
		sub := rt.Bus().Subscribe(events.ForTask(task.ID), 64)
		defer sub.Close()
		deadline := time.NewTimer(timeout)
		defer deadline.Stop()
		recheck := time.NewTicker(limits.WaitRecheck)
		defer recheck.Stop()
	loop:
		for {
			if task, err = call.DB.GetTask(ctx, in.TaskID); err != nil {
				return WaitOut{}, err
			}
			if settled(task.State) {
				break
			}
			select {
			case <-ctx.Done():
				return WaitOut{}, ctx.Err()
			case <-deadline.C:
				if task, err = call.DB.GetTask(ctx, in.TaskID); err != nil {
					return WaitOut{}, err
				}
				break loop
			case <-sub.C:
			case <-recheck.C:
			}
		}
	}
	out := WaitOut{TaskID: task.ID, BotID: task.BotID, ThreadID: task.ThreadID, State: task.State, Verdict: task.Verdict,
		Settled: settled(task.State), WaitedSeconds: time.Since(start).Round(time.Millisecond).Seconds()}
	out.NeedsPerson = task.State == store.TaskWaitingInput || task.State == store.TaskWaitingApproval
	if out.NeedsPerson {
		if e, err := call.DB.LatestTaskEvent(ctx, task.BotID, task.ID, runtime.KindNeedsApproval); err == nil {
			var ev runtime.NeedsPayload
			if json.Unmarshal(e.Payload, &ev) == nil && ev.Request != nil {
				out.Request = &Request{Kind: ev.Request.Kind, Prompt: ev.Request.Prompt, Choices: ev.Request.Choices, InboxID: ev.InboxID}
			}
		}
	}
	if task.ThreadID != "" {
		msgs, err := call.DB.ListMessages(ctx, store.ScopeThread, task.ThreadID, 0, limits.PageDefault)
		if err != nil {
			return out, err
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if !strings.HasPrefix(m.Sender, "bot:") || m.Kind != "text" { // Bot 發的卡片（C11）不是回覆
				continue
			}
			if v := messageView(m); v.TaskID == task.ID {
				out.Text, out.MessageID = v.Content, v.ID
				break
			}
		}
	}
	return out, nil
}

// ── 回覆 Bot 的提問（C12）─────────────────────────────

func (d Deps) respond(ctx context.Context, call *ops.Call, in RespondIn) (RespondOut, error) {
	// SEC-8／§18：只有你本人能回答 Bot 的提問、允許它的指令；代為處理（MCP-10）之後才開放給有授權的代理。
	if call.Identity.Kind != ops.KindOwner {
		return RespondOut{}, ops.Forbidden("只有你本人能回覆 Bot 的提問")
	}
	rt, err := d.runtime()
	if err != nil {
		return RespondOut{}, err
	}
	it, err := findCard(ctx, call, in.InboxID, in.TaskID)
	if err != nil {
		return RespondOut{}, err
	}
	out := RespondOut{InboxID: it.ID, TaskID: it.TaskID, ThreadID: it.ThreadID, Status: it.Status}
	if call.DryRun {
		if it.Status != store.InboxOpen {
			return out, ops.Conflict(runtime.ErrAnswered.Error())
		}
		return out, nil
	}
	a := runtime.Answer{Choice: in.Choice, Text: in.Text, Answers: in.Answers, Value: in.Value, Values: in.Values, Decline: in.Decline}
	got, err := rt.Respond(ctx, it.ID, a, call.Identity.Actor())
	var bad *runtime.InvalidAnswer
	switch {
	case errors.As(err, &bad):
		return out, ops.InvalidInput(ops.FieldError{Field: bad.Field, Message: bad.Message})
	case errors.Is(err, runtime.ErrAnswered), errors.Is(err, runtime.ErrNotQuestion):
		return out, ops.Conflict(err.Error())
	case errors.Is(err, runtime.ErrRequestGone):
		return out, ops.Conflict(err.Error() + "；需要的話請重新交代")
	case errors.Is(err, store.ErrNotFound):
		return out, ops.NotFound("找不到這張卡")
	case err != nil:
		return out, err
	}
	out.Status, out.Resolution = got.Status, got.Resolution
	return out, nil
}

// findCard 找要回覆的卡：直接給 inbox_id，或給 task_id 找那件工作目前在等的。
func findCard(ctx context.Context, call *ops.Call, inboxID, taskID string) (store.InboxItem, error) {
	if (inboxID == "") == (taskID == "") {
		return store.InboxItem{}, ops.InvalidInput(ops.FieldError{Message: "inbox_id、task_id 要擇一"})
	}
	var it store.InboxItem
	var err error
	if inboxID != "" {
		it, err = call.DB.GetInboxItem(ctx, inboxID)
	} else {
		var items []store.InboxItem
		items, err = call.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, TaskID: taskID,
			Kinds: []string{"approval", "question", "secret"}})
		if err == nil && len(items) == 0 {
			err = store.ErrNotFound
		}
		if err == nil {
			it = items[0]
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		return it, ops.NotFound("找不到 Bot 在等的這個問題")
	}
	if err != nil {
		return it, err
	}
	if it.BotID != "" && !call.Identity.CanAccess(ops.ResourceBot, it.BotID) {
		return it, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這個 Bot")
	}
	return it, nil
}

func (d Deps) askAgain(ctx context.Context, call *ops.Call, in AskAgainIn) (SendOut, error) {
	if call.Identity.Kind != ops.KindOwner {
		return SendOut{}, ops.Forbidden("只有你本人能請 Bot 再問一次")
	}
	rt, err := d.runtime()
	if err != nil {
		return SendOut{}, err
	}
	it, err := findCard(ctx, call, in.InboxID, "")
	if err != nil {
		return SendOut{}, err
	}
	if it.Status != store.InboxExpired || it.ThreadID == "" {
		return SendOut{}, ops.Conflict(runtime.ErrNotExpired.Error())
	}
	if call.DryRun {
		if _, task := rt.Active(it.BotID); task != "" {
			return SendOut{}, busy()
		}
		return SendOut{BotID: it.BotID, ThreadID: it.ThreadID}, nil
	}
	res, _, err := rt.AskAgain(ctx, it.ID)
	switch {
	case errors.Is(err, runtime.ErrBusy):
		return SendOut{}, busy()
	case errors.Is(err, runtime.ErrAnswered):
		return SendOut{}, ops.Conflict("已經請它再問過了")
	case errors.Is(err, runtime.ErrNotExpired):
		return SendOut{}, ops.Conflict(err.Error())
	case errors.Is(err, runtime.ErrNoModel):
		return SendOut{}, ops.Conflict("還沒設定模型：請先用 xbot.models.set_provider 設定模型供應商與預設模型")
	case err != nil:
		return SendOut{}, err
	}
	return SendOut{BotID: it.BotID, ThreadID: res.ThreadID, TaskID: res.TaskID, MessageID: res.MessageID}, nil
}

func steps(ctx context.Context, call *ops.Call, in StepsIn) (StepsOut, error) {
	th, err := thread(ctx, call, in.ThreadID)
	if err != nil {
		return StepsOut{}, err
	}
	limit := in.Limit
	if limit <= 0 || limit > limits.StepsPageMax {
		limit = limits.StepsPageMax
	}
	rows, err := call.DB.ListThreadEvents(ctx, th.ID, in.TaskID, stepKinds, in.After, limit+1)
	if err != nil {
		return StepsOut{}, err
	}
	out := StepsOut{Events: []StepEvent{}}
	if len(rows) > limit {
		rows, out.HasMore = rows[:limit], true
	}
	for _, r := range rows {
		out.Events = append(out.Events, StepEvent{ID: r.ID, Kind: r.Kind, BotID: r.BotID, ThreadID: r.ThreadID, TaskID: r.TaskID,
			Payload: r.Payload, At: r.CreatedAt})
	}
	if out.HasMore {
		out.NextAfter = rows[len(rows)-1].ID
	}
	return out, nil
}
