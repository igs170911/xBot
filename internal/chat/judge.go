package chat

// X04：目前進度（xbot.tasks.progress，PRD §19.2）與完成判定的改正（xbot.judge.correct、xbot.judge.corrections，§20 FJ-7）。

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"xbot/internal/judge"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// Judgment 是一件工作做完了沒的系統判斷，加上你的改正（有的話）。
type Judgment struct {
	runtime.Judgment
	Corrected judge.Verdict `json:"corrected,omitempty" desc:"你按「不對？」改正成哪一種"`
}

// ProgressIn 是 xbot.tasks.progress 的輸入。
type ProgressIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"要看哪個 Bot"`
}

// CorrectIn 是 xbot.judge.correct 的輸入。
type CorrectIn struct {
	TaskID  string        `json:"task_id" required:"true" max:"64" desc:"被判斷的那件工作"`
	Verdict judge.Verdict `json:"verdict" required:"true" desc:"應該是哪一種"`
	Note    string        `json:"note,omitempty" max:"500" desc:"補充說明（可空白）"`
}

// CorrectOut 是 xbot.judge.correct 的輸出。
type CorrectOut struct {
	Correction store.JudgeCorrection `json:"correction"`
}

// CorrectionsIn 是 xbot.judge.corrections 的輸入。
type CorrectionsIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot 的"`
	Limit int    `json:"limit,omitempty" min:"1" max:"500" desc:"最多幾筆；預設 50"`
}

// CorrectionsOut 是 xbot.judge.corrections 的輸出。
type CorrectionsOut struct {
	Corrections []store.JudgeCorrection `json:"corrections" desc:"新到舊"`
}

// ReplayIn 是 xbot.judge.replay 的輸入。
type ReplayIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot 的工作；不填是全部"`
	Days  int    `json:"days,omitempty" min:"1" max:"90" desc:"看最近幾天結束的工作；預設 7"`
}

// ReplayCase 是一件重新判過的工作。
type ReplayCase struct {
	TaskID string `json:"task_id"`
	BotID  string `json:"bot_id"`
	// Asked 是交代的事（前 80 字）；ReplyEnd 是 Bot 回覆的最後 160 字（判斷主要看這裡）。
	Asked    string        `json:"asked"`
	ReplyEnd string        `json:"reply_end"`
	Was      judge.Verdict `json:"was" desc:"當時的判斷"`
	Now      judge.Verdict `json:"now" desc:"用現在的規則重判"`
	NowWhy   string        `json:"now_reason"`
	Yours    judge.Verdict `json:"yours,omitempty" desc:"你按「不對？」改正成的（有的話）"`
}

// ReplayOut 是 xbot.judge.replay 的輸出。
type ReplayOut struct {
	Checked int `json:"checked" desc:"重判了幾件工作"`
	Changed int `json:"changed" desc:"現在的規則判得和當時不一樣的件數"`
	// Corrected、AgreedBefore、AgreesNow：你改正過的幾件、當時判對幾件、現在的規則判對幾件。
	Corrected    int          `json:"corrected"`
	AgreedBefore int          `json:"agreed_before"`
	AgreesNow    int          `json:"agrees_now"`
	Cases        []ReplayCase `json:"cases" desc:"你改正過的、和判斷會變的工作（最多 50 件）"`
}

