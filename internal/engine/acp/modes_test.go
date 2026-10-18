package acp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// SEC-2：任何權限等級都不能對應到「完全不檢查」的模式，也不能用開行程的參數或環境變數關掉核准。
func TestNoModeBypassesReview(t *testing.T) {
	// 各引擎「全部自動允許」的模式：Claude Code bypassPermissions、Codex agent-full-access、Gemini yolo、Grok bypassPermissions／dontAsk。
	forbidden := map[string]bool{"bypassPermissions": true, "agent-full-access": true, "yolo": true, "dontAsk": true}
	levels := []string{"ask", "edits", "auto", "full"}
	for _, name := range Engines {
		a, ok := Agents[name]
		if !ok {
			t.Fatalf("%s 沒有定義", name)
		}
		for level, mode := range a.Modes {
			if forbidden[mode] {
				t.Fatalf("%s 的 %s 對應到 %s，違反 SEC-2", name, level, mode)
			}
		}
		// 開行程的指令與每個等級的環境變數都不能帶「全部允許」的開關。
		for _, level := range levels {
			launched := a.ForMode(level)
			all := strings.ToLower(strings.Join(append(append([]string{}, launched.Command...), launched.Env...), " "))
			for _, bad := range []string{"--always-approve", "--yolo", "--dangerously", "bypasspermissions", "--approval-mode", "dontask", "full-access"} {
				if strings.Contains(all, bad) {
					t.Fatalf("%s 的 %s 帶了 %s：%s", name, level, bad, all)
				}
			}
			for _, kv := range launched.Env {
				k, v, _ := strings.Cut(kv, "=")
				if k != "OPENCODE_CONFIG_CONTENT" {
					continue
				}
				// OpenCode：指令在任何等級都要先問，也不能用萬用字元放行。
				var cfg struct {
					Permission map[string]any `json:"permission"`
				}
				if err := json.Unmarshal([]byte(v), &cfg); err != nil {
					t.Fatalf("OpenCode 的設定不是 JSON：%v", err)
				}
				if cfg.Permission["bash"] != "ask" {
					t.Fatalf("OpenCode 的 %s：bash 是 %v，要每次都問", level, cfg.Permission["bash"])
				}
				if strings.Contains(v, `"*":"allow"`) {
					t.Fatalf("OpenCode 的 %s 用萬用字元放行：%s", level, v)
				}
			}
		}
		if name == EngineOpenCode && len(a.ModeEnv) != len(levels) {
			t.Fatalf("OpenCode 每個權限等級都要有權限設定：%v", a.ModeEnv)
		}
	}
	// Grok 的 /always-approve 不能送進去。
	if _, blocked := Agents[EngineGrok].Blocked("/always-approve on"); !blocked {
		t.Fatal("Grok 要擋 /always-approve")
	}
}

// Gemini CLI 的系統設定（box/gemini-settings.json → /etc/gemini-cli/settings.json）：關掉 YOLO、用連結的 Google 登入。
func TestGeminiSystemSettings(t *testing.T) {
	raw, err := os.ReadFile("../../../box/gemini-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
			DisableYoloMode bool `json:"disableYoloMode"`
		} `json:"security"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if !s.Security.DisableYoloMode || s.Security.Auth.SelectedType != "oauth-personal" {
		t.Fatalf("Gemini 系統設定：%+v", s)
	}
	df, err := os.ReadFile("../../../box/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "gemini-settings.json /etc/gemini-cli/settings.json") {
		t.Fatal("映像要放 Gemini 的系統設定")
	}
}
