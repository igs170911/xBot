// Package teaching 是示範教學（B32，PRD BX-9，畫面 E04、E05）：你在 Bot 的電腦上親自做一次，錄製器記下每一步，
// 做完 XBot 把步驟整理成技能草稿（SKILL.md），你看過、改過再存成這個 Bot 的技能。
//
//   - 開始：XBot 接手畫面（B14 screen.Takeover：Bot 暫停、排隊的工作先等），在電腦裡以 Bot 使用者（1000:1000）啟動兩個錄製器：
//     瀏覽器（box-teach-browser.mjs，經 CDP 注入、記語意步驟與截圖）與桌面 App（box-teach-desktop，X RECORD＋AT-SPI）。
//     錄製檔寫在 /workspace/.teaching/<id>（可寫的 volume；根檔案系統是唯讀的）。
//   - 示範中：每秒讀一次錄製檔，發 teaching.updated 事件（即時步驟清單）；可補充說明、暫停、刪掉做錯的步驟。
//   - 完成：停掉錄製器、交還畫面，步驟照 LazyBoy server/teaching.py 整理，寫成技能草稿（快判第二層有開時潤飾名稱與用途）。
//   - 存檔：草稿（你改過的）＋挑出的截圖存進 Bot 的 /hermes/skills（C31），刪掉錄製檔；預設請 Bot 試做一次（E05）。
//
// 和 LazyBoy 不同：LazyBoy 完成時直接交給 Bot 自己寫技能；XBot 先產生草稿給你確認（任務卡 B32、E04 的「整理技能中」）。
package teaching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/runtime"
	"xbot/internal/screen"
	"xbot/internal/skills"
	"xbot/internal/store"
)

// KindTeaching 是示範教學有變化的事件（開始、新步驟、暫停、補充、完成、存檔、捨棄）。payload：Event。
const KindTeaching = "teaching.updated"

// Dir 是錄製檔放的地方（Bot 電腦裡；/workspace 是可寫的 volume）。
const Dir = box.WorkspaceDir + "/.teaching"

// SourceTaught 是示範教學存成的技能來源。
const SourceTaught = "taught"

// 錄製器（box/）。
const (
	browserRecorder = "/usr/local/bin/box-teach-browser.mjs"
	desktopRecorder = "/usr/local/bin/box-teach-desktop"
	// launch 在背景啟動兩個錄製器（$0 是錄製資料夾；輸出寫到資料夾裡的 .log）。
	launch = `cd "$0" && DISPLAY=:1 nohup node ` + browserRecorder + ` "$0" >"$0/browser.log" 2>&1 </dev/null &
cd "$0" && DISPLAY=:1 nohup /usr/bin/python3 ` + desktopRecorder + ` "$0" >"$0/desktop.log" 2>&1 </dev/null &`
)

// Screen 是接手與交還（B14）；*screen.Service 符合。
type Screen interface {
	Takeover(ctx context.Context, botID, by, byName string) (screen.Takeover, error)
	Handback(ctx context.Context, botID, by, note string) (screen.HandbackResult, error)
	TakeoverOf(botID string) *screen.Takeover
}

// Runtime 是 Bot 的工作；*runtime.Runtime 符合。
type Runtime interface {
	Active(botID string) (threadID, taskID string)
	Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error)
	Note(ctx context.Context, botID, threadID, activity, text string) error
}

// CreateSkill 在 Bot 身上新增一個技能（C31 skills.Bot.Create）。
type CreateSkill func(ctx context.Context, botID string, e skills.Edit) (skills.Skill, error)

// Polisher 請快判第二層把步驟整理成技能名稱與用途；沒有開或沒有回答時 ok=false（照範本）。
type Polisher func(ctx context.Context, text, botID string) (name, description string, ok bool)

// Options 是 Service 的設定。
type Options struct {
	DB      *store.DB
	Screen  Screen
	Runtime Runtime
	Open    Opener
	Create  CreateSkill
	// Polish 可為 nil（只用範本）。
	Polish Polisher
	Bus    *events.Bus
	Logger *slog.Logger
	// Poll、StartTimeout、StopTimeout：0 用 limits 的值（測試調短）。
	Poll, StartTimeout, StopTimeout time.Duration
}

