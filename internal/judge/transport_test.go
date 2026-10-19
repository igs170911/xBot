package judge

import (
	"context"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/proxy"
)

// 快判只打 chat/completions：只支援 Responses API 的模型（OpenCode Go 的 muse-spark）存設定時擋下；
// 舊的設定裡已經有的，執行時略過那一層、不呼叫（不然每次都 400），設定頁列出問題，也不會偷偷改掉你的設定。
func TestJudgeSkipsModelsWithoutChatCompletions(t *testing.T) {
	e, reg := opsEnv(t)
	e.svc.Transport = proxy.NewDBStore(e.db).TransportFor
	ctx := context.Background()
	owner := ops.Owner("test")
	muse := TierModel{Provider: "opencode-go", Model: "muse-spark-1.3-contributor"}

	// 存設定：擋下，說清楚原因。
	_, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Tier1: &muse})
	if oe := ops.AsError(err); codeOf(err) != ops.CodeInvalidInput || len(oe.Fields) != 1 || oe.Fields[0].Field != "tier1.model" ||
		!strings.Contains(oe.Fields[0].Message, "只支援 Responses API") {
		t.Fatalf("Responses API 的模型應該擋下：%+v", oe)
	}
	if _, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Tier1: &goTier}); err != nil {
		t.Fatalf("chat/completions 的模型可以存：%v", err)
	}

	// 舊設定已經是 muse-spark：第一層略過、不呼叫，第二層照常回答。
	set, _ := e.svc.Settings(ctx)
	set.Tier1, set.Tier2 = muse, goTier
	if err := e.svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	// 沒改到模型的設定照樣能存。
	if _, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_set", SetIn{Tier1: &muse, Modes: map[string]Mode{UseRoute: ModeLive}}); err != nil {
		t.Fatalf("沒改模型時不擋：%v", err)
	}
	e.up.set(jsonReply("chat", 0.9, "打招呼"))
	d, err := e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨嗨"})
	if err != nil || d.Tier != Tier2 || d.ModelID != goTier.Model || !d.Live() {
		t.Fatalf("應該由第二層回答：%+v %v", d, err)
	}
	for _, c := range e.up.calls() {
		if c.Body["model"] == muse.Model {
			t.Fatalf("不該呼叫 muse-spark：%+v", c)
		}
	}
	if set, _ := e.svc.Settings(ctx); set.Tier1 != muse {
		t.Fatalf("不能偷偷改掉你的設定：%+v", set.Tier1)
	}

	// 兩層都不能用：一次都不呼叫，照規則。
	set.Tier2 = muse
	if err := e.svc.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	before := len(e.up.calls())
	d, _ = e.svc.Decide(ctx, Ask{Use: UseRoute, Text: "嗨嗨"})
	if d.Outcome != OutcomeUnsupported || d.Live() || len(e.up.calls()) != before {
		t.Fatalf("兩層都不能用：%+v，呼叫了 %d 次", d, len(e.up.calls())-before)
	}

	// 設定頁列出問題（同一個供應商一次）。
	got, err := invoke[SettingsOut](t, reg, owner, "xbot.judge.settings_get", SettingsIn{})
	if err != nil || len(got.Problems) != 1 || got.Problems[0].Kind != OutcomeUnsupported || !strings.Contains(got.Problems[0].Message, muse.Model) {
		t.Fatalf("問題：%+v %v", got.Problems, err)
	}
}
