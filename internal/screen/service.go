// Package screen 是看 Bot 的畫面與接手（B14，PRD BX-7、BX-8）：
//
//   - noVNC 只經過 XBot 服務：GET /api/v1/computers/vnc?bot=<id>（WebSocket）。預設只能看（服務這端丟掉鍵盤滑鼠，rfb.go），
//     只有正在接手的人能用 mode=control。Bot 電腦的 noVNC 只發布在主機的 127.0.0.1，遠端主機經 SSH 轉發（hosts.Host.Addr）。
//   - 接手（xbot.computers.takeover）：Bot 暫停（runtime.Pause）；交還（xbot.computers.handback）帶說明，Bot 在同一條對話串接著做。
//   - Bot 自己可以請你接手（xbot.computers.request_takeover，登入、驗證碼）：待我處理出現 kind=takeover 的卡。
//   - 有人在看畫面時記錄電腦正在使用（box.Manager.Touch），閒置休眠（B13）不會在看的時候關機。
//   - 辦公室的縮圖：xbot.computers.snapshot 在 Bot 電腦裡擷取一張小 JPEG（ImageMagick import）。
package screen

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"xbot/internal/box"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// KindScreen 是電腦畫面狀態改變的事件（接手、交還、Bot 請你接手、有人開始或停止看）。payload：Event。
const KindScreen = "computer.screen"

// InboxKind 是 Bot 請你接手的卡片種類（PRD IN、notify 的 decide 分類）。
const InboxKind = "takeover"

// 卡片的 resolution。
const (
	ResolutionTakenOver  = "taken_over"  // 你接手了（卡片仍開著，直到交還）
	ResolutionHandedBack = "handed_back" // 你交還了
)

// DefaultNote 是交還時沒寫說明的預設內容（E02 規格卡）。
const DefaultNote = "我剛剛在電腦上操作過，請從目前畫面繼續。"

// Boxes 是 Bot 電腦的狀態、「剛被使用」與「有人在用，不要休眠」（B13）；*box.Manager 符合。
type Boxes interface {
	Status(ctx context.Context, botID string) (box.Status, error)
	Touch(ctx context.Context, botID string) error
	Hold(botID string) (release func())
}

// Runtime 是暫停與接著做；*runtime.Runtime 符合。
type Runtime interface {
	Pause(ctx context.Context, botID string) (runtime.Paused, error)
	Resume(ctx context.Context, botID string, req runtime.ResumeRequest) (string, error)
	Active(botID string) (threadID, taskID string)
	Note(ctx context.Context, botID, threadID, activity, text string) error
}

// Options 是 Service 的設定。
type Options struct {
	DB      *store.DB
	Boxes   Boxes
	Hosts   box.Hosts
	Runtime Runtime
	Bus     *events.Bus
	Logger  *slog.Logger
	// Snapshot 擷取一張畫面（JPEG）；nil 用 docker exec import（測試換成假的）。
	Snapshot func(ctx context.Context, api hosts.DockerAPI, containerID string) ([]byte, error)
	// CloseTabs 關掉瀏覽器的分頁、只留一個空白分頁，回傳關了幾個；nil 用 docker exec 經 Chromium 的 DevTools（測試換成假的）。
	CloseTabs func(ctx context.Context, api hosts.DockerAPI, containerID string) (int, error)
	// TakeoverIdle、TouchInterval：0 用 limits 的值（測試調短）。
	TakeoverIdle, TouchInterval time.Duration
	// Now：0 值用 time.Now（測試用）。
	Now func() time.Time
}

// Service 管理每個 Bot 的畫面：誰在看、誰在接手。接手狀態只在記憶體（服務重開後 Bot 的工作也已經結束，見 runtime.RecoverTasks）。
type Service struct {
	o   Options
	log *slog.Logger

	mu    sync.Mutex
	bots  map[string]*botState
	snaps map[string]snapshot
}

type botState struct {
	takeover *Takeover
	viewers  int
	controls map[*vncConn]struct{} // 接手中的 control 連線：交還時關掉
	idleFrom time.Time             // 接手中、最後一個 control 連線離開的時間（沒有連線時）
	release  func()                // 接手期間電腦不休眠（box.Manager.Hold）
}