// Service 管理每個 Bot 的示範教學。錄製中的狀態（錄製器、讀到的紀錄）只在記憶體；服務重開時錄製中的教學會被捨棄（Recover）。
type Service struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	live     map[string]*session // botID → 錄製中的教學
	starting map[string]bool
}

type session struct {
	lessonID string
	cancel   context.CancelFunc
	comp     Computer
	rows     []row
	size     int
	ending   bool
}

// 錯誤（操作層轉成 ops 錯誤）。
var (
	ErrBusy         = errors.New("這個 Bot 正在工作，請等它做完或先停止，再開始教學")
	ErrStarting     = errors.New("正在開始示範，請稍候")
	ErrNotOpen      = errors.New("這次教學已經結束")
	ErrNoSteps      = errors.New("還沒有記錄到任何操作。請先在電腦上示範，或取消這次教學")
	ErrUnsupported  = errors.New("這台電腦的環境還不支援教學，請先在電腦設定按「更新電腦」")
	ErrDraftPending = errors.New("上一次示範的技能草稿還沒存檔或捨棄")
	ErrNotReady     = errors.New("瀏覽器紀錄沒有啟動，請確認電腦的瀏覽器有開著")
	ErrNoShot       = errors.New("找不到這張截圖")
	ErrEmptyNote    = errors.New("請輸入補充說明")
)

// New 建立 Service。
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Poll <= 0 {
		o.Poll = limits.TeachPoll
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = limits.TeachStartTimeout
	}
	if o.StopTimeout <= 0 {
		o.StopTimeout = limits.TeachStopTimeout
	}
	return &Service{o: o, log: o.Logger, live: map[string]*session{}, starting: map[string]bool{}}
}

// Lesson 是一次示範教學（介面看到的樣子）。
type Lesson struct {
	ID        string `json:"id"`
	BotID     string `json:"bot_id"`
	Goal      string `json:"goal" desc:"要教什麼（可以做完再補）"`
	State     string `json:"state" desc:"recording 錄製中、paused 暫停、stopped 錄完（草稿等你確認）、saved 已存成技能、discarded 已捨棄"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	Steps     []Step `json:"steps"`
	Draft     *Draft `json:"draft,omitempty"`
	SkillID   string `json:"skill_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	// TrialTaskID 是存檔後 Bot 試做一次的工作。
	TrialTaskID string `json:"trial_task_id,omitempty"`
}

// Event 是 teaching.updated 的內容。Lesson 是 nil 時這個 Bot 沒有進行中的教學。
type Event struct {
	BotID  string  `json:"bot_id"`
	Lesson *Lesson `json:"lesson"`
}

func dirOf(id string) string { return Dir + "/" + id }

func decode[T any](raw json.RawMessage) T {
	var v T
	_ = json.Unmarshal(raw, &v)
	return v
}

