package teaching

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/boxfs"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/screen"
	"xbot/internal/skills"
	"xbot/internal/store"
)

var ctx = context.Background()

// fakeComputer 是一台假電腦：檔案在記憶體，錄製器「啟動」就寫就緒標記、收到 .stop 就寫結束標記（LazyBoy 測試的 box fixture）。
type fakeComputer struct {
	mu          sync.Mutex
	mem         *boxfs.Mem
	cmds        [][]string
	unsupported bool // 沒有錄製器（舊映像）
	noReady     bool // 瀏覽器錄製器起不來
	running     bool
}

func newComputer() *fakeComputer { return &fakeComputer{mem: boxfs.NewMem(), running: true} }

func (f *fakeComputer) ReadFile(c context.Context, p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem.ReadFile(c, p)
}

func (f *fakeComputer) ReadDir(c context.Context, dir string, max int64) ([]boxfs.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem.ReadDir(c, dir, max)
}

func (f *fakeComputer) WriteFiles(c context.Context, dir string, files []boxfs.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem.WriteFiles(c, dir, files)
}

func (f *fakeComputer) Remove(c context.Context, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem.Remove(c, p)
}

func (f *fakeComputer) put(p, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem.Files[p] = []byte(text)
}

func (f *fakeComputer) has(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.mem.Files[p]
	return ok
}

func (f *fakeComputer) ran(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			n++
		}
	}
	return n
}

func (f *fakeComputer) Run(_ context.Context, cmd ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if !f.running {
		return boxfs.ErrNotRunning
	}
	switch cmd[0] {
	case "test":
		if f.unsupported {
			return &boxfs.ExitError{Cmd: "test", Code: 1}
		}
	case "sh":
		dir := cmd[len(cmd)-1]
		if !strings.Contains(cmd[2], "box-teach-browser.mjs") || !strings.Contains(cmd[2], "box-teach-desktop") {
			return errors.New("錄製器沒有啟動")
		}
		if !f.noReady {
			f.mem.Files[dir+"/.ready-browser"] = []byte("1")
			f.mem.Files[dir+"/.ready-desktop"] = []byte("2")
		}
	case "touch":
		f.mem.Files[cmd[1]] = nil
		if path.Base(cmd[1]) == ".stop" {
			f.mem.Files[path.Dir(cmd[1])+"/.done-browser"] = nil
			f.mem.Files[path.Dir(cmd[1])+"/.done-desktop"] = nil
		}
	case "rm":
		delete(f.mem.Files, cmd[len(cmd)-1])
	}
	return nil
}

// fakeScreen 是假的接手（B14）。
type fakeScreen struct {
	mu        sync.Mutex
	takeovers map[string]*screen.Takeover
	handbacks []string
}

func (s *fakeScreen) Takeover(_ context.Context, botID, by, byName string) (screen.Takeover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.takeovers[botID]; cur != nil {
		if cur.By != by {
			return *cur, screen.ErrTakenByOther
		}
		return *cur, nil
	}
	s.takeovers[botID] = &screen.Takeover{By: by, ByName: byName}
	return *s.takeovers[botID], nil
}

func (s *fakeScreen) Handback(_ context.Context, botID, by, note string) (screen.HandbackResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.takeovers[botID] == nil {
		return screen.HandbackResult{}, screen.ErrNotTakenOver
	}
	delete(s.takeovers, botID)
	s.handbacks = append(s.handbacks, note)
	return screen.HandbackResult{Note: note}, nil
}

func (s *fakeScreen) TakeoverOf(botID string) *screen.Takeover {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.takeovers[botID]; t != nil {
		c := *t
		return &c
	}
	return nil
}

func (s *fakeScreen) handbackCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.handbacks)
}

// fakeRuntime 記下交辦與系統訊息。
type fakeRuntime struct {
	mu     sync.Mutex
	active map[string]string
	starts []runtime.ChatRequest
	notes  []string
}

func (r *fakeRuntime) Active(botID string) (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.active[botID]; t != "" {
		return "th-busy", t
	}
	return "", ""
}

