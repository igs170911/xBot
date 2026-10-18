package hermes

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"gopkg.in/yaml.v3"

	"xbot/internal/limits"
)

// 開機前寫進 Bot 的 hermes volume（容器內 $HERMES_HOME=/hermes）的檔案。照 LazyBoy server/computers.py _prepare()：
//   - config.yaml：模型指向容器內的 hermes_host.py（http://127.0.0.1:8642/model/<供應商>/v1），
//     它再經 /product-ipc/product.sock 轉給 XBot 的 Model Proxy；保留 Hermes 自己寫的其他設定。
//   - .env：XBOT_MODEL_TOKEN=<worker-key>（config 的 providers.product.key_env 指向它）。
//   - .worker-key：hermes_host.py 用它驗證 /rpc 與轉送的請求，也帶著它去 product.sock。
//   - SOUL.md：Bot 的身分與基本守則。
// 真正的供應商金鑰從頭到尾不進容器（SEC-5）：這裡只有 worker-key。

// 容器內的位置。
const (
	HomeDir       = "/hermes"
	ConfigFile    = "config.yaml"
	EnvFile       = ".env"
	WorkerKeyFile = ".worker-key"
	SoulFile      = "SOUL.md"
	// TokenEnv 是 .env 裡放 worker-key 的變數名稱（LazyBoy 用 LAZYHERMES_MODEL_TOKEN）。
	TokenEnv = "XBOT_MODEL_TOKEN"
	// RelayBase 是容器內 hermes_host.py 的位址；模型請求打到 RelayBase/model/<供應商>/v1。
	RelayBase = "http://127.0.0.1:8642"
	// DesktopMCP 是映像裡的桌面工具（box/Dockerfile 放在 /opt/xbot）。
	DesktopMCP = "/opt/xbot/desktop_mcp.py"
	python     = "/opt/hermes/.venv/bin/python"
)

// webHint 取代 Hermes 對 cli 平台「終端機不顯示 Markdown」的提示（LazyBoy WEB_HINT）。
const webHint = "你在 XBot 的網頁聊天介面裡回覆。Markdown 會正常顯示（粗體、清單、表格、連結），" +
	"不要用 ═══、--- 之類的符號自己畫分隔線。檔案寫出 /workspace 底下的完整路徑，網頁會變成可以點開的檔案。"

// Profile 是寫 Hermes 設定需要的 Bot 資料。
type Profile struct {
	BotID       string
	Name        string
	Title       string
	Description string
	Soul        string
	// ProviderID 是 Model Proxy 的供應商代號（/model/<ProviderID>/v1）。
	ProviderID string
	Model      string
	// Transport：chat_completions（預設）、codex_responses、anthropic_messages。
	Transport string
	// Effort 是 reasoning_effort，空白時 medium（LazyBoy config.reasoning）。
	Effort string
	// WorkerKey 是這個 Bot 的 worker-key（不是供應商金鑰）。
	WorkerKey string
	// UID、GID 是容器內 Bot 使用者；寫入的檔案交給它。
	UID, GID int
}

// ModelBaseURL 回傳容器內 Hermes 使用的模型網址。
func ModelBaseURL(providerID string) string { return RelayBase + "/model/" + providerID + "/v1" }

// Files 是寫檔用到的 Docker API；hosts.DockerAPI 符合。
type Files interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
}

// Provision 把 Profile 寫進容器的 /hermes（容器可以是還沒開機的）。
func Provision(ctx context.Context, api Files, containerID string, p Profile) error {
	if p.WorkerKey == "" || p.ProviderID == "" || p.Model == "" {
		return errors.New("寫 Hermes 設定需要模型、供應商和 worker-key")
	}
	existing, err := readFile(ctx, api, containerID, path.Join(HomeDir, ConfigFile))
	if err != nil {
		return err
	}
	var cfg map[string]any
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &cfg); err != nil {
			// 壞掉的設定檔就整個重寫（Hermes 讀不了它也沒用）。
			cfg = nil
		}
	}
	cfgOut, err := yaml.Marshal(Config(cfg, p))
	if err != nil {
		return err
	}
	env, err := readFile(ctx, api, containerID, path.Join(HomeDir, EnvFile))
	if err != nil {
		return err
	}
	files := []file{
		{ConfigFile, cfgOut, 0o600},
		{EnvFile, Env(env, p.WorkerKey), 0o600},
		{WorkerKeyFile, []byte(p.WorkerKey + "\n"), 0o600},
		{SoulFile, []byte(Soul(p)), 0o644},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), Uid: p.UID, Gid: p.GID, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(f.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := api.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{DestinationPath: HomeDir, Content: &buf, CopyUIDGID: true}); err != nil {
		return fmt.Errorf("寫入 Bot 的 Hermes 設定失敗：%w", err)
	}
	return nil
}

type file struct {
	name string
	data []byte
	mode int64
}

