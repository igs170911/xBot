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

// 2026-10-20：回覆太長看不下去，要像傳訊息一樣短、分段傳。回話風格取代原本的四段報告格式；給隊友的不套。
func TestSoulAsksForChatStyleReplies(t *testing.T) {
	soul := Soul(Profile{Name: "水哥哥"})
	for _, want := range []string{"## 回話風格", "段落之間空一行", "一次最多 4 段", "第一段直接講結果", "一題自己一段", "其他專案",
		"不要寫「無」", "工具之間也不要寫旁白", "** 粗體、1. 2. 編號", "要照 A 店調價嗎？\n\n", "存成 /workspace 裡的檔案", "```mermaid", "給隊友的訊息", "（回話提醒："} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
	for _, gone := range []string{"**結論**", "四段", "區分已完成、估計和未取得的資料"} {
		if strings.Contains(soul, gone) {
			t.Fatalf("不該再有 %q：%s", gone, soul)
		}
	}
	// 回話提醒在最後（名冊之後）
	if soul := Soul(Profile{Name: "A", Team: "## 隊友與協作\nB"}); !strings.HasSuffix(strings.TrimSpace(soul), "不寫過程。）") {
		t.Fatalf("提醒要放最後：%s", soul)
	}
}

func TestSoulFollowsTheReplyStyleSetting(t *testing.T) {
	cases := map[ReplyStyle]string{"": "一次最多 4 段", ReplyBrief: "一次最多 4 段", ReplyNormal: "一次最多 6 段", ReplyDetailed: "可以完整說明", "loud": "一次最多 4 段"}
	for style, want := range cases {
		if soul := Soul(Profile{Name: "A", ReplyStyle: style}); !strings.Contains(soul, want) {
			t.Fatalf("%q 要有 %q：%s", style, want, soul)
		}
	}
}

// 2026-10-20：Bot 把自己整份 SOUL.md 存成人設。PersonaOnly 只留人設；介紹是人設截出來的就不寫兩次。
func TestPersonaOnlyDropsGeneratedRules(t *testing.T) {
	full := Soul(Profile{Name: "水哥哥", Soul: "你是水哥哥，使用者的幕僚長。說話簡短。", Team: "## 隊友與協作\n研究員"})
	if got := PersonaOnly("我的人設\n\n" + full[strings.Index(full, replyStyleHead):]); got != "我的人設" {
		t.Fatalf("回話風格段落也要切掉：%q", got)
	}
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
