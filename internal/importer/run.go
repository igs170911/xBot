package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"xbot/internal/bots"
	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/memory"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/skills"
	"xbot/internal/store"
)

// 匯入的狀態。
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
)

// 每一項的結果。
const (
	ResultOK      = "ok"
	ResultFailed  = "failed"
	ResultSkipped = "skipped"
)

// Result 是一項的結果。
type Result struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Status string `json:"status" desc:"ok、failed、skipped"`
	Detail string `json:"detail,omitempty"`
	Target string `json:"target,omitempty" desc:"在 XBot 建立的 id（Bot、定時任務、團隊、群組）"`
}

// Run 是一次匯入（xbot.import.run、xbot.import.status、事件 import.progress）。
type Run struct {
	ID       string   `json:"id"`
	SourceID string   `json:"source_id"`
	Label    string   `json:"label"`
	State    string   `json:"state" desc:"running、done、failed"`
	Step     string   `json:"step,omitempty" desc:"正在做什麼（給人看）"`
	Done     int      `json:"done"`
	Total    int      `json:"total"`
	Percent  int      `json:"percent"`
	Bytes    int64    `json:"bytes" desc:"已經複製的電腦資料"`
	Results  []Result `json:"results"`
	Bots     []string `json:"bots" desc:"這次建立的 Bot id"`
	Error    string   `json:"error,omitempty"`
	Started  int64    `json:"started_at"`
	Finished int64    `json:"finished_at,omitempty"`
}

// Rename 是 Bot 匯入後的名字。
type Rename struct {
	Key  string `json:"key" required:"true" max:"200" desc:"Bot 項目代號（bot:<舊 id>）"`
	Name string `json:"name" required:"true" max:"100"`
}

// RunIn 是 xbot.import.run 的輸入。
type RunIn struct {
	Source string   `json:"source,omitempty" max:"80" desc:"xbot.import.detect 的 id"`
	Path   string   `json:"path,omitempty" max:"1000" desc:"或直接給舊資料的資料夾"`
	Items  []string `json:"items" required:"true" max:"2000" desc:"要帶的項目代號（xbot.import.plan 的 items[].key）"`
	Names  []Rename `json:"names,omitempty" max:"200" desc:"改 Bot 的名字"`
	Wait   bool     `json:"wait,omitempty" desc:"等做完才回（指令列用）；不等時看 xbot.import.status 或 import.progress 事件"`
}

func (s *Service) snapshot(r *Run) Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *r
	c.Results = slices.Clone(r.Results)
	c.Bots = slices.Clone(r.Bots)
	return c
}

func (s *Service) update(r *Run, f func(r *Run)) {
	s.mu.Lock()
	f(r)
	s.mu.Unlock()
	if s.Publish != nil {
		s.Publish(KindProgress, s.snapshot(r))
	}
}

// Runs 回傳最近的匯入（新的在前）。
func (s *Service) Runs() []Run {
	s.mu.Lock()
	list := slices.Clone(s.runs)
	s.mu.Unlock()
	out := []Run{}
	for _, r := range list {
		out = append(out, s.snapshot(r))
	}
	return out
}

// job 是一次匯入要做的事。
type job struct {
	p      *plan
	sel    map[string]bool
	actor  string
	db     *store.DB
	run    *Run
	bots   map[string]string // 舊 Bot id → XBot Bot id（這次建立的與之前帶過的）
	fresh  map[string]bool   // 這次建立的舊 Bot id
	teams  map[string]string
	fs     map[string]boxfs.FS
	fsErr  map[string]error
	total  int64 // 要複製的位元組
	copied int64
}

