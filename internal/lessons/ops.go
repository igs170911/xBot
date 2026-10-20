package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ListIn 是 xbot.lessons.list 的輸入。
type ListIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"也列出這個 Bot 自己的；不填只列給所有 Bot 的"`
}

// ListOut 是 xbot.lessons.list 的輸出。
type ListOut struct {
	All []Lesson `json:"all" desc:"給所有 Bot 的"`
	Bot []Lesson `json:"bot" desc:"只給這個 Bot 的"`
	Max int      `json:"max" desc:"每一組最多幾條"`
}

// AddIn 是 xbot.lessons.add 的輸入。
type AddIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"加給這個 Bot；不填是加給所有 Bot"`
	Text  string `json:"text" required:"true" max:"200" desc:"一句守則（80 字內）"`
}

// DeleteIn 是 xbot.lessons.delete 的輸入。
type DeleteIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"這個 Bot 的；不填是給所有 Bot 的"`
	ID    string `json:"id" required:"true" max:"64"`
}

// Apply 是怎麼處理一張學到一件事的卡。
type Apply string

// EnumValues 讓 JSON Schema 列出可用值。
func (Apply) EnumValues() []string { return []string{"bot", "all", "skill", "no"} }

// DecideIn 是 xbot.lessons.decide 的輸入。
type DecideIn struct {
	InboxID string `json:"inbox_id" required:"true" max:"64" desc:"學到一件事的卡（kind=lesson）"`
	Apply   Apply  `json:"apply" required:"true" desc:"bot：加給這個 Bot；all：加給所有 Bot；skill：加進那個技能（卡片有技能時）；no：不要"`
	Text    string `json:"text,omitempty" max:"200" desc:"改過的字；不填照卡片上的"`
}

// DecideOut 是 xbot.lessons.decide 的輸出。
type DecideOut struct {
	Lesson *Lesson `json:"lesson,omitempty"`
	Skill  string  `json:"skill,omitempty" desc:"加進了哪個技能"`
}

// StatsIn 是 xbot.skills.stats 的輸入。
type StatsIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64"`
}

// StatsOut 是 xbot.skills.stats 的輸出。
type StatsOut struct {
	Skills []SkillStat `json:"skills" desc:"最近 30 天用過的技能，最近用的在前"`
}

// Register 登錄 xbot.lessons.* 與 xbot.skills.stats。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	need := func() error {
		if s == nil {
			return ops.Internal(errors.New("學到的事服務沒有啟動"))
		}
		return nil
	}
	owner := func(call *ops.Call) error {
		if call.Identity.Kind != ops.KindOwner {
			return ops.Forbidden("只有你本人能決定 Bot 的守則")
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.lessons.list", Summary: "列出 Bot 從失敗學到的事（寫在守則裡的）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			out := ListOut{All: []Lesson{}, Bot: []Lesson{}, Max: limits.LessonsMax}
			all, err := s.list(ctx, "")
			if err != nil {
				return out, err
			}
			out.All = append(out.All, all...)
			if in.BotID != "" {
				mine, err := s.list(ctx, in.BotID)
				if err != nil {
					return out, err
				}
				out.Bot = append(out.Bot, mine...)
			}
			return out, nil
		},
	})
	ops.Register(reg, ops.Op[AddIn, Lesson]{
		Name: "xbot.lessons.add", Summary: "幫 Bot 加一條守則（學到的事）",
		Description: "加進 Bot 的守則（SOUL.md 的「學到的事」），下一段新對話開始生效。不填 bot_id 是加給所有 Bot。只有你本人能加。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in AddIn) (Lesson, error) {
			if err := need(); err != nil {
				return Lesson{}, err
			}
			if err := owner(call); err != nil {
				return Lesson{}, err
			}
			if call.DryRun {
				return Lesson{Text: oneLine(in.Text), BotID: in.BotID}, nil
			}
			l, err := s.Add(ctx, in.BotID, in.Text, "")
			return l, asOps(err)
		},
	})
	ops.Register(reg, ops.Op[DeleteIn, struct {
		Deleted bool `json:"deleted"`
	}]{
		Name: "xbot.lessons.delete", Summary: "刪掉一條學到的事",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DeleteIn) (struct {
			Deleted bool `json:"deleted"`
		}, error) {
			var out struct {
				Deleted bool `json:"deleted"`
			}
			if err := need(); err != nil {
				return out, err
			}
			if err := owner(call); err != nil {
				return out, err
			}
			if call.DryRun {
				return out, nil
			}
			ok, err := s.Delete(ctx, in.BotID, in.ID)
			if err == nil && !ok {
				return out, ops.NotFound("找不到這一條")
			}
			out.Deleted = ok
			return out, err
		},
	})
	ops.Register(reg, ops.Op[DecideIn, DecideOut]{
		Name: "xbot.lessons.decide", Summary: "處理一張「學到一件事」的卡：加給這個 Bot、所有 Bot、加進技能，或不要",
		Description: "老師模型看了沒做完的工作，提議的一條守則或技能注意事項。可以先改字。只有你本人能決定。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DecideIn) (DecideOut, error) {
			if err := need(); err != nil {
				return DecideOut{}, err
			}
			if err := owner(call); err != nil {
				return DecideOut{}, err
			}
			return s.decide(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[StatsIn, StatsOut]{
		Name: "xbot.skills.stats", Summary: "看一個 Bot 的技能最近用了幾次、幾次沒做完",
		Description: "最近 30 天，每件用了技能的工作看那一串最後的完成判定：做完（含該停下來等你的）或沒做完（只說要做、交回給你、卡住、出錯）。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StatsIn) (StatsOut, error) {
			if err := need(); err != nil {
				return StatsOut{}, err
			}
			st, err := s.SkillStats(ctx, in.BotID)
			if st == nil {
				st = []SkillStat{}
			}
			return StatsOut{Skills: st}, err
		},
	})
}

