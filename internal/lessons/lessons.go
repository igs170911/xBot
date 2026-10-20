// Package lessons 是「從失敗學到的事」：harness2 的做法（看行為的差別、提出修改、留下有用的）用在 XBot 上。
//
// 一件工作沒做完（自動繼續也救不回來），或你把「已完成」改正成別的，就請老師模型（預設是 ChatGPT 方案最好的模型，
// 不另外花錢；沒有時用預設模型）看這個 Bot 的步驟和最後的回覆，寫一條下次能避免的守則；
// 如果這次用了一個最近常沒做完的技能，改成提議修那個技能。提議放在「待我處理」，你同意才加進 Bot 的守則
// （SOUL.md 的「學到的事」）或技能，可以改字、可以加給所有 Bot。
//
// 為什麼：2026-10-20 基準測試裡，笨模型做不到的地方（網站被擋就停下來、查資料一直開瀏覽器）都是看了聰明模型的做法
// 寫成一句守則就改善了（deepseek 查資料從 265 秒、66 萬 token 降到 154 秒、23 萬）。這裡讓它自己發生，不用等人發現。
package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// CardKind 是「學到一件事」的待我處理卡。
const CardKind = "lesson"

// Lesson 是一條學到的事。BotID 空白表示給所有 Bot。
type Lesson struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	BotID    string `json:"bot_id,omitempty"`
	FromTask string `json:"from_task,omitempty"`
	At       int64  `json:"at"`
}

func key(botID string) string {
	if botID == "" {
		return "lessons.all"
	}
	return "lessons.bot." + botID
}

// Service 提議、保存學到的事。
type Service struct {
	DB   *store.DB
	Call Caller
	// Teacher 挑老師用的模型；ok 是 false 時不提議。
	Teacher func(ctx context.Context) (Model, bool)
	// SoulChanged 在學到的事改了以後重寫 Bot 的 SOUL.md；botID 空白表示所有 Bot。可為 nil。
	SoulChanged func(ctx context.Context, botID string)
	// Notify 把卡片事件推給畫面。可為 nil。
	Notify func(evs ...store.Event)
	Logger *slog.Logger
	// Now 換掉現在時間（測試用）。
	Now func() time.Time

	mu   sync.Mutex
	busy map[string]bool
}

func (s *Service) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// list 讀一組學到的事（botID 空白是所有 Bot 的）。
func (s *Service) list(ctx context.Context, botID string) ([]Lesson, error) {
	var out []Lesson
	_, err := s.DB.GetSettingInto(ctx, key(botID), &out)
	return out, err
}

func (s *Service) save(ctx context.Context, botID string, l []Lesson) error {
	if len(l) == 0 {
		return s.DB.DeleteSetting(ctx, key(botID))
	}
	return s.DB.SetSetting(ctx, key(botID), l)
}

// For 是寫進一個 Bot 守則的學到的事：先所有 Bot 的，再這個 Bot 的。讀不到時是空的（不擋開機）。
func (s *Service) For(ctx context.Context, botID string) []string {
	var out []string
	for _, id := range []string{"", botID} {
		l, err := s.list(ctx, id)
		if err != nil {
			s.log().Warn("讀不到學到的事", "bot", id, "err", err)
			continue
		}
		for _, x := range l {
			out = append(out, x.Text)
		}
	}
	return out
}

// ErrFull 表示學到的事滿了。
var ErrFull = fmt.Errorf("學到的事最多 %d 條，請先刪掉不需要的", limits.LessonsMax)

// ErrText 表示文字不對。
var ErrText = fmt.Errorf("學到的事要 1 到 %d 個字", limits.LessonTextMax)

// Add 加一條學到的事（botID 空白是所有 Bot），寫進守則。
func (s *Service) Add(ctx context.Context, botID, text, fromTask string) (Lesson, error) {
	text = oneLine(text)
	if text == "" || utf8.RuneCountInString(text) > limits.LessonTextMax {
		return Lesson{}, ErrText
	}
	l, err := s.list(ctx, botID)
	if err != nil {
		return Lesson{}, err
	}
	if slices.ContainsFunc(l, func(x Lesson) bool { return x.Text == text }) {
		return Lesson{}, errors.New("已經有這一條了")
	}
	if len(l) >= limits.LessonsMax {
		return Lesson{}, ErrFull
	}
	x := Lesson{ID: store.NewID(), Text: text, BotID: botID, FromTask: fromTask, At: s.now().UnixMilli()}
	if err := s.save(ctx, botID, append(l, x)); err != nil {
		return Lesson{}, err
	}
	if s.SoulChanged != nil {
		s.SoulChanged(ctx, botID)
	}
	return x, nil
}