func (r *fakeRuntime) Start(_ context.Context, req runtime.ChatRequest) (runtime.ChatResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, req)
	return runtime.ChatResult{ThreadID: "th-trial", TaskID: "task-trial"}, nil
}

func (r *fakeRuntime) Note(_ context.Context, _, _, _, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, text)
	return nil
}

type fixture struct {
	db     *store.DB
	comp   *fakeComputer
	skills *boxfs.Mem
	scr    *fakeScreen
	rt     *fakeRuntime
	bus    *events.Bus
	svc    *Service
	bot    store.Bot
	polish func(context.Context, string, string) (string, string, bool)
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bot, err := db.CreateBot(ctx, store.Bot{Name: "抓蟲", Engine: "hermes"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, comp: newComputer(), skills: boxfs.NewMem(), scr: &fakeScreen{takeovers: map[string]*screen.Takeover{}},
		rt: &fakeRuntime{active: map[string]string{}}, bus: events.New(), bot: bot}
	f.svc = New(Options{DB: db, Screen: f.scr, Runtime: f.rt, Bus: f.bus, Poll: 10 * time.Millisecond, StartTimeout: 200 * time.Millisecond,
		StopTimeout: 100 * time.Millisecond,
		Open: func(_ context.Context, botID string, wake bool) (Computer, error) {
			if wake {
				f.comp.mu.Lock()
				f.comp.running = true
				f.comp.mu.Unlock()
			}
			return f.comp, nil
		},
		Create: func(c context.Context, botID string, e skills.Edit) (skills.Skill, error) {
			return skills.Bot{FS: f.skills, BotID: botID}.Create(c, e)
		},
		Polish: func(c context.Context, text, botID string) (string, string, bool) {
			if f.polish == nil {
				return "", "", false
			}
			return f.polish(c, text, botID)
		},
	})
	t.Cleanup(func() { f.svc.Forget(bot.ID) })
	return f
}

func (f *fixture) rows(t *testing.T, lessonID string, rows ...map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		raw, _ := json.Marshal(r)
		b.Write(raw)
		b.WriteByte('\n')
	}
	f.comp.put(dirOf(lessonID)+"/browser.jsonl", b.String())
}

func (f *fixture) start(t *testing.T) Lesson {
	t.Helper()
	l, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, Goal: "查訂單", ThreadID: "th1", By: "owner", ByName: "你"})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// 翻自 LazyBoy test_teaching_holds_the_computer_and_hands_the_demo_to_the_bot：接手電腦、錄製器以 Bot 使用者啟動、
