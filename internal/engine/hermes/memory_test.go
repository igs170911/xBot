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
	for _, want := range []string{"**結論**", "**要注意**", "**需要你回答**", "**建議下一步**", "一題一行", "其他專案", "給隊友的訊息", "不用這個格式", "不要寫「無」"} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
}

// 2026-10-20：Bot 把自己整份 SOUL.md 存成人設。PersonaOnly 只留人設；介紹是人設截出來的就不寫兩次。
func TestPersonaOnlyDropsGeneratedRules(t *testing.T) {
	full := Soul(Profile{Name: "水哥哥", Soul: "你是水哥哥，使用者的幕僚長。說話簡短。", Team: "## 隊友與協作\n研究員"})
	if got := PersonaOnly("你是水哥哥，使用者的幕僚長。說話簡短。\n\n" + full[strings.Index(full, baseRulesStart):]); got != "你是水哥哥，使用者的幕僚長。說話簡短。" {
		t.Fatalf("人設：%q", got)
	}
	if got := PersonaOnly("只寫專長。\n## 我的習慣\n早上先看信"); got != "只寫專長。\n## 我的習慣\n早上先看信" {
		t.Fatalf("使用者自己的段落要留著：%q", got)
	}
	soul := Soul(Profile{Name: "研究員", Description: "你是產業研究員。只信數據，附…", Soul: "你是產業研究員。\n只信數據，附來源。"})
	if strings.Count(soul, "你是產業研究員") != 1 {
		t.Fatalf("人設寫了兩次：%s", soul)
	}
	if soul := Soul(Profile{Name: "A", Description: "幫你比價", Soul: "說話簡短。"}); !strings.Contains(soul, "幫你比價") {
		t.Fatalf("不同的介紹要留著：%s", soul)
	}
}
