package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/store"
)

var defaultStaleMS = limits.OpRequestStale.Milliseconds()

// 事件種類：每次會改東西的呼叫成功、或送去等核准，都寫一筆（SSE 用）。
const (
	EventOp = "op" // payload：{op, actor, status, request_id, target, approval_id?, inbox_id?}
)

var scopeNames = map[Scope]string{ScopeRead: "查看", ScopeOperate: "交辦工作", ScopeManage: "管理", ScopeAdmin: "系統設定"}
var resourceNames = map[string]string{ResourceBot: "Bot", ResourceTeam: "團隊", ResourceHost: "主機"}

// Invoke 執行一個操作。中介層依序是：身分 → 權限範圍 → 輸入驗證 → 資源範圍 → request_id 去重
// → dry_run → 風險（confirm 送去待我處理）→ 執行 → 稽核 → 發事件。
// 回傳的錯誤一定是 *Error（可用 AsError 取出）。
func (r *Registry) Invoke(ctx context.Context, id Identity, name string, raw json.RawMessage, opts Options) (Result, error) {
	res, err := r.invoke(ctx, id, name, raw, opts)
	if err != nil {
		return Result{Op: name}, err
	}
	return res, nil
}

func (r *Registry) invoke(ctx context.Context, id Identity, name string, raw json.RawMessage, opts Options) (Result, *Error) {
	r.mu.RLock()
	e, ok := r.ops[name]
	policy, limiter := r.cfg.Policy, r.cfg.Limiter
	r.mu.RUnlock()
	if !ok {
		return Result{}, NotFound("沒有這個操作：" + name)
	}
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	id, err := r.withOwned(ctx, id)
	if err != nil {
		return Result{}, Internal(err)
	}
	info := &e.info
	call := &Call{Op: info, Identity: id, RequestID: opts.RequestID, DryRun: opts.DryRun, DB: r.cfg.DB}
	masked := Mask(raw, info.Input)
	audit := auditor{r: r, call: call, params: masked}

	// 1. 身分
	if !id.valid() {
		return Result{}, audit.fail(ctx, Forbidden("不知道你是誰，請重新登入或配對"))
	}
	// 上限（MCP-12：代理的每分鐘呼叫、每日呼叫、每日花費）
	if limiter != nil {
		if lerr := limiter(ctx, id, info); lerr != nil {
			return Result{}, audit.fail(ctx, lerr)
		}
	}
	// 2. 權限範圍
	if !id.HasScope(info.Scope) {
		return Result{}, audit.fail(ctx, Forbidden(fmt.Sprintf("%s 沒有「%s」權限，不能使用 %s", id.DisplayName(), scopeNames[info.Scope], name)))
	}
	// Bot 只能用開放給 Bot 的操作（C11）；product.sock 的 MCP 只列出這些，這裡再擋一次。
	if id.Kind == KindBot && !info.ForBot {
		return Result{}, audit.fail(ctx, Forbidden(fmt.Sprintf("Bot 不能使用 %s", name)))
	}
	// 輸入驗證（資源範圍要讀輸入，所以先驗證）
	if fields := info.Input.Validate(raw); len(fields) > 0 {
		return Result{}, audit.fail(ctx, InvalidInput(fields...))
	}
	// 3. 資源範圍
	refs := e.refs(raw)
	if len(refs) > 0 {
		audit.target = refs[0].id
	}
	for _, ref := range refs {
		if !id.CanAccess(ref.kind, ref.id) {
			return Result{}, audit.fail(ctx, Forbidden(fmt.Sprintf("%s 不能碰這個%s（%s）", id.DisplayName(), resourceNames[ref.kind], ref.id)))
		}
	}
	botID := firstBot(refs)

	// 4. request_id 去重（只針對會改東西、不是預演的呼叫）
	claimed := false
	if opts.RequestID != "" && info.Mutates && !opts.DryRun {
		prev, ok, err := r.cfg.DB.ClaimOpRequest(ctx, id.Actor(), opts.RequestID, name, paramsHash(name, raw), r.cfg.StaleMS)
		if err != nil {
			return Result{}, audit.fail(ctx, Internal(err))
		}
		if !ok {
			return r.replay(ctx, prev, name, paramsHash(name, raw))
		}
		claimed = true
	}
	release := func(err *Error) *Error {
		if claimed {
			if e := r.cfg.DB.ReleaseOpRequest(context.WithoutCancel(ctx), id.Actor(), opts.RequestID); e != nil {
				r.cfg.Logger.Error("釋放 request_id 失敗", "op", name, "request_id", opts.RequestID, "err", e)
			}
		}
		return audit.fail(ctx, err)
	}

	risk := e.risk(raw)

	// 5. dry_run：handler 拿到唯讀資料庫；登錄表自己什麼都不寫。
	if opts.DryRun {
		call.DB = r.cfg.DB.ReadOnly()
		out, err := e.run(ctx, call, raw)
		if err != nil {
			return Result{}, AsError(err)
		}
		res := Result{Op: name, Output: out, DryRun: true}
		if risk == RiskConfirm {
			d, err := policy(ctx, call)
			if err != nil {
				return Result{}, Internal(err)
			}
			res.NeedsApproval = d == NeedApproval
		}
		return res, nil
	}

	// 6. 風險
	if risk == RiskConfirm {
		d, err := policy(ctx, call)
		if err != nil {
			return Result{}, release(Internal(err))
		}
		if d == NeedApproval {
			perr := r.requestApproval(ctx, call, raw, masked, botID, audit.target, claimed)
			if perr.Code != CodePendingApproval {
				return Result{}, release(perr)
			}
			return Result{}, perr
		}
	}

	// 7. 執行
	out, err := e.run(ctx, call, raw)
	if err != nil {
		return Result{}, release(AsError(err))
	}
	// 8. 稽核＋9. 事件（和去重結果一起寫進同一個交易）
	if err := r.finish(ctx, call, botID, audit, out, claimed); err != nil {
		// 動作已經做了，只是紀錄沒寫成；不要讓呼叫端以為失敗而重做。
		r.cfg.Logger.Error("寫入稽核或事件失敗", "op", name, "actor", id.Actor(), "err", err)
	}
	return Result{Op: name, Output: out}, nil
}