func encode(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// view 組出介面看到的樣子；錄製中的步驟從記憶體裡的紀錄即時整理。
func (s *Service) view(l store.Lesson) Lesson {
	v := Lesson{ID: l.ID, BotID: l.BotID, Goal: l.Goal, State: l.State, StartedAt: l.StartedAt, EndedAt: l.EndedAt, SkillID: l.SkillID,
		ThreadID: l.ThreadID, TrialTaskID: l.TrialTaskID}
	if l.Recording() {
		s.mu.Lock()
		var rows []row
		if ss := s.live[l.BotID]; ss != nil && ss.lessonID == l.ID {
			rows = ss.rows
		}
		s.mu.Unlock()
		v.Steps = normalize(rows, decode[[]Note](l.Notes), decode[[]string](l.Dropped))
	} else {
		v.Steps = decode[[]Step](l.Steps)
	}
	if v.Steps == nil {
		v.Steps = []Step{}
	}
	if d := decode[Draft](l.Draft); d.Name != "" || d.Instructions != "" {
		if d.Shots == nil {
			d.Shots = []string{}
		}
		v.Draft = &d
	}
	return v
}

// publish 發 teaching.updated；persist 時寫進 event 表（開始、完成、存檔、捨棄），步驟增加只即時發布。
func (s *Service) publish(botID string, l *Lesson, persist bool) {
	if s.o.Bus == nil {
		return
	}
	raw, _ := json.Marshal(Event{BotID: botID, Lesson: l})
	e := events.Event{Kind: KindTeaching, BotID: botID, Payload: raw, At: time.Now().UnixMilli()}
	if persist {
		row, err := s.o.DB.AppendEvent(context.Background(), store.Event{BotID: botID, Kind: KindTeaching, Payload: raw})
		if err == nil {
			e.ID, e.At = row.ID, row.CreatedAt
		}
	}
	s.o.Bus.Publish(e)
}

func (s *Service) announce(l store.Lesson, persist bool) Lesson {
	v := s.view(l)
	if l.Open() {
		s.publish(l.BotID, &v, persist)
	} else {
		s.publish(l.BotID, nil, persist)
	}
	return v
}

// note 在開始示範的對話串記一行系統訊息（沒有對話串時不記）。
func (s *Service) note(ctx context.Context, l store.Lesson, text string) {
	if l.ThreadID == "" || s.o.Runtime == nil {
		return
	}
	if err := s.o.Runtime.Note(context.WithoutCancel(ctx), l.BotID, l.ThreadID, runtime.ActivityTeaching, text); err != nil {
		s.log.Warn("記錄示範教學失敗", "bot", l.BotID, "err", err)
	}
}

// Current 回傳 Bot 進行中的教學（錄製中、暫停、草稿等你確認）；沒有時 nil。
func (s *Service) Current(ctx context.Context, botID string) (*Lesson, error) {
	if _, err := s.o.DB.GetBot(ctx, botID); err != nil {
		return nil, err
	}
	l, err := s.o.DB.OpenLesson(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := s.view(l)
	return &v, nil
}

// lesson 讀 Bot 的這次教學（別的 Bot 的當成找不到）。
func (s *Service) lesson(ctx context.Context, botID, lessonID string) (store.Lesson, error) {
	l, err := s.o.DB.GetLesson(ctx, lessonID)
	if err == nil && l.BotID != botID {
		err = store.ErrNotFound
	}
	return l, err
}

// StartRequest 是開始示範。
type StartRequest struct {
	BotID, Goal, ThreadID string
	// By、ByName 是誰在示範（接手畫面的人）。
	By, ByName string
}

// Start 開始示範：叫醒電腦、接手畫面、啟動錄製器，等瀏覽器錄製器就緒。同一個 Bot 已經在示範時回傳那一次。
func (s *Service) Start(ctx context.Context, req StartRequest) (Lesson, error) {
	ctx = context.WithoutCancel(ctx)
	bot, err := s.o.DB.GetBot(ctx, req.BotID)
	if err != nil {
		return Lesson{}, err
	}
	s.mu.Lock()
	if s.starting[bot.ID] {
		s.mu.Unlock()
		return Lesson{}, ErrStarting
	}
	s.starting[bot.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, bot.ID)
		s.mu.Unlock()
	}()

	if l, err := s.o.DB.OpenLesson(ctx, bot.ID); err == nil {
		if l.State == store.LessonStopped {
			return Lesson{}, ErrDraftPending
		}
		s.mu.Lock()
		ss := s.live[bot.ID]
		s.mu.Unlock()
		if ss != nil && ss.lessonID == l.ID {
			return s.view(l), nil
		}
		// 錄製中卻沒有在跑的錄製器（不會發生；Recover 會收掉）：當成已捨棄。
		l.State, l.EndedAt = store.LessonDiscarded, store.NowMS()
		_, _ = s.o.DB.UpdateLesson(ctx, l)
	} else if !errors.Is(err, store.ErrNotFound) {
		return Lesson{}, err
	}
	if thread, _ := s.o.Runtime.Active(bot.ID); thread != "" {
		return Lesson{}, ErrBusy
	}

	comp, err := s.o.Open(ctx, bot.ID, true)
	if err != nil {
		return Lesson{}, err
	}
	if err := comp.Run(ctx, "test", "-x", desktopRecorder, "-a", "-f", browserRecorder); err != nil {
		var exit *boxfs.ExitError
		if errors.As(err, &exit) {
			return Lesson{}, ErrUnsupported
		}
		return Lesson{}, err
	}
	took := s.o.Screen.TakeoverOf(bot.ID) == nil
	if _, err := s.o.Screen.Takeover(ctx, bot.ID, req.By, req.ByName); err != nil {
		return Lesson{}, err
	}
	l, err := s.o.DB.CreateLesson(ctx, store.Lesson{BotID: bot.ID, Goal: clip(req.Goal, limits.TeachGoalMax), ThreadID: req.ThreadID, CreatedBy: req.By})
	if err != nil {
		s.release(ctx, bot.ID, req.By, took)
		return Lesson{}, err
	}
	dir := dirOf(l.ID)
	fail := func(err error) (Lesson, error) {
		s.stopRecorders(ctx, comp, l.ID)
		if rerr := comp.Remove(ctx, dir); rerr != nil {
			s.log.Warn("清掉錄製資料夾失敗", "bot", bot.ID, "err", rerr)
		}
		s.release(ctx, bot.ID, req.By, took)
		l.State, l.EndedAt = store.LessonDiscarded, store.NowMS()
		_, _ = s.o.DB.UpdateLesson(ctx, l)
		return Lesson{}, err
	}
	if err := comp.Run(ctx, "mkdir", "-p", dir+"/shots"); err != nil {
		return fail(err)
	}
	if err := comp.Run(ctx, "sh", "-c", launch, dir); err != nil {
		return fail(err)
	}
	deadline := time.Now().Add(s.o.StartTimeout)
	for {
		if _, err := comp.ReadFile(ctx, dir+"/.ready-browser"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			if logText, err := comp.ReadFile(ctx, dir+"/browser.log"); err == nil {
				s.log.Warn("瀏覽器錄製器沒有啟動", "bot", bot.ID, "log", tail(string(logText), 800))
			}
			return fail(ErrNotReady)
		}
		time.Sleep(min(250*time.Millisecond, s.o.StartTimeout/4))
	}
	if _, err := comp.ReadFile(ctx, dir+"/.ready-desktop"); err != nil {
		s.log.Warn("桌面錄製器沒有就緒，只記瀏覽器的操作", "bot", bot.ID, "lesson", l.ID)
	}
	wctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.live[bot.ID] = &session{lessonID: l.ID, cancel: cancel, comp: comp}
	s.mu.Unlock()
	go s.watch(wctx, bot.ID, l.ID)
	s.note(ctx, l, "你開始示範，"+bot.Name+"在旁邊看著，每一步都會記下來")
	return s.announce(l, true), nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// release 交還畫面（只在是這次教學接手的時候）。
func (s *Service) release(ctx context.Context, botID, by string, took bool) {
	if !took {
		return
	}
	if _, err := s.o.Screen.Handback(context.WithoutCancel(ctx), botID, by, "示範教學結束，畫面可能和之前不一樣，請先看一下目前的畫面。"); err != nil &&
		!errors.Is(err, screen.ErrNotTakenOver) {
		s.log.Warn("示範結束交還畫面失敗", "bot", botID, "err", err)
	}
}

// readRows 讀錄製檔；回傳紀錄和總大小（判斷有沒有新的步驟）。
func readRows(ctx context.Context, c Computer, lessonID string) ([]row, int, error) {
	var data [][]byte
	total := 0
	for _, name := range []string{"browser.jsonl", "desktop.jsonl"} {
		b, err := c.ReadFile(ctx, dirOf(lessonID)+"/"+name)
		if errors.Is(err, boxfs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if total += len(b); total > limits.TeachRowsBytes {
			return nil, 0, errors.New("錄製紀錄太大")
		}
		data = append(data, b)
	}
	return parseRows(data...), total, nil
}

// refresh 重讀錄製檔；有新的紀錄時回 true。
func (s *Service) refresh(ctx context.Context, botID, lessonID string) (bool, error) {
	s.mu.Lock()
	ss := s.live[botID]
	s.mu.Unlock()
	if ss == nil || ss.lessonID != lessonID {
		return false, ErrNotOpen
	}
	rows, size, err := readRows(ctx, ss.comp, lessonID)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := size != ss.size
	ss.rows, ss.size = rows, size
	return changed, nil
}

// watch 在示範中每秒看一次錄製檔，有新步驟就發事件。畫面被交還（你在別處交還、接手太久沒人看自動交還）時自動結束示範。
func (s *Service) watch(ctx context.Context, botID, lessonID string) {
	tick := time.NewTicker(s.o.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if s.o.Screen.TakeoverOf(botID) == nil {
			s.log.Info("示範中畫面被交還，自動結束示範", "bot", botID, "lesson", lessonID)
			if _, err := s.finish(ctx, botID, lessonID, "", nil, true); err != nil && !errors.Is(err, ErrNotOpen) {
				s.log.Warn("自動結束示範失敗", "bot", botID, "err", err)
			}
			return
		}
		changed, err := s.refresh(ctx, botID, lessonID)
		if errors.Is(err, ErrNotOpen) {
			return
		}
		if err != nil {
			s.log.Debug("讀錄製紀錄失敗", "bot", botID, "err", err)
			continue
		}
		if changed {
			if l, err := s.o.DB.GetLesson(ctx, lessonID); err == nil && l.Recording() {
				s.announce(l, false)
			}
		}
	}
}

// change 改錄製中的教學（補充、刪步驟、暫停）並發事件。
func (s *Service) change(ctx context.Context, botID, lessonID string, fn func(*store.Lesson, Computer) error) (Lesson, error) {
	l, err := s.lesson(ctx, botID, lessonID)
	if err != nil {
		return Lesson{}, err
	}
	s.mu.Lock()
	ss := s.live[botID]
	ok := ss != nil && ss.lessonID == lessonID && !ss.ending && l.Recording()
	s.mu.Unlock()
	if !ok {
		return Lesson{}, ErrNotOpen
	}
	if err := fn(&l, ss.comp); err != nil {
		return Lesson{}, err
	}
	if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
		return Lesson{}, err
	}
	return s.announce(l, false), nil
}

// Pause 暫停或繼續錄製（錄製器看 .paused 檔：暫停時的操作不記）。
func (s *Service) Pause(ctx context.Context, botID, lessonID string, paused bool) (Lesson, error) {
	return s.change(ctx, botID, lessonID, func(l *store.Lesson, c Computer) error {
		flag := dirOf(l.ID) + "/.paused"
		if paused {
			l.State = store.LessonPaused
			return c.Run(ctx, "touch", flag)
		}
		l.State = store.LessonRecording
		return c.Run(ctx, "rm", "-f", flag)
	})
}

// AddNote 補充一句說明（成為一步）。
func (s *Service) AddNote(ctx context.Context, botID, lessonID, text string) (Lesson, error) {
	text = clip(text, limits.TeachNoteMax)
	if text == "" {
		return Lesson{}, ErrEmptyNote
	}
	return s.change(ctx, botID, lessonID, func(l *store.Lesson, _ Computer) error {
		l.Notes = encode(append(decode[[]Note](l.Notes), Note{At: store.NowMS(), Text: text}))
		return nil
	})
}

// Drop 刪掉做錯的一步（編號自動重排）。
func (s *Service) Drop(ctx context.Context, botID, lessonID, stepID string) (Lesson, error) {
	return s.change(ctx, botID, lessonID, func(l *store.Lesson, _ Computer) error {
		l.Dropped = encode(append(decode[[]string](l.Dropped), stepID))
		return nil
	})
}

// stopRecorders 叫錄製器結束（.stop），等它們寫完（打到一半的字會在結束時寫出），最後保險再 pkill。
func (s *Service) stopRecorders(ctx context.Context, c Computer, lessonID string) {
	dir := dirOf(lessonID)
	if err := c.Run(ctx, "touch", dir+"/.stop"); err != nil {
		s.log.Debug("通知錄製器結束失敗", "lesson", lessonID, "err", err)
	}
	deadline := time.Now().Add(s.o.StopTimeout)
	for time.Now().Before(deadline) {
		_, b := c.ReadFile(ctx, dir+"/.done-browser")
		_, d := c.ReadFile(ctx, dir+"/.done-desktop")
		if b == nil && d == nil {
			break
		}
		time.Sleep(min(200*time.Millisecond, s.o.StopTimeout/5))
	}
	// pkill 沒找到行程時結束碼是 1，不算錯。
	_ = c.Run(ctx, "pkill", "-f", "box-teach-.*"+lessonID)
}

// claim 標記這次教學正在結束（第二次完成或取消回 ErrNotOpen），並停掉 watch。
func (s *Service) claim(botID, lessonID string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.live[botID]
	if ss == nil || ss.lessonID != lessonID || ss.ending {
		return nil, ErrNotOpen
	}
	ss.ending = true
	ss.cancel()
	return ss, nil
}

func (s *Service) forget(botID, lessonID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.live[botID]; ss != nil && ss.lessonID == lessonID {
		ss.cancel()
		delete(s.live, botID)
	}
}

// Finish 完成示範（E04「完成，請它學起來」）：停掉錄製器、交還畫面、整理步驟、產生技能草稿。goal 不是 nil 時順便補上要教什麼。
func (s *Service) Finish(ctx context.Context, botID, lessonID, by string, goal *string) (Lesson, error) {
	return s.finish(ctx, botID, lessonID, by, goal, false)
}

func (s *Service) finish(ctx context.Context, botID, lessonID, by string, goal *string, auto bool) (Lesson, error) {
	ctx = context.WithoutCancel(ctx)
	l, err := s.lesson(ctx, botID, lessonID)
	if err != nil {
		return Lesson{}, err
	}
	if !l.Recording() {
		return Lesson{}, ErrNotOpen
	}
	if !auto {
		// 打到一半、還沒寫出的字在錄製器結束時才寫；其他的都已經在檔案裡。
		if _, err := s.refresh(ctx, botID, lessonID); err != nil && !errors.Is(err, ErrNotOpen) {
			return Lesson{}, err
		}
		if v := s.view(l); len(v.Steps) == 0 {
			return Lesson{}, ErrNoSteps
		}
	}
	ss, err := s.claim(botID, lessonID)
	if err != nil {
		return Lesson{}, err
	}
	s.stopRecorders(ctx, ss.comp, lessonID)
	rows, _, rerr := readRows(ctx, ss.comp, lessonID)
	if rerr != nil {
		s.log.Warn("讀錄製紀錄失敗", "bot", botID, "err", rerr)
		rows = ss.rows
	}
	s.forget(botID, lessonID)
	if !auto {
		s.release(ctx, botID, by, true)
	}
	steps := normalize(rows, decode[[]Note](l.Notes), decode[[]string](l.Dropped))
	if goal != nil {
		l.Goal = clip(*goal, limits.TeachGoalMax)
	}
	l.EndedAt = store.NowMS()
	if len(steps) == 0 {
		// 自動結束（畫面被交還）而且什麼都沒記到：直接捨棄。
		l.State = store.LessonDiscarded
		s.removeDir(ctx, ss.comp, botID, lessonID)
		if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
			return Lesson{}, err
		}
		s.note(ctx, l, "示範結束，沒有記到任何操作")
		return s.announce(l, true), nil
	}
	d := s.draft(ctx, l.Goal, steps, botID)
	l.State, l.Steps, l.Draft = store.LessonStopped, encode(steps), encode(d)
	if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
		return Lesson{}, err
	}
	s.note(ctx, l, fmt.Sprintf("示範完成，記下 %d 步，整理成技能草稿等你確認", len(steps)))
	return s.announce(l, true), nil
}

