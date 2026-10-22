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
// 2026-10-21：講話不夠自然。口語、句子接得順、長度跟著問題走；拿掉「每段 40 字以內」和一句一段的電報範例。
// 2026-10-22：改善忙碌感：「簡潔 = 點子少一點、每個點子說完整」取代「不要寫成電報」，reply-class 取代硬性字數限制。
func TestSoulAsksForChatStyleReplies(t *testing.T) {
	soul := Soul(Profile{Name: "水哥哥"})
	for _, want := range []string{"## 回話風格", "講話自然的朋友", "句子之間接得順", "簡潔 = 點子少一點", "不是寫成電報句", "大小跟著需要走", "段落之間空一行", "直接給答案和理由",
		"一題自己一段", "其他專案", "也不寫「無」", "工具之間也不要寫旁白", "不要用 # 標題和 ** 粗體", "兩件事的比較寫成兩句話", "存成 /workspace 裡的檔案", "看使用者之後會不會再用", "/workspace/tmp/",
		"```mermaid", "所以沒算進去。要照 A 店的價格調嗎？\n\n", "給隊友的訊息", "（回話提醒："} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
	for _, gone := range []string{"**結論**", "四段", "區分已完成、估計和未取得的資料", "40 個字", "傳訊息給老闆", "一次最多 4 段", "三到五句"} {
		if strings.Contains(soul, gone) {
			t.Fatalf("不該再有 %q：%s", gone, soul)
		}
	}
	// 回話提醒在最後（名冊之後）
	if soul := Soul(Profile{Name: "A", Team: "## 隊友與協作\nB"}); !strings.HasSuffix(strings.TrimSpace(soul), "不寫過程。）") {
		t.Fatalf("提醒要放最後：%s", soul)
	}
}

// 2026-10-21：要會判斷什麼時候上網查：會變的、要精確的、沒把握的先查並附來源；常識、閒聊直接答。
func TestSoulSaysWhenToUseTools(t *testing.T) {
	soul := Soul(Profile{Name: "水哥哥"})
	for _, want := range []string{"什麼時候用工具", "先上網查再回答，附上來源連結", "一般常識、意見、閒聊、改寫、翻譯直接回答"} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
}

// 2026-10-22：Reply-class 取代硬性長度限制，檢查各模式有對應的指引。
func TestSoulFollowsTheReplyStyleSetting(t *testing.T) {
	cases := map[ReplyStyle]string{"": "簡單問題或事實", ReplyBrief: "簡單問題或事實", ReplyNormal: "需要時最多 6 段", ReplyDetailed: "完整說明", "loud": "簡單問題或事實"}
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

// 2026-10-20：「要我持續確認，他才會開始動作」。交代的事直接做到完成，不問「要開始嗎」、不說「接下來去做」就停；
// 2026-10-21：「現在好破碎」：使用者要建團隊就一次建完整，只有 Bot 自己想找隊友時先確認計畫。
// 2026-10-22：多步驟工作先說計畫、報里程碑、給結論；保留「完整做」的例子（建團隊）但改強調規劃。
func TestSoulSaysFinishWhatYouStart(t *testing.T) {
	soul := Soul(Profile{Name: "水哥哥"})
	for _, want := range []string{"多步驟工作：先說一行計畫", "完成時給結論", "先照假設做", "直接做完整套", "報告線、群組一起建好", "你自己想找隊友分工或建新 Bot 時", "沒真的查過不要說研究過", "子代理（delegate_task）不是隊友"} {
		if !strings.Contains(soul, want) {
			t.Fatalf("少了 %q：%s", want, soul)
		}
	}
	// 2026-10-22：刪掉反規劃的指令（「不要停下來問」「說要做的同一輪就做完」），改成正面的規劃要求。
	if strings.Contains(soul, "建 Bot 或團隊之前，先列計畫") {
		t.Fatal("使用者要建的團隊也先確認計畫，就做一部分停一次")
	}
	if !strings.Contains(soul, "使用者現在說的和記憶不一樣時，照現在說的做") {
		t.Fatal("少了記憶的守則")
	}
	if strings.Contains(soul, "要做很多步") {
		t.Fatal("「要做很多步」也要先確認計畫，Bot 就一直停下來問")
	}
}

// 2026-10-20：deepseek 一步只叫一個工具（每步 1.07 個，GPT luna 1.82 個），每多一步就重送整段上下文。
// 守則叫它互不相關的搜尋、讀網頁一起送出。
func TestSoulAsksForParallelLookups(t *testing.T) {
	if soul := Soul(Profile{Name: "水哥哥"}); !strings.Contains(soul, "同一步一起送出") || !strings.Contains(soul, "web_extract 一次給幾個網址") {
		t.Fatalf("SOUL：%q", soul)
	}
}

// 2026-10-20：deepseek 比價時蝦皮被擋後又換了 5 種方法硬闖（瀏覽器、改 User-Agent 的 curl、閱讀器服務），寫完檔再修補 3 次，
// 每一步都重送整段上下文。
func TestSoulStopsRetryingABlockedSite(t *testing.T) {
	if soul := Soul(Profile{Name: "水哥哥"}); !strings.Contains(soul, "擋過一次就不要換方法再闖") || !strings.Contains(soul, "一次寫完") {
		t.Fatalf("SOUL：%q", soul)
	}
}
