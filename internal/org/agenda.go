package org

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"xbot/internal/collab"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// 提案與討論（TM-5、H04；LazyBoy agenda.py）。Bot 讓你參與的三種方式，由輕到重：
//   - propose：「我打算做 X，<日期> 前你沒意見我就做」。沒意見就是同意；你可以現在就同意、否決或什麼都不做。
//   - discuss：「這件事想聽你的想法」附背景、選項與建議時間。你可以保留或改時間、直接用文字回覆、或說不用討論。
//     時間到時叫醒 Bot 把事情整理好、問你。
//   - xbot.approvals.request（C11）：只有你能決定，你決定前什麼都不會發生。

// 種類。
const (
	KindPropose = "propose"
	KindDiscuss = "discuss"
)

// DefaultApprovalItems 是預設「只有你能決定的事」（LazyBoy org.py DEFAULT_APPROVALS）。
var DefaultApprovalItems = []string{"新商品定價", "新增支出", "簽約", "上架", "重要對客承諾"}

// onlyUser 是代表金錢、簽約、上架、對客承諾的字眼：沒有回覆不能視同同意（LazyBoy ONLY_USER）。
var onlyUser = regexp.MustCompile(`(?i)花錢|花費|預算|支出|付款|付費|購買|訂閱|刷卡|NT\$|\$\d|簽約|合約|定價|訂價|調價|上架|下架|對客|承諾|賠償|退款`)

// pending 是還算「等你的事」的狀態。
var pending = []string{store.AgendaOpen, store.AgendaHeld}

const approvalItemsKey = "org.approval_items"

// ApprovalItems 是只有你能決定的事（F01；可編輯，最多 20 項、每項 40 字）。
func (s *Service) ApprovalItems(ctx context.Context) ([]string, error) {
	var items []string
	ok, err := s.db.GetSettingInto(ctx, approvalItemsKey, &items)
	if err != nil {
		return nil, err
	}
	if !ok || items == nil {
		return append([]string{}, DefaultApprovalItems...), nil
	}
	return items, nil
}

// SetApprovalItems 存「只有你能決定的事」：去掉空白與重複，每項 40 字、最多 20 項（LazyBoy set_approval_items）。
func (s *Service) SetApprovalItems(ctx context.Context, items []string) ([]string, error) {
	clean := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		it = collab.Plain(it, limits.ApprovalItemMax)
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		clean = append(clean, it)
		if len(clean) == limits.ApprovalItemsMax {
			break
		}
	}
	return clean, s.db.SetSetting(ctx, approvalItemsKey, clean)
}

// ParseWhen 讀 Bot 寫的時間：ISO 8601，或台北時間的「YYYY-MM-DD HH:MM」。空白回傳零值（LazyBoy parse_when）。
func (s *Service) ParseWhen(text string) (time.Time, error) {
	text = strings.ReplaceAll(strings.TrimSpace(text), "/", "-")
	if text == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC(), nil
		}
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, text, s.zone); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, bad("時間格式要像 2026-10-20 15:00")
}

// DefaultDiscussionTime 是至少一天後的平日晚上 6 點（台北）：來得及準備，也不會太晚（LazyBoy default_discussion_time）。
func (s *Service) DefaultDiscussionTime(after time.Time) time.Time {
	local := after.In(s.zone).AddDate(0, 0, 1)
	c := time.Date(local.Year(), local.Month(), local.Day(), limits.DiscussDefaultHour, 0, 0, 0, s.zone)
	for c.Weekday() == time.Saturday || c.Weekday() == time.Sunday {
		c = c.AddDate(0, 0, 1)
	}
	return c.UTC()
}

func (s *Service) checkDiscussTime(t time.Time) error {
	now := s.now()
	if !t.After(now.Add(limits.DiscussLeadMin)) {
		return badf("討論時間至少要在 %d 分鐘之後", int(limits.DiscussLeadMin.Minutes()))
	}
	if t.After(now.Add(limits.DiscussAheadMax)) {
		return badf("討論時間最多排到 %d 天後", int(limits.DiscussAheadMax.Hours()/24))
	}
	return nil
}

