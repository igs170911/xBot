// Package inbox 是「待我處理」、核准與稽核的操作（PRD §6.11 IN-*、MCP-9、MCP-10、MCP-13）：
// approvals.list、approvals.wait、approvals.decide、inbox.list、inbox.counts、inbox.mark_read、inbox.dismiss、inbox.undo、audit.list。
// Bot 在對話裡的提問（核准指令、補充問題、密碼）由 xbot.chat.respond 回覆（internal/chat），卡片一樣在這裡列出。
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"xbot/internal/delegation"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── 輸入輸出 ──────────────────────────────────────────

// ApprovalStatus 是核准的狀態。
type ApprovalStatus string

// EnumValues 讓 JSON Schema 列出可用值。
func (ApprovalStatus) EnumValues() []string {
	return []string{"pending", "approved", "denied", "expired", "canceled"}
}

// Approval 是對外的核准（參數已遮蔽密鑰）。
type Approval struct {
	ID          string          `json:"id"`
	BotID       string          `json:"bot_id,omitempty"`
	Op          string          `json:"op" desc:"要執行的操作，例：xbot.bots.delete"`
	Summary     string          `json:"summary"`
	Params      json.RawMessage `json:"params" desc:"操作的輸入（密鑰已遮蔽）"`
	RequestedBy string          `json:"requested_by" desc:"誰要求的：agent:<id>、bot:<id>"`
	RequestID   string          `json:"request_id,omitempty"`
	Status      ApprovalStatus  `json:"status"`
	DecidedBy   string          `json:"decided_by,omitempty"`
	DecidedAt   int64           `json:"decided_at,omitempty"`
	CreatedAt   int64           `json:"created_at"`
}

// ApprovalsListIn 是 xbot.approvals.list 的輸入。
type ApprovalsListIn struct {
	Status ApprovalStatus `json:"status,omitempty" desc:"只列這個狀態；不填列全部"`
	BotID  string         `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只列和這個 Bot 有關的"`
	Limit  int            `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾筆；預設 50"`
}

// ApprovalsListOut 是 xbot.approvals.list 的輸出。
type ApprovalsListOut struct {
	Approvals []Approval `json:"approvals" desc:"新到舊；代理只看得到自己要求的"`
}

// WaitIn 是 xbot.approvals.wait 的輸入。
type WaitIn struct {
	ApprovalID string `json:"approval_id" required:"true" max:"64" desc:"pending_approval 錯誤裡的 needs_approval_id"`
	TimeoutS   int    `json:"timeout_s,omitempty" min:"1" max:"300" desc:"最多等幾秒；預設 60"`
}

// Outcome 是核准的結果與（允許後）動作的執行結果。
type Outcome struct {
	Approval Approval `json:"approval"`
	// Settled：已經決定了（允許時還要等動作執行完或失敗）。false 表示等到逾時、還沒決定。
	Settled  bool            `json:"settled"`
	Executed bool            `json:"executed" desc:"允許後動作已經執行完成"`
	Failed   bool            `json:"failed" desc:"允許了但執行失敗（error 是原因；你可以再按一次允許重試）"`
	Output   json.RawMessage `json:"output,omitempty" desc:"動作的輸出（executed 時）"`
	Error    *ops.Error      `json:"error,omitempty" desc:"允許了但執行失敗時的錯誤"`
}

// DecideIn 是 xbot.approvals.decide 的輸入。
type DecideIn struct {
	ApprovalID string `json:"approval_id" required:"true" max:"64"`
	Decision   string `json:"decision" required:"true" enum:"allow,deny" desc:"allow：允許這次並執行；deny：退回"`
}

// InboxStatus 是待我處理的狀態篩選。
type InboxStatus string

// EnumValues 讓 JSON Schema 列出可用值。
func (InboxStatus) EnumValues() []string {
	return []string{"open", "resolved", "dismissed", "expired", "all"}
}

// Group 是通知中心的篩選分類（B04、L14）。
type Group string

// EnumValues 讓 JSON Schema 列出可用值。
func (Group) EnumValues() []string { return notify.Groups }

