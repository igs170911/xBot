package judge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func judge(t *testing.T, in Input) Result {
	t.Helper()
	if in.Outcome == "" {
		in.Outcome = OutcomeAnswer
	}
	r, err := Rules{}.Judge(context.Background(), in)
	if err != nil {
		t.Fatalf("%+v：%v", in, err)
	}
	if r.By != ByRules || r.Reason == "" || strings.Contains(r.Reason, "\n") {
		t.Fatalf("原因要一行：%+v", r)
	}
	return r
}

// LazyBoy test_a_report_asking_to_check_it_is_finished_but_going_live_still_waits_for_the_person
func TestAReportAskingToCheckItIsFinishedButGoingLiveStillWaitsForThePerson(t *testing.T) {
	for _, text := range []string{"完成了，請你確認結果", "整理好了，如果你方便的話看一下"} {
		if NeedsPersonIn(text) || HandsBackIn("完成了，請你確認結果") {
			t.Errorf("%q 不算要你本人", text)
		}
	}
	for _, text := range []string{"請你確認內容沒問題後我再正式上線", "信件寫好了，請你看過我再送出"} {
		if !NeedsPersonIn(text) {
			t.Errorf("%q 要等你同意才對外發送", text)
		}
	}
	if !HandsBackIn("短鏈讀不到，請你在蝦皮後台手動點一次取得連結") {
		t.Error("請你去後台手動點＝丟回來")
	}
	if got := judge(t, Input{Text: "完成了，請你確認結果"}); got.Verdict != Done {
		t.Errorf("完成了請你確認：%+v", got)
	}
	if got := judge(t, Input{Text: "信件寫好了，請你看過我再送出"}); got.Verdict != NeedsPerson {
		t.Errorf("送出前要你看過：%+v", got)
	}
}

// LazyBoy PROMISE：只說要做的回覆。
func TestPromise(t *testing.T) {
	yes := []string{"查一下團隊狀態，看看他們現在在做什麼。", "好的，我來查一下", "收到，我先確認一下價格。", "讓我看看", "馬上處理！",
		"了解，我去找資料", "我會整理成表格", "開始追蹤", "好的我來看", "查"}
	no := []string{
		"我查了團隊狀態：三位成員都閒置，原因是沒有人叫醒他們。", // 「查了」：已經做了
		"收到，我已派工給三位成員，正在等待結果。",        // 不是要去查
		"看過了，沒問題。", // 看過
		"確認完成。",    // 確認完
		"查一下：結果如下", // 有冒號，後面是答案
		"我來查一下？",   // 問句
		"今天天氣很好",
		"查一下" + strings.Repeat("很", 40), // 太長
	}
	for _, s := range yes {
		if !IsPromise(s) {
			t.Errorf("%q 應該算只說要做", s)
		}
	}
	for _, s := range no {
		if IsPromise(s) {
			t.Errorf("%q 不算", s)
		}
	}
}

// LazyBoy test_a_reply_that_only_promises_to_look_is_sent_back_to_actually_do_it（判斷的部分）。
func TestOnlyPromised(t *testing.T) {
	in := Input{Text: "查一下團隊狀態，看看他們現在在做什麼。"}
	if got := judge(t, in); got.Verdict != OnlyPromised || !Continues(answer(in), got) {
		t.Fatalf("%+v", got)
	}
	if n := NudgeFor(in); !strings.Contains(n.Prompt, "只回了一句要去做的話") || n.Shown != "自動繼續：剛才只說要做，現在真的去做" {
		t.Fatalf("%+v", n)
	}
	// 用了工具、回了事實、或真的是回答：不算。
	for _, in := range []Input{
		{Text: "查一下團隊狀態，看看他們現在在做什麼。", UsedTools: true},
		{Text: "我查了團隊狀態：三位成員都閒置，原因是沒有人叫醒他們。"},
		{Text: "收到，我已派工給三位成員，正在等待結果。"},
		{Text: "查一下團隊狀態", Background: true},
	} {
		if got := judge(t, in); got.Verdict != Done {
			t.Errorf("%+v → %+v", in, got)
		}
	}
	// 超過 70 字的不算只說要做。
	long := Input{Text: "我來查一下" + strings.Repeat("資料", 33)}
	if Promised(long) {
		t.Error("超過 70 字")
	}
}

