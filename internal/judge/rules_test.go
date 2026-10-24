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
	if n := NudgeFor(in, Result{}); !strings.Contains(n.Prompt, "只回了一句要去做的話") || n.Shown != "自動繼續：剛才只說要做，現在真的去做" {
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
	n := NudgeFor(in, Result{})
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
		if got.Verdict != HandedBack || !Continues(answer(in), got) || NudgeFor(in, Result{}).Shown != "自動繼續：自己把剩下的做完" {
			t.Errorf("%q → %+v %+v", text, got, NudgeFor(in, Result{}))
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
	n := NudgeFor(in, Result{})
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

// 2026-10-20：「要我持續確認，他才會開始動作」。水哥哥用過工具之後最後一句說「現在去做某某」就結束、
// 或問「要我現在開始抓嗎？」：都要自己接著做（XBot 新增，LazyBoy 只看整輪沒用工具的短句）。
func TestEndingWithNextStepIsNotDone(t *testing.T) {
	ctx := context.Background()
	for _, text := range []string{
		"已登入，帳號確認是 @kupi.cat.31。現在去個人頁抓基本盤，然後去洞察頁。",
		"好，帳號是 @kupi.cat.31「Ruby 王酷比｜台灣生活實測」。按技能流程：先抓數據、再給方案。\n\n讓我開瀏覽器看帳號。先檢查有沒有已登入的 Threads 分頁。",
		"資料夠了，接下來我去整理成報告",
		"抱歉，上一輪 browser_exec 結束後我沒有自動繼續。現在繼續。\n\n已登入、帳號確認 @kupi.cat.31。接下來：去個人頁抓基本盤 → 洞察頁 → 逐篇貼文。",
	} {
		r, err := Rules{}.Judge(ctx, Input{Outcome: OutcomeAnswer, Text: text, UsedTools: true})
		if err != nil || r.Verdict != OnlyPromised || !Continues(Input{Outcome: OutcomeAnswer, Text: text, UsedTools: true}, r) {
			t.Fatalf("%q → %+v %v", text, r, err)
		}
		if n := NudgeFor(Input{Outcome: OutcomeAnswer, Text: text, UsedTools: true}, Result{}); !strings.Contains(n.Prompt, "卻沒有做就結束了") {
			t.Fatalf("提示：%s", n.Prompt)
		}
	}
	if NeedsPersonIn("已登入，帳號確認是 @kupi.cat.31。") || !NeedsPersonIn("請你先登入 Threads") {
		t.Fatal("「已登入」不是在等你登入；「請你先登入」才是")
	}
	ask := "沒有在做了。我剛才只載入了技能。\n\n要我現在開始抓嗎？流程是：開瀏覽器、抓個人頁、抓洞察頁。"
	if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: ask}); r.Verdict != HandedBack {
		t.Fatalf("問「要我現在開始嗎」：%+v", r)
	}
	for _, ask := range []string{
		"我會開三個子代理，分別查台北天氣、美元兌台幣匯率、台積電最新股價，等全部回來後整理成三行。\n\n請確認要開始嗎？",
		"計畫如上，可以開始嗎？",
		"我會同時開三個子代理，各自查多個來源並寫約 500 字摘要，最後彙整回報。\n\n請確認要照這個計畫執行。",
		"我會同時開三個子代理，最後彙整回報。\n\n確認要照這個計畫執行嗎？",
		"各查多個來源並交付約 500 字摘要。\n\n要照這個計畫開始嗎？",
	} {
		if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: ask}); r.Verdict != HandedBack {
			t.Fatalf("%q：%+v", ask, r)
		}
	}
	// 等你開口才做的提議（「直接講要做什麼，我就開始」）：不算只說要做（2026-10-20：回「嗨」被自動繼續，Bot 自己編了工作）
	for _, text := range []string{
		"我是你電腦裡的常駐 Bot，可以幫你查資料、做報表。\n\n有想先做哪一件嗎？直接講要做什麼，我就開始。",
		"需要的話，我去查最新的價格。",
		"你確認之後，我就開始整理。",
	} {
		if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: text}); r.Verdict == OnlyPromised || Continues(Input{Outcome: OutcomeAnswer, Text: text}, r) {
			t.Fatalf("提議不是只說要做：%q → %+v", text, r)
		}
	}
	// 說明流程時提到核准、「取得結果後再判斷」：不是在要你做事（重播快判找到的誤判）
	explain := "模型需要時就呼叫工具執行動作，取得結果後再判斷，可能要幾輪。\n\n需要我等你按核准，或等隊友回覆時，流程會停在 F 和 H 之間；這段等待不算我思考的時間。"
	if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: explain}); r.Verdict != Done {
		t.Fatalf("說明流程：%+v", r)
	}
	if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: "蝦皮要登入才看得到價格，你登入後再叫我繼續。"}); r.Verdict != NeedsPerson {
		t.Fatalf("真的要你登入：%+v", r)
	}
	// 程式碼區塊、流程圖裡的字不算 Bot 在說話
	diagram := "流程是這樣：\n\n```mermaid\nflowchart TD\n    F --> G{需要動手做嗎}\n    G -- 要 --> H[呼叫工具]\n```\n\n等核准時流程會停在 F 和 H 之間。"
	if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: diagram}); r.Verdict != Done {
		t.Fatalf("流程圖：%+v", r)
	}
	if got := Prose("a\n```py\nprint('要開始嗎？')\n```\nb"); strings.Contains(got, "要開始嗎") || !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Fatalf("Prose：%q", got)
	}
	// 做完了、在問真的要你決定的事、提到以後的事：不算
	for _, text := range []string{
		"報告整理好了，在 /workspace/reports/漲粉方案.md。",
		"接下來我整理好了，放在桌面。",
		"要照 A 店調價嗎？",
		"比價表做好了。\n\n先看定稿方案，再看卡點報告決定是否核准採購。",
	} {
		if r, _ := (Rules{}).Judge(ctx, Input{Outcome: OutcomeAnswer, Text: text, UsedTools: true}); r.Verdict == OnlyPromised {
			t.Fatalf("%q 不該算只說要做：%+v", text, r)
		}
	}
}