// replay 處理同一個身分重送同一個 request_id。
func (r *Registry) replay(ctx context.Context, prev store.OpRequest, name, hash string) (Result, *Error) {
	if prev.Op != name || prev.ParamsHash != hash {
		return Result{}, Conflict("這個 request_id 已經用在別的內容，請換一個新的")
	}
	switch prev.Status {
	case "ok":
		return Result{Op: name, Output: prev.Result, Replayed: true}, nil
	case "pending_approval":
		a, err := r.cfg.DB.GetApproval(ctx, prev.ApprovalID)
		if err != nil {
			return Result{}, Internal(err)
		}
		switch a.Status {
		case "pending", "approved": // approved 但還沒執行完：繼續等
			return Result{}, pendingError(a.ID)
		case "expired":
			return Result{}, Forbidden("這個動作的核准已經過期，請重新送出")
		case "canceled":
			return Result{}, Forbidden("這個動作已經取消")
		default:
			return Result{}, Forbidden("你沒有允許這個動作")
		}
	default: // running
		e := Conflict("同一個請求還在處理中，請稍後再試")
		e.Retryable = true
		return Result{}, e
	}
}

func pendingError(approvalID string) *Error {
	return &Error{Code: CodePendingApproval, Message: "已送到「待我處理」，等你按允許後才會執行", NeedsApprovalID: approvalID}
}

// approvalPayload 是 approval.payload 的內容（給介面顯示，已遮蔽密鑰）。
type approvalPayload struct {
	Op        string          `json:"op"`
	Summary   string          `json:"summary"`
	Params    json.RawMessage `json:"params"`
	Identity  Identity        `json:"identity"`
	RequestID string          `json:"request_id,omitempty"`
}

// requestApproval 建立核准與待我處理卡片，回傳 pending_approval 錯誤（失敗時回其他錯誤）。
func (r *Registry) requestApproval(ctx context.Context, call *Call, raw, masked json.RawMessage, botID, target string, claimed bool) *Error {
	t, err := r.createApproval(ctx, call, raw, masked, botID, target, claimed, ApprovalCard{})
	if err != nil {
		return AsError(err)
	}
	return pendingError(t.ApprovalID)
}