// Start 照勾選的項目開始匯入。
func (s *Service) Start(ctx context.Context, db *store.DB, actor string, in RunIn) (Run, error) {
	src, found, err := s.resolve(ctx, in.Source, in.Path)
	if err != nil {
		return Run{}, sourceErr(err)
	}
	renames := map[string]string{}
	for _, r := range in.Names {
		renames[r.Key] = r.Name
	}
	p, err := s.buildPlan(ctx, db, src, found, renames)
	if err != nil {
		return Run{}, err
	}
	sel := map[string]bool{}
	for _, k := range in.Items {
		it, ok := p.items[k]
		if !ok {
			return Run{}, ops.InvalidInput(ops.FieldError{Field: "items", Message: "沒有這個項目：" + k + "（先重新預演）"})
		}
		if it.Status == StatusReady {
			sel[k] = true
		}
	}
	// 跟著 Bot 的東西：那個 Bot 要一起帶，或之前已經帶過。
	for k := range sel {
		it := p.items[k]
		if it.Bot == "" {
			continue
		}
		owner := p.items[it.Bot]
		if owner == nil || (!sel[it.Bot] && owner.Status != StatusDone) {
			delete(sel, k)
		}
	}
	if len(sel) == 0 {
		return Run{}, ops.InvalidInput(ops.FieldError{Field: "items", Message: "沒有可以帶的項目（都已經帶過，或沒有勾選）"})
	}
	newBots := 0
	for k := range sel {
		if p.items[k].Kind == ItemBot {
			newBots++
		}
	}
	if err := bots.CheckQuota(ctx, db, nil, newBots); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return Run{}, ops.Conflict("已經在匯入了，等這次做完再試")
	}
	s.active = true
	run := &Run{ID: store.NewID(), SourceID: found.ID, Label: found.Label, State: RunRunning, Total: len(sel), Results: []Result{}, Bots: []string{},
		Started: s.now().UnixMilli()}
	s.runs = append([]*Run{run}, s.runs...)
	if len(s.runs) > limits.ImportRunsKept {
		s.runs = s.runs[:limits.ImportRunsKept]
	}
	s.mu.Unlock()
	j := &job{p: p, sel: sel, actor: actor, db: db, run: run, bots: map[string]string{}, fresh: map[string]bool{}, teams: map[string]string{},
		fs: map[string]boxfs.FS{}, fsErr: map[string]error{}}
	base := s.base()
	if in.Wait {
		s.execute(mergeCancel(ctx, base), j)
		return s.snapshot(run), nil
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(base, j)
	}()
	return s.snapshot(run), nil
}

// mergeCancel 回傳 a 結束或 b 結束時就結束的 context（值用 a 的）。
func mergeCancel(a, b context.Context) context.Context {
	ctx, cancel := context.WithCancel(a)
	stop := context.AfterFunc(b, cancel)
	context.AfterFunc(ctx, func() { stop() })
	return ctx
}

func sourceErr(err error) error {
	switch {
	case errors.Is(err, ErrNotLegacy):
		return ops.InvalidInput(ops.FieldError{Field: "path", Message: err.Error()})
	case errors.Is(err, errNoSource):
		return ops.NotFound(err.Error())
	}
	var oe *ops.Error
	if errors.As(err, &oe) {
		return err
	}
	return ops.InvalidInput(ops.FieldError{Field: "path", Message: err.Error()})
}

func (s *Service) execute(ctx context.Context, j *job) {
	defer func() {
		s.mu.Lock()
		s.active = false
		s.mu.Unlock()
	}()
	p, m := j.p, j.p.m
	// 之前帶過的 Bot
	for id := range p.bots {
		if target, ok := m.bot(ctx, j.db, id); ok {
			j.bots[id] = target
		}
	}
	for _, it := range p.out.Items {
		if j.sel[it.Key] && (it.Kind == ItemData || it.Kind == ItemLogin) {
			j.total += it.Bytes
		}
	}
	steps := []struct {
		kind string
		step string
	}{
		{ItemKey, "存模型金鑰"}, {ItemBot, "建立 Bot"}, {ItemTeam, "建立團隊與匯報線"}, {ItemRoom, "建立群組"},
		{ItemApprovals, "只有你能決定的事"}, {ItemRoutine, "帶入定時任務（先暫停）"},
		{ItemSkills, "帶入技能（先關閉）"}, {ItemMemory, "帶入記憶"}, {ItemData, "複製電腦裡的檔案"}, {ItemLogin, "複製瀏覽器登入"},
	}
	for _, st := range steps {
		for _, it := range p.out.Items {
			if it.Kind != st.kind || !j.sel[it.Key] {
				continue
			}
			if ctx.Err() != nil {
				s.finishItem(j, it, ResultFailed, "匯入被中斷了", "")
				continue
			}
			s.update(j.run, func(r *Run) { r.Step = st.step + "：" + it.Title })
			status, detail, target := s.do(ctx, j, it)
			s.finishItem(j, it, status, detail, target)
		}
		if st.kind == ItemTeam {
			// 匯報線也在這一步：沒有勾團隊時一樣要接上。
			s.linkOrg(ctx, j)
		}
	}
	s.update(j.run, func(r *Run) {
		r.State, r.Step, r.Finished, r.Percent = RunDone, "", s.now().UnixMilli(), 100
		if ctx.Err() != nil {
			r.State, r.Error = RunFailed, "匯入被中斷了；已經建立的留著，再匯入一次會接著做"
		}
	})
}