// 2026-10-20：beam-free 把同一個 web_search 送了 50 次，Hermes 停下這一輪；原本判成「已完成」，不會接著做。
// 引擎停下的重複是卡住：自己換做法接著做，快判模型不能改判。
func TestLoopHaltIsStuckAndContinues(t *testing.T) {
	in := Input{Outcome: OutcomeAnswer, Text: "我一直重複用 web_search（50 次）都沒有新進展，先停下來。", UsedTools: true, Looped: "web_search", LoopCount: 50}
	got := judge(t, in)
	if got.Verdict != Stuck || !Continues(in, got) || !strings.Contains(got.Reason, "web_search（50 次）") {
		t.Fatalf("%+v", got)
	}
	n := NudgeFor(in, Result{})
	if !strings.Contains(n.Prompt, "不要再送同樣的呼叫") || !strings.Contains(n.Prompt, "先用已經拿到的資料把成品做出來") || n.Shown != "自動繼續：一直重複同一個動作，換個做法" {
		t.Fatalf("%+v", n)
	}
	if got, _ := (Fallback{Primary: fakeJudge{r: Result{Verdict: Done, By: ByModel}}}).Judge(context.Background(), in); got.Verdict != Stuck || got.By != ByRules {
		t.Fatalf("快判模型不能改判：%+v", got)
	}
}

// 2026-10-23：你交代了它卻沒做（10/19～21 的真實例子）。只看你本人交代的第一輪。
func TestResearchAskedButNotDone(t *testing.T) {
	asked := "三五人吧，你研究一下職能，看我需要什麼我不知道"
	for _, c := range []struct {
		name     string
		in       Input
		want     Verdict
		research bool
	}{
		{"沒查就回", Input{Asked: asked, Text: "建議五個角色：主管、研究、驗證、文案、資料整理。"}, OnlyPromised, true},
		{"查了一次就說研究完", Input{Asked: asked, Research: 1, UsedTools: true, Text: "研究完了，建議五個角色。"}, OnlyPromised, true},
		{"真的查了", Input{Asked: asked, Research: 4, UsedTools: true, Text: "建議五個角色，來源附在檔案。"}, Done, false},
		{"研究員是名字", Input{Asked: "研究員最近在忙什麼", Text: "研究員在查貓砂。"}, Done, false},
		{"先不要做、先討論", Input{Asked: "你先不要做動，先跟我討論，研究一下需要哪些角色", Text: "我覺得要五個角色。"}, Done, false},
		{"系統叫它接著做的那一輪不再套", Input{Asked: asked, Continued: true, Text: "建議五個角色。"}, Done, false},
		{"隊友交辦的不套", Input{Text: "建議五個角色。"}, Done, false},
		{"deep-research 技能", Input{Asked: "使用技能「deep-research」：比較三家電信的 5G 吃到飽", Text: "遠傳最划算。"}, OnlyPromised, true},
		{"問進度裡提到研究結果", Input{Asked: "為啥停止不了？然後你背景做的事情有在做嗎？你說研究結果回來後再補完整方案的事情", Text: "背景的研究還在跑。"}, Done, false},
		{"在程式裡查", Input{Asked: asked, Research: 2, UsedTools: true, Text: "研究完了，建議五個角色。"}, Done, false},
	} {
		c.in.Outcome = OutcomeAnswer
		got, _ := Rules{}.Judge(context.Background(), c.in)
		if got.Verdict != c.want {
			t.Errorf("%s：%+v", c.name, got)
			continue
		}
		if c.research {
			n := NudgeFor(c.in, got)
			if !strings.Contains(n.Prompt, "web_search") || n.Shown != "自動繼續：要研究，先真的去查" || !Continues(c.in, got) {
				t.Errorf("%s 的提示：%+v", c.name, n)
			}
		}
	}
}