// Delete 刪掉一條學到的事。
func (s *Service) Delete(ctx context.Context, botID, id string) (bool, error) {
	l, err := s.list(ctx, botID)
	if err != nil {
		return false, err
	}
	n := len(l)
	l = slices.DeleteFunc(l, func(x Lesson) bool { return x.ID == id })
	if len(l) == n {
		return false, nil
	}
	if err := s.save(ctx, botID, l); err != nil {
		return false, err
	}
	if s.SoulChanged != nil {
		s.SoulChanged(ctx, botID)
	}
	return true, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// ── 提議 ────────────────────────────────────────────────

// 沒做完的判斷（runtime 的完成判定）：這幾種才請老師看。需要你（登入、核准）是該停的，不算。
var unfinished = []string{"only_promised", "handed_back", "stuck", "failed"}

// Unfinished 回答這個判斷算不算沒做完。
func Unfinished(verdict string) bool { return slices.Contains(unfinished, verdict) }

// cardPayload 是學到一件事的卡片內容。
type cardPayload struct {
	Lesson string `json:"lesson"`
	Why    string `json:"why,omitempty"`
	// Skill 有值時是修這個技能（在技能最後加一段注意事項），不是加一條守則。
	Skill   string `json:"skill,omitempty"`
	Teacher string `json:"teacher,omitempty"`
	BotName string `json:"bot_name,omitempty"`
	Asked   string `json:"asked,omitempty"`
}

// Consider 在一件工作確定沒做完以後呼叫（背景執行，不擋對話）：請老師看，提議一條學到的事。
// taskID 可以是續做串裡的任何一件；why 是完成判定的原因。同一串只提議一次，一個 Bot 同時只有一張卡、一天最多 limits.LessonsPerDay 張。
func (s *Service) Consider(ctx context.Context, taskID, why string) {
	if s == nil || s.Teacher == nil || s.Call == nil {
		return
	}
	t, err := s.DB.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	root := rootOf(t)
	if !s.claim(t.BotID) {
		return
	}
	defer s.release(t.BotID)
	if ok, err := s.allowed(ctx, t.BotID, root); err != nil || !ok {
		return
	}
	m, ok := s.Teacher(ctx)
	if !ok {
		return
	}
	in, err := s.trace(ctx, t, root, why)
	if err != nil || in.reply == "" {
		return
	}
	skill := ""
	for _, name := range in.skills {
		if st, err := s.skillStat(ctx, t.BotID, name); err == nil && st.Unfinished >= limits.SkillFailMin && st.Unfinished*2 >= st.Uses {
			skill = name
			break
		}
	}
	tctx, cancel := context.WithTimeout(ctx, limits.LessonTeacherTimeout)
	defer cancel()
	text, err := complete(tctx, s.Call, m, teacherSystem(skill), in.prompt(skill), 600)
	if err != nil {
		s.log().Warn("老師模型看不了這次的工作", "bot", t.BotID, "task", t.ID, "err", err)
		return
	}
	lesson, reason := parseLesson(text)
	if lesson == "" {
		return // 不是 Bot 能改的（要登入、額度用完…），或老師覺得沒什麼可學
	}
	s.card(ctx, t, root, cardPayload{Lesson: lesson, Why: reason, Skill: skill, Teacher: m.String(), BotName: in.botName, Asked: clip(in.asked, 80)})
}

func (s *Service) claim(botID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[botID] {
		return false
	}
	s.busy[botID] = true
	return true
}

func (s *Service) release(botID string) {
	s.mu.Lock()
	delete(s.busy, botID)
	s.mu.Unlock()
}

func rootOf(t store.Task) string {
	var m struct {
		Root string `json:"continue_root"`
	}
	if json.Unmarshal(t.Meta, &m) == nil && m.Root != "" {
		return m.Root
	}
	return t.ID
}

// allowed：這一串還沒提議過、這個 Bot 沒有還開著的卡、今天還沒到上限。
func (s *Service) allowed(ctx context.Context, botID, root string) (bool, error) {
	if exists, err := s.DB.InboxRefExists(ctx, "lesson."+root); err != nil || exists {
		return false, err
	}
	cards, err := s.DB.ListInboxFiltered(ctx, store.InboxFilter{Kinds: []string{CardKind}, BotID: botID})
	if err != nil {
		return false, err
	}
	y, m, d := s.now().Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, s.now().Location()).UnixMilli()
	today := 0
	for _, c := range cards {
		if c.Status == store.InboxOpen {
			return false, nil
		}
		if c.CreatedAt >= dayStart {
			today++
		}
	}
	return today < limits.LessonsPerDay, nil
}

// trace 是交給老師看的這一串工作：交代的事、步驟、最後的回覆。
type trace struct {
	botName, model, asked, reply, why string
	steps, skills                     []string
}

