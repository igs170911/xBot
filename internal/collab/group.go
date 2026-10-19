package collab

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── Bot 改群組（xbot.collab.update_group）────────────────
//
// Bot 說過「我沒有改群組名稱的工具」「沒有移除群組成員的功能」（使用者回報 2026-10-19）。
// Bot 可以改它所在的群組：名稱、公告、回應規則、主責、加減成員，或自己離開；有團隊管理權限的 Bot 可以改任何群組。
// 用和你在網頁上改群組同一段程式（UpdateRoom），人數限制一樣是 limits.RoomMembersMin～RoomMembersMax。
// 風險都是 safe：都可以復原（你在「編輯群組」改回來），也不給 Bot 新的能力——
// 加隊友進群組和 xbot.collab.start_group 拉隊友開新群組一樣（隊友本來就能互相傳話），
// 新成員讀得到的群組訊息，呼叫的成員自己也讀得到、可以轉述。刪群組才要你按允許（xbot.collab.remove_group）。
// 每次改動都在群組貼一行系統提示，你看得到是誰改了什麼。

// GroupChange 是 Bot 要改的群組設定；nil 或空白的欄位不改。
type GroupChange struct {
	Name     *string  // 空字串＝改回用成員名字自動命名
	Bulletin *string  // 空字串＝清掉公告
	Rule     *string  // 回應規則
	Lead     string   // 新的主責（成員名字或 id）
	Add      []string // 加入的隊友（名字或 id）
	Remove   []string // 移出的成員（名字或 id）
	Leave    bool     // 自己離開
}

// GroupUpdateResult 是 Bot 改群組的結果。
type GroupUpdateResult struct {
	Group   string   `json:"group"`
	GroupID string   `json:"group_id"`
	Members []string `json:"members" desc:"改完之後的成員"`
	Lead    string   `json:"lead" desc:"主責"`
	Changed []string `json:"changed" desc:"改了什麼"`
	Left    bool     `json:"left,omitempty" desc:"你已經離開這個群組"`
	Note    string   `json:"note,omitempty"`
}

// groupFor 找 Bot 能改的群組：它所在的群組（可以用名稱）；有團隊管理權限時任何群組都可以。回傳它是不是成員。
func (s *Service) groupFor(ctx context.Context, botID, ref string) (store.Room, bool, error) {
	r, err := s.FindRoom(ctx, ref, botID)
	if err == nil {
		return r, true, nil
	}
	me, gerr := s.db.GetBot(ctx, botID)
	if gerr != nil || !me.CanManage {
		return r, false, err
	}
	if other, oerr := s.FindRoom(ctx, ref, ""); oerr == nil {
		return other, false, nil
	}
	return r, false, &Error{Code: CodeNotFound, Msg: fmt.Sprintf("找不到群組「%s」", strings.TrimSpace(ref))}
}

// pickBot 用 id 或名字（完全相同、不分大小寫）在 bots 裡找唯一的一位。
func pickBot(bots []store.Bot, ref string) (store.Bot, bool) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
	lower := strings.ToLower(ref)
	for _, test := range []func(store.Bot) bool{
		func(b store.Bot) bool { return b.ID == ref },
		func(b store.Bot) bool { return b.Name == ref },
		func(b store.Bot) bool { return strings.ToLower(b.Name) == lower },
	} {
		var hits []store.Bot
		for _, b := range bots {
			if test(b) {
				hits = append(hits, b)
			}
		}
		if len(hits) == 1 {
			return hits[0], true
		}
	}
	return store.Bot{}, false
}

func botNames(bots []store.Bot) []string {
	out := []string{}
	for _, b := range bots {
		out = append(out, b.Name)
	}
	return out
}

