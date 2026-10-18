package engines

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"xbot/internal/bots"
	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 每個 ACP 引擎都有憑證規格、Bot 的引擎選項裡也都有（E31）。
func TestEveryEngineHasSpec(t *testing.T) {
	for _, e := range acp.Engines {
		if _, ok := Specs[e]; !ok {
			t.Fatalf("%s 沒有憑證規格", e)
		}
		if !slices.Contains((bots.Engine("")).EnumValues(), e) {
			t.Fatalf("bots.Engine 沒有 %s", e)
		}
	}
}

// Gemini CLI、OpenCode、Grok：連結後憑證唯讀放進 Bot 電腦，家目錄的連結（含多層資料夾）都由 Bot 擁有。
func TestLinkNewEngines(t *testing.T) {
	cases := []struct {
		engine, hostDir, file, boxLink string
	}{
		{acp.EngineGemini, ".gemini", "oauth_creds.json", "/home/box/.gemini/oauth_creds.json"},
		{acp.EngineOpenCode, ".local/share/opencode", "auth.json", "/home/box/.local/share/opencode/auth.json"},
		{acp.EngineGrok, ".grok", "auth.json", "/home/box/.grok/auth.json"},
	}
	for _, c := range cases {
		t.Run(c.engine, func(t *testing.T) {
			e := setup(t)
			content := `{"token":"` + fakeToken + `"}`
			if c.engine == acp.EngineOpenCode {
				content = `{"opencode-go":{"type":"api","key":"` + fakeToken + `"},"anthropic":{"type":"oauth","access":"sk-ant-oat-FAKE"}}`
			}
			e.hostCred(t, filepath.Join(e.home, c.hostDir), c.file, content)
			b := e.bot(t, c.engine)
			if _, err := e.call(owner, "xbot.engines.link_credentials", LinkIn{BotID: b.ID, Engine: Engine(c.engine)}); err != nil {
				t.Fatal(err)
			}
			cred := e.files.get("/opt/xbot/cred/" + c.engine + "/" + c.file)
			if cred.mode != 0o400 || cred.uid != 1000 || !strings.Contains(cred.data, fakeToken) {
				t.Fatalf("憑證：%+v", cred)
			}
			if c.engine == acp.EngineOpenCode && strings.Contains(cred.data, "anthropic") {
				t.Fatalf("OpenCode 的 Claude 登入不能放進 Bot 電腦（EN-2a）：%s", cred.data)
			}
			if l := e.files.get(c.boxLink); l.link != "/opt/xbot/cred/"+c.engine+"/"+c.file || l.uid != 1000 {
				t.Fatalf("家目錄的連結：%+v", l)
			}
			// 每一層資料夾都由 Bot 擁有（Docker 不會用 root 補建）。
			dir := "/home/box"
			for _, part := range strings.Split(filepath.Dir(strings.TrimPrefix(c.boxLink, "/home/box/")), "/") {
				dir += "/" + part
				if d := e.files.get(dir); d.uid != 1000 || d.typ != '5' {
					t.Fatalf("%s：%+v", dir, d)
				}
			}
		})
	}
}

func TestOpenCodeOnlyClaude(t *testing.T) {
	e := setup(t)
	e.hostCred(t, filepath.Join(e.home, ".local/share/opencode"), "auth.json", `{"anthropic":{"type":"oauth","access":"sk-ant-oat-FAKE"}}`)
	b := e.bot(t, acp.EngineOpenCode)
	_, err := e.call(owner, "xbot.engines.link_credentials", LinkIn{BotID: b.ID, Engine: acp.EngineOpenCode})
	if code(err) != ops.CodeConflict || !strings.Contains(err.Error(), "Claude") {
		t.Fatalf("只有 Claude 的登入不能連結：%v", err)
	}
	// XDG_DATA_HOME 改了位置也找得到。
	e.envVars["XDG_DATA_HOME"] = filepath.Join(e.home, "xdg")
	if got := e.svc.HostPath(Specs[acp.EngineOpenCode]); got != filepath.Join(e.home, "xdg", "opencode", "auth.json") {
		t.Fatal(got)
	}
}