// ApprovalCard 是核准卡片的顯示內容；空白欄位用預設（「<誰> 想要：<操作說明>」）。
type ApprovalCard struct {
	Title, Body string
	// Params 是卡片上顯示的參數（已遮蔽）；空白用操作的輸入。
	Params json.RawMessage
	// ThreadID、TaskID：卡片屬於哪條對話串、哪件工作（Bot 在對話裡要求的核准）。
	ThreadID, TaskID string
}

// ApprovalTicket 是建立好的核准與它的待我處理卡片。
type ApprovalTicket struct {
	ApprovalID string
	InboxID    string
}

// RequestApproval 由操作自己建立一個核准（例：Bot 用 xbot.approvals.request 請你核准一件事，C11）：
// 和 confirm 級操作走同一條路（approval、待我處理卡片、inbox.created 事件、稽核、op 事件）。
// 你允許後，ExecuteApproved 會以 call.ApprovalID 不是空白再執行一次這個操作。
// call 是目前這次呼叫；raw 是核准後執行時要用的輸入。DryRun 時不能呼叫。
func (r *Registry) RequestApproval(ctx context.Context, call *Call, raw json.RawMessage, botID string, card ApprovalCard) (ApprovalTicket, error) {
	if call.DryRun {
		return ApprovalTicket{}, Internal(errors.New("預演時不能建立核准"))
	}
	masked := Mask(raw, call.Op.Input)
	t, err := r.createApproval(ctx, call, raw, masked, botID, botID, false, card)
	if err != nil {
		return t, AsError(err)
	}
	return t, nil
}

func (r *Registry) createApproval(ctx context.Context, call *Call, raw, masked json.RawMessage, botID, target string, claimed bool, c ApprovalCard) (ApprovalTicket, error) {
	id, info := call.Identity, call.Op
	var t ApprovalTicket
	var ev, cardEv *store.Event
	if c.Title == "" {
		c.Title = fmt.Sprintf("%s 想要：%s", id.DisplayName(), info.Summary)
	}
	if c.Body == "" {
		c.Body = "要允許嗎？按允許才會執行。"
	}
	if len(c.Params) == 0 {
		c.Params = masked
	}
	err := r.cfg.DB.Tx(ctx, func(q *store.Queries) error {
		bot, err := existingBot(ctx, q, botID)
		if err != nil {
			return err
		}
		if botID != "" && bot == "" {
			return NotFound("找不到這個 Bot")
		}
		payload, err := json.Marshal(approvalPayload{Op: info.Name, Summary: info.Summary, Params: c.Params, Identity: id, RequestID: call.RequestID})
		if err != nil {
			return err
		}
		a, err := q.CreateApproval(ctx, store.Approval{BotID: bot, Action: info.Name, Payload: payload, Input: raw,
			RequestedBy: id.Actor(), RequestID: call.RequestID})
		if err != nil {
			return err
		}
		t.ApprovalID = a.ID
		card, err := json.Marshal(map[string]any{"op": info.Name, "params": c.Params, "approval_id": a.ID, "actor": id.Actor(),
			"actor_name": id.DisplayName(), "via": id.Via, "request_id": call.RequestID, "category": info.Category,
			"delegable": info.Delegable(), "reversible": info.Reversible})
		if err != nil {
			return err
		}
		item, err := q.CreateInboxItem(ctx, store.InboxItem{Kind: "approval", BotID: bot, Ref: a.ID, ThreadID: c.ThreadID, TaskID: c.TaskID,
			Title: c.Title, Body: c.Body, Payload: card})
		if err != nil {
			return err
		}
		t.InboxID = item.ID
		created, err := q.AppendEvent(ctx, notify.Event(notify.KindCreated, item))
		if err != nil {
			return err
		}
		cardEv = &created
		// 沒帶 request_id 也記一筆，讓核准後執行只會做一次（StartApprovedOpRequest）。
		reqID := call.RequestID
		if !claimed {
			reqID = "approval:" + a.ID
			if _, _, err := q.ClaimOpRequest(ctx, id.Actor(), reqID, info.Name, paramsHash(info.Name, raw), r.cfg.StaleMS); err != nil {
				return err
			}
		}
		if err := q.FinishOpRequest(ctx, id.Actor(), reqID, "pending_approval", nil, a.ID); err != nil {
			return err
		}
		if _, err := q.AddAudit(ctx, store.AuditEntry{Actor: id.Actor(), Via: id.Via, Op: info.Name, RequestID: call.RequestID,
			Target: target, Params: masked, Status: "pending_approval", ErrorCode: string(CodePendingApproval)}); err != nil {
			return err
		}
		ev, err = addEvent(ctx, q, bot, map[string]any{"op": info.Name, "actor": id.Actor(), "actor_name": id.DisplayName(), "via": id.Via, "status": "pending_approval",
			"request_id": call.RequestID, "target": target, "approval_id": a.ID, "inbox_id": item.ID})
		return err
	})
	if err != nil {
		return ApprovalTicket{}, err
	}
	r.notify(cardEv)
	r.notify(ev)
	return t, nil
}