// draft 照範本整理技能草稿；快判第二層有開而且有回答時，用它的名稱與用途。
func (s *Service) draft(ctx context.Context, goal string, steps []Step, botID string) Draft {
	d := buildDraft(goal, steps)
	if s.o.Polish == nil {
		return d
	}
	name, desc, ok := s.o.Polish(ctx, stepsText(goal, steps), botID)
	if !ok {
		return d
	}
	if name = clip(strings.Trim(name, "「」\"'"), limits.SkillNameMax); name != "" && !strings.ContainsAny(name, "/\\") {
		d.Name, d.By = name, "model"
	}
	if desc = clip(desc, limits.SkillDescriptionMax); desc != "" {
		d.Description, d.By = desc, "model"
	}
	return d
}

// Redraft 重新整理草稿（補上要教什麼之後）。只有錄完、還沒存的教學可以。
func (s *Service) Redraft(ctx context.Context, botID, lessonID string, goal *string) (Lesson, error) {
	l, err := s.lesson(ctx, botID, lessonID)
	if err != nil {
		return Lesson{}, err
	}
	if l.State != store.LessonStopped {
		return Lesson{}, ErrNotOpen
	}
	if goal != nil {
		l.Goal = clip(*goal, limits.TeachGoalMax)
	}
	l.Draft = encode(s.draft(ctx, l.Goal, decode[[]Step](l.Steps), botID))
	if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
		return Lesson{}, err
	}
	return s.announce(l, false), nil
}

