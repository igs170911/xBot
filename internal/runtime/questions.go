package runtime

// Bot 在工作中等人回覆（核准指令、補充問題、密碼或驗證碼）時，開一張「待我處理」卡（PRD IN-1、IN-2、C08～C10）；
// 從對話或待我處理回覆都走 Respond（xbot.chat.respond），用 Driver.Respond 交給引擎、把卡結掉。
// 無人在場的工作（不是你交辦的）提問 10 分鐘沒回就過期：核准自動拒絕、提問告訴 Bot 不要猜（LazyBoy expire_question）。
// 密碼類的值只交給引擎，不寫進卡片、事件或稽核（SEC-6）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// 卡片的處理方式（inbox_item.resolution）。
const (
	ResolutionOnce       = "once"
	ResolutionAlways     = "always"
	ResolutionDeny       = "deny"
	ResolutionAnswered   = "answered"
	ResolutionProvided   = "provided"
	ResolutionDeclined   = "declined"
	ResolutionExpired    = "expired"
	ResolutionGone       = "gone"     // Bot 已經不等了（工作結束、服務重開）
	ResolutionCanceled   = "canceled" // 引擎自己取消了這個請求
	ResolutionAskedAgain = "asked_again"
	ResolutionInfo       = "info" // 通知類的卡（完成、出錯），不用處理
)

// 回覆的錯誤；chat.respond 依它們回 conflict／invalid_input。
var (
	// ErrAnswered 表示這張卡已經處理過了。
	ErrAnswered = errors.New("這一項已經處理過了")
	// ErrRequestGone 表示 Bot 已經不等這個回覆了（工作結束或服務重開過）。
	ErrRequestGone = errors.New("這個問題已結束，Bot 沒有在等了")
	// ErrNotQuestion 表示這張卡不是 Bot 在對話裡的提問（例：操作的核准要用 xbot.approvals.decide）。
	ErrNotQuestion = errors.New("這一項不是 Bot 在對話裡的提問")
	// ErrNotExpired 表示只有過期的提問才能請它再問一次。
	ErrNotExpired = errors.New("只有過期的提問才能請它再問一次")
)

// InvalidAnswer 是回覆的內容不對（例：核准卡回了 always 但這個指令不能一律允許）。
type InvalidAnswer struct{ Field, Message string }

func (e *InvalidAnswer) Error() string { return e.Message }

