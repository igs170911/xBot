package hermes

import (
	"slices"
	"testing"
)

func TestApplyAppsReplacesOnlyManagedEntries(t *testing.T) {
	cfg := map[string]any{"mcp_servers": map[string]any{"mine": map[string]any{"url": "https://x"}, "old": map[string]any{"command": "x"}},
		AppsKey: []any{"old", "xbot"}}
	cfg = Config(cfg, Profile{ProviderID: "p", Model: "m"})
	apps := []AppServer{{Name: "notes", Command: Python, Args: []string{XBotMCP}, Env: map[string]string{"XBOT_MCP_PATH": "/internal/apps/notes"}},
		{Name: "report", Command: "npx", Args: []string{"-y", "r"}, Secret: map[string]string{"K": "v"}}, {Name: "computer", Command: "evil"}}
	gone := applyApps(cfg, apps)
	mcp := cfg["mcp_servers"].(map[string]any)
	if mcp["mine"] == nil || mcp["old"] != nil || mcp["xbot"] == nil || mcp["computer"].(map[string]any)["command"] != Python {
		t.Fatalf("只換 XBot 管的連接，不能蓋掉 computer、xbot：%v", mcp)
	}
	if !slices.Equal(gone, []string{"old"}) {
		t.Fatalf("不再使用的：%v", gone)
	}
	notes := mcp["notes"].(map[string]any)
	if notes["env"].(map[string]any)["XBOT_MCP_PATH"] != "/internal/apps/notes" {
		t.Fatalf("notes：%v", notes)
	}
	report := mcp["report"].(map[string]any)
	if report["command"] != "/bin/sh" || report["env"] != nil {
		t.Fatalf("祕密不能放進 config.yaml：%v", report)
	}
	files := appFiles(apps, gone)
	if len(files) != 2 || files[0].name != ".app-report.env" || files[0].mode != 0o400 || files[1].name != ".app-old.env" || len(files[1].data) != 0 {
		t.Fatalf("env 檔：%+v", files)
	}
	if names := cfg[AppsKey].([]any); len(names) != 2 {
		t.Fatalf("記下管理的名字：%v", names)
	}
}

// 連接的應用程式一律 untrusted：會寫入的工具要先核准（G1 第 4 項）。
func TestAppServersAreUntrusted(t *testing.T) {
	cfg := map[string]any{}
	applyApps(cfg, []AppServer{{Name: "notes", Command: Python, Args: []string{XBotMCP}}})
	entry := cfg["mcp_servers"].(map[string]any)["notes"].(map[string]any)
	if entry["trust"] != "untrusted" {
		t.Fatalf("應用程式應該是 untrusted：%v", entry)
	}
}