// Item 是一張待我處理（或通知）。
type Item struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind" desc:"approval、question、secret、signin、takeover、proposal、discussion、routine_failed、budget、done、error、mention、delegated（已由代理代為核准的紀錄）"`
	Group      string          `json:"group" desc:"decide（要你決定）、done（完成）、mention（提到你）、error（出錯）"`
	BotID      string          `json:"bot_id,omitempty"`
	TaskID     string          `json:"task_id,omitempty" desc:"Bot 在對話裡問的：是哪件工作"`
	ThreadID   string          `json:"thread_id,omitempty" desc:"Bot 在對話裡問的：是哪條對話串"`
	Ref        string          `json:"ref,omitempty" desc:"例：核准的 id"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Payload    json.RawMessage `json:"payload" desc:"卡片內容（選項、指令、輸入欄…）；不含任何密碼類的值"`
	Status     string          `json:"status" desc:"open、resolved、dismissed、expired"`
	Resolution string          `json:"resolution,omitempty" desc:"怎麼處理的：once、always、deny、allow、answered、provided、declined、expired、gone、canceled、asked_again、dismissed、info、delegated（代理代為核准）、undone（代為核准的動作已復原）"`
	ResolvedBy string          `json:"resolved_by,omitempty"`
	ResolvedAt int64           `json:"resolved_at,omitempty"`
	Read       bool            `json:"read"`
	ExpiresAt  int64           `json:"expires_at,omitempty" desc:"無人在場的提問：這個時間沒回就過期（0 表示不會過期）"`
	UndoUntil  int64           `json:"undo_until,omitempty" desc:"這個時間之前可以用 xbot.inbox.undo 復原（0 表示不能復原）"`
	CreatedAt  int64           `json:"created_at"`
}

// InboxListIn 是 xbot.inbox.list 的輸入。
type InboxListIn struct {
	Status   InboxStatus `json:"status,omitempty" desc:"預設 open（還沒處理的）；通知中心用 all"`
	Group    Group       `json:"group,omitempty" desc:"只列這一類：decide（要你決定）、done（完成）、mention（提到你）、error（出錯）"`
	Kind     string      `json:"kind,omitempty" max:"32" desc:"只列這一種"`
	BotID    string      `json:"bot_id,omitempty" resource:"bot" max:"64"`
	ThreadID string      `json:"thread_id,omitempty" max:"64" desc:"只列這條對話串的"`
	Unread   bool        `json:"unread,omitempty" desc:"只列未讀的"`
	Limit    int         `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾筆；預設 50"`
}

// InboxListOut 是 xbot.inbox.list 的輸出。
type InboxListOut struct {
	Items []Item `json:"items" desc:"新到舊"`
}

// CountsIn 是 xbot.inbox.counts 的輸入。
type CountsIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只算這個 Bot 的"`
}

// GroupCount 是一個分類的數量。
type GroupCount struct {
	Open   int `json:"open" desc:"還沒處理的"`
	Unread int `json:"unread" desc:"未讀的"`
}

// CountsOut 是 xbot.inbox.counts 的輸出（側欄與 Dock 徽章、篩選膠囊上的數字）。
type CountsOut struct {
	Open   int                   `json:"open" desc:"待我處理：還沒處理的卡（側欄徽章）"`
	Unread int                   `json:"unread" desc:"通知中心的未讀數"`
	Groups map[string]GroupCount `json:"groups" desc:"各分類：decide、done、mention、error"`
}

// MarkReadIn 是 xbot.inbox.mark_read 的輸入：ids、thread_id、all 擇一。
type MarkReadIn struct {
	IDs      []string `json:"ids,omitempty" max:"200" desc:"這幾張"`
	ThreadID string   `json:"thread_id,omitempty" max:"64" desc:"這條對話串的全部（打開對話時）"`
	All      bool     `json:"all,omitempty" desc:"全部標為已讀"`
}

// MarkReadOut 是 xbot.inbox.mark_read 的輸出。
type MarkReadOut struct {
	Marked int `json:"marked" desc:"這次從未讀變成已讀的張數"`
}

// ItemIn 是只帶一張卡的輸入（dismiss、undo）。
type ItemIn struct {
	InboxID string `json:"inbox_id" required:"true" max:"64"`
}

// ItemOut 是處理後的卡。
type ItemOut struct {
	Item Item `json:"item"`
}

// AuditListIn 是 xbot.audit.list 的輸入。
type AuditListIn struct {
	Actor  string `json:"actor,omitempty" max:"80" desc:"只列這個呼叫者：owner、agent:<id>、bot:<id>（代理只看得到自己的）"`
	Target string `json:"target,omitempty" max:"64" desc:"只列和這個 id 有關的"`
	Before int64  `json:"before,omitempty" min:"0" desc:"往前翻頁：只列 id 比它小的（上一頁的 next_before）"`
	Limit  int    `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾筆；預設 50"`
}

// AuditListOut 是 xbot.audit.list 的輸出。
type AuditListOut struct {
	Entries    []store.AuditEntry `json:"entries" desc:"新到舊；參數已遮蔽密鑰"`
	NextBefore int64              `json:"next_before,omitempty"`
}

