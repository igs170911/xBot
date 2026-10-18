// Package engine 是引擎驅動的共同介面與正規化事件（TECH-DESIGN §4、PRD EN-1）。
//
// XBot 不自己寫 agent harness：每個引擎（Hermes、之後的 ACP）各寫一個 Driver，
// 把引擎自己的事件轉成這裡的 Event，runtime 只看 Event。
package engine

import (
	"context"
	"encoding/json"
	"errors"
)

// EventType 是正規化事件的種類。
type EventType string

const (
	// EventText 是回覆文字的一段（串流）。
	EventText EventType = "text"
	// EventThinking 是模型思考過程的一段（串流）。
	EventThinking EventType = "thinking"
	// EventToolCall 是開始呼叫一個工具。
	EventToolCall EventType = "tool_call"
	// EventToolResult 是工具呼叫結束。
	EventToolResult EventType = "tool_result"
	// EventNeedsApproval 是引擎在等人回覆（核准、補充問題、密碼等）；用 Driver.Respond 回覆。
	EventNeedsApproval EventType = "needs_approval"
	// EventRequestClosed 是引擎不再等某個 needs_approval 的回覆了（例：Hermes 自己逾時或取消）；Request 只有 ID。
	EventRequestClosed EventType = "request_closed"
	// EventUsage 是用量更新（輪中的即時值；Done 會帶最後的值）。
	EventUsage EventType = "usage"
	// EventDone 是這一輪結束（Status 說明是正常完成、被中斷或失敗）。
	EventDone EventType = "done"
	// EventError 是錯誤。Fatal 為 true 表示連線斷了，這個 Session 不能再用。
	EventError EventType = "error"
	// EventStatus 是給人看的進度提示（例：正在壓縮對話）。
	EventStatus EventType = "status"
)

// Done 的狀態。
const (
	StatusComplete    = "complete"
	StatusInterrupted = "interrupted"
	StatusError       = "error"
)

// Event 是正規化後的引擎事件。依 Type 只有部分欄位有值。
type Event struct {
	Type EventType `json:"type"`
	// Session 是引擎裡的 session（Session.ID()）。
	Session string `json:"session,omitempty"`
	// Text：text／thinking 的這一段；done 的完整回覆；error、status 的說明。
	Text string `json:"text,omitempty"`
	// Status：done 的結果（complete／interrupted／error）。
	Status string     `json:"status,omitempty"`
	Tool   *ToolEvent `json:"tool,omitempty"`
	// Request：needs_approval 的內容。
	Request *Request `json:"request,omitempty"`
	Usage   *Usage   `json:"usage,omitempty"`
	// Fatal：error 時表示連線已中斷。
	Fatal bool `json:"fatal,omitempty"`
	// Quota：error、done 是額度用完、限流、餘額不足或登入過期時（EN-7）。
	Quota *QuotaError `json:"quota,omitempty"`
}

// ToolEvent 是一次工具呼叫。
type ToolEvent struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args,omitempty"`
	Context string          `json:"context,omitempty"` // 引擎給的簡短說明（例：要開的網址）
	Summary string          `json:"summary,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	// OK 只在 tool_result 有意義。
	OK       bool    `json:"ok"`
	Duration float64 `json:"duration_s,omitempty"`
}

// Request 是引擎要人回覆的請求。
type Request struct {
	ID string `json:"id"`
	// Kind：approval（核准）、clarify（補充問題）、secret（密碼、驗證碼）、或引擎自己的名稱。
	Kind string `json:"kind"`
	// Method 是引擎原本的請求名稱（Hermes：clarify、approval、secret、sudo、vault.code…）。
	Method  string   `json:"method,omitempty"`
	Prompt  string   `json:"prompt"`
	Choices []string `json:"choices,omitempty"`
	// Params 是引擎原本的參數，回覆時可能用得到。
	Params json.RawMessage `json:"params,omitempty"`
}

// Usage 是一輪的用量。
type Usage struct {
	Model        string  `json:"model,omitempty"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Reasoning    int64   `json:"reasoning_tokens,omitempty"`
	CacheRead    int64   `json:"cache_read_tokens,omitempty"`
	CacheWrite   int64   `json:"cache_write_tokens,omitempty"`
	Calls        int64   `json:"calls,omitempty"`
	ContextUsed  int64   `json:"context_used,omitempty"`
	ContextMax   int64   `json:"context_max,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// StartOptions 是開始（或接回）一段對話需要的資料。
type StartOptions struct {
	BotID    string
	ThreadID string
	// Resume 是之前存下的引擎 session（Session.ResumeID()）；空白表示開新的。
	Resume string
	Title  string
	// Model 是這段對話要用的模型（引擎自己的寫法）。
	Model string
	// Mode 是 Bot 的權限等級（bot.approval_mode：ask／edits／auto／full）。ACP 引擎用它挑 session 模式；
	// Hermes 不用（容器內的指令審核已關閉）。
	Mode string
}

// Session 是引擎裡的一段對話，對應 XBot 的一條對話串。
type Session interface {
	// ID 是引擎目前這次連線裡的 session 代號。
	ID() string
	// ResumeID 是可以存起來、之後接回同一段對話的代號（Hermes 的 stored_session_id）。
	ResumeID() string
}

// Message 是要送給引擎的一則使用者訊息。
type Message struct {
	Text string
}

// Answer 是對 needs_approval 的回覆。
type Answer struct {
	// Choice：approval 用 once／always／deny。
	Choice string
	// Answers：clarify 有多個問題時，逐題的回答（依問題順序）；空的時每題都用 Text。
	Answers []string
	// Text：clarify 的回答。
	Text string
	// Value：secret 類的值（不會寫進紀錄）。
	Value string
}

// Driver 是一種引擎的驅動。
type Driver interface {
	// Name 是引擎名稱（例：hermes），寫在 bot.engine。
	Name() string
	// Start 開始或接回一段對話。
	Start(ctx context.Context, o StartOptions) (Session, error)
	// Send 送出一則訊息；回覆經 Events 串流回來，最後一定有一個 done（或連線中斷的 error）。
	Send(ctx context.Context, s Session, m Message) error
	// Interrupt 停止目前這一輪；之後仍會收到 status=interrupted 的 done。
	Interrupt(ctx context.Context, s Session) error
	// Respond 回覆 needs_approval。
	Respond(ctx context.Context, s Session, requestID string, a Answer) error
	// Events 是這段對話的事件；連線中斷時送出 Fatal error 後關閉。
	Events(s Session) <-chan Event
}

// ErrDisconnected 表示和引擎的連線已中斷。
var ErrDisconnected = errors.New("和 Bot 的連線已中斷")