func TestOfferEndingAfterATaskIsHandedBack(t *testing.T) {
	for _, c := range []struct {
		name, asked, text string
		want              Verdict
	}{
		{"建了一部分就問", "五人版本其他也要建立", "行銷長建好了。\n\n要我繼續建其他四位成員嗎？", HandedBack},
		{"要我開群組嗎", "把大家整理到群組裡", "五位成員都建好了，報告線也設了。\n\n要我開一個新群組嗎？", HandedBack},
		{"確認後就動手", "幫我建一個三人的影片團隊", "我打算這樣分工：A 剪輯、B 腳本、C 發布。確認後我就動手建。", HandedBack},
		{"沒寫我：要繼續建嗎", "另建立行銷長，不是我喔", "行銷長已建立，負責統籌方向、審核產出。要繼續建其他成員（研究員、驗證員、文案、資料整理員）嗎？", HandedBack},
		{"沒寫我：要設報告線嗎", "五人版本其他也要建立R", "五人團隊建好：行銷長、研究員、驗證員、文案、資料整理員。要設報告線（四人向行銷長報告）或開群組對話嗎？", HandedBack},
		{"問要哪些職能不是要它建", "我想要建立一個蝦皮海巡團隊，每個 agent 要有啥職能", "建議五個角色：隊長、海巡員、選品、寫手、查證。\n\n要我照這個建團隊嗎？", Done},
		{"要你選的不算", "把三家的報價整理成一張表", "比價表做好了，在 /workspace/比價.xlsx。\n\nA 店最便宜，比我們低 8%。要照 A 店的價格調嗎？", Done},
		{"只是問問題", "怎麼建團隊比較好？", "先定主管，再分職能。要我直接幫你建嗎？", Done},
		{"你說先討論", "你先不要做動，先跟我討論要哪些角色", "建議五個角色。\n\n要我建團隊嗎？", Done},
		{"刪除要先問", "整理一下群組", "整理好了。\n\n要我把舊的群組刪掉嗎？", Done},
		{"打招呼", "嗨", "嗨！要我幫你做什麼嗎？", Done},
		{"做完了沒問", "幫我建一個三人的影片團隊", "三位都建好了，群組也開了。", Done},
	} {
		in := Input{Outcome: OutcomeAnswer, Asked: c.asked, Text: c.text, UsedTools: true}
		got, _ := Rules{}.Judge(context.Background(), in)
		if got.Verdict != c.want {
			t.Errorf("%s：%+v", c.name, got)
			continue
		}
		if c.want == HandedBack && strings.HasPrefix(got.Reason, reasonOffer) {
			if n := NudgeFor(in, got); !strings.Contains(n.Prompt, "照他交代的直接做完") || n.Shown != "自動繼續：照交代的直接做完" {
				t.Errorf("%s 的提示：%+v", c.name, n)
			}
		}
	}
	in := Input{Outcome: OutcomeAnswer, Asked: "五人版本其他也要建立", Text: "要我繼續建其他四位成員嗎？", Continued: true}
	if got, _ := (Rules{}).Judge(context.Background(), in); got.Verdict == HandedBack && strings.HasPrefix(got.Reason, reasonOffer) {
		t.Errorf("接著做的那一輪不再套新規則：%+v", got)
	}
}