// SubmitIn 是 Bot 送出的一件提案或討論。
type SubmitIn struct {
	Kind    string
	Subject string
	Details string
	Days    int    // 提案：幾天後生效（1–14，預設 3）
	When    string // 討論：建議時間；空白用隔天（平日）晚上 6 點
}

// SubmitOut 是送出的結果。
type SubmitOut struct {
	Sent     bool   `json:"sent" desc:"false：同一件事已經在等使用者了，沒有重送"`
	AgendaID string `json:"agenda_id,omitempty"`
	DueAt    int64  `json:"due_at,omitempty" desc:"提案生效或討論的時間（unix 毫秒）"`
	Note     string `json:"note"`
}

// Submit 收下一件提案或討論：放上你的行事曆和「待我處理」（LazyBoy submit）。
func (s *Service) Submit(ctx context.Context, botID string, in SubmitIn) (SubmitOut, error) {
	b, err := s.db.GetBot(ctx, botID)
	if isNotFound(err) {
		return SubmitOut{}, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return SubmitOut{}, err
	}
	if in.Kind != KindPropose && in.Kind != KindDiscuss {
		return SubmitOut{}, bad("kind 只能是 propose 或 discuss")
	}
	subject := collab.Plain(in.Subject, limits.AgendaSubjectMax)
	if subject == "" {
		return SubmitOut{}, bad("請寫出要提案或討論的事")
	}
	mine, err := s.db.ListAgenda(ctx, store.AgendaFilter{States: pending, BotID: botID})
	if err != nil {
		return SubmitOut{}, err
	}
	for _, a := range mine {
		if a.Subject == subject {
			return SubmitOut{Sent: false, AgendaID: a.ID, DueAt: a.DueAt, Note: "這件事已經在行事曆上等使用者了，不需要重送。"}, nil
		}
	}
	if len(mine) >= limits.AgendaOpenMax {
		return SubmitOut{}, ops.Limit(fmt.Sprintf("你已經有 %d 件事在等使用者；先把相關的事合併成一件，或等使用者處理", limits.AgendaOpenMax))
	}
	now := s.now()
	details := strings.TrimSpace(in.Details)
	if r := []rune(details); len(r) > limits.AgendaDetailsMax {
		details = string(r[:limits.AgendaDetailsMax])
	}
	var due time.Time
	days := in.Days
	if in.Kind == KindPropose {
		// 沒有回覆絕不能當成同意花錢、簽約、上架或對客承諾：這些要明確同意。
		text := subject + " " + details
		items, err := s.ApprovalItems(ctx)
		if err != nil {
			return SubmitOut{}, err
		}
		hit := ""
		for _, it := range items {
			if it != "" && strings.Contains(text, it) {
				hit = it
				break
			}
		}
		if hit == "" {
			hit = onlyUser.FindString(text)
		}
		if hit != "" {
			return SubmitOut{}, badf("這件事涉及「%s」，只有使用者能決定，沒有回覆不能視同同意；請改用 xbot.approvals.request 送給使用者核准。", hit)
		}
		vetoed, err := s.db.LastVetoed(ctx, botID, subject)
		if err != nil {
			return SubmitOut{}, err
		}
		if vetoed > now.Add(-limits.VetoMemory).UnixMilli() {
			return SubmitOut{}, bad("使用者前不久否決過同一件事；改過做法再提案，或換個說法說明哪裡不同。")
		}
		if days == 0 {
			days = limits.ProposalDaysDefault
		}
		if days < limits.ProposalDaysMin || days > limits.ProposalDaysMax {
			return SubmitOut{}, badf("生效天數要介於 %d 到 %d 天", limits.ProposalDaysMin, limits.ProposalDaysMax)
		}
		due = now.Add(time.Duration(days) * 24 * time.Hour)
	} else {
		if due, err = s.ParseWhen(in.When); err != nil {
			return SubmitOut{}, err
		}
		if due.IsZero() {
			due = s.DefaultDiscussionTime(now)
		}
		if err := s.checkDiscussTime(due); err != nil {
			return SubmitOut{}, err
		}
	}
	row := store.Agenda{BotID: botID, Kind: in.Kind, Subject: subject, Details: details, DueAt: due.UnixMilli()}
	if s.rt != nil {
		if _, taskID := s.rt.Active(botID); taskID != "" {
			if t, err := s.db.GetTask(ctx, taskID); err == nil {
				row.TaskID = t.ID
				// 群組座位的一輪在隱藏的對話串裡跑：你的回覆改送到 Bot 自己的新對話。
				if t.Source != runtime.SourceRoom {
					row.ThreadID = t.ThreadID
				}
				var m struct {
					FromBot  string `json:"from_bot"`
					FromName string `json:"from_name"`
				}
				_ = json.Unmarshal(t.Meta, &m)
				row.FromBot, row.FromName = m.FromBot, m.FromName
			}
		}
	}
	local := due.In(s.zone).Format("01/02 15:04")
	kindLabel := map[string]string{KindPropose: "提案", KindDiscuss: "想找你討論"}[in.Kind]
	var created store.InboxItem
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		if row, err = q.CreateAgenda(ctx, row); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"agenda_id": row.ID, "kind": row.Kind, "due_at": row.DueAt, "subject": subject})
		inboxKind := map[string]string{KindPropose: "proposal", KindDiscuss: "discussion"}[in.Kind]
		created, err = q.CreateInboxItem(ctx, store.InboxItem{Kind: inboxKind, BotID: botID, TaskID: row.TaskID, ThreadID: row.ThreadID, Ref: row.ID,
			Title: fmt.Sprintf("%s %s：%s", b.Name, kindLabel, subject), Body: details, Payload: payload})
		return err
	})
	if err != nil {
		return SubmitOut{}, err
	}
	s.inboxEvent(ctx, notify.KindCreated, created)
	s.emit(botID, KindAgendaUpdated, map[string]string{"agenda_id": row.ID, "state": row.State})
	event := map[string]string{KindPropose: "proposal", KindDiscuss: "discussion"}[in.Kind]
	s.note(ctx, botID, event, subject, map[string]any{"agenda_id": row.ID})
	out := SubmitOut{Sent: true, AgendaID: row.ID, DueAt: row.DueAt}
	if in.Kind == KindPropose {
		out.Note = fmt.Sprintf("已放上使用者的行事曆，%d 天後（%s）沒有意見就視同同意，到時你會收到通知再執行；在那之前不要做這件事。"+
			"使用者要否決會在「待我處理」或行事曆上按「否決」。回覆裡用一句話說明你打算做什麼和生效日期就好，不要寫編號。", days, local)
	} else {
		out.Note = fmt.Sprintf("已排在使用者的行事曆：%s。使用者可能改時間、直接用文字回覆、或說不用討論；時間到你會收到提醒，到時再把背景、選項和你的建議整理給他。"+
			"現在先回報其他進度，不要等。回覆裡不要寫編號。", local)
	}
	return out, nil
}

