package org

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"xbot/internal/bots"
	"xbot/internal/collab"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Member 是團隊裡的一位：既有的 Bot（Bot 填 id 或名字），或要新建的 Bot（Name 等欄位）。
type Member struct {
	Bot     string `json:"bot,omitempty" max:"100" desc:"既有 Bot 的 id 或名字（加入既有的 Bot 時填）"`
	Name    string `json:"name,omitempty" max:"100" desc:"新 Bot 的名字（新建時填）"`
	Title   string `json:"title,omitempty" max:"100" desc:"新 Bot 的職稱"`
	Persona string `json:"persona,omitempty" max:"4000" desc:"新 Bot 的人設（常駐指示）"`
	Color   string `json:"color,omitempty" max:"20" desc:"頭像顏色；不填自動挑"`
	Shape   string `json:"shape,omitempty" max:"20" desc:"頭像形狀；不填自動挑"`
}

func (m Member) existing() bool { return strings.TrimSpace(m.Bot) != "" }
func (m Member) empty() bool {
	return strings.TrimSpace(m.Bot) == "" && strings.TrimSpace(m.Name) == ""
}

// TeamSpec 是要建立的團隊（LazyBoy create_team）。
type TeamSpec struct {
	Name      string
	About     string
	Lead      Member
	Members   []Member
	ReportsTo string // 主管向誰報告：Bot 的 id 或名字；空白＝你
	GroupChat bool   // 同時開一個同名群組（主管＋成員）
	Template  string
	// LeadManage：主管拿到團隊管理權限（公司範本的執行長）。
	LeadManage bool
	// Creator 是建立團隊的 Bot（主管 Bot 替你建時；算進它的每日建立數）；空白是你本人或代理。
	Creator string
	Actor   string // 寫進 created_by／updated_by
}

// TeamResult 是建好的團隊。
type TeamResult struct {
	Team      store.Team
	LeadID    string
	MemberIDs []string
	Created   []string // 新建的 Bot id
	RoomID    string
	Note      string
}