// ExecuteApproved 在使用者允許之後執行原本被擋下的動作（approvals.decide 呼叫，C05）。
// 只會執行一次：按兩次允許，第二次回傳第一次的結果（Replayed）。執行失敗時可以再呼叫重試。
func (r *Registry) ExecuteApproved(ctx context.Context, approvalID string) (Result, error) {
	res, err := r.executeApproved(ctx, approvalID)
	if err != nil {
		return res, err
	}
	return res, nil
}

func (r *Registry) executeApproved(ctx context.Context, approvalID string) (Result, *Error) {
	a, err := r.cfg.DB.GetApproval(ctx, approvalID)
	if errors.Is(err, store.ErrNotFound) {
		// 核准跟著 Bot 一起刪掉了（approval.bot_id 是 ON DELETE CASCADE，例：核准的動作就是刪這個 Bot）。
		// 已經執行過就回第一次的結果。
		if prev, e := r.cfg.DB.GetOpRequestByApproval(ctx, approvalID); e == nil && prev.Status == "ok" {
			return Result{Op: prev.Op, Output: prev.Result, Replayed: true}, nil
		}
	}
	if err != nil {
		return Result{}, AsError(err)
	}
	if a.Status != "approved" {
		return Result{}, Conflict("這個動作還沒有被允許")
	}
	r.mu.RLock()
	e, ok := r.ops[a.Action]
	r.mu.RUnlock()
	if !ok {
		return Result{}, NotFound("沒有這個操作：" + a.Action)
	}
	var p approvalPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return Result{}, Internal(err)
	}
	prev, started, err := r.cfg.DB.StartApprovedOpRequest(ctx, approvalID)
	if err != nil {
		return Result{}, AsError(err)
	}
	if !started {
		if prev.Status == "ok" {
			return Result{Op: a.Action, Output: prev.Result, Replayed: true}, nil
		}
		e := Conflict("這個動作正在執行中")
		e.Retryable = true
		return Result{}, e
	}
	call := &Call{Op: &e.info, Identity: p.Identity, RequestID: a.RequestID, ApprovalID: a.ID, DB: r.cfg.DB}
	refs := e.refs(a.Input)
	audit := auditor{r: r, call: call, params: Mask(a.Input, e.info.Input)}
	if len(refs) > 0 {
		audit.target = refs[0].id
	}
	out, err := e.run(ctx, call, a.Input)
	if err != nil {
		// 退回等待狀態，讓之後可以再試；記下錯誤，approvals.wait 才分得出「允許了但失敗」。
		bg := context.WithoutCancel(ctx)
		if e := r.cfg.DB.FinishOpRequest(bg, prev.Actor, prev.RequestID, "pending_approval", nil, approvalID); e != nil {
			r.cfg.Logger.Error("退回核准狀態失敗", "approval", approvalID, "err", e)
		}
		oe := AsError(err)
		if raw, e := json.Marshal(oe); e == nil {
			if e := r.cfg.DB.SetApprovalError(bg, approvalID, string(raw)); e != nil && !errors.Is(e, store.ErrNotFound) {
				r.cfg.Logger.Error("記錄核准執行失敗", "approval", approvalID, "err", e)
			}
		}
		return Result{}, audit.fail(ctx, oe)
	}
	if a.Error != "" {
		if e := r.cfg.DB.SetApprovalError(context.WithoutCancel(ctx), approvalID, ""); e != nil && !errors.Is(e, store.ErrNotFound) {
			r.cfg.Logger.Error("清除核准錯誤失敗", "approval", approvalID, "err", e)
		}
	}
	audit.requestID = prev.RequestID
	if err := r.finish(ctx, call, firstBot(refs), audit, out, true); err != nil {
		r.cfg.Logger.Error("寫入稽核或事件失敗", "op", a.Action, "approval", approvalID, "err", err)
	}
	return Result{Op: a.Action, Output: out}, nil
}

