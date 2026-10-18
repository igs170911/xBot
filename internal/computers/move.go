package computers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 搬家（CL-5，B31）：把 Bot 的電腦搬到另一台主機，登入狀態跟著走；也能把一台主機上的 Bot 全部搬到另一台（16.1a：
// 從驗證主機換到專用主機）。所有搬家排成一列，一次搬一台（同一條 SSH 連線、同一個 XBot 的頻寬）。
// 實際的搬法在 internal/box（Manager.Move）；這裡管排隊、紀錄（box_move）、進度事件、取消、服務重啟後的收尾，
// 以及保留期過了以後刪掉原主機上的舊資料。

// KindMove：搬家進度（payload：MoveStatus）。
const KindMove = "computers.move"

// 搬家的狀態（同 store.Move*）與步驟。
const (
	MoveQueued   = store.MoveQueued
	MoveRunning  = store.MoveRunning
	MoveDone     = store.MoveDone
	MoveFailed   = store.MoveFailed
	MoveCanceled = store.MoveCanceled

	// PhaseImage：先把電腦映像傳到新主機（新主機上沒有或比較舊時）。其餘步驟見 box.Phase*。
	PhaseImage = "image"
)

// MoveJob 是一次搬家。
type MoveJob struct {
	ID       string `json:"id"`
	BotID    string `json:"bot_id"`
	BotName  string `json:"bot_name"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
	To       string `json:"to"`
	ToName   string `json:"to_name"`
	State    string `json:"state" desc:"queued、running、done、failed、canceled"`
	Phase    string `json:"phase,omitempty" desc:"image（傳映像）、stop（停止電腦）、measure（量資料）、copy（傳資料）、verify（驗證）、switch（切換）、start（在新主機開機）"`
	Volume   string `json:"volume,omitempty" desc:"正在傳的資料：home、workspace、hermes"`
	// Percent 是整體進度（0～100）；Done、Total 是已傳與全部的位元組（Total 是估計值）。
	Percent int    `json:"percent"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Files   int    `json:"files"`
	Error   string `json:"error,omitempty"`
	// Note 是搬好了但要注意的事（例：在新主機開機失敗）。
	Note string `json:"note,omitempty"`
	// KeepUntil 是原主機上的舊資料保留到什麼時候（毫秒；0＝沒有留）。
	KeepUntil  int64 `json:"keep_until,omitempty"`
	Cleaned    bool  `json:"cleaned,omitempty" desc:"原主機上的舊資料已經刪掉"`
	CreatedAt  int64 `json:"created_at"`
	FinishedAt int64 `json:"finished_at,omitempty"`
}

// Active：排隊中或進行中。
func (j MoveJob) Active() bool { return j.State == MoveQueued || j.State == MoveRunning }

// MoveStatus 是所有搬家的進度：進行中與排隊中的，加上最近做完的。
type MoveStatus struct {
	Running bool      `json:"running" desc:"有搬家正在進行或排隊"`
	Jobs    []MoveJob `json:"jobs"`
}

// 搬家的錯誤。
var (
	ErrMoveQueued  = errors.New("這個 Bot 已經在排隊搬家了")
	ErrHostOffline = errors.New("主機不在線上")
	ErrNoMove      = errors.New("沒有可以取消的搬家")
)

// mover 是搬家的排隊與進度（Service 的一部分）。
type mover struct {
	mu      sync.Mutex
	jobs    []*MoveJob // 排隊中與進行中（依順序）
	cancel  map[string]context.CancelFunc
	running bool // 有一個 goroutine 正在依序處理
	// drop 刪掉原主機上的舊資料；nil 時用 Boxes.DropMoved（測試可換掉）。
	drop func(ctx context.Context, hostID, botID string) error
}

func (s *Service) hostName(ctx context.Context, id string) string {
	if row, err := s.DB.GetHost(ctx, id); err == nil {
		return row.Name
	}
	return id
}

// whereIs 回傳 Bot 的電腦現在在哪台主機（有電腦時以電腦為準，否則 bot.host_id，都沒有時是預設主機）。
func (s *Service) whereIs(ctx context.Context, botID string) (string, error) {
	if b, err := s.DB.GetBox(ctx, botID); err == nil && b.HostID != "" {
		return b.HostID, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	bot, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return "", err
	}
	if bot.HostID != "" {
		return bot.HostID, nil
	}
	all, err := s.DB.ListHosts(ctx)
	if err != nil {
		return "", err
	}
	for _, h := range all {
		if h.Kind == "local" {
			return h.ID, nil
		}
	}
	return "", store.ErrNotFound
}