func registerJudge(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[ReplayIn, ReplayOut]{
		Name: "xbot.judge.replay", Summary: "用現在的規則重新判斷最近的工作，看完成判定有沒有更對或冒出新的誤判",
		Description: "改了完成判定（已完成、需要你、交回給你、只說要做、卡住了）的規則之後用：拿最近結束的工作和你按「不對？」改正過的，" +
			"用現在的規則重判一次，列出判斷會變的工作和你改正過的工作現在判得對不對。只用規則、不呼叫模型，不會改變任何工作。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: replay,
	})
	ops.Register(reg, ops.Op[ProgressIn, runtime.Progress]{
		Name: "xbot.tasks.progress", Summary: "看 Bot 的目前進度（正在做什麼、卡在哪、等誰）",
		Description: "系統查得的事實，不是 Bot 自己說的：正在做的工作與經過時間、做了幾步、正在執行的工具、排隊幾件、" +
			"卡在等你的問題、傳給隊友還沒回來的請求（做完要回報給誰）、最後一次出錯、最近一次完成與系統判斷。" +
			"busy=false 而交辦的事還沒完成，代表卡住了。text 是同一份事實寫成幾行字（你問 Bot「進度如何」時它拿到的就是這份）。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ProgressIn) (runtime.Progress, error) {
			rt, err := d.runtime()
			if err != nil {
				return runtime.Progress{}, err
			}
			if _, err := call.DB.GetBot(ctx, in.BotID); errors.Is(err, store.ErrNotFound) {
				return runtime.Progress{}, ops.NotFound("找不到這個 Bot")
			} else if err != nil {
				return runtime.Progress{}, err
			}
			return rt.Progress(ctx, in.BotID, "")
		},
	})
	ops.Register(reg, ops.Op[CorrectIn, CorrectOut]{
		Name: "xbot.judge.correct", Summary: "改正系統對一件工作的判斷（不對？）",
		Description: "系統判斷（已完成、需要你、交回給你、只說要做、卡住了）不對時，告訴它應該是哪一種。" +
			"只記下來用來校正規則與快判門檻，不會改變工作本身。同一件工作再改一次會換成新的。只有你本人能改正。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: correct,
	})
	ops.Register(reg, ops.Op[CorrectionsIn, CorrectionsOut]{
		Name: "xbot.judge.corrections", Summary: "列出你對系統判斷的改正",
		Description: "用來比對規則與快判（PRD §20 FJ-5）：每筆有系統當時的判斷、誰判的（rules／model）、原因，以及你改成哪一種。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in CorrectionsIn) (CorrectionsOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = limits.PageDefault
			}
			list, err := call.DB.ListJudgeCorrections(ctx, store.JudgeCorrectionFilter{Use: runtime.UseTaskDone, BotID: in.BotID, Limit: limit})
			if err != nil {
				return CorrectionsOut{}, err
			}
			out := CorrectionsOut{Corrections: []store.JudgeCorrection{}}
			for _, c := range list {
				if call.Identity.CanAccess(ops.ResourceBot, c.BotID) {
					out.Corrections = append(out.Corrections, c)
				}
			}
			return out, nil
		},
	})
}