// CreateTeam 一次建好團隊：分區、主管與成員（新建或既有的 Bot）、每位成員向主管報告、可以順便開群組。
// 全部檢查完才寫入，有一位不對就什麼都不留（LazyBoy create_team）。
func (s *Service) CreateTeam(ctx context.Context, sp TeamSpec) (TeamResult, error) {
	name := collab.Plain(sp.Name, limits.TeamNameMax)
	if name == "" {
		return TeamResult{}, bad("請替團隊取名字")
	}
	if sp.Lead.empty() {
		return TeamResult{}, bad("請指定團隊主管")
	}
	for _, m := range sp.Members {
		if m.empty() {
			return TeamResult{}, bad("每位成員都要寫成名字，或是含名字的設定")
		}
	}
	if len(sp.Members) == 0 {
		return TeamResult{}, bad("團隊至少要有一位成員")
	}
	if len(sp.Members)+1 > limits.TeamMax {
		return TeamResult{}, badf("一個團隊最多 %d 位（含主管）", limits.TeamMax)
	}
	r, err := s.roster(ctx)
	if err != nil {
		return TeamResult{}, err
	}
	all := append([]Member{sp.Lead}, sp.Members...)
	fresh := 0
	for _, m := range all {
		if !m.existing() {
			fresh++
		}
	}
	var creator *store.Bot
	if sp.Creator != "" {
		sp.Actor = "bot:" + sp.Creator // 新建的 Bot 記在它名下，才算得到它每天建了幾個
		c, ok := r.byID[sp.Creator]
		if !ok {
			return TeamResult{}, ops.NotFound("找不到建立團隊的 Bot")
		}
		creator = &c
	}
	if err := bots.CheckQuota(ctx, s.db, creator, fresh); err != nil {
		return TeamResult{}, err
	}
	taken := map[string]bool{}
	for _, b := range r.list {
		taken[b.Name] = true
	}
	planned := map[string]bool{}
	for _, m := range all {
		if m.existing() {
			continue
		}
		label := collab.Plain(m.Name, limits.BotCreateNameMax)
		if label == "" {
			return TeamResult{}, bad("每位成員都要有名字")
		}
		if taken[label] || planned[label] {
			return TeamResult{}, badf("已經有叫「%s」的 Bot，請換一個名字，或直接用名字加入既有的 Bot", label)
		}
		planned[label] = true
	}
	manager := ""
	if !userWords[strings.TrimSpace(sp.ReportsTo)] {
		m, err := r.find(sp.ReportsTo)
		if err != nil {
			return TeamResult{}, ops.NotFound("找不到團隊要報告的對象「" + sp.ReportsTo + "」")
		}
		manager = m.ID
	}
	// 既有的主管與成員
	ids := make([]string, len(all))
	for i, m := range all {
		if !m.existing() {
			continue
		}
		b, err := r.find(m.Bot)
		if err != nil {
			return TeamResult{}, err
		}
		ids[i] = b.ID
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if seen[id] {
			return TeamResult{}, bad("同一個 Bot 不能在團隊裡出現兩次，主管也不能同時是成員")
		}
		seen[id] = true
	}
	leadID := ids[0]
	if leadID != "" && manager != "" {
		if _, err := r.checkManager(leadID, manager); err != nil {
			return TeamResult{}, err
		}
	}
	// 改完之後的組織圖：主管在 manager 底下、既有成員在主管底下。原本沒有循環、只有這些線會變，
	// 所以有循環的話一定經過主管：從 manager 往上走，碰到主管或成員就是繞回來了。
	boss := map[string]string{}
	for _, b := range r.list {
		boss[b.ID] = b.ReportsTo
	}
	head := leadID
	if head == "" {
		head = "\x00new-lead"
	}
	boss[head] = manager
	for _, id := range ids[1:] {
		if id != "" {
			boss[id] = head
		}
	}
	visited := map[string]bool{}
	for at := manager; at != "" && !visited[at]; at = boss[at] {
		if at == head || slices.Contains(ids[1:], at) {
			return TeamResult{}, bad("這樣安排會讓報告關係繞成一圈，請換一位團隊的上級")
		}
		visited[at] = true
	}
	teams, err := s.db.ListTeams(ctx)
	if err != nil {
		return TeamResult{}, err
	}
	name = uniqueTeamName(teams, name)
	if sp.GroupChat && s.o.Collab == nil {
		sp.GroupChat = false
	}

	// 寫入
	res := TeamResult{}
	var team store.Team
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		team, err = q.CreateTeam(ctx, store.Team{Name: name, About: strings.TrimSpace(sp.About), Template: sp.Template, CreatedBy: sp.Actor})
		if err != nil {
			return err
		}
		count := len(r.list)
		place := func(m Member, found, reportsTo string) (string, error) {
			if found != "" {
				return found, q.SetBotOrg(ctx, found, team.ID, reportsTo, sp.Actor)
			}
			b, err := newBot(ctx, q, bots.Spec{Name: m.Name, Title: m.Title, Persona: m.Persona, Color: m.Color, Shape: m.Shape,
				TeamID: team.ID, ReportsTo: reportsTo}, count, sp.Actor)
			if err != nil {
				return "", err
			}
			count++
			res.Created = append(res.Created, b.ID)
			return b.ID, nil
		}
		lead, err := place(sp.Lead, leadID, manager)
		if err != nil {
			return err
		}
		res.LeadID = lead
		if sp.LeadManage {
			if err := q.SetBotCanManage(ctx, lead, true, sp.Actor); err != nil {
				return err
			}
		}
		for i, m := range sp.Members {
			id, err := place(m, ids[i+1], lead)
			if err != nil {
				return err
			}
			res.MemberIDs = append(res.MemberIDs, id)
		}
		team.ChiefID = lead
		team, err = q.UpdateTeam(ctx, team)
		return err
	})
	if err != nil {
		return TeamResult{}, err
	}
	if sp.GroupChat {
		createdBy := sp.Actor
		room, err := s.o.Collab.CreateRoom(ctx, append([]string{res.LeadID}, res.MemberIDs...),
			collab.RoomOptions{Name: name, ResponderRule: store.RuleLead, LeadID: res.LeadID, CreatedBy: createdBy})
		if err != nil {
			s.log.Warn("建團隊時開群組失敗", "team", team.ID, "err", err)
		} else {
			team.RoomID = room.ID
			if team, err = s.db.UpdateTeam(ctx, team); err != nil {
				return TeamResult{}, err
			}
			res.RoomID = room.ID
		}
	}
	res.Team = team
	if sp.LeadManage && s.o.ManageChanged != nil {
		s.o.ManageChanged(ctx, res.LeadID)
	}
	lead, _ := s.db.GetBot(ctx, res.LeadID)
	res.Note = fmt.Sprintf("已建立「%s」：%s 帶 %d 位成員", name, lead.Name, len(res.MemberIDs))
	if res.RoomID != "" {
		res.Note += "，並開了同名群組對話"
	}
	res.Note += "。可以用 xbot.collab.message_bot 交辦第一件事給主管。"
	if sp.Creator != "" {
		s.note(ctx, sp.Creator, "team_created", name, map[string]any{"peer_id": res.LeadID, "peer_name": name, "room_id": res.RoomID})
	}
	s.emit("", KindTeamUpdated, map[string]string{"team_id": team.ID})
	return res, nil
}

// uniqueTeamName 在同名的團隊已存在時加上「（2）」「（3）」…（F02 例外狀況）。
func uniqueTeamName(teams []store.Team, name string) string {
	used := map[string]bool{}
	for _, t := range teams {
		used[t.Name] = true
	}
	if !used[name] {
		return name
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s（%d）", name, i)
		if !used[n] {
			return n
		}
	}
}