func (s *Service) trace(ctx context.Context, t store.Task, root, why string) (trace, error) {
	out := trace{why: why}
	b, err := s.DB.GetBot(ctx, t.BotID)
	if err != nil {
		return out, err
	}
	out.botName, out.model = b.Name, b.Model
	chain := []store.Task{}
	if r, err := s.DB.GetTask(ctx, root); err == nil {
		chain = append(chain, r)
		out.asked = r.Input
	}
	more, err := s.DB.ListTasksByMeta(ctx, "$.continue_root", root, false)
	if err != nil {
		return out, err
	}
	chain = append(chain, more...)
	if len(chain) == 0 {
		chain = []store.Task{t}
		out.asked = t.Input
	}
	last := chain[len(chain)-1]
	for i, c := range chain {
		if i > 0 {
			out.steps = append(out.steps, "（系統叫它自己接著做）")
		}
		evs, err := s.DB.ListThreadEvents(ctx, c.ThreadID, c.ID, []string{"chat.tool_call", "chat.tool_result"}, 0, 400)
		if err != nil {
			return out, err
		}
		failed := map[string]bool{}
		for _, e := range evs {
			if e.Kind == "chat.tool_result" {
				var r struct {
					Tool struct {
						ID string `json:"id"`
						OK *bool  `json:"ok"`
					} `json:"tool"`
				}
				if json.Unmarshal(e.Payload, &r) == nil && r.Tool.OK != nil && !*r.Tool.OK {
					failed[r.Tool.ID] = true
				}
			}
		}
		for _, e := range evs {
			var p struct {
				Tool struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"tool"`
			}
			if e.Kind != "chat.tool_call" || json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			if p.Tool.Name == "skill_view" {
				var a struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(p.Tool.Args, &a) == nil && a.Name != "" && !slices.Contains(out.skills, a.Name) {
					out.skills = append(out.skills, a.Name)
				}
			}
			if len(out.steps) < limits.LessonSteps {
				step := p.Tool.Name + " " + clip(string(p.Tool.Args), 120)
				if failed[p.Tool.ID] {
					step += "（失敗）"
				}
				out.steps = append(out.steps, step)
			}
		}
	}
	if m, err := s.DB.TaskReply(ctx, last.ThreadID, last.ID); err == nil {
		out.reply = m.Content
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	return out, nil
}

func teacherSystem(skill string) string {
	if skill != "" {
		return "你是 AI 助理團隊的教練。一個 Bot 用了技能「" + skill + "」，卻沒把交代的事做完；這個技能最近常這樣。" +
			"看它的步驟和最後的回覆，寫一條要加進這個技能最後「注意」段落的提醒，讓之後用這個技能的 Bot 做得完。" +
			`只回 JSON：{"lesson": "一句繁體中文提醒，` + fmt.Sprint(limits.LessonTextMax) + ` 字內，具體可執行，不提這次的細節", "why": "一句原因，30 字內"}。` +
			"如果沒做完的原因不是技能或 Bot 能改的（要使用者登入、驗證碼、付款、額度用完、網站壞掉），lesson 回空字串。"
	}
	return "你是 AI 助理團隊的教練。一個 Bot 這次沒把交代的事做完。看它的步驟和最後的回覆，找出它自己做錯、下次能避免的地方，" +
		"寫成一條給它的守則。好的守則是做法（例：「網站被擋時先換比價網站或搜尋引擎找，查不到才請使用者登入」），不是口號（例：「要更努力」）。" +
		`只回 JSON：{"lesson": "一句繁體中文守則，` + fmt.Sprint(limits.LessonTextMax) + ` 字內，具體可執行，不提這次的細節", "why": "一句原因，30 字內"}。` +
		"如果沒做完的原因不是 Bot 能改的（要使用者登入、驗證碼、付款、額度用完、網站壞掉），lesson 回空字串。"
}

func (in trace) prompt(skill string) string {
	var b strings.Builder
	b.WriteString("Bot：" + in.botName)
	if in.model != "" {
		b.WriteString("（模型 " + in.model + "）")
	}
	b.WriteString("\n使用者交代：" + clip(in.asked, 600) + "\n")
	if skill != "" {
		b.WriteString("用的技能：" + skill + "\n")
	}
	b.WriteString("系統判斷：" + in.why + "\n步驟（工具與參數）：\n")
	if len(in.steps) == 0 {
		b.WriteString("（沒有用任何工具）\n")
	}
	for i, st := range in.steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, st)
	}
	b.WriteString("最後的回覆：\n" + clip(in.reply, 1500))
	return b.String()
}

// parseLesson 讀老師的 JSON（容許包在 ```json 裡、前後有字）。
func parseLesson(text string) (lesson, why string) {
	t := strings.TrimSpace(text)
	if i, j := strings.Index(t, "{"), strings.LastIndex(t, "}"); i >= 0 && j > i {
		t = t[i : j+1]
	}
	var v struct {
		Lesson string `json:"lesson"`
		Why    string `json:"why"`
	}
	if json.Unmarshal([]byte(t), &v) != nil {
		return "", ""
	}
	l := oneLine(v.Lesson)
	if utf8.RuneCountInString(l) > limits.LessonTextMax {
		l = string([]rune(l)[:limits.LessonTextMax])
	}
	return l, clip(oneLine(v.Why), 60)
}

func (s *Service) card(ctx context.Context, t store.Task, root string, p cardPayload) {
	raw, _ := json.Marshal(p)
	title := "從這次學到一件事"
	if p.Skill != "" {
		title = "技能「" + p.Skill + "」可以這樣改"
	}
	it, err := s.DB.CreateInboxItem(ctx, store.InboxItem{Kind: CardKind, BotID: t.BotID, TaskID: root, ThreadID: t.ThreadID, Ref: "lesson." + root,
		Title: title, Body: p.Lesson, Payload: raw})
	if err != nil {
		s.log().Warn("開學到一件事的卡失敗", "bot", t.BotID, "err", err)
		return
	}
	e, err := s.DB.AppendEvent(ctx, notify.Event(notify.KindCreated, it))
	if err == nil && s.Notify != nil {
		s.Notify(e)
	}
}

// ── 技能成效 ─────────────────────────────────────────────

// SkillStat 是一個技能最近被用了幾次、幾次沒做完。
type SkillStat struct {
	Name       string `json:"name"`
	Uses       int    `json:"uses" desc:"最近用了幾次（幾件工作）"`
	Done       int    `json:"done" desc:"其中做完或該停下來等你的"`
	Unfinished int    `json:"unfinished" desc:"其中沒做完的（只說要做、交回給你、卡住、出錯）"`
	LastUsed   int64  `json:"last_used"`
}

// SkillStats 統計一個 Bot 最近 limits.SkillStatsDays 天用過的技能：每件工作看那一串最後的完成判定。
func (s *Service) SkillStats(ctx context.Context, botID string) ([]SkillStat, error) {
	since := s.now().Add(-time.Duration(limits.SkillStatsDays) * 24 * time.Hour).UnixMilli()
	views, err := s.DB.SkillViews(ctx, botID, since)
	if err != nil {
		return nil, err
	}
	stats := map[string]*SkillStat{}
	seen := map[string]bool{}
	finals := map[string]string{}
	for _, v := range views {
		t, err := s.DB.GetTask(ctx, v.TaskID)
		if err != nil {
			continue
		}
		root := rootOf(t)
		if seen[v.Name+"\x00"+root] {
			continue
		}
		seen[v.Name+"\x00"+root] = true
		verdict, ok := finals[root]
		if !ok {
			verdict = s.finalVerdict(ctx, root)
			finals[root] = verdict
		}
		if verdict == "" {
			continue // 還在做、或是停掉的
		}
		st := stats[v.Name]
		if st == nil {
			st = &SkillStat{Name: v.Name}
			stats[v.Name] = st
		}
		st.Uses++
		if Unfinished(verdict) {
			st.Unfinished++
		} else {
			st.Done++
		}
		st.LastUsed = max(st.LastUsed, v.At)
	}
	out := make([]SkillStat, 0, len(stats))
	for _, st := range stats {
		out = append(out, *st)
	}
	slices.SortFunc(out, func(a, b SkillStat) int { return int(b.LastUsed - a.LastUsed) })
	return out, nil
}

func (s *Service) skillStat(ctx context.Context, botID, name string) (SkillStat, error) {
	all, err := s.SkillStats(ctx, botID)
	if err != nil {
		return SkillStat{}, err
	}
	for _, st := range all {
		if st.Name == name {
			return st, nil
		}
	}
	return SkillStat{Name: name}, nil
}

// finalVerdict 是一串工作最後的完成判定（續做的最後一件）；還沒判、停掉的回空字串。
func (s *Service) finalVerdict(ctx context.Context, root string) string {
	chain, err := s.DB.ListTasksByMeta(ctx, "$.continue_root", root, false)
	if err != nil {
		return ""
	}
	if len(chain) == 0 {
		t, err := s.DB.GetTask(ctx, root)
		if err != nil {
			return ""
		}
		chain = []store.Task{t}
	}
	var m struct {
		Judge struct {
			Verdict string `json:"verdict"`
		} `json:"judge"`
	}
	last := chain[len(chain)-1]
	if json.Unmarshal(last.Meta, &m) != nil {
		return ""
	}
	return m.Judge.Verdict
}