func (s *Service) finishItem(j *job, it Item, status, detail, target string) {
	if status == ResultOK && target != "" {
		rec := store.ImportRecord{Source: j.p.src.Kind, Kind: it.Kind, SourceID: strings.SplitN(it.Key, ":", 2)[1], TargetID: target, Detail: it.Title}
		if err := j.db.PutImport(context.WithoutCancel(context.Background()), rec); err != nil {
			s.log().Warn("記錄匯入對照失敗", "key", it.Key, "err", err)
		}
		j.p.m.put(rec)
	}
	s.update(j.run, func(r *Run) {
		r.Results = append(r.Results, Result{Key: it.Key, Kind: it.Kind, Title: it.Title, Status: status, Detail: detail, Target: target})
		r.Done++
		r.Percent = min(99, r.Done*100/max(1, r.Total))
		if it.Kind == ItemBot && status == ResultOK {
			r.Bots = append(r.Bots, target)
		}
	})
}

// do 帶一項；回傳結果、說明與目標 id（技能、記憶等跟著 Bot 的是 Bot id）。
func (s *Service) do(ctx context.Context, j *job, it Item) (string, string, string) {
	sid := strings.SplitN(it.Key, ":", 2)[1]
	fail := func(err error) (string, string, string) { return ResultFailed, err.Error(), "" }
	switch it.Kind {
	case ItemKey:
		k, ok := findKey(j.p.src.Keys, sid)
		if !ok || s.Providers == nil {
			return ResultSkipped, "找不到這把金鑰", ""
		}
		if _, err := s.Providers.Get(ctx, k.ID); err == nil {
			return ResultSkipped, "你已經設定了 " + k.ID + "，不覆寫", ""
		}
		err := s.Providers.Put(ctx, proxy.Provider{ID: k.ID, BaseURL: k.BaseURL, APIKey: proxy.Secret(k.APIKey), Label: k.Label, Transport: k.Transport, Preset: k.Preset})
		if err != nil {
			return fail(err)
		}
		return ResultOK, "加密後存在 XBot 服務；到「模型」設成預設或給 Bot 用", k.ID
	case ItemBot:
		b := j.p.bots[sid]
		created, err := s.createBot(ctx, j, b, it)
		if err != nil {
			return fail(err)
		}
		j.bots[sid], j.fresh[sid] = created.ID, true
		detail := "權限從「先問我」開始"
		if b.Model != "" {
			detail += "；模型用你這裡的預設（原本是 " + b.Model + "）"
		}
		return ResultOK, detail, created.ID
	case ItemTeam:
		return s.createTeam(ctx, j, sid)
	case ItemRoom:
		return s.createRoom(ctx, j, sid)
	case ItemApprovals:
		if s.ApprovalItems == nil {
			return ResultSkipped, "這個服務沒有組織設定", ""
		}
		list, err := s.ApprovalItems(ctx, j.p.src.ApprovalItems)
		if err != nil {
			return fail(err)
		}
		return ResultOK, strings.Join(list, "、"), "org"
	case ItemRoutine:
		return s.createRoutine(ctx, j, sid)
	}
	// 以下都寫進 Bot 的電腦
	b := j.p.bots[sid]
	botID, ok := j.bots[sid]
	if !ok {
		return ResultSkipped, "這個 Bot 沒有帶進來", ""
	}
	f, err := s.computer(ctx, j, botID)
	if err != nil {
		return ResultFailed, "要先建立 Bot 的電腦才能放進去：" + err.Error(), ""
	}
	switch it.Kind {
	case ItemSkills:
		return s.importSkills(ctx, f, botID, b)
	case ItemMemory:
		return s.importMemory(ctx, j, f, botID, b)
	case ItemData:
		var files int
		var size int64
		var skipped []string
		for _, t := range b.Data {
			n, sz, sk, err := s.copyTree(ctx, j, f, t)
			files, size, skipped = files+n, size+sz, append(skipped, sk...)
			if err != nil {
				return ResultFailed, fmt.Sprintf("%s 複製到一半失敗（已複製 %d 個檔案）：%v", t.Label, files, err), ""
			}
		}
		return ResultOK, copiedText(files, size, skipped), botID
	case ItemLogin:
		if b.Login == nil {
			return ResultSkipped, "沒有瀏覽器登入", ""
		}
		n, sz, sk, err := s.copyTree(ctx, j, f, *b.Login)
		if err != nil {
			return ResultFailed, fmt.Sprintf("複製到一半失敗：%v", err), ""
		}
		return ResultOK, copiedText(n, sz, sk) + "；有些網站可能要你再登入一次", botID
	}
	return ResultSkipped, "不認得的項目", ""
}

