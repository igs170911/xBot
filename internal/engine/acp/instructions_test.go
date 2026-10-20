package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
)

// soul 是假的 Bot 身分與守則（實際是 hermes.Soul：名稱、人設、基本守則、隊友與組織）。
const soul = "# 內容策略\n\n用台灣口吻寫社群貼文。\n\n## 隊友與協作\n- 小秘（幕僚長）"

type promptParams struct {
	Prompt []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"prompt"`
}

func calls(a *acptest.Agent, method string) []json.RawMessage {
	var out []json.RawMessage
	for _, c := range a.Calls() {
		if c.Method == method {
			out = append(out, c.Params)
		}
	}
	return out
}

func prompts(t *testing.T, a *acptest.Agent) []promptParams {
	t.Helper()
	var out []promptParams
	for _, raw := range calls(a, "session/prompt") {
		var p promptParams
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func newDriver(engineName string, agent *acptest.Agent, inst func(context.Context, string) (string, error)) *acp.Driver {
	return acp.New(acp.Options{Agent: acp.Agents[engineName], Instructions: inst,
		Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }})
}

func hello(t *testing.T, d *acp.Driver, s engine.Session) {
	t.Helper()
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptHello}); err != nil {
		t.Fatal(err)
	}
	if evs := collect(t, ch); text(evs, engine.EventText) != "Hello" {
		t.Fatalf("回覆：%+v", evs)
	}
}

// ACP 引擎也拿到 Bot 的身分與守則（使用者回報 2026-10-19：Grok 的 Bot 不照人設、回「我是 Grok」、用簡體中文）。
// Claude Code 放進系統提示（session/new、session/load 的 _meta.systemPrompt.append），每一則訊息都不多帶。
func TestClaudeGetsInstructionsAsSystemPrompt(t *testing.T) {
	agent := acptest.New()
	var asked []string
	d := newDriver(acp.EngineClaude, agent, func(_ context.Context, botID string) (string, error) {
		asked = append(asked, botID)
		return soul, nil
	})
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var sn struct {
		Meta struct {
			SystemPrompt struct {
				Append string `json:"append"`
			} `json:"systemPrompt"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(calls(agent, "session/new")[0], &sn)
	if got := sn.Meta.SystemPrompt.Append; !strings.Contains(got, soul) || !strings.Contains(got, "就是下面這位 Bot") {
		t.Fatalf("系統提示：%q", got)
	}
	hello(t, d, s)
	hello(t, d, s)
	for _, p := range prompts(t, agent) {
		if len(p.Prompt) != 1 || p.Prompt[0].Text != acptest.PromptHello {
			t.Fatalf("訊息不重複帶守則：%+v", p)
		}
	}
	// 接回之前的 session 也帶（agent 重開、對話接回時用新的守則）
	d.Close("b")
	if _, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t2", Resume: s.ID()}); err != nil {
		t.Fatal(err)
	}
	loads := calls(agent, "session/load")
	if len(loads) != 1 || !strings.Contains(string(loads[0]), "systemPrompt") {
		t.Fatalf("session/load：%s", loads)
	}
	if len(asked) != 2 || asked[0] != "b" {
		t.Fatalf("每次開 session 拿一次：%v", asked)
	}
}

// 沒有系統提示可以放的 agent（Codex、Gemini、OpenCode）：加在新 session 第一則訊息前面，之後不再帶；
// 接回的 session 歷史裡已經有了，不再重送。
func TestOtherAgentsGetInstructionsInTheFirstMessage(t *testing.T) {
	for _, name := range []string{acp.EngineCodex, acp.EngineGemini, acp.EngineOpenCode} {
		t.Run(name, func(t *testing.T) {
			agent := acptest.NewFor(name)
			d := newDriver(name, agent, func(context.Context, string) (string, error) { return soul, nil })
			s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
			if err != nil {
				t.Fatal(err)
			}
			if raw := calls(agent, "session/new")[0]; strings.Contains(string(raw), "_meta") {
				t.Fatalf("不認得 _meta.systemPrompt 的 agent 不帶：%s", raw)
			}
			hello(t, d, s)
			hello(t, d, s)
			ps := prompts(t, agent)
			first := ps[0].Prompt
			if len(first) != 2 || !strings.Contains(first[0].Text, "<xbot-instructions>\n"+soul) || !strings.Contains(first[0].Text, "就是下面這位 Bot") ||
				first[1].Text != acptest.PromptHello {
				t.Fatalf("第一則：%+v", first)
			}
			if len(ps[1].Prompt) != 1 {
				t.Fatalf("第二則不再帶：%+v", ps[1])
			}
			// 接回之前的 session：不再帶
			d.Close("b")
			s2, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Resume: s.ID()})
			if err != nil {
				t.Fatal(err)
			}
			hello(t, d, s2)
			if ps := prompts(t, agent); len(ps[2].Prompt) != 1 {
				t.Fatalf("接回的 session 不再帶：%+v", ps[2])
			}
		})
	}
}

// Grok 接受 session/new 的 _meta.rules（加在它的系統提示後面）：守則放那裡，不放進訊息；接回時也帶，
// 權限等級 auto、full 帶 autoMode，ask 不帶（SEC-2：任何等級都不帶 yoloMode）。
func TestGrokGetsInstructionsAsRules(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, func(context.Context, string) (string, error) { return soul, nil })
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(calls(agent, "session/new")[0], &p); err != nil {
		t.Fatal(err)
	}
	rules, _ := p.Meta["rules"].(string)
	if !strings.Contains(rules, soul) || !strings.Contains(rules, "就是下面這位 Bot") || !strings.Contains(rules, "不用自己輪詢") ||
		p.Meta["autoMode"] != true || p.Meta["yoloMode"] != nil {
		t.Fatalf("session/new 的 _meta：%+v", p.Meta)
	}
	hello(t, d, s)
	if ps := prompts(t, agent); len(ps[0].Prompt) != 1 {
		t.Fatalf("守則不放進訊息：%+v", ps[0])
	}
	d.Close("b")
	if _, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Resume: s.ID(), Mode: "ask"}); err != nil {
		t.Fatal(err)
	}
	raw := string(calls(agent, "session/load")[0])
	if !strings.Contains(raw, `"rules"`) || strings.Contains(raw, "autoMode") {
		t.Fatalf("session/load（ask）：%s", raw)
	}
}

// 讀不到守則時照樣開 session（只是少了守則），不要讓 Bot 不能用。
func TestInstructionsErrorDoesNotBlockTheSession(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := newDriver(acp.EngineGrok, agent, func(context.Context, string) (string, error) { return "", errors.New("資料庫忙") })
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	hello(t, d, s)
	if ps := prompts(t, agent); len(ps[0].Prompt) != 1 {
		t.Fatalf("%+v", ps)
	}
}