func online(row store.Host) bool { return row.Kind == "local" || row.Status == store.HostOnline }

// planMove 檢查能不能把 botID 搬到 to，回傳要排進去的工作（還沒寫進資料庫）。
func (s *Service) planMove(ctx context.Context, botID, to string) (MoveJob, error) {
	bot, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return MoveJob{}, err
	}
	from, err := s.whereIs(ctx, botID)
	if err != nil {
		return MoveJob{}, err
	}
	dst, err := s.DB.GetHost(ctx, to)
	if err != nil {
		return MoveJob{}, err
	}
	j := MoveJob{BotID: botID, BotName: bot.Name, From: from, FromName: s.hostName(ctx, from), To: to, ToName: dst.Name, State: MoveQueued}
	if from == to {
		return j, box.ErrSameHost
	}
	if !online(dst) {
		return j, fmt.Errorf("%w：%s（等它回來再搬）", ErrHostOffline, dst.Name)
	}
	if src, err := s.DB.GetHost(ctx, from); err == nil && !online(src) {
		return j, fmt.Errorf("%w：%s 上的資料要等它回來才能打包", ErrHostOffline, src.Name)
	}
	if s.queued(botID) {
		return j, ErrMoveQueued
	}
	return j, nil
}

func (s *Service) queued(botID string) bool {
	s.mv.mu.Lock()
	defer s.mv.mu.Unlock()
	for _, j := range s.mv.jobs {
		if j.BotID == botID {
			return true
		}
	}
	return false
}

// PlanMove 是 xbot.computers.move 的預演：檢查能不能搬，不動任何東西。
func (s *Service) PlanMove(ctx context.Context, botID, to string) (MoveStatus, error) {
	j, err := s.planMove(ctx, botID, to)
	if err == nil && s.busy(botID) {
		err = box.ErrBusy
	}
	return MoveStatus{Jobs: []MoveJob{j}}, err
}

// Move 把一個 Bot 排進搬家（Bot 正在工作時拒絕），回傳目前的進度。
func (s *Service) Move(ctx context.Context, botID, to string) (MoveStatus, error) {
	j, err := s.planMove(ctx, botID, to)
	if err != nil {
		return MoveStatus{}, err
	}
	if s.busy(botID) {
		return MoveStatus{}, box.ErrBusy
	}
	if err := s.enqueue(ctx, []MoveJob{j}); err != nil {
		return MoveStatus{}, err
	}
	return s.MoveStatus(ctx, "")
}

// PlanMoveAll 列出把 from 上所有 Bot 搬到 to 的計畫（16.1a）：電腦在 from 上的，以及指定放在 from、還沒有電腦的。
func (s *Service) PlanMoveAll(ctx context.Context, from, to string) ([]MoveJob, error) {
	if from == to {
		return nil, box.ErrSameHost
	}
	src, err := s.DB.GetHost(ctx, from)
	if err != nil {
		return nil, err
	}
	dst, err := s.DB.GetHost(ctx, to)
	if err != nil {
		return nil, err
	}
	if !online(dst) {
		return nil, fmt.Errorf("%w：%s（等它回來再搬）", ErrHostOffline, dst.Name)
	}
	if !online(src) {
		return nil, fmt.Errorf("%w：%s 上的資料要等它回來才能打包", ErrHostOffline, src.Name)
	}
	bots, err := s.DB.ListBots(ctx, true)
	if err != nil {
		return nil, err
	}
	var out []MoveJob
	for _, b := range bots {
		where, err := s.whereIs(ctx, b.ID)
		if err != nil || where != from {
			continue
		}
		if b.HostID == "" {
			// 還沒擺放的 Bot 沒有固定在 from（之後會自動擺放），不用搬。
			if _, err := s.DB.GetBox(ctx, b.ID); errors.Is(err, store.ErrNotFound) {
				continue
			}
		}
		if s.queued(b.ID) {
			continue
		}
		out = append(out, MoveJob{BotID: b.ID, BotName: b.Name, From: from, FromName: src.Name, To: to, ToName: dst.Name, State: MoveQueued})
	}
	return out, nil
}

