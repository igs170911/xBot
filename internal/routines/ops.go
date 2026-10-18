package routines

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── 輸入輸出 ──────────────────────────────────────────

// HookView 是網址觸發的設定。
type HookView struct {
	ID   string `json:"id"`
	Path string `json:"path" desc:"POST 這個路徑（接在 XBot 的網址後面），例：/hooks/h…"`
	// Secret 只給你本人看（Bot 和代理看到的是空白）。
	Secret     string `json:"secret,omitempty" desc:"金鑰：Authorization: Bearer <金鑰>，或用它對內容算 HMAC-SHA256 放在 X-XBot-Signature: sha256=<hex>"`
	LastHookAt int64  `json:"last_hook_at,omitempty"`
}

// View 是對外的定時任務。
type View struct {
	ID              string    `json:"id"`
	BotID           string    `json:"bot_id"`
	Title           string    `json:"title"`
	Prompt          string    `json:"prompt" desc:"每次交給 Bot 的指示"`
	Schedule        Schedule  `json:"schedule"`
	When            string    `json:"when" desc:"排程的白話寫法，例：每天上午 9:00"`
	JitterMinutes   int       `json:"jitter_minutes" desc:"每次隨機延後 0～N 分鐘"`
	NotifyOnFailure bool      `json:"notify_on_failure" desc:"沒跑成功時發一張卡到待我處理"`
	Goal            *Goal     `json:"goal,omitempty" desc:"目標型：做到完成條件為止（RT-4）"`
	ThreadID        string    `json:"thread_id,omitempty" desc:"結果貼到的對話串（第一次跑時建立）"`
	Status          string    `json:"status" desc:"active 啟用中、paused 已暫停、done 已結束（單次跑完、目標完成或用完輪數、過了結束日）"`
	Running         bool      `json:"running" desc:"這個定時任務交辦的工作正在跑"`
	NextRunAt       int64     `json:"next_run_at,omitempty" desc:"下一次（Unix 毫秒，已含隨機延後）"`
	LastRunAt       int64     `json:"last_run_at,omitempty"`
	LastTaskID      string    `json:"last_task_id,omitempty"`
	LastError       string    `json:"last_error,omitempty" desc:"最近一次開不了工的原因"`
	Failures        int       `json:"failures,omitempty" desc:"連續開不了工的次數（退避 1／5／15／60 分鐘重試）"`
	RetryAt         int64     `json:"retry_at,omitempty"`
	Hook            *HookView `json:"hook,omitempty" desc:"網址觸發"`
	CreatedBy       string    `json:"created_by,omitempty"`
	CreatedAt       int64     `json:"created_at"`
	UpdatedAt       int64     `json:"updated_at"`
}

// GoalIn 是目標型的設定。
type GoalIn struct {
	DoneWhen  string `json:"done_when" required:"true" max:"1000" desc:"完成條件，例：連續兩輪沒有新的錯誤"`
	MaxRounds int    `json:"max_rounds,omitempty" min:"1" max:"50" desc:"最多幾輪；預設 10"`
}

// IDIn 指定一個定時任務。
type IDIn struct {
	ID string `json:"id" required:"true" max:"64" desc:"定時任務 id"`
}

// ListIn 是 xbot.routines.list 的輸入。
type ListIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只列這個 Bot 的"`
}

// ListOut 是 xbot.routines.list 的輸出。
type ListOut struct {
	Routines []View `json:"routines"`
}

