package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
)

// 每個 ACP 引擎（E31）各用一個扮演它的假 agent：開得起來、權限等級對到它自己的模式、額度用完時變成 QuotaError。
func TestEachEngine(t *testing.T) {
	type want struct {
		mode  map[string]string // 權限等級 → session/set_mode 的 modeId（空白＝不設定）
		kind  string
		reset bool
	}
	cases := map[string]want{
		acp.EngineClaude:   {mode: map[string]string{"ask": "", "edits": "acceptEdits", "auto": "auto", "full": "auto"}, kind: engine.QuotaExhausted, reset: true},
		acp.EngineCodex:    {mode: map[string]string{"ask": "workspace-write", "edits": "workspace-write", "auto": "agent", "full": "agent"}, kind: engine.QuotaExhausted, reset: true},
		acp.EngineGemini:   {mode: map[string]string{"ask": "", "edits": "autoEdit", "auto": "autoEdit", "full": "autoEdit"}, kind: engine.QuotaExhausted, reset: true},
		acp.EngineOpenCode: {mode: map[string]string{"ask": "", "edits": "", "auto": "", "full": ""}, kind: engine.QuotaFunds},
		acp.EngineGrok:     {mode: map[string]string{"ask": "", "edits": "", "auto": "", "full": ""}, kind: engine.QuotaRateLimit, reset: true},
	}
	if len(cases) != len(acp.Agents) || len(acp.Engines) != len(acp.Agents) {
		t.Fatalf("每個引擎都要測：%v", acp.Engines)
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			for _, level := range []string{"ask", "edits", "auto", "full"} {
				agent := acptest.NewFor(name)
				var launched acp.Agent
				d := acp.New(acp.Options{Agent: acp.Agents[name], Launch: func(_ context.Context, _ string, a acp.Agent) (acp.Process, error) {
					launched = a
					return agent.Start(), nil
				}})
				s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: level})
				if err != nil {
					t.Fatal(err)
				}
				if launched.Engine != name || strings.Join(launched.Command, " ") != strings.Join(acp.Agents[name].Command, " ") {
					t.Fatalf("開的 agent：%+v", launched)
				}
				got := ""
				for _, c := range agent.Calls() {
					if c.Method == "session/set_mode" {
						var p struct {
							ModeID string `json:"modeId"`
						}
						_ = json.Unmarshal(c.Params, &p)
						got = p.ModeID
					}
				}
				if got != w.mode[level] {
					t.Fatalf("%s 的 %s：set_mode %q，要 %q", name, level, got, w.mode[level])
				}
				if level != "full" {
					d.Close("b")
					continue
				}
				// 額度用完：error 與 done 都帶 QuotaError。
				ch := d.Events(s)
				if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptQuota}); err != nil {
					t.Fatal(err)
				}
				evs := collect(t, ch)
				done := evs[len(evs)-1]
				if done.Type != engine.EventDone || done.Status != engine.StatusError || done.Quota == nil {
					t.Fatalf("要以額度錯誤結束：%+v", evs)
				}
				q := done.Quota
				if q.Engine != name || q.Kind != w.kind || (q.ResetAt != 0) != w.reset {
					t.Fatalf("額度錯誤：%+v", q)
				}
				d.Close("b")
			}
		})
	}
}

// Claude Code：錯誤本身沒有時間時，用之前 usage_update 帶的 _claude/rateLimit resetsAt。
func TestClaudeQuotaResetFromRateLimit(t *testing.T) {
	agent := acptest.NewFor(acp.EngineClaude)
	d := acp.New(acp.Options{Agent: acp.Agents[acp.EngineClaude], Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }})
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: acptest.PromptQuota}); err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if q := evs[len(evs)-1].Quota; q == nil || q.ResetAt != 4102444800*1000 {
		t.Fatalf("恢復時間要用 rateLimit.resetsAt：%+v", q)
	}
}

// SEC-2：Grok 的 /always-approve（關掉所有核准）不送給 agent，隊友或網頁內容帶的也一樣（SEC-8）。
func TestBlockedPrompt(t *testing.T) {
	agent := acptest.NewFor(acp.EngineGrok)
	d := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGrok], Launch: func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }})
	s, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t", Mode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"/always-approve on", "  /ALWAYS-APPROVE"} {
		if err := d.Send(context.Background(), s, engine.Message{Text: text}); !errors.Is(err, acp.ErrBlocked) {
			t.Fatalf("%q 要擋下：%v", text, err)
		}
	}
	for _, c := range agent.Calls() {
		if c.Method == "session/prompt" {
			t.Fatalf("不該送出：%s", c.Params)
		}
	}
	// 一般的訊息照送。
	ch := d.Events(s)
	if err := d.Send(context.Background(), s, engine.Message{Text: "請說明 /always-approve 是什麼"}); err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
}

// OpenCode 的權限放在開行程的環境變數：權限等級改了、沒有進行中的一輪時重開 agent。
func TestModeEnvRelaunch(t *testing.T) {
	agent := acptest.NewFor(acp.EngineOpenCode)
	var mu sync.Mutex
	var envs [][]string
	d := acp.New(acp.Options{Agent: acp.Agents[acp.EngineOpenCode], Launch: func(_ context.Context, _ string, a acp.Agent) (acp.Process, error) {
		mu.Lock()
		envs = append(envs, a.Env)
		mu.Unlock()
		return agent.Start(), nil
	}})
	for _, level := range []string{"ask", "ask", "edits"} {
		if _, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t-" + level, Mode: level}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(envs) != 2 {
		t.Fatalf("同一個等級共用行程、改了等級才重開：%d 次", len(envs))
	}
	if !strings.Contains(strings.Join(envs[0], "\n"), `"edit":"ask"`) || !strings.Contains(strings.Join(envs[1], "\n"), `"edit":"allow"`) {
		t.Fatalf("權限設定：%v", envs)
	}
}

// 找不到登入就停下來要授權碼、然後結束的 CLI（Gemini CLI）：說清楚是沒有登入（分類成登入過期），不是一般的「沒有啟動」。
type promptingProcess struct{ r *strings.Reader }

func (p *promptingProcess) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *promptingProcess) Write(b []byte) (int, error) { return len(b), nil }
func (p *promptingProcess) Close() error                { return nil }
func (p *promptingProcess) StderrTail() string          { return "Failed to authenticate with user code." }

func TestLaunchNeedsLogin(t *testing.T) {
	d := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGemini], Launch: func(context.Context, string, acp.Agent) (acp.Process, error) {
		return &promptingProcess{r: strings.NewReader("Please visit the following URL to authorize the application:\nEnter the authorization code: ")}, nil
	}})
	_, err := d.Start(context.Background(), engine.StartOptions{BotID: "b", ThreadID: "t"})
	if err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Fatalf("要說沒有登入：%v", err)
	}
	if q := engine.ClassifyQuota(acp.EngineGemini, err.Error(), time.Now()); q == nil || q.Kind != engine.QuotaAuth {
		t.Fatalf("要分類成登入過期：%+v", q)
	}
}