func (s *Service) removeDir(ctx context.Context, c Computer, botID, lessonID string) {
	if c == nil {
		var err error
		if c, err = s.o.Open(ctx, botID, false); err != nil {
			s.log.Warn("清掉錄製資料夾失敗", "bot", botID, "err", err)
			return
		}
	}
	if err := c.Remove(ctx, dirOf(lessonID)); err != nil {
		s.log.Warn("清掉錄製資料夾失敗", "bot", botID, "lesson", lessonID, "err", err)
	}
}

// Discard 捨棄這次教學：錄製中的先停掉錄製器、交還畫面；刪掉錄製檔。
func (s *Service) Discard(ctx context.Context, botID, lessonID, by string) (Lesson, error) {
	ctx = context.WithoutCancel(ctx)
	l, err := s.lesson(ctx, botID, lessonID)
	if err != nil {
		return Lesson{}, err
	}
	if !l.Open() {
		return Lesson{}, ErrNotOpen
	}
	var comp Computer
	if l.Recording() {
		ss, err := s.claim(botID, lessonID)
		if err != nil {
			return Lesson{}, err
		}
		comp = ss.comp
		s.stopRecorders(ctx, comp, lessonID)
		s.forget(botID, lessonID)
		s.release(ctx, botID, by, true)
	}
	s.removeDir(ctx, comp, botID, lessonID)
	l.State, l.EndedAt = store.LessonDiscarded, store.NowMS()
	if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
		return Lesson{}, err
	}
	s.note(ctx, l, "已取消這次示範")
	return s.announce(l, true), nil
}

