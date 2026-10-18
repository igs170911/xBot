package computers

import (
	"context"
	"errors"
	"time"

	"xbot/internal/box"
	"xbot/internal/store"
)

// 「更新電腦」（BX-15，B12 待決定事項「映像過期」）：make box 換了映像以後，主機清單標出 image_outdated、
// 電腦標出 outdated。這個背景工作一台主機做完再做下一台：先把新映像傳到那台（遠端主機），再把上面要更新的電腦
// 一台一台重建（停機 → 用新映像重建容器 → 原本在跑的再開機；home／workspace／hermes／cred 四個資料區都保留）。
// 正在工作的 Bot 跳過（等它做完再更新一次）。進度發 computers.update 事件。

// 一台主機、一台電腦在更新中的狀態。
const (
	StatePending  = "pending"
	StateImage    = "image"   // 正在把新映像傳到主機
	StateRunning  = "running" // 正在重建這台主機上的電腦
	StateDone     = "done"
	StateFailed   = "failed"
	StateSkipped  = "skipped"
	StateRebuilt  = "rebuilt"
	StateUpToDate = "up_to_date"
)

// BotUpdate 是一台電腦的更新結果。
type BotUpdate struct {
	BotID string `json:"bot_id"`
	Name  string `json:"name"`
	State string `json:"state" desc:"pending、running、rebuilt、skipped、failed"`
	// Reason 是跳過或失敗的原因。
	Reason string `json:"reason,omitempty"`
}

// HostUpdate 是一台主機的更新進度。
type HostUpdate struct {
	HostID        string      `json:"host_id"`
	Name          string      `json:"name"`
	ImageOutdated bool        `json:"image_outdated" desc:"要先把新映像傳到這台主機"`
	State         string      `json:"state" desc:"pending、image、running、done、failed、skipped"`
	Error         string      `json:"error,omitempty"`
	Bots          []BotUpdate `json:"bots"`
}

// Progress 是一次「更新電腦」的進度（也是預演的計畫）。
type Progress struct {
	Running    bool         `json:"running"`
	StartedAt  int64        `json:"started_at,omitempty"`
	FinishedAt int64        `json:"finished_at,omitempty"`
	Hosts      []HostUpdate `json:"hosts"`
	// Total 是要重建的電腦數，Done 是已經處理完（重建、跳過或失敗）的數量。
	Total int `json:"total"`
	Done  int `json:"done"`
	// Current 是正在處理的電腦（Bot 名稱）。
	Current string `json:"current,omitempty"`
}

// ErrUpdateRunning 表示已經有一次「更新電腦」在進行。
var ErrUpdateRunning = errors.New("已經在更新電腦了，等它做完")

// Plan 列出要更新的主機與電腦（hostID 空白＝全部主機）。
func (s *Service) Plan(ctx context.Context, hostID string) (Progress, error) {
	var rows []store.Host
	if hostID != "" {
		row, err := s.DB.GetHost(ctx, hostID)
		if err != nil {
			return Progress{}, err
		}
		rows = []store.Host{row}
	} else {
		all, err := s.DB.ListHosts(ctx)
		if err != nil {
			return Progress{}, err
		}
		rows = all
	}
	outdatedImage := map[string]bool{}
	if s.Hosts != nil {
		views, err := s.Hosts.List(ctx)
		if err != nil {
			return Progress{}, err
		}
		for _, v := range views {
			outdatedImage[v.ID] = v.ImageOutdated
		}
	}
	p := Progress{Hosts: []HostUpdate{}}
	for _, row := range rows {
		hu := HostUpdate{HostID: row.ID, Name: row.Name, ImageOutdated: outdatedImage[row.ID], State: StatePending, Bots: []BotUpdate{}}
		if row.Kind != "local" && row.Status != store.HostOnline {
			hu.State, hu.Error = StateSkipped, "主機不在線上，恢復後再更新"
			p.Hosts = append(p.Hosts, hu)
			continue
		}
		boxes, err := s.DB.ListBoxes(ctx, row.ID)
		if err != nil {
			return Progress{}, err
		}
		for _, b := range boxes {
			if b.ContainerID == "" && b.State == box.StateNone {
				continue
			}
			outdated := hu.ImageOutdated
			if !outdated {
				o, err := s.Boxes.Outdated(ctx, b.BotID)
				if err != nil {
					hu.Bots = append(hu.Bots, BotUpdate{BotID: b.BotID, Name: botName(ctx, s.DB, b.BotID), State: StateFailed, Reason: err.Error()})
					continue
				}
				outdated = o
			}
			if outdated {
				hu.Bots = append(hu.Bots, BotUpdate{BotID: b.BotID, Name: botName(ctx, s.DB, b.BotID), State: StatePending})
				p.Total++
			}
		}
		if len(hu.Bots) == 0 && !hu.ImageOutdated {
			hu.State = StateUpToDate
		}
		p.Hosts = append(p.Hosts, hu)
	}
	return p, nil
}

