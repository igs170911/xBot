package judge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/store"
)

func opsEnv(t *testing.T) (*env, *ops.Registry) {
	t.Helper()
	e := newEnv(t)
	reg := ops.New(ops.Config{DB: e.db})
	Register(reg, e.svc)
	return e, reg
}

func invoke[T any](t *testing.T, reg *ops.Registry, id ops.Identity, op string, in any) (T, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	var out T
	res, err := reg.Invoke(context.Background(), id, op, raw, ops.Options{})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func codeOf(err error) ops.Code {
	var e *ops.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestJudgeOps(t *testing.T) {
	e, reg := opsEnv(t)
	owner := ops.Owner("test")
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "代理", Via: "mcp", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeAdmin, ops.ScopeOperate, ops.ScopeManage}}

	got, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_get", SettingsIn{})
	if err != nil || got.Settings.Tier1 != DefaultTier1 || len(got.Uses) != len(Uses) || got.Today.DailyCap != 300 || got.MinP != 0.6 {
		t.Fatalf("設定：%+v %v", got, err)
	}
	// 免費模型不能用；不認得的用途、開關要擋。
	if _, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Tier1: &TierModel{Provider: "opencode-zen", Model: "jev-1.13-free"}}); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("免費模型：%v", err)
	}
	if _, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Modes: map[string]Mode{"nope": ModeLive}}); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("用途：%v", err)
	}
	low := 50
	got, err = invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Modes: map[string]Mode{UseRoute: ModeLive}, DailyCap: &low})
	if err != nil || got.Settings.DailyCap != 50 || got.Settings.Modes[UseRoute] != ModeLive {
		t.Fatalf("改設定：%+v %v", got, err)
	}
	// 代理降低上限直接生效；提高上限要你核准（不能代為核准）。
	lower := 40
	if _, err := invoke[SettingsOut](t, reg, agent, "xbot.judge.settings_set", SetIn{DailyCap: &lower}); err != nil {
		t.Fatalf("降低：%v", err)
	}
	high := 1000
	if _, err := invoke[SettingsOut](t, reg, agent, "xbot.judge.settings_set", SetIn{DailyCap: &high}); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("提高要核准：%v", err)
	}
	if set, _ := e.svc.Settings(context.Background()); set.DailyCap != 40 {
		t.Fatalf("沒核准不改：%d", set.DailyCap)
	}

	// judge.run：生效時用模型；影子時回規則、看得到模型怎麼說。
	e.up.set(jsonReply("chat", 0.9, "打招呼"))
	d, err := invoke[Decision](t, reg, agent, "xbot.judge.run", RunIn{Use: UseRoute, Text: "嗨嗨"})
	if err != nil || d.Label != "chat" || d.By != ByModel || d.Mode != ModeLive {
		t.Fatalf("run：%+v %v", d, err)
	}
	d, err = invoke[Decision](t, reg, agent, "xbot.judge.run", RunIn{Use: UseErrorKind, Text: "connection reset"})
	if err != nil || d.Label != "transient" || d.By != ByRules || d.Mode != ModeShadow || d.Outcome != OutcomeInvalid {
		t.Fatalf("影子：%+v %v", d, err)
	}
	if _, err := invoke[Decision](t, reg, agent, "xbot.judge.run", RunIn{Use: "nope", Text: "x"}); codeOf(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
	st, err := invoke[StatsOut](t, reg, agent, "xbot.judge.stats", StatsIn{})
	if err != nil || st.Today.Calls != 3 || len(st.Recent) != 2 {
		t.Fatalf("統計：%+v %v", st.Today, err)
	}

	// 一句話建 Bot：關的時候用最接近的範本；生效時用模型，漏掉的欄位用範本補。
	s, err := invoke[SuggestOut](t, reg, owner, "xbot.bots.suggest", SuggestIn{Text: "幫我寫社群貼文"})
	if err != nil || s.Name != "小寫" || s.Title != "文案" || s.By != ByRules || s.Soul == "" || s.Template != "writer" {
		t.Fatalf("範本：%+v %v", s, err)
	}
	if _, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Modes: map[string]Mode{UseBotSuggest: ModeLive}}); err != nil {
		t.Fatal(err)
	}
	e.up.set(func(upstreamReq) (int, string, time.Duration) {
		return 200, reply(`{"label":"小貼","p":0.8,"why":"寫貼文","fields":{"title":"社群小編","description":"每天寫兩則貼文"}}`), 0
	})
	s, err = invoke[SuggestOut](t, reg, owner, "xbot.bots.suggest", SuggestIn{Text: "幫我寫社群貼文"})
	if err != nil || s.Name != "小貼" || s.Title != "社群小編" || s.Description != "每天寫兩則貼文" || s.By != ByModel || s.Soul == "" {
		t.Fatalf("模型：%+v %v", s, err)
	}

	// 先判斷與「不對？」。
	b, _ := e.db.CreateBot(context.Background(), store.Bot{Name: "小研"})
	th, _ := e.db.CreateThread(context.Background(), store.Thread{BotID: b.ID, Title: "x"})
	task, _ := e.db.CreateTask(context.Background(), store.Task{BotID: b.ID, ThreadID: th.ID, Source: "owner", State: "done", Input: "x"})
	pc, err := invoke[PreCheckOut](t, reg, owner, "xbot.judge.precheck", PreCheckIn{BotID: b.ID, Text: "查天氣", TaskID: task.ID})
	if err != nil || len(pc.Chips) != 0 {
		t.Fatalf("影子模式不顯示：%+v %v", pc, err)
	}
	fb, err := invoke[FeedbackOut](t, reg, owner, "xbot.judge.feedback", FeedbackIn{Use: UsePreComputer, TaskID: task.ID, Judged: "web", Corrected: "browser"})
	if err != nil || fb.Correction.Use != UsePreComputer || fb.Correction.Corrected != "browser" || fb.Correction.JudgedBy != ByModel {
		t.Fatalf("改正：%+v %v", fb, err)
	}
	if _, err := invoke[FeedbackOut](t, reg, owner, "xbot.judge.feedback", FeedbackIn{Use: UsePreComputer, TaskID: task.ID, Judged: "web", Corrected: "moon"}); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("選項以外：%v", err)
	}
	if _, err := invoke[FeedbackOut](t, reg, agent, "xbot.judge.feedback", FeedbackIn{Use: UsePreComputer, TaskID: task.ID, Judged: "web"}); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("只有你本人：%v", err)
	}
	if _, err := invoke[FeedbackOut](t, reg, owner, "xbot.judge.feedback", FeedbackIn{Use: UseTaskDone, TaskID: task.ID, Judged: "done"}); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("完成判定用 judge.correct：%v", err)
	}
}