func copiedText(files int, size int64, skipped []string) string {
	t := fmt.Sprintf("複製了 %d 個檔案（%s）", files, human(size))
	if len(skipped) > 0 {
		t += fmt.Sprintf("；%d 個沒複製：%s", len(skipped), clip(strings.Join(skipped, "、"), 200))
	}
	return t
}

func findKey(keys []Key, id string) (Key, bool) {
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return Key{}, false
}

// computer 找（沒有時建立，不開機）Bot 的電腦。
func (s *Service) computer(ctx context.Context, j *job, botID string) (boxfs.FS, error) {
	if f, ok := j.fs[botID]; ok {
		return f, nil
	}
	if err, ok := j.fsErr[botID]; ok {
		return nil, err
	}
	if s.Locate == nil {
		return nil, errors.New("這個服務沒有管理 Bot 的電腦")
	}
	f, err := s.Locate(ctx, botID, true)
	if err != nil {
		j.fsErr[botID] = err
		return nil, err
	}
	j.fs[botID] = f
	return f, nil
}

func (s *Service) createBot(ctx context.Context, j *job, b Bot, it Item) (store.Bot, error) {
	if err := bots.CheckQuota(ctx, j.db, nil, 1); err != nil {
		return store.Bot{}, err
	}
	all, err := j.db.ListBots(ctx, true)
	if err != nil {
		return store.Bot{}, err
	}
	taken := map[string]bool{}
	for _, x := range all {
		taken[x.Name] = true
	}
	name, _ := uniqueName(cmpOr(it.Name, clip(b.Name, limits.BotNameMax)), taken)
	av, _ := json.Marshal(map[string]string{"shape": it.Shape, "eyes": pick(b.Eyes, bots.EyeVariants, ""), "image": it.Image})
	row := store.Bot{Name: name, Title: clip(b.Title, 100), Description: clip(b.Description, limits.BotDescriptionMax), Soul: clip(b.Soul, limits.BotSoulMax),
		Color: it.Color, Avatar: av, Engine: string(bots.EngineHermes),
		ApprovalMode:  string(bots.ModeAsk), // SEC-1：匯入的 Bot 一律先問我
		MemoryEnabled: true, CreatedBy: j.actor}
	return j.db.CreateBot(ctx, row)
}