// TeamUpdate 是要改的團隊設定；nil 的欄位不改。
type TeamUpdate struct {
	Name    *string
	About   *string
	ChiefID *string
	Memory  []string // 不是 nil 時整份換掉
	Add     []string // 加入的 Bot id
	Remove  []string // 移出的 Bot id（回到未分區）
	Actor   string
}

// UpdateTeam 改團隊：名稱、說明、主管、團隊記憶、加入或移出成員。
// 加入的成員改向主管報告；移出的成員原本向主管報告的改成直接向你報告。換主管時，向舊主管報告的成員改向新主管報告。
func (s *Service) UpdateTeam(ctx context.Context, id string, u TeamUpdate) (store.Team, error) {
	t, err := s.db.GetTeam(ctx, id)
	if isNotFound(err) {
		return t, ops.NotFound("找不到這個團隊")
	}
	if err != nil {
		return t, err
	}
	r, err := s.roster(ctx)
	if err != nil {
		return t, err
	}
	if u.Name != nil {
		n := collab.Plain(*u.Name, limits.TeamNameMax)
		if n == "" {
			return t, bad("請替團隊取名字")
		}
		if n != t.Name {
			if other, err := s.db.FindTeamByName(ctx, n); err == nil && other.ID != t.ID {
				return t, badf("已經有叫「%s」的團隊", n)
			}
		}
		t.Name = n
	}
	if u.About != nil {
		t.About = strings.TrimSpace(*u.About)
	}
	if u.Memory != nil {
		var mem []string
		for _, m := range u.Memory {
			if m = strings.TrimSpace(m); m != "" {
				mem = append(mem, m)
			}
		}
		t.Memory = mem
	}
	members := map[string]bool{}
	for _, b := range r.list {
		if b.TeamID == t.ID {
			members[b.ID] = true
		}
	}
	for _, a := range u.Add {
		if _, ok := r.byID[a]; !ok {
			return t, ops.NotFound("找不到 Bot " + a)
		}
		members[a] = true
	}
	for _, x := range u.Remove {
		delete(members, x)
	}
	if len(members) > limits.TeamMax {
		return t, badf("一個團隊最多 %d 位（含主管）", limits.TeamMax)
	}
	oldChief := t.ChiefID
	if u.ChiefID != nil {
		c := strings.TrimSpace(*u.ChiefID)
		if c != "" && !members[c] {
			return t, bad("主管要是這個團隊的成員")
		}
		t.ChiefID = c
	}
	if t.ChiefID != "" && !members[t.ChiefID] {
		t.ChiefID = "" // 主管被移出了
	}
	// 新的報告線：先算好、檢查沒有循環，再一起寫。
	boss := map[string]string{}
	for _, b := range r.list {
		boss[b.ID] = b.ReportsTo
	}
	team := map[string]string{}
	for _, x := range u.Remove {
		if r.byID[x].TeamID == t.ID {
			team[x] = ""
			if boss[x] == oldChief || boss[x] == t.ChiefID {
				boss[x] = ""
			}
		}
	}
	if t.ChiefID != oldChief && t.ChiefID != "" {
		if boss[t.ChiefID] == oldChief {
			boss[t.ChiefID] = r.byID[oldChief].ReportsTo
		}
		for id := range members {
			if id != t.ChiefID && (boss[id] == oldChief || boss[id] == "") {
				boss[id] = t.ChiefID
			}
		}
	}
	for _, a := range u.Add {
		team[a] = t.ID
		if a != t.ChiefID && t.ChiefID != "" {
			boss[a] = t.ChiefID
		}
	}
	if err := noLoops(boss); err != nil {
		return t, err
	}
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		for _, b := range r.list {
			tm, moved := team[b.ID]
			if !moved {
				tm = b.TeamID
			}
			if tm != b.TeamID || boss[b.ID] != b.ReportsTo {
				if err := q.SetBotOrg(ctx, b.ID, tm, boss[b.ID], u.Actor); err != nil {
					return err
				}
			}
		}
		var err error
		t, err = q.UpdateTeam(ctx, t)
		return err
	})
	if err != nil {
		return t, err
	}
	s.emit("", KindTeamUpdated, map[string]string{"team_id": t.ID})
	return t, nil
}

// noLoops 檢查報告線（id → 主管）沒有繞成一圈。
func noLoops(boss map[string]string) error {
	for start := range boss {
		seen := map[string]bool{}
		for at := start; at != ""; at = boss[at] {
			if seen[at] {
				return bad("這樣安排會讓報告關係繞成一圈，請換一位主管")
			}
			seen[at] = true
		}
	}
	return nil
}