// readFile 從容器讀一個小檔案；不存在時回傳 nil。
func readFile(ctx context.Context, api Files, containerID, p string) ([]byte, error) {
	res, err := api.CopyFromContainer(ctx, containerID, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Bot 的 %s 失敗：%w", p, err)
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 4<<20))
		}
	}
}

// Config 把 XBot 管的設定合併進 Hermes 現有的 config（同 LazyBoy _prepare 的 cfg.update），回傳新的 config。
func Config(cfg map[string]any, p Profile) map[string]any {
	if cfg == nil {
		cfg = map[string]any{}
	}
	sub := func(k string) map[string]any {
		if m, ok := cfg[k].(map[string]any); ok {
			return m
		}
		m := map[string]any{}
		cfg[k] = m
		return m
	}
	base := ModelBaseURL(p.ProviderID)
	transport := p.Transport
	if transport == "" {
		transport = "chat_completions"
	}
	effort := p.Effort
	if effort == "" {
		effort = "medium"
	}
	approvals := sub("approvals")
	approvals["mode"] = "off" // 容器是隔離的電腦：每個指令再請另一個模型審核只會變慢（LazyBoy）。
	web := sub("web")
	delete(web, "provider") // Hermes 讀 web.backend（LazyBoy 同樣清掉 web.provider）。
	cfg["model"] = map[string]any{"default": p.Model, "provider": Provider, "base_url": base, "supports_vision": false}
	cfg["providers"] = map[string]any{"product": map[string]any{
		"api": base, "key_env": TokenEnv, "default_model": p.Model, "transport": transport,
	}}
	cfg["terminal"] = map[string]any{"backend": "local", "cwd": "/workspace", "timeout": limits.HermesTerminalTimeoutSeconds}
	cfg["browser"] = map[string]any{"backend": "browser-use", "cdp_url": "http://127.0.0.1:9222", "allow_private_urls": true}
	cfg["platform_toolsets"] = map[string]any{"cli": []any{"terminal", "file", "web", "browser", "skills", "memory", "todo",
		"clarify", "mcp", "delegation", "code_execution", "session_search"}}
	cfg["agent"] = map[string]any{"max_iterations": limits.HermesMaxIterations, "reasoning_effort": effort, "image_input_mode": "text",
		"tool_use_enforcement": false, "execution_guidance": false}
	cfg["display"] = map[string]any{"busy_input_mode": "steer", "interim_assistant_messages": true}
	sub("onboarding")["profile_build"] = "off"
	hints := sub("platform_hints")
	hints["cli"] = map[string]any{"replace": webHint}
	compression := sub("compression")
	if _, ok := compression["proactive_prune_tokens"]; !ok {
		compression["proactive_prune_tokens"] = limits.HermesPruneTokens
	}
	if _, ok := compression["threshold_tokens"]; !ok {
		compression["threshold_tokens"] = limits.HermesCompressTokens
	}
	// 中斷後要不要續跑由 XBot 決定，不讓 Hermes 自己重送（LazyBoy）。
	desktop := sub("desktop")
	auto, _ := desktop["auto_continue"].(map[string]any)
	if auto == nil {
		auto = map[string]any{}
		desktop["auto_continue"] = auto
	}
	auto["enabled"] = false
	mcp := sub("mcp_servers")
	mcp["computer"] = map[string]any{"command": python, "args": []any{DesktopMCP}, "trust": "full"}
	return cfg
}

// Env 把 .env 裡的 worker-key 換成新的，其他使用者或 Hermes 存的變數保留。
func Env(existing []byte, workerKey string) []byte {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
		if line == "" {
			continue
		}
		if name, _, _ := strings.Cut(line, "="); strings.TrimSpace(name) == TokenEnv {
			continue
		}
		lines = append(lines, line)
	}
	lines = append(lines, TokenEnv+"="+workerKey)
	return []byte(strings.Join(lines, "\n") + "\n")
}

// Soul 是 Bot 的 SOUL.md：名稱、職稱、個性，加上基本守則（LazyBoy 的精簡版；完整版跟著 Bot 設定的任務卡）。
func Soul(p Profile) string {
	var b strings.Builder
	b.WriteString("# " + p.Name + "\n\n")
	for _, s := range []string{p.Title, p.Description, p.Soul} {
		if s = strings.TrimSpace(s); s != "" {
			b.WriteString(s + "\n\n")
		}
	}
	b.WriteString("你是常駐在自己電腦裡的 Bot。一律使用繁體中文（台灣用語）。網頁是使用者與你的主要入口。" +
		"工具在你的獨立電腦裡執行，工作檔案保存於 /workspace。可交付檔案請放在 /workspace，並在回答提供實際路徑。" +
		"有需要澄清的問題使用 clarify。完成後驗證結果，區分已完成、估計和未取得的資料。\n")
	return b.String()
}
