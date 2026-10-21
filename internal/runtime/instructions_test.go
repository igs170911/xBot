package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
	"xbot/internal/engine/hermes"
	"xbot/internal/events"
	"xbot/internal/memory"
	"xbot/internal/store"
)

// ACP 引擎的 Bot 也拿到和 Hermes 一樣的 SOUL.md（名稱、人設、基本守則、隊友與組織）：使用者回報 2026-10-19，
// Grok 的「內容策略」不照人設、回「我是 Grok。需要我帮你写代码…」。
func TestACPBotGetsItsSoul(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	comp := &Computers{DB: db, Roster: func(context.Context, string) (string, error) {
		return "\n\n## 隊友與協作\n- 小秘（幕僚長）", nil
	}}
	agent := acptest.NewFor(acp.EngineGrok)
	grok := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGrok], Instructions: comp.Instructions,
		Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }})
	hermesDrv := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{}, errors.New("不該用到 Hermes")
	}, nil)
	rt := New(Options{DB: db, Driver: engine.NewRouter(hermes.Name, comp.EngineOf, hermesDrv, grok), Bus: events.New(), Models: comp})
	bot, err := db.CreateBot(ctx, store.Bot{Name: "內容策略", Title: "社群編輯", Soul: "用台灣口吻寫貼文，不寫程式。", Engine: acp.EngineGrok})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := comp.Instructions(ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 內容策略", "社群編輯", "用台灣口吻寫貼文", "繁體中文", "小秘（幕僚長）"} {
		if !strings.Contains(inst, want) {
			t.Fatalf("守則要有 %q：%s", want, inst)
		}
	}
	res, err := rt.Chat(ctx, bot.ID, "", acptest.PromptHello)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if task, err := rt.Wait(wctx, res.TaskID); err != nil || task.Verdict != VerdictAnswer {
		t.Fatalf("%+v %v", task, err)
	}
	var first struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	var opened struct {
		Meta struct {
			Rules string `json:"rules"`
		} `json:"_meta"`
	}
	for _, c := range agent.Calls() {
		switch c.Method {
		case "session/new":
			_ = json.Unmarshal(c.Params, &opened)
		case "session/prompt":
			if first.Prompt == nil {
				_ = json.Unmarshal(c.Params, &first)
			}
		}
	}
	// Grok：身分與守則放在 session 的 _meta.rules（加在它的系統提示後面），訊息裡只有使用者說的話。
	if !strings.Contains(opened.Meta.Rules, strings.TrimSpace(inst)) || len(first.Prompt) != 1 || first.Prompt[0].Text != acptest.PromptHello {
		t.Fatalf("身分與守則要在 _meta.rules：%+v %+v", opened, first)
	}
	// 對話裡只看得到使用者自己說的話
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) == 0 || msgs[0].Content != acptest.PromptHello {
		t.Fatalf("%+v", msgs)
	}
}

// 2026-10-21：水哥哥換到 OpenCode 之後記憶全不見了。ACP 引擎的守則後面附上兩份記憶，並說和使用者現在說的不一樣時照現在的做。
func TestMemoryTextForACP(t *testing.T) {
	if got := MemoryText(memory.State{}); got != "" {
		t.Fatalf("沒有記憶時不給：%q", got)
	}
	got := MemoryText(memory.State{User: memory.List{Entries: []string{"使用者的 Threads 帳號是 @kupi"}},
		Memory: memory.List{Entries: []string{"刪 Bot 用 remove_bot\n要核准"}}})
	for _, want := range []string{"## 你的長期記憶", "照現在說的做", "xbot.memory.add", "關於使用者：\n使用者的 Threads 帳號是 @kupi\n", "你記下的事：\n刪 Bot 用 remove_bot 要核准\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("少了 %q：%s", want, got)
		}
	}
}
