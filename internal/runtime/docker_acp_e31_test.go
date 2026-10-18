//go:build docker

// E31 的整合測試：真的開一台 Bot 電腦，確認 Gemini CLI、OpenCode、Grok 都經 docker exec 以 Bot 使用者回應 ACP initialize；
// OpenCode 用假的 auth.json 連結後開得了 session（權限設定由 OPENCODE_CONFIG_CONTENT 帶進去），多層資料夾的連結由 Bot 擁有；
// Grok 沒有登入時說要登入。只用假的憑證，絕對不碰這台電腦上真的登入、也不送任何訊息（不花額度）。
package runtime

import (
	"bufio"
	"context"
	"encoding/json"
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

func TestDockerACPNewEngines(t *testing.T) {
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
	dir, err := os.MkdirTemp("", "xbacp31")
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
	registry := hosts.NewRegistry(local)
	comp := &Computers{DB: db, Hosts: registry}
	home := filepath.Join(dir, "home")
	const fake = "FAKE-OPENCODE-KEY-for-docker-test"
	ocDir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(ocDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auth := `{"opencode-go":{"type":"api","key":"` + fake + `"},"anthropic":{"type":"oauth","access":"sk-ant-oat-FAKE"}}`
	if err := os.WriteFile(filepath.Join(ocDir, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := &engines.Service{DB: db, HostHome: home, Getenv: func(string) string { return "" }, Locate: comp.Locate, UID: box.BotUID, GID: box.BotGID}
	comp.Creds = creds
	guardlock.Shared(t)
	boxes := box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	opencode := acp.New(acp.Options{Agent: acp.Agents[acp.EngineOpenCode], Launch: comp.LaunchACP, Authorize: creds.Authorize, MCPServers: comp.MCPServers})
	grok := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGrok], Launch: comp.LaunchACP, MCPServers: comp.MCPServers})

	bot, err := db.CreateBot(ctx, store.Bot{Name: "E31 整合", Engine: acp.EngineOpenCode, ApprovalMode: "edits"})
	if err != nil {
		t.Fatal(err)
	}
	name := box.ContainerName(bot.ID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		opencode.Close(bot.ID)
		grok.Close(bot.ID)
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

	if _, err := creds.Link(ctx, bot.ID, acp.EngineOpenCode); err != nil {
		t.Fatal(err)
	}
	// OpenCode：開機（Prepare 放憑證）→ opencode acp → initialize → session/new。
	s, err := opencode.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "o1", Mode: "edits"})
	if err != nil {
		t.Fatalf("OpenCode 的 ACP session：%v\n%s", err, logs())
	}
	t.Logf("OpenCode ACP session：%s", s.ID())
	const target = "/opt/xbot/cred/opencode/auth.json"
	if out, _ := inBox("1000:1000", "readlink", "/home/box/.local/share/opencode/auth.json"); out != target {
		t.Fatalf("家目錄的連結：%q", out)
	}
	for _, d := range []string{"/home/box/.local", "/home/box/.local/share", "/home/box/.local/share/opencode"} {
		if out, _ := inBox("0", "stat", "-c", "%u", d); out != "1000" {
			t.Fatalf("%s 要由 Bot 擁有：%q", d, out)
		}
	}
	if out, _ := inBox("1000:1000", "cat", target); !strings.Contains(out, fake) || strings.Contains(out, "anthropic") {
		t.Fatalf("容器裡的 auth.json（不能有 Claude 的登入）：%q", out)
	}
	// 權限設定：opencode 行程的環境變數帶「edits」等級的設定（bash 一律要問）。
	pid, _ := inBox("0", "pgrep", "-u", "1000", "-o", "-f", "opencode acp")
	if pid == "" {
		t.Fatal("找不到 opencode 行程")
	}
	env, _ := inBox("1000:1000", "sh", "-c", "tr '\\0' '\\n' < /proc/"+strings.Fields(pid)[0]+"/environ")
	if !strings.Contains(env, `"edit":"allow","bash":"ask"`) {
		t.Fatalf("OpenCode 的權限設定沒有帶進去：%s", env)
	}

	// Grok：initialize 成功，沒有登入時 session/new 說要登入。
	if _, err := grok.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "g1"}); err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Fatalf("Grok 沒有登入時：%v", err)
	}

	// Gemini CLI：照映像的系統設定（用連結的 Google 登入），沒有有效的登入時開機就停下來要授權碼、然後結束；
	// 驅動要很快說「還沒登入」，不能卡住。
	gemini := acp.New(acp.Options{Agent: acp.Agents[acp.EngineGemini], Launch: comp.LaunchACP, MCPServers: comp.MCPServers})
	start := time.Now()
	if _, err := gemini.Start(ctx, engine.StartOptions{BotID: bot.ID, ThreadID: "m1"}); err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Fatalf("Gemini 沒有登入時：%v", err)
	}
	t.Logf("Gemini CLI 沒有登入：%s 後說明", time.Since(start).Round(time.Second))
	gemini.Close(bot.ID)
	// 不讀系統設定時只做 initialize：確認它講 ACP。
	a := acp.Agents[acp.EngineGemini]
	a.Env = append(append([]string{}, a.Env...), "GEMINI_CLI_SYSTEM_SETTINGS_PATH=/nonexistent")
	p, err := comp.LaunchACP(ctx, bot.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false}}}` + "\n"
	if _, err := p.Write([]byte(init)); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(p)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			if strings.Contains(sc.Text(), `"id":1`) {
				got <- sc.Text()
				return
			}
		}
		got <- ""
	}()
	select {
	case line := <-got:
		var res struct {
			Result struct {
				ProtocolVersion int `json:"protocolVersion"`
				AgentInfo       struct {
					Version string `json:"version"`
				} `json:"agentInfo"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &res) != nil || res.Result.ProtocolVersion != 1 {
			t.Fatalf("Gemini CLI 的 initialize：%q", line)
		}
		t.Logf("Gemini CLI %s 回應 initialize", res.Result.AgentInfo.Version)
	case <-time.After(90 * time.Second):
		t.Fatal("Gemini CLI 沒有回應 initialize")
	}
}
