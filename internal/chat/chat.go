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
	"xbot/internal/voice"
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
	ResumeInterrupted(ctx context.Context, taskID string, req runtime.ChatRequest) (runtime.ChatResult, error)
	Progress(ctx context.Context, botID, skipTask string) (runtime.Progress, error)
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

// DeleteThreadIn 是 xbot.chat.delete_thread 的輸入。
type DeleteThreadIn struct {
	ThreadID string `json:"thread_id" required:"true" max:"64" desc:"要刪的對話串"`
}

// DeleteThreadOut 是 xbot.chat.delete_thread 的輸出。
type DeleteThreadOut struct {
	Deleted bool   `json:"deleted"`
	BotID   string `json:"bot_id"`
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
	// Restart：服務重開時這則回覆還在寫。status=interrupted 時這件工作做到一半被中斷了，可以用 xbot.chat.resume 接著做；
	// 沒有 status 時系統已經自動接著做（下一則回覆接在後面）。Resumed：已經按過「接著做」。
	Restart bool `json:"restart,omitempty" desc:"服務重開時這則回覆還在寫（status=interrupted：做到一半被中斷，可以 xbot.chat.resume）"`
	Resumed bool `json:"resumed,omitempty" desc:"被中斷的工作已經接著做了"`
	// Silent：隊友交辦或群組裡 Bot 回 [silent]（沒有要補充），畫面不顯示。
	Silent bool `json:"silent,omitempty"`
	// Peer：kind=peer 時這一行是什麼（已傳訊息給…、來自…的訊息、開了群組…，X02）。
	Peer *PeerLine `json:"peer,omitempty"`
	// Activity：kind=activity（sender=system）時是哪一種系統紀錄，例：auto_continue（自動繼續，X04）。
	Activity string `json:"activity,omitempty" desc:"kind=activity 時是哪一種：auto_continue（系統叫 Bot 自己接著做）"`
	// Judge：Bot 這一輪回覆的完成判定（X04）。
	Judge *Judgment `json:"judge,omitempty" desc:"系統判斷這件工作做完了沒（Bot 的回覆才有）"`
	// Card：kind=card 時卡片的內容（Bot 用 xbot.cards.post 發的資訊、核准、檔案卡，C11）。
	Card *Card `json:"card,omitempty"`
	// Voice：Bot 的回覆念成了語音訊息（E32，VC-1）；畫面顯示播放器。
	Voice *VoiceNote `json:"voice,omitempty"`
	// Quota：這一輪因為引擎額度用完、限流、餘額不足或登入過期而失敗（EN-7）；換引擎用 xbot.engines.switch。
	Quota     *runtime.QuotaView `json:"quota,omitempty"`
	CreatedAt int64              `json:"created_at"`
	UpdatedAt int64              `json:"updated_at"`
}