// LazyBoy test_a_turn_that_stops_with_its_plan_unfinished_carries_on_by_itself（判斷與提示的部分）。
func TestPlanUnfinishedAndHandBack(t *testing.T) {
	plan := []string{"找 Threads 新貼文"}
	in := Input{Text: "商品挑好了，Threads 還沒找到。要我繼續嗎？", PlanLeft: plan}
	got := judge(t, in)
	if got.Verdict != HandedBack || !Continues(answer(in), got) {
		t.Fatalf("%+v", got)
	}
	n := NudgeFor(in)
	if !strings.Contains(n.Prompt, "找 Threads 新貼文") || !strings.Contains(n.Prompt, "不要問使用者要不要繼續") || n.Shown != "自動繼續：計畫還有 1 項沒完成" {
		t.Fatalf("%+v", n)
	}
	// 只有計畫沒做完：卡住，也自己接著做。
	in = Input{Text: "先做到這裡。", PlanLeft: plan}
	if got := judge(t, in); got.Verdict != Stuck || !Continues(answer(in), got) || !strings.Contains(got.Reason, "1 項") {
		t.Fatalf("%+v", got)
	}
	// 沒有計畫，但把工作丟回來：一樣自己接著做。問方向的問題不算。
	for _, text := range []string{
		"短鏈讀不到，請你在蝦皮後台手動點一次取得連結，把短鏈貼給我",
		// 前面順便提到的登入不擋；最後要你做的那一步才算。
		"未完成：廣告要你登入 Meta。\n\n需要你做的一小步：\n請在電腦上點回覆列、貼上草稿，按送出，然後按交還給 Bot。\n\n另外：\n- Meta 廣告活動需要你登入後建立。",
	} {
		in := Input{Text: text}
		got := judge(t, in)
		if got.Verdict != HandedBack || !Continues(answer(in), got) || NudgeFor(in).Shown != "自動繼續：自己把剩下的做完" {
			t.Errorf("%q → %+v %+v", text, got, NudgeFor(in))
		}
	}
	if got := judge(t, Input{Text: "你想走省錢路線，還是嘴碎路線？"}); got.Verdict != Done {
		t.Errorf("問方向：%+v", got)
	}
	// 要你本人（登入、對外發送前的同意）：停下等你，不自動續做。
	for _, text := range []string{
		"需要你先登入 Threads 才能繼續",
		"三則留言的草稿寫好了，請你確認內容後我再發。",
		"草稿在 /workspace/reports/drafts.md，要不要發出去？",
		"信件已經擬好，請你看一下，可以寄出去嗎？",
	} {
		in := Input{Text: text, PlanLeft: plan}
		got := judge(t, in)
		if got.Verdict != NeedsPerson || Continues(answer(in), got) || got.Ask == "" {
			t.Errorf("%q → %+v", text, got)
		}
		// 沒有計畫也一樣：結尾在要你回答。
		if got := judge(t, Input{Text: text}); got.Verdict != NeedsPerson {
			t.Errorf("沒有計畫 %q → %+v", text, got)
		}
	}
	// 只是提到登入（已經做完的）：不算要你。
	if got := judge(t, Input{Text: "已經登入後台，三篇都發好了。"}); got.Verdict != Done {
		t.Errorf("%+v", got)
	}
}

// LazyBoy test_a_bot_whose_last_helper_failed_and_was_never_resent_is_woken（判斷與提示的部分）。
func TestStranded(t *testing.T) {
	in := Input{Text: "聯盟網結果已到；只剩下其他平台的子任務失敗，尚未重派。", Background: true, Stranded: []string{"研究其他台灣平台"}}
	got := judge(t, in)
	if got.Verdict != Stuck || !Continues(answer(in), got) {
		t.Fatalf("%+v", got)
	}
	n := NudgeFor(in)
	if !strings.Contains(n.Prompt, "研究其他台灣平台") || !strings.Contains(n.Prompt, "message_bot") || n.Shown != "自動繼續：1 個背景子任務失敗，重新處理" {
		t.Fatalf("%+v", n)
	}
}