// ProposeIn 是 xbot.routines.propose 的輸入。
type ProposeIn struct {
	Text     string `json:"text" required:"true" max:"2000" desc:"一句話，例：每天早上 9 點整理新聞給我"`
	BotID    string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"誰做；不填看句子裡有沒有提到 Bot 的名字，沒有就是第一個 Bot（Bot 自己呼叫時是它自己）"`
	Timezone string `json:"timezone,omitempty" max:"64" desc:"時區；預設 Asia/Taipei"`
}

// Card 是確認卡上的幾列（H02、L17：誰做／什麼時候／做什麼／結果貼到／下次執行）。
type Card struct {
	Who     string `json:"who"`
	When    string `json:"when" desc:"沒看到時間時是「沒看到時間，要幾點做？」"`
	What    string `json:"what"`
	PostsTo string `json:"posts_to"`
	Next    string `json:"next" desc:"下次執行，例：明天上午 9:00"`
}

// Proposal 是 xbot.routines.propose 的輸出：解析好的草稿與確認卡。確認後把 draft 交給 xbot.routines.create。
type Proposal struct {
	Draft     CreateIn `json:"draft"`
	BotName   string   `json:"bot_name"`
	Card      Card     `json:"card"`
	NextRunAt int64    `json:"next_run_at,omitempty"`
	Ready     bool     `json:"ready" desc:"可以直接建立；false 時看 missing"`
	Missing   []string `json:"missing" desc:"還缺：time（沒看到時間）、task（沒看到要做什麼）、bot（沒有 Bot）"`
	Notes     []string `json:"notes" desc:"解析時做的假設，確認卡上要讓使用者看到"`
	Problem   string   `json:"problem,omitempty" desc:"草稿有問題（例：單次的時間已經過了）"`
}

// CreateIn 是 xbot.routines.create 的輸入。
type CreateIn struct {
	BotID           string    `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"誰做；Bot 自己呼叫時不用填（就是你自己）"`
	Title           string    `json:"title,omitempty" max:"80" desc:"標題；不填從指示取"`
	Prompt          string    `json:"prompt,omitempty" max:"8000" desc:"每次交給 Bot 的指示"`
	Schedule        *Schedule `json:"schedule,omitempty" desc:"排程；只給 text 時可以不填"`
	JitterMinutes   int       `json:"jitter_minutes,omitempty" min:"0" max:"240" desc:"每次隨機延後 0～N 分鐘（畫面選項 15／30／60／90／120）；間隔型要小於間隔"`
	NotifyOnFailure *bool     `json:"notify_on_failure,omitempty" desc:"沒跑成功時通知我；預設開"`
	Goal            *GoalIn   `json:"goal,omitempty" desc:"做到完成為止（目標型）"`
	ThreadID        string    `json:"thread_id,omitempty" max:"64" desc:"結果貼到哪條對話串；不填第一次跑時開一條新的"`
	Hook            bool      `json:"hook,omitempty" desc:"同時開一個網址觸發"`
	Text            string    `json:"text,omitempty" max:"2000" desc:"也可以只給一句話（同 xbot.routines.propose）；有給的欄位優先"`
}

// CreateOut 是 xbot.routines.create 的輸出。
type CreateOut struct {
	Routine    *View  `json:"routine,omitempty" desc:"建好的定時任務（送去確認時沒有）"`
	Proposed   bool   `json:"proposed,omitempty" desc:"Bot 或代理建立的：已送到「待我處理」等使用者確認，確認後才會建立"`
	ApprovalID string `json:"approval_id,omitempty" desc:"proposed 時：用 xbot.approvals.wait 等結果"`
	Note       string `json:"note,omitempty"`
}

// UpdateIn 是 xbot.routines.update 的輸入：有給的欄位才改。
type UpdateIn struct {
	ID              string    `json:"id" required:"true" max:"64"`
	Title           *string   `json:"title,omitempty" min:"1" max:"80"`
	Prompt          *string   `json:"prompt,omitempty" min:"1" max:"8000"`
	Schedule        *Schedule `json:"schedule,omitempty" desc:"整個換掉"`
	JitterMinutes   *int      `json:"jitter_minutes,omitempty" min:"0" max:"240"`
	NotifyOnFailure *bool     `json:"notify_on_failure,omitempty"`
	Goal            *GoalIn   `json:"goal,omitempty" desc:"換新的完成條件會從第 0 輪重新開始；只加輪數會接著做"`
	ClearGoal       bool      `json:"clear_goal,omitempty" desc:"拿掉目標，變回一般定時任務"`
	ThreadID        *string   `json:"thread_id,omitempty" max:"64" desc:"空白字串表示下次開一條新的對話串"`
}

// LogsIn 是 xbot.routines.logs 的輸入。
type LogsIn struct {
	ID    string `json:"id" required:"true" max:"64"`
	Limit int    `json:"limit,omitempty" min:"1" max:"20" desc:"最多幾筆；預設 20（每個定時任務保留最近 20 筆）"`
}

// LogsOut 是 xbot.routines.logs 的輸出。
type LogsOut struct {
	Runs []store.RoutineRun `json:"runs" desc:"新到舊。trigger：schedule 排程、catchup 補跑、manual 手動、webhook 網址觸發、test 測試；status：starting、running、done、failed、cancelled、skipped、retrying、test"`
}

// RunOut 是 xbot.routines.run_now 的輸出。
type RunOut struct {
	Run      store.RoutineRun `json:"run"`
	ThreadID string           `json:"thread_id"`
}

// DeleteOut 是 xbot.routines.delete 的輸出。
type DeleteOut struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Deleted bool   `json:"deleted" desc:"預演時是 false"`
}

// CompleteIn 是 xbot.routines.complete 的輸入。
type CompleteIn struct {
	ID      string `json:"id" required:"true" max:"64"`
	Summary string `json:"summary" required:"true" max:"4000" desc:"一句話成果：學到或完成了什麼"`
}

// CalendarIn 是 xbot.routines.calendar 的輸入。
type CalendarIn struct {
	From  int64  `json:"from" required:"true" min:"0" desc:"開始（Unix 毫秒）"`
	To    int64  `json:"to" required:"true" min:"0" desc:"結束（Unix 毫秒，不含）；最多 42 天"`
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot 的"`
}