// MoveAll 把 from 上所有 Bot 依序搬到 to。正在工作的 Bot 輪到它時跳過（記成失敗，可以之後再搬）。
func (s *Service) MoveAll(ctx context.Context, from, to string) (MoveStatus, error) {
	jobs, err := s.PlanMoveAll(ctx, from, to)
	if err != nil {
		return MoveStatus{}, err
	}
	if err := s.enqueue(ctx, jobs); err != nil {
		return MoveStatus{}, err
	}
	return s.MoveStatus(ctx, "")
}

// enqueue 把工作寫進資料庫並排進去，必要時開始處理。
func (s *Service) enqueue(ctx context.Context, jobs []MoveJob) error {
	var added []*MoveJob
	for _, j := range jobs {
		row, err := s.DB.CreateMove(ctx, store.BoxMove{BotID: j.BotID, FromHost: j.From, ToHost: j.To})
		if err != nil {
			return err
		}
		j.ID, j.CreatedAt = row.ID, row.CreatedAt
		added = append(added, &j)
	}
	s.mv.mu.Lock()
	s.mv.jobs = append(s.mv.jobs, added...)
	start := !s.mv.running && len(s.mv.jobs) > 0
	if start {
		s.mv.running = true
	}
	s.mv.mu.Unlock()
	s.publishMove(false)
	if start {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runMoves()
		}()
	}
	return nil
}

// runMoves 依序處理排隊的搬家，直到沒有了。
func (s *Service) runMoves() {
	for {
		s.mv.mu.Lock()
		var j *MoveJob
		for _, cand := range s.mv.jobs {
			if cand.State == MoveQueued {
				j = cand
				break
			}
		}
		if j == nil {
			s.mv.running = false
			s.mv.mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), limits.BoxMoveTimeout)
		if s.mv.cancel == nil {
			s.mv.cancel = map[string]context.CancelFunc{}
		}
		s.mv.cancel[j.ID] = cancel
		j.State = MoveRunning
		s.mv.mu.Unlock()
		s.runMove(ctx, j)
		cancel()
		s.mv.mu.Lock()
		delete(s.mv.cancel, j.ID)
		for i, cand := range s.mv.jobs {
			if cand == j {
				s.mv.jobs = append(s.mv.jobs[:i], s.mv.jobs[i+1:]...)
				break
			}
		}
		s.mv.mu.Unlock()
		s.publishMove(true)
	}
}

// update 改一個工作並發進度事件。
func (s *Service) update(j *MoveJob, f func(j *MoveJob)) {
	s.mv.mu.Lock()
	f(j)
	s.mv.mu.Unlock()
	s.publishMove(false)
}

func (s *Service) runMove(ctx context.Context, j *MoveJob) {
	row, err := s.DB.GetMove(ctx, j.ID)
	if err != nil {
		s.log().Warn("搬家紀錄不見了", "id", j.ID, "err", err)
		return
	}
	row.State = MoveRunning
	_ = s.DB.UpdateMove(ctx, row)
	s.publishMove(false)
	finish := func(state, msg string) {
		s.update(j, func(j *MoveJob) {
			j.State, j.Error, j.FinishedAt = state, msg, time.Now().UnixMilli()
			if state == MoveDone {
				j.Percent, j.Phase, j.Volume = 100, "", ""
			}
		})
		row.State, row.Error, row.FinishedAt = state, msg, j.FinishedAt
		row.Files, row.Bytes, row.KeepUntil = j.Files, j.Total, j.KeepUntil
		if err := s.DB.UpdateMove(context.WithoutCancel(ctx), row); err != nil {
			s.log().Warn("寫入搬家紀錄失敗", "id", j.ID, "err", err)
		}
	}
	// 輪到時再確認一次：Bot 有工作就不搬（關掉引擎連線要和「收下新工作」互斥，見 Activity.Quiesce）。
	if s.Activity != nil && !s.Activity.Quiesce(j.BotID) {
		finish(MoveFailed, box.ErrBusy.Error())
		return
	}
	if s.Hosts != nil {
		s.update(j, func(j *MoveJob) { j.Phase = PhaseImage })
		if err := s.Hosts.EnsureImage(ctx, j.To); err != nil {
			finish(s.failState(ctx), "新主機上沒有電腦映像："+err.Error())
			return
		}
	}
	res, err := s.Boxes.Move(ctx, j.BotID, j.To, func(p box.MoveProgress) {
		s.update(j, func(j *MoveJob) {
			j.Phase, j.Volume, j.Done, j.Total = p.Phase, p.Volume, p.Done, max(p.Total, p.Done)
			j.Percent = movePercent(p)
		})
	})
	if err != nil {
		s.log().Warn("搬家失敗，Bot 留在原主機", "bot", j.BotID, "from", j.From, "to", j.To, "err", err)
		finish(s.failState(ctx), err.Error())
		return
	}
	s.update(j, func(j *MoveJob) {
		j.Files = res.Files
		j.Total, j.Done = res.Bytes, res.Bytes
		if res.Kept {
			j.KeepUntil = time.Now().Add(limits.BoxMoveKeep).UnixMilli()
		}
		if res.StartErr != nil {
			j.Note = "資料已經搬好，但在新主機開機失敗：" + res.StartErr.Error()
		}
	})
	// 新主機上更早搬走後留下的舊資料，已經被這次搬家清掉了。
	if kept, err := s.DB.KeptMoves(ctx, j.BotID, j.To); err == nil {
		for _, k := range kept {
			k.CleanedAt = time.Now().UnixMilli()
			_ = s.DB.UpdateMove(ctx, k)
		}
	}
	s.log().Info("搬家完成", "bot", j.BotID, "from", j.From, "to", j.To, "files", res.Files, "bytes", res.Bytes)
	finish(MoveDone, "")
}