// UpdateGroup 是 Bot 改群組。dryRun 時只檢查、回傳會改什麼。
func (s *Service) UpdateGroup(ctx context.Context, botID, ref string, c GroupChange, dryRun bool) (GroupUpdateResult, error) {
	room, member, err := s.groupFor(ctx, botID, ref)
	if err != nil {
		return GroupUpdateResult{}, err
	}
	me, err := s.db.GetBot(ctx, botID)
	if err != nil {
		return GroupUpdateResult{}, err
	}
	bots, _, err := s.members(ctx, room.ID)
	if err != nil {
		return GroupUpdateResult{}, err
	}
	ids := make([]string, 0, len(bots))
	byID := map[string]store.Bot{}
	for _, b := range bots {
		ids = append(ids, b.ID)
		byID[b.ID] = b
	}
	next := slices.Clone(ids)
	var changed, notes []string
	membersChanged := false

	if c.Leave {
		if !member {
			return GroupUpdateResult{}, invalid("你不在這個群組，不能離開")
		}
		next = slices.DeleteFunc(next, func(id string) bool { return id == botID })
		changed, notes = append(changed, "離開"), append(notes, "離開了群組")
		membersChanged = true
	}
	var removed []string
	for _, r := range c.Remove {
		b, ok := pickBot(bots, r)
		if !ok {
			return GroupUpdateResult{}, &Error{Code: CodeNotFound, Msg: fmt.Sprintf("「%s」不在這個群組。成員：%s", strings.TrimSpace(r), strings.Join(botNames(bots), "、"))}
		}
		if !slices.Contains(next, b.ID) {
			continue
		}
		next = slices.DeleteFunc(next, func(id string) bool { return id == b.ID })
		removed = append(removed, b.Name)
	}
	if len(removed) > 0 {
		changed, notes = append(changed, "移出成員"), append(notes, "把 "+strings.Join(removed, "、")+" 移出群組")
		membersChanged = true
	}
	var added []string
	for _, a := range c.Add {
		id := botID // 有團隊管理權限、不在群組裡的 Bot 可以把自己加進去
		if _, self := pickBot([]store.Bot{me}, a); !self {
			m, err := s.Resolve(ctx, a, botID)
			if err != nil {
				return GroupUpdateResult{}, err
			}
			id = m.ID
		}
		if slices.Contains(next, id) {
			continue
		}
		b, err := s.db.GetBot(ctx, id)
		if err != nil {
			return GroupUpdateResult{}, err
		}
		next = append(next, b.ID)
		byID[b.ID] = b
		added = append(added, b.Name)
	}
	if len(added) > 0 {
		changed, notes = append(changed, "加入成員"), append(notes, "把 "+strings.Join(added, "、")+" 加進群組")
		membersChanged = true
	}
	if membersChanged {
		switch {
		case len(next) < limits.RoomMembersMin && c.Leave:
			return GroupUpdateResult{}, invalid(fmt.Sprintf("群組至少要 %d 位成員，你離開後就不能用了。要刪掉群組請用 xbot.collab.remove_group（要使用者按允許）", limits.RoomMembersMin))
		case len(next) < limits.RoomMembersMin:
			return GroupUpdateResult{}, invalid(fmt.Sprintf("群組至少要 %d 位成員。要刪掉群組請用 xbot.collab.remove_group（要使用者按允許）", limits.RoomMembersMin))
		case len(next) > limits.RoomMembersMax:
			return GroupUpdateResult{}, &Error{Code: CodeLimit, Msg: fmt.Sprintf("群組最多 %d 位 Bot", limits.RoomMembersMax)}
		}
	}
	var lead *string
	if strings.TrimSpace(c.Lead) != "" {
		cands := make([]store.Bot, 0, len(next))
		for _, id := range next {
			cands = append(cands, byID[id])
		}
		b, ok := pickBot(cands, c.Lead)
		if !ok {
			return GroupUpdateResult{}, invalid(fmt.Sprintf("主責要是群組成員：「%s」不在群組裡", strings.TrimSpace(c.Lead)))
		}
		if b.ID != room.LeadID {
			lead = &b.ID
			changed, notes = append(changed, "主責"), append(notes, "把主責換成 "+b.Name)
		}
	}
	if c.Name != nil {
		if n := plain(*c.Name, limits.RoomNameMax); n != "" {
			if n != room.Name || room.AutoName {
				changed, notes = append(changed, "名稱"), append(notes, "把群組名稱改成「"+n+"」")
			}
		} else if !room.AutoName {
			changed, notes = append(changed, "名稱"), append(notes, "把群組名稱改回用成員名字")
		}
	}
	if c.Bulletin != nil {
		b := strings.TrimSpace(*c.Bulletin)
		if runes(b) > limits.RoomBulletinMax {
			return GroupUpdateResult{}, invalid(fmt.Sprintf("群組公告最多 %d 字", limits.RoomBulletinMax))
		}
		if b != room.Bulletin {
			changed = append(changed, "公告")
			if b == "" {
				notes = append(notes, "清掉了群組公告")
			} else {
				notes = append(notes, "更新了群組公告")
			}
		}
	}
	if c.Rule != nil && *c.Rule != room.ResponderRule {
		if !slices.Contains(Rules, *c.Rule) {
			return GroupUpdateResult{}, invalid("回應規則只能是 " + strings.Join(Rules, "、"))
		}
		changed, notes = append(changed, "回應規則"), append(notes, "把沒點名時誰回改成「"+ruleNames[*c.Rule]+"」")
	}
	if len(changed) == 0 {
		return GroupUpdateResult{}, invalid("沒有要改的（沒有給欄位，或給的和現在一樣）。可以改：name（名稱）、bulletin（公告）、rule（回應規則）、lead（主責）、add（加入）、remove（移出），或 leave（離開）")
	}

	out := GroupUpdateResult{Group: room.Name, GroupID: room.ID, Changed: changed, Left: c.Leave}
	if dryRun {
		for _, id := range next {
			out.Members = append(out.Members, byID[id].Name)
		}
		out.Note = "會" + strings.Join(notes, "、")
		return out, nil
	}
	u := RoomUpdate{Name: c.Name, Bulletin: c.Bulletin, ResponderRule: c.Rule, LeadID: lead}
	if membersChanged {
		u.MemberIDs = next
		if c.Leave {
			_, u.Keep = s.rt.Active(botID) // 正在做的這一輪讓它做完（最後回答照樣貼到群組）
		}
	}
	updated, err := s.UpdateRoom(ctx, room.ID, u)
	if err != nil {
		return GroupUpdateResult{}, err
	}
	summary := me.Name + " " + strings.Join(notes, "、")
	if _, err := s.addRoomMessage(ctx, room.ID, "system", summary, roomMeta{Notice: "updated"}); err != nil {
		s.log.Warn("寫入群組系統提示失敗", "room", room.ID, "err", err)
	}
	thread, taskID := s.rt.Active(botID)
	s.note(ctx, botID, thread, taskID, "改了群組「"+updated.Name+"」："+strings.Join(changed, "、"), PeerEvent{Event: "group_updated", RoomID: room.ID, PeerName: updated.Name})
	after, _, err := s.members(ctx, room.ID)
	if err != nil {
		return GroupUpdateResult{}, err
	}
	out.Group, out.Members = updated.Name, botNames(after)
	for _, b := range after {
		if b.ID == updated.LeadID {
			out.Lead = b.Name
		}
	}
	if c.Leave {
		out.Note = "你已經離開群組「" + updated.Name + "」，之後不會再收到它的訊息。"
	}
	return out, nil
}

