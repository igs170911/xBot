package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// ── 接到產品流程的用途 ─────────────────────────────

// errNotLive 表示這次沒有採用模型（關、影子、退回規則）；Fallback 收到就用規則。
var errNotLive = errors.New("快判沒有生效，照規則")

// TaskDone 是用途 2「任務完成了沒」的模型判斷，接在 Fallback 的 Primary（X04 的介面）。
// 影子模式只記錄；生效模式模型合格才用。
//
// 2026-10-23（影子紀錄 217 次的分析）：只在規則判「做完了」、是你本人、定時任務或 Webhook 交代的第一輪、而且不是閒聊或測試時才問模型；
// 模型只能往嚴的方向改（做完了 → 交回、只說要做、要你），要有 limits.JudgeTaskDoneMinP 的把握，「卡住」不採用、也不降級。
// 模型看得到你原本交代的話（原本只看回覆，把「嗨」的回覆當成沒做完）。因為只看第一輪，同一個請求最多被模型叫回去一次。
type TaskDone struct{ S *Service }

// Judge 實作 Judge。
func (m TaskDone) Judge(ctx context.Context, in Input) (Result, error) {
	if m.S == nil || in.Outcome != OutcomeAnswer || in.Asked == "" || in.Continued || in.Background || IsChitchat(in.Asked) {
		return Result{}, errNotLive
	}
	base, err := (Rules{}).Judge(ctx, in)
	if err != nil {
		return Result{}, err
	}
	if base.Verdict != Done {
		return Result{}, errNotLive
	}
	facts := []string{"the owner asked: " + clipRunes(strings.TrimSpace(in.Asked), limits.JudgeRouteInputMax)}
	if in.UsedTools {
		facts = append(facts, "the bot used tools in this turn")
	} else {
		facts = append(facts, "the bot used no tools in this turn")
	}
	if len(in.PlanLeft) > 0 {
		facts = append(facts, fmt.Sprintf("its plan still has %d unfinished items", len(in.PlanLeft)))
	}
	if len(in.Stranded) > 0 {
		facts = append(facts, fmt.Sprintf("%d background subtasks failed and were not retried", len(in.Stranded)))
	}
	d, _ := m.S.Decide(ctx, Ask{Use: UseTaskDone, Text: headTail(in.Text, limits.JudgeTaskDoneInputMax), Context: facts, Ref: in.TaskID, BotID: in.BotID,
		Rule: &Answer{Label: string(base.Verdict), Why: base.Reason}})
	if !d.Live() {
		return Result{}, errNotLive
	}
	if v := Verdict(d.Label); (v != HandedBack && v != OnlyPromised && v != NeedsPerson) || d.P < limits.JudgeTaskDoneMinP {
		return Result{}, errNotLive
	}
	why := d.Why
	if why == "" {
		why = "快判模型判斷"
	}
	return Result{Verdict: Verdict(d.Label), Reason: clipRunes(why, limits.JudgeReasonMax), By: ByModel, Confidence: d.P, Ask: base.Ask}, nil
}

// headTail 把太長的回覆截成開頭一段和結尾一段：沒做完的線索多半在最後（原本只留開頭 800 字，長回覆的結尾都看不到）。
func headTail(text string, max int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= max {
		return string(r)
	}
	head := max * 3 / 10
	return string(r[:head]) + "\n…\n" + string(r[len(r)-(max-head-3):])
}

// TaskDoneJudge 是 runtime 用的完成判定：快判模型＋規則兜底（Fallback）。
func (s *Service) TaskDoneJudge() Judge {
	return Fallback{Primary: TaskDone{S: s}, Rules: Rules{}, Wait: limits.JudgeTaskDoneWait}
}

// memberOption 是候選 Bot 給模型看的一句話（OpenMausBot memberOption）。
func memberOption(b store.Bot) string {
	s := b.Name
	if b.Title != "" {
		s += ", " + b.Title
	}
	s += " bot."
	if b.Description != "" {
		s += " " + clipRunes(strings.Join(strings.Fields(b.Description), " "), 200)
	}
	return s
}