// CardFile 是檔案卡上的一個檔案（Bot 電腦裡的完整路徑，C33 點了預覽或下載）。
type CardFile struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// Card 是對話裡的一張卡片（botops.card 的對外版本）。
type Card struct {
	Type       string     `json:"type" desc:"info、approval、file"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	ApprovalID string     `json:"approval_id,omitempty"`
	Category   string     `json:"category,omitempty"`
	Files      []CardFile `json:"files,omitempty"`
}

// VoiceNote 是語音訊息（xbot.voice.speak 的 voice_note）。
type VoiceNote struct {
	Engine  string  `json:"engine" desc:"proxy：用 audio_id 播放；browser：用瀏覽器內建的聲音念"`
	AudioID string  `json:"audio_id,omitempty" desc:"GET /api/v1/voice.audio?id=…"`
	Voice   string  `json:"voice"`
	Speed   float64 `json:"speed"`
	Chars   int     `json:"chars"`
	Bytes   int64   `json:"bytes,omitempty"`
}

// ReadOut 是 xbot.chat.read 的輸出。
type ReadOut struct {
	Thread     Thread    `json:"thread"`
	Messages   []Message `json:"messages" desc:"由舊到新"`
	HasMore    bool      `json:"has_more" desc:"還有更早的訊息"`
	NextBefore int64     `json:"next_before,omitempty" desc:"讀更早一頁時帶的 before"`
	// Tasks 是這一頁訊息提到的工作現在的狀態（task id → queued／running／done…）；畫面以它為準，不用從事件推。
	Tasks map[string]string `json:"tasks,omitempty" desc:"這一頁訊息提到的工作現在的狀態（task id → 狀態）"`
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
	Verdict  string `json:"verdict,omitempty" desc:"done 時的結果：answer、cancelled、failed（answer 只代表這一輪結束，不代表目標達成；看 judge）"`
	// Judge 是做完了沒的系統判斷（X04）：done、needs_person、handed_back、only_promised、stuck。
	Judge *runtime.Judgment `json:"judge,omitempty" desc:"系統判斷做完了沒（done 時才有；你停掉的沒有）"`
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
	// Via：voice 表示是在通話中用說的（E32）。用說的只能允許低風險的事，拒絕一律可以；高風險的要在畫面上按。
	Via string `json:"via,omitempty" enum:"voice" desc:"voice：通話中用說的決定（危險的指令、花錢、對外發送等不能用說的允許）"`
}

// RespondOut 是 xbot.chat.respond 的輸出。
type RespondOut struct {
	InboxID    string `json:"inbox_id"`
	TaskID     string `json:"task_id"`
	ThreadID   string `json:"thread_id"`
	Status     string `json:"status" desc:"卡片狀態：resolved（已交給 Bot）；預演時是 open"`
	Resolution string `json:"resolution,omitempty" desc:"once、always、deny、answered、provided、declined"`
}

// ResumeIn 是 xbot.chat.resume 的輸入。
type ResumeIn struct {
	TaskID string `json:"task_id" required:"true" max:"64" desc:"做到一半被中斷的那件工作（訊息的 task_id，status=interrupted、restart=true）"`
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
	registerJudge(reg, d)
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
	ops.Register(reg, ops.Op[DeleteThreadIn, DeleteThreadOut]{
		Name: "xbot.chat.delete_thread", Summary: "刪除一條對話串和裡面的訊息",
		Description: "訊息刪了無法復原；Bot 的電腦、檔案、記憶不受影響。Bot 正在這條對話串做事時回 conflict，先 xbot.chat.stop。" +
			"群組的座位不能刪。",
		Scope: ops.ScopeOperate, Risk: ops.RiskConfirm,
		Handle: d.deleteThread,
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
	ops.Register(reg, ops.Op[ResumeIn, SendOut]{
		Name: "xbot.chat.resume", Summary: "請 Bot 接著做被中斷的工作",
		Description: "服務重開時，你交辦和隊友交辦的工作會自動接著做一次；再被中斷，或定時任務、Webhook 的工作被中斷時，" +
			"Bot 的回覆標成 status=interrupted、restart=true。用它在同一條對話串請 Bot 接著做（開一件你交辦的新工作，" +
			"排在手上的工作後面），每件只能一次。不是被中斷的工作、群組的一輪回 conflict。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.resume,
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

func (d Deps) deleteThread(ctx context.Context, call *ops.Call, in DeleteThreadIn) (DeleteThreadOut, error) {
	rt, err := d.runtime()
	if err != nil {
		return DeleteThreadOut{}, err
	}
	th, err := thread(ctx, call, in.ThreadID)
	if err != nil {
		return DeleteThreadOut{}, err
	}
	if th.RoomID != "" {
		return DeleteThreadOut{}, ops.Conflict("這是群組的座位，不能單獨刪除")
	}
	if active, _ := rt.Active(th.BotID); active == th.ID {
		return DeleteThreadOut{}, ops.Conflict("Bot 正在這條對話串做事，先停止它再刪")
	}
	out := DeleteThreadOut{Deleted: true, BotID: th.BotID}
	if call.DryRun {
		return out, nil
	}
	if err := call.DB.DeleteThread(ctx, th.ID); err != nil {
		return DeleteThreadOut{}, err
	}
	return out, nil
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
	judgments(ctx, call.DB, out.Messages)
	out.Tasks = map[string]string{}
	for _, m := range out.Messages {
		if m.TaskID == "" {
			continue
		}
		if _, ok := out.Tasks[m.TaskID]; ok {
			continue
		}
		t, err := call.DB.GetTask(ctx, m.TaskID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return ReadOut{}, err
		}
		out.Tasks[m.TaskID] = t.State
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
	TaskID    string             `json:"task_id"`
	Actor     string             `json:"actor"`
	ActorName string             `json:"actor_name"`
	Via       string             `json:"via"`
	Streaming bool               `json:"streaming"`
	Status    string             `json:"status"`
	Restart   bool               `json:"restart"`
	Resumed   bool               `json:"resumed"`
	Silent    bool               `json:"silent"`
	Peer      *PeerLine          `json:"peer"`
	Activity  string             `json:"activity"`
	Judge     *runtime.Judgment  `json:"judge"`
	Card      *Card              `json:"card"`
	Quota     *runtime.QuotaView `json:"quota"`
	Voice     *VoiceNote         `json:"voice"`
}

func messageView(m store.Message) Message {
	var meta messageMeta
	_ = json.Unmarshal(m.Meta, &meta)
	out := Message{ID: m.ID, Seq: m.Seq, Sender: m.Sender, Kind: m.Kind, Content: m.Content, TaskID: meta.TaskID,
		Actor: meta.Actor, ActorName: meta.ActorName, Via: meta.Via, Streaming: meta.Streaming, Status: meta.Status, Restart: meta.Restart, Resumed: meta.Resumed, Silent: meta.Silent, Peer: meta.Peer,
		Activity: meta.Activity, Card: meta.Card, Quota: meta.Quota, Voice: meta.Voice, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	if meta.Judge != nil && meta.Judge.Verdict != "" {
		out.Judge = &Judgment{Judgment: *meta.Judge}
	}
	return out
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
	out := WaitOut{TaskID: task.ID, BotID: task.BotID, ThreadID: task.ThreadID, State: task.State, Verdict: task.Verdict, Judge: runtime.TaskJudgment(task),
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
	if in.Via == voice.ViaVoice {
		if err := voiceAllowed(it, in); err != nil {
			return out, err
		}
	}
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

// voiceAllowed 檢查通話中用說的回覆（E32）：拒絕、略過一律可以；密碼類不能用說的；
// 允許要是低風險的引擎動作，而且不能「一律允許」。
func voiceAllowed(it store.InboxItem, in RespondIn) error {
	if in.Decline || in.Choice == "deny" {
		return nil
	}
	switch it.Kind {
	case "secret":
		return ops.Forbidden("密碼、驗證碼要在畫面上輸入，不能用說的")
	case "approval":
		if in.Choice == "always" || voice.EngineApprovalNeedsTap(it.Payload) {
			return ops.Forbidden(voice.TapMessage)
		}
	}
	return nil
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

func (d Deps) resume(ctx context.Context, call *ops.Call, in ResumeIn) (SendOut, error) {
	rt, err := d.runtime()
	if err != nil {
		return SendOut{}, err
	}
	task, err := call.DB.GetTask(ctx, in.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return SendOut{}, ops.NotFound("找不到這件工作")
	}
	if err != nil {
		return SendOut{}, err
	}
	if !call.Identity.CanAccess(ops.ResourceBot, task.BotID) {
		return SendOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這件工作的 Bot")
	}
	if call.DryRun {
		if task.State != store.TaskDone || task.Verdict != runtime.VerdictInterrupted || task.Source == runtime.SourceRoom {
			return SendOut{}, ops.Conflict(runtime.ErrNotInterrupted.Error())
		}
		return SendOut{BotID: task.BotID, ThreadID: task.ThreadID}, nil
	}
	id := call.Identity
	res, err := rt.ResumeInterrupted(ctx, task.ID, runtime.ChatRequest{Actor: id.Actor(), ActorName: id.DisplayName(), Via: id.Via})
	var over *proxy.BudgetError
	switch {
	case errors.Is(err, runtime.ErrNotInterrupted), errors.Is(err, runtime.ErrResumed):
		return SendOut{}, ops.Conflict(err.Error())
	case errors.As(err, &over):
		return SendOut{}, ops.Budget(over.Error())
	case errors.Is(err, runtime.ErrNoModel):
		return SendOut{}, ops.Conflict("還沒設定模型：請先用 xbot.models.set_provider 設定模型供應商與預設模型")
	case err != nil:
		return SendOut{}, err
	}
	return SendOut{BotID: task.BotID, ThreadID: res.ThreadID, TaskID: res.TaskID, MessageID: res.MessageID, Queued: res.Queued}, nil
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