// ruleNames 是回應規則給人看的名字（群組系統提示用）。
var ruleNames = map[string]string{
	store.RuleAuto:      "自動挑一位",
	store.RuleLead:      "主責",
	store.RuleMentioned: "只回應被點名的",
	store.RuleEveryone:  "所有成員",
}

// UpdateGroupIn 是 xbot.collab.update_group 的輸入：有給的欄位才改。
type UpdateGroupIn struct {
	Group    string   `json:"group" required:"true" max:"100" desc:"群組名稱或 id（你所在的群組；有團隊管理權限時任何群組都可以）"`
	Name     *string  `json:"name,omitempty" max:"60" desc:"新的群組名稱；空白字串表示改回用成員名字自動命名"`
	Bulletin *string  `json:"bulletin,omitempty" max:"4000" desc:"新的群組公告（每位成員都會看到的共同指示）；空白字串表示清掉"`
	Rule     *string  `json:"rule,omitempty" enum:"auto,lead,mentioned,everyone" desc:"沒點名時誰回：auto（自動挑一位）、lead（主責）、mentioned（只回應被點名的）、everyone（所有成員）"`
	Lead     string   `json:"lead,omitempty" max:"100" desc:"新的主責（成員名字）"`
	Add      []string `json:"add,omitempty" max:"9" desc:"要加進群組的隊友（名字）"`
	Remove   []string `json:"remove,omitempty" max:"9" desc:"要移出群組的成員（名字）"`
	Leave    bool     `json:"leave,omitempty" desc:"true：你自己離開這個群組"`
}

func registerGroupUpdate(reg *ops.Registry, s *Service) {
	ops.Register(reg, ops.Op[UpdateGroupIn, GroupUpdateResult]{
		Name: "xbot.collab.update_group", Summary: "改你所在的群組：名稱、公告、主責、成員，或離開",
		Description: fmt.Sprintf("只改有給的欄位。群組要有 %d 到 %d 位 Bot；剩不到 %d 位的話不能再移出或離開（要刪群組用 xbot.collab.remove_group）。"+
			"有團隊管理權限的 Bot 可以改任何群組。每次改動都會在群組貼一行提示。你本人請用 xbot.rooms.update。",
			limits.RoomMembersMin, limits.RoomMembersMax, limits.RoomMembersMin),
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateGroupIn) (GroupUpdateResult, error) {
			if err := ready(s); err != nil {
				return GroupUpdateResult{}, err
			}
			if call.Identity.Kind != ops.KindBot {
				return GroupUpdateResult{}, ops.Forbidden("這是給 Bot 用的；你本人請用 xbot.rooms.update")
			}
			out, err := s.UpdateGroup(ctx, call.Identity.ID, in.Group, GroupChange{Name: in.Name, Bulletin: in.Bulletin, Rule: in.Rule, Lead: in.Lead,
				Add: in.Add, Remove: in.Remove, Leave: in.Leave}, call.DryRun)
			if out.Members == nil {
				out.Members = []string{}
			}
			if out.Changed == nil {
				out.Changed = []string{}
			}
			return out, opsErr(err)
		},
	})
}