// 補充與刪步驟與暫停、完成後交還並整理成草稿、存成技能（含截圖）、請 Bot 試做一次。
func TestTeachingHoldsTheComputerAndTurnsTheDemoIntoASkill(t *testing.T) {
	f := setup(t)
	sub := f.bus.Subscribe(func(e events.Event) bool { return e.Kind == KindTeaching }, 0)
	defer sub.Close()
	l := f.start(t)
	if l.State != store.LessonRecording || l.Goal != "查訂單" || f.scr.TakeoverOf(f.bot.ID) == nil {
		t.Fatalf("開始：%+v", l)
	}
	if f.comp.ran("sh -c") != 1 || f.comp.ran("mkdir -p "+dirOf(l.ID)+"/shots") != 1 {
		t.Fatalf("錄製器：%v", f.comp.cmds)
	}
	if !strings.HasPrefix(dirOf(l.ID), "/workspace/.teaching/") {
		t.Fatalf("錄製檔要寫在可寫的 /workspace：%s", dirOf(l.ID))
	}
	// 同一個 Bot 再按一次開始：回傳同一次。
	if again, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); err != nil || again.ID != l.ID {
		t.Fatalf("再開始一次：%+v %v", again, err)
	}

	f.comp.put(dirOf(l.ID)+"/shots/b-0001.jpg", "\xff\xd8jpeg-1")
	f.rows(t, l.ID,
		map[string]any{"at": 1000, "source": "browser", "kind": "click", "target": map[string]any{"role": "button", "name": "訂單", "selector": "#orders"},
			"shot": "shots/b-0001.jpg", "url": "https://shop.test/"},
		map[string]any{"at": 2000, "source": "browser", "kind": "click", "target": map[string]any{"role": "button", "name": "點錯了"}})
	// 即時步驟清單：watch 讀到新的紀錄就發事件。
	deadline := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case e := <-sub.C:
			var ev Event
			_ = json.Unmarshal(e.Payload, &ev)
			got = ev.Lesson != nil && len(ev.Lesson.Steps) == 2
		case <-deadline:
			t.Fatal("沒有收到步驟更新的事件")
		}
	}
	v, err := f.svc.AddNote(ctx, f.bot.ID, l.ID, "只看已完成")
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(v.Steps); strings.Join(got, "|") != "點擊按鈕「訂單」|點擊按鈕「點錯了」|使用者補充：只看已完成" {
		t.Fatalf("補充後：%q", got)
	}
	if v, err = f.svc.Drop(ctx, f.bot.ID, l.ID, v.Steps[1].ID); err != nil || len(v.Steps) != 2 {
		t.Fatalf("刪步驟：%+v %v", v.Steps, err)
	}
	if v, err = f.svc.Pause(ctx, f.bot.ID, l.ID, true); err != nil || v.State != store.LessonPaused || !f.comp.has(dirOf(l.ID)+"/.paused") {
		t.Fatalf("暫停：%+v %v", v, err)
	}
	if v, err = f.svc.Pause(ctx, f.bot.ID, l.ID, false); err != nil || v.State != store.LessonRecording || f.comp.has(dirOf(l.ID)+"/.paused") {
		t.Fatalf("繼續：%+v %v", v, err)
	}

	done, err := f.svc.Finish(ctx, f.bot.ID, l.ID, "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.comp.has(dirOf(l.ID)+"/.stop") || f.comp.ran("pkill -f box-teach-.*"+l.ID) != 1 || f.scr.TakeoverOf(f.bot.ID) != nil {
		t.Fatalf("完成要停掉錄製器並交還畫面：%v", f.comp.cmds)
	}
	if done.State != store.LessonStopped || done.Draft == nil || strings.Join(texts(done.Steps), "|") != "點擊按鈕「訂單」|使用者補充：只看已完成" {
		t.Fatalf("完成：%+v", done)
	}
	if !strings.Contains(done.Draft.Instructions, "定位：`#orders`") || !strings.Contains(done.Draft.Instructions, "截圖：shots/b-0001.jpg") || done.Draft.Name != "查訂單" {
		t.Fatalf("草稿：%+v", done.Draft)
	}
	if _, err := f.svc.AddNote(ctx, f.bot.ID, l.ID, "太晚了"); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("結束後補充：%v", err)
	}
	if cur, _ := f.svc.Current(ctx, f.bot.ID); cur == nil || cur.ID != l.ID || cur.State != store.LessonStopped {
		t.Fatalf("草稿等你確認時還算進行中：%+v", cur)
	}
	// 草稿沒存之前不能開始下一次。
	if _, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); !errors.Is(err, ErrDraftPending) {
		t.Fatalf("草稿還在：%v", err)
	}

	out, err := f.svc.Save(ctx, SaveRequest{BotID: f.bot.ID, LessonID: l.ID, Name: "查已完成訂單", Description: "要查訂單時使用",
		Instructions: done.Draft.Instructions + "\n改過的一行", Trial: true, By: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Skill.Name != "查已完成訂單" || out.Skill.Source != SourceTaught || out.TaskID != "task-trial" || out.Lesson.State != store.LessonSaved {
		t.Fatalf("存檔：%+v", out)
	}
	md := string(f.skills.Files[skills.SkillsDir+"/"+out.Skill.ID+"/SKILL.md"])
	if !strings.Contains(md, "name: 查已完成訂單") || !strings.Contains(md, "改過的一行") {
		t.Fatalf("SKILL.md：%s", md)
	}
	if string(f.skills.Files[skills.SkillsDir+"/"+out.Skill.ID+"/shots/b-0001.jpg"]) != "\xff\xd8jpeg-1" {
		t.Fatalf("截圖要一起存進技能：%v", f.skills.Files)
	}
	if f.comp.has(dirOf(l.ID) + "/browser.jsonl") {
		t.Fatal("存檔後錄製檔要刪掉")
	}
	if len(f.rt.starts) != 1 || !strings.Contains(f.rt.starts[0].Prompt, "查已完成訂單") || !strings.Contains(f.rt.starts[0].Prompt, "不要按下去") {
		t.Fatalf("試做：%+v", f.rt.starts)
	}
	if cur, _ := f.svc.Current(ctx, f.bot.ID); cur != nil {
		t.Fatalf("存檔後沒有進行中的教學：%+v", cur)
	}
	if strings.Join(f.rt.notes, "|") != "你開始示範，抓蟲在旁邊看著，每一步都會記下來|示範完成，記下 2 步，整理成技能草稿等你確認|學會了新技能「查已完成訂單」" {
		t.Fatalf("對話串的系統訊息：%q", f.rt.notes)
	}
}

func TestFinishingBeforeAnythingHappenedKeepsTeaching(t *testing.T) {
	f := setup(t)
	l := f.start(t)
	if _, err := f.svc.Finish(ctx, f.bot.ID, l.ID, "owner", nil); !errors.Is(err, ErrNoSteps) {
		t.Fatalf("什麼都沒記到：%v", err)
	}
	if cur, _ := f.svc.Current(ctx, f.bot.ID); cur == nil || cur.State != store.LessonRecording || f.scr.TakeoverOf(f.bot.ID) == nil {
		t.Fatalf("示範要繼續：%+v", cur)
	}
	f.comp.put(dirOf(l.ID)+"/browser.jsonl", `{"at":1,"source":"browser","kind":"click"}`)
	if _, err := f.svc.Discard(ctx, f.bot.ID, l.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := f.svc.Current(ctx, f.bot.ID); cur != nil || f.comp.has(dirOf(l.ID)+"/browser.jsonl") || f.scr.TakeoverOf(f.bot.ID) != nil {
		t.Fatalf("取消後：%+v", cur)
	}
	if got, _ := f.db.GetLesson(ctx, l.ID); got.State != store.LessonDiscarded {
		t.Fatalf("%+v", got)
	}
	// 取消後暫停是普通的錯誤（LazyBoy test_pausing_a_cancelled_demonstration_is_a_plain_error）。
	if _, err := f.svc.Pause(ctx, f.bot.ID, l.ID, true); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("%v", err)
	}
	if _, err := f.svc.Discard(ctx, f.bot.ID, l.ID, "owner"); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("取消兩次：%v", err)
	}
}