func (s *Service) createTeam(ctx context.Context, j *job, sid string) (string, string, string) {
	i := slices.IndexFunc(j.p.src.Teams, func(t Team) bool { return t.ID == sid })
	if i < 0 {
		return ResultSkipped, "找不到這個團隊", ""
	}
	t := j.p.src.Teams[i]
	var members []string
	for _, id := range t.Members {
		if target, ok := j.bots[id]; ok {
			members = append(members, target)
		}
	}
	if len(members) == 0 {
		return ResultSkipped, "成員都沒有帶進來", ""
	}
	name := clip(t.Name, limits.TeamNameMax)
	for n := 2; ; n++ {
		if _, err := j.db.FindTeamByName(ctx, name); errors.Is(err, store.ErrNotFound) {
			break
		} else if err != nil {
			return ResultFailed, err.Error(), ""
		}
		name = clip(t.Name, limits.TeamNameMax-3) + fmt.Sprintf(" %d", n)
	}
	row := store.Team{Name: name, About: t.About, ChiefID: j.bots[t.ChiefID], CreatedBy: j.actor, Memory: []string{}}
	created, err := j.db.CreateTeam(ctx, row)
	if err != nil {
		return ResultFailed, err.Error(), ""
	}
	j.teams[sid] = created.ID
	detail := fmt.Sprintf("%d 位成員", len(members))
	if name != t.Name {
		detail += "；你已經有同名的團隊，改叫「" + name + "」"
	}
	return ResultOK, detail, created.ID
}

// linkOrg 把這次建立的 Bot 放進團隊、接上匯報線（主管也帶進來時才接）。
func (s *Service) linkOrg(ctx context.Context, j *job) {
	for sid := range j.fresh {
		b := j.p.bots[sid]
		team := j.teams[b.TeamID]
		if team == "" && b.TeamID != "" {
			if id, ok := j.p.m.done(ctx, j.db, ItemTeam, b.TeamID); ok {
				team = id
			}
		}
		boss := j.bots[b.ReportsTo]
		if boss == j.bots[sid] || loops(j, sid) {
			boss = ""
		}
		if team == "" && boss == "" {
			continue
		}
		if err := j.db.SetBotOrg(ctx, j.bots[sid], team, boss, j.actor); err != nil {
			s.log().Warn("接上匯報線失敗", "bot", j.bots[sid], "err", err)
		}
	}
}

// loops 回答照舊資料接上匯報線會不會繞回自己（舊資料壞掉時）。
func loops(j *job, sid string) bool {
	seen := map[string]bool{sid: true}
	cur := j.p.bots[sid].ReportsTo
	for cur != "" {
		if seen[cur] {
			return true
		}
		seen[cur] = true
		cur = j.p.bots[cur].ReportsTo
	}
	return false
}

func (s *Service) createRoom(ctx context.Context, j *job, sid string) (string, string, string) {
	i := slices.IndexFunc(j.p.src.Rooms, func(r Room) bool { return r.ID == sid })
	if i < 0 {
		return ResultSkipped, "找不到這個群組", ""
	}
	r := j.p.src.Rooms[i]
	var members []string
	for _, id := range r.Members {
		if target, ok := j.bots[id]; ok && !slices.Contains(members, target) {
			members = append(members, target)
		}
	}
	if len(members) < limits.RoomMembersMin {
		return ResultSkipped, fmt.Sprintf("帶進來的成員不到 %d 個", limits.RoomMembersMin), ""
	}
	if len(members) > limits.RoomMembersMax {
		members = members[:limits.RoomMembersMax]
	}
	row := store.Room{Name: clip(r.Name, limits.RoomNameMax), LeadID: members[0], Bulletin: strings.TrimSpace(r.Bulletin), CreatedBy: j.actor}
	if row.Name == "" {
		var names []string
		for _, id := range members {
			if b, err := j.db.GetBot(ctx, id); err == nil {
				names = append(names, b.Name)
			}
		}
		row.Name, row.AutoName = clip(strings.Join(names, "、"), limits.RoomNameMax), true
	}
	err := j.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		if row, err = q.CreateRoom(ctx, row); err != nil {
			return err
		}
		return q.SetRoomMembers(ctx, row.ID, members)
	})
	if err != nil {
		return ResultFailed, err.Error(), ""
	}
	return ResultOK, fmt.Sprintf("%d 位成員；舊的對話不帶", len(members)), row.ID
}

