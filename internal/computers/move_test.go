package computers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type moveFixture struct {
	db                   *store.DB
	svc                  *Service
	local, cloud, office store.Host
}

func newMoveFixture(t *testing.T) moveFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host := func(name, kind, status string) store.Host {
		h, err := db.CreateHost(ctx, store.Host{Name: name, Kind: kind, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	f := moveFixture{db: db, svc: &Service{DB: db}}
	f.local = host("這台主機", "local", store.HostOnline)
	f.cloud = host("cloud-01", "ssh", store.HostOnline)
	f.office = host("office-pc", "ssh", store.HostOffline)
	return f
}

func (f moveFixture) bot(t *testing.T, name, hostID string, withBox bool) store.Bot {
	t.Helper()
	ctx := context.Background()
	b, err := f.db.CreateBot(ctx, store.Bot{Name: name, HostID: hostID})
	if err != nil {
		t.Fatal(err)
	}
	if withBox {
		if err := f.db.UpsertBox(ctx, store.Box{BotID: b.ID, HostID: hostID, State: box.StateSleeping}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// 搬家前的檢查：同一台、主機離線、已經在排隊都要擋；電腦在哪台以電腦為準。
func TestPlanMove(t *testing.T) {
	ctx := context.Background()
	f := newMoveFixture(t)
	a := f.bot(t, "阿凱", f.local.ID, true)
	st, err := f.svc.PlanMove(ctx, a.ID, f.cloud.ID)
	if err != nil || len(st.Jobs) != 1 {
		t.Fatal(st, err)
	}
	j := st.Jobs[0]
	if j.From != f.local.ID || j.FromName != "這台主機" || j.ToName != "cloud-01" || j.BotName != "阿凱" || j.State != MoveQueued {
		t.Fatalf("計畫：%+v", j)
	}
	if _, err := f.svc.PlanMove(ctx, a.ID, f.local.ID); !errors.Is(err, box.ErrSameHost) {
		t.Fatalf("同一台：%v", err)
	}
	if _, err := f.svc.PlanMove(ctx, a.ID, f.office.ID); !errors.Is(err, ErrHostOffline) {
		t.Fatalf("新主機離線：%v", err)
	}
	b := f.bot(t, "小編", f.office.ID, true)
	if _, err := f.svc.PlanMove(ctx, b.ID, f.cloud.ID); !errors.Is(err, ErrHostOffline) {
		t.Fatalf("原主機離線（資料要等它回來才能打包）：%v", err)
	}
	// 電腦已經在 cloud-01（bot.host_id 還是舊的）：以電腦為準。
	c := f.bot(t, "數據喵", f.local.ID, false)
	if err := f.db.UpsertBox(ctx, store.Box{BotID: c.ID, HostID: f.cloud.ID, State: box.StateSleeping}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.PlanMove(ctx, c.ID, f.cloud.ID); !errors.Is(err, box.ErrSameHost) {
		t.Fatalf("以電腦所在主機為準：%v", err)
	}
	f.svc.mv.jobs = []*MoveJob{{ID: "x", BotID: a.ID, State: MoveQueued}}
	if _, err := f.svc.PlanMove(ctx, a.ID, f.cloud.ID); !errors.Is(err, ErrMoveQueued) {
		t.Fatalf("已經在排隊：%v", err)
	}
	for _, e := range []error{box.ErrSameHost, ErrHostOffline, ErrMoveQueued, box.ErrMoving, ErrNoMove} {
		if got := ops.AsError(opErr(e)); got == nil || got.Code != ops.CodeConflict {
			t.Errorf("%v → %+v", e, got)
		}
	}
	if got := ops.AsError(opErr(box.ErrEgressNotSet)); got == nil || got.Code != ops.CodeInvalidInput {
		t.Errorf("沒設定 exit node：%+v", got)
	}
}

// 全部搬走（16.1a）：電腦在這台的、指定放這台還沒有電腦的都要搬；還沒擺放的、在別台的、已經排隊的不搬。
func TestPlanMoveAll(t *testing.T) {
	ctx := context.Background()
	f := newMoveFixture(t)
	a := f.bot(t, "阿凱", f.local.ID, true)
	b := f.bot(t, "小秘", f.local.ID, false) // 指定放這台、還沒有電腦
	f.bot(t, "沒擺放", "", false)             // 還沒擺放：之後自動擺放，不搬
	f.bot(t, "雲端的", f.cloud.ID, true)
	d := f.bot(t, "排隊中", f.local.ID, true)
	f.svc.mv.jobs = []*MoveJob{{ID: "x", BotID: d.ID, State: MoveQueued}}
	jobs, err := f.svc.PlanMoveAll(ctx, f.local.ID, f.cloud.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range jobs {
		got[j.BotID] = true
		if j.From != f.local.ID || j.To != f.cloud.ID || j.ToName != "cloud-01" {
			t.Fatalf("計畫：%+v", j)
		}
	}
	if len(jobs) != 2 || !got[a.ID] || !got[b.ID] {
		t.Fatalf("要搬阿凱與小秘：%+v", jobs)
	}
	if _, err := f.svc.PlanMoveAll(ctx, f.local.ID, f.local.ID); !errors.Is(err, box.ErrSameHost) {
		t.Fatal(err)
	}
	if _, err := f.svc.PlanMoveAll(ctx, f.local.ID, f.office.ID); !errors.Is(err, ErrHostOffline) {
		t.Fatal(err)
	}
}

func TestMovePercent(t *testing.T) {
	cases := []struct {
		p    box.MoveProgress
		want int
	}{
		{box.MoveProgress{Phase: box.PhaseStop}, 2},
		{box.MoveProgress{Phase: box.PhaseCopy, Done: 0, Total: 0}, 5},
		{box.MoveProgress{Phase: box.PhaseCopy, Done: 50, Total: 100}, 47},
		{box.MoveProgress{Phase: box.PhaseCopy, Done: 200, Total: 100}, 90},
		{box.MoveProgress{Phase: box.PhaseVerify}, 90},
		{box.MoveProgress{Phase: box.PhaseSwitch}, 97},
	}
	for _, c := range cases {
		if got := movePercent(c.p); got != c.want {
			t.Errorf("%+v → %d，想要 %d", c.p, got, c.want)
		}
	}
}

// 保留期過了才刪原主機上的舊資料；主機暫時連不上的下次再試，主機已移除的直接記成刪掉。
func TestCleanupMoves(t *testing.T) {
	ctx := context.Background()
	f := newMoveFixture(t)
	a := f.bot(t, "阿凱", f.cloud.ID, true)
	now := time.Now()
	mk := func(from string, keep time.Duration, state string) store.BoxMove {
		m, err := f.db.CreateMove(ctx, store.BoxMove{BotID: a.ID, FromHost: from, ToHost: f.cloud.ID, State: state})
		if err != nil {
			t.Fatal(err)
		}
		m.KeepUntil = now.Add(keep).UnixMilli()
		if err := f.db.UpdateMove(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	expired := mk(f.local.ID, -time.Minute, store.MoveDone)
	kept := mk(f.local.ID, time.Hour, store.MoveDone)
	failed := mk(f.local.ID, -time.Minute, store.MoveFailed)
	offline := mk(f.office.ID, -time.Minute, store.MoveDone)
	gone := mk("removed-host", -time.Minute, store.MoveDone)
	var dropped []string
	f.svc.mv.drop = func(_ context.Context, hostID, botID string) error {
		if botID != a.ID {
			t.Fatalf("bot %s", botID)
		}
		if hostID == f.office.ID || hostID == "removed-host" {
			return hosts.ErrUnknownHost
		}
		dropped = append(dropped, hostID)
		return nil
	}
	n, err := f.svc.CleanupMoves(ctx, now)
	if err != nil || n != 2 || len(dropped) != 1 || dropped[0] != f.local.ID {
		t.Fatalf("刪了 %d 筆 %v：%v", n, dropped, err)
	}
	check := func(m store.BoxMove, cleaned bool) {
		t.Helper()
		got, err := f.db.GetMove(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (got.CleanedAt > 0) != cleaned {
			t.Errorf("%s 刪掉=%v，想要 %v", m.FromHost, got.CleanedAt > 0, cleaned)
		}
	}
	check(expired, true)
	check(kept, false)
	check(failed, false)
	check(offline, false)
	check(gone, true)
	// 再跑一次：已經刪過的不會再刪。
	dropped = nil
	if n, err := f.svc.CleanupMoves(ctx, now); err != nil || n != 0 || len(dropped) != 0 {
		t.Fatalf("重跑：%d %v %v", n, dropped, err)
	}
	// 刪失敗的回錯誤、不記成刪掉。
	mk(f.local.ID, -time.Second, store.MoveDone)
	f.svc.mv.drop = func(context.Context, string, string) error { return errors.New("docker 壞了") }
	if n, err := f.svc.CleanupMoves(ctx, now); err == nil || n != 0 {
		t.Fatalf("失敗：%d %v", n, err)
	}
	if rows, _ := f.db.KeptMoves(ctx, a.ID, f.local.ID); len(rows) != 2 {
		t.Fatalf("還留著的：%+v", rows)
	}
}

// 服務重新啟動：已經切換過去的算完成（舊資料照樣保留 24 小時），其餘算失敗（原主機的資料沒動）。
func TestRecoverMoves(t *testing.T) {
	ctx := context.Background()
	f := newMoveFixture(t)
	switched := f.bot(t, "已切換", f.cloud.ID, true)
	stuck := f.bot(t, "中斷", f.local.ID, true)
	queued := f.bot(t, "排隊", f.local.ID, true)
	mk := func(b store.Bot, state string) store.BoxMove {
		m, err := f.db.CreateMove(ctx, store.BoxMove{BotID: b.ID, FromHost: f.local.ID, ToHost: f.cloud.ID, State: state})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m1, m2, m3 := mk(switched, store.MoveRunning), mk(stuck, store.MoveRunning), mk(queued, store.MoveQueued)
	if err := f.svc.RecoverMoves(ctx); err != nil {
		t.Fatal(err)
	}
	g1, _ := f.db.GetMove(ctx, m1.ID)
	g2, _ := f.db.GetMove(ctx, m2.ID)
	g3, _ := f.db.GetMove(ctx, m3.ID)
	if g1.State != store.MoveDone || g1.KeepUntil < time.Now().Add(limits.BoxMoveKeep-time.Minute).UnixMilli() {
		t.Fatalf("已切換：%+v", g1)
	}
	if g2.State != store.MoveFailed || g2.Error == "" || g3.State != store.MoveFailed {
		t.Fatalf("中斷：%+v %+v", g2, g3)
	}
	if rows, _ := f.db.UnfinishedMoves(ctx); len(rows) != 0 {
		t.Fatal(rows)
	}
	st, err := f.svc.MoveStatus(ctx, "")
	if err != nil || st.Running || len(st.Jobs) != 3 {
		t.Fatalf("紀錄：%+v %v", st, err)
	}
	for _, j := range st.Jobs {
		if j.BotID == switched.ID && (j.Percent != 100 || j.KeepUntil == 0 || j.ToName != "cloud-01") {
			t.Fatalf("完成的搬家：%+v", j)
		}
	}
}

// 取消排隊中的搬家：拿掉並記成 canceled。
func TestCancelQueuedMove(t *testing.T) {
	ctx := context.Background()
	f := newMoveFixture(t)
	a := f.bot(t, "阿凱", f.local.ID, true)
	row, err := f.db.CreateMove(ctx, store.BoxMove{BotID: a.ID, FromHost: f.local.ID, ToHost: f.cloud.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.mv.jobs = []*MoveJob{{ID: row.ID, BotID: a.ID, State: MoveQueued}}
	if _, err := f.svc.CancelMove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetMove(ctx, row.ID); got.State != store.MoveCanceled || len(f.svc.mv.jobs) != 0 {
		t.Fatalf("取消：%+v", got)
	}
	if _, err := f.svc.CancelMove(ctx, a.ID); !errors.Is(err, ErrNoMove) {
		t.Fatal(err)
	}
}