// LazyBoy test_teaching_needs_an_idle_bot_and_a_current_computer、test_a_failed_start_releases_the_computer。
func TestTeachingNeedsAnIdleBotAndACurrentComputer(t *testing.T) {
	f := setup(t)
	f.comp.unsupported = true
	if _, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("舊電腦：%v", err)
	}
	if f.scr.TakeoverOf(f.bot.ID) != nil {
		t.Fatal("不支援時不接手")
	}
	f.comp.unsupported = false
	f.rt.active[f.bot.ID] = "task1"
	if _, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Bot 在忙：%v", err)
	}
	delete(f.rt.active, f.bot.ID)
	// 瀏覽器錄製器起不來：交還畫面、刪掉錄製資料夾、這次教學捨棄。
	f.comp.noReady = true
	if _, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("錄製器沒起來：%v", err)
	}
	if f.scr.TakeoverOf(f.bot.ID) != nil || f.scr.handbackCount() != 1 || f.comp.ran("pkill") != 1 {
		t.Fatalf("失敗要放開電腦：%v", f.comp.cmds)
	}
	if cur, _ := f.svc.Current(ctx, f.bot.ID); cur != nil {
		t.Fatalf("%+v", cur)
	}
	// 別人正在操作畫面。
	f.comp.noReady = false
	f.scr.takeovers[f.bot.ID] = &screen.Takeover{By: "agent:x"}
	if _, err := f.svc.Start(ctx, StartRequest{BotID: f.bot.ID, By: "owner"}); !errors.Is(err, screen.ErrTakenByOther) {
		t.Fatalf("別人在操作：%v", err)
	}
}

