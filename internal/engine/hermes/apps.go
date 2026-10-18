package hermes

import (
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// 連接的應用程式（C34）在 Bot 電腦裡的 MCP 伺服器。
//
// 遠端網址（HTTP）的應用程式一律經 XBot 轉送：Bot 電腦裡跑的是 XBot MCP 的 stdio 橋接（box/xbot_mcp.py），
// XBOT_MCP_PATH 指到 product.sock 上的 /internal/apps/<名字>，XBot 在服務端帶上權杖再轉給遠端，
// 所以 OAuth 權杖與 API 金鑰從頭到尾不進容器（SEC-5）。
//
// 唯一的例外是自訂的本機程式（stdio）需要的環境變數：程式在 Bot 的電腦裡執行，祕密只能交給它。
// 這些值寫進 /hermes/.app-<名字>.env（權限 0400、只屬於 Bot 使用者、只寫進有授權的 Bot），
// 再用 sh 讀進來執行程式；不放在 config.yaml，也不放進 ACP 的 session 參數。收回授權時檔案會被清空。

// AppServer 是授權給這個 Bot 的一個連接在 Bot 電腦裡怎麼啟動。
type AppServer struct {
	Name    string            // mcp_servers.<Name>（工具在 Hermes 裡叫 mcp__<Name>__<工具>）
	Command string            // 要執行的程式
	Args    []string          // 參數
	Env     map[string]string // 不是祕密的環境變數（例：XBOT_MCP_PATH），直接放進設定
	Secret  map[string]string // 祕密的環境變數：寫進唯讀的 env 檔（見上面的例外說明）
}

// AppsKey 是 config.yaml 裡 XBot 管理的連接名字清單：只有這些 mcp_servers 項目會被 XBot 改寫或移除，
// 使用者或 Hermes 自己加的不動。
const AppsKey = "xbot_apps"

// AppNameRE 是連接名字的格式（也是 Hermes 工具名稱的一部分）。
var AppNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// ReservedApp 回答一個名字是不是 XBot 自己用的 MCP 伺服器（computer、xbot）。
func ReservedApp(name string) bool { return name == "computer" || name == XBotMCPServer }

// AppEnvFile 是一個連接的祕密環境變數檔（容器裡的路徑）。
func AppEnvFile(name string) string { return path.Join(HomeDir, appEnvName(name)) }

func appEnvName(name string) string { return ".app-" + name + ".env" }

// Launch 回傳實際執行的指令與參數：有祕密時用 sh 先讀 env 檔再 exec 原本的程式。
func (a AppServer) Launch() (string, []string) {
	if len(a.Secret) == 0 {
		return a.Command, slices.Clone(a.Args)
	}
	return "/bin/sh", append([]string{"-c", `set -a; . "$0"; set +a; exec "$@"`, AppEnvFile(a.Name), a.Command}, a.Args...)
}

// EnvFile 是 env 檔的內容（每行 KEY='值'，值裡的單引號跳脫）。
func (a AppServer) EnvFile() []byte {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(a.Secret)) {
		b.WriteString(k + "='" + strings.ReplaceAll(a.Secret[k], "'", `'\''`) + "'\n")
	}
	return []byte(b.String())
}

// applyApps 把 XBot 管理的連接寫進 cfg 的 mcp_servers：上次寫的先拿掉，再放這次的。回傳不再使用的名字（要清空 env 檔）。
func applyApps(cfg map[string]any, apps []AppServer) []string {
	mcp, _ := cfg["mcp_servers"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		cfg["mcp_servers"] = mcp
	}
	var old []string
	if list, ok := cfg[AppsKey].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && !ReservedApp(s) {
				old = append(old, s)
				delete(mcp, s)
			}
		}
	}
	names := []any{}
	now := map[string]bool{}
	for _, a := range apps {
		if ReservedApp(a.Name) || !AppNameRE.MatchString(a.Name) {
			continue
		}
		cmd, args := a.Launch()
		entry := map[string]any{"command": cmd, "args": toAny(args)}
		if len(a.Env) > 0 {
			env := map[string]any{}
			for k, v := range a.Env {
				env[k] = v
			}
			entry["env"] = env
		}
		mcp[a.Name] = entry
		names = append(names, a.Name)
		now[a.Name] = true
	}
	cfg[AppsKey] = names
	var gone []string
	for _, s := range old {
		if !now[s] {
			gone = append(gone, s)
		}
	}
	return gone
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// appFiles 是要寫的 env 檔：有祕密的連接寫內容（0400），不再使用的寫成空的。
func appFiles(apps []AppServer, gone []string) []file {
	var out []file
	for _, a := range apps {
		if len(a.Secret) > 0 && AppNameRE.MatchString(a.Name) {
			out = append(out, file{appEnvName(a.Name), a.EnvFile(), 0o400})
		}
	}
	for _, name := range gone {
		if AppNameRE.MatchString(name) {
			out = append(out, file{appEnvName(name), nil, 0o400})
		}
	}
	return out
}

// WriteApps 只更新 config.yaml 裡 XBot 管理的連接與 env 檔（授權改變時，C34）。容器可以是還沒開機的。
// Hermes 要重新載入 MCP（driver.ReloadMCP）或開新的對話才會用到新的設定。
func WriteApps(ctx context.Context, api Files, containerID, botID string, uid, gid int, apps []AppServer) error {
	defer LockConfig(botID)()
	existing, err := readFile(ctx, api, containerID, path.Join(HomeDir, ConfigFile))
	if err != nil {
		return err
	}
	var cfg map[string]any
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &cfg); err != nil {
			return fmt.Errorf("這個 Bot 的 Hermes 設定壞掉了，下次開機會重寫：%w", err)
		}
	}
	if cfg == nil {
		// 還沒寫過設定（從來沒開過機）：開機前的 Provision 會一起寫。
		return nil
	}
	gone := applyApps(cfg, apps)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeFiles(ctx, api, containerID, Profile{UID: uid, GID: gid}, append([]file{{ConfigFile, out, 0o600}}, appFiles(apps, gone)...))
}
