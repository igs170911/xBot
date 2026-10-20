package computers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"xbot/internal/box"
	"xbot/internal/cleanup"
	"xbot/internal/limits"
)

// 例行清理（BX-14，internal/cleanup）：每天一次清掉每台電腦裡會自己長回來的東西，加上看用量、立即清理兩個操作。
// 照 LazyBoy server/cleanup.py daily() 與 main.py 的 /storage、/storage/clean。

// ErrCleanBusy 表示 Bot 正在工作，現在不清理。出自 LazyBoy main.py clean_agent_storage()。
var ErrCleanBusy = errors.New("這個 Bot 正在工作，等它做完再清理")

// StorageView 是 xbot.computers.storage 的輸出。
type StorageView struct {
	BotID string `json:"bot_id"`
	// HasComputer：有電腦資料（還沒開過機的 Bot 全部是 0）。
	HasComputer bool          `json:"has_computer" desc:"有電腦資料；還沒開過機時用量全部是 0"`
	Usage       cleanup.Usage `json:"usage"`
}

// CleanView 是 xbot.computers.clean 的輸出：清出多少，以及清完的用量。
type CleanView struct {
	BotID       string         `json:"bot_id"`
	HasComputer bool           `json:"has_computer" desc:"有電腦資料；沒有時什麼都不用清"`
	Result      cleanup.Result `json:"result"`
	Usage       cleanup.Usage  `json:"usage"`
}

// shell 找到 Bot 的電腦資料並呼叫 fn（box.Manager.WithShell；測試可換掉 s.cl.shell）。
func (s *Service) shell(ctx context.Context, botID string, write bool, fn func(context.Context, box.Shell) error) error {
	if s.cl.shell != nil {
		return s.cl.shell(ctx, botID, write, fn)
	}
	return s.Boxes.WithShell(ctx, botID, write, fn)
}

type cleaner struct {
	shell func(ctx context.Context, botID string, write bool, fn func(context.Context, box.Shell) error) error
}

// Storage 量一個 Bot 的電腦空間花在哪裡。沒開機也可以（不會叫醒它）。
func (s *Service) Storage(ctx context.Context, botID string) (StorageView, error) {
	if _, err := s.DB.GetBot(ctx, botID); err != nil {
		return StorageView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.CleanupBotTimeout)
	defer cancel()
	v := StorageView{BotID: botID}
	err := s.shell(ctx, botID, false, func(ctx context.Context, sh box.Shell) error {
		v.HasComputer = true
		var err error
		v.Usage, err = cleanup.Measure(ctx, sh)
		return err
	})
	if errors.Is(err, box.ErrNoData) {
		return StorageView{BotID: botID}, nil
	}
	return v, err
}

// Clean 立即清理一個 Bot 的電腦（xbot.computers.clean）：只清會自己長回來的快取與暫存，附件不刪（等每天的例行清理）。
// Bot 正在工作時回 ErrCleanBusy。
func (s *Service) Clean(ctx context.Context, botID string) (CleanView, error) {
	if _, err := s.DB.GetBot(ctx, botID); err != nil {
		return CleanView{}, err
	}
	if s.busy(botID) {
		return CleanView{}, ErrCleanBusy
	}
	return s.cleanBot(ctx, botID, time.Now(), cleanup.Options{}, true)
}

// cleanBot 清一個 Bot 的電腦；measure 時順便量清完的用量。
func (s *Service) cleanBot(ctx context.Context, botID string, now time.Time, opt cleanup.Options, measure bool) (CleanView, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.CleanupBotTimeout)
	defer cancel()
	v := CleanView{BotID: botID}
	err := s.shell(ctx, botID, true, func(ctx context.Context, sh box.Shell) error {
		v.HasComputer = true
		var errs []error
		var err error
		v.Result, err = cleanup.Clean(ctx, sh, now, opt)
		errs = append(errs, err)
		if measure {
			v.Usage, err = cleanup.Measure(ctx, sh)
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	})
	if errors.Is(err, box.ErrNoData) {
		return CleanView{BotID: botID}, nil
	}
	return v, err
}

// CleanAll 是每天的例行清理：每個有電腦的 Bot 清一次（含放超過 30 天的附件），正在工作或搬家中的等下一輪。
// 回傳清了幾個 Bot、合計清出多少。一個 Bot 失敗時其他照做，錯誤一起回傳。出自 server/cleanup.py daily()。
func (s *Service) CleanAll(ctx context.Context, now time.Time) (int, int64, error) {
	rows, err := s.DB.ListBoxes(ctx, "")
	if err != nil {
		return 0, 0, err
	}
	n := 0
	var freed int64
	var errs []error
	for _, r := range rows {
		if ctx.Err() != nil {
			return n, freed, ctx.Err()
		}
		if s.busy(r.BotID) || (s.Boxes != nil && s.Boxes.Moving(r.BotID)) {
			continue
		}
		v, err := s.cleanBot(ctx, r.BotID, now, cleanup.Options{Uploads: true}, false)
		if v.HasComputer {
			n++
		}
		freed += v.Result.Freed
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", botName(ctx, s.DB, r.BotID), err))
		}
		if v.Result.Freed > 0 {
			s.log().Info("例行清理", "bot", botName(ctx, s.DB, r.BotID), "freed_mb", fmt.Sprintf("%.1f", float64(v.Result.Freed)/(1<<20)),
				"scratch", v.Result.Scratch, "uploads", v.Result.Uploads, "browser", v.Result.Browser)
		}
	}
	return n, freed, errors.Join(errs...)
}

// RunCleanup 在服務開始 limits.CleanupFirstDelay 後清一次，之後每 limits.CleanupEvery 一次，直到 ctx 結束。
func (s *Service) RunCleanup(ctx context.Context) {
	t := time.NewTimer(limits.CleanupFirstDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, _, err := s.CleanAll(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log().Warn("例行清理有些 Bot 沒清完，明天再試", "err", err)
		}
		t.Reset(limits.CleanupEvery)
	}
}
