package importer

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/boxfs"
	"xbot/internal/memory"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/skills"
	"xbot/internal/store"
)

var ctx = context.Background()

var owner = ops.Owner("test")

type fakeProviders struct {
	mu sync.Mutex
	m  map[string]proxy.Provider
}

func (f *fakeProviders) Put(_ context.Context, p proxy.Provider) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[p.ID] = p
	return nil
}

func (f *fakeProviders) Get(_ context.Context, id string) (proxy.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok {
		return p, proxy.ErrNotFound
	}
	return p, nil
}

type env struct {
	db        *store.DB
	reg       *ops.Registry
	svc       *Service
	fs        map[string]*boxfs.Mem
	providers *fakeProviders
	approvals []string
	events    []Run
	mu        sync.Mutex
}

func setup(t *testing.T, roots ...string) *env {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), fs: map[string]*boxfs.Mem{}, providers: &fakeProviders{m: map[string]proxy.Provider{}}}
	locate := func(_ context.Context, botID string, create bool) (boxfs.FS, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if m, ok := e.fs[botID]; ok {
			return m, nil
		}
		if !create {
			return nil, boxfs.ErrNoComputer
		}
		e.fs[botID] = boxfs.NewMem()
		return e.fs[botID], nil
	}
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	e.svc = &Service{DB: db, Locate: locate, Memory: &memory.Service{DB: db, Locate: locate}, Providers: e.providers, Roots: roots,
		TempDir: t.TempDir(), Now: func() time.Time { return now },
		ApprovalItems: func(_ context.Context, add []string) ([]string, error) {
			e.approvals = append(e.approvals, add...)
			return e.approvals, nil
		},
		Publish: func(kind string, payload any) {
			if kind == KindProgress {
				e.mu.Lock()
				e.events = append(e.events, payload.(Run))
				e.mu.Unlock()
			}
		}}
	t.Cleanup(e.svc.Close)
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, e.svc)
	return e
}

