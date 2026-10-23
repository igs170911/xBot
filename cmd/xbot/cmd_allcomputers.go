package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"xbot/internal/box"
	"xbot/internal/computers"
	"xbot/internal/config"
	"xbot/internal/opsclient"
	"xbot/internal/screen"
	"xbot/internal/service"
)

// make sleep-all／make purge-all：對所有 Bot 的電腦一次做 xbot.computers.sleep／purge。
// 只是逐台呼叫現有操作（忙碌檢查、稽核都照操作本身）；正在工作的 Bot 跳過。

// callFunc 呼叫一個操作；測試時換成假的。
type callFunc func(ctx context.Context, name string, in, out any) error

// liveClient 是呼叫已安裝服務的 client；服務沒有回應時 ok 是 false。
func liveClient(m *service.Manager, cfg config.Config) (c *opsclient.Client, ok bool) {
	base := m.URL()
	if v := strings.TrimSpace(os.Getenv("XBOT_URL")); v != "" {
		base = strings.TrimRight(v, "/")
	}
	return &opsclient.Client{BaseURL: base, Token: cliToken(cfg)}, m.Healthy(base + "/healthz")
}

func liveCall(m *service.Manager, cfg config.Config) (callFunc, error) {
	c, ok := liveClient(m, cfg)
	if !ok {
		return nil, errors.New("服務沒有在執行（先 make start）")
	}
	return func(ctx context.Context, name string, in, out any) error { return callOp(ctx, c, name, in, out) }, nil
}

// sleepComputers：xbot service sleep-computers（make sleep-all）。
func sleepComputers(m *service.Manager, cfg config.Config) error {
	call, err := liveCall(m, cfg)
	if err != nil {
		return err
	}
	return sleepAll(context.Background(), call, m.Out)
}

// purgeComputers：xbot service purge-computers（make purge-all）。
func purgeComputers(m *service.Manager, cfg config.Config) error {
	call, err := liveCall(m, cfg)
	if err != nil {
		return err
	}
	return purgeAll(context.Background(), call, m.Yes, m.In, m.Out)
}

// sleepAll 把開著的電腦全部關機；資料與瀏覽器登入都保留，有工作時會自己開機。
func sleepAll(ctx context.Context, call callFunc, w io.Writer) error {
	list, err := listComputers(ctx, call)
	if err != nil {
		return err
	}
	running := func(s screen.Screen) bool { return s.Box == box.StateRunning }
	if !anyComputer(list, running) {
		fmt.Fprintln(w, "✅ 沒有開著的電腦")
		return nil
	}
	fmt.Fprintln(w, "▶ 關掉所有 Bot 的電腦（資料與瀏覽器登入都保留，有工作時會自己開機）…")
	return forAllComputers(ctx, call, "xbot.computers.sleep", list, running, w).report(w, "關機")
}

// purgeAll 刪掉所有 Bot 的電腦和裡面的資料，無法復原；Bot 本身、對話與設定保留。
// 要輸入「刪除」確認（yes 時不問）。沒有電腦的 Bot 也做一次，順便清掉留下來的資料區。
func purgeAll(ctx context.Context, call callFunc, yes bool, in *bufio.Reader, w io.Writer) error {
	list, err := listComputers(ctx, call)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "✅ 還沒有 Bot")
		return nil
	}
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.Name
	}
	fmt.Fprintf(w, "⚠️  即將刪除 %d 個 Bot 的電腦和裡面所有資料（家目錄、工作資料夾、瀏覽器登入、引擎紀錄），無法復原：\n   %s\n",
		len(list), strings.Join(names, "、"))
	fmt.Fprintln(w, "   Bot 本身、對話和設定都保留，下次有工作時會開一台全新的電腦。")
	if !yes {
		fmt.Fprint(w, "   確定的話請輸入「刪除」：")
		line, _ := in.ReadString('\n')
		if strings.TrimSpace(line) != "刪除" {
			fmt.Fprintln(w, "已取消，什麼都沒刪。")
			return nil
		}
	}
	all := func(screen.Screen) bool { return true }
	return forAllComputers(ctx, call, "xbot.computers.purge", list, all, w).report(w, "刪除")
}

func listComputers(ctx context.Context, call callFunc) ([]screen.Screen, error) {
	var out screen.ScreensOut
	if err := call(ctx, "xbot.computers.screens", map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("讀不到 Bot 的電腦：%w", err)
	}
	return out.Screens, nil
}

func anyComputer(list []screen.Screen, pick func(screen.Screen) bool) bool {
	for _, s := range list {
		if pick(s) {
			return true
		}
	}
	return false
}

// allResult 是逐台做完的結果。
type allResult struct{ done, skipped, failed int }

// forAllComputers 對 pick 選中的每台電腦呼叫 op（輸入只有 bot_id）；正在工作的 Bot 跳過。
func forAllComputers(ctx context.Context, call callFunc, op string, list []screen.Screen, pick func(screen.Screen) bool, w io.Writer) allResult {
	var r allResult
	for _, s := range list {
		if !pick(s) {
			continue
		}
		if s.Working {
			r.skipped++
			fmt.Fprintf(w, "   ⏭  %s：正在工作，跳過\n", s.Name)
			continue
		}
		var v computers.View
		if err := call(ctx, op, map[string]any{"bot_id": s.BotID}, &v); err != nil {
			r.failed++
			fmt.Fprintf(w, "   ❌ %s：%v\n", s.Name, err)
			continue
		}
		r.done++
		fmt.Fprintf(w, "   ✅ %s\n", s.Name)
	}
	return r
}

func (r allResult) report(w io.Writer, verb string) error {
	fmt.Fprintf(w, "✅ %s %d 台", verb, r.done)
	if r.skipped > 0 {
		fmt.Fprintf(w, "，跳過 %d 台正在工作的（等它做完再執行一次）", r.skipped)
	}
	fmt.Fprintln(w)
	if r.failed > 0 {
		return fmt.Errorf("%d 台電腦沒有%s（上面是原因）", r.failed, verb)
	}
	return nil
}