// Responder 是用途 7：群組沒點名時誰回（collab.Judge 的簽名）。生效、機率 ≥ 0.6、選到一位成員才回 ok；
// 「several」或其他情況回 ok=false，群組交給主責（OpenMausBot room-routing：失敗照原本規則）。
func (s *Service) Responder(ctx context.Context, room store.Room, members []store.Bot, text string) (string, float64, bool) {
	if len(members) < 2 {
		return "", 0, false
	}
	options := make([]Option, 0, len(members)+1)
	names := make([]string, 0, len(members))
	lead := members[0].ID
	for _, b := range members {
		options = append(options, Option{Key: b.ID, Meaning: memberOption(b), Shown: b.Name})
		names = append(names, b.Name)
		if b.ID == room.LeadID {
			lead = b.ID
		}
	}
	options = append(options, Option{Key: SeveralKey, Meaning: responderSeveralTxt, Shown: "好幾位"})
	d, _ := s.Decide(ctx, Ask{Use: UseResponder, Text: text, Options: options,
		Context: []string{"room: " + room.Name, "members: " + strings.Join(names, ", ")},
		Rule:    &Answer{Label: lead, Why: "交給主責"}, Ref: room.ID})
	if !d.Live() || d.Label == SeveralKey {
		return "", 0, false
	}
	return d.Label, d.P, true
}

// Summary 是用途 8：一行摘要（≤ 30 字）。生效且模型有回答時 ok=true；否則回規則（第一句截斷）、ok=false。
func (s *Service) Summary(ctx context.Context, text, ref, botID string) (string, bool) {
	d, _ := s.Decide(ctx, Ask{Use: UseSummary, Text: text, Ref: ref, BotID: botID})
	return d.Label, d.Live()
}

// Title 是用途 10：對話串命名（≤ 16 字）。生效且模型有回答時 ok=true。
func (s *Service) Title(ctx context.Context, text, botID string) (string, bool) {
	d, _ := s.Decide(ctx, Ask{Use: UseTitle, Text: text, BotID: botID})
	return d.Label, d.Live()
}

// Routine 是一句話建定時任務的快判：把你的話整理成規則解析器看得懂的說法。生效且模型有回答時 ok=true。
func (s *Service) Routine(ctx context.Context, text string) (string, bool) {
	d, _ := s.Decide(ctx, Ask{Use: UseRoutine, Text: text})
	return d.Label, d.Live()
}

// Teach 是用途 17（B32）：示範的步驟 → 技能名稱與一句「什麼時候使用」。生效且模型有回答時 ok=true；否則照範本（ok=false）。
func (s *Service) Teach(ctx context.Context, text, botID string) (name, description string, ok bool) {
	d, _ := s.Decide(ctx, Ask{Use: UseTeach, Text: text, BotID: botID})
	if !d.Live() || d.Label == "" {
		return "", "", false
	}
	return d.Label, d.Fields["description"], true
}

// Chip 是「先判斷」列的一項（C15）。
type Chip struct {
	Use   string  `json:"use"`
	N     int     `json:"n"`
	Label string  `json:"label"`
	Shown string  `json:"shown" desc:"膠囊上的字，例：夠清楚、交給 Hermes · 中"`
	P     float64 `json:"p"`
	Why   string  `json:"why,omitempty"`
	By    string  `json:"by"`
	// Options 是「不對？」選單：這一項的其他選項。
	Options []ChipOption `json:"options"`
}

// ChipOption 是「不對？」選單的一個選項。
type ChipOption struct {
	Key   string `json:"key"`
	Shown string `json:"shown"`
}

// PreCheck 是「先判斷」用途 11–14：同時問四項，只回生效、模型合格（機率 ≥ 0.6）的那幾項（C15：不確定或逾時就不顯示、照規則走）。
// 只是建議：不會擋住送出，也不會換引擎或開不開電腦。engine 是 Bot 目前的引擎與思考強度（規則的判斷），例：hermes_medium。
func (s *Service) PreCheck(ctx context.Context, text, botID, ref, engine string) []Chip {
	chips := make([]*Chip, len(PreCheckUses))
	var wg sync.WaitGroup
	for i, id := range PreCheckUses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := Ask{Use: id, Text: text, BotID: botID, Ref: ref}
			if id == UsePreEngine && engine != "" {
				a.Rule = &Answer{Label: engine, Why: "照 Bot 的預設引擎"}
			}
			d, err := s.Decide(ctx, a)
			if err != nil || !d.Live() {
				return
			}
			u, _ := UseByID(id)
			c := &Chip{Use: id, N: u.N, Label: d.Label, Shown: d.Shown, P: d.P, Why: d.Why, By: d.By, Options: []ChipOption{}}
			for _, o := range u.Options {
				if o.Key != d.Label {
					c.Options = append(c.Options, ChipOption{Key: o.Key, Shown: o.Shown})
				}
			}
			chips[i] = c
		}()
	}
	wg.Wait()
	out := []Chip{}
	for _, c := range chips {
		if c != nil {
			out = append(out, *c)
		}
	}
	return out
}