func TestResearchTools(t *testing.T) {
	for name, want := range map[string]bool{"web_search": true, "websearch": true, "Exa Web Search: marketing team roles": true, "web_extract": true,
		"browser_navigate": true, "execute_code": true, "WebFetch": true, "terminal": false, "write_file": false, "memory": false} {
		if IsResearchTool(name) != want {
			t.Errorf("IsResearchTool(%q) 要 %v", name, want)
		}
	}
}

func TestIsChitchat(t *testing.T) {
	for asked, want := range map[string]bool{
		"嗨": true, "測試：台北現在大概幾點？一句話回答就好。": true, "只回我一個字「好」": true, "為啥一定要開電腦？": true,
		"禿頭怎麼辦，落健有用嗎": true, "幫我建一個三人的影片團隊": false, "你研究一下職能": false, "把這份表整理成三欄": false,
		"幫我查一下台積電今天的收盤價？": false,
	} {
		if got := IsChitchat(asked); got != want {
			t.Errorf("IsChitchat(%q) = %v，要 %v", asked, got, want)
		}
	}
}

// 2026-10-19～23 實際開出的「需要你處理」卡（8 張）：登入、核准這類字要在「要你做事或回答的那一句」裡才算。
// 介紹功能時講到登入、講自己遇到哪類事會先送核准、引號裡描述做過的事，都不是在等你；真的要你註冊、登入的照樣算。
func TestNeedsPersonOnlyWhenTheAskingSentenceNeedsYou(t *testing.T) {
	cases := []struct {
		name, asked, text string
		want              bool
	}{
		{"介紹功能：講到登入、講會先送核准", "你好島輝，你有啥功能",
			"我是島灰，常駐在你這台電腦上。查資料、比價、寫報告、做檔案我都能直接處理，需要登入或點來點去的網站我也會自己開瀏覽器看完再整理給你。\n\n" +
				"行銷團隊那幾位我可以直接傳訊息交辦。定價、花錢、簽約這類要你拍板的，我會先送核准再做。想看哪一塊再跟我說就好。", false},
		{"介紹功能：問你想先試哪一種", "介紹一下你的功能",
			"可以幫你上網查證並整理成報告或試算表，也能開瀏覽器去登入、填表、抓資料；要定時做的事可以設排程。\n\n需要分工時我可以直接發工作給他們。你有想先試哪一種？", false},
		{"引號裡描述做過的事", "你隨意操作一下電腦做一件事，讓我確定你有使用電腦的能力",
			"最後寫成檔案存到 /workspace/電腦測試紀錄.md。\n\n另外有件事要說：我一開始想執行系統指令看機器狀態，卻跳出「需要你同意」的關卡；等了五分鐘沒回應，就沒執行成功。" +
				"所以終端機指令要你按允許我才跑得動，瀏覽器和寫檔案則是可以直接做。", false},
		{"回答你問的登入問題、最後問你選哪一種", "我給你一組 api key 或者是 code plan 登入帳號，會在兩個地方登入嗎",
			"所以我這邊一登入，你自己電腦上的 session 很可能就斷了。\n\n要讓我跑訂閱型的，比較安全是另外開一個帳號或給 API key；用同一組就是兩邊不要同時跑。你打算給哪一種？", false},
		{"真的要你本人註冊", "",
			"我已經把選品定案升到 v1.2。\n\n有一件事需要你本人處理：momo 分潤要註冊新後台（affiliate.momoshop.com.tw），舊版點點賺 1 月就關了。註冊免費，但要用你的資料，我沒辦法代辦。", true},
		{"真的要你登入", "幫我抓後台的訂單",
			"後台要先登入才看得到訂單。請你在 Bot 的電腦登入蝦皮賣家中心，登入好跟我說一聲我就接著抓。", true},
		{"要你給驗證碼", "幫我登入銀行查餘額", "已經輸入帳號密碼了，銀行傳了簡訊驗證碼到你的手機，可以把驗證碼給我嗎？", true},
	}
	for _, c := range cases {
		r, err := Rules{}.Judge(context.Background(), Input{Outcome: OutcomeAnswer, Text: c.text, Asked: c.asked, UsedTools: true, Research: 3})
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Verdict == NeedsPerson; got != c.want {
			t.Errorf("%s：%s（%s）", c.name, r.Verdict, r.Reason)
		}
	}
}