func (s *Service) createRoutine(ctx context.Context, j *job, sid string) (string, string, string) {
	i := slices.IndexFunc(j.p.src.Routines, func(r Routine) bool { return r.ID == sid })
	if i < 0 {
		return ResultSkipped, "找不到這個定時任務", ""
	}
	r := j.p.src.Routines[i]
	botID, ok := j.bots[r.BotID]
	if !ok {
		return ResultSkipped, "它的 Bot 沒有帶進來", ""
	}
	n, err := j.db.CountRoutines(ctx, botID)
	if err != nil {
		return ResultFailed, err.Error(), ""
	}
	if n >= limits.RoutinesPerBot {
		return ResultSkipped, fmt.Sprintf("一個 Bot 最多 %d 個定時任務", limits.RoutinesPerBot), ""
	}
	sc, err := routines.Normalize(r.Schedule, r.Jitter, s.now())
	if err != nil {
		return ResultFailed, "排程帶不過來：" + err.Error(), ""
	}
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		return ResultSkipped, "沒有工作內容", ""
	}
	row := store.Routine{BotID: botID, Title: clip(cmpOr(r.Title, routines.TitleOf(prompt)), limits.RoutineTitleMax), Prompt: prompt,
		JitterMinutes: r.Jitter, NotifyOnFailure: true, Status: store.RoutinePaused, CreatedBy: j.actor}
	row.Schedule, _ = json.Marshal(sc)
	created, err := j.db.CreateRoutine(ctx, row)
	if err != nil {
		return ResultFailed, err.Error(), ""
	}
	detail := routines.Describe(sc) + "；先暫停，你確認後再啟用"
	if r.Note != "" {
		detail += "（" + r.Note + "）"
	}
	return ResultOK, detail, created.ID
}