// withOwned 把代理（或 Bot）自己建立的 Bot 加進它能碰的範圍（MCP-8「預設只能碰自己建立的東西」）。
// 只在 Bot 範圍有限制時才查；不限制（nil）的不用加。
func (r *Registry) withOwned(ctx context.Context, id Identity) (Identity, error) {
	if id.Kind == KindOwner || id.Resources.Bots == nil || r.cfg.DB == nil || !id.valid() {
		return id, nil
	}
	own, err := r.cfg.DB.ListBotIDsCreatedBy(ctx, id.Actor())
	if err != nil || len(own) == 0 {
		return id, err
	}
	bots := append([]string{}, id.Resources.Bots...)
	for _, b := range own {
		if !slices.Contains(bots, b) {
			bots = append(bots, b)
		}
	}
	id.Resources.Bots = bots
	return id, nil
}

// RevertApproved 復原一個已經允許並執行完的動作（代為核准的紀錄卡 10 分鐘內按「復原」，MCP-10）。
// by 是按復原的人（寫進稽核）。動作沒有 Revert、還沒執行完、或已經復原過時回 conflict。
// 時限與「已經復原過」由呼叫端（inbox.undo）在同一張卡上檢查。
func (r *Registry) RevertApproved(ctx context.Context, approvalID string, by Identity) error {
	if err := r.revertApproved(ctx, approvalID, by); err != nil {
		return err
	}
	return nil
}

func (r *Registry) revertApproved(ctx context.Context, approvalID string, by Identity) *Error {
	a, err := r.cfg.DB.GetApproval(ctx, approvalID)
	if err != nil {
		return AsError(err)
	}
	r.mu.RLock()
	e, ok := r.ops[a.Action]
	r.mu.RUnlock()
	if !ok {
		return NotFound("沒有這個操作：" + a.Action)
	}
	if e.revert == nil {
		return Conflict("這個動作做了就不能復原")
	}
	prev, err := r.cfg.DB.GetOpRequestByApproval(ctx, approvalID)
	if err != nil || prev.Status != "ok" {
		return Conflict("這個動作還沒執行完，不能復原")
	}
	call := &Call{Op: &e.info, Identity: by, RequestID: prev.RequestID, ApprovalID: a.ID, DB: r.cfg.DB}
	refs := e.refs(a.Input)
	audit := auditor{r: r, call: call, params: Mask(a.Input, e.info.Input)}
	if len(refs) > 0 {
		audit.target = refs[0].id
	}
	if err := e.revert(ctx, call, a.Input, prev.Result); err != nil {
		return audit.fail(ctx, AsError(err))
	}
	bg := context.WithoutCancel(ctx)
	var ev *store.Event
	err = r.cfg.DB.Tx(bg, func(q *store.Queries) error {
		if _, err := q.AddAudit(bg, audit.entry("reverted", "")); err != nil {
			return err
		}
		bot, err := existingBot(bg, q, firstBot(refs))
		if err != nil {
			return err
		}
		ev, err = addEvent(bg, q, bot, map[string]any{"op": e.info.Name, "actor": by.Actor(), "actor_name": by.DisplayName(), "via": by.Via,
			"status": "reverted", "approval_id": a.ID, "target": audit.target})
		return err
	})
	if err != nil {
		r.cfg.Logger.Error("寫入復原紀錄失敗", "op", e.info.Name, "approval", approvalID, "err", err)
		return nil
	}
	r.notify(ev)
	return nil
}