func (s *Service) inboxEvent(ctx context.Context, kind string, it store.InboxItem) {
	ev, err := s.db.AppendEvent(ctx, notify.Event(kind, it))
	if err != nil {
		s.log.Warn("寫入待我處理事件失敗", "err", err)
		return
	}
	s.publish(ev)
}

// 你的決定。
const (
	DecideAccept  = "accept"  // 提案：現在就同意；討論：保留時間（或帶 when 改時間）
	DecideVeto    = "veto"    // 提案：否決
	DecideAnswer  = "answer"  // 討論：直接用文字回覆，不用另外討論
	DecideDecline = "decline" // 討論：不用討論
)

// Decide 是你對一件提案或討論的決定；結果以你的訊息送回 Bot 的對話（LazyBoy decide）。
// 送不到 Bot 時恢復原狀並回錯誤，你可以再給一次。
func (s *Service) Decide(ctx context.Context, id, decision, note, when string) (store.Agenda, error) {
	row, err := s.db.GetAgenda(ctx, id)
	if isNotFound(err) {
		return row, ops.NotFound("找不到這件提案或討論")
	}
	if err != nil {
		return row, err
	}
	if row.State != store.AgendaOpen && row.State != store.AgendaHeld {
		return row, ops.Conflict("這件事已經處理過了")
	}
	note = strings.TrimSpace(note)
	if r := []rune(note); len(r) > limits.AgendaNoteMax {
		note = string(r[:limits.AgendaNoteMax])
	}
	var state, text string
	if row.Kind == KindPropose {
		switch decision {
		case DecideAccept:
			state, text = store.AgendaAccepted, "【董事長回覆】同意："+row.Subject+withLine("備註：", note)+"\n可以開始執行。"
		case DecideVeto:
			state, text = store.AgendaVetoed, "【董事長回覆】否決："+row.Subject+withLine("原因：", note)+"\n不要做這件事；需要的話提出修改後的做法再提案。"
		default:
			return row, bad("提案只能同意或否決")
		}
	} else {
		switch decision {
		case DecideAccept:
			moved, err := s.ParseWhen(when)
			if err != nil {
				return row, err
			}
			if !moved.IsZero() {
				if err := s.checkDiscussTime(moved); err != nil {
					return row, err
				}
				if row.State == store.AgendaHeld {
					// 時間到了已經叫醒過 Bot：改時間就是重新排一次。
					if err := s.db.SettleAgenda(ctx, id, []string{store.AgendaHeld}, store.AgendaOpen, row.Note, 0); err != nil {
						return row, err
					}
				}
				if err := s.db.MoveAgenda(ctx, id, moved.UnixMilli(), false); err != nil {
					return row, err
				}
				s.emit(row.BotID, KindAgendaUpdated, map[string]string{"agenda_id": id, "state": store.AgendaOpen})
			}
			return s.db.GetAgenda(ctx, id)
		case DecideAnswer:
			if note == "" {
				return row, bad("請寫下你的回覆")
			}
			state, text = store.AgendaAnswered, "【董事長回覆】關於「"+row.Subject+"」：\n"+note+"\n不用另外安排討論了，照這個方向做。"
		case DecideDecline:
			state = store.AgendaDeclined
			if note != "" {
				text = "【董事長回覆】「" + row.Subject + "」不需要討論：" + note
			} else {
				text = "【董事長回覆】「" + row.Subject + "」不需要討論，照你的判斷做。"
			}
		default:
			return row, bad("討論只能保留時間、直接回覆或取消")
		}
	}
	if err := s.db.SettleAgenda(ctx, id, pending, state, note, s.now().UnixMilli()); err != nil {
		if errors.Is(err, store.ErrAlreadyDecided) {
			return row, ops.Conflict("這件事已經處理過了")
		}
		return row, err
	}
	if err := s.tell(ctx, row, text, "owner"); err != nil {
		// 沒告訴到 Bot：你的回覆保持未處理，可以再給一次，不會遺失。
		_ = s.db.RestoreAgenda(context.WithoutCancel(ctx), row)
		return row, err
	}
	s.settleCard(ctx, row.ID, state)
	s.emit(row.BotID, KindAgendaUpdated, map[string]string{"agenda_id": id, "state": state})
	return s.db.GetAgenda(ctx, id)
}

