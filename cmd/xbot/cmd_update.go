package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"xbot/internal/computers"
	"xbot/internal/config"
	"xbot/internal/service"
)

// updateComputers：xbot service update-computers（make update 的最後一步，V10）。
// 叫已安裝的服務「更新電腦」：映像換過或 XBot 改了電腦的設定時，把電腦逐台重建（資料保留）。
// 正在工作的 Bot 跳過，之後在「設定 → 電腦與裝置」按「更新電腦」或再執行一次 make update 即可。
func updateComputers(m *service.Manager, cfg config.Config) error {
	c, ok := liveClient(m, cfg)
	if !ok {
		return errors.New("服務沒有在執行，略過更新電腦（make start 之後再執行 xbot computers update）")
	}
	ctx := context.Background()
	fmt.Println("▶ 檢查 Bot 的電腦要不要更新…")
	var p computers.Progress
	if err := callOp(ctx, c, "xbot.computers.update", map[string]any{}, &p); err != nil {
		return fmt.Errorf("更新電腦沒有開始：%w", err)
	}
	if p.Total == 0 && !anyImageOutdated(p) {
		fmt.Println("✅ 所有 Bot 的電腦都是最新的")
		return reportSkippedHosts(p)
	}
	fmt.Printf("   要更新 %d 台電腦（正在工作的 Bot 會跳過）\n", p.Total)
	last := ""
	for p.Running {
		time.Sleep(time.Second)
		if err := callOp(ctx, c, "xbot.computers.update_status", map[string]any{}, &p); err != nil {
			return err
		}
		if line := progressLine(p); line != last {
			fmt.Println("   " + line)
			last = line
		}
	}
	return printUpdateResult(p)
}

func anyImageOutdated(p computers.Progress) bool {
	for _, h := range p.Hosts {
		if h.ImageOutdated {
			return true
		}
	}
	return false
}

func progressLine(p computers.Progress) string {
	for _, h := range p.Hosts {
		if h.State == computers.StateImage {
			return fmt.Sprintf("%s：傳新映像…", h.Name)
		}
	}
	if p.Current != "" {
		return fmt.Sprintf("%d/%d：重建 %s 的電腦…", p.Done, p.Total, p.Current)
	}
	return fmt.Sprintf("%d/%d", p.Done, p.Total)
}

func reportSkippedHosts(p computers.Progress) error {
	for _, h := range p.Hosts {
		if h.State == computers.StateSkipped && h.Error != "" {
			fmt.Printf("⚠️  %s：%s\n", h.Name, h.Error)
		}
	}
	return nil
}

func printUpdateResult(p computers.Progress) error {
	rebuilt, skipped, failed := 0, 0, 0
	for _, h := range p.Hosts {
		if h.Error != "" {
			fmt.Printf("⚠️  %s：%s\n", h.Name, h.Error)
		}
		for _, b := range h.Bots {
			switch b.State {
			case computers.StateRebuilt:
				rebuilt++
			case computers.StateSkipped:
				skipped++
				fmt.Printf("   ⏭  %s：%s\n", b.Name, b.Reason)
			case computers.StateFailed:
				failed++
				fmt.Printf("   ❌ %s：%s\n", b.Name, b.Reason)
			}
		}
	}
	fmt.Printf("✅ 電腦更新完成：重建 %d 台", rebuilt)
	if skipped > 0 {
		fmt.Printf("，跳過 %d 台（之後在「設定 → 電腦與裝置」按「更新電腦」，或執行 xbot computers update）", skipped)
	}
	fmt.Println()
	if failed > 0 {
		return fmt.Errorf("%d 台電腦更新失敗（上面是原因；資料都保留著）", failed)
	}
	return nil
}