// CalendarItem 是行事曆上的一格。
type CalendarItem struct {
	RoutineID string `json:"routine_id"`
	BotID     string `json:"bot_id"`
	Title     string `json:"title"`
	At        int64  `json:"at" desc:"Unix 毫秒；未來的是排定時間（不含隨機延後）"`
	Status    string `json:"status" desc:"scheduled 排程中，或執行紀錄的狀態（done、failed、running、skipped、retrying、cancelled、test）"`
	Trigger   string `json:"trigger,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

// CalendarOut 是 xbot.routines.calendar 的輸出。
type CalendarOut struct {
	Items     []CalendarItem `json:"items" desc:"依時間排序"`
	Truncated bool           `json:"truncated,omitempty" desc:"太多（例：每 5 分鐘一次）只列前面的"`
}

// HookOut 是 hook_create、hook_rotate 的輸出。
type HookOut struct {
	Routine View `json:"routine"`
}

// Register 登錄定時任務的操作（PRD 6.8 RT-*）。svc 是 nil 時只登錄定義（指令列、產生前端型別），呼叫會回 internal。
func Register(reg *ops.Registry, svc *Service) {
	d := &deps{reg: reg, svc: svc}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.routines.list", Summary: "列出定時任務", Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: d.list,
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.routines.get", Summary: "讀一個定時任務", Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (View, error) {
			r, err := d.get(ctx, call, in.ID)
			if err != nil {
				return View{}, err
			}
			return d.view(call, r), nil
		},
	})
	ops.Register(reg, ops.Op[ProposeIn, Proposal]{
		Name: "xbot.routines.propose", Summary: "把一句話解析成定時任務草稿",
		Description: "例：「每天早上 9 點整理新聞給我」「平日下午三點」「每 30 分鐘」「每週一、三」「明天早上 8 點」。" +
			"回傳草稿與確認卡（誰做、什麼時候、做什麼、結果貼到、下次執行），不會建立任何東西；確認後把 draft 交給 xbot.routines.create。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: d.propose,
	})
	ops.Register(reg, ops.Op[CreateIn, CreateOut]{
		Name: "xbot.routines.create", Summary: "新增定時任務",
		Description: "給結構化的 schedule，或只給 text（一句話）。Bot 或代理建立的會先送到使用者的「待我處理」，使用者確認後才建立（RT-2）。" +
			"每個 Bot 最多 50 個定時任務；間隔最短 5 分鐘。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: d.create,
	})
	ops.Register(reg, ops.Op[UpdateIn, View]{
		Name: "xbot.routines.update", Summary: "修改定時任務", Description: "只改有給的欄位；改完重新排下一次、清掉失敗的退避。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.update,
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.routines.pause", Summary: "暫停定時任務", Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (View, error) {
			return d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
				if r.Status == store.RoutineActive {
					r.Status = store.RoutinePaused
				}
				return nil
			})
		},
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.routines.resume", Summary: "繼續已暫停的定時任務", Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (View, error) {
			return d.mutate(ctx, call, in.ID, func(r *store.Routine) error { return d.resume(r) })
		},
	})
	ops.Register(reg, ops.Op[IDIn, RunOut]{
		Name: "xbot.routines.run_now", Summary: "現在就跑一次定時任務",
		Description: "不影響原本的排程。Bot 正在忙時回 conflict。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: d.runNow,
	})
	ops.Register(reg, ops.Op[IDIn, DeleteOut]{
		Name: "xbot.routines.delete", Summary: "刪除定時任務",
		Description: "連同執行紀錄與網址觸發一起刪除，無法復原。要你本人確認，不能代為核准。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: d.delete,
	})
	ops.Register(reg, ops.Op[LogsIn, LogsOut]{
		Name: "xbot.routines.logs", Summary: "看定時任務的執行紀錄", Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in LogsIn) (LogsOut, error) {
			r, err := d.get(ctx, call, in.ID)
			if err != nil {
				return LogsOut{}, err
			}
			limit := in.Limit
			if limit == 0 {
				limit = limits.RoutineHistory
			}
			runs, err := call.DB.ListRoutineRuns(ctx, r.ID, limit)
			if runs == nil {
				runs = []store.RoutineRun{}
			}
			return LogsOut{Runs: runs}, err
		},
	})
	ops.Register(reg, ops.Op[CompleteIn, View]{
		Name: "xbot.routines.complete", Summary: "目標達成，結束這個目標型定時任務",
		Description: "目標型定時任務達到完成條件（或連續兩輪沒有新進展）時呼叫：關掉這個定時任務，並在它的對話裡通知使用者。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: d.complete,
	})
	ops.Register(reg, ops.Op[CalendarIn, CalendarOut]{
		Name: "xbot.routines.calendar", Summary: "行事曆：一段時間內的定時任務",
		Description: "過去的是執行紀錄，未來的是排程展開（隨機延後的以排定時間顯示）。最多 42 天。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: d.calendar,
	})
	ops.Register(reg, ops.Op[IDIn, HookOut]{
		Name: "xbot.routines.hook_create", Summary: "幫定時任務開一個網址觸發",
		Description: "外部服務 POST 到 /hooks/<id>，帶 Authorization: Bearer <金鑰>（或 X-XBot-Signature HMAC 簽章），" +
			"內容會當成資料交給 Bot（不是指令）。內容最多 64 KiB；同一個網址至少隔 60 秒；帶 X-XBot-Test: 1 是測試，不開始工作。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (HookOut, error) {
			v, err := d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
				if r.HookID != "" {
					return ops.Conflict("已經有網址觸發了；要換金鑰用 xbot.routines.hook_rotate")
				}
				r.HookID, r.HookSecret = NewHookID(), NewSecret()
				return nil
			})
			return HookOut{Routine: v}, err
		},
	})
	ops.Register(reg, ops.Op[IDIn, HookOut]{
		Name: "xbot.routines.hook_rotate", Summary: "換網址觸發的金鑰", Description: "舊金鑰立刻失效；網址不變。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (HookOut, error) {
			v, err := d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
				if r.HookID == "" {
					return ops.Conflict("這個定時任務沒有開網址觸發")
				}
				r.HookSecret = NewSecret()
				return nil
			})
			return HookOut{Routine: v}, err
		},
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.routines.hook_delete", Summary: "關掉網址觸發", Description: "網址與金鑰立刻失效。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (View, error) {
			return d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
				r.HookID, r.HookSecret, r.LastHookAt = "", "", 0
				if ScheduleOf(*r).Kind == KindTrigger && r.Status == store.RoutineActive {
					r.Status = store.RoutinePaused // 只靠網址觸發的，沒有網址就不會再跑
				}
				return nil
			})
		},
	})
}

// ── 實作 ─────────────────────────────────────────────

type deps struct {
	reg *ops.Registry
	svc *Service
}

var errNoService = errors.New("定時任務排程器沒有啟動")

func (d *deps) now() time.Time {
	if d.svc != nil {
		return d.svc.Now()
	}
	return time.Now()
}

func (d *deps) rnd(n int64) int64 {
	if d.svc != nil {
		return d.svc.Rand(n)
	}
	return 0
}

func (d *deps) lock() func() {
	if d.svc == nil {
		return func() {}
	}
	d.svc.Lock()
	return d.svc.Unlock
}

// get 讀一個定時任務，並確認呼叫者能碰它的 Bot。
func (d *deps) get(ctx context.Context, call *ops.Call, id string) (store.Routine, error) {
	r, err := call.DB.GetRoutine(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !call.Identity.CanAccess(ops.ResourceBot, r.BotID)) {
		return r, ops.NotFound("找不到這個定時任務")
	}
	return r, err
}

func (d *deps) view(call *ops.Call, r store.Routine) View {
	sc := ScheduleOf(r)
	v := View{ID: r.ID, BotID: r.BotID, Title: r.Title, Prompt: r.Prompt, Schedule: sc, When: Describe(sc), JitterMinutes: r.JitterMinutes,
		NotifyOnFailure: r.NotifyOnFailure, Goal: GoalOf(r), ThreadID: r.ThreadID, Status: r.Status, NextRunAt: r.NextRunAt,
		LastRunAt: r.LastRunAt, LastTaskID: r.LastTaskID, LastError: r.LastError, Failures: r.Failures, RetryAt: r.RetryAt,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.HookID != "" {
		v.Hook = &HookView{ID: r.HookID, Path: HookPrefix + r.HookID, LastHookAt: r.LastHookAt}
		if call.Identity.Kind == ops.KindOwner {
			v.Hook.Secret = r.HookSecret
		}
	}
	if d.svc != nil && r.LastTaskID != "" {
		_, active := d.svc.o.Runtime.Active(r.BotID)
		v.Running = active == r.LastTaskID
	}
	return v
}

func (d *deps) list(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
	botID := in.BotID
	if botID == "" && call.Identity.Kind == ops.KindBot {
		botID = call.Identity.ID
	}
	rs, err := call.DB.ListRoutines(ctx, botID)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Routines: []View{}}
	for _, r := range rs {
		if call.Identity.CanAccess(ops.ResourceBot, r.BotID) {
			out.Routines = append(out.Routines, d.view(call, r))
		}
	}
	return out, nil
}

// bots 是解析一句話時認得的 Bot：呼叫的 Bot 自己（或指定的）排第一，再來是主要 Bot（幕僚長），其他依建立順序。
func (d *deps) bots(ctx context.Context, call *ops.Call, first string) ([]BotRef, error) {
	bs, err := call.DB.ListBots(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []BotRef
	slices.SortStableFunc(bs, func(a, b store.Bot) int {
		rank := func(x store.Bot) int {
			switch {
			case x.ID == first:
				return 0
			case x.IsChief:
				return 1
			}
			return 2
		}
		return rank(a) - rank(b)
	})
	for _, b := range bs {
		if call.Identity.CanAccess(ops.ResourceBot, b.ID) {
			out = append(out, BotRef{ID: b.ID, Name: b.Name})
		}
	}
	return out, nil
}

func (d *deps) propose(ctx context.Context, call *ops.Call, in ProposeIn) (Proposal, error) {
	tz := strings.TrimSpace(in.Timezone)
	loc, err := Schedule{Timezone: tz}.Location()
	if err != nil {
		return Proposal{}, ops.InvalidInput(ops.FieldError{Field: "timezone", Message: "無效的時區"})
	}
	first := in.BotID
	if first == "" && call.Identity.Kind == ops.KindBot {
		first = call.Identity.ID
	}
	bots, err := d.bots(ctx, call, first)
	if err != nil {
		return Proposal{}, err
	}
	if in.BotID != "" && (len(bots) == 0 || bots[0].ID != in.BotID) {
		return Proposal{}, ops.NotFound("找不到這個 Bot")
	}
	now := d.now()
	p := Parse(in.Text, now, loc, bots)
	// 快判先試（X05）：模型把你的話整理過，解析得一樣完整或更完整才採用；否則照規則解析原句。
	if d.svc != nil && d.svc.o.Rewrite != nil && !call.DryRun {
		if text, ok := d.svc.o.Rewrite(ctx, in.Text); ok && strings.TrimSpace(text) != strings.TrimSpace(in.Text) {
			if mp := Parse(text, now, loc, bots); len(mp.Missing) <= len(p.Missing) {
				p = mp
				p.Notes = append(p.Notes, "系統判斷：整理成「"+strings.TrimSpace(text)+"」")
			}
		}
	}
	if in.BotID != "" && p.BotID != in.BotID {
		// 指定了 Bot：句子裡的其他名字當成內容。
		p.BotID, p.BotName = bots[0].ID, bots[0].Name
	}
	draftSchedule := p.Schedule
	out := Proposal{Draft: CreateIn{BotID: p.BotID, Title: p.Title, Prompt: p.Prompt, Schedule: &draftSchedule}, BotName: p.BotName,
		Missing: p.Missing, Notes: p.Notes}
	out.Card = Card{Who: p.BotName, What: p.Prompt, When: Describe(p.Schedule), PostsTo: "和" + p.BotName + "的對話"}
	if p.BotName == "" {
		out.Card.PostsTo = ""
	}
	if slices.Contains(p.Missing, "time") {
		out.Card.When = "沒看到時間，要幾點做？"
	}
	if len(p.Missing) == 0 {
		sc, err := Normalize(p.Schedule, 0, now)
		if err != nil {
			out.Problem = err.Error()
		} else if next, ok, err := sc.Slot(now); err != nil || !ok {
			out.Problem = "這個時間已經過了，請換一個時間"
		} else {
			out.Draft.Schedule, out.NextRunAt = &sc, next.UnixMilli()
			out.Card.Next = relDay(next, now, loc)
			out.Ready = true
		}
	}
	return out, nil
}

// relDay 寫成「今天上午 9:00」「明天上午 9:00」「10月20日（週二）上午 9:00」。
func relDay(t, now time.Time, loc *time.Location) string {
	t, now = t.In(loc), now.In(loc)
	days := int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)).Hours()+12) / 24
	clk := ClockText(t.Hour(), t.Minute())
	switch days {
	case 0:
		return "今天" + clk
	case 1:
		return "明天" + clk
	case 2:
		return "後天" + clk
	}
	return DateText(t) + " " + clk
}

func scheduleErr(err error) error {
	var se *ScheduleError
	if errors.As(err, &se) {
		field := "schedule." + se.Field
		if se.Field == "jitter_minutes" {
			field = se.Field
		}
		return ops.InvalidInput(ops.FieldError{Field: field, Message: se.Message})
	}
	return err
}

// build 從輸入組出新的定時任務（還沒寫進資料庫）。
func (d *deps) build(ctx context.Context, call *ops.Call, in *CreateIn) (store.Routine, error) {
	if in.BotID == "" && call.Identity.Kind == ops.KindBot {
		in.BotID = call.Identity.ID
	}
	if in.Schedule == nil {
		in.Schedule = &Schedule{}
	}
	if strings.TrimSpace(in.Text) != "" && in.Schedule.Kind == "" {
		p, err := d.propose(ctx, call, ProposeIn{Text: in.Text, BotID: in.BotID, Timezone: in.Schedule.Timezone})
		if err != nil {
			return store.Routine{}, err
		}
		if slices.Contains(p.Missing, "time") {
			return store.Routine{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: "沒看到時間，要幾點做？"})
		}
		in.BotID = firstNonEmpty(in.BotID, p.Draft.BotID)
		in.Schedule = p.Draft.Schedule
		in.Prompt = firstNonEmpty(strings.TrimSpace(in.Prompt), p.Draft.Prompt)
	}
	if in.BotID == "" {
		return store.Routine{}, ops.InvalidInput(ops.FieldError{Field: "bot_id", Message: "要指定哪個 Bot 做"})
	}
	if !call.Identity.CanAccess(ops.ResourceBot, in.BotID) {
		return store.Routine{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這個 Bot")
	}
	b, err := call.DB.GetBot(ctx, in.BotID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Routine{}, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return store.Routine{}, err
	}
	if b.Archived {
		return store.Routine{}, ops.Conflict("這個 Bot 已封存")
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return store.Routine{}, ops.InvalidInput(ops.FieldError{Field: "prompt", Message: "請寫要做什麼"})
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = TitleOf(prompt)
	}
	sc, err := Normalize(*in.Schedule, in.JitterMinutes, d.now())
	if err != nil {
		return store.Routine{}, scheduleErr(err)
	}
	r := store.Routine{BotID: b.ID, Title: clip(title, limits.RoutineTitleMax), Prompt: prompt, JitterMinutes: in.JitterMinutes,
		NotifyOnFailure: in.NotifyOnFailure == nil || *in.NotifyOnFailure, ThreadID: strings.TrimSpace(in.ThreadID), Status: store.RoutineActive,
		CreatedBy: call.Identity.Actor()}
	r.Schedule, _ = json.Marshal(sc)
	if r.ThreadID != "" {
		th, err := call.DB.GetThread(ctx, r.ThreadID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && th.BotID != b.ID) {
			return r, ops.NotFound("找不到這條對話串")
		} else if err != nil {
			return r, err
		}
	}
	if in.Goal != nil {
		r.ID = store.NewID()
		g, err := goalFrom(*in.Goal, nil, r.ID)
		if err != nil {
			return r, err
		}
		setGoal(&r, g)
	}
	if in.Hook {
		r.HookID, r.HookSecret = NewHookID(), NewSecret()
	}
	if err := d.schedule(&r); err != nil {
		return r, err
	}
	return r, nil
}

// schedule 依排程算下一次（status 是 active 時）；單次的時間已過回錯誤。
func (d *deps) schedule(r *store.Routine) error {
	r.Failures, r.RetryAt, r.LastError = 0, 0, ""
	if r.Status != store.RoutineActive {
		return nil
	}
	sc := ScheduleOf(*r)
	next, ok, err := sc.Next(d.now(), r.JitterMinutes, d.rnd)
	if err != nil {
		return scheduleErr(err)
	}
	if !ok {
		r.NextRunAt = 0
		switch {
		case sc.Kind == KindTrigger:
		case sc.Kind == KindOnce:
			return ops.InvalidInput(ops.FieldError{Field: "schedule.date", Message: "單次排程必須是未來的時間"})
		default:
			return ops.InvalidInput(ops.FieldError{Field: "schedule.end_date", Message: "結束日已經過了"})
		}
		return nil
	}
	r.NextRunAt = next.UnixMilli()
	return nil
}

// goalFrom 組出目標。LazyBoy Scheduler.goal：換新的完成條件從頭開始；只加輪數讓用完的接著做。
func goalFrom(in GoalIn, old *Goal, routineID string) (*Goal, error) {
	doneWhen := clip(strings.TrimSpace(in.DoneWhen), limits.GoalDoneWhenMax)
	if doneWhen == "" {
		return nil, ops.InvalidInput(ops.FieldError{Field: "goal.done_when", Message: "請寫完成條件"})
	}
	rounds := in.MaxRounds
	if rounds == 0 {
		rounds = limits.GoalRoundsDefault
		if old != nil {
			rounds = old.MaxRounds
		}
	}
	if rounds < 1 || rounds > limits.GoalRoundsMax {
		return nil, ops.InvalidInput(ops.FieldError{Field: "goal.max_rounds", Message: fmt.Sprintf("最多輪數要介於 1 到 %d", limits.GoalRoundsMax)})
	}
	g := &Goal{DoneWhen: doneWhen, MaxRounds: rounds, ProgressFile: "/workspace/goals/" + routineID[:8] + ".md"}
	if old != nil {
		*g = *old
		g.DoneWhen, g.MaxRounds = doneWhen, rounds
		if old.ProgressFile != "" {
			g.ProgressFile = old.ProgressFile
		}
		if doneWhen != old.DoneWhen {
			g.Rounds, g.CompletedAt, g.Summary = 0, 0, ""
		} else if rounds > old.MaxRounds {
			g.CompletedAt, g.Summary = 0, ""
		}
	}
	return g, nil
}

func (d *deps) create(ctx context.Context, call *ops.Call, in CreateIn) (CreateOut, error) {
	r, err := d.build(ctx, call, &in)
	if err != nil {
		return CreateOut{}, err
	}
	n, err := call.DB.CountRoutines(ctx, r.BotID)
	if err != nil {
		return CreateOut{}, err
	}
	if n >= limits.RoutinesPerBot {
		return CreateOut{}, ops.Limit(fmt.Sprintf("一個 Bot 最多 %d 個定時任務；先刪掉用不到的再新增", limits.RoutinesPerBot))
	}
	// Bot 或代理提議的：先送到使用者的待我處理（RT-2），確認後由 ExecuteApproved 再呼叫一次（帶 ApprovalID）。
	if call.Identity.Kind != ops.KindOwner && call.ApprovalID == "" {
		if call.DryRun {
			v := d.view(call, r)
			return CreateOut{Routine: &v, Proposed: true, Note: "會送到使用者的待我處理，確認後才建立"}, nil
		}
		in.Text = "" // 核准後照確認卡上的內容建立，不再重新解析
		sc := ScheduleOf(r)
		in.Schedule, in.Prompt, in.Title = &sc, r.Prompt, r.Title
		raw, err := json.Marshal(in)
		if err != nil {
			return CreateOut{}, err
		}
		b, _ := call.DB.GetBot(ctx, r.BotID)
		body := fmt.Sprintf("誰做：%s\n什麼時候：%s\n做什麼：%s", b.Name, Describe(sc), r.Prompt)
		if r.NextRunAt != 0 {
			loc, _ := sc.Location()
			body += "\n下次執行：" + relDay(time.UnixMilli(r.NextRunAt), d.now(), loc)
		}
		t, err := d.reg.RequestApproval(ctx, call, raw, r.BotID, ops.ApprovalCard{
			Title: fmt.Sprintf("%s 想新增定時任務：%s", call.Identity.DisplayName(), r.Title), Body: body})
		if err != nil {
			return CreateOut{}, err
		}
		return CreateOut{Proposed: true, ApprovalID: t.ApprovalID, Note: "已送到使用者的「待我處理」；使用者確認後才會建立。"}, nil
	}
	if call.DryRun {
		v := d.view(call, r)
		return CreateOut{Routine: &v}, nil
	}
	unlock := d.lock()
	defer unlock()
	r, err = call.DB.CreateRoutine(ctx, r)
	if err != nil {
		return CreateOut{}, err
	}
	v := d.view(call, r)
	return CreateOut{Routine: &v}, nil
}

// mutate 讀出定時任務、套用 fn、寫回（和排程器互斥）。預演時不寫。
func (d *deps) mutate(ctx context.Context, call *ops.Call, id string, fn func(r *store.Routine) error) (View, error) {
	unlock := func() {}
	if !call.DryRun {
		unlock = d.lock()
	}
	defer unlock()
	r, err := d.get(ctx, call, id)
	if err != nil {
		return View{}, err
	}
	if err := fn(&r); err != nil {
		return View{}, err
	}
	if !call.DryRun {
		if r, err = call.DB.UpdateRoutine(ctx, r); err != nil {
			return View{}, err
		}
	}
	return d.view(call, r), nil
}

// resume 繼續：目標結束的、單次時間已過的不能直接繼續（LazyBoy manage resume）。
func (d *deps) resume(r *store.Routine) error {
	if g := GoalOf(*r); g.Ended() {
		return ops.Conflict("這個目標已經結束了；要繼續請編輯，換新的完成條件或增加最多輪數")
	}
	sc := ScheduleOf(*r)
	if sc.Kind == KindTrigger && r.HookID == "" {
		return ops.Conflict("這個定時任務只靠網址觸發，請先開網址觸發")
	}
	r.Status = store.RoutineActive
	if err := d.schedule(r); err != nil {
		if sc.Kind == KindOnce {
			return ops.Conflict("這個單次排程的時間已經過了，請編輯並設定新的日期")
		}
		return err
	}
	return nil
}

func (d *deps) update(ctx context.Context, call *ops.Call, in UpdateIn) (View, error) {
	return d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
		if in.Title != nil {
			if t := strings.TrimSpace(*in.Title); t != "" {
				r.Title = clip(t, limits.RoutineTitleMax)
			}
		}
		if in.Prompt != nil {
			if p := strings.TrimSpace(*in.Prompt); p != "" {
				r.Prompt = p
			}
		}
		sc := ScheduleOf(*r)
		if in.Schedule != nil {
			sc = *in.Schedule
		}
		if in.JitterMinutes != nil {
			r.JitterMinutes = *in.JitterMinutes
		}
		sc, err := Normalize(sc, r.JitterMinutes, d.now())
		if err != nil {
			return scheduleErr(err)
		}
		r.Schedule, _ = json.Marshal(sc)
		if in.NotifyOnFailure != nil {
			r.NotifyOnFailure = *in.NotifyOnFailure
		}
		if in.ThreadID != nil {
			id := strings.TrimSpace(*in.ThreadID)
			if id != "" {
				th, err := call.DB.GetThread(ctx, id)
				if errors.Is(err, store.ErrNotFound) || (err == nil && th.BotID != r.BotID) {
					return ops.NotFound("找不到這條對話串")
				} else if err != nil {
					return err
				}
			}
			r.ThreadID = id
		}
		switch {
		case in.ClearGoal:
			setGoal(r, nil)
		case in.Goal != nil:
			g, err := goalFrom(*in.Goal, GoalOf(*r), r.ID)
			if err != nil {
				return err
			}
			setGoal(r, g)
			if r.Status == store.RoutineDone && !g.Ended() {
				r.Status = store.RoutineActive // 換了完成條件或加了輪數：接著做
			}
		}
		if g := GoalOf(*r); g.Ended() && r.Status == store.RoutineActive {
			return ops.Conflict("這個目標已經結束了；要繼續請換新的完成條件或增加最多輪數")
		}
		if r.Status == store.RoutineDone && sc.Kind != KindOnce && GoalOf(*r) == nil {
			r.Status = store.RoutineActive // 改了排程（例：延長結束日）：重新啟用
		}
		if r.Status == store.RoutineDone && sc.Kind == KindOnce && in.Schedule != nil {
			r.Status = store.RoutineActive // 單次換了新日期
		}
		return d.schedule(r)
	})
}

func (d *deps) runNow(ctx context.Context, call *ops.Call, in IDIn) (RunOut, error) {
	r, err := d.get(ctx, call, in.ID)
	if err != nil {
		return RunOut{}, err
	}
	if g := GoalOf(r); g != nil && g.CompletedAt != 0 {
		return RunOut{}, ops.Conflict(ErrGoalDone.Error())
	}
	if call.DryRun {
		return RunOut{ThreadID: r.ThreadID}, nil
	}
	if d.svc == nil {
		return RunOut{}, ops.Internal(errNoService)
	}
	run, err := d.svc.RunNow(ctx, r.ID, store.TriggerManual, "", 0)
	if err != nil {
		var oe *ops.Error
		if errors.As(err, &oe) {
			return RunOut{}, oe
		}
		return RunOut{}, ops.Conflict(err.Error())
	}
	cur, _ := call.DB.GetRoutine(ctx, r.ID)
	return RunOut{Run: run, ThreadID: cur.ThreadID}, nil
}

func (d *deps) delete(ctx context.Context, call *ops.Call, in IDIn) (DeleteOut, error) {
	r, err := d.get(ctx, call, in.ID)
	if err != nil {
		return DeleteOut{}, err
	}
	out := DeleteOut{ID: r.ID, Title: r.Title}
	if call.DryRun {
		return out, nil
	}
	unlock := d.lock()
	defer unlock()
	if err := call.DB.DeleteRoutine(ctx, r.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	out.Deleted = true
	return out, nil
}

func (d *deps) complete(ctx context.Context, call *ops.Call, in CompleteIn) (View, error) {
	summary := clip(strings.TrimSpace(in.Summary), limits.GoalSummaryMax)
	var done bool
	v, err := d.mutate(ctx, call, in.ID, func(r *store.Routine) error {
		g := GoalOf(*r)
		if g == nil {
			return ops.Conflict("這個定時任務沒有設定目標；不需要的話直接暫停或刪除")
		}
		if g.CompletedAt != 0 {
			return nil
		}
		if summary == "" {
			return ops.InvalidInput(ops.FieldError{Field: "summary", Message: "請寫出這個目標學到或完成了什麼"})
		}
		g.CompletedAt, g.Summary = d.now().UnixMilli(), summary
		setGoal(r, g)
		r.Status, r.NextRunAt = store.RoutineDone, 0
		done = true
		return nil
	})
	if err != nil || !done || call.DryRun {
		return v, err
	}
	// 在它的對話裡說一聲（LazyBoy：goal_completed 事件訊息）。
	if v.ThreadID != "" {
		meta, _ := json.Marshal(map[string]any{"event": "goal_completed", "routine_id": v.ID,
			"card": map[string]string{"type": "info", "title": "目標完成：" + v.Title, "body": summary}})
		var ev store.Event
		err := call.DB.Tx(ctx, func(q *store.Queries) error {
			m, err := q.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: v.ThreadID, Sender: "bot:" + v.BotID, Kind: "card",
				Content: "目標完成：" + v.Title + "\n\n" + summary, Meta: meta})
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(m)
			ev, err = q.AppendEvent(ctx, store.Event{BotID: v.BotID, ThreadID: v.ThreadID, Kind: "message.created", Payload: payload})
			return err
		})
		if err == nil {
			d.reg.Notify(ev)
		}
	}
	return v, nil
}

func (d *deps) calendar(ctx context.Context, call *ops.Call, in CalendarIn) (CalendarOut, error) {
	if in.To <= in.From {
		return CalendarOut{}, ops.InvalidInput(ops.FieldError{Field: "to", Message: "要晚於 from"})
	}
	if time.Duration(in.To-in.From)*time.Millisecond > limits.RoutineCalendarDays*24*time.Hour {
		return CalendarOut{}, ops.InvalidInput(ops.FieldError{Field: "to", Message: fmt.Sprintf("最多 %d 天", limits.RoutineCalendarDays)})
	}
	botID := in.BotID
	rs, err := call.DB.ListRoutines(ctx, botID)
	if err != nil {
		return CalendarOut{}, err
	}
	titles := map[string]store.Routine{}
	out := CalendarOut{Items: []CalendarItem{}}
	now := d.now()
	for _, r := range rs {
		if !call.Identity.CanAccess(ops.ResourceBot, r.BotID) {
			continue
		}
		titles[r.ID] = r
		if r.Status != store.RoutineActive {
			continue
		}
		// 未來：從現在（或 from）往後展開排程。
		sc := ScheduleOf(r)
		n := 0
		after := time.UnixMilli(max(in.From, now.UnixMilli()) - 1)
		if r.NextRunAt != 0 && r.NextRunAt >= in.From && r.NextRunAt < in.To {
			// 下一次已經排好（含隨機延後與退避）：以排定的格子顯示。
			slot, ok, _ := sc.Slot(time.UnixMilli(r.NextRunAt).Add(-time.Duration(r.JitterMinutes)*time.Minute - time.Millisecond))
			at := r.NextRunAt
			if ok && slot.UnixMilli() <= r.NextRunAt {
				at = slot.UnixMilli()
			}
			out.Items = append(out.Items, CalendarItem{RoutineID: r.ID, BotID: r.BotID, Title: r.Title, At: at, Status: "scheduled"})
			after = time.UnixMilli(max(r.NextRunAt, after.UnixMilli()))
			n = 1
		}
		for ; ; n++ {
			if n >= limits.RoutineCalendarPerRoutine {
				out.Truncated = true
				break
			}
			slot, ok, err := sc.Slot(after)
			if err != nil || !ok || slot.UnixMilli() >= in.To {
				break
			}
			out.Items = append(out.Items, CalendarItem{RoutineID: r.ID, BotID: r.BotID, Title: r.Title, At: slot.UnixMilli(), Status: "scheduled"})
			after = slot
		}
	}
	// 過去：執行紀錄。
	runs, err := call.DB.ListRunsBetween(ctx, botID, in.From, in.To)
	if err != nil {
		return CalendarOut{}, err
	}
	for _, run := range runs {
		r, ok := titles[run.RoutineID]
		if !ok {
			continue
		}
		at := run.ScheduledAt
		if at == 0 {
			at = run.CreatedAt
		}
		out.Items = append(out.Items, CalendarItem{RoutineID: r.ID, BotID: r.BotID, Title: r.Title, At: at, Status: run.Status, Trigger: run.Trigger,
			RunID: run.ID, TaskID: run.TaskID})
	}
	slices.SortStableFunc(out.Items, func(a, b CalendarItem) int {
		switch {
		case a.At < b.At:
			return -1
		case a.At > b.At:
			return 1
		}
		return 0
	})
	return out, nil
}
