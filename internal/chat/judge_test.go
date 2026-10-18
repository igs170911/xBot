package chat

import (
	"errors"
	"strings"
	"testing"

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
	raw, err = e.call(owner, "xbot.tasks.progress", map[string]any{"bot_id": e.bot.ID})
	p = must[runtime.Progress](t, raw, err)
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
