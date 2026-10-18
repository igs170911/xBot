// Package inbox 是「待我處理」、核准與稽核的操作（PRD §6.11 IN-*、MCP-9、MCP-10、MCP-13）：
// approvals.list、approvals.wait、approvals.decide、inbox.list、audit.list。
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"xbot/internal/limits"
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
	// Settled：已經決定了（允許時還要等動作執行完）。false 表示等到逾時、還沒決定。
	Settled  bool            `json:"settled"`
	Executed bool            `json:"executed" desc:"允許後動作已經執行完成"`
	Output   json.RawMessage `json:"output,omitempty" desc:"動作的輸出（executed 時）"`
	Error    *ops.Error      `json:"error,omitempty" desc:"允許了但執行失敗時的錯誤（可以再允許一次重試）"`
}

// DecideIn 是 xbot.approvals.decide 的輸入。
type DecideIn struct {
	ApprovalID string `json:"approval_id" required:"true" max:"64"`
	Decision   string `json:"decision" required:"true" enum:"allow,deny" desc:"allow：允許這次並執行；deny：退回"`
}

// InboxStatus 是待我處理的狀態篩選。
type InboxStatus string

// EnumValues 讓 JSON Schema 列出可用值。
func (InboxStatus) EnumValues() []string { return []string{"open", "resolved", "dismissed", "all"} }