func correct(ctx context.Context, call *ops.Call, in CorrectIn) (CorrectOut, error) {
	if call.Identity.Kind != ops.KindOwner {
		return CorrectOut{}, ops.Forbidden("只有你本人能改正系統的判斷")
	}
	if !in.Verdict.Valid() {
		return CorrectOut{}, ops.InvalidInput(ops.FieldError{Field: "verdict", Message: "要是 done、needs_person、handed_back、only_promised、stuck 其中一種"})
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > limits.JudgeNoteMax {
		return CorrectOut{}, ops.InvalidInput(ops.FieldError{Field: "note", Message: "太長了"})
	}
	task, err := call.DB.GetTask(ctx, in.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return CorrectOut{}, ops.NotFound("找不到這件工作")
	}
	if err != nil {
		return CorrectOut{}, err
	}
	if !call.Identity.CanAccess(ops.ResourceBot, task.BotID) {
		return CorrectOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這件工作的 Bot")
	}
	j := runtime.TaskJudgment(task)
	if j == nil {
		return CorrectOut{}, ops.Conflict("這件工作沒有系統判斷（還沒做完，或是你停掉的）")
	}
	c := store.JudgeCorrection{Use: runtime.UseTaskDone, TaskID: task.ID, BotID: task.BotID, Judged: string(j.Verdict), JudgedBy: j.By,
		Reason: j.Reason, Corrected: string(in.Verdict), Note: note, Actor: call.Identity.Actor()}
	if call.DryRun {
		return CorrectOut{Correction: c}, nil
	}
	saved, err := call.DB.PutJudgeCorrection(ctx, c)
	if err != nil {
		return CorrectOut{}, err
	}
	return CorrectOut{Correction: saved}, nil
}

// judgments 讀這些工作的改正，接到訊息的判斷上（重新整理後畫面也看得到你改過）。
func judgments(ctx context.Context, db *store.DB, msgs []Message) {
	var ids []string
	for _, m := range msgs {
		if m.Judge != nil && m.TaskID != "" {
			ids = append(ids, m.TaskID)
		}
	}
	if len(ids) == 0 {
		return
	}
	list, err := db.ListJudgeCorrections(ctx, store.JudgeCorrectionFilter{Use: runtime.UseTaskDone, TaskIDs: ids})
	if err != nil {
		return
	}
	by := map[string]string{}
	for _, c := range list {
		by[c.TaskID] = c.Corrected
	}
	for i := range msgs {
		if m := &msgs[i]; m.Judge != nil {
			m.Judge.Corrected = judge.Verdict(by[m.TaskID])
		}
	}
}

func replay(ctx context.Context, call *ops.Call, in ReplayIn) (ReplayOut, error) {
	days := in.Days
	if days <= 0 {
		days = limits.JudgeReplayDays
	}
	since := store.NowMS() - int64(days)*24*3600*1000
	tasks, err := call.DB.RecentJudgedTasks(ctx, in.BotID, since, limits.JudgeReplayTasks)
	if err != nil {
		return ReplayOut{}, err
	}
	corrections, err := call.DB.ListJudgeCorrections(ctx, store.JudgeCorrectionFilter{Use: runtime.UseTaskDone, BotID: in.BotID, Limit: limits.JudgeReplayTasks})
	if err != nil {
		return ReplayOut{}, err
	}
	yours := map[string]judge.Verdict{}
	seen := map[string]bool{}
	for _, c := range corrections {
		yours[c.TaskID] = judge.Verdict(c.Corrected)
	}
	// 改正過的工作不在最近幾天裡也要重判
	for _, c := range corrections {
		if t, err := call.DB.GetTask(ctx, c.TaskID); err == nil {
			tasks = append(tasks, t)
		}
	}
	out := ReplayOut{Cases: []ReplayCase{}}
	for _, t := range tasks {
		if seen[t.ID] || !call.Identity.CanAccess(ops.ResourceBot, t.BotID) {
			continue
		}
		seen[t.ID] = true
		was := runtime.TaskJudgment(t)
		if was == nil {
			continue
		}
		jin, ok, err := runtime.ReplayInput(ctx, call.DB, t)
		if err != nil {
			return ReplayOut{}, err
		}
		if !ok {
			continue
		}
		now, err := (judge.Rules{}).Judge(ctx, jin)
		if errors.Is(err, judge.ErrSkip) {
			continue
		} else if err != nil {
			return ReplayOut{}, err
		}
		out.Checked++
		c := ReplayCase{TaskID: t.ID, BotID: t.BotID, Asked: clip(t.Input, 80), ReplyEnd: tail(judge.Prose(jin.Text), 160),
			Was: was.Verdict, Now: now.Verdict, NowWhy: now.Reason}
		changed := now.Verdict != was.Verdict
		if changed {
			out.Changed++
		}
		if y, ok := yours[t.ID]; ok {
			c.Yours = y
			out.Corrected++
			if was.Verdict == y {
				out.AgreedBefore++
			}
			if now.Verdict == y {
				out.AgreesNow++
			}
		}
		if (changed || c.Yours != "") && len(out.Cases) < limits.JudgeReplayCases {
			out.Cases = append(out.Cases, c)
		}
	}
	return out, nil
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func tail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return string(r)
}
