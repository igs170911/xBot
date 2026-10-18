package box

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"xbot/internal/store"
)

// fakeActivity 是 runtime 的工作狀態。
type fakeActivity struct {
	mu        sync.Mutex
	busy      map[string]bool
	quiesced  []string
	connected map[string]bool // 引擎連線（Quiesce 關掉）
}

func (a *fakeActivity) HasWork(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busy[id]
}

func (a *fakeActivity) Quiesce(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy[id] {
		return false
	}
	a.quiesced = append(a.quiesced, id)
	delete(a.connected, id)
	return true
}

func (a *fakeActivity) set(id string, busy bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.busy[id] = busy
}

func TestIdleSleep(t *testing.T) {
	e := setup(t)
	var notes []Status
	e.m.notify = func(s Status) { notes = append(notes, s) }
	act := &fakeActivity{busy: map[string]bool{}, connected: map[string]bool{e.bot: true}}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	now := time.Now()

	// 還沒到 30 分鐘：不休眠。
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, act, now.Add(29*time.Minute))); len(slept) != 0 {
		t.Fatalf("還沒閒置夠久：%v", slept)
	}
	// Bot 有工作：不休眠，而且算是有在用（最後使用時間往後推）。
	act.set(e.bot, true)
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, act, now.Add(31*time.Minute))); len(slept) != 0 {
		t.Fatalf("有工作不能休眠：%v", slept)
	}
	act.set(e.bot, false)
	// 有人在看畫面（B14）：不休眠。
	release := e.m.Hold(e.bot)
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, act, now.Add(time.Hour))); len(slept) != 0 {
		t.Fatalf("有人在看不能休眠：%v", slept)
	}
	release()
	if s := ok[Status](t)(e.m.Status(ctx, e.bot)); !s.Running {
		t.Fatal("還不該休眠")
	}

	// 閒置超過 30 分鐘（從最後一次有工作或有人看算起）：休眠，先關掉引擎連線，停機時等 20 秒收尾。
	stops := len(e.docker.stops)
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	later := time.UnixMilli(row.LastActiveAt).Add(30*time.Minute + time.Second)
	slept := ok[[]string](t)(e.m.SleepIdle(ctx, act, later))
	if !slices.Equal(slept, []string{e.bot}) || !slices.Contains(act.quiesced, e.bot) || len(e.docker.stops) != stops+1 {
		t.Fatalf("閒置休眠：slept=%v quiesced=%v stops=%d", slept, act.quiesced, len(e.docker.stops))
	}
	s := ok[Status](t)(e.m.Status(ctx, e.bot))
	if s.Running || s.State != StateSleeping || s.ContainerID == "" {
		t.Fatalf("休眠後：%+v", s)
	}
	if n := len(notes); n == 0 || notes[n-1].State != StateSleeping {
		t.Fatalf("休眠要通知：%+v", notes)
	}
	// 已經休眠的不會再停一次。
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, act, later.Add(time.Hour))); len(slept) != 0 {
		t.Fatalf("已經休眠：%v", slept)
	}

	// 有工作時叫醒：同一台電腦、資料都在。
	s2 := ok[Status](t)(e.m.Start(ctx, e.bot))
	if !s2.Running || s2.ContainerID != s.ContainerID || e.creates() != 1 {
		t.Fatalf("叫醒：%+v", s2)
	}
	if n := len(notes); notes[n-1].State != StateRunning {
		t.Fatalf("叫醒要通知：%+v", notes[n-1])
	}
}

func TestIdleMinutesPerBot(t *testing.T) {
	e := setup(t)
	bot := ok[store.Bot](t)(e.db.GetBot(ctx, e.bot))
	bot.IdleMinutes = 5
	if _, err := e.db.UpdateBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, nil, time.Now().Add(6*time.Minute))); !slices.Equal(slept, []string{e.bot}) {
		t.Fatalf("5 分鐘就休眠：%v", slept)
	}
	if IdleFor(store.Bot{}) != 30*time.Minute {
		t.Fatal("預設 30 分鐘")
	}
}

func TestIdleQuiesceRace(t *testing.T) {
	// 檢查時沒工作、要停之前剛好來了工作（Quiesce 回 false）：不停。
	e := setup(t)
	ok[Status](t)(e.m.Start(ctx, e.bot))
	act := &fakeActivity{busy: map[string]bool{}}
	racy := &racyActivity{fakeActivity: act, bot: e.bot}
	if slept := ok[[]string](t)(e.m.SleepIdle(ctx, racy, time.Now().Add(time.Hour))); len(slept) != 0 {
		t.Fatalf("剛來了工作：%v", slept)
	}
	if s := ok[Status](t)(e.m.Status(ctx, e.bot)); !s.Running {
		t.Fatal("不該停")
	}
}

// racyActivity：HasWork 說沒有，Quiesce 時才發現有。
type racyActivity struct {
	*fakeActivity
	bot string
}

func (r *racyActivity) Quiesce(string) bool { return false }

func TestSleepRestartBusy(t *testing.T) {
	e := setup(t)
	act := &fakeActivity{busy: map[string]bool{e.bot: true}}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if _, err := e.m.Sleep(ctx, e.bot, act); !errors.Is(err, ErrBusy) {
		t.Fatalf("有工作不能休眠：%v", err)
	}
	if _, err := e.m.Restart(ctx, e.bot, act); !errors.Is(err, ErrBusy) {
		t.Fatalf("有工作不能重開：%v", err)
	}
	act.set(e.bot, false)
	if s := ok[Status](t)(e.m.Sleep(ctx, e.bot, act)); s.Running {
		t.Fatalf("休眠：%+v", s)
	}
	s := ok[Status](t)(e.m.Restart(ctx, e.bot, act))
	if !s.Running || e.creates() != 1 {
		t.Fatalf("重開：%+v", s)
	}
}