// finish 在成功後寫：去重結果、稽核、事件。
func (r *Registry) finish(ctx context.Context, call *Call, botID string, audit auditor, out json.RawMessage, claimed bool) error {
	ctx = context.WithoutCancel(ctx) // 動作已經做了，紀錄一定要寫完
	id, info := call.Identity, call.Op
	reqID := call.RequestID
	if audit.requestID != "" {
		reqID = audit.requestID
	}
	var ev *store.Event
	err := r.cfg.DB.Tx(ctx, func(q *store.Queries) error {
		if claimed {
			if err := q.FinishOpRequest(ctx, id.Actor(), reqID, "ok", out, call.ApprovalID); err != nil {
				return err
			}
		}
		if audit.enabled() {
			if _, err := q.AddAudit(ctx, audit.entry("ok", "")); err != nil {
				return err
			}
		}
		if !info.Mutates {
			return nil
		}
		bot, err := existingBot(ctx, q, botID)
		if err != nil {
			return err
		}
		payload := map[string]any{"op": info.Name, "actor": id.Actor(), "actor_name": id.DisplayName(), "via": id.Via,
			"status": "ok", "request_id": call.RequestID, "target": audit.target}
		if call.ApprovalID != "" {
			payload["approval_id"] = call.ApprovalID
		}
		ev, err = addEvent(ctx, q, bot, payload)
		return err
	})
	if err == nil {
		r.notify(ev)
	}
	return err
}

// addEvent 在交易裡寫一筆 op 事件；交易提交後再由呼叫端 notify。
func addEvent(ctx context.Context, q *store.Queries, botID string, payload map[string]any) (*store.Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	e, err := q.AppendEvent(ctx, store.Event{BotID: botID, Kind: EventOp, Payload: b})
	return &e, err
}

func (r *Registry) notify(ev *store.Event) {
	if ev != nil && r.cfg.Notify != nil {
		r.cfg.Notify(*ev)
	}
}

// Notify 通知已經寫進 event 表的事件（SSE 用）；操作自己寫事件時（例：待我處理卡片被處理）在交易提交後呼叫。
func (r *Registry) Notify(evs ...store.Event) {
	for i := range evs {
		r.notify(&evs[i])
	}
}

// existingBot 回傳存在的 Bot id；不存在（例：剛被刪掉）回空白，事件就記成全域的。
func existingBot(ctx context.Context, q *store.Queries, botID string) (string, error) {
	if botID == "" {
		return "", nil
	}
	_, err := q.GetBot(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return botID, nil
}

func firstBot(refs []resourceRef) string {
	for _, ref := range refs {
		if ref.kind == ResourceBot {
			return ref.id
		}
	}
	return ""
}

// auditor 寫稽核紀錄（MCP-13）。參數一律是遮蔽後的內容。
type auditor struct {
	r         *Registry
	call      *Call
	params    json.RawMessage
	target    string
	requestID string
}

// enabled：會改東西的呼叫都記；查看類只記代理和 Bot 的（使用者自己在介面上看東西不記，避免洗版）。
func (a auditor) enabled() bool {
	return a.call.Op.Mutates || a.call.Identity.Kind != KindOwner
}

func (a auditor) entry(status, code string) store.AuditEntry {
	reqID := a.call.RequestID
	if a.requestID != "" {
		reqID = a.requestID
	}
	return store.AuditEntry{Actor: a.call.Identity.Actor(), Via: a.call.Identity.Via, Op: a.call.Op.Name, RequestID: reqID,
		Target: a.target, Params: a.params, Status: status, ErrorCode: code}
}

// fail 記下失敗的呼叫（預演不記），回傳同一個錯誤。
func (a auditor) fail(ctx context.Context, e *Error) *Error {
	if e.Code == CodeInternal {
		a.r.cfg.Logger.Error("操作失敗", "op", a.call.Op.Name, "actor", a.call.Identity.Actor(), "err", e)
	}
	if a.call.DryRun || !(a.enabled() || e.Code == CodeForbidden) {
		return e
	}
	if _, err := a.r.cfg.DB.AddAudit(context.WithoutCancel(ctx), a.entry("error", string(e.Code))); err != nil {
		a.r.cfg.Logger.Error("寫入稽核失敗", "op", a.call.Op.Name, "err", err)
	}
	return e
}
