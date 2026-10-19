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
	for _, c := range agent.Calls() {
		if c.Method == "session/prompt" {
			_ = json.Unmarshal(c.Params, &first)
			break
		}
	}
	if len(first.Prompt) != 2 || !strings.Contains(first.Prompt[0].Text, inst) || first.Prompt[1].Text != acptest.PromptHello {
		t.Fatalf("第一則訊息前面要帶身分與守則：%+v", first)
	}
	// 對話裡只看得到使用者自己說的話
	msgs, _ := db.ListMessages(ctx, store.ScopeThread, res.ThreadID, 0, 0)
	if len(msgs) == 0 || msgs[0].Content != acptest.PromptHello {
		t.Fatalf("%+v", msgs)
	}
}