// Gemini CLI 找不到登入時會停下來等人輸入授權碼：這台電腦上登出後，就算同意還在也不開 agent。
func TestGeminiNeedsHostLogin(t *testing.T) {
	e := setup(t)
	dir := filepath.Join(e.home, ".gemini")
	e.hostCred(t, dir, "oauth_creds.json", `{"refresh_token":"`+fakeToken+`"}`)
	b := e.bot(t, acp.EngineGemini)
	if _, err := e.call(owner, "xbot.engines.link_credentials", LinkIn{BotID: b.ID, Engine: acp.EngineGemini}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Authorize(bg, b.ID, acp.EngineGemini); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Authorize(bg, b.ID, acp.EngineGemini); !errors.Is(err, ErrNoHostLogin) {
		t.Fatalf("登出後不開 agent：%v", err)
	}
}

// EN-7：額度卡的選項只列這台電腦登入過的其他引擎、Hermes 的其他供應商；換引擎要同意時帶 link。
func TestQuotaCardAndSwitch(t *testing.T) {
	e := setup(t)
	e.hostCred(t, filepath.Join(e.home, ".claude"), ".credentials.json", `{"token":"`+fakeToken+`"}`)
	e.hostCred(t, filepath.Join(e.home, ".codex"), "auth.json", `{"token":"`+fakeToken+`"}`)
	e.svc.Models = func(context.Context) ([]ModelChoice, error) {
		return []ModelChoice{{Provider: "opencode-go", Model: "deepseek-v4.1-flash", ProviderLabel: "OpenCode Go"}, {Provider: "openai", Model: "gpt-6-luna", ProviderLabel: "OpenAI"}}, nil
	}
	var resent []string
	e.svc.Resend = func(_ context.Context, botID, threadID, text string) (string, error) {
		resent = append(resent, botID+"|"+threadID+"|"+text)
		return "task-2", nil
	}
	var forgot []string
	e.svc.Forget = func(botID string) { forgot = append(forgot, botID) }
	busy := false
	e.svc.Busy = func(string) bool { return busy }
	b := e.bot(t, acp.EngineClaude)
	if _, err := e.call(owner, "xbot.engines.link_credentials", LinkIn{BotID: b.ID, Engine: acp.EngineClaude}); err != nil {
		t.Fatal(err)
	}

	label, opts := e.svc.QuotaCard(bg, b.ID, &engine.QuotaError{Engine: acp.EngineClaude, Kind: engine.QuotaExhausted})
	if label != "Claude Code" {
		t.Fatal(label)
	}
	if len(opts) != 3 || opts[0].Engine != acp.EngineCodex || !opts[0].NeedsLink || opts[0].Label != "Codex CLI（ChatGPT 方案）" ||
		opts[1].Model != "opencode-go/deepseek-v4.1-flash" || opts[2].Model != "openai/gpt-6-luna" {
		t.Fatalf("選項（不含 Claude 自己、只列登入過的）：%+v", opts)
	}
	// Hermes 的供應商額度用完：不列同一個供應商。
	label, opts = e.svc.QuotaCard(bg, b.ID, &engine.QuotaError{Engine: hermes.Name, Provider: "opencode-go", Kind: engine.QuotaExhausted})
	if label != "OpenCode Go" || slices.ContainsFunc(opts, func(o engine.SwitchOption) bool { return strings.HasPrefix(o.Model, "opencode-go/") }) {
		t.Fatalf("%s %+v", label, opts)
	}

	// 額度卡（runtime 開的）。
	thread, err := e.db.CreateThread(bg, store.Thread{BotID: b.ID, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"engine": acp.EngineClaude, "kind": "quota", "thread_id": thread.ID, "retry_text": "整理報表"})
	card, err := e.db.CreateInboxItem(bg, store.InboxItem{Kind: engine.QuotaCardKind, BotID: b.ID, ThreadID: thread.ID, Title: "Claude Code 的額度用完了", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}

	// 沒同意 Codex 就不能換過去。
	_, err = e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: acp.EngineCodex, InboxID: card.ID})
	if code(err) != ops.CodeConflict {
		t.Fatalf("要先同意：%v", err)
	}
	// 只有你本人。
	if _, err := e.call(ops.Identity{Kind: ops.KindAgent, ID: "a1", Scopes: []ops.Scope{ops.ScopeManage}}, "xbot.engines.switch",
		SwitchIn{BotID: b.ID, Engine: acp.EngineCodex, Link: true}); err == nil {
		t.Fatal("代理不能幫你同意")
	}
	// 正在做事時不換。
	busy = true
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: acp.EngineCodex, Link: true}); code(err) != ops.CodeConflict {
		t.Fatalf("忙碌時：%v", err)
	}
	busy = false
	// 試算不改任何東西。
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: acp.EngineCodex, Link: true}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.db.GetBot(bg, b.ID); got.Engine != acp.EngineClaude {
		t.Fatal("試算不能真的換")
	}

	raw, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: acp.EngineCodex, Link: true, Resend: true, InboxID: card.ID})
	if err != nil {
		t.Fatal(err)
	}
	var out SwitchOut
	_ = json.Unmarshal(raw, &out)
	if !out.Bot.Linked || out.Bot.Engine != acp.EngineCodex || out.TaskID != "task-2" || out.ThreadID != thread.ID {
		t.Fatalf("換好：%+v", out)
	}
	if got, _ := e.db.GetBot(bg, b.ID); got.Engine != acp.EngineCodex || got.Model != "" {
		t.Fatalf("Bot：%+v", got)
	}
	if !slices.Equal(resent, []string{b.ID + "|" + thread.ID + "|整理報表"}) || !slices.Equal(forgot, []string{b.ID}) || !slices.Contains(e.closed, b.ID) {
		t.Fatalf("重送 %v、忘掉 %v、關掉舊行程 %v", resent, forgot, e.closed)
	}
	if got, _ := e.db.GetInboxItem(bg, card.ID); got.Status != store.InboxResolved || got.Resolution != ResolutionSwitched {
		t.Fatalf("額度卡要收掉：%+v", got)
	}
	if c := e.files.get("/opt/xbot/cred/codex/auth.json"); !strings.Contains(c.data, fakeToken) {
		t.Fatalf("Codex 的憑證要放進去：%+v", c)
	}

	// 換到 Hermes 的某個模型（不用訂閱）。
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: hermes.Name, Model: "nomodel"}); err == nil {
		t.Fatal("Hermes 的模型要有供應商")
	}
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: hermes.Name, Model: "openai/gpt-6-luna"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.db.GetBot(bg, b.ID); got.Engine != hermes.Name || got.Model != "openai/gpt-6-luna" {
		t.Fatalf("Bot：%+v", got)
	}
	// 重送要有額度卡。
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: hermes.Name, Resend: true}); err == nil {
		t.Fatal("沒有卡不能重送")
	}
}