// Progress 回傳目前（或最近一次）「更新電腦」的進度；沒有時 ok 是 false。
func (s *Service) Progress() (Progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return Progress{}, false
	}
	return clone(*s.job), true
}

func clone(p Progress) Progress {
	p.Hosts = append([]HostUpdate(nil), p.Hosts...)
	for i := range p.Hosts {
		p.Hosts[i].Bots = append([]BotUpdate(nil), p.Hosts[i].Bots...)
	}
	return p
}

// Update 在背景開始「更新電腦」（hostID 空白＝全部主機），回傳一開始的計畫。已經在更新時回 ErrUpdateRunning。
func (s *Service) Update(ctx context.Context, hostID string) (Progress, error) {
	p, err := s.Plan(ctx, hostID)
	if err != nil {
		return Progress{}, err
	}
	s.mu.Lock()
	if s.job != nil && s.job.Running {
		cur := clone(*s.job)
		s.mu.Unlock()
		return cur, ErrUpdateRunning
	}
	p.Running, p.StartedAt = true, time.Now().UnixMilli()
	s.job = &p
	snap := clone(p)
	s.mu.Unlock()
	s.publish(snap, false)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(context.WithoutCancel(ctx))
	}()
	return snap, nil
}

// step 改進度並發事件。
func (s *Service) step(f func(p *Progress)) {
	s.mu.Lock()
	f(s.job)
	snap := clone(*s.job)
	s.mu.Unlock()
	s.publish(snap, !snap.Running)
}

func (s *Service) publish(p Progress, persist bool) {
	Publish(s.DB, s.Bus, KindUpdate, "", p, persist)
}

func (s *Service) run(ctx context.Context) {
	s.mu.Lock()
	n := len(s.job.Hosts)
	s.mu.Unlock()
	for i := range n {
		s.mu.Lock()
		hu := s.job.Hosts[i]
		s.mu.Unlock()
		if hu.State != StatePending {
			continue
		}
		s.updateHost(ctx, i, hu)
	}
	s.step(func(p *Progress) { p.Running, p.FinishedAt, p.Current = false, time.Now().UnixMilli(), "" })
	s.log().Info("更新電腦完成")
}

func (s *Service) updateHost(ctx context.Context, i int, hu HostUpdate) {
	if hu.ImageOutdated && s.Hosts != nil {
		s.step(func(p *Progress) { p.Hosts[i].State = StateImage })
		if err := s.Hosts.EnsureImage(ctx, hu.HostID); err != nil {
			s.log().Warn("更新電腦：映像沒有傳到主機", "host", hu.Name, "err", err)
			s.step(func(p *Progress) {
				h := &p.Hosts[i]
				h.State, h.Error = StateFailed, err.Error()
				for j := range h.Bots {
					h.Bots[j].State, h.Bots[j].Reason = StateSkipped, "主機上沒有新映像"
					p.Done++
				}
			})
			return
		}
	}
	s.step(func(p *Progress) { p.Hosts[i].State = StateRunning })
	failed := false
	for j, bu := range hu.Bots {
		if bu.State != StatePending {
			continue
		}
		s.step(func(p *Progress) { p.Hosts[i].Bots[j].State, p.Current = StateRunning, bu.Name })
		state, reason := StateRebuilt, ""
		switch {
		case s.Activity != nil && !s.Activity.Quiesce(bu.BotID):
			state, reason = StateSkipped, "Bot 正在工作，做完後再更新一次"
		default:
			if _, err := s.Boxes.Rebuild(ctx, bu.BotID); err != nil {
				state, reason, failed = StateFailed, err.Error(), true
				s.log().Warn("更新電腦：重建失敗", "bot", bu.BotID, "err", err)
			}
		}
		s.step(func(p *Progress) {
			b := &p.Hosts[i].Bots[j]
			b.State, b.Reason = state, reason
			p.Done++
		})
	}
	s.step(func(p *Progress) {
		if failed {
			p.Hosts[i].State = StateFailed
		} else {
			p.Hosts[i].State = StateDone
		}
	})
}