// DeleteTeam 刪除團隊：成員回到未分區，Bot、報告線、群組都不受影響。回傳原本的成員數。
func (s *Service) DeleteTeam(ctx context.Context, id string) (int, error) {
	t, err := s.db.GetTeam(ctx, id)
	if isNotFound(err) {
		return 0, ops.NotFound("找不到這個團隊")
	}
	if err != nil {
		return 0, err
	}
	n, err := s.memberCount(ctx, t.ID)
	if err != nil {
		return 0, err
	}
	if err := s.db.DeleteTeam(ctx, t.ID); err != nil {
		return 0, err
	}
	s.emit("", KindTeamUpdated, map[string]string{"team_id": t.ID})
	return n, nil
}

func (s *Service) memberCount(ctx context.Context, teamID string) (int, error) {
	all, err := s.db.ListBots(ctx, true)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range all {
		if b.TeamID == teamID {
			n++
		}
	}
	return n, nil
}

// BotUpdate 是主管 Bot 要改的另一個 Bot 的欄位（LazyBoy update_bot）；空白不改。
type BotUpdate struct {
	Title     string
	Persona   string
	ReportsTo string // 「使用者」表示直接向你報告
	Team      string // 團隊名稱；「未分區」或 none 表示離開團隊；不存在的名稱會建立新的團隊
}

// RequireManager 確認 Bot 有團隊管理權限（LazyBoy require_manager）。
func (s *Service) RequireManager(ctx context.Context, botID string) (store.Bot, error) {
	b, err := s.db.GetBot(ctx, botID)
	if isNotFound(err) {
		return b, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return b, err
	}
	if !b.CanManage {
		return b, ops.Forbidden("你沒有團隊管理權限。請使用者到「Bot 設定 → 權限與花費」（或「組織架構」頁）開啟你的「團隊管理權限」，或請使用者自己在網頁上調整。")
	}
	return b, nil
}

// UpdateBot 是有團隊管理權限的 Bot 調整另一個 Bot 的職稱、人設、報告對象或團隊（不能刪除）。回傳改了哪些欄位。
func (s *Service) UpdateBot(ctx context.Context, actorID, ref string, u BotUpdate) (string, []string, error) {
	actor, err := s.RequireManager(ctx, actorID)
	if err != nil {
		return "", nil, err
	}
	r, err := s.roster(ctx)
	if err != nil {
		return "", nil, err
	}
	target, err := r.find(ref)
	if err != nil {
		return "", nil, err
	}
	var fields, labels []string
	if t := collab.Plain(u.Title, limits.BotProfileTitleMax); t != "" {
		target.Title = t
		fields, labels = append(fields, "title"), append(labels, "職稱")
	}
	if p := strings.TrimSpace(u.Persona); p != "" {
		if rs := []rune(p); len(rs) > limits.BotSoulMax {
			p = string(rs[:limits.BotSoulMax])
		}
		target.Soul, target.Description = p, collab.Plain(p, limits.BotDescriptionMax)
		fields, labels = append(fields, "persona"), append(labels, "人設")
	}
	if strings.TrimSpace(u.ReportsTo) != "" {
		boss, err := r.checkManager(target.ID, u.ReportsTo)
		if err != nil {
			return "", nil, err
		}
		target.ReportsTo = boss
		fields, labels = append(fields, "reports_to"), append(labels, "報告對象")
	}
	if tn := collab.Plain(u.Team, limits.TeamNameMax); tn != "" {
		switch tn {
		case "未分區", "none":
			target.TeamID = ""
		default:
			t, err := s.db.FindTeamByName(ctx, tn)
			if isNotFound(err) {
				t, err = s.db.CreateTeam(ctx, store.Team{Name: tn, CreatedBy: "bot:" + actorID})
			}
			if err != nil {
				return "", nil, err
			}
			if t.ID != target.TeamID {
				if n, err := s.memberCount(ctx, t.ID); err != nil {
					return "", nil, err
				} else if n >= limits.TeamMax {
					return "", nil, badf("「%s」已經有 %d 位（含主管），不能再加人", t.Name, limits.TeamMax)
				}
			}
			target.TeamID = t.ID
		}
		fields, labels = append(fields, "team"), append(labels, "團隊")
	}
	if len(fields) == 0 {
		return "", nil, bad("沒有要更新的欄位")
	}
	target.UpdatedBy = "bot:" + actorID
	if _, err := s.db.UpdateBot(ctx, target); err != nil {
		return "", nil, err
	}
	s.note(ctx, actor.ID, "profile_updated", fmt.Sprintf("調整了 %s 的%s", target.Name, strings.Join(labels, "、")), map[string]any{"peer_id": target.ID})
	s.emit("", KindTeamUpdated, map[string]string{"team_id": target.TeamID, "bot_id": target.ID})
	return target.Name, fields, nil
}