// NeedsPayload 是 chat.needs_approval 事件的內容：引擎的請求，加上它的待我處理卡。
type NeedsPayload struct {
	engine.Event
	InboxID   string `json:"inbox_id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// AnsweredPayload 是 chat.answered 事件的內容。Text 是對話裡顯示的那一行（密碼類只寫「已提供」）。
type AnsweredPayload struct {
	InboxID    string `json:"inbox_id"`
	RequestID  string `json:"request_id"`
	Kind       string `json:"kind"`
	Resolution string `json:"resolution"`
	Text       string `json:"text"`
	By         string `json:"by"`
}

// Answer 是對 Bot 提問的回覆。
type Answer struct {
	// Choice：核准卡用 once（允許這次）／always（一律允許）／deny（拒絕）。
	Choice string
	// Text：提問的回答（點選項就是選項的文字）。
	Text string
	// Answers：多個問題時逐題的回答。
	Answers []string
	// Value／Values：密碼類的值（Values 給帳號＋密碼這種多欄位的）。只交給引擎。
	Value  string
	Values map[string]string
	// Decline：不回答（核准＝拒絕、提問＝略過、密碼＝不提供）。
	Decline bool
}

// pending 是 Bot 正在等的一個回覆。
type pending struct {
	reqID   string
	kind    string // approval / question / secret
	method  string
	inboxID string
	choices []string // 核准可用的選項（once／always／deny）
	nq      int      // 提問的題數
	req     *engine.Request
	expires int64
	timer   *time.Timer
}

func (p *pending) stop() {
	if p.timer != nil {
		p.timer.Stop()
	}
}

// cardKind 把引擎的請求種類對到卡片種類。
func cardKind(k string) string {
	switch k {
	case "approval":
		return "approval"
	case "secret":
		return "secret"
	}
	return "question"
}

// cardQuestion 是卡片上的一題。
type cardQuestion struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
}

// cardField 是密碼卡的一個輸入欄（照 LazyBoy human_request 的 fields）。
type cardField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // text / password / code
}

// cardPayload 是 Bot 提問卡的 payload（給介面畫卡片）。不含任何使用者輸入的密碼類值。
type cardPayload struct {
	RequestID   string         `json:"request_id"`
	Method      string         `json:"method,omitempty"`
	Prompt      string         `json:"prompt"`
	Command     string         `json:"command,omitempty"`
	Description string         `json:"description,omitempty"`
	Choices     []string       `json:"choices,omitempty"` // 核准：once／always／deny；單題提問：選項文字
	Questions   []cardQuestion `json:"questions,omitempty"`
	Fields      []cardField    `json:"fields,omitempty"`
	Unattended  bool           `json:"unattended"`
	BotName     string         `json:"bot_name,omitempty"`
	ThreadTitle string         `json:"thread_title,omitempty"`
	// Answer 是回答後顯示的那一行（提問的回答文字；密碼類永遠不填）。
	Answer string `json:"answer,omitempty"`
}

// openCard 為引擎的請求開一張待我處理卡，記下 Bot 在等它。開卡失敗只記錄，不影響工作（仍可在對話裡看到「在等你」）。
func (r *Runtime) openCard(t *turn, req *engine.Request) store.InboxItem {
	if req == nil {
		return store.InboxItem{}
	}
	ctx := context.Background()
	kind := cardKind(req.Kind)
	var params struct {
		Command     string         `json:"command"`
		Description string         `json:"description"`
		Prompt      string         `json:"prompt"`
		EnvVar      string         `json:"env_var"`
		Hint        string         `json:"hint"`
		Site        string         `json:"site"`
		DisplayName string         `json:"display_name"`
		Questions   []cardQuestion `json:"questions"`
	}
	_ = json.Unmarshal(req.Params, &params)
	p := cardPayload{RequestID: req.ID, Method: req.Method, Prompt: req.Prompt, Command: params.Command, Description: params.Description,
		Unattended: t.source != "owner", BotName: t.botName}
	if th, err := r.o.DB.GetThread(ctx, t.threadID); err == nil {
		p.ThreadTitle = th.Title
	}
	pend := &pending{reqID: req.ID, kind: kind, method: req.Method}
	var title string
	switch kind {
	case "approval":
		// Hermes 只在這個指令可以加進允許清單時給 always（LazyBoy human_request）。
		pend.choices = []string{ResolutionOnce}
		for _, c := range req.Choices {
			if c == ResolutionAlways {
				pend.choices = append(pend.choices, ResolutionAlways)
			}
		}
		pend.choices = append(pend.choices, ResolutionDeny)
		p.Choices = pend.choices
		title = t.botName + "想執行指令"
	case "secret":
		p.Fields = secretFields(req.Method, firstNonEmpty(params.Hint, params.Site, params.DisplayName, params.Prompt, params.EnvVar, req.Prompt))
		title = t.botName + "需要你輸入資料"
	default:
		p.Questions = params.Questions
		pend.nq = len(params.Questions)
		if len(params.Questions) <= 1 {
			p.Choices = req.Choices
		}
		title = t.botName + "有問題問你"
	}
	raw, _ := json.Marshal(p)
	item := store.InboxItem{Kind: kind, BotID: t.botID, TaskID: t.taskID, ThreadID: t.threadID, Ref: req.ID,
		Title: title, Body: req.Prompt, Payload: raw}
	if p.Unattended {
		item.ExpiresAt = time.Now().Add(r.o.QuestionTTL).UnixMilli()
	}

	t.mu.Lock()
	prev := t.pending
	t.pending = nil
	t.mu.Unlock()
	if prev != nil {
		// 同一輪又問了新的問題：舊的那張 Bot 已經不等了。
		prev.stop()
		r.settle(t, prev, store.InboxDismissed, ResolutionGone, "system", nil, "")
	}

	created, err := r.o.DB.CreateInboxItem(ctx, item)
	if err != nil {
		r.log.Warn("開待我處理卡失敗", "task", t.taskID, "err", err)
		return store.InboxItem{}
	}
	pend.inboxID, pend.req, pend.expires = created.ID, req, created.ExpiresAt
	r.emitStored(t, notify.Event(notify.KindCreated, created))
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		r.settle(t, pend, store.InboxDismissed, ResolutionGone, "system", nil, "")
		return created
	}
	t.pending = pend
	if p.Unattended {
		pend.timer = time.AfterFunc(r.o.QuestionTTL, func() { r.expire(t, pend) })
	}
	t.mu.Unlock()
	return created
}

// secretFields 是密碼卡要的輸入欄（LazyBoy human_request）。
func secretFields(method, label string) []cardField {
	switch method {
	case "vault.save_login":
		return []cardField{{Name: "identifier", Label: "帳號", Kind: "text"}, {Name: "password", Label: "密碼", Kind: "password"}}
	case "vault.code":
		return []cardField{{Name: "value", Label: firstNonEmpty(label, "驗證碼"), Kind: "code"}}
	case "vault.unlock_prompt":
		return []cardField{{Name: "value", Label: firstNonEmpty(label, "解鎖密碼"), Kind: "password"}}
	}
	return []cardField{{Name: "value", Label: firstNonEmpty(label, "秘密輸入"), Kind: "password"}}
}

// claim 取走 Bot 正在等的回覆（itemID 那張卡的）；不是在等它時回 nil。
func (t *turn) claim(itemID string) *pending {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.pending
	if t.finished || p == nil || p.inboxID != itemID {
		return nil
	}
	t.pending = nil
	return p
}

// release 把沒送出去的回覆放回去（Driver.Respond 失敗時），之後可以再回一次。
func (t *turn) release(p *pending) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.finished && t.pending == nil {
		t.pending = p
	}
}

// Respond 回覆一張 Bot 提問卡（xbot.chat.respond）。by 是回覆的人（owner）。
// 成功後卡片結掉（resolved），對話串發布 chat.answered，任務回到工作中。
func (r *Runtime) Respond(ctx context.Context, itemID string, a Answer, by string) (store.InboxItem, error) {
	it, err := r.o.DB.GetInboxItem(ctx, itemID)
	if err != nil {
		return it, err
	}
	if it.TaskID == "" || (it.Kind != "approval" && it.Kind != "question" && it.Kind != "secret") {
		return it, ErrNotQuestion
	}
	if it.Status != store.InboxOpen {
		return it, ErrAnswered
	}
	r.mu.Lock()
	t := r.active[it.BotID]
	r.mu.Unlock()
	var p *pending
	if t != nil && t.taskID == it.TaskID {
		// 開卡時卡片先寫進資料庫（已經查得到），之後才記下 Bot 在等它；整段在 askMu 裡。
		// 等那段做完再看，否則剛開的卡會被誤當成 Bot 已經不等而收掉。
		t.askMu.Lock()
		t.mu.Lock()
		p = t.pending
		ok := !t.finished && p != nil && p.inboxID == it.ID
		t.mu.Unlock()
		t.askMu.Unlock()
		if !ok {
			p = nil
		}
	}
	if p == nil {
		// Bot 已經不等了（服務重開過、工作已結束）：卡片收掉，請使用者重新交代。
		r.settleStale(ctx, it)
		return it, ErrRequestGone
	}
	ans, resolution, shown, payload, err := buildAnswer(p, it, a)
	if err != nil {
		return it, err
	}
	if p = t.claim(itemID); p == nil {
		return it, ErrAnswered // 同時有另一個回覆（或剛好過期）先送出了
	}
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	// 先結卡、改回工作中再送出：引擎收到回覆後可能馬上做完或又問下一題，那些不能被這裡蓋掉。
	p.stop()
	r.settle(t, p, store.InboxResolved, resolution, by, payload, shown)
	state := r.resume(t)
	if err := r.o.Driver.Respond(ctx, s, p.reqID, ans); err != nil {
		// 沒送出去：卡片回到等你，可以再回一次。
		r.reopen(t, p)
		r.rewait(t, state)
		return it, err
	}
	return r.o.DB.GetInboxItem(context.WithoutCancel(ctx), itemID)
}

// buildAnswer 依卡片種類檢查回覆，回傳要交給引擎的內容、處理方式、對話裡顯示的那一行，以及要寫回卡片的 payload。
func buildAnswer(p *pending, it store.InboxItem, a Answer) (engine.Answer, string, string, json.RawMessage, error) {
	switch p.kind {
	case "approval":
		choice := a.Choice
		if a.Decline {
			choice = ResolutionDeny
		}
		allowed := false
		for _, c := range p.choices {
			allowed = allowed || c == choice
		}
		if !allowed {
			return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "choice", Message: "這個指令只能選：" + strings.Join(p.choices, "、")}
		}
		return engine.Answer{Choice: choice}, choice, choiceLabel(choice), nil, nil
	case "secret":
		if a.Decline {
			return engine.Answer{}, ResolutionDeclined, "不提供", nil, nil
		}
		value := a.Value
		if p.method == "vault.save_login" {
			if strings.TrimSpace(a.Values["identifier"]) == "" || a.Values["password"] == "" {
				return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "values", Message: "帳號和密碼都要填"}
			}
			raw, _ := json.Marshal(map[string]string{"identifier": a.Values["identifier"], "password": a.Values["password"]})
			value = string(raw)
		} else if value == "" {
			value = a.Values["value"]
		}
		if value == "" {
			return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "value", Message: "請輸入內容，或選「不提供」"}
		}
		return engine.Answer{Value: value}, ResolutionProvided, "已提供", nil, nil
	default:
		if a.Decline {
			skip := "（使用者略過這個問題。不要猜答案；停下需要這個答案的部分，回報目前進度與需要使用者決定的事。）"
			return engine.Answer{Text: skip}, ResolutionDeclined, "已略過", nil, nil
		}
		text := strings.TrimSpace(a.Text)
		answers := make([]string, len(a.Answers))
		for i, s := range a.Answers {
			answers[i] = strings.TrimSpace(s)
		}
		shown := text
		if len(answers) > 0 {
			if p.nq > 1 && len(answers) != p.nq {
				return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "answers", Message: fmt.Sprintf("要回答全部 %d 題", p.nq)}
			}
			for _, s := range answers {
				if s == "" {
					return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "answers", Message: "每一題都要回答"}
				}
			}
			shown = strings.Join(answers, "；")
			if text == "" {
				text = answers[0]
			}
		} else if p.nq > 1 {
			return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "answers", Message: "這張卡有多個問題，請逐題回答"}
		}
		if text == "" {
			return engine.Answer{}, "", "", nil, &InvalidAnswer{Field: "text", Message: "請回答，或選「略過」"}
		}
		var cp cardPayload
		_ = json.Unmarshal(it.Payload, &cp)
		cp.Answer = clip(shown, 200)
		raw, _ := json.Marshal(cp)
		return engine.Answer{Text: text, Answers: answers}, ResolutionAnswered, shown, raw, nil
	}
}

func choiceLabel(c string) string {
	switch c {
	case ResolutionOnce:
		return "允許這次"
	case ResolutionAlways:
		return "一律允許"
	}
	return "拒絕"
}

// expire 是無人在場的提問過期：照 LazyBoy expire_question，核准拒絕、提問告訴 Bot 使用者沒回、密碼送空白。
func (r *Runtime) expire(t *turn, p *pending) {
	if t.claim(p.inboxID) == nil {
		return // 已經有人回覆了
	}
	var ans engine.Answer
	note := "請求已過期：10 分鐘內沒有回覆"
	switch p.kind {
	case "approval":
		ans, note = engine.Answer{Choice: ResolutionDeny}, "審批已過期：10 分鐘內沒有人允許，已自動拒絕"
	case "question":
		ans = engine.Answer{Text: "（使用者 10 分鐘內沒有回應。不要猜答案；停下需要這個答案或授權的部分，回報目前進度與需要使用者決定的事。）"}
		note = "提問已過期：10 分鐘內沒有回覆"
	}
	t.mu.Lock()
	s := t.session
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 先記下「已過期」再回覆 Bot：Bot 收到回覆後可能馬上結束這一輪，紀錄要在那之前寫好。
	r.settle(t, p, store.InboxExpired, ResolutionExpired, "system", nil, note)
	r.resume(t)
	if err := r.o.Driver.Respond(ctx, s, p.reqID, ans); err != nil {
		r.log.Warn("過期的提問無法回覆 Bot", "task", t.taskID, "err", err)
	}
}

// closeRequest 是引擎自己取消了請求（Hermes request.cancel）。
func (r *Runtime) closeRequest(t *turn, reqID string) {
	t.mu.Lock()
	p := t.pending
	if p == nil || p.reqID != reqID {
		t.mu.Unlock()
		return
	}
	t.pending = nil
	t.mu.Unlock()
	p.stop()
	r.settle(t, p, store.InboxDismissed, ResolutionCanceled, "system", nil, "Bot 不再等這個回覆")
}

// settle 結掉一張提問卡，發布 inbox.updated 與 chat.answered（text 空白時不發 chat.answered 以外的說明）。
func (r *Runtime) settle(t *turn, p *pending, status, resolution, by string, payload json.RawMessage, text string) {
	if p.inboxID == "" {
		return
	}
	ctx := context.Background()
	if err := r.o.DB.SettleInboxItem(ctx, p.inboxID, status, resolution, by, payload); err != nil && !errors.Is(err, store.ErrAlreadyDecided) {
		r.log.Warn("結掉待我處理卡失敗", "inbox", p.inboxID, "err", err)
		return
	}
	if it, err := r.o.DB.GetInboxItem(ctx, p.inboxID); err == nil {
		r.emitStored(t, notify.Event(notify.KindUpdated, it))
	}
	r.emit(t, KindAnswered, AnsweredPayload{InboxID: p.inboxID, RequestID: p.reqID, Kind: p.kind, Resolution: resolution, Text: text, By: by}, true)
}

// settleStale 收掉一張 Bot 已經不等的提問卡（沒有進行中的一輪可以發事件時，直接寫 event 表）。
func (r *Runtime) settleStale(ctx context.Context, it store.InboxItem) {
	ctx = context.WithoutCancel(ctx)
	if err := r.o.DB.SettleInboxItem(ctx, it.ID, store.InboxDismissed, ResolutionGone, "system", nil); err != nil {
		return
	}
	if cur, err := r.o.DB.GetInboxItem(ctx, it.ID); err == nil {
		r.publishStored(notify.Event(notify.KindUpdated, cur))
	}
}

// resume 在送出回覆前把任務改回工作中（停止中、已結束的不動），回傳原本在等的狀態（沒改時是空白）。
// 和 finish、新問題的開卡互斥，所以不會蓋掉「已完成」或下一個問題的「等你」。
func (r *Runtime) resume(t *turn) string {
	t.askMu.Lock()
	defer t.askMu.Unlock()
	t.mu.Lock()
	ok := t.waiting && !t.finished && !t.stopping && t.pending == nil
	if ok {
		t.waiting = false
	}
	t.mu.Unlock()
	if !ok {
		return ""
	}
	state := store.TaskWaitingInput
	if task, err := r.o.DB.GetTask(context.Background(), t.taskID); err == nil {
		state = task.State
	}
	r.setState(t, store.TaskRunning)
	return state
}

// reopen 在回覆沒送出去時把卡片打開、重新發 needs_approval（畫面才會再出現回覆卡）。
func (r *Runtime) reopen(t *turn, p *pending) {
	ctx := context.Background()
	if err := r.o.DB.ReopenInboxItem(ctx, p.inboxID); err != nil {
		r.log.Warn("重新打開待我處理卡失敗", "inbox", p.inboxID, "err", err)
	}
	if it, err := r.o.DB.GetInboxItem(ctx, p.inboxID); err == nil {
		r.emitStored(t, notify.Event(notify.KindUpdated, it))
	}
	if p.expires > 0 {
		p.timer = time.AfterFunc(max(time.Until(time.UnixMilli(p.expires)), 0), func() { r.expire(t, p) })
	}
	t.release(p)
	if p.req != nil {
		r.emit(t, KindNeedsApproval, NeedsPayload{Event: engine.Event{Type: engine.EventNeedsApproval, Request: p.req}, InboxID: p.inboxID, ExpiresAt: p.expires}, true)
	}
}

// rewait 在回覆沒送出去時把任務改回等你。
func (r *Runtime) rewait(t *turn, state string) {
	if state == "" {
		return
	}
	t.askMu.Lock()
	defer t.askMu.Unlock()
	t.mu.Lock()
	ok := !t.finished && !t.stopping
	if ok {
		t.waiting = true
	}
	t.mu.Unlock()
	if ok {
		r.setState(t, state)
	}
}

// notifyDone 在工作結束時發一張通知卡（B04「完成」「出錯」）；你停掉的不發。
func (r *Runtime) notifyDone(t *turn, verdict, final string) {
	var it store.InboxItem
	switch verdict {
	case VerdictAnswer:
		it = store.InboxItem{Kind: "done", Title: t.botName + "完成了工作"}
	case VerdictFailed:
		it = store.InboxItem{Kind: "error", Title: t.botName + "的工作沒有完成"}
	default:
		return
	}
	it.BotID, it.TaskID, it.ThreadID, it.Status, it.Resolution = t.botID, t.taskID, t.threadID, store.InboxResolved, ResolutionInfo
	it.Body = clip(firstLine(final), 120)
	raw, _ := json.Marshal(map[string]any{"bot_name": t.botName, "input": clip(t.input, 120)})
	it.Payload = raw
	created, err := r.o.DB.CreateInboxItem(context.Background(), it)
	if err != nil {
		r.log.Warn("寫入完成通知失敗", "task", t.taskID, "err", err)
		return
	}
	r.emitStored(t, notify.Event(notify.KindCreated, created))
}

// AskAgain 是過期的提問按「請它再問一次」（L14）：在原本的對話串請 Bot 把問題再問一次。Bot 正在忙時回 ErrBusy。
func (r *Runtime) AskAgain(ctx context.Context, itemID string) (ChatResult, store.InboxItem, error) {
	it, err := r.o.DB.GetInboxItem(ctx, itemID)
	if err != nil {
		return ChatResult{}, it, err
	}
	if it.Status != store.InboxExpired || it.ThreadID == "" {
		return ChatResult{}, it, ErrNotExpired
	}
	if it.Resolution == ResolutionAskedAgain {
		return ChatResult{}, it, ErrAnswered
	}
	text := "剛剛你問我的事過期了，我現在在。請再問我一次：\n" + clip(it.Body, 2000)
	res, err := r.Chat(ctx, it.BotID, it.ThreadID, text)
	if err != nil {
		return res, it, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := r.o.DB.SetInboxResolution(ctx, it.ID, ResolutionAskedAgain); err != nil {
		return res, it, err
	}
	it, err = r.o.DB.GetInboxItem(ctx, it.ID)
	if err == nil {
		r.publishStored(notify.Event(notify.KindUpdated, it))
	}
	return res, it, err
}

// Recover 在服務啟動時收掉上次留下的提問卡：引擎連線已經換過，Bot 不會再等那些回覆。
func (r *Runtime) Recover(ctx context.Context) error {
	items, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"approval", "question", "secret"}})
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.TaskID != "" {
			r.settleStale(ctx, it)
		}
	}
	return nil
}

// emitStored 寫一筆已組好的事件（例：inbox.*）並發布。
func (r *Runtime) emitStored(t *turn, e store.Event) {
	if e.ThreadID == "" {
		e.ThreadID = t.threadID
	}
	r.publishStored(e)
}

func (r *Runtime) publishStored(e store.Event) {
	row, err := r.o.DB.AppendEvent(context.Background(), e)
	if err != nil {
		r.log.Warn("寫入事件失敗", "kind", e.Kind, "err", err)
		return
	}
	r.o.Bus.Publish(events.FromStore(row))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
