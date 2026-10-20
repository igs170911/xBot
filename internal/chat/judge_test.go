package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/judge"
	"xbot/internal/ops"
	"xbot/internal/runtime"
)

// X04：wait_until_settled、chat.read 帶完成判定；不對？的改正記下來，重新讀時看得到；目前進度。
func TestJudgmentAndCorrection(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "回覆 OK"})
	sent := must[SendOut](t, raw, err)
	w := e.waitFor(t, owner, sent.TaskID)
	if w.Judge == nil || w.Judge.Verdict != judge.Done || w.Judge.By != judge.ByRules || w.Judge.Label != "已完成" {
		t.Fatalf("%+v", w.Judge)
	}
	read := func() Message {
		raw, err := e.call(owner, "xbot.chat.read", map[string]any{"thread_id": sent.ThreadID})
		r := must[ReadOut](t, raw, err)
		return r.Messages[len(r.Messages)-1]
	}
	if m := read(); m.Judge == nil || m.Judge.Verdict != judge.Done || m.Judge.Corrected != "" {
		t.Fatalf("%+v", m)
	}
	// 只有你本人能改正；不認得的判斷、找不到的工作不行。
	if _, err := e.call(agent(e.bot.ID), "xbot.judge.correct", map[string]any{"task_id": sent.TaskID, "verdict": "stuck"}); !isCode(err, ops.CodeForbidden) {
		t.Fatalf("代理：%v", err)
	}
	if _, err := e.call(owner, "xbot.judge.correct", map[string]any{"task_id": sent.TaskID, "verdict": "maybe"}); !isCode(err, ops.CodeInvalidInput) {
		t.Fatalf("不認得：%v", err)
	}
	if _, err := e.call(owner, "xbot.judge.correct", map[string]any{"task_id": "nope", "verdict": "stuck"}); !isCode(err, ops.CodeNotFound) {
		t.Fatalf("找不到：%v", err)
	}
	raw, err = e.call(owner, "xbot.judge.correct", map[string]any{"task_id": sent.TaskID, "verdict": "stuck", "note": "其實沒做完"})
	c := must[CorrectOut](t, raw, err)
	if c.Correction.Judged != "done" || c.Correction.Corrected != "stuck" || c.Correction.JudgedBy != "rules" || c.Correction.Use != runtime.UseTaskDone || c.Correction.Note != "其實沒做完" {
		t.Fatalf("%+v", c)
	}
	// 再改一次：換成新的，只有一筆。
	raw, err = e.call(owner, "xbot.judge.correct", map[string]any{"task_id": sent.TaskID, "verdict": "needs_person"})
	must[CorrectOut](t, raw, err)
	raw, err = e.call(owner, "xbot.judge.corrections", map[string]any{})
	list := must[CorrectionsOut](t, raw, err)
	if len(list.Corrections) != 1 || list.Corrections[0].Corrected != "needs_person" {
		t.Fatalf("%+v", list)
	}
	if m := read(); m.Judge == nil || m.Judge.Corrected != judge.NeedsPerson {
		t.Fatalf("重新讀要看得到改正：%+v", m.Judge)
	}
	// 只看得到自己能碰的 Bot 的改正。
	raw, err = e.call(agent(e.other.ID), "xbot.judge.corrections", map[string]any{})
	if l := must[CorrectionsOut](t, raw, err); len(l.Corrections) != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestProgressOp(t *testing.T) {
	e := setup(t)
	raw, err := e.call(owner, "xbot.tasks.progress", map[string]any{"bot_id": e.bot.ID})
	p := must[runtime.Progress](t, raw, err)
	if p.State != "idle" || p.Busy || !strings.Contains(p.Text, "目前沒有任何人在工作") {
		t.Fatalf("%+v", p)
	}
	raw, err = e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "回覆 OK"})
	sent := must[SendOut](t, raw, err)
	e.waitFor(t, owner, sent.TaskID)
	// 快判在工作結束後才判，機器忙的時候會晚一點寫進去：等到有判斷（最多 5 秒）
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		raw, err = e.call(owner, "xbot.tasks.progress", map[string]any{"bot_id": e.bot.ID})
		p = must[runtime.Progress](t, raw, err)
		if (p.Last != nil && p.Last.Judge != nil) || time.Now().After(deadline) {
			break
		}
	}
	if p.Last == nil || p.Last.TaskID != sent.TaskID || p.Last.Judge == nil || p.Last.Result != "OK" {
		t.Fatalf("%+v", p.Last)
	}
	if _, err := e.call(agent(e.other.ID), "xbot.tasks.progress", map[string]any{"bot_id": e.bot.ID}); !isCode(err, ops.CodeForbidden) {
		t.Fatalf("別的 Bot：%v", err)
	}
}

func isCode(err error, code ops.Code) bool {
	var oe *ops.Error
	return errors.As(err, &oe) && oe.Code == code
}

// 重播快判：當時判錯（舊規則把等你開口的提議當成只說要做）的工作，用現在的規則重判會變；你改正過的看得到判得對不對。
func TestJudgeReplay(t *testing.T) {
	e := setup(t)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Complete("我可以幫你查資料、做報表。\n\n直接講要做什麼，我就開始。", "complete", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "嗨，你今天可以幫我做什麼？"})
	sent := must[SendOut](t, raw, err)
	if w := e.waitFor(t, owner, sent.TaskID); w.Judge == nil || w.Judge.Verdict != judge.Done {
		t.Fatalf("現在的規則：%+v", w.Judge)
	}
	// 假裝是舊規則判的：只說要做
	if err := e.db.PatchTaskMeta(bg, sent.TaskID, map[string]any{"judge": map[string]any{"verdict": "only_promised", "label": "只說要做", "by": "rules"}}); err != nil {
		t.Fatal(err)
	}
	raw, err = e.call(owner, "xbot.judge.replay", map[string]any{})
	out := must[ReplayOut](t, raw, err)
	if out.Checked != 1 || out.Changed != 1 || len(out.Cases) != 1 || out.Cases[0].Was != judge.OnlyPromised || out.Cases[0].Now != judge.Done ||
		!strings.Contains(out.Cases[0].ReplyEnd, "我就開始") || out.Cases[0].Asked != "嗨，你今天可以幫我做什麼？" {
		t.Fatalf("%+v", out)
	}
	if _, err := e.call(owner, "xbot.judge.correct", map[string]any{"task_id": sent.TaskID, "verdict": "done"}); err != nil {
		t.Fatal(err)
	}
	raw, err = e.call(owner, "xbot.judge.replay", map[string]any{"bot_id": e.bot.ID})
	out = must[ReplayOut](t, raw, err)
	if out.Corrected != 1 || out.AgreedBefore != 0 || out.AgreesNow != 1 || out.Cases[0].Yours != judge.Done {
		t.Fatalf("改正：%+v", out)
	}
	// 別的 Bot 只看得到自己的
	raw, err = e.call(agent(e.other.ID), "xbot.judge.replay", map[string]any{})
	if o := must[ReplayOut](t, raw, err); o.Checked != 0 {
		t.Fatalf("%+v", o)
	}
}
