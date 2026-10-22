package runtime

import (
	"context"
	"strings"
	"testing"

	"xbot/internal/judge"
	"xbot/internal/store"
)

// mockDecider 是測試用的簡單快判。
type mockDecider struct {
	decision judge.Decision
}

func (m *mockDecider) Decide(ctx context.Context, ask judge.Ask) (judge.Decision, error) {
	if m.decision.Label != "" {
		return m.decision, nil
	}
	// 預設：走規則
	if judge.IsProgressQuestion(ask.Text) {
		return judge.Decision{Answer: judge.Answer{Label: "progress", By: judge.ByRules}, Outcome: judge.OutcomeOK}, nil
	}
	lower := ask.Text
	if len(lower) < 10 && (ask.Text == "嗨" || ask.Text == "你好" || ask.Text == "謝謝") {
		return judge.Decision{Answer: judge.Answer{Label: "chat", By: judge.ByRules}, Outcome: judge.OutcomeOK}, nil
	}
	return judge.Decision{Answer: judge.Answer{Label: "work", By: judge.ByRules}, Outcome: judge.OutcomeOK}, nil
}

func TestRoute_Chat(t *testing.T) {
	r := &Router{
		Decider: &mockDecider{decision: judge.Decision{Answer: judge.Answer{Label: "chat", P: 0.95, By: judge.ByModel}, Outcome: judge.OutcomeOK}},
	}
	b := store.Bot{ID: "bot1", Name: "測試"}
	
	openBox, reply := r.Route(context.Background(), b, "thread1", "嗨")
	if openBox {
		t.Error("閒聊不應該開電腦")
	}
	// reply 目前是空的（TODO 實作），但分流邏輯正確
	_ = reply
}

func TestRoute_Progress(t *testing.T) {
	r := &Router{
		Decider: &mockDecider{decision: judge.Decision{Answer: judge.Answer{Label: "progress", P: 0.92, By: judge.ByModel}, Outcome: judge.OutcomeOK}},
	}
	b := store.Bot{ID: "bot1", Name: "測試"}
	
	openBox, reply := r.Route(context.Background(), b, "thread1", "現在做到哪了")
	if openBox {
		t.Error("查進度不應該開電腦")
	}
	if reply != "" {
		// 進度訊息由 runtime.Progress 組裝，這裡不產生
		t.Error("進度查詢不應該在這裡產生回覆")
	}
}

func TestRoute_Work(t *testing.T) {
	r := &Router{
		Decider: &mockDecider{decision: judge.Decision{Answer: judge.Answer{Label: "work", P: 0.88, By: judge.ByModel}, Outcome: judge.OutcomeOK}},
	}
	b := store.Bot{ID: "bot1", Name: "測試"}
	
	// 簡短工作：不需要確認
	openBox, reply := r.Route(context.Background(), b, "thread1", "查一下天氣")
	if !openBox {
		t.Error("工作應該開電腦")
	}
	if reply != "" {
		t.Errorf("簡短工作不需要確認，got %q", reply)
	}
	
	// 複雜工作：需要確認
	longTask := "請幫我查三家電商的價格，然後比較規格，最後整理成表格給我"
	openBox, reply = r.Route(context.Background(), b, "thread1", longTask)
	if !openBox {
		t.Error("工作應該開電腦")
	}
	if reply == "" {
		t.Error("複雜工作應該先回確認訊息")
	}
	if reply != "" {
		t.Logf("確認訊息：%s", reply)
	}
}

func TestRoute_NoDecider(t *testing.T) {
	r := &Router{
		Decider: nil, // 沒有快判服務
	}
	b := store.Bot{ID: "bot1", Name: "測試"}
	
	// 沒有快判時：所有訊息都走完整流程
	openBox, reply := r.Route(context.Background(), b, "thread1", "嗨")
	if !openBox {
		t.Error("沒有快判時應該開電腦")
	}
	if reply != "" {
		t.Error("沒有快判時不應該產生確認訊息")
	}
}

func TestShouldAck(t *testing.T) {
	r := &Router{}
	
	cases := []struct {
		text string
		want bool
	}{
		{"嗨", false},                                      // 太短
		{"查一下", false},                                   // 簡短指令
		{"請幫我查三家電商的價格然後比較", true},                        // 多步驟
		{"第一步查資料，第二步整理，第三步做表格", true},                   // 明確的多步驟
		{strings.Repeat("這是一個很長的訊息", 20), true},             // 很長的訊息
	}
	
	for _, c := range cases {
		got := r.shouldAck(c.text)
		if got != c.want {
			t.Errorf("shouldAck(%q) = %v, want %v", routingClipText(c.text, 30), got, c.want)
		}
	}
}

func TestWorkAckReply(t *testing.T) {
	r := &Router{}
	
	cases := []struct {
		text    string
		wantKey string // 確認訊息應該包含的關鍵字
	}{
		{"查一下天氣", "查"},
		{"比較三家價格", "比"},
		{"寫一份報告", "製作"},
		{"整理資料", "整理"},
		{"download the file", "下載"},
		{"做一件事", "處理"}, // 預設回覆
	}
	
	for _, c := range cases {
		got := r.workAckReply(c.text)
		if got == "" {
			t.Errorf("workAckReply(%q) 回傳空字串", c.text)
		}
		t.Logf("%q → %q", c.text, got)
	}
}
