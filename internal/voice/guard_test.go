package voice

import "testing"

func TestVoiceGuard(t *testing.T) {
	engine := []struct {
		payload string
		tap     bool
	}{
		{`{"command":"rm -rf /workspace/old","description":"刪掉舊資料夾"}`, true},
		{`{"command":"ls"}`, true}, // 終端機指令一律要按（Hermes 只對危險指令要核准）
		{`{"description":"讀取 /workspace/報表.csv"}`, false},
		{`{"description":"寄信給客戶"}`, true},
		{`{"description":"編輯 notes.md","irreversible":true}`, true},
		{`{"prompt":"要用 3,200 元買這個網域嗎？"}`, true},
		{`not json`, true},
	}
	for _, c := range engine {
		if got := EngineApprovalNeedsTap([]byte(c.payload)); got != c.tap {
			t.Errorf("引擎核准 %s：要按＝%v，應該是 %v", c.payload, got, c.tap)
		}
	}
	if !OpApprovalNeedsTap("xbot.bots.delete", []byte(`{}`)) || !OpApprovalNeedsTap("xbot.usage.set_cap", []byte(`{}`)) {
		t.Error("confirm 級的操作一律要按")
	}
	for subject, tap := range map[string]bool{"調降 5 項商品售價": false, "付款 NT$1,280": true, "簽約：年度合作": true, "上架新品": true, "Send the newsletter": true} {
		in := []byte(`{"subject":"` + subject + `"}`)
		if got := OpApprovalNeedsTap("xbot.approvals.request", in); got != tap {
			t.Errorf("Bot 請你決定「%s」：要按＝%v，應該是 %v", subject, got, tap)
		}
	}
}
