package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Service 讀寫 Bot 的記憶並記下變更（MM-3）。
type Service struct {
	DB *store.DB
	// Locate 找 Bot 的電腦（runtime.Computers.BotFS）。
	Locate boxfs.Locator
	// Active 回傳 Bot 正在做事的對話串（runtime.Active）：Bot 用 xbot.memory.add 記的事記下是在哪條對話串。可以是 nil。
	Active func(botID string) (thread, task string)

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (s *Service) lock(botID string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	l, ok := s.locks[botID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[botID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ── 輸入輸出 ──────────────────────────────────────────

// BotIn 指定一個 Bot。
type BotIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot id；Bot 自己呼叫時不用填（就是你自己）"`
}

// ReadOut 是 xbot.memory.read 的輸出。
type ReadOut struct {
	State
	Enabled  bool `json:"enabled" desc:"記憶有沒有打開；關掉時不會帶進對話"`
	Computer bool `json:"computer" desc:"Bot 已經有電腦（沒有時兩份都是空的）"`
}

// WriteIn 是 xbot.memory.write 的輸入。
type WriteIn struct {
	BotID    string   `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Target   Target   `json:"target" required:"true" desc:"user：關於你（USER.md）；memory：Bot 記住的事（MEMORY.md）"`
	Entries  []string `json:"entries" required:"true" max:"500" desc:"整份清單（一條一項）；空陣列表示清空"`
	Previous []string `json:"previous,omitempty" max:"500" desc:"你編輯的是哪一版（xbot.memory.read 讀到的 entries）；Bot 在這之間改過就不寫，回 conflict"`
}

// AddIn 是 xbot.memory.add 的輸入（Bot 自己用）。
type AddIn struct {
	Target  Target `json:"target" required:"true" desc:"user：關於使用者；memory：你的工作與環境"`
	Content string `json:"content" required:"true" max:"2000" desc:"一條記憶，一兩句話；密碼和金鑰會被遮蔽"`
}

// HistoryIn 是 xbot.memory.history 的輸入。
type HistoryIn struct {
	BotID  string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Target Target `json:"target,omitempty" desc:"只看這一份；不填兩份都看"`
}

// Change 是一次變更。
type Change struct {
	ID          string   `json:"id"`
	Target      Target   `json:"target"`
	Actor       string   `json:"actor" desc:"誰改的：owner、agent:<id>、bot:<id>"`
	Added       []string `json:"added" desc:"新增的條目"`
	Removed     []string `json:"removed" desc:"拿掉的條目（和 added 都是空的：只調整了順序或拿掉重複的條目）"`
	ThreadID    string   `json:"thread_id,omitempty" desc:"Bot 在哪條對話串裡記的（看得出來時才有）"`
	ThreadTitle string   `json:"thread_title,omitempty" desc:"那條對話串的標題；在群組裡記的是群組名稱"`
	RoomID      string   `json:"room_id,omitempty" desc:"在群組裡記的：群組 id"`
	CreatedAt   int64    `json:"created_at"`
	Undoable    bool     `json:"undoable" desc:"之後沒有再改過，可以復原"`
}

// HistoryOut 是 xbot.memory.history 的輸出。
type HistoryOut struct {
	Changes []Change `json:"changes"`
}

// RestoreIn 是 xbot.memory.restore 的輸入。
type RestoreIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	ChangeID string `json:"change_id" required:"true" max:"64" desc:"xbot.memory.history 的 id"`
}

// Register 登錄 xbot.memory.* 操作。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	need := func() error {
		if s == nil {
			return ops.Internal(errors.New("記憶服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[BotIn, ReadOut]{
		Name: "xbot.memory.read", Summary: "讀 Bot 的記憶",
		Description: "兩份清單：關於你（USER.md）、Bot 記住的事（MEMORY.md），各有用量與上限。Hermes 在每個新對話開始時載入。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (ReadOut, error) {
			if err := need(); err != nil {
				return ReadOut{}, err
			}
			id, err := self(call, in.BotID)
			if err != nil {
				return ReadOut{}, err
			}
			return s.Read(ctx, call.DB, id)
		},
	})
	ops.Register(reg, ops.Op[WriteIn, ReadOut]{
		Name: "xbot.memory.write", Summary: "改 Bot 的一份記憶",
		Description: "換掉整份清單。超過上限時拒絕（已經超過的可以一條一條縮小）。寫入前遮蔽密碼與金鑰，舊版本留著可以復原。" +
			"帶 previous 時，Bot 在你編輯期間改過就不寫（回 conflict），請重讀再改。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in WriteIn) (ReadOut, error) {
			if err := need(); err != nil {
				return ReadOut{}, err
			}
			if call.DryRun {
				return s.Read(ctx, call.DB, in.BotID)
			}
			return s.Write(ctx, in.BotID, in.Target, in.Entries, in.Previous, call.Identity.Actor())
		},
	})
	ops.Register(reg, ops.Op[AddIn, ReadOut]{
		Name: "xbot.memory.add", Summary: "記住一件事",
		Description: "把一條記憶加進你自己的記憶（user：關於使用者；memory：你的工作與環境）。只記長期有用的事實，不記一次性的進度。" +
			"超過上限時會被拒絕：先請使用者整理。密碼和金鑰會被遮蔽。下一個對話開始時生效。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in AddIn) (ReadOut, error) {
			if err := need(); err != nil {
				return ReadOut{}, err
			}
			if call.Identity.Kind != ops.KindBot {
				return ReadOut{}, ops.Forbidden("這是給 Bot 自己用的操作；要改記憶請用 xbot.memory.write")
			}
			if call.DryRun {
				return s.Read(ctx, call.DB, call.Identity.ID)
			}
			return s.Add(ctx, call.DB, call.Identity.ID, in.Target, in.Content, call.Identity.Actor())
		},
	})
	ops.Register(reg, ops.Op[HistoryIn, HistoryOut]{
		Name: "xbot.memory.history", Summary: "看 Bot 記憶最近的變更",
		Description: "每次修改（你、代理、或 Bot 自己在電腦裡改的）都記一筆，每份最多留 20 筆。可以復原的會標 undoable。" +
			"Bot 在對話裡記的會帶 thread_id／thread_title（在哪條對話串記的）。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in HistoryIn) (HistoryOut, error) {
			if err := need(); err != nil {
				return HistoryOut{}, err
			}
			return s.History(ctx, call.DB, in.BotID, in.Target)
		},
	})
	ops.Register(reg, ops.Op[RestoreIn, ReadOut]{
		Name: "xbot.memory.restore", Summary: "復原一次記憶的變更",
		Description: "把那一份記憶改回這次變更之前。之後又被改過的不能復原（回 conflict）。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in RestoreIn) (ReadOut, error) {
			if err := need(); err != nil {
				return ReadOut{}, err
			}
			if call.DryRun {
				return s.Read(ctx, call.DB, in.BotID)
			}
			return s.Restore(ctx, in.BotID, in.ChangeID, call.Identity.Actor())
		},
	})
}

func self(call *ops.Call, botID string) (string, error) {
	if call.Identity.Kind == ops.KindBot {
		if botID != "" && botID != call.Identity.ID {
			return "", ops.Forbidden("只能讀你自己的記憶")
		}
		return call.Identity.ID, nil
	}
	if botID == "" {
		return "", ops.InvalidInput(ops.FieldError{Field: "bot_id", Message: "要指定 Bot"})
	}
	return botID, nil
}

func (s *Service) bot(ctx context.Context, db *store.DB, botID string) (store.Bot, error) {
	b, err := db.GetBot(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return b, ops.NotFound("找不到這個 Bot")
	}
	return b, err
}

func empty() State {
	return State{User: List{Entries: []string{}, Limit: limits.MemoryUserCharsDefault}, Memory: List{Entries: []string{}, Limit: limits.MemoryCharsDefault}}
}

// Read 讀記憶；Bot 自己在電腦裡改過（和最後一次紀錄不同）時記一筆變更（G06：以檔案為準並列進最近的變更）。
func (s *Service) Read(ctx context.Context, db *store.DB, botID string) (ReadOut, error) {
	b, err := s.bot(ctx, db, botID)
	if err != nil {
		return ReadOut{}, err
	}
	f, err := s.Locate(ctx, botID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return ReadOut{State: empty(), Enabled: b.MemoryEnabled}, nil
	}
	if err != nil {
		return ReadOut{}, err
	}
	defer s.lock(botID)()
	st, err := Read(ctx, f)
	if err != nil {
		return ReadOut{}, err
	}
	if err := s.noticeAll(ctx, botID, st, "", 0); err != nil {
		return ReadOut{}, err
	}
	return ReadOut{State: st, Enabled: b.MemoryEnabled, Computer: true}, nil
}

// Notice 讀 Bot 的記憶檔，Bot 改過就記一筆（Bot 用記憶工具之後馬上呼叫，時間和對話串才對）。
// thread 是 Bot 在哪條對話串改的；at 是什麼時候（毫秒，0 表示現在）。Bot 還沒有電腦時不做事。
func (s *Service) Notice(ctx context.Context, botID, thread string, at int64) error {
	f, err := s.Locate(ctx, botID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return nil
	}
	if err != nil {
		return err
	}
	defer s.lock(botID)()
	st, err := Read(ctx, f)
	if err != nil {
		return err
	}
	return s.noticeAll(ctx, botID, st, thread, at)
}

func (s *Service) noticeAll(ctx context.Context, botID string, st State, thread string, at int64) error {
	for _, t := range []Target{TargetUser, TargetMemory} {
		if err := s.noticeBot(ctx, botID, t, Join(st.Get(t).Entries), thread, at); err != nil {
			return err
		}
	}
	return nil
}

// noticeBot 比對檔案和最後一次紀錄；不同就是 Bot 自己改的，記一筆。還沒有任何紀錄時從空白算起
// （否則 Bot 自己記的事要等你改過一次才看得到）。thread、at 見 Notice。
func (s *Service) noticeBot(ctx context.Context, botID string, t Target, current, thread string, at int64) error {
	vs, err := s.DB.ListMemoryVersions(ctx, botID, string(t))
	if err != nil {
		return err
	}
	v := store.MemoryVersion{BotID: botID, Target: string(t), After: current, Actor: "bot:" + botID, ThreadID: thread, CreatedAt: at}
	if len(vs) > 0 {
		v.Before = vs[0].After
		if at <= vs[0].CreatedAt {
			v.CreatedAt = 0 // 不要排到最後一筆紀錄前面：用現在
		}
	}
	if Join(Entries(v.Before)) == current {
		return nil
	}
	_, err = s.DB.AddMemoryVersion(ctx, v, limits.MemoryVersionsKept)
	return err
}

func toOpsErr(err error) error {
	var inv *InvalidError
	switch {
	case errors.As(err, &inv):
		return ops.InvalidInput(ops.FieldError{Field: "entries", Message: inv.Msg})
	case errors.Is(err, ErrChanged):
		return ops.Conflict(err.Error())
	}
	return err
}

// Write 換掉一份記憶並記一筆變更。
func (s *Service) Write(ctx context.Context, botID string, t Target, entries, previous []string, actor string) (ReadOut, error) {
	b, err := s.bot(ctx, s.DB, botID)
	if err != nil {
		return ReadOut{}, err
	}
	if !t.valid() {
		return ReadOut{}, ops.InvalidInput(ops.FieldError{Field: "target", Message: "要是 user 或 memory"})
	}
	f, err := s.Locate(ctx, botID, true)
	if err != nil {
		return ReadOut{}, err
	}
	unlock := s.lock(botID)
	if err := s.write(ctx, f, botID, t, entries, previous, actor, ""); err != nil {
		unlock()
		return ReadOut{}, err
	}
	st, err := Read(ctx, f)
	unlock()
	if err != nil {
		return ReadOut{}, err
	}
	return ReadOut{State: st, Enabled: b.MemoryEnabled, Computer: true}, nil
}

// write 換掉一份記憶並記一筆變更；thread 是 Bot 在哪條對話串改的（不是 Bot 時空白）。
func (s *Service) write(ctx context.Context, f boxfs.FS, botID string, t Target, entries, previous []string, actor, thread string) error {
	raw, err := ReadRaw(ctx, f, t)
	if err != nil {
		return err
	}
	if err := s.noticeBot(ctx, botID, t, Join(Entries(raw)), "", 0); err != nil {
		return err
	}
	before, after, err := Write(ctx, f, t, entries, previous)
	if err != nil {
		return toOpsErr(err)
	}
	if Join(Entries(before)) == after {
		return nil
	}
	_, err = s.DB.AddMemoryVersion(ctx, store.MemoryVersion{BotID: botID, Target: string(t), Before: Join(Entries(before)), After: after, Actor: actor,
		ThreadID: thread}, limits.MemoryVersionsKept)
	return err
}

// Add 在一份記憶後面加一條（Bot 自己用；上限照 Hermes）。已經有一樣的就不重複加。
func (s *Service) Add(ctx context.Context, db *store.DB, botID string, t Target, content, actor string) (ReadOut, error) {
	b, err := s.bot(ctx, db, botID)
	if err != nil {
		return ReadOut{}, err
	}
	if !b.MemoryEnabled {
		return ReadOut{}, ops.Conflict("使用者關掉了你的記憶，不能寫入")
	}
	if !t.valid() {
		return ReadOut{}, ops.InvalidInput(ops.FieldError{Field: "target", Message: "要是 user 或 memory"})
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ReadOut{}, ops.InvalidInput(ops.FieldError{Field: "content", Message: "不能是空的"})
	}
	f, err := s.Locate(ctx, botID, true)
	if err != nil {
		return ReadOut{}, err
	}
	defer s.lock(botID)()
	raw, err := ReadRaw(ctx, f, t)
	if err != nil {
		return ReadOut{}, err
	}
	current := Entries(raw)
	clean, err := Clean([]string{content})
	if err != nil {
		return ReadOut{}, toOpsErr(err)
	}
	next := current
	if len(clean) == 1 && !containsStr(current, clean[0]) {
		next = append(append([]string{}, current...), clean[0])
	}
	var thread string
	if s.Active != nil {
		thread, _ = s.Active(botID)
	}
	if err := s.write(ctx, f, botID, t, next, current, actor, thread); err != nil {
		var oe *ops.Error
		if errors.As(err, &oe) && oe.Code == ops.CodeInvalidInput {
			return ReadOut{}, ops.Limit("這份記憶滿了：" + oe.Message + "。請使用者整理，或改寫成更短的一條")
		}
		return ReadOut{}, err
	}
	st, err := Read(ctx, f)
	if err != nil {
		return ReadOut{}, err
	}
	return ReadOut{State: st, Enabled: b.MemoryEnabled, Computer: true}, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// History 列出變更（新的在前）；之後沒再改過（檔案現在就是 after）的可以復原。
func (s *Service) History(ctx context.Context, db *store.DB, botID string, t Target) (HistoryOut, error) {
	if _, err := s.bot(ctx, db, botID); err != nil {
		return HistoryOut{}, err
	}
	if t != "" && !t.valid() {
		return HistoryOut{}, ops.InvalidInput(ops.FieldError{Field: "target", Message: "要是 user 或 memory"})
	}
	// 先讀一次，把 Bot 自己的修改記進來。
	cur := map[Target]string{}
	if out, err := s.Read(ctx, db, botID); err == nil && out.Computer {
		cur[TargetUser], cur[TargetMemory] = Join(out.User.Entries), Join(out.Memory.Entries)
	} else if err != nil {
		return HistoryOut{}, err
	}
	vs, err := db.ListMemoryVersions(ctx, botID, string(t))
	if err != nil {
		return HistoryOut{}, err
	}
	out := HistoryOut{Changes: []Change{}}
	seen := map[Target]bool{}
	places := map[string]place{}
	for _, v := range vs {
		tt := Target(v.Target)
		before, after := Entries(v.Before), Entries(v.After)
		c := Change{ID: v.ID, Target: tt, Actor: v.Actor, Added: diff(after, before), Removed: diff(before, after), CreatedAt: v.CreatedAt}
		if v.ThreadID != "" {
			p, ok := places[v.ThreadID]
			if !ok {
				p = where(ctx, db, v.ThreadID)
				places[v.ThreadID] = p
			}
			c.ThreadID, c.ThreadTitle, c.RoomID = p.thread, p.title, p.room
		}
		c.Undoable = !seen[tt] && cur[tt] == Join(after)
		seen[tt] = true
		out.Changes = append(out.Changes, c)
	}
	return out, nil
}

// place 是一次變更在哪裡記的：對話串（標題），或群組（名稱）。
type place struct{ thread, title, room string }

// where 找對話串的標題；Bot 在群組裡的座位用群組名稱。對話串刪掉了就什麼都不給。
func where(ctx context.Context, db *store.DB, threadID string) place {
	th, err := db.GetThread(ctx, threadID)
	if err != nil {
		return place{}
	}
	p := place{thread: th.ID, title: th.Title}
	if th.RoomID != "" {
		p.room, p.title = th.RoomID, ""
		if r, err := db.GetRoom(ctx, th.RoomID); err == nil {
			p.title = r.Name
		}
	}
	return p
}

func diff(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if !containsStr(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// Restore 把一次變更復原（記憶改回變更之前）。
func (s *Service) Restore(ctx context.Context, botID, changeID, actor string) (ReadOut, error) {
	v, err := s.DB.GetMemoryVersion(ctx, changeID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.BotID != botID) {
		return ReadOut{}, ops.NotFound("找不到這次變更")
	}
	if err != nil {
		return ReadOut{}, err
	}
	f, err := s.Locate(ctx, botID, true)
	if err != nil {
		return ReadOut{}, err
	}
	t := Target(v.Target)
	raw, err := ReadRaw(ctx, f, t)
	if err != nil {
		return ReadOut{}, err
	}
	if Join(Entries(raw)) != Join(Entries(v.After)) {
		return ReadOut{}, ops.Conflict("這份記憶之後又改過了，不能復原這一次")
	}
	out, err := s.Write(ctx, botID, t, Entries(v.Before), Entries(v.After), actor)
	if err != nil {
		var oe *ops.Error
		if errors.As(err, &oe) && oe.Code == ops.CodeConflict {
			return ReadOut{}, ops.Conflict(fmt.Sprintf("復原失敗：%s", oe.Message))
		}
	}
	return out, err
}