func TestFailedAndStopped(t *testing.T) {
	got := judge(t, Input{Outcome: OutcomeFailed, Text: "Error: 401"})
	if got.Verdict != Stuck || Continues(Input{Outcome: OutcomeFailed}, got) {
		t.Fatalf("%+v", got)
	}
	if got := judge(t, Input{Outcome: OutcomeFailed, Stalled: true}); !strings.Contains(got.Reason, "看門狗") {
		t.Fatalf("%+v", got)
	}
	for _, o := range []string{OutcomeCancelled, OutcomeInterrupted} {
		if _, err := (Rules{}).Judge(context.Background(), Input{Outcome: o}); !errors.Is(err, ErrSkip) {
			t.Errorf("%s：%v", o, err)
		}
	}
}

func TestDone(t *testing.T) {
	got := judge(t, Input{Text: "三家都比完了：A 最便宜，報告在 /workspace/report.md。", UsedTools: true})
	if got.Verdict != Done || Continues(Input{Outcome: OutcomeAnswer}, got) {
		t.Fatalf("%+v", got)
	}
}

// LazyBoy test_a_progress_question_gets_the_facts_with_it_and_other_messages_do_not（判斷的部分）。
func TestProgressQuestion(t *testing.T) {
	if !IsProgressQuestion("他們有在做嗎") || !IsProgressQuestion("進度如何") {
		t.Error("問進度")
	}
	for _, text := range []string{"幫我整理報告", "進度報告要寫哪幾段，請依照我上傳的檔案整理成一份很長的說明，" + strings.Repeat("細節", 30)} {
		if IsProgressQuestion(text) {
			t.Errorf("%q 不是問進度", text)
		}
	}
}

func TestAskOfTakesTheHandBackParagraph(t *testing.T) {
	text := "第一段提到登入。\n\n第二段：請你在後台手動點一下給我\n\n第三段"
	ask, handed := AskOf(text)
	if !handed || strings.Contains(ask, "第一段") || strings.Contains(ask, "第三段") || !strings.Contains(ask, "第二段") {
		t.Fatalf("%q %v", ask, handed)
	}
	long := strings.Repeat("字", 500)
	if ask, handed := AskOf(long); handed || len([]rune(ask)) != 300 {
		t.Fatalf("%d", len([]rune(ask)))
	}
}

type fakeJudge struct {
	r     Result
	err   error
	sleep time.Duration
}

func (f fakeJudge) Judge(ctx context.Context, _ Input) (Result, error) {
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return f.r, f.err
}

// 模型判斷（X05）出錯、逾時、回不認得的判斷都退回規則；規則判成要你時不降級（FJ-2、FJ-4）。
func TestFallback(t *testing.T) {
	ctx := context.Background()
	in := Input{Outcome: OutcomeAnswer, Text: "做完了"}
	model := Result{Verdict: Stuck, Reason: "模型說卡住", By: ByModel, Confidence: 0.9}
	if got, _ := (Fallback{Primary: fakeJudge{r: model}}).Judge(ctx, in); got.By != ByModel || got.Verdict != Stuck {
		t.Fatalf("%+v", got)
	}
	for _, p := range []Judge{fakeJudge{err: errors.New("429")}, fakeJudge{r: Result{Verdict: "maybe"}}, fakeJudge{r: model, sleep: time.Second}} {
		got, err := (Fallback{Primary: p, Wait: 20 * time.Millisecond}).Judge(ctx, in)
		if err != nil || got.By != ByRules || got.Verdict != Done {
			t.Errorf("%+v %v", got, err)
		}
	}
	strict := Input{Outcome: OutcomeAnswer, Text: "需要你先登入才能繼續"}
	if got, _ := (Fallback{Primary: fakeJudge{r: Result{Verdict: Done, By: ByModel}}}).Judge(ctx, strict); got.Verdict != NeedsPerson || got.By != ByRules {
		t.Fatalf("不能降級：%+v", got)
	}
	if _, err := (Fallback{Primary: fakeJudge{r: model}}).Judge(ctx, Input{Outcome: OutcomeCancelled}); !errors.Is(err, ErrSkip) {
		t.Fatal(err)
	}
}

func TestLabels(t *testing.T) {
	for _, v := range Verdicts {
		if !v.Valid() || v.Label() == string(v) {
			t.Errorf("%s", v)
		}
	}
	if Verdict("x").Valid() {
		t.Error("x")
	}
}

func answer(in Input) Input {
	in.Outcome = OutcomeAnswer
	return in
}
