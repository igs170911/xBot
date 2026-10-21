// Package computers 是 Bot 電腦的生命週期操作（B13）：看狀態、叫醒、休眠、重開、重建（保留資料）、連資料刪除、
// 私有網路白名單（SEC-4），以及「更新電腦」——映像換了以後，一台一台主機地把新映像傳過去並重建上面的電腦（BX-15）。
// 閒置休眠本身在 internal/box（Manager.RunIdle），這裡只是操作與背景工作。
package computers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"xbot/internal/box"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 事件種類。
const (
	// KindStatus：一台電腦的狀態變了（開機、休眠、重建、刪除、錯誤；payload：box.Status）。
	KindStatus = "computer.status"
	// KindUpdate：「更新電腦」的進度（payload：Progress）。
	KindUpdate = "computers.update"
)

// Service 是 xbot.computers.* 背後的服務。
type Service struct {
	DB    *store.DB
	Boxes *box.Manager
	// Hosts 是主機管理（傳映像到遠端主機）；nil 時只有本機。
	Hosts *hosts.Service
	// Activity 是 Bot 的工作狀態（*runtime.Runtime）；nil 時當作都沒有工作。
	Activity box.Activity
	Bus      *events.Bus
	Logger   *slog.Logger

	// Sealer 加密「從家裡出去」的 Tailscale 登入金鑰（proxy.Keyring）；nil 時不能存金鑰。
	Sealer Sealer
	// TailscaleKey 讀內建 Tailscale（C35）存的登入金鑰，「從家裡出去」沒有自己的金鑰時用；可為 nil。
	TailscaleKey func(ctx context.Context) (string, error)

	mu  sync.Mutex
	job *Progress
	wg  sync.WaitGroup
	mv  mover
	cl  cleaner
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Publish 發布一個事件（persist 時也寫進 event 表，SSE 斷線續傳用）。
func Publish(db *store.DB, bus *events.Bus, kind, botID string, payload any, persist bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	e := events.Event{Kind: kind, BotID: botID, Payload: raw, At: time.Now().UnixMilli()}
	if persist && db != nil {
		if row, err := db.AppendEvent(context.Background(), store.Event{Kind: kind, BotID: botID, Payload: raw}); err == nil {
			e = events.FromStore(row)
		}
	}
	if bus != nil {
		bus.Publish(e)
	}
}

// View 是一台電腦給人看的狀態（xbot.computers.status）。
type View struct {
	Computer box.Status `json:"computer"`
	BotName  string     `json:"bot_name"`
	HostName string     `json:"host_name"`
	// IdleMinutes 是閒置多久休眠（-1：永不休眠）；LastActiveAt 是最後一次使用（毫秒）；SleepsAt 是照現在這樣會在什麼時候休眠（沒開機或有工作時 0）。
	IdleMinutes  int   `json:"idle_minutes"`
	LastActiveAt int64 `json:"last_active_at"`
	SleepsAt     int64 `json:"sleeps_at" desc:"預計閒置休眠的時間（毫秒）；沒開機、有工作或有人在看時是 0"`
	Busy         bool  `json:"busy" desc:"Bot 有進行中或排隊中的工作"`
	Outdated     bool  `json:"outdated" desc:"映像或強化設定換了，要更新電腦（重建，資料保留）"`
	// NetIP 是電腦在 xbot-box 網路上的 IP；AllowPrivate 是它能連的私有網路（白名單，SEC-4）。
	NetIP        string   `json:"net_ip,omitempty"`
	AllowPrivate []string `json:"allow_private"`
	// Home：這個 Bot 的電腦連外從家裡出去（CL-7）。
	Home bool `json:"home" desc:"電腦連外從家裡出去（Tailscale exit node）"`
	// Moving：正在搬家；Move 是進行中或最近一次的搬家（CL-5）。
	Moving bool     `json:"moving" desc:"電腦正在搬家（不能開機、重建或刪除）"`
	Move   *MoveJob `json:"move,omitempty" desc:"進行中或最近一次的搬家"`
}

// ErrNoComputer 表示 Bot 還沒有電腦。
var ErrNoComputer = errors.New("這個 Bot 還沒有電腦")

func (s *Service) busy(botID string) bool { return s.Activity != nil && s.Activity.HasWork(botID) }

// Status 讀一台電腦（會向 Docker 確認實際狀態）。
func (s *Service) Status(ctx context.Context, botID string) (View, error) {
	bot, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return View{}, err
	}
	st, err := s.Boxes.Status(ctx, botID)
	if err != nil {
		return View{}, err
	}
	v := View{Computer: st, BotName: bot.Name, IdleMinutes: idleMinutes(bot), Busy: s.busy(botID), AllowPrivate: []string{}}
	if row, err := s.DB.GetHost(ctx, st.HostID); err == nil {
		v.HostName = row.Name
	}
	if b, err := s.DB.GetBox(ctx, botID); err == nil {
		v.LastActiveAt, v.NetIP = b.LastActiveAt, b.NetIP
		if st.Running && !v.Busy && b.LastActiveAt > 0 && !box.NeverSleeps(bot) {
			v.SleepsAt = b.LastActiveAt + box.IdleFor(bot).Milliseconds()
		}
	}
	if st.ContainerID != "" {
		if v.Outdated, err = s.Boxes.Outdated(ctx, botID); err != nil {
			s.log().Warn("看不出電腦要不要更新", "bot", botID, "err", err)
		}
	}
	allow, err := s.Boxes.NetAllow(ctx, botID)
	if err != nil {
		return v, err
	}
	for _, a := range allow {
		v.AllowPrivate = append(v.AllowPrivate, a.String())
	}
	if v.Home, err = s.Boxes.EgressOn(ctx, botID); err != nil {
		return v, err
	}
	v.Moving, v.Move = s.Boxes.Moving(botID), s.lastMove(ctx, botID)
	return v, nil
}