// LazyBoy test_a_restart_discards_an_unfinished_demonstration。
func TestARestartDiscardsAnUnfinishedDemonstration(t *testing.T) {
	f := setup(t)
	l := f.start(t)
	f.svc.Forget(f.bot.ID)
	again := New(Options{DB: f.db, Screen: f.scr, Runtime: f.rt, Open: func(context.Context, string, bool) (Computer, error) { return f.comp, nil }})
	if err := again.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetLesson(ctx, l.ID); got.State != store.LessonDiscarded {
		t.Fatalf("%+v", got)
	}
	for i := 0; i < 100 && !f.comp.has(dirOf(l.ID)+"/.stop"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !f.comp.has(dirOf(l.ID) + "/.stop") {
		t.Fatal("要請錄製器結束")
	}
}

// 示範中畫面被交還（你在別處交還、接手太久沒人看自動交還）：示範自動結束，記到的步驟整理成草稿。
func TestHandingBackTheScreenEndsTheDemonstration(t *testing.T) {
	f := setup(t)
	l := f.start(t)
	f.rows(t, l.ID, map[string]any{"at": 1000, "source": "browser", "kind": "click", "target": map[string]any{"role": "link", "name": "報表"}})
	if _, err := f.scr.Handback(ctx, f.bot.ID, "system", ""); err != nil {
		t.Fatal(err)
	}
	var got store.Lesson
	for i := 0; i < 300; i++ {
		got, _ = f.db.GetLesson(ctx, l.ID)
		if got.State == store.LessonStopped {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.State != store.LessonStopped || !strings.Contains(string(got.Draft), "點擊連結「報表」") {
		t.Fatalf("自動結束：%+v", got)
	}
	// 什麼都沒記到時直接捨棄。
	f2 := setup(t)
	l2 := f2.start(t)
	_, _ = f2.scr.Handback(ctx, f2.bot.ID, "system", "")
	for i := 0; i < 300; i++ {
		if got, _ = f2.db.GetLesson(ctx, l2.ID); got.State == store.LessonDiscarded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.State != store.LessonDiscarded {
		t.Fatalf("沒記到步驟：%+v", got)
	}
}

func TestDraftUsesTheFastJudgeWhenItAnswers(t *testing.T) {
	f := setup(t)
	var seen string
	f.polish = func(_ context.Context, text, _ string) (string, string, bool) {
		seen = text
		return "「重現運費歸零」", "結帳頁運費不對時使用", true
	}
	l := f.start(t)
	f.rows(t, l.ID, map[string]any{"at": 1000, "source": "browser", "kind": "click", "target": map[string]any{"role": "button", "name": "套用"}})
	goal := "重現結帳運費歸零"
	done, err := f.svc.Finish(ctx, f.bot.ID, l.ID, "owner", &goal)
	if err != nil {
		t.Fatal(err)
	}
	if done.Goal != goal || done.Draft.Name != "重現運費歸零" || done.Draft.Description != "結帳頁運費不對時使用" || done.Draft.By != "model" {
		t.Fatalf("%+v", done.Draft)
	}
	if !strings.Contains(seen, "目的：重現結帳運費歸零") || !strings.Contains(seen, "1. [瀏覽器] 點擊按鈕「套用」") {
		t.Fatalf("給快判的步驟：%q", seen)
	}
	// 快判沒有回答：照範本。補上目的後重新整理。
	f.polish = nil
	other := "查運費"
	v, err := f.svc.Redraft(ctx, f.bot.ID, l.ID, &other)
	if err != nil || v.Draft.Name != "查運費" || v.Draft.By != "rules" {
		t.Fatalf("%+v %v", v.Draft, err)
	}
	// 同名技能已經存在。
	n, d, i := "查運費", "x", "y"
	if _, err := (skills.Bot{FS: f.skills, BotID: f.bot.ID}).Create(ctx, skills.Edit{Name: &n, Description: &d, Instructions: &i}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Save(ctx, SaveRequest{BotID: f.bot.ID, LessonID: l.ID, Name: "查運費", Description: "d", Instructions: "i"}); !errors.Is(err, skills.ErrExists) {
		t.Fatalf("同名：%v", err)
	}
	if got, _ := f.db.GetLesson(ctx, l.ID); got.State != store.LessonStopped {
		t.Fatalf("存檔失敗時草稿還在：%+v", got)
	}
}

func TestShots(t *testing.T) {
	f := setup(t)
	l := f.start(t)
	f.comp.put(dirOf(l.ID)+"/shots/b-0001.jpg", "\xff\xd8x")
	if b, err := f.svc.Shot(ctx, f.bot.ID, l.ID, "shots/b-0001.jpg"); err != nil || string(b) != "\xff\xd8x" {
		t.Fatalf("%q %v", b, err)
	}
	for _, bad := range []string{"../../etc/passwd", "shots/b-0002.jpg", "browser.jsonl"} {
		if _, err := f.svc.Shot(ctx, f.bot.ID, l.ID, bad); !errors.Is(err, ErrNoShot) {
			t.Errorf("%q：%v", bad, err)
		}
	}
	other, _ := f.db.CreateBot(ctx, store.Bot{Name: "別人", Engine: "hermes"})
	if _, err := f.svc.Shot(ctx, other.ID, l.ID, "shots/b-0001.jpg"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("別的 Bot 的教學：%v", err)
	}
}

// 操作只給你本人（MCP 優先，但示範教學要人在電腦前；外部代理與 Bot 不能）。
func TestOpsAreOwnerOnly(t *testing.T) {
	f := setup(t)
	reg := ops.New(ops.Config{DB: f.db})
	Register(reg, f.svc)
	want := map[string]ops.Scope{"xbot.teaching.get": ops.ScopeRead, "xbot.teaching.start": ops.ScopeOperate, "xbot.teaching.pause": ops.ScopeOperate,
		"xbot.teaching.note": ops.ScopeOperate, "xbot.teaching.drop": ops.ScopeOperate, "xbot.teaching.stop": ops.ScopeOperate,
		"xbot.teaching.draft": ops.ScopeOperate, "xbot.teaching.save": ops.ScopeManage, "xbot.teaching.discard": ops.ScopeOperate,
		"xbot.teaching.shot": ops.ScopeRead}
	for _, d := range reg.List() {
		if s, ok := want[d.Name]; ok {
			if d.Scope != s || d.Risk != ops.RiskSafe || d.ForBot {
				t.Errorf("%s：%+v", d.Name, d)
			}
			delete(want, d.Name)
		}
	}
	if len(want) > 0 {
		t.Fatalf("少了：%v", want)
	}
	in, _ := json.Marshal(map[string]any{"bot_id": f.bot.ID})
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Scopes: ops.Scopes}
	if _, err := reg.Invoke(ctx, agent, "xbot.teaching.start", in, ops.Options{}); ops.AsError(err).Code != ops.CodeForbidden {
		t.Fatalf("代理不能教：%v", err)
	}
	owner := ops.Identity{Kind: ops.KindOwner}
	res, err := reg.Invoke(ctx, owner, "xbot.teaching.start", in, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var l Lesson
	_ = json.Unmarshal(res.Output, &l)
	if l.State != store.LessonRecording {
		t.Fatalf("%s", res.Output)
	}
	stop, _ := json.Marshal(map[string]any{"bot_id": f.bot.ID, "lesson_id": l.ID})
	if _, err := reg.Invoke(ctx, owner, "xbot.teaching.stop", stop, ops.Options{}); ops.AsError(err).Code != ops.CodeInvalidInput {
		t.Fatalf("還沒記到操作：%v", err)
	}
}