// failState：被取消時是 canceled，否則 failed。
func (s *Service) failState(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return MoveCanceled
	}
	return MoveFailed
}

// movePercent 把各步驟換算成整體進度：傳資料佔 5～90，驗證 90～97，切換與開機 97～99。
func movePercent(p box.MoveProgress) int {
	switch p.Phase {
	case box.PhaseStop:
		return 2
	case box.PhaseMeasure:
		return 4
	case box.PhaseCopy:
		if p.Total <= 0 {
			return 5
		}
		return 5 + int(min(1, float64(p.Done)/float64(p.Total))*85)
	case box.PhaseVerify:
		return 90
	case box.PhaseSwitch:
		return 97
	case box.PhaseStart:
		return 99
	}
	return 0
}

// CancelMove 取消一個 Bot 排隊中或進行中的搬家（還沒切換到新主機前都會復原，原主機的資料不動）。
func (s *Service) CancelMove(ctx context.Context, botID string) (MoveStatus, error) {
	s.mv.mu.Lock()
	var found *MoveJob
	for _, j := range s.mv.jobs {
		if j.BotID == botID {
			found = j
			break
		}
	}
	if found == nil {
		s.mv.mu.Unlock()
		return MoveStatus{}, ErrNoMove
	}
	if found.State == MoveRunning {
		if c := s.mv.cancel[found.ID]; c != nil {
			c()
		}
		s.mv.mu.Unlock()
		return s.MoveStatus(ctx, "")
	}
	for i, j := range s.mv.jobs {
		if j == found {
			s.mv.jobs = append(s.mv.jobs[:i], s.mv.jobs[i+1:]...)
			break
		}
	}
	s.mv.mu.Unlock()
	if row, err := s.DB.GetMove(ctx, found.ID); err == nil {
		row.State, row.FinishedAt = MoveCanceled, time.Now().UnixMilli()
		_ = s.DB.UpdateMove(ctx, row)
	}
	s.publishMove(true)
	return s.MoveStatus(ctx, "")
}

// MoveStatus 回傳進行中、排隊中與最近做完的搬家（botID 不是空白時只看那個 Bot）。
func (s *Service) MoveStatus(ctx context.Context, botID string) (MoveStatus, error) {
	st := MoveStatus{Jobs: []MoveJob{}}
	seen := map[string]bool{}
	s.mv.mu.Lock()
	for _, j := range s.mv.jobs {
		if botID == "" || j.BotID == botID {
			st.Jobs = append(st.Jobs, *j)
			seen[j.ID] = true
			st.Running = true
		}
	}
	s.mv.mu.Unlock()
	rows, err := s.DB.ListMoves(ctx, botID, limits.BoxMoveHistory)
	if err != nil {
		return st, err
	}
	for _, r := range rows {
		if seen[r.ID] || r.State == MoveQueued || r.State == MoveRunning {
			continue
		}
		st.Jobs = append(st.Jobs, s.jobOf(ctx, r))
	}
	return st, nil
}

