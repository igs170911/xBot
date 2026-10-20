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

// 2026-10-19：Bot 說「我沒有改群組名稱的工具」。SOUL.md 告訴它改群組、刪群組各用哪個工具。
func TestSoulTellsHowToChangeAGroup(t *testing.T) {
	if soul := Soul(Profile{Name: "水哥哥"}); !strings.Contains(soul, "xbot.collab.update_group") || !strings.Contains(soul, "xbot.collab.remove_group") {
		t.Fatalf("SOUL：%q", soul)
	}
}

// 2026-10-20：回覆太多雜訊。回覆使用者用結論、要注意、需要你回答、建議下一步四段；給隊友的不套。
func TestSoulAsksForTheOwnerReplyFormat(t *testing.T) {
	soul := Soul(Profile{Name: "水哥哥"})
	for _, want := range []string{"**結論**", "**要注意**", "**需要你回答**", "**建議下一步**", "一題一行", "其他專案", "給隊友的訊息", "不用這個格式"} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
}