// Takeover 是正在進行的接手。
type Takeover struct {
	By     string `json:"by" desc:"誰在操作：owner 或 agent:<權杖 id>"`
	ByName string `json:"by_name" desc:"顯示名稱"`
	Since  int64  `json:"since"`
	// ThreadID、TaskID 是被暫停的那一輪（Bot 本來沒在做事時空白）。
	ThreadID string `json:"thread_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
}

// Request 是 Bot 請你接手（待我處理裡開著的 takeover 卡）。
type Request struct {
	InboxID  string `json:"inbox_id"`
	Reason   string `json:"reason" desc:"Bot 寫的原因，例：需要輸入簡訊驗證碼"`
	ThreadID string `json:"thread_id,omitempty"`
	At       int64  `json:"at"`
}

// New 建立 Service。
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.CloseTabs == nil {
		o.CloseTabs = dockerCloseTabs
	}
	if o.Snapshot == nil {
		o.Snapshot = dockerSnapshot
	}
	if o.TakeoverIdle <= 0 {
		o.TakeoverIdle = limits.TakeoverIdle
	}
	if o.TouchInterval <= 0 {
		o.TouchInterval = limits.ViewerTouchInterval
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, log: o.Logger, bots: map[string]*botState{}, snaps: map[string]snapshot{}}
}

func (s *Service) state(botID string) *botState {
	st := s.bots[botID]
	if st == nil {
		st = &botState{controls: map[*vncConn]struct{}{}}
		s.bots[botID] = st
	}
	return st
}

// Errors（操作層轉成 ops 錯誤）。
var (
	ErrTakenByOther = errors.New("已經有人在操作這台電腦")
	ErrNotTakenOver = errors.New("這台電腦沒有在接手中")
	ErrNotHolder    = errors.New("只有正在操作的人能交還")
)

// TakeoverOf 回傳正在進行的接手（沒有時 nil）。
func (s *Service) TakeoverOf(botID string) *Takeover {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.bots[botID]; st != nil && st.takeover != nil {
		t := *st.takeover
		return &t
	}
	return nil
}

// Viewers 回傳正在看這台電腦畫面的連線數。
func (s *Service) Viewers(botID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.bots[botID]; st != nil {
		return st.viewers
	}
	return 0
}

// Takeover 接手：Bot 暫停（正在做的那一輪中斷，排隊的先不開始），之後 by 可以用 mode=control 操作畫面。
// 同一個人重複接手直接回傳原本的狀態；別人正在操作時回 ErrTakenByOther。
func (s *Service) Takeover(ctx context.Context, botID, by, byName string) (Takeover, error) {
	bot, err := s.o.DB.GetBot(ctx, botID)
	if err != nil {
		return Takeover{}, err
	}
	s.mu.Lock()
	st := s.state(botID)
	if cur := st.takeover; cur != nil {
		s.mu.Unlock()
		if cur.By != by {
			return *cur, ErrTakenByOther
		}
		return *cur, nil
	}
	tk := &Takeover{By: by, ByName: byName, Since: s.o.Now().UnixMilli()}
	st.takeover, st.idleFrom = tk, s.o.Now()
	st.release = s.o.Boxes.Hold(botID)
	s.mu.Unlock()

	ctx = context.WithoutCancel(ctx)
	p, err := s.o.Runtime.Pause(ctx, botID)
	if err != nil {
		s.mu.Lock()
		st.takeover = nil
		st.release()
		st.release = nil
		s.mu.Unlock()
		return Takeover{}, err
	}
	s.mu.Lock()
	tk.ThreadID, tk.TaskID = p.ThreadID, p.TaskID
	out := *tk
	s.mu.Unlock()

	thread := p.ThreadID
	reqs, _ := s.requests(ctx, botID)
	for _, r := range reqs {
		if thread == "" {
			thread = r.ThreadID
		}
		s.markTakenOver(ctx, r.InboxID)
	}
	if thread != "" {
		if err := s.o.Runtime.Note(ctx, botID, thread, runtime.ActivityTakeover, "你接手了電腦，"+bot.Name+"先暫停"); err != nil {
			s.log.Warn("記錄接手失敗", "bot", botID, "err", err)
		}
	}
	if err := s.o.Boxes.Touch(ctx, botID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("記錄電腦使用時間失敗", "bot", botID, "err", err)
	}
	s.publish(botID, true)
	return out, nil
}

// HandbackResult 是交還的結果。
type HandbackResult struct {
	Note string `json:"note" desc:"送給 Bot 的說明"`
	// ResumedTaskID 是 Bot 接著做的那件工作（Bot 本來沒在做事、也沒有請你接手時空白）。
	ResumedTaskID string `json:"resumed_task_id,omitempty"`
	ThreadID      string `json:"thread_id,omitempty"`
}

// Handback 交還：關掉接手的畫面連線，Bot 帶著說明在同一條對話串接著做（沒寫說明用 DefaultNote）。
// by 是 owner 時一律可以交還；其他身分只能交還自己接手的。
func (s *Service) Handback(ctx context.Context, botID, by, note string) (HandbackResult, error) {
	bot, err := s.o.DB.GetBot(ctx, botID)
	if err != nil {
		return HandbackResult{}, err
	}
	s.mu.Lock()
	st := s.state(botID)
	tk := st.takeover
	if tk == nil {
		s.mu.Unlock()
		return HandbackResult{}, ErrNotTakenOver
	}
	if by != tk.By && by != "owner" && by != "system" {
		s.mu.Unlock()
		return HandbackResult{}, ErrNotHolder
	}
	st.takeover = nil
	if st.release != nil {
		st.release()
		st.release = nil
	}
	for c := range st.controls {
		c.close("已交還給 Bot")
	}
	s.mu.Unlock()

	ctx = context.WithoutCancel(ctx)
	note = strings.TrimSpace(note)
	if note == "" {
		note = DefaultNote
	}
	thread := tk.ThreadID
	reqs, _ := s.requests(ctx, botID)
	if thread == "" && len(reqs) > 0 {
		thread = reqs[0].ThreadID
	}
	text := "已交還給" + bot.Name + "：" + note
	prompt := "使用者已接手你的電腦並完成：" + note + "\n請重新看一下目前的畫面，從那裡接著做原本的工作；使用者做過的步驟不要重做。"
	id, err := s.o.Runtime.Resume(ctx, botID, runtime.ResumeRequest{ThreadID: thread, Text: text, Prompt: prompt})
	if err != nil && !errors.Is(err, runtime.ErrNotPaused) {
		s.log.Warn("交還後接著做失敗", "bot", botID, "err", err)
	}
	for _, r := range reqs {
		s.settle(ctx, r.InboxID, ResolutionHandedBack, by)
	}
	s.publish(botID, true)
	return HandbackResult{Note: note, ResumedTaskID: id, ThreadID: thread}, nil
}

// RequestResult 是 Bot 請你接手的結果。
type RequestResult struct {
	InboxID        string `json:"inbox_id"`
	AlreadyPending bool   `json:"already_pending,omitempty" desc:"已經請過了，還在等使用者"`
}

// RequestTakeover 開一張「需要你接手電腦」的卡（Bot 遇到登入、驗證碼等要真人的步驟）。已經有一張開著的就不再開。
func (s *Service) RequestTakeover(ctx context.Context, botID, reason string) (RequestResult, error) {
	bot, err := s.o.DB.GetBot(ctx, botID)
	if err != nil {
		return RequestResult{}, err
	}
	if open, err := s.requests(ctx, botID); err != nil {
		return RequestResult{}, err
	} else if len(open) > 0 {
		return RequestResult{InboxID: open[0].InboxID, AlreadyPending: true}, nil
	}
	thread, task := s.o.Runtime.Active(botID)
	reason = strings.TrimSpace(reason)
	payload, _ := json.Marshal(map[string]any{"bot_name": bot.Name, "reason": reason})
	it, err := s.o.DB.CreateInboxItem(ctx, store.InboxItem{Kind: InboxKind, BotID: botID, ThreadID: thread, TaskID: task,
		Ref: "takeover:" + botID, Title: bot.Name + "需要你接手電腦", Body: reason, Payload: payload})
	if err != nil {
		return RequestResult{}, err
	}
	s.emitStored(notify.Event(notify.KindCreated, it))
	s.publish(botID, true)
	return RequestResult{InboxID: it.ID}, nil
}

// requests 讀 Bot 開著的 takeover 卡（新的在前）。
func (s *Service) requests(ctx context.Context, botID string) ([]Request, error) {
	items, err := s.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{InboxKind}, BotID: botID})
	if err != nil {
		return nil, err
	}
	out := make([]Request, 0, len(items))
	for _, it := range items {
		out = append(out, Request{InboxID: it.ID, Reason: it.Body, ThreadID: it.ThreadID, At: it.CreatedAt})
	}
	return out, nil
}

func (s *Service) markTakenOver(ctx context.Context, inboxID string) {
	it, err := s.o.DB.GetInboxItem(ctx, inboxID)
	if err != nil || it.ReadAt != 0 {
		return
	}
	if _, err := s.o.DB.MarkInboxRead(ctx, []string{inboxID}, ""); err == nil {
		if cur, err := s.o.DB.GetInboxItem(ctx, inboxID); err == nil {
			s.emitStored(notify.Event(notify.KindUpdated, cur))
		}
	}
}

func (s *Service) settle(ctx context.Context, inboxID, resolution, by string) {
	if err := s.o.DB.SettleInboxItem(ctx, inboxID, store.InboxResolved, resolution, by, nil); err != nil {
		s.log.Warn("收掉接手卡失敗", "inbox", inboxID, "err", err)
		return
	}
	if cur, err := s.o.DB.GetInboxItem(ctx, inboxID); err == nil {
		s.emitStored(notify.Event(notify.KindUpdated, cur))
	}
}

func (s *Service) emitStored(e store.Event) {
	row, err := s.o.DB.AppendEvent(context.Background(), e)
	if err != nil {
		s.log.Warn("寫入事件失敗", "kind", e.Kind, "err", err)
		return
	}
	if s.o.Bus != nil {
		s.o.Bus.Publish(events.FromStore(row))
	}
}

// Event 是 computer.screen 事件的內容。
type Event struct {
	BotID    string    `json:"bot_id"`
	Takeover *Takeover `json:"takeover"`
	Viewers  int       `json:"viewers"`
}

// publish 發 computer.screen；persist 時寫進 event 表（接手、交還），看的人數變化只即時發布。
func (s *Service) publish(botID string, persist bool) {
	if s.o.Bus == nil {
		return
	}
	raw, _ := json.Marshal(Event{BotID: botID, Takeover: s.TakeoverOf(botID), Viewers: s.Viewers(botID)})
	e := events.Event{Kind: KindScreen, BotID: botID, Payload: raw, At: s.o.Now().UnixMilli()}
	if persist {
		row, err := s.o.DB.AppendEvent(context.Background(), store.Event{BotID: botID, Kind: KindScreen, Payload: raw})
		if err == nil {
			e.ID, e.At = row.ID, row.CreatedAt
		}
	}
	s.o.Bus.Publish(e)
}

// Run 每分鐘檢查一次：接手中、但沒有人連著畫面超過 TakeoverIdle 的，自動交還（帶預設說明），Bot 不會一直等。
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(min(time.Minute, s.o.TakeoverIdle/2))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.expire(ctx)
		}
	}
}

func (s *Service) expire(ctx context.Context) {
	now := s.o.Now()
	var idle []string
	s.mu.Lock()
	for id, st := range s.bots {
		if st.takeover != nil && len(st.controls) == 0 && now.Sub(st.idleFrom) >= s.o.TakeoverIdle {
			idle = append(idle, id)
		}
	}
	s.mu.Unlock()
	for _, id := range idle {
		s.log.Info("接手後太久沒有人操作，自動交還", "bot", id)
		if _, err := s.Handback(ctx, id, "system", ""); err != nil && !errors.Is(err, ErrNotTakenOver) {
			s.log.Warn("自動交還失敗", "bot", id, "err", err)
		}
	}
}

// Forget 在 Bot 被刪除時清掉它的狀態。
func (s *Service) Forget(botID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.bots[botID]; st != nil {
		for c := range st.controls {
			c.close("Bot 已刪除")
		}
		if st.release != nil {
			st.release()
		}
	}
	delete(s.bots, botID)
	delete(s.snaps, botID)
}