func withLine(label, note string) string {
	if note == "" {
		return ""
	}
	return "\n" + label + note
}

// tell 把一段話排進 Bot 的對話，當成自己的一輪（排在 Bot 正在做的事後面，不會被當成提問的答案）。
func (s *Service) tell(ctx context.Context, row store.Agenda, text, sender string) error {
	if s.rt == nil {
		return errors.New("執行層沒有啟動")
	}
	if row.FromName != "" {
		text += "\n這件事原本是 " + row.FromName + " 交辦的，處理完用 xbot.collab.message_bot 告訴它結果。"
	}
	thread := row.ThreadID
	if thread != "" {
		if th, err := s.db.GetThread(ctx, thread); err != nil || th.BotID != row.BotID {
			thread = ""
		}
	}
	title := "提案：" + row.Subject
	if row.Kind == KindDiscuss {
		title = "討論：" + row.Subject
	}
	_, err := s.rt.Start(ctx, runtime.ChatRequest{BotID: row.BotID, ThreadID: thread, Text: text, Source: runtime.SourceOwner, Sender: sender,
		Title: title, Queue: true, TaskMeta: mustJSON(map[string]string{"agenda_id": row.ID})})
	return err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// settleCard 把這件事的「待我處理」卡結掉。
func (s *Service) settleCard(ctx context.Context, agendaID, resolution string) {
	items, err := s.db.ResolveInboxByRef(ctx, agendaID, resolution, "owner")
	if err != nil {
		s.log.Warn("結掉提案卡失敗", "agenda", agendaID, "err", err)
	}
	for _, it := range items {
		s.inboxEvent(ctx, notify.KindUpdated, it)
	}
}

// Tick 處理到期的提案與討論：提案過了生效日沒人反對就是同意；討論時間到叫醒 Bot 整理說明（LazyBoy tick）。
// 送不到 Bot 的那件留著下次再試，並回傳錯誤。
func (s *Service) Tick(ctx context.Context) error {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	rows, err := s.db.ListAgenda(ctx, store.AgendaFilter{States: []string{store.AgendaOpen}})
	if err != nil {
		return err
	}
	var errs []error
	for _, snap := range rows {
		now := s.now()
		// 你可能在處理前一件的時候回覆了這一件：重讀。
		row, err := s.db.GetAgenda(ctx, snap.ID)
		if err != nil || row.State != store.AgendaOpen || row.DueAt > now.UnixMilli() {
			continue
		}
		if row.Kind == KindPropose && !row.Grace && row.DueAt < now.Add(-limits.ProposalGrace).UnixMilli() {
			// 到期時沒有人看得到（服務關著、你不在）：再給一天可以反對。
			if err := s.db.MoveAgenda(ctx, row.ID, now.Add(limits.ProposalGraceExtra).UnixMilli(), true); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		var state, text, sender string
		if row.Kind == KindPropose {
			state, sender = store.AgendaAuto, "system"
			text = "【視同同意】" + row.Subject + "\n你提案時說的生效日期到了，使用者沒有表示意見，視同同意，可以開始執行。" +
				"做完用兩三行回報結果；如果情況和提案時不同，先說明再決定要不要做。"
		} else {
			state, sender = store.AgendaHeld, "system"
			text = "【討論時間到】" + row.Subject + "\n把這件事的背景、選項、你的建議和風險整理成一段（先結論、不超過 10 行），" +
				"向使用者說明，然後問他的想法。他回覆後照他的方向做；他沒空的話這件事就先擱著，不要重複提醒。"
		}
		if err := s.db.SettleAgenda(ctx, row.ID, []string{store.AgendaOpen}, state, row.Note, now.UnixMilli()); err != nil {
			continue // 你剛好處理了
		}
		if err := s.tell(ctx, row, text, sender); err != nil {
			// 沒告訴到 Bot：留著下次再試，不要弄丟。
			_ = s.db.RestoreAgenda(context.WithoutCancel(ctx), row)
			errs = append(errs, err)
			continue
		}
		if state == store.AgendaAuto {
			s.settleCard(ctx, row.ID, state)
		}
		s.emit(row.BotID, KindAgendaUpdated, map[string]string{"agenda_id": row.ID, "state": state})
	}
	return errors.Join(errs...)
}

// Run 每 limits.AgendaTick 跑一次 Tick，直到 ctx 結束。
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(limits.AgendaTick)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil {
			s.log.Warn("處理到期的提案與討論失敗", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ── 行事曆（H04；C32 之後把定時任務併進來）───────────────

// Item 是一件提案或討論。
type Item struct {
	ID        string `json:"id"`
	BotID     string `json:"bot_id"`
	BotName   string `json:"bot_name"`
	Kind      string `json:"kind" desc:"propose（提案）、discuss（討論）"`
	Subject   string `json:"subject"`
	Details   string `json:"details"`
	DueAt     int64  `json:"due_at" desc:"提案生效或討論的時間（unix 毫秒）"`
	State     string `json:"state" desc:"open（等你）、held（討論時間到了，Bot 在對話裡等你）、accepted、vetoed、auto（視同同意）、answered、declined、cancelled"`
	Note      string `json:"note" desc:"你回覆時寫的備註或原因"`
	FromName  string `json:"from_name,omitempty" desc:"這件事原本是誰交辦給 Bot 的"`
	ThreadID  string `json:"thread_id,omitempty"`
	DecidedAt int64  `json:"decided_at,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// ApprovalRef 是一件等你核准的事（行事曆上「待你核定」，沒有時間）。
type ApprovalRef struct {
	ID        string `json:"id"`
	BotID     string `json:"bot_id"`
	BotName   string `json:"bot_name"`
	Subject   string `json:"subject"`
	CreatedAt int64  `json:"created_at"`
}

// CalendarEntry 是行事曆上的一格（給 H04 與 C32 的行事曆用）。
type CalendarEntry struct {
	ID       string `json:"id" desc:"agenda:<id>"`
	At       int64  `json:"at"`
	Kind     string `json:"kind" desc:"propose、discuss（C32 會加上 routine、learning）"`
	Title    string `json:"title"`
	BotID    string `json:"bot_id"`
	BotName  string `json:"bot_name"`
	AgendaID string `json:"agenda_id"`
	State    string `json:"state"`
}

// ListOut 是 xbot.agenda.list 的輸出。
type ListOut struct {
	From      int64           `json:"from"`
	To        int64           `json:"to"`
	Pending   []Item          `json:"pending" desc:"等你的提案與討論（依時間先後）"`
	Decided   []Item          `json:"decided" desc:"最近 7 天處理過的"`
	Calendar  []CalendarEntry `json:"calendar" desc:"接下來幾天有時間的事"`
	Approvals []ApprovalRef   `json:"approvals" desc:"待你核定（不會過期，另外算，每個 Bot 最多 10 件）"`
	OpenMax   int             `json:"open_max" desc:"每個 Bot 提案＋討論合計最多幾件"`
}

// List 是行事曆的資料：等你的提案與討論、最近處理過的、接下來 days 天的行程、待你核定。
func (s *Service) List(ctx context.Context, days int, botID string) (ListOut, error) {
	if days <= 0 {
		days = 7
	}
	now := s.now()
	out := ListOut{From: now.UnixMilli(), To: now.Add(time.Duration(days) * 24 * time.Hour).UnixMilli(), Pending: []Item{}, Decided: []Item{},
		Calendar: []CalendarEntry{}, Approvals: []ApprovalRef{}, OpenMax: limits.AgendaOpenMax}
	r, err := s.roster(ctx)
	if err != nil {
		return out, err
	}
	rows, err := s.db.ListAgenda(ctx, store.AgendaFilter{BotID: botID})
	if err != nil {
		return out, err
	}
	recent := now.Add(-limits.TeamRecentDays * 24 * time.Hour).UnixMilli()
	for _, a := range rows {
		it := Item{ID: a.ID, BotID: a.BotID, BotName: r.byID[a.BotID].Name, Kind: a.Kind, Subject: a.Subject, Details: a.Details, DueAt: a.DueAt,
			State: a.State, Note: a.Note, FromName: a.FromName, ThreadID: a.ThreadID, DecidedAt: a.DecidedAt, CreatedAt: a.CreatedAt}
		switch {
		case a.State == store.AgendaOpen || a.State == store.AgendaHeld:
			out.Pending = append(out.Pending, it)
			if a.DueAt <= out.To && len(out.Calendar) < limits.CalendarWeekItems {
				out.Calendar = append(out.Calendar, CalendarEntry{ID: "agenda:" + a.ID, At: a.DueAt, Kind: a.Kind, Title: a.Subject, BotID: a.BotID,
					BotName: it.BotName, AgendaID: a.ID, State: a.State})
			}
		case a.DecidedAt > recent:
			out.Decided = append(out.Decided, it)
		}
	}
	approvals, err := s.db.ListApprovals(ctx, "pending")
	if err != nil {
		return out, err
	}
	for _, a := range approvals {
		if botID != "" && a.BotID != botID {
			continue
		}
		out.Approvals = append(out.Approvals, ApprovalRef{ID: a.ID, BotID: a.BotID, BotName: r.byID[a.BotID].Name, Subject: subject(a), CreatedAt: a.CreatedAt})
	}
	return out, nil
}