func TestTouchNeverGoesBack(t *testing.T) {
	e := setup(t)
	ok[Status](t)(e.m.Start(ctx, e.bot))
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	stale := row
	stale.LastActiveAt = 1
	if err := e.db.UpsertBox(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if got := ok[store.Box](t)(e.db.GetBox(ctx, e.bot)); got.LastActiveAt != row.LastActiveAt {
		t.Fatalf("舊的狀態寫回時不能把最後使用時間改回去：%d → %d", row.LastActiveAt, got.LastActiveAt)
	}
}

func TestReleaseForwards(t *testing.T) {
	e := setup(t)
	s := ok[Status](t)(e.m.Start(ctx, e.bot))
	old := []int{s.NoVNCPort, s.HermesPort}
	// 停機：舊埠的轉發放掉。
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	if !slices.Contains(e.host.released, old[0]) || !slices.Contains(e.host.released, old[1]) {
		t.Fatalf("停機要放掉轉發：%v（舊埠 %v）", e.host.released, old)
	}
	// 重建：舊埠放掉，新埠不放。
	s = ok[Status](t)(e.m.Start(ctx, e.bot))
	e.host.released = nil
	s2 := ok[Status](t)(e.m.Rebuild(ctx, e.bot))
	if !slices.Contains(e.host.released, s.NoVNCPort) || slices.Contains(e.host.released, s2.NoVNCPort) {
		t.Fatalf("重建：放掉 %v，舊 %d 新 %d", e.host.released, s.NoVNCPort, s2.NoVNCPort)
	}
	// 別台電腦正在用的埠不放。
	other := ok[store.Bot](t)(e.db.CreateBot(ctx, store.Bot{Name: "另一個"}))
	if err := e.db.UpsertBox(ctx, store.Box{BotID: other.ID, HostID: e.host.id, State: StateRunning, NoVNCPort: s2.NoVNCPort}); err != nil {
		t.Fatal(err)
	}
	e.host.released = nil
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	if slices.Contains(e.host.released, s2.NoVNCPort) {
		t.Fatalf("別台在用的埠不能放：%v", e.host.released)
	}
	// 連資料一起刪：主機上的 bots/<id>（遠端主機連反向轉發）一起清掉，服務這邊的 product.sock 先關。
	var released []string
	e.m.release = func(id string) { released = append(released, id) }
	ok[Status](t)(e.m.Remove(ctx, e.bot, false))
	if len(e.host.forgot) != 0 || len(released) != 0 {
		t.Fatal("只刪容器時不能清資料夾")
	}
	ok[Status](t)(e.m.Remove(ctx, e.bot, true))
	if !slices.Equal(e.host.forgot, []string{e.bot}) || !slices.Equal(released, []string{e.bot}) {
		t.Fatalf("purge：forgot=%v released=%v", e.host.forgot, released)
	}
}

func TestStaleLimitsRebuiltOnStart(t *testing.T) {
	e := setup(t)
	ok[Status](t)(e.m.Start(ctx, e.bot))
	ok[Status](t)(e.m.Stop(ctx, e.bot))
	row := ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	row.Limits = []byte(`{"cpus":1}`)
	if err := e.db.UpsertBox(ctx, row); err != nil {
		t.Fatal(err)
	}
	// 資源限制改了（BX-11）：下次開機前自動重建，資料保留。
	s := ok[Status](t)(e.m.Start(ctx, e.bot))
	if !s.Running || e.creates() != 2 || e.lastCreate().HostConfig.CPUQuota != 10000 || e.vols() != 4 {
		t.Fatalf("限制改了要重建：%+v creates=%d", s, e.creates())
	}
	// 在跑的電腦不會被重建（等它下次開機）。
	row = ok[store.Box](t)(e.db.GetBox(ctx, e.bot))
	row.Limits = []byte(`{"cpus":3}`)
	if err := e.db.UpsertBox(ctx, row); err != nil {
		t.Fatal(err)
	}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if e.creates() != 2 {
		t.Fatal("在跑的電腦不能突然重建")
	}
	if outdated := ok[bool](t)(e.m.Outdated(ctx, e.bot)); !outdated {
		t.Fatal("限制和容器不同時要標成要更新")
	}
}

func TestOutdatedImage(t *testing.T) {
	e := setup(t)
	if outdated := ok[bool](t)(e.m.Outdated(ctx, e.bot)); outdated {
		t.Fatal("沒有電腦時不用更新")
	}
	ok[Status](t)(e.m.Start(ctx, e.bot))
	if outdated := ok[bool](t)(e.m.Outdated(ctx, e.bot)); outdated {
		t.Fatal("剛建好不用更新")
	}
	e.docker.images[DefaultImage] = "sha256:image2" // make box 換了映像
	if outdated := ok[bool](t)(e.m.Outdated(ctx, e.bot)); !outdated {
		t.Fatal("映像換了要更新")
	}
	ok[Status](t)(e.m.Rebuild(ctx, e.bot))
	if outdated := ok[bool](t)(e.m.Outdated(ctx, e.bot)); outdated {
		t.Fatal("重建後就是新的")
	}
}