// Wake 開機（還沒有電腦時先建立）；wait 為 true 時等桌面與 Hermes 就緒。
func (s *Service) Wake(ctx context.Context, botID string, wait bool) (View, error) {
	if _, err := s.Boxes.Start(ctx, botID); err != nil {
		return View{}, err
	}
	if wait {
		if _, err := s.Boxes.WaitReady(ctx, botID); err != nil {
			return View{}, err
		}
	}
	return s.Status(ctx, botID)
}

// Sleep 讓電腦休眠。
func (s *Service) Sleep(ctx context.Context, botID string) (View, error) {
	if _, err := s.Boxes.Sleep(ctx, botID, s.Activity); err != nil {
		return View{}, err
	}
	return s.Status(ctx, botID)
}

// Restart 重開電腦。
func (s *Service) Restart(ctx context.Context, botID string) (View, error) {
	if _, err := s.Boxes.Restart(ctx, botID, s.Activity); err != nil {
		return View{}, err
	}
	return s.Status(ctx, botID)
}

// Rebuild 用目前的映像與設定重建電腦，資料保留（原本在跑的話重建後再開機）。
func (s *Service) Rebuild(ctx context.Context, botID string) (View, error) {
	if s.Activity != nil && !s.Activity.Quiesce(botID) {
		return View{}, box.ErrBusy
	}
	if _, err := s.Boxes.Rebuild(ctx, botID); err != nil {
		return View{}, err
	}
	return s.Status(ctx, botID)
}

// Purge 刪掉電腦和它的所有資料（家目錄、工作資料夾、引擎狀態、訂閱憑證、私有網路白名單）。Bot 本身保留，下次有工作時開一台全新的。
func (s *Service) Purge(ctx context.Context, botID string) (View, error) {
	if s.Activity != nil && !s.Activity.Quiesce(botID) {
		return View{}, box.ErrBusy
	}
	if _, err := s.Boxes.Remove(ctx, botID, true); err != nil {
		return View{}, err
	}
	return s.Status(ctx, botID)
}

// SetNetwork 換掉 Bot 的私有網路白名單（立刻生效）。
func (s *Service) SetNetwork(ctx context.Context, botID string, allow []string) (View, error) {
	if _, err := s.Boxes.SetNetAllow(ctx, botID, allow); err != nil {
		return View{}, err
	}
	return s.Status(ctx, botID)
}

// Wait 等背景工作（更新電腦）結束；測試與關閉服務時用。
func (s *Service) Wait() { s.wg.Wait() }

func botName(ctx context.Context, db *store.DB, id string) string {
	if b, err := db.GetBot(ctx, id); err == nil {
		return b.Name
	}
	return id
}

// idleMinutes 是畫面上的閒置休眠時間（分鐘）；永不休眠是 -1。
func idleMinutes(b store.Bot) int {
	if box.NeverSleeps(b) {
		return limits.BoxNeverSleep
	}
	return int(box.IdleFor(b) / time.Minute)
}