// importSkills 把技能放進 Bot 自己的技能並先關閉（SEC-1、SK-1）；同名的已經有就略過。
func (s *Service) importSkills(ctx context.Context, f boxfs.FS, botID string, b Bot) (string, string, string) {
	bot := skills.Bot{FS: f, BotID: botID}
	var added, merged, failed []string
	for _, sk := range b.Skills {
		name, desc, body := oneLine(sk.Name), oneLine(sk.Description), sk.Instructions
		if utf8.RuneCountInString(desc) > limits.SkillDescriptionMax {
			desc = clip(desc, limits.SkillDescriptionMax)
		}
		_, err := bot.Create(ctx, skills.Edit{Name: &name, Description: &desc, Instructions: &body, Place: skills.PlaceOff, Source: "imported", Files: sk.Files})
		switch {
		case errors.Is(err, skills.ErrExists):
			merged = append(merged, name)
		case err != nil:
			failed = append(failed, name+"（"+err.Error()+"）")
		default:
			added = append(added, name)
		}
	}
	detail := fmt.Sprintf("放進 %d 個，先關閉；到 Bot 的「能力」看過內容再打開", len(added))
	if len(merged) > 0 {
		detail += fmt.Sprintf("；%d 個已經有同名的：%s", len(merged), strings.Join(merged, "、"))
	}
	if len(failed) > 0 {
		detail += fmt.Sprintf("；%d 個沒放進去：%s", len(failed), clip(strings.Join(failed, "、"), 300))
	}
	if len(added) == 0 && len(failed) > 0 {
		return ResultFailed, detail, ""
	}
	return ResultOK, detail, botID
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// memoryDir 是放不進記憶上限的條目與記憶檔放在工作資料夾的位置。
var memoryDir = path.Join(box.WorkspaceDir, "匯入的記憶")

// importMemory 把記憶條目寫進 Bot 的記憶（照 Hermes 的上限；放不下的與分開的記憶檔放進工作資料夾）。
func (s *Service) importMemory(ctx context.Context, j *job, f boxfs.FS, botID string, b Bot) (string, string, string) {
	if s.Memory == nil {
		return ResultSkipped, "這個服務沒有記憶功能", ""
	}
	files := slices.Clone(b.MemoryFiles)
	var kept int
	for _, t := range []struct {
		target memory.Target
		items  []string
		limit  int
	}{{memory.TargetMemory, b.Memory, limits.MemoryCharsDefault}, {memory.TargetUser, b.User, limits.MemoryUserCharsDefault}} {
		if len(t.items) == 0 {
			continue
		}
		fit, rest := fitEntries(t.items, t.limit)
		if len(fit) > 0 {
			cur, err := memory.ReadRaw(ctx, f, t.target)
			if err != nil {
				return ResultFailed, err.Error(), ""
			}
			existing := memory.Entries(cur)
			merged := slices.Clone(existing)
			for _, e := range fit {
				if !slices.Contains(merged, e) {
					merged = append(merged, e)
				}
			}
			merged, more := fitEntries(merged, t.limit)
			rest = append(rest, more...)
			if _, err := s.Memory.Write(ctx, botID, t.target, merged, existing, j.actor); err != nil {
				return ResultFailed, err.Error(), ""
			}
			kept += len(merged) - len(existing)
		}
		if len(rest) > 0 {
			files = append(files, boxfs.File{Name: "放不下的" + t.target.File(), Data: []byte(strings.Join(rest, "\n\n---\n\n") + "\n"), Mode: 0o644})
		}
	}
	detail := fmt.Sprintf("記住 %d 條", kept)
	if len(files) > 0 {
		if err := f.WriteFiles(ctx, memoryDir, files); err != nil {
			return ResultFailed, detail + "；記憶檔沒放進去：" + err.Error(), ""
		}
		detail += fmt.Sprintf("；%d 份記憶檔放在工作資料夾的「匯入的記憶」", len(files))
	}
	return ResultOK, detail, botID
}

// fitEntries 依序放進上限內（每條也照上限截短）；放不下的回傳在 rest。
func fitEntries(items []string, limit int) (fit, rest []string) {
	used := 0
	for _, it := range items {
		it = strings.TrimSpace(strings.ReplaceAll(it, "§", " "))
		if it == "" {
			continue
		}
		it = clip(it, limits.MemoryEntryMax)
		n := utf8.RuneCountInString(it)
		if len(fit) > 0 {
			n += utf8.RuneCountInString(memory.Delimiter)
		}
		if used+n > limit {
			rest = append(rest, it)
			continue
		}
		used += n
		fit = append(fit, it)
	}
	return fit, rest
}

// copyTree 把一個本機資料夾複製進 Bot 的電腦（分批，每批一次 docker cp）。回傳檔案數、大小與沒複製的檔案。
func (s *Service) copyTree(ctx context.Context, j *job, f boxfs.FS, t Tree) (int, int64, []string, error) {
	var batch []boxfs.File
	var batchBytes, size int64
	var files int
	var skipped []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := f.WriteFiles(ctx, t.Dest, batch); err != nil {
			return err
		}
		files += len(batch)
		size += batchBytes
		n := batchBytes
		batch, batchBytes = nil, 0
		j.copied += n
		s.update(j.run, func(r *Run) {
			r.Bytes = j.copied
			if j.total > 0 {
				r.Step = fmt.Sprintf("複製電腦資料：%s／%s", human(j.copied), human(j.total))
			}
		})
		return nil
	}
	err := walkTree(t, func(rel, p string, info os.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if size+batchBytes+info.Size() > limits.ImportDataBytes {
			skipped = append(skipped, rel)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			skipped = append(skipped, rel)
			return nil
		}
		if batchBytes+int64(len(data)) > limits.ImportBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		batch = append(batch, boxfs.File{Name: rel, Data: data, Mode: int64(info.Mode().Perm())})
		batchBytes += int64(len(data))
		return nil
	})
	if err == nil {
		err = flush()
	}
	return files, size, skipped, err
}