func (s *Service) jobOf(ctx context.Context, r store.BoxMove) MoveJob {
	j := MoveJob{ID: r.ID, BotID: r.BotID, BotName: botName(ctx, s.DB, r.BotID), From: r.FromHost, FromName: s.hostName(ctx, r.FromHost),
		To: r.ToHost, ToName: s.hostName(ctx, r.ToHost), State: r.State, Error: r.Error, Files: r.Files, Total: r.Bytes, Done: r.Bytes,
		KeepUntil: r.KeepUntil, Cleaned: r.CleanedAt > 0, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
	if r.State == MoveDone {
		j.Percent = 100
	}
	return j
}

// lastMove 回傳 Bot 進行中的搬家，或最近一次搬家（給電腦面板顯示）；沒有時 nil。
func (s *Service) lastMove(ctx context.Context, botID string) *MoveJob {
	st, err := s.MoveStatus(ctx, botID)
	if err != nil || len(st.Jobs) == 0 {
		return nil
	}
	j := st.Jobs[0]
	return &j
}

func (s *Service) publishMove(persist bool) {
	st, err := s.MoveStatus(context.Background(), "")
	if err != nil {
		return
	}
	Publish(s.DB, s.Bus, KindMove, "", st, persist)
}

// Close 停下所有搬家（進行中的會復原，Bot 留在原主機；排隊中的取消）並等背景工作結束。關閉服務時用。
func (s *Service) Close() {
	s.mv.mu.Lock()
	var queued []string
	keep := s.mv.jobs[:0]
	for _, j := range s.mv.jobs {
		if j.State == MoveQueued {
			queued = append(queued, j.ID)
			continue
		}
		keep = append(keep, j)
	}
	s.mv.jobs = keep
	for _, c := range s.mv.cancel {
		c()
	}
	s.mv.mu.Unlock()
	ctx := context.Background()
	for _, id := range queued {
		if row, err := s.DB.GetMove(ctx, id); err == nil {
			row.State, row.Error, row.FinishedAt = MoveCanceled, "服務關閉", time.Now().UnixMilli()
			_ = s.DB.UpdateMove(ctx, row)
		}
	}
	s.Wait()
}

// RecoverMoves 在服務啟動時收尾上次沒做完的搬家：已經切換到新主機的算完成（舊資料照樣保留），其餘算失敗
// （切換前中斷，原主機的資料沒動；新主機上做到一半的東西，下次搬到那台時會先清掉）。
func (s *Service) RecoverMoves(ctx context.Context) error {
	rows, err := s.DB.UnfinishedMoves(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, r := range rows {
		where, _ := s.whereIs(ctx, r.BotID)
		r.FinishedAt = now.UnixMilli()
		if r.State == MoveRunning && where == r.ToHost {
			r.State, r.KeepUntil = MoveDone, now.Add(limits.BoxMoveKeep).UnixMilli()
		} else {
			r.State, r.Error = MoveFailed, "服務重新啟動，搬家中斷；原主機的資料沒有動，可以再搬一次"
		}
		if err := s.DB.UpdateMove(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// CleanupMoves 刪掉保留期已過、還留在原主機上的舊資料，回傳刪了幾筆。主機暫時連不上的下次再試；
// 主機已經移除的直接記成刪掉（那台機器上的東西 XBot 管不到了）。
func (s *Service) CleanupMoves(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.DB.ExpiredMoves(ctx, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	drop := s.mv.drop
	if drop == nil {
		drop = s.Boxes.DropMoved
	}
	n := 0
	var errs []error
	for _, r := range rows {
		err := drop(ctx, r.FromHost, r.BotID)
		if errors.Is(err, hosts.ErrUnknownHost) {
			if _, gerr := s.DB.GetHost(ctx, r.FromHost); !errors.Is(gerr, store.ErrNotFound) {
				continue // 主機還在，只是現在連不上
			}
			err = nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", r.BotID, err))
			continue
		}
		r.CleanedAt = now.UnixMilli()
		if err := s.DB.UpdateMove(ctx, r); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// RunMoveCleanup 每 limits.BoxMoveCleanupEvery 執行一次 CleanupMoves，直到 ctx 結束。
func (s *Service) RunMoveCleanup(ctx context.Context) {
	t := time.NewTicker(limits.BoxMoveCleanupEvery)
	defer t.Stop()
	for {
		if n, err := s.CleanupMoves(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log().Warn("刪除搬家後留下的舊資料失敗，稍後再試", "err", err)
		} else if n > 0 {
			s.log().Info("已刪除搬家後保留期已過的舊資料", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