func (e *env) call(t *testing.T, id ops.Identity, op string, in, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(ctx, id, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return err
}

func (e *env) must(t *testing.T, op string, in, out any) {
	t.Helper()
	if err := e.call(t, owner, op, in, out); err != nil {
		t.Fatalf("%s：%v", op, err)
	}
}

func item(t *testing.T, p PlanOut, key string) Item {
	t.Helper()
	for _, it := range p.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("預演沒有 %s：%+v", key, p.Items)
	return Item{}
}

func checked(p PlanOut) []string {
	var out []string
	for _, it := range p.Items {
		if it.Checked {
			out = append(out, it.Key)
		}
	}
	return out
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func TestDetectFindsBothAndSummarizes(t *testing.T) {
	lb, omb := lazyBoyFixture(t), openMausBotFixture(t)
	e := setup(t, lb, omb, filepath.Join(t.TempDir(), "沒有這個"))
	var out DetectOut
	e.must(t, "xbot.import.detect", DetectIn{}, &out)
	if len(out.Sources) != 2 {
		t.Fatalf("找到：%+v", out.Sources)
	}
	l, o := out.Sources[0], out.Sources[1]
	if l.Kind != KindLazyBoy || l.Bots != 3 || l.Skills != 1 || l.Memory != 2 || l.Routines != 3 || l.Teams != 1 || l.Rooms != 1 || l.Keys != 1 || l.Logins != 1 {
		t.Fatalf("LazyBoy：%+v", l)
	}
	if l.Summary != "在這台電腦找到 LazyBoy 的 3 個 Bot、1 把金鑰和 3 個定時任務。" {
		t.Fatalf("摘要：%s", l.Summary)
	}
	if o.Kind != KindOpenMausBot || o.Bots != 2 || o.Skills != 1 || o.Memory != 1 || o.Routines != 3 || o.Teams != 1 || o.Rooms != 1 || o.Keys != 1 {
		t.Fatalf("OpenMausBot：%+v", o)
	}
	// 指定的資料夾不是舊資料
	err := e.call(t, owner, "xbot.import.detect", DetectIn{Path: t.TempDir()}, nil)
	if code(err) != ops.CodeInvalidInput {
		t.Fatalf("不是舊資料：%v", err)
	}
	// 只有你本人能匯入（會讀這台電腦的檔案與金鑰）
	if err := e.call(t, ops.Bot("b1", "小秘"), "xbot.import.detect", DetectIn{}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("Bot 呼叫：%v", err)
	}
}

func TestDefaultRootsFromEnv(t *testing.T) {
	t.Setenv(EnvPaths, "off")
	if r := DefaultRoots(); len(r) != 0 {
		t.Fatalf("off：%v", r)
	}
	t.Setenv(EnvPaths, "/a"+string(filepath.ListSeparator)+"/b")
	if r := DefaultRoots(); !slices.Equal(r, []string{"/a", "/b"}) {
		t.Fatalf("自訂：%v", r)
	}
}

func TestLazyBoyPlanRunAndIdempotent(t *testing.T) {
	lb := lazyBoyFixture(t)
	before := treeSum(t, lb)
	e := setup(t, lb)
	// XBot 已經有一個「小秘」
	if _, err := e.db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes", ApprovalMode: "ask"}); err != nil {
		t.Fatal(err)
	}
	var found DetectOut
	e.must(t, "xbot.import.detect", DetectIn{}, &found)
	src := found.Sources[0].ID

	var p PlanOut
	e.must(t, "xbot.import.plan", PlanIn{Source: src}, &p)
	if p.NewBots != 3 || !p.CanCreate || len(p.NotCarried) == 0 {
		t.Fatalf("預演：%+v", p)
	}
	solo := item(t, p, "bot:"+lbSolo)
	if solo.Name != "小秘 2" || !solo.Renamed || !solo.Checked {
		t.Fatalf("同名：%+v", solo)
	}
	if it := item(t, p, "key:opencode-go"); it.Checked || !it.Explicit || it.Status != StatusReady {
		t.Fatalf("金鑰要明確勾選：%+v", it)
	}
	if it := item(t, p, "login:"+lbBoss); it.Checked || !it.Explicit {
		t.Fatalf("登入要明確勾選：%+v", it)
	}
	if it := item(t, p, "skills:"+lbBoss); it.Count != 1 || !strings.Contains(it.Detail, "taiwan-opendata") || it.Attention == "" {
		t.Fatalf("技能只帶自己學的：%+v", it)
	}
	if it := item(t, p, "data:"+lbBoss); !it.Checked || it.Count != 3 {
		// report.md、data/a.csv、Downloads/file.pdf；node_modules、隱藏的資料夾、chrome-profile 不算
		t.Fatalf("電腦資料：%+v", it)
	}
	if it := item(t, p, "routine:r-bad"); it.Status != StatusBlocked || it.Checked {
		t.Fatalf("排程帶不過來：%+v", it)
	}
	if it := item(t, p, "routine:r-daily"); !it.Checked || !strings.Contains(it.Detail, "平日") {
		t.Fatalf("定時任務：%+v", it)
	}
	if it := item(t, p, "room:"+lbRoomID); it.Count != 2 || !it.Checked {
		t.Fatalf("群組（不在的成員去掉）：%+v", it)
	}

	// 照預設勾選，再加上金鑰與登入，並改一個名字
	sel := append(checked(p), "key:opencode-go", "login:"+lbBoss)
	var run Run
	e.must(t, "xbot.import.run", RunIn{Source: src, Items: sel, Names: []Rename{{Key: "bot:" + lbSolo, Name: "舊小秘"}}, Wait: true}, &run)
	if run.State != RunDone || run.Percent != 100 || len(run.Bots) != 3 {
		t.Fatalf("匯入：%+v", run)
	}
	for _, r := range run.Results {
		if r.Status != ResultOK {
			t.Fatalf("%s 沒成功：%+v", r.Key, r)
		}
	}
	if len(e.events) == 0 || e.events[len(e.events)-1].State != RunDone {
		t.Fatalf("進度事件：%d", len(e.events))
	}
	target := map[string]string{}
	for _, r := range run.Results {
		target[r.Key] = r.Target
	}
	boss, err := e.db.GetBot(ctx, target["bot:"+lbBoss])
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := e.db.GetBot(ctx, target["bot:"+lbWorker])
	solo2, _ := e.db.GetBot(ctx, target["bot:"+lbSolo])
	// SEC-1：一律先問我；團隊管理權限不帶
	for _, b := range []store.Bot{boss, worker, solo2} {
		if b.ApprovalMode != "ask" || b.CanManage || b.Engine != "hermes" || b.Model != "" {
			t.Fatalf("匯入的 Bot：%+v", b)
		}
	}
	if boss.Soul != "繁體中文。你帶兩位研究員。" || boss.Title != "主管" || solo2.Name != "舊小秘" {
		t.Fatalf("人設與名字：%+v %+v", boss, solo2)
	}
	// 團隊與匯報線
	team, err := e.db.GetTeam(ctx, target["team:"+lbGroupID])
	if err != nil || team.Name != "蝦皮分潤研究" || team.ChiefID != boss.ID {
		t.Fatalf("團隊：%+v %v", team, err)
	}
	if worker.ReportsTo != boss.ID || worker.TeamID != team.ID || boss.ReportsTo != "" || boss.TeamID != team.ID {
		t.Fatalf("匯報線：boss=%+v worker=%+v", boss, worker)
	}
	// 群組
	members, err := e.db.ListRoomMembers(ctx, target["room:"+lbRoomID])
	if err != nil || len(members) != 2 {
		t.Fatalf("群組成員：%v %v", members, err)
	}
	// 定時任務先暫停
	rt, err := e.db.GetRoutine(ctx, target["routine:r-daily"])
	if err != nil || rt.Status != store.RoutinePaused || rt.BotID != boss.ID || rt.Prompt != "整理昨天的研究" || rt.HookID != "" {
		t.Fatalf("定時任務：%+v %v", rt, err)
	}
	var sc routines.Schedule
	_ = json.Unmarshal(rt.Schedule, &sc)
	if sc.Kind != "weekdays" || sc.Time != "09:00" {
		t.Fatalf("排程：%+v", sc)
	}
	// 金鑰進 Model Proxy（不進電腦）
	if k, err := e.providers.Get(ctx, "opencode-go"); err != nil || string(k.APIKey) != "sk-lb-opencode-0000000000000000" || k.Preset != "opencode-go" {
		t.Fatalf("金鑰：%+v %v", k, err)
	}
	if !slices.Equal(e.approvals, []string{"新商品定價", "簽約"}) {
		t.Fatalf("只有你能決定的事：%v", e.approvals)
	}
	// 主管的電腦：技能先關閉、記憶、檔案、登入
	fs := e.fs[boss.ID]
	all, err := skills.Bot{FS: fs, BotID: boss.ID}.List(ctx)
	if err != nil || len(all) != 1 || all[0].Name != "taiwan-opendata" || all[0].Place != skills.PlaceOff {
		t.Fatalf("技能：%+v %v", all, err)
	}
	if string(fs.Files[skills.SkillsDir+"/"+all[0].ID+"/references/api.md"]) != "# API" {
		t.Fatalf("技能附帶的檔案：%v", keys(fs))
	}
	st, err := memory.Read(ctx, fs)
	if err != nil || !slices.Equal(st.Memory.Entries, []string{"使用者在台北", "報告要附來源"}) || !slices.Equal(st.User.Entries, []string{"叫他王先生"}) {
		t.Fatalf("記憶：%+v %v", st, err)
	}
	for _, want := range []string{"/workspace/report.md", "/workspace/data/a.csv", "/home/box/Downloads/file.pdf", "/home/box/chrome-profile/Default/Cookies"} {
		if fs.Files[want] == nil {
			t.Fatalf("少了 %s：%v", want, keys(fs))
		}
	}
	for _, k := range keys(fs) {
		for _, bad := range []string{".grok", ".bash_history", "node_modules", "/Cache/", "Singleton", ".env", "state.db", "SOUL.md"} {
			if strings.Contains(k, bad) {
				t.Fatalf("不該帶 %s", k)
			}
		}
	}
	// 研究員只有記憶
	if st, _ := memory.Read(ctx, e.fs[worker.ID]); len(st.Memory.Entries) != 1 {
		t.Fatalf("研究員的記憶：%+v", st)
	}

	// 再匯入一次：都已經帶過，不重複建立
	var again PlanOut
	e.must(t, "xbot.import.plan", PlanIn{Source: src}, &again)
	if again.NewBots != 0 || len(checked(again)) != 0 {
		t.Fatalf("再預演：%+v", checked(again))
	}
	if it := item(t, again, "bot:"+lbBoss); it.Status != StatusDone || it.Target != boss.ID {
		t.Fatalf("已經帶過：%+v", it)
	}
	if err := e.call(t, owner, "xbot.import.run", RunIn{Source: src, Items: sel, Wait: true}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("再匯入：%v", err)
	}
	n, _ := e.db.CountBots(ctx)
	if n != 4 {
		t.Fatalf("Bot 數：%d", n)
	}
	var found2 DetectOut
	e.must(t, "xbot.import.detect", DetectIn{}, &found2)
	if found2.Sources[0].Imported != 3 || !strings.Contains(found2.Sources[0].Summary, "都帶進來了") {
		t.Fatalf("偵測：%+v", found2.Sources[0])
	}
	// 刪掉一個匯入的 Bot 後可以再帶一次（只有它）
	if err := e.db.DeleteBot(ctx, solo2.ID); err != nil {
		t.Fatal(err)
	}
	e.must(t, "xbot.import.plan", PlanIn{Source: src}, &again)
	if again.NewBots != 1 || item(t, again, "bot:"+lbSolo).Status != StatusReady {
		t.Fatalf("刪掉後：%+v", checked(again))
	}
	// 舊資料完全沒動
	if after := treeSum(t, lb); after != before {
		t.Fatal("匯入改到了 LazyBoy 的資料")
	}
}

func keys(m *boxfs.Mem) []string {
	var out []string
	for k := range m.Files {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestOpenMausBotRun(t *testing.T) {
	omb := openMausBotFixture(t)
	before := treeSum(t, omb)
	e := setup(t, omb)
	var p PlanOut
	e.must(t, "xbot.import.plan", PlanIn{Path: omb}, &p)
	if it := item(t, p, "routine:o3"); it.Status != StatusBlocked {
		t.Fatalf("群組目標：%+v", it)
	}
	if it := item(t, p, "bot:"+ombChief); it.Color != "purple" || it.Shape != "star" || !strings.HasPrefix(it.Image, "data:image/png;base64,") {
		t.Fatalf("頭像：%+v", it)
	}
	if it := item(t, p, "bot:"+ombWriter); it.Color == "nope" || it.Shape != "cursor" {
		t.Fatalf("不認得的顏色用預設：%+v", it)
	}
	var run Run
	e.must(t, "xbot.import.run", RunIn{Path: omb, Items: append(checked(p), "key:anthropic"), Wait: true}, &run)
	target := map[string]string{}
	for _, r := range run.Results {
		if r.Status != ResultOK {
			t.Fatalf("%s：%+v", r.Key, r)
		}
		target[r.Key] = r.Target
	}
	chief, _ := e.db.GetBot(ctx, target["bot:"+ombChief])
	writer, _ := e.db.GetBot(ctx, target["bot:"+ombWriter])
	if chief.ApprovalMode != "ask" || writer.ApprovalMode != "ask" {
		t.Fatalf("OpenMausBot 的 full／auto 也要變先問我：%s %s", chief.ApprovalMode, writer.ApprovalMode)
	}
	if writer.ReportsTo != chief.ID || writer.TeamID == "" || writer.TeamID != chief.TeamID {
		t.Fatalf("社群團隊：%+v", writer)
	}
	team, _ := e.db.GetTeam(ctx, writer.TeamID)
	if team.Name != "社群" || team.About != "對外文案先給主管看" || team.ChiefID != chief.ID {
		t.Fatalf("團隊：%+v", team)
	}
	if _, ok := target["room:g2"]; ok {
		t.Fatal("私訊不帶")
	}
	room, _ := e.db.GetRoom(ctx, target["room:g1"])
	if room.Name != "社群討論" || room.Bulletin != "先給主管看" {
		t.Fatalf("群組：%+v", room)
	}
	r1, _ := e.db.GetRoutine(ctx, target["routine:o1"])
	r2, _ := e.db.GetRoutine(ctx, target["routine:o2"])
	var s1, s2 routines.Schedule
	_ = json.Unmarshal(r1.Schedule, &s1)
	_ = json.Unmarshal(r2.Schedule, &s2)
	if s1.Kind != "weekdays" || s1.Time != "08:30" || s2.Kind != "cron" || s2.Cron != "0 20 * * 0" || s2.Timezone != "Asia/Tokyo" || r1.Status != store.RoutinePaused {
		t.Fatalf("排程：%+v %+v", s1, s2)
	}
	fs := e.fs[writer.ID]
	all, _ := skills.Bot{FS: fs, BotID: writer.ID}.List(ctx)
	if len(all) != 1 || all[0].Name != "post-format" || all[0].Place != skills.PlaceOff {
		t.Fatalf("技能：%+v", all)
	}
	st, _ := memory.Read(ctx, fs)
	if !slices.Equal(st.Memory.Entries, []string{"品牌色是紫色", "不用驚嘆號", "客戶是小店家。"}) {
		t.Fatalf("記憶：%+v", st.Memory.Entries)
	}
	for _, want := range []string{"/workspace/drafts/post1.md", memoryDir + "/MEMORY.md", memoryDir + "/memory/brand.md"} {
		if fs.Files[want] == nil {
			t.Fatalf("少了 %s：%v", want, keys(fs))
		}
	}
	if fs.Files["/workspace/skills/post-format/SKILL.md"] != nil {
		t.Fatal("技能不該再複製到工作資料夾")
	}
	if k, err := e.providers.Get(ctx, "anthropic"); err != nil || k.Preset != "anthropic" || k.BaseURL == "" {
		t.Fatalf("金鑰：%+v %v", k, err)
	}
	if after := treeSum(t, omb); after != before {
		t.Fatal("匯入改到了 OpenMausBot 的資料")
	}
}

func TestQuotaAndConflicts(t *testing.T) {
	lb := lazyBoyFixture(t)
	e := setup(t, lb)
	for i := range 49 {
		if _, err := e.db.CreateBot(ctx, store.Bot{Name: "b" + string(rune('A'+i)), Engine: "hermes", ApprovalMode: "ask"}); err != nil {
			t.Fatal(err)
		}
	}
	var p PlanOut
	e.must(t, "xbot.import.plan", PlanIn{Path: lb}, &p)
	if p.CanCreate || p.Remaining != 1 || p.Reason == "" {
		t.Fatalf("上限：%+v", p)
	}
	if err := e.call(t, owner, "xbot.import.run", RunIn{Path: lb, Items: checked(p), Wait: true}, nil); code(err) != ops.CodeLimit {
		t.Fatalf("超過上限：%v", err)
	}
	// 只挑一個可以
	var run Run
	e.must(t, "xbot.import.run", RunIn{Path: lb, Items: []string{"bot:" + lbSolo, "memory:" + lbBoss}, Wait: true}, &run)
	if len(run.Bots) != 1 || len(run.Results) != 1 {
		t.Fatalf("挑一個（主管沒帶，它的記憶不能帶）：%+v", run)
	}
	// 項目代號不對
	if err := e.call(t, owner, "xbot.import.run", RunIn{Path: lb, Items: []string{"bot:沒有"}}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("不對的項目：%v", err)
	}
	// 預演不建立
	raw, _ := json.Marshal(RunIn{Path: lb, Items: []string{"bot:" + lbBoss}})
	if _, err := e.reg.Invoke(ctx, owner, "xbot.import.run", raw, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.db.CountBots(ctx); n != 50 {
		t.Fatalf("Bot 數：%d", n)
	}
}

func TestBackgroundRunAndStatus(t *testing.T) {
	omb := openMausBotFixture(t)
	e := setup(t, omb)
	var p PlanOut
	e.must(t, "xbot.import.plan", PlanIn{Path: omb}, &p)
	var run Run
	e.must(t, "xbot.import.run", RunIn{Path: omb, Items: checked(p)}, &run)
	if run.ID == "" || run.Total == 0 {
		t.Fatalf("開始：%+v", run)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var st StatusOut
		e.must(t, "xbot.import.status", StatusIn{}, &st)
		if !st.Running && st.Runs[0].State == RunDone {
			if st.Runs[0].ID != run.ID || st.Runs[0].Done != st.Runs[0].Total {
				t.Fatalf("狀態：%+v", st.Runs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等太久：%+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemoryOverflowGoesToWorkspace(t *testing.T) {
	fit, rest := fitEntries([]string{strings.Repeat("a", 1500), strings.Repeat("b", 1000), "c§d", " "}, 2200)
	if len(fit) != 2 || len(rest) != 1 || fit[1] != "c d" || !strings.HasPrefix(rest[0], "bbb") {
		t.Fatalf("fit=%d rest=%d %q", len(fit), len(rest), fit)
	}
}

func TestOMBSchedules(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want routines.Schedule
	}{
		{map[string]any{"type": "daily", "time": "07:00", "weekdays": []any{0.0, 6.0}}, routines.Schedule{Kind: "weekly", Time: "07:00", Weekdays: []int{6, 7}}},
		{map[string]any{"type": "daily", "time": "07:00", "weekdays": []any{}}, routines.Schedule{Kind: "daily", Time: "07:00"}},
		{map[string]any{"type": "interval", "everyMinutes": 30.0, "window": map[string]any{"start": "09:00", "end": "18:00"}},
			routines.Schedule{Kind: "interval", EveryMinutes: 30, ActiveFrom: "09:00", ActiveTo: "18:00"}},
	}
	for _, c := range cases {
		got := ombRoutineOf(ombRoutine{ID: "x", BotID: "b", Prompt: "p", Schedule: c.in}).Schedule
		if got.Kind != c.want.Kind || got.Time != c.want.Time || !slices.Equal(got.Weekdays, c.want.Weekdays) || got.EveryMinutes != c.want.EveryMinutes || got.ActiveTo != c.want.ActiveTo {
			t.Errorf("%v → %+v", c.in, got)
		}
		if _, err := routines.Normalize(got, 0, time.Now()); err != nil {
			t.Errorf("%v：%v", c.in, err)
		}
	}
	once := ombRoutineOf(ombRoutine{Schedule: map[string]any{"type": "once", "at": float64(time.Date(2027, 1, 2, 1, 30, 0, 0, time.UTC).UnixMilli())}}).Schedule
	if once.Kind != "once" || once.Date != "2027-01-02" || once.Time != "09:30" {
		t.Errorf("單次：%+v", once)
	}
}

func TestSplitNotes(t *testing.T) {
	got := splitNotes("# 標題\n\n- 一\n- 二\n  接續\n\n三段\n第二行\n")
	if !slices.Equal(got, []string{"一", "二\n接續", "三段\n第二行"}) {
		t.Fatalf("%q", got)
	}
}