// Register 登錄核准、待我處理與稽核的操作。approvals.decide 允許時用 reg.ExecuteApproved 執行原本的動作。
// 卡片有變化時在交易裡寫 inbox.* 事件，提交後用 reg.Notify 推給 SSE。
func Register(reg *ops.Registry) {
	ops.Register(reg, ops.Op[ApprovalsListIn, ApprovalsListOut]{
		Name: "xbot.approvals.list", Summary: "列出等你核准的動作",
		Description: "代理看得到自己要求的；有代為核准授權的代理也看得到授權類別內、它能碰的 Bot 的核准。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ApprovalsListIn) (ApprovalsListOut, error) {
			return listApprovals(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[WaitIn, Outcome]{
		Name: "xbot.approvals.wait", Summary: "等一個核准的結果",
		Description: "收到 pending_approval 後用它等：使用者允許並執行完（executed）、允許了但執行失敗（failed）、退回、" +
			"或逾時（settled=false，可以再呼叫）。代理只能等自己要求的核准。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in WaitIn) (Outcome, error) {
			return wait(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[DecideIn, Outcome]{
		Name: "xbot.approvals.decide", Summary: "允許或退回一個等核准的動作",
		Description: "允許後立刻執行原本的動作並回傳結果。只有你本人、或拿到代為核准授權的代理（MCP-10，xbot.agents.set_delegation）能用；" +
			"代理只能允許授權類別內的動作，受每日次數、金額與有效期限限制。刪除 Bot 或資料、移除主機、提高花費上限、改代為核准授權永遠要你本人。" +
			"每次代為核准都會在待我處理留一張紀錄卡，動作可以復原的話，你 10 分鐘內可以復原。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DecideIn) (Outcome, error) {
			return decide(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[InboxListIn, InboxListOut]{
		Name: "xbot.inbox.list", Summary: "列出「待我處理」與通知",
		Description: "預設只列還沒處理的（待我處理）；通知中心用 status=all 加 group 篩選。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: listInbox,
	})
	ops.Register(reg, ops.Op[CountsIn, CountsOut]{
		Name: "xbot.inbox.counts", Summary: "待我處理與通知的數量（徽章用）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: counts,
	})
	ops.Register(reg, ops.Op[MarkReadIn, MarkReadOut]{
		Name: "xbot.inbox.mark_read", Summary: "把通知標為已讀",
		Description: "ids、thread_id、all 擇一。只有你本人能用（已讀是你的狀態）。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in MarkReadIn) (MarkReadOut, error) {
			return markRead(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[ItemIn, ItemOut]{
		Name: "xbot.inbox.dismiss", Summary: "略過一張不用處理的卡",
		Description: "操作的核准請用 xbot.approvals.decide；Bot 在對話裡的提問請用 xbot.chat.respond（decline）。10 分鐘內可以復原。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ItemIn) (ItemOut, error) {
			return dismiss(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[ItemIn, ItemOut]{
		Name: "xbot.inbox.undo", Summary: "復原剛剛的決定",
		Description: "10 分鐘內可以復原：退回的核准會回到等你決定，略過的卡會回到待我處理，" +
			"代理代為核准的動作（可以復原的話）會被收回。已經執行的動作、已經交給 Bot 的回答不能復原（undo_until 是 0）。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ItemIn) (ItemOut, error) {
			return undo(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[AuditListIn, AuditListOut]{
		Name: "xbot.audit.list", Summary: "列出稽核紀錄（誰、從哪裡、做了什麼）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: listAudit,
	})
}

func pageLimit(n int) int {
	if n <= 0 {
		return limits.PageDefault
	}
	return min(n, limits.PageMax)
}

type payload struct {
	Summary string          `json:"summary"`
	Params  json.RawMessage `json:"params"`
}

func approvalView(a store.Approval) Approval {
	var p payload
	_ = json.Unmarshal(a.Payload, &p)
	if len(p.Params) == 0 {
		p.Params = json.RawMessage("{}")
	}
	return Approval{ID: a.ID, BotID: a.BotID, Op: a.Action, Summary: p.Summary, Params: p.Params, RequestedBy: a.RequestedBy,
		RequestID: a.RequestID, Status: ApprovalStatus(a.Status), DecidedBy: a.DecidedBy, DecidedAt: a.DecidedAt, CreatedAt: a.CreatedAt}
}

// canSee：你本人看得到全部；代理與 Bot 看得到自己要求的核准；
// 有代為核准授權的代理也看得到授權類別內、它能碰的 Bot 的核准（MCP-10）。
func canSee(ctx context.Context, reg *ops.Registry, call *ops.Call, a store.Approval) bool {
	id := call.Identity
	if id.Kind == ops.KindOwner || a.RequestedBy == id.Actor() {
		return true
	}
	info, ok := reg.Get(a.Action)
	return ok && delegation.Covers(ctx, call.DB, id, info, a.BotID)
}

func listApprovals(ctx context.Context, reg *ops.Registry, call *ops.Call, in ApprovalsListIn) (ApprovalsListOut, error) {
	all, err := call.DB.ListApprovals(ctx, string(in.Status))
	if err != nil {
		return ApprovalsListOut{}, err
	}
	limit := pageLimit(in.Limit)
	out := ApprovalsListOut{Approvals: []Approval{}}
	for _, a := range all {
		if len(out.Approvals) == limit {
			break
		}
		if (in.BotID != "" && a.BotID != in.BotID) || !canSee(ctx, reg, call, a) {
			continue
		}
		out.Approvals = append(out.Approvals, approvalView(a))
	}
	return out, nil
}

func getApproval(ctx context.Context, reg *ops.Registry, call *ops.Call, id string) (store.Approval, error) {
	a, err := call.DB.GetApproval(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !canSee(ctx, reg, call, a)) {
		return a, ops.NotFound("找不到這個核准")
	}
	return a, err
}

// outcome 整理核准目前的結果；允許了的話看動作有沒有執行完（op_request 是 ok），或執行失敗（approval.error）。
func outcome(ctx context.Context, db *store.DB, a store.Approval) (Outcome, error) {
	o := Outcome{Approval: approvalView(a), Settled: a.Status != "pending"}
	if a.Status != "approved" {
		return o, nil
	}
	req, err := db.GetOpRequestByApproval(ctx, a.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return o, err
	}
	if err == nil && req.Status == "ok" {
		o.Executed, o.Output = true, req.Result
		return o, nil
	}
	if a.Error != "" {
		var oe ops.Error
		if json.Unmarshal([]byte(a.Error), &oe) == nil && oe.Code != "" {
			o.Failed, o.Error = true, &oe
		}
	}
	return o, nil
}

func wait(ctx context.Context, reg *ops.Registry, call *ops.Call, in WaitIn) (Outcome, error) {
	a, err := getApproval(ctx, reg, call, in.ApprovalID)
	if err != nil {
		return Outcome{}, err
	}
	timeout := limits.ChatWaitDefault
	if in.TimeoutS > 0 {
		timeout = min(time.Duration(in.TimeoutS)*time.Second, limits.ChatWaitMax)
	}
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(limits.ApprovalWaitPoll)
	defer tick.Stop()
	for {
		o, err := outcome(ctx, call.DB, a)
		if err != nil {
			return o, err
		}
		// 允許了但還沒執行完（使用者那邊正在執行）就再等一下；執行失敗也算有結果。
		done := o.Settled && (a.Status != "approved" || o.Executed || o.Failed)
		if done || call.DryRun || !time.Now().Before(deadline) {
			if !done {
				o.Settled = false
			}
			return o, nil
		}
		select {
		case <-ctx.Done():
			return o, ctx.Err()
		case <-tick.C:
		}
		a, err = call.DB.GetApproval(ctx, a.ID)
		if errors.Is(err, store.ErrNotFound) {
			// 核准跟著 Bot 一起刪掉了（例：核准的動作就是刪這個 Bot）：看執行紀錄。
			o.Settled, o.Approval.Status = true, "approved"
			if req, e := call.DB.GetOpRequestByApproval(ctx, in.ApprovalID); e == nil && req.Status == "ok" {
				o.Executed, o.Output = true, req.Result
			}
			return o, nil
		}
		if err != nil {
			return o, err
		}
	}
}

func decide(ctx context.Context, reg *ops.Registry, call *ops.Call, in DecideIn) (Outcome, error) {
	// MCP-9／MCP-10：核准只認你本人，唯一例外是你授權的代理代為允許（delegation）。Bot 一律不能決定。
	if call.Identity.Kind != ops.KindOwner {
		if call.Identity.Kind != ops.KindAgent {
			return Outcome{}, ops.Forbidden("只有你本人能核准；請用 xbot.approvals.wait 等結果")
		}
		return decideDelegated(ctx, reg, call, in)
	}
	a, err := getApproval(ctx, reg, call, in.ApprovalID)
	if err != nil {
		return Outcome{}, err
	}
	allow := in.Decision == "allow"
	switch {
	case a.Status == "pending":
	case a.Status == "approved" && allow:
		// 允許過但上次執行失敗：再執行一次（ExecuteApproved 保證只會成功一次）。
	default:
		return Outcome{}, ops.Conflict(store.ErrAlreadyDecided.Error())
	}
	if call.DryRun {
		o := Outcome{Approval: approvalView(a), Settled: true}
		o.Approval.Status = "denied"
		if allow {
			o.Approval.Status = "approved"
		}
		return o, nil
	}
	if a.Status == "pending" {
		status, resolution := "denied", "deny"
		if allow {
			status, resolution = "approved", "allow"
		}
		var evs []store.Event
		err := call.DB.Tx(ctx, func(q *store.Queries) error {
			if err := q.DecideApproval(ctx, a.ID, status, call.Identity.Actor()); err != nil {
				return err
			}
			items, err := q.ResolveInboxByRef(ctx, a.ID, resolution, call.Identity.Actor())
			if err != nil {
				return err
			}
			evs, err = appendUpdated(ctx, q, items...)
			return err
		})
		if err != nil {
			return Outcome{}, err
		}
		reg.Notify(evs...)
	}
	if !allow {
		a, err = call.DB.GetApproval(ctx, a.ID)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Approval: approvalView(a), Settled: true}, nil
	}
	return execute(ctx, reg, call, a)
}

// execute 執行允許了的動作，整理成 Outcome。
func execute(ctx context.Context, reg *ops.Registry, call *ops.Call, a store.Approval) (Outcome, error) {
	res, execErr := reg.ExecuteApproved(ctx, a.ID)
	o := Outcome{Settled: true}
	if cur, err := call.DB.GetApproval(ctx, a.ID); err == nil {
		o.Approval = approvalView(cur)
	} else {
		// 核准跟著被刪的 Bot 一起刪掉了（動作就是刪這個 Bot）。
		o.Approval = approvalView(a)
		o.Approval.Status, o.Approval.DecidedBy = "approved", call.Identity.Actor()
	}
	if execErr != nil {
		o.Failed, o.Error = true, ops.AsError(execErr)
		return o, nil
	}
	o.Executed, o.Output = true, res.Output
	return o, nil
}

// decideDelegated 是代理代為核准（MCP-10）：只能允許；授權（類別、每日次數與金額、期限）在同一個交易裡檢查並記下，
// 同時把核准改成允許、結掉原本的核准卡、留一張「已由 ○○ 代為核准」紀錄卡，然後執行動作。
func decideDelegated(ctx context.Context, reg *ops.Registry, call *ops.Call, in DecideIn) (Outcome, error) {
	id := call.Identity
	if in.Decision != "allow" {
		return Outcome{}, ops.Forbidden("代理只能代為允許；要退回請等你本人決定")
	}
	a, err := call.DB.GetApproval(ctx, in.ApprovalID)
	if errors.Is(err, store.ErrNotFound) {
		return Outcome{}, ops.NotFound("找不到這個核准")
	}
	if err != nil {
		return Outcome{}, err
	}
	info, ok := reg.Get(a.Action)
	if !ok {
		return Outcome{}, ops.NotFound("沒有這個操作：" + a.Action)
	}
	amount := reg.Amount(a.Action, a.Input)
	retry := a.Status == "approved" && a.DecidedBy == id.Actor()
	if !retry {
		// 先檢查授權（不記錄）：沒授權的代理拿到「沒有授權」，不是「已經處理過」。
		if _, err := delegation.Authorize(ctx, call.DB.Queries, id, info, a, amount, false); err != nil && (a.Status == "pending" || ops.AsError(err).Code != ops.CodeLimit) {
			return Outcome{}, err
		}
	}
	switch {
	case a.Status == "pending":
	case retry:
		// 這個代理允許過、上次執行失敗：再執行一次（不再計次）。
		if call.DryRun {
			return Outcome{Approval: approvalView(a), Settled: true}, nil
		}
		return execute(ctx, reg, call, a)
	default:
		return Outcome{}, ops.Conflict(store.ErrAlreadyDecided.Error())
	}
	if call.DryRun {
		o := Outcome{Approval: approvalView(a), Settled: true}
		o.Approval.Status, o.Approval.DecidedBy = "approved", id.Actor()
		return o, nil
	}
	var evs []store.Event
	err = call.DB.Tx(ctx, func(q *store.Queries) error {
		use, err := delegation.Authorize(ctx, q, id, info, a, amount, true)
		if err != nil {
			return err
		}
		if err := q.DecideApproval(ctx, a.ID, "approved", id.Actor()); err != nil {
			return err
		}
		items, err := q.ResolveInboxByRef(ctx, a.ID, "allow", id.Actor())
		if err != nil {
			return err
		}
		card, err := delegatedCard(ctx, q, id, info, a, use)
		if err != nil {
			return err
		}
		created, err := q.AppendEvent(ctx, notify.Event(notify.KindCreated, card))
		if err != nil {
			return err
		}
		evs, err = appendUpdated(ctx, q, items...)
		evs = append(evs, created)
		return err
	})
	if errors.Is(err, store.ErrAlreadyDecided) {
		return Outcome{}, ops.Conflict(err.Error())
	}
	if err != nil {
		return Outcome{}, err
	}
	reg.Notify(evs...)
	a.Status, a.DecidedBy = "approved", id.Actor()
	return execute(ctx, reg, call, a)
}

// delegatedPayload 是代為核准紀錄卡的內容。
type delegatedPayload struct {
	Op           string          `json:"op"`
	Summary      string          `json:"summary"`
	Params       json.RawMessage `json:"params"`
	ApprovalID   string          `json:"approval_id"`
	Actor        string          `json:"actor"`
	ActorName    string          `json:"actor_name"`
	Via          string          `json:"via,omitempty"`
	RequestedBy  string          `json:"requested_by"`
	Category     string          `json:"category"`
	CategoryName string          `json:"category_name"`
	AmountMicros int64           `json:"amount_micros,omitempty"`
	Count        int             `json:"count" desc:"今天第幾次"`
	Limit        int             `json:"limit" desc:"每天上限"`
	Reversible   bool            `json:"reversible"`
}

// delegatedCard 建一張「已由 ○○ 代為核准」紀錄卡（C16、F08）：一建立就是處理過的（resolution=delegated），
// 未讀，出現在通知中心與待我處理的「最近的決定」；動作可以復原的話 10 分鐘內可以按「復原」。
func delegatedCard(ctx context.Context, q *store.Queries, id ops.Identity, info ops.Info, a store.Approval, use delegation.Use) (store.InboxItem, error) {
	var p payload
	_ = json.Unmarshal(a.Payload, &p)
	if len(p.Params) == 0 {
		p.Params = json.RawMessage("{}")
	}
	cat := ops.CategoryNames[use.Category]
	via := "MCP"
	if id.Via != "" && id.Via != "mcp" {
		via = strings.ToUpper(id.Via)
	}
	body := fmt.Sprintf("%s · 今天第 %d／%d 次", info.Summary, use.Count, use.Limit)
	if use.AmountMicros > 0 {
		body = fmt.Sprintf("%s · %s · 今天第 %d／%d 次", info.Summary, delegation.USD(use.AmountMicros), use.Count, use.Limit)
	}
	raw, err := json.Marshal(delegatedPayload{Op: info.Name, Summary: info.Summary, Params: p.Params, ApprovalID: a.ID,
		Actor: id.Actor(), ActorName: id.DisplayName(), Via: id.Via, RequestedBy: a.RequestedBy, Category: use.Category, CategoryName: cat,
		AmountMicros: use.AmountMicros, Count: use.Count, Limit: use.Limit, Reversible: info.Reversible})
	if err != nil {
		return store.InboxItem{}, err
	}
	return q.CreateInboxItem(ctx, store.InboxItem{Kind: kindDelegated, BotID: a.BotID, Ref: a.ID,
		Title:  fmt.Sprintf("已由 %s（%s）代為核准 · %s", id.DisplayName(), via, cat),
		Body:   body,
		Status: store.InboxResolved, Resolution: resolutionDelegated, ResolvedBy: id.Actor(), ResolvedAt: clock().UnixMilli(),
		Payload: raw})
}

const (
	kindDelegated       = "delegated"
	resolutionDelegated = "delegated"
	resolutionUndone    = "undone"
)

// ── 待我處理 ─────────────────────────────────────────

func listInbox(ctx context.Context, call *ops.Call, in InboxListIn) (InboxListOut, error) {
	f := store.InboxFilter{Status: string(in.Status), BotID: in.BotID, Unread: in.Unread}
	switch f.Status {
	case "":
		f.Status = store.InboxOpen
	case "all":
		f.Status = ""
	}
	if in.Group != "" {
		f.Kinds = notify.KindsOf(string(in.Group))
	}
	if in.Kind != "" {
		f.Kinds = []string{in.Kind}
	}
	all, err := call.DB.ListInboxFiltered(ctx, f)
	if err != nil {
		return InboxListOut{}, err
	}
	limit := pageLimit(in.Limit)
	out := InboxListOut{Items: []Item{}}
	now := clock().UnixMilli()
	for _, i := range all {
		if len(out.Items) == limit {
			break
		}
		if (in.Group != "" && notify.Group(i.Kind) != string(in.Group)) || (in.ThreadID != "" && i.ThreadID != in.ThreadID) || !visible(call.Identity, i) {
			continue
		}
		v, err := itemView(ctx, call.DB, i, now)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// visible：受資源範圍限制的身分只看得到它能碰的 Bot 的卡；不屬於任何 Bot 的卡只有不受限的身分看得到。
func visible(id ops.Identity, i store.InboxItem) bool {
	if i.BotID != "" {
		return id.CanAccess(ops.ResourceBot, i.BotID)
	}
	return id.Kind == ops.KindOwner || id.Resources.Bots == nil
}

func itemView(ctx context.Context, db *store.DB, i store.InboxItem, now int64) (Item, error) {
	v := Item{ID: i.ID, Kind: i.Kind, Group: notify.Group(i.Kind), BotID: i.BotID, TaskID: i.TaskID, ThreadID: i.ThreadID, Ref: i.Ref,
		Title: i.Title, Body: i.Body, Payload: i.Payload, Status: i.Status, Resolution: i.Resolution, ResolvedBy: i.ResolvedBy,
		ResolvedAt: i.ResolvedAt, Read: i.ReadAt != 0, ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
	if len(v.Payload) == 0 {
		v.Payload = json.RawMessage("{}")
	}
	ok, err := undoable(ctx, db, i)
	if err != nil {
		return v, err
	}
	if until := i.ResolvedAt + limits.UndoWindow.Milliseconds(); ok && until > now {
		v.UndoUntil = until
	}
	return v, nil
}

const resolutionDismissed = "dismissed"

// clock 是現在時間（測試可以換，檢查復原時限）。
var clock = time.Now

// undoable：退回的操作核准（Bot 還在）和你略過的卡可以復原；已經執行的動作、交給 Bot 的回答不行。
func undoable(ctx context.Context, db *store.DB, i store.InboxItem) (bool, error) {
	if i.Status == store.InboxOpen || i.ResolvedBy == "" || i.ResolvedBy == "system" {
		return false, nil
	}
	switch {
	case i.Resolution == resolutionDismissed:
		return true, nil
	case i.Kind == kindDelegated && i.Resolution == resolutionDelegated && i.Ref != "":
		// 代為核准的動作：可以復原（有 Revert）而且已經執行完成才能復原。
		var p delegatedPayload
		if json.Unmarshal(i.Payload, &p) != nil || !p.Reversible {
			return false, nil
		}
		req, err := db.GetOpRequestByApproval(ctx, i.Ref)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return err == nil && req.Status == "ok", err
	case i.Kind == "approval" && i.TaskID == "" && i.Ref != "" && i.Resolution == "deny":
		a, err := db.GetApproval(ctx, i.Ref)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return err == nil && a.Status == "denied", err
	}
	return false, nil
}

func counts(ctx context.Context, call *ops.Call, in CountsIn) (CountsOut, error) {
	all, err := call.DB.ListInboxFiltered(ctx, store.InboxFilter{BotID: in.BotID})
	if err != nil {
		return CountsOut{}, err
	}
	out := CountsOut{Groups: map[string]GroupCount{}}
	for _, g := range notify.Groups {
		out.Groups[g] = GroupCount{}
	}
	for _, i := range all {
		g := notify.Group(i.Kind)
		if g == "" || !visible(call.Identity, i) {
			continue
		}
		c := out.Groups[g]
		if i.Status == store.InboxOpen {
			c.Open++
			out.Open++
		}
		if i.ReadAt == 0 {
			c.Unread++
			out.Unread++
		}
		out.Groups[g] = c
	}
	return out, nil
}

// ownerOnly：已讀、略過、復原是你本人的決定，代理和 Bot 不能代勞（SEC-8）。
func ownerOnly(call *ops.Call) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能處理待我處理的卡")
	}
	return nil
}

// appendUpdated 在交易裡為每張卡寫一筆 inbox.updated 事件。
func appendUpdated(ctx context.Context, q *store.Queries, items ...store.InboxItem) ([]store.Event, error) {
	var evs []store.Event
	for _, it := range items {
		e, err := q.AppendEvent(ctx, notify.Event(notify.KindUpdated, it))
		if err != nil {
			return nil, err
		}
		evs = append(evs, e)
	}
	return evs, nil
}

func markRead(ctx context.Context, reg *ops.Registry, call *ops.Call, in MarkReadIn) (MarkReadOut, error) {
	if err := ownerOnly(call); err != nil {
		return MarkReadOut{}, err
	}
	n := 0
	for _, set := range []bool{len(in.IDs) > 0, in.ThreadID != "", in.All} {
		if set {
			n++
		}
	}
	if n != 1 {
		return MarkReadOut{}, ops.InvalidInput(ops.FieldError{Message: "ids、thread_id、all 要擇一"})
	}
	if call.DryRun {
		return MarkReadOut{}, nil
	}
	var evs []store.Event
	var marked []string
	err := call.DB.Tx(ctx, func(q *store.Queries) error {
		var err error
		if marked, err = q.MarkInboxRead(ctx, in.IDs, in.ThreadID); err != nil {
			return err
		}
		for _, id := range marked {
			it, err := q.GetInboxItem(ctx, id)
			if err != nil {
				return err
			}
			e, err := appendUpdated(ctx, q, it)
			if err != nil {
				return err
			}
			evs = append(evs, e...)
		}
		return nil
	})
	if err != nil {
		return MarkReadOut{}, err
	}
	reg.Notify(evs...)
	return MarkReadOut{Marked: len(marked)}, nil
}

func getItem(ctx context.Context, call *ops.Call, id string) (store.InboxItem, error) {
	it, err := call.DB.GetInboxItem(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(call.Identity, it)) {
		return it, ops.NotFound("找不到這張卡")
	}
	return it, err
}

func dismiss(ctx context.Context, reg *ops.Registry, call *ops.Call, in ItemIn) (ItemOut, error) {
	if err := ownerOnly(call); err != nil {
		return ItemOut{}, err
	}
	it, err := getItem(ctx, call, in.InboxID)
	if err != nil {
		return ItemOut{}, err
	}
	switch {
	case it.Status != store.InboxOpen:
		return ItemOut{}, ops.Conflict(store.ErrAlreadyDecided.Error())
	case it.TaskID != "" && (it.Kind == "approval" || it.Kind == "question" || it.Kind == "secret"):
		return ItemOut{}, ops.Conflict("Bot 正在等這個回覆：請用 xbot.chat.respond 回答，或選不回答（decline）")
	case it.Kind == "approval" && it.Ref != "":
		return ItemOut{}, ops.Conflict("這是要你核准的動作：請用 xbot.approvals.decide 允許或退回")
	}
	if call.DryRun {
		now := clock().UnixMilli()
		it.Status, it.Resolution, it.ResolvedBy, it.ResolvedAt = store.InboxDismissed, resolutionDismissed, call.Identity.Actor(), now
		v, err := itemView(ctx, call.DB, it, now)
		return ItemOut{Item: v}, err
	}
	var evs []store.Event
	err = call.DB.Tx(ctx, func(q *store.Queries) error {
		if err := q.SettleInboxItem(ctx, it.ID, store.InboxDismissed, resolutionDismissed, call.Identity.Actor(), nil); err != nil {
			return err
		}
		cur, err := q.GetInboxItem(ctx, it.ID)
		if err != nil {
			return err
		}
		it = cur
		evs, err = appendUpdated(ctx, q, cur)
		return err
	})
	if errors.Is(err, store.ErrAlreadyDecided) {
		return ItemOut{}, ops.Conflict(err.Error())
	}
	if err != nil {
		return ItemOut{}, err
	}
	reg.Notify(evs...)
	v, err := itemView(ctx, call.DB, it, clock().UnixMilli())
	return ItemOut{Item: v}, err
}

func undo(ctx context.Context, reg *ops.Registry, call *ops.Call, in ItemIn) (ItemOut, error) {
	if err := ownerOnly(call); err != nil {
		return ItemOut{}, err
	}
	it, err := getItem(ctx, call, in.InboxID)
	if err != nil {
		return ItemOut{}, err
	}
	v, err := itemView(ctx, call.DB, it, clock().UnixMilli())
	if err != nil {
		return ItemOut{}, err
	}
	if v.UndoUntil == 0 {
		if ok, _ := undoable(ctx, call.DB, it); ok {
			return ItemOut{}, ops.Conflict("已經超過 10 分鐘，不能復原了")
		}
		return ItemOut{}, ops.Conflict("這個決定不能復原（動作已經執行，或回答已經交給 Bot）")
	}
	if it.Kind == kindDelegated {
		return undoDelegated(ctx, reg, call, it, v)
	}
	if call.DryRun {
		v.Status, v.Resolution, v.ResolvedBy, v.ResolvedAt, v.UndoUntil = store.InboxOpen, "", "", 0, 0
		return ItemOut{Item: v}, nil
	}
	var evs []store.Event
	err = call.DB.Tx(ctx, func(q *store.Queries) error {
		if it.Kind == "approval" && it.Ref != "" && it.TaskID == "" {
			if err := q.ReopenApproval(ctx, it.Ref); err != nil {
				return err
			}
		}
		if err := q.ReopenInboxItem(ctx, it.ID); err != nil {
			return err
		}
		cur, err := q.GetInboxItem(ctx, it.ID)
		if err != nil {
			return err
		}
		it = cur
		evs, err = appendUpdated(ctx, q, cur)
		return err
	})
	if errors.Is(err, store.ErrAlreadyDecided) {
		return ItemOut{}, ops.Conflict("這個決定已經改過了，不能復原")
	}
	if err != nil {
		return ItemOut{}, err
	}
	reg.Notify(evs...)
	v, err = itemView(ctx, call.DB, it, clock().UnixMilli())
	return ItemOut{Item: v}, err
}

// undoDelegated 收回代理代為核准的動作：先把卡改成「已復原」佔住（同時按兩次只會復原一次），
// 再執行操作的 Revert；失敗就把卡改回來，讓你可以再試。
func undoDelegated(ctx context.Context, reg *ops.Registry, call *ops.Call, it store.InboxItem, v Item) (ItemOut, error) {
	if call.DryRun {
		v.Resolution, v.UndoUntil = resolutionUndone, 0
		return ItemOut{Item: v}, nil
	}
	if err := call.DB.SwapInboxResolution(ctx, it.ID, resolutionDelegated, resolutionUndone); errors.Is(err, store.ErrAlreadyDecided) {
		return ItemOut{}, ops.Conflict("這個決定已經復原過了")
	} else if err != nil {
		return ItemOut{}, err
	}
	if err := reg.RevertApproved(ctx, it.Ref, call.Identity); err != nil {
		if e := call.DB.SwapInboxResolution(context.WithoutCancel(ctx), it.ID, resolutionUndone, resolutionDelegated); e != nil {
			return ItemOut{}, errors.Join(err, e)
		}
		return ItemOut{}, err
	}
	var evs []store.Event
	err := call.DB.Tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetInboxItem(ctx, it.ID)
		if err != nil {
			return err
		}
		it = cur
		evs, err = appendUpdated(ctx, q, cur)
		return err
	})
	if err != nil {
		return ItemOut{}, err
	}
	reg.Notify(evs...)
	v, err = itemView(ctx, call.DB, it, clock().UnixMilli())
	return ItemOut{Item: v}, err
}

// ── 稽核 ─────────────────────────────────────────────

func listAudit(ctx context.Context, call *ops.Call, in AuditListIn) (AuditListOut, error) {
	f := store.AuditFilter{Actor: in.Actor, Target: in.Target, Before: in.Before, Limit: pageLimit(in.Limit)}
	if call.Identity.Kind != ops.KindOwner {
		// 代理與 Bot 只看得到自己的呼叫紀錄。
		if in.Actor != "" && in.Actor != call.Identity.Actor() {
			return AuditListOut{}, ops.Forbidden("只能看自己的稽核紀錄")
		}
		f.Actor = call.Identity.Actor()
	}
	es, err := call.DB.ListAudit(ctx, f)
	if err != nil {
		return AuditListOut{}, err
	}
	out := AuditListOut{Entries: es}
	if out.Entries == nil {
		out.Entries = []store.AuditEntry{}
	}
	if len(es) == f.Limit {
		out.NextBefore = es[len(es)-1].ID
	}
	return out, nil
}