// SaveRequest 是把草稿存成技能（你看過、可能改過的內容）。
type SaveRequest struct {
	BotID, LessonID                 string
	Name, Description, Instructions string
	// Trial：存好後請 Bot 試做一次（E05：預設開）。
	Trial bool
	By    string
}

// SaveResult 是存檔的結果。
type SaveResult struct {
	Lesson Lesson       `json:"lesson"`
	Skill  skills.Skill `json:"skill"`
	// TaskID、ThreadID 是 Bot 試做一次的工作與對話串（沒有試做時空白）。
	TaskID   string `json:"task_id,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Save 把草稿存成 Bot 的技能：SKILL.md＋內文還提到的截圖（shots/…）；刪掉錄製檔；trial 時請 Bot 試做一次。
func (s *Service) Save(ctx context.Context, req SaveRequest) (SaveResult, error) {
	ctx = context.WithoutCancel(ctx)
	l, err := s.lesson(ctx, req.BotID, req.LessonID)
	if err != nil {
		return SaveResult{}, err
	}
	if l.State != store.LessonStopped {
		return SaveResult{}, ErrNotOpen
	}
	d := decode[Draft](l.Draft)
	comp, err := s.o.Open(ctx, req.BotID, false)
	if err != nil {
		return SaveResult{}, err
	}
	var files []boxfs.File
	for _, name := range usedShots(d, req.Instructions) {
		b, err := comp.ReadFile(ctx, dirOf(l.ID)+"/"+name)
		if err != nil || len(b) > limits.TeachShotBytes {
			s.log.Warn("截圖讀不到或太大，不放進技能", "bot", req.BotID, "shot", name, "err", err)
			continue
		}
		files = append(files, boxfs.File{Name: name, Data: b, Mode: 0o644})
	}
	name, desc, instr := req.Name, req.Description, req.Instructions
	sk, err := s.o.Create(ctx, req.BotID, skills.Edit{Name: &name, Description: &desc, Instructions: &instr, Source: SourceTaught, Files: files})
	if err != nil {
		return SaveResult{}, err
	}
	s.removeDir(ctx, comp, req.BotID, l.ID)
	l.State, l.SkillID = store.LessonSaved, sk.ID
	if l.EndedAt == 0 {
		l.EndedAt = store.NowMS()
	}
	d.Name, d.Description, d.Instructions = sk.Name, sk.Description, req.Instructions
	l.Draft = encode(d)
	out := SaveResult{Skill: sk}
	if req.Trial {
		meta, _ := json.Marshal(map[string]string{"lesson_id": l.ID})
		res, err := s.o.Runtime.Start(ctx, runtime.ChatRequest{BotID: req.BotID, Text: "試做一次剛學會的「" + sk.Name + "」",
			Prompt: trialPrompt(sk.Name), Title: "試做：" + sk.Name, TaskMeta: meta, Queue: true, Actor: req.By})
		if err != nil {
			out.Note = "技能已經存好，但請 Bot 試做時出了問題：" + err.Error()
		} else {
			l.TrialTaskID, out.TaskID, out.ThreadID = res.TaskID, res.TaskID, res.ThreadID
		}
	}
	if l, err = s.o.DB.UpdateLesson(ctx, l); err != nil {
		return SaveResult{}, err
	}
	s.note(ctx, l, "學會了新技能「"+sk.Name+"」")
	out.Lesson = s.announce(l, true)
	return out, nil
}

// Shot 讀一張示範截圖（JPEG）。錄製檔在存檔或捨棄後就刪掉了，那時回 ErrNoShot。
func (s *Service) Shot(ctx context.Context, botID, lessonID, name string) ([]byte, error) {
	if !ValidShot(name) {
		return nil, ErrNoShot
	}
	l, err := s.lesson(ctx, botID, lessonID)
	if err != nil {
		return nil, err
	}
	if !l.Open() {
		return nil, ErrNoShot
	}
	s.mu.Lock()
	var comp Computer
	if ss := s.live[botID]; ss != nil && ss.lessonID == lessonID {
		comp = ss.comp
	}
	s.mu.Unlock()
	if comp == nil {
		if comp, err = s.o.Open(ctx, botID, false); err != nil {
			return nil, err
		}
	}
	b, err := comp.ReadFile(ctx, dirOf(lessonID)+"/"+name)
	if errors.Is(err, boxfs.ErrNotExist) {
		return nil, ErrNoShot
	}
	if err != nil {
		return nil, err
	}
	if len(b) > limits.TeachShotBytes {
		return nil, errors.New("截圖太大")
	}
	return b, nil
}

// Recover 在服務啟動時收掉上次沒結束的示範（錄製器的監督者不在了）：標成已捨棄，並在背景請還開著的電腦停掉錄製器。
func (s *Service) Recover(ctx context.Context) error {
	open, err := s.o.DB.ListLessons(ctx, "", store.LessonRecording, store.LessonPaused)
	if err != nil {
		return err
	}
	for _, l := range open {
		l.State, l.EndedAt = store.LessonDiscarded, store.NowMS()
		if _, err := s.o.DB.UpdateLesson(ctx, l); err != nil {
			return err
		}
		go func(l store.Lesson) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			c, err := s.o.Open(ctx, l.BotID, false)
			if err != nil {
				return
			}
			_ = c.Run(ctx, "touch", dirOf(l.ID)+"/.stop")
			_ = c.Run(ctx, "pkill", "-f", "box-teach-.*"+l.ID)
		}(l)
	}
	return nil
}

// Forget 在 Bot 被刪除時停掉它的示範（資料列隨 Bot 刪除）。
func (s *Service) Forget(botID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.live[botID]; ss != nil {
		ss.cancel()
	}
	delete(s.live, botID)
}
