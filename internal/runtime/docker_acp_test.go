//go:build docker

// ACP 驅動的整合測試（E12）：真的開一台 Bot 電腦，經 docker exec 跑 Claude Code 的 ACP adapter，做到 initialize 與 session/new
// （不需要真的登入）；Codex CLI 的 adapter 做到 initialize，session/new 回「還沒登入」。也驗證憑證放進容器的方式與收回。
// 預設用假的憑證檔，絕對不碰這台電腦上真的登入。設 XBOT_TEST_CLAUDE_LINK=1 時才用這台電腦上真的 Claude Code 登入，
// 並真的送一則訊息（會用到你的訂閱額度）。
// 需要 Docker 與 make box 建好的映像（BOX_IMAGE，預設 xbot-box:dev）；沒有時略過。
package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engines"
	"xbot/internal/hosts"
	"xbot/internal/store"
)

func TestDockerACPClaudeCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	real := os.Getenv("XBOT_TEST_CLAUDE_LINK") == "1"

	dir, err := os.MkdirTemp("", "xbacp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	// 和 cmd/xbot/stack.go 相同的組裝（ACP 的 Bot 不需要 Model Proxy）。
	registry := hosts.NewRegistry(local)
	comp := &Computers{DB: db, Hosts: registry}
	home := filepath.Join(dir, "home")
	const fake = "FAKE-CLAUDE-CREDENTIAL-for-docker-test"
	if real {
		if home, err = os.UserHomeDir(); err != nil {
			t.Fatal(err)
		}
		t.Log("XBOT_TEST_CLAUDE_LINK=1：用這台電腦上真的 Claude Code 登入")
	} else {
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		cred := `{"claudeAiOauth":{"accessToken":"` + fake + `","refreshToken":"` + fake + `","expiresAt":1,"scopes":[]}}`
		if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(cred), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	creds := &engines.Service{DB: db, HostHome: home, Getenv: func(string) string { return "" }, Locate: comp.Locate, UID: box.BotUID, GID: box.BotGID}
	comp.Creds = creds
	// 和其他套件共用本機的網路防護（xbot-netguard）；網路防護測試在跑時先等它做完（見 guardlock）。
	guardlock.Shared(t)
	boxes := box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	claude := acp.New(acp.Options{Agent: acp.Agents[acp.EngineClaude], Launch: comp.LaunchACP, Authorize: creds.Authorize, MCPServers: comp.MCPServers})
	codex := acp.New(acp.Options{Agent: acp.Agents[acp.EngineCodex], Launch: comp.LaunchACP, MCPServers: comp.MCPServers})
	creds.Close = func(botID string) { claude.Close(botID); codex.Close(botID) }

	bot, err := db.CreateBot(ctx, store.Bot{Name: "ACP 整合", Engine: acp.EngineClaude, ApprovalMode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	name := box.ContainerName(bot.ID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		claude.Close(bot.ID)
		codex.Close(bot.ID)
		if _, err := boxes.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	}()
	logs := func() string {
		out, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
		return string(out)
	}
	inBox := func(user string, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "docker", append([]string{"exec", "-u", user, name}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// 還沒同意：驅動拒絕，不開 agent。
	if _, err := claude.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "t0"}); err == nil || !strings.Contains(err.Error(), "訂閱") {
		t.Fatalf("還沒同意應該拒絕：%v", err)
	}
	if _, err := creds.Link(ctx, bot.ID, acp.EngineClaude); err != nil {
		t.Fatal(err)
	}

	// 開機（Prepare 放憑證）→ docker exec claude-agent-acp → initialize → session/new（cwd /workspace，掛 desktop_mcp）。
	s, err := claude.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "t1", Mode: "full"})
	if err != nil {
		t.Fatalf("Claude Code 的 ACP session：%v\n%s", err, logs())
	}
	if s.ID() == "" {
		t.Fatal("沒有 sessionId")
	}
	t.Logf("Claude Code ACP session：%s", s.ID())
	// desktop_mcp（電腦工具）由 Claude Code 以 Bot 使用者身分開起來（EN-2c）。這個測試沒有用到 Hermes，它不會開。
	mcpUp := false
	for i := 0; i < 30 && !mcpUp; i++ {
		if out, _ := inBox("0", "pgrep", "-u", "1000", "-f", "desktop_mcp.py"); out != "" {
			mcpUp = true
		} else {
			time.Sleep(time.Second)
		}
	}
	if !mcpUp {
		t.Fatalf("Claude Code 沒有開 desktop_mcp\n%s", logs())
	}

	// 憑證：容器檔案層 0400、Bot 擁有；家目錄是連結；Bot 改不了它。
	const target = "/opt/xbot/cred/claude-code/.credentials.json"
	if out, err := inBox("0", "stat", "-c", "%u %a", target); err != nil || out != "1000 400" {
		t.Fatalf("憑證權限：%q %v", out, err)
	}
	if out, _ := inBox("0", "stat", "-c", "%U %a", "/opt/xbot/cred/claude-code"); out != "root 755" {
		t.Fatalf("憑證資料夾：%q", out)
	}
	if out, _ := inBox("1000:1000", "readlink", "/home/box/.claude/.credentials.json"); out != target {
		t.Fatalf("家目錄的連結：%q", out)
	}
	if _, err := inBox("1000:1000", "sh", "-c", "echo x >> "+target); err == nil {
		t.Fatal("Bot 不應該能改憑證檔")
	}
	if _, err := inBox("1000:1000", "rm", "-f", target); err == nil {
		if out, _ := inBox("0", "test", "-e", target); out == "" {
			t.Fatal("Bot 不應該能刪掉憑證檔")
		}
	}
	if !real {
		if out, _ := inBox("1000:1000", "cat", target); !strings.Contains(out, fake) {
			t.Fatalf("容器裡應該是同意的那份憑證：%q", out)
		}
	}

	if real {
		if err := claude.Send(ctx, s, engine.Message{Text: "只回覆兩個英文字母：OK"}); err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for e := range claude.Events(s) {
			if e.Type == engine.EventText {
				text.WriteString(e.Text)
			}
			if e.Type == engine.EventError {
				t.Logf("錯誤事件：%s", e.Text)
			}
			if e.Type == engine.EventDone || e.Fatal {
				t.Logf("done：%+v", e)
				break
			}
		}
		if !strings.Contains(strings.ToUpper(text.String()), "OK") {
			t.Fatalf("用連結的訂閱回覆：%q", text.String())
		}
	}

	// Codex CLI 的 adapter：initialize 成功，沒有登入時 session/new 說要登入。
	if _, err := codex.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "c1"}); err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Fatalf("Codex 沒有登入時：%v", err)
	}

	// 收回：agent 行程結束、容器裡的憑證清掉、驅動拒絕。
	cleared, err := creds.Unlink(ctx, bot.ID)
	if err != nil || !cleared {
		t.Fatalf("收回：%v %v", cleared, err)
	}
	ended := time.After(30 * time.Second)
	for closed := false; !closed; {
		select {
		case e, ok := <-claude.Events(s):
			closed = !ok || e.Fatal
		case <-ended:
			t.Fatal("收回後 agent 行程應該結束")
		}
	}
	if out, _ := inBox("1000:1000", "cat", target); strings.Contains(out, fake) || out != "{}" {
		t.Fatalf("收回後容器裡還有憑證：%q", out)
	}
	if _, err := claude.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "t2"}); err == nil || !strings.Contains(err.Error(), "訂閱") {
		t.Fatalf("收回後應該拒絕：%v", err)
	}
}
