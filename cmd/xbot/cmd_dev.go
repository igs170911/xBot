package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"xbot/internal/config"
	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// cmdDev 是開發用的暫時指令（C05 的 chat.send 等操作做好後移除）。
func cmdDev(args []string) error {
	if len(args) == 0 || args[0] != "chat" {
		return errors.New("用法：xbot dev chat --bot <名稱> [選項] \"訊息\"（xbot dev chat -h 看選項）")
	}
	return devChat(args[1:])
}

// devChat 在這個程序裡開起整套元件（不需要 xbot serve），對一個 Bot 說一句話並印出串流回覆。
// 不要和使用同一個資料目錄的 xbot serve 同時執行：兩邊都會建立同一個 product.sock。
func devChat(args []string) error {
	fs := flag.NewFlagSet("dev chat", flag.ContinueOnError)
	botName := fs.String("bot", "測試", "Bot 名稱；沒有就建立")
	thread := fs.String("thread", "", "接續的對話串 id（空白：開新的）")
	provider := fs.String("provider", "openai", "模型供應商代號（Model Proxy 的 /model/<代號>/v1）")
	baseURL := fs.String("base-url", os.Getenv("XBOT_DEV_BASE_URL"), "設定供應商的服務網址（例：https://api.openai.com/v1；也可用 XBOT_DEV_BASE_URL）")
	model := fs.String("model", os.Getenv("XBOT_DEV_MODEL"), "設成預設模型（也可用 XBOT_DEV_MODEL）")
	timeout := fs.Duration("timeout", 10*time.Minute, "最多等多久")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(text) == "" {
		return errors.New("請給一句要對 Bot 說的話")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := openStack(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if *baseURL != "" {
		// 金鑰從環境變數讀，避免出現在指令紀錄裡。
		key := os.Getenv("XBOT_DEV_API_KEY")
		if err := st.providers.Put(ctx, proxy.Provider{ID: *provider, BaseURL: *baseURL, APIKey: proxy.Secret(key)}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "已設定供應商 %s → %s\n", *provider, *baseURL)
	}
	if *model != "" {
		if err := st.db.SetSetting(ctx, runtime.DefaultModelSetting, runtime.ModelRef{Provider: *provider, Model: *model}); err != nil {
			return err
		}
	}
	bot, err := findOrCreateBot(ctx, st.db, *botName)
	if err != nil {
		return err
	}

	sub := st.runtime.Bus().Subscribe(events.ForBot(bot.ID), 0)
	defer sub.Close()
	res, err := st.runtime.Chat(ctx, bot.ID, *thread, text)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Bot %s（%s）對話串 %s，任務 %s；第一次會先開機，請稍候…\n", bot.Name, bot.ID, res.ThreadID, res.TaskID)
	for {
		select {
		case <-ctx.Done():
			sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = st.runtime.Stop(sctx, bot.ID, res.ThreadID)
			c()
			return ctx.Err()
		case e, ok := <-sub.C:
			if !ok {
				return errors.New("事件中斷")
			}
			if e.TaskID != res.TaskID {
				continue
			}
			done, err := printEvent(e)
			if done {
				return err
			}
		}
	}
}

func printEvent(e events.Event) (bool, error) {
	switch e.Kind {
	case runtime.KindText:
		var p map[string]string
		_ = json.Unmarshal(e.Payload, &p)
		fmt.Print(p["text"])
	case runtime.KindToolCall, runtime.KindToolResult, runtime.KindError, runtime.KindNeedsApproval, runtime.KindStatus:
		var ev engine.Event
		_ = json.Unmarshal(e.Payload, &ev)
		line := ev.Text
		if ev.Tool != nil {
			line = ev.Tool.Name + " " + ev.Tool.Context
		}
		if ev.Request != nil {
			line = ev.Request.Kind + "：" + ev.Request.Prompt + "（dev chat 不能回覆，請按 Ctrl+C）"
		}
		fmt.Fprintf(os.Stderr, "\n[%s] %s\n", e.Kind, line)
	case runtime.KindTaskState:
		var p map[string]string
		_ = json.Unmarshal(e.Payload, &p)
		fmt.Fprintf(os.Stderr, "[狀態] %s\n", p["state"])
	case runtime.KindDone:
		var p map[string]string
		_ = json.Unmarshal(e.Payload, &p)
		fmt.Println()
		fmt.Fprintf(os.Stderr, "[完成] %s\n", p["verdict"])
		if p["verdict"] == runtime.VerdictFailed {
			return true, errors.New(p["text"])
		}
		return true, nil
	}
	return false, nil
}

func findOrCreateBot(ctx context.Context, db *store.DB, name string) (store.Bot, error) {
	bots, err := db.ListBots(ctx, false)
	if err != nil {
		return store.Bot{}, err
	}
	for _, b := range bots {
		if b.Name == name {
			return b, nil
		}
	}
	return db.CreateBot(ctx, store.Bot{Name: name, Engine: "hermes"})
}