func asOps(err error) error {
	switch {
	case errors.Is(err, ErrFull):
		return ops.Conflict(err.Error())
	case errors.Is(err, ErrText):
		return ops.InvalidInput(ops.FieldError{Field: "text", Message: err.Error()})
	case err != nil && strings.Contains(err.Error(), "已經有"):
		return ops.Conflict(err.Error())
	}
	return err
}

func (s *Service) decide(ctx context.Context, reg *ops.Registry, call *ops.Call, in DecideIn) (DecideOut, error) {
	it, err := call.DB.GetInboxItem(ctx, in.InboxID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && it.Kind != CardKind) {
		return DecideOut{}, ops.NotFound("找不到這張卡")
	} else if err != nil {
		return DecideOut{}, err
	}
	if it.Status != store.InboxOpen {
		return DecideOut{}, ops.Conflict("這張卡已經處理過了")
	}
	var p cardPayload
	_ = json.Unmarshal(it.Payload, &p)
	text := oneLine(in.Text)
	if text == "" {
		text = p.Lesson
	}
	if utf8.RuneCountInString(text) > limits.LessonTextMax {
		return DecideOut{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: ErrText.Error()})
	}
	if in.Apply == "skill" && p.Skill == "" {
		return DecideOut{}, ops.InvalidInput(ops.FieldError{Field: "apply", Message: "這張卡沒有對應的技能"})
	}
	if call.DryRun {
		return DecideOut{}, nil
	}
	out := DecideOut{}
	resolution := "rejected"
	switch in.Apply {
	case "bot", "all":
		botID := it.BotID
		if in.Apply == "all" {
			botID = ""
		}
		l, err := s.Add(ctx, botID, text, it.TaskID)
		if err != nil {
			return DecideOut{}, asOps(err)
		}
		out.Lesson, resolution = &l, "applied_"+string(in.Apply)
	case "skill":
		if err := s.noteSkill(ctx, reg, call, it.BotID, p.Skill, text); err != nil {
			return DecideOut{}, err
		}
		out.Skill, resolution = p.Skill, "applied_skill"
	}
	if err := call.DB.SettleInboxItem(ctx, it.ID, store.InboxResolved, resolution, call.Identity.Actor(), nil); err != nil && !errors.Is(err, store.ErrAlreadyDecided) {
		return out, err
	}
	if cur, err := call.DB.GetInboxItem(ctx, it.ID); err == nil {
		if e, err := call.DB.AppendEvent(ctx, notify.Event(notify.KindUpdated, cur)); err == nil {
			reg.Notify(e)
		}
	}
	return out, nil
}

// skillNoteHead 是技能最後放學到的提醒的段落。
const skillNoteHead = "## 注意（從沒做完的工作學到的）"

// noteSkill 把一句提醒加進 Bot 的技能最後的「注意」段落（經 xbot.skills.get／update，MCP 優先）。
func (s *Service) noteSkill(ctx context.Context, reg *ops.Registry, call *ops.Call, botID, name, text string) error {
	raw, _ := json.Marshal(map[string]string{"bot_id": botID, "id": name})
	res, err := reg.Invoke(ctx, call.Identity, "xbot.skills.get", raw, ops.Options{})
	if err != nil {
		return err
	}
	var sk struct {
		ID           string `json:"id"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(res.Output, &sk); err != nil || sk.ID == "" {
		return ops.NotFound("找不到技能「" + name + "」")
	}
	body := strings.TrimRight(sk.Instructions, "\n")
	if strings.Contains(body, "- "+text) {
		return nil
	}
	if !strings.Contains(body, skillNoteHead) {
		body += "\n\n" + skillNoteHead + "\n"
	}
	body += "\n- " + text + "\n"
	raw, _ = json.Marshal(map[string]string{"bot_id": botID, "id": sk.ID, "instructions": body})
	_, err = reg.Invoke(ctx, call.Identity, "xbot.skills.update", raw, ops.Options{})
	return err
}
