package org

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // 台北時間（提案與討論的時間）不依賴系統的時區資料

	"xbot/internal/bots"
	"xbot/internal/collab"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// Runtime 是組織用到的執行層；*runtime.Runtime 符合。
type Runtime interface {
	Start(ctx context.Context, req runtime.ChatRequest) (runtime.ChatResult, error)
	Active(botID string) (threadID, taskID string)
	Bus() *events.Bus
}

// Options 是組織的設定。
type Options struct {
	DB      *store.DB
	Runtime Runtime
	// Collab 用來在建團隊時開群組；nil 時不開。
	Collab *collab.Service
	Logger *slog.Logger
	// Now 是時間（測試可以換）；nil 用 time.Now。
	Now func() time.Time
	// ManageChanged 在建團隊順便打開主管的團隊管理權限以後呼叫（重新載入它的 XBot MCP 工具）。可為 nil。
	ManageChanged func(ctx context.Context, botID string)
}

// Service 是團隊、匯報線、提案與討論。
type Service struct {
	o    Options
	db   *store.DB
	rt   Runtime
	log  *slog.Logger
	zone *time.Location

	tickMu sync.Mutex
}

// New 建立組織服務。
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	zone, err := time.LoadLocation(limits.AgendaZone)
	if err != nil {
		zone = time.FixedZone("CST", 8*3600)
	}
	return &Service{o: o, db: o.DB, rt: o.Runtime, log: o.Logger, zone: zone}
}

func (s *Service) now() time.Time { return s.o.Now() }

// Zone 是提案與討論用的時區（台北）。
func (s *Service) Zone() *time.Location { return s.zone }

// ── 錯誤 ─────────────────────────────────────────────

func bad(msg string) error          { return ops.InvalidInput(ops.FieldError{Message: msg}) }
func badf(f string, a ...any) error { return bad(fmt.Sprintf(f, a...)) }

// errChiefNoTeam：大總管不加入任何團隊，也不當團隊主管（2026-10-21 使用者：「每次請他建立團隊，他就變成那個團隊的主管，這樣不對」）。
// 團隊要向它回報時，主管的報告對象填它就好。
func errChiefNoTeam(name string) error {
	return badf("%s 是大總管，不加入團隊、也不當團隊主管：請另外指定或新建一位主管；要團隊向 %s 回報，把 reports_to 填 %s", name, name, name)
}

// ── 事件 ─────────────────────────────────────────────

// 事件種類。
const (
	KindTeamUpdated   = "team.updated"   // payload：{team_id}；團隊、成員、報告線有變（側欄、團隊地圖重讀）
	KindAgendaUpdated = "agenda.updated" // payload：{agenda_id, state}
)

func (s *Service) publish(ev store.Event) {
	if s.rt != nil && s.rt.Bus() != nil {
		s.rt.Bus().Publish(events.FromStore(ev))
	}
}

func (s *Service) emit(botID, kind string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev, err := s.db.AppendEvent(context.Background(), store.Event{BotID: botID, Kind: kind, Payload: raw})
	if err != nil {
		s.log.Warn("寫入組織事件失敗", "kind", kind, "err", err)
		return
	}
	s.publish(ev)
}

// note 在 Bot 正在進行的對話加一行事件（「送出提案…」「建立了團隊…」；LazyBoy collab.note）。
func (s *Service) note(ctx context.Context, botID, event, text string, extra map[string]any) {
	if s.rt == nil {
		return
	}
	thread, task := s.rt.Active(botID)
	if thread == "" {
		return
	}
	peer := map[string]any{"event": event}
	for k, v := range extra {
		peer[k] = v
	}
	meta, _ := json.Marshal(map[string]any{"peer": peer, "task_id": task})
	m, err := s.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: thread, Sender: "bot:" + botID, Kind: "peer", Content: text, Meta: meta})
	if err != nil {
		s.log.Warn("寫入組織事件行失敗", "bot", botID, "err", err)
		return
	}
	raw, _ := json.Marshal(m)
	ev, err := s.db.AppendEvent(ctx, store.Event{BotID: botID, ThreadID: thread, TaskID: task, Kind: runtime.KindMessageCreated, Payload: raw})
	if err == nil {
		s.publish(ev)
	}
}

// ── 匯報線（TM-2）─────────────────────────────────────

// roster 是所有 Bot（含封存的；匯報線檢查要看全部），以 id 查。
type roster struct {
	list []store.Bot
	byID map[string]store.Bot
}

func (s *Service) roster(ctx context.Context) (roster, error) {
	list, err := s.db.ListBots(ctx, true)
	if err != nil {
		return roster{}, err
	}
	r := roster{list: list, byID: map[string]store.Bot{}}
	for _, b := range list {
		r.byID[b.ID] = b
	}
	return r, nil
}