// Item 是一張待我處理。
type Item struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind" desc:"approval、question、secret、signin、takeover、proposal、discussion、routine_failed、budget"`
	BotID      string          `json:"bot_id,omitempty"`
	Ref        string          `json:"ref,omitempty" desc:"例：核准的 id"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	ResolvedBy string          `json:"resolved_by,omitempty"`
	ResolvedAt int64           `json:"resolved_at,omitempty"`
	CreatedAt  int64           `json:"created_at"`
}

// InboxListIn 是 xbot.inbox.list 的輸入。
type InboxListIn struct {
	Status InboxStatus `json:"status,omitempty" desc:"預設 open（還沒處理的）"`
	Kind   string      `json:"kind,omitempty" max:"32" desc:"只列這一種"`
	BotID  string      `json:"bot_id,omitempty" resource:"bot" max:"64"`
	Limit  int         `json:"limit,omitempty" min:"1" max:"200" desc:"最多幾筆；預設 50"`
}

// InboxListOut 是 xbot.inbox.list 的輸出。
type InboxListOut struct {
	Items []Item `json:"items" desc:"新到舊"`
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
func Register(reg *ops.Registry) {
	ops.Register(reg, ops.Op[ApprovalsListIn, ApprovalsListOut]{
		Name: "xbot.approvals.list", Summary: "列出等你核准的動作",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: listApprovals,
	})
	ops.Register(reg, ops.Op[WaitIn, Outcome]{
		Name: "xbot.approvals.wait", Summary: "等一個核准的結果",
		Description: "收到 pending_approval 後用它等：使用者允許並執行完、退回、或逾時（settled=false，可以再呼叫）。" +
			"代理只能等自己要求的核准。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: wait,
	})
	ops.Register(reg, ops.Op[DecideIn, Outcome]{
		Name: "xbot.approvals.decide", Summary: "允許或退回一個等核准的動作",
		Description: "允許後立刻執行原本的動作並回傳結果。目前只有你本人能決定；代為核准（MCP-10）之後才開放給有授權的代理。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DecideIn) (Outcome, error) {
			return decide(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[InboxListIn, InboxListOut]{
		Name: "xbot.inbox.list", Summary: "列出「待我處理」",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: listInbox,
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

// canSee：你本人看得到全部；代理與 Bot 只看得到自己要求的核准。
func canSee(id ops.Identity, a store.Approval) bool {
	return id.Kind == ops.KindOwner || a.RequestedBy == id.Actor()
}

func listApprovals(ctx context.Context, call *ops.Call, in ApprovalsListIn) (ApprovalsListOut, error) {
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
		if !canSee(call.Identity, a) || (in.BotID != "" && a.BotID != in.BotID) {
			continue
		}
		out.Approvals = append(out.Approvals, approvalView(a))
	}
	return out, nil
}

func getApproval(ctx context.Context, call *ops.Call, id string) (store.Approval, error) {
	a, err := call.DB.GetApproval(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !canSee(call.Identity, a)) {
		return a, ops.NotFound("找不到這個核准")
	}
	return a, err
}

// outcome 整理核准目前的結果；允許了的話看動作有沒有執行完（op_request 是 ok）。
func outcome(ctx context.Context, db *store.DB, a store.Approval) (Outcome, error) {
	o := Outcome{Approval: approvalView(a), Settled: a.Status != "pending"}
	if a.Status != "approved" {
		return o, nil
	}
	req, err := db.GetOpRequestByApproval(ctx, a.ID)
	if errors.Is(err, store.ErrNotFound) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	if req.Status == "ok" {
		o.Executed, o.Output = true, req.Result
	}
	return o, nil
}

func wait(ctx context.Context, call *ops.Call, in WaitIn) (Outcome, error) {
	a, err := getApproval(ctx, call, in.ApprovalID)
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
		// 允許了但還沒執行完（使用者那邊正在執行）就再等一下。
		done := o.Settled && (a.Status != "approved" || o.Executed)
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
	// MCP-9／MCP-10：核准只認你本人；代為核准授權（C21）做好之前，代理和 Bot 一律不能決定。
	if call.Identity.Kind != ops.KindOwner {
		return Outcome{}, ops.Forbidden("只有你本人能核准；代理請用 xbot.approvals.wait 等結果")
	}
	a, err := getApproval(ctx, call, in.ApprovalID)
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
		status := "denied"
		if allow {
			status = "approved"
		}
		err := call.DB.Tx(ctx, func(q *store.Queries) error {
			if err := q.DecideApproval(ctx, a.ID, status, call.Identity.Actor()); err != nil {
				return err
			}
			_, err := q.ResolveInboxByRef(ctx, a.ID, call.Identity.Actor())
			return err
		})
		if err != nil {
			return Outcome{}, err
		}
	}
	if !allow {
		a, err = call.DB.GetApproval(ctx, a.ID)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Approval: approvalView(a), Settled: true}, nil
	}
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
		o.Error = ops.AsError(execErr)
		return o, nil
	}
	o.Executed, o.Output = true, res.Output
	return o, nil
}

func listInbox(ctx context.Context, call *ops.Call, in InboxListIn) (InboxListOut, error) {
	status := string(in.Status)
	switch status {
	case "":
		status = "open"
	case "all":
		status = ""
	}
	all, err := call.DB.ListInbox(ctx, status)
	if err != nil {
		return InboxListOut{}, err
	}
	limit := pageLimit(in.Limit)
	id := call.Identity
	out := InboxListOut{Items: []Item{}}
	for _, i := range all {
		if len(out.Items) == limit {
			break
		}
		if (in.Kind != "" && i.Kind != in.Kind) || (in.BotID != "" && i.BotID != in.BotID) {
			continue
		}
		// 受資源範圍限制的身分只看得到它能碰的 Bot 的卡；不屬於任何 Bot 的卡只有不受限的身分看得到。
		if i.BotID != "" && !id.CanAccess(ops.ResourceBot, i.BotID) {
			continue
		}
		if i.BotID == "" && id.Kind != ops.KindOwner && id.Resources.Bots != nil {
			continue
		}
		out.Items = append(out.Items, Item{ID: i.ID, Kind: i.Kind, BotID: i.BotID, Ref: i.Ref, Title: i.Title, Body: i.Body,
			Payload: i.Payload, Status: i.Status, ResolvedBy: i.ResolvedBy, ResolvedAt: i.ResolvedAt, CreatedAt: i.CreatedAt})
	}
	return out, nil
}

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
