package hermes

import (
	"strings"
	"testing"
)

// C31：bot.memory_enabled 對到 Hermes 的 memory.memory_enabled／user_profile_enabled；上限等其他記憶設定保留。
func TestConfigMemoryToggle(t *testing.T) {
	cfg := Config(map[string]any{"memory": map[string]any{"memory_char_limit": 3000}}, Profile{ProviderID: "p", Model: "m"})
	mem := cfg["memory"].(map[string]any)
	if mem["memory_enabled"] != true || mem["user_profile_enabled"] != true || mem["memory_char_limit"] != 3000 {
		t.Fatalf("%v", mem)
	}
	cfg = Config(cfg, Profile{ProviderID: "p", Model: "m", MemoryOff: true})
	mem = cfg["memory"].(map[string]any)
	if mem["memory_enabled"] != false || mem["user_profile_enabled"] != false {
		t.Fatalf("%v", mem)
	}
}

// 2026-10-19：Bot 把「工具做不到」記進記憶，工具更新後還一直拒絕使用者。SOUL.md 提醒它不要記這種結論。
func TestSoulWarnsAgainstMemorizingToolLimits(t *testing.T) {
	if soul := Soul(Profile{Name: "水哥哥"}); !strings.Contains(soul, "不要把「XBot 工具做不到某事」") || !strings.Contains(soul, "先查工具清單") {
		t.Fatalf("SOUL：%q", soul)
	}
}
