package lessons

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

var bg = context.Background()

func TestReplyText(t *testing.T) {
	for body, want := range map[string]string{
		`{"choices":[{"message":{"content":"甲"}}]}`:                                     "甲",
		`{"content":[{"type":"text","text":"乙"}]}`:                                      "乙",
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"丙"}]}]}`: "丙",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"丁\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"戊\"}\n": "丁戊",
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"己\"}]}]}}\n":                                         "己",
	} {
		if got := replyText([]byte(body)); got != want {
			t.Errorf("%s → %q，應該是 %q", body, got, want)
		}
	}
	if l, w := parseLesson("好的：\n```json\n{\"lesson\": \"網站被擋時先換比價網站找\", \"why\": \"它停下來等登入\"}\n```"); l != "網站被擋時先換比價網站找" || w != "它停下來等登入" {
		t.Fatalf("%q %q", l, w)
	}
	if l, _ := parseLesson(`{"lesson": ""}`); l != "" {
		t.Fatal("空的不提議")
	}
}

type env struct {
	db    *store.DB
	reg   *ops.Registry
	s     *Service
	bot   store.Bot
	mu    sync.Mutex
	asked []string
	reply string
	souls []string
	skill string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reply: `{"lesson":"網站被擋時先換比價網站或搜尋引擎找，查不到才請使用者登入","why":"它一被擋就停"}`}
	e.reg = ops.New(ops.Config{DB: db})
	e.s = &Service{DB: db,
		Teacher: func(context.Context) (Model, bool) {
			return Model{Provider: "chatgpt", Model: "gpt-6-sol", Transport: proxy.TransportResponses}, true
		},
		Call: func(_ context.Context, req proxy.CallRequest) (proxy.CallResponse, error) {
			e.mu.Lock()
			e.asked = append(e.asked, req.Path+" "+string(req.Body))
			e.mu.Unlock()
			sse := "data: " + mustJSON(map[string]any{"type": "response.output_text.delta", "delta": e.reply}) + "\n"
			return proxy.CallResponse{Status: 200, Body: []byte(sse)}, nil
		},
		SoulChanged: func(_ context.Context, botID string) {
			e.mu.Lock()
			e.souls = append(e.souls, botID)
			e.mu.Unlock()
		},
	}
	Register(e.reg, e.s)
	// 假的技能操作（noteSkill 經 xbot.skills.get／update 改技能）
	type skillIn struct {
		BotID        string `json:"bot_id,omitempty"`
		ID           string `json:"id"`
		Instructions string `json:"instructions,omitempty"`
	}
	type skillOut struct {
		ID           string `json:"id"`
		Instructions string `json:"instructions"`
	}
	e.skill = "# 比價\n\n先查蝦皮。"
	ops.Register(e.reg, ops.Op[skillIn, skillOut]{Name: "xbot.skills.get", Summary: "測試", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, _ *ops.Call, in skillIn) (skillOut, error) {
			return skillOut{ID: "shop-compare", Instructions: e.skill}, nil
		}})
	ops.Register(e.reg, ops.Op[skillIn, skillOut]{Name: "xbot.skills.update", Summary: "測試", Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, _ *ops.Call, in skillIn) (skillOut, error) {
			e.skill = in.Instructions
			return skillOut{ID: in.ID, Instructions: in.Instructions}, nil
		}})
	if e.bot, err = db.CreateBot(bg, store.Bot{Name: "小幫手", Model: "openai/deepseek-v4.1-flash"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// failedTask 是一件沒做完的工作：用了一個技能、瀏覽器被擋、最後請你登入。
func (e *env) failedTask(t *testing.T, verdict string, skill string) store.Task {
	t.Helper()
	th, err := e.db.CreateThread(bg, store.Thread{BotID: e.bot.ID, Title: "比價"})
	if err != nil {
		t.Fatal(err)
	}
	meta := mustJSON(map[string]any{"judge": map[string]any{"verdict": verdict, "reason": "把還沒做完的部分交回給你"}})
	task, err := e.db.CreateTask(bg, store.Task{BotID: e.bot.ID, ThreadID: th.ID, Source: "owner", Input: "幫我找三款車用垃圾桶比價", Meta: json.RawMessage(meta)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.SetTaskState(bg, task.ID, store.TaskDone, "answer"); err != nil {
		t.Fatal(err)
	}
	for i, call := range []map[string]any{
		{"id": "c0", "name": "skill_view", "args": map[string]any{"name": skill}},
		{"id": "c1", "name": "browser_exec", "args": map[string]any{"code": "open shopee"}},
	} {
		if skill == "" && i == 0 {
			continue
		}
		if _, err := e.db.AppendEvent(bg, store.Event{BotID: e.bot.ID, TaskID: task.ID, ThreadID: th.ID, Kind: "chat.tool_call", Payload: json.RawMessage(mustJSON(map[string]any{"tool": call}))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.AppendEvent(bg, store.Event{BotID: e.bot.ID, TaskID: task.ID, ThreadID: th.ID, Kind: "chat.tool_result",
		Payload: json.RawMessage(mustJSON(map[string]any{"tool": map[string]any{"id": "c1", "name": "browser_exec", "ok": false}}))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.AddMessage(bg, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "bot:" + e.bot.ID, Content: "蝦皮擋住了，請你登入後交還給我。",
		Meta: json.RawMessage(mustJSON(map[string]any{"task_id": task.ID}))}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.db.GetTask(bg, task.ID)
	return got
}

func (e *env) cards(t *testing.T, status string) []store.InboxItem {
	t.Helper()
	items, err := e.db.ListInboxFiltered(bg, store.InboxFilter{Kinds: []string{CardKind}, BotID: e.bot.ID, Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

var owner = ops.Owner("test")

func (e *env) call(id ops.Identity, op string, in any) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(bg, id, op, raw, ops.Options{})
	return res.Output, err
}

func TestConsiderProposesAndApply(t *testing.T) {
	e := setup(t)
	task := e.failedTask(t, "handed_back", "")
	e.s.Consider(bg, task.ID, "把還沒做完的部分交回給你")
	open := e.cards(t, store.InboxOpen)
	if len(open) != 1 || open[0].Title != "從這次學到一件事" || !strings.Contains(open[0].Body, "比價網站") {
		t.Fatalf("卡片：%+v", open)
	}
	// 老師看到的：交代的事、步驟（失敗的標出來）、最後的回覆；用 ChatGPT 方案的 Responses
	e.mu.Lock()
	asked := strings.Join(e.asked, "\n")
	e.mu.Unlock()
	for _, want := range []string{"responses ", "幫我找三款車用垃圾桶比價", "browser_exec", "（失敗）", "請你登入後交還給我", "deepseek-v4.1-flash"} {
		if !strings.Contains(asked, want) {
			t.Fatalf("老師要看到 %q：%s", want, asked)
		}
	}
	// 同一串不再提議、還有卡開著時不再提議
	e.s.Consider(bg, task.ID, "x")
	other := e.failedTask(t, "stuck", "")
	e.s.Consider(bg, other.ID, "x")
	if n := len(e.cards(t, "")); n != 1 {
		t.Fatalf("只能有一張：%d", n)
	}
	// Bot 不能決定
	if _, err := e.call(ops.Bot(e.bot.ID, "小幫手"), "xbot.lessons.decide", DecideIn{InboxID: open[0].ID, Apply: "bot"}); err == nil {
		t.Fatal("Bot 不能幫自己加守則")
	}
	raw, err := e.call(owner, "xbot.lessons.decide", DecideIn{InboxID: open[0].ID, Apply: "bot", Text: "網站被擋時先換比價網站找"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "網站被擋時先換比價網站找") {
		t.Fatalf("%s", raw)
	}
	if got := e.s.For(bg, e.bot.ID); len(got) != 1 || got[0] != "網站被擋時先換比價網站找" {
		t.Fatalf("寫進守則的：%v", got)
	}
	if e.souls[len(e.souls)-1] != e.bot.ID {
		t.Fatalf("要重寫這個 Bot 的 SOUL：%v", e.souls)
	}
	if len(e.cards(t, store.InboxOpen)) != 0 {
		t.Fatal("卡片要收掉")
	}
	if _, err := e.call(owner, "xbot.lessons.decide", DecideIn{InboxID: open[0].ID, Apply: "bot"}); err == nil {
		t.Fatal("處理過的不能再處理")
	}

	// 加給所有 Bot、手動加、刪除
	if _, err := e.call(owner, "xbot.lessons.add", AddIn{Text: "查資料先用 web_search，要登入的頁面才開瀏覽器"}); err != nil {
		t.Fatal(err)
	}
	if got := e.s.For(bg, e.bot.ID); len(got) != 2 || !strings.Contains(got[0], "web_search") {
		t.Fatalf("所有 Bot 的在前：%v", got)
	}
	raw, _ = e.call(owner, "xbot.lessons.list", ListIn{BotID: e.bot.ID})
	var l ListOut
	_ = json.Unmarshal(raw, &l)
	if len(l.All) != 1 || len(l.Bot) != 1 || l.Max != limits.LessonsMax {
		t.Fatalf("%s", raw)
	}
	if _, err := e.call(owner, "xbot.lessons.delete", DeleteIn{BotID: e.bot.ID, ID: l.Bot[0].ID}); err != nil {
		t.Fatal(err)
	}
	if got := e.s.For(bg, e.bot.ID); len(got) != 1 {
		t.Fatalf("刪掉後：%v", got)
	}
}

func TestConsiderSkipsWhatTheBotCannotFix(t *testing.T) {
	e := setup(t)
	e.reply = `{"lesson":"","why":"要使用者登入"}`
	task := e.failedTask(t, "handed_back", "")
	e.s.Consider(bg, task.ID, "x")
	if n := len(e.cards(t, "")); n != 0 {
		t.Fatalf("不是 Bot 能改的不提議：%d", n)
	}
}

// 一個技能最近常沒做完：改提議修那個技能，同意後加在技能最後的「注意」段落。
func TestSkillThatKeepsFailing(t *testing.T) {
	e := setup(t)
	e.s.Now = func() time.Time { return time.Now() }
	for range limits.SkillFailMin {
		e.failedTask(t, "stuck", "shop-compare")
	}
	st, err := e.s.SkillStats(bg, e.bot.ID)
	if err != nil || len(st) != 1 || st[0].Name != "shop-compare" || st[0].Uses != limits.SkillFailMin || st[0].Unfinished != limits.SkillFailMin {
		t.Fatalf("%+v %v", st, err)
	}
	task := e.failedTask(t, "stuck", "shop-compare")
	e.s.Consider(bg, task.ID, "卡住了")
	open := e.cards(t, store.InboxOpen)
	if len(open) != 1 || open[0].Title != "技能「shop-compare」可以這樣改" {
		t.Fatalf("%+v", open)
	}
	if _, err := e.call(owner, "xbot.lessons.decide", DecideIn{InboxID: open[0].ID, Apply: "skill"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.skill, skillNoteHead) || !strings.Contains(e.skill, "- 網站被擋時先換比價網站") || !strings.HasPrefix(e.skill, "# 比價") {
		t.Fatalf("技能：%s", e.skill)
	}
	if len(e.s.For(bg, e.bot.ID)) != 0 {
		t.Fatal("修技能不加守則")
	}
}