// reports 是 id 底下的 Bot：直屬，或 deep 時所有下屬（LazyBoy reports）。
func (r roster) reports(id string, deep bool) []store.Bot {
	var found []store.Bot
	seen := map[string]bool{id: true}
	frontier := map[string]bool{id: true}
	for len(frontier) > 0 {
		next := map[string]bool{}
		for _, b := range r.list {
			if frontier[b.ReportsTo] && !seen[b.ID] {
				seen[b.ID] = true
				found = append(found, b)
				next[b.ID] = true
			}
		}
		if !deep {
			break
		}
		frontier = next
	}
	return found
}

// find 用 id 或名字找 Bot：id、名字完全相同、不分大小寫，依序找唯一的一位。
func (r roster) find(ref string) (store.Bot, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
	lower := strings.ToLower(ref)
	for _, test := range []func(store.Bot) bool{
		func(b store.Bot) bool { return b.ID == ref },
		func(b store.Bot) bool { return b.Name == ref },
		func(b store.Bot) bool { return strings.ToLower(b.Name) == lower },
	} {
		var hits []store.Bot
		for _, b := range r.list {
			if test(b) {
				hits = append(hits, b)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
	}
	return store.Bot{}, ops.NotFound(fmt.Sprintf("找不到 Bot「%s」", ref))
}

// userWords 是「直接向使用者報告」的寫法（LazyBoy update_bot）。
var userWords = map[string]bool{"": true, "使用者": true, "user": true, "董事長": true, "你": true, "me": true, "owner": true}

// checkManager 回傳 botID 的主管（空白＝使用者）。不認得的 Bot、向自己報告、繞成一圈都拒絕（LazyBoy check_manager）。
func (r roster) checkManager(botID, managerRef string) (string, error) {
	if userWords[strings.TrimSpace(managerRef)] {
		return "", nil
	}
	m, err := r.find(managerRef)
	if err != nil {
		return "", ops.NotFound("找不到要報告的對象")
	}
	if m.ID == botID {
		return "", bad("Bot 不能向自己報告")
	}
	for _, b := range r.reports(botID, true) {
		if b.ID == m.ID {
			return "", badf("%s 在 %s 底下，不能反過來當它的主管", m.Name, r.byID[botID].Name)
		}
	}
	return m.ID, nil
}

// CheckManager 回傳 botID 要報告的對象（managerRef 是 id 或名字；空白或「使用者」表示直接向你報告）。
func (s *Service) CheckManager(ctx context.Context, botID, managerRef string) (string, error) {
	r, err := s.roster(ctx)
	if err != nil {
		return "", err
	}
	if _, ok := r.byID[botID]; !ok {
		return "", ops.NotFound("找不到這個 Bot")
	}
	return r.checkManager(botID, managerRef)
}

// Reports 是 botID 底下的 Bot（直屬，或 deep 時所有下屬）。
func (s *Service) Reports(ctx context.Context, botID string, deep bool) ([]store.Bot, error) {
	r, err := s.roster(ctx)
	if err != nil {
		return nil, err
	}
	return r.reports(botID, deep), nil
}

// SetReportsTo 改 Bot 的報告對象（拒絕循環）。
func (s *Service) SetReportsTo(ctx context.Context, botID, managerRef, by string) (store.Bot, error) {
	r, err := s.roster(ctx)
	if err != nil {
		return store.Bot{}, err
	}
	b, ok := r.byID[botID]
	if !ok {
		return b, ops.NotFound("找不到這個 Bot")
	}
	boss, err := r.checkManager(botID, managerRef)
	if err != nil {
		return b, err
	}
	if err := s.db.SetBotOrg(ctx, botID, b.TeamID, boss, by); err != nil {
		return b, err
	}
	s.emit("", KindTeamUpdated, map[string]string{"team_id": b.TeamID, "bot_id": botID})
	return s.db.GetBot(ctx, botID)
}

// ── 文字整理 ──────────────────────────────────────────

// frame 是隊友傳話、群組座位加上的框架（看 Bot 在做什麼時拿掉，LazyBoy FRAME）。
var frame = regexp.MustCompile(`^\[來自隊友 [^\]]*\]\n|<teammate-message[^>]*>\n|\n</teammate-message>|\n\n（這則訊息來自另一個 Bot[\s\S]*$|^\[群組[^\]]*\]\n`)

// taskText 是一件工作要做的事，去掉隊友傳話的框架（LazyBoy task_text）。
func taskText(t store.Task) string {
	return collab.Plain(frame.ReplaceAllString(t.Input, ""), 200)
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

// newBot 存一個新 Bot（建團隊時用；顏色形狀依現有數量輪流挑）。
func newBot(ctx context.Context, q *store.Queries, sp bots.Spec, existing int, actor string) (store.Bot, error) {
	return q.CreateBot(ctx, bots.Build(sp, existing, actor))
}
