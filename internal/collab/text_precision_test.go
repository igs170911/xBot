package collab

import (
	"testing"
)

// TestClassifyMessage_PrecisionCases tests the 2026-10-22g precision improvements
func TestClassifyMessage_PrecisionCases(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantClass MessageClass
	}{
		// Must PASS (have concrete residue)
		{
			name:      "addition with concrete content",
			text:      "@小安 我補一點：混合制比較好",
			wantClass: ClassNewInfo, // Has concrete: "混合制"
		},
		{
			name:      "conclusion with date",
			text:      "結論是下週三上線",
			wantClass: ClassResult, // Has completion word + concrete (下週三)
		},
		{
			name:      "completion with concrete",
			text:      "已經寄出給客戶了",
			wantClass: ClassResult, // Has "已經" + "寄出" + concrete residue
		},
		{
			name:      "change with quoted content",
			text:      "這版標語我改成「晨光，剛好」",
			wantClass: ClassResult, // Has "改成" + quoted string
		},
		{
			name:      "choice with proper nouns",
			text:      "我投 Slack，五個人用不到 Discord 的語音房。",
			wantClass: ClassNewInfo, // Has proper nouns (Slack, Discord) + numbers
		},
		{
			name:      "proposal with concrete topic",
			text:      "@Scout 我提議主題是人工智慧",
			wantClass: ClassHandoff, // Has "提議" + concrete content
		},

		// Must be BLOCKED (no concrete residue)
		{
			name:      "agreement with deference",
			text:      "我也建議照你說的做",
			wantClass: ClassFiller, // "照你說的" is deference, no concrete residue
		},
		{
			name:      "choice deferring to other",
			text:      "好，就選你說的那個",
			wantClass: ClassFiller, // "你說的那個" is deference, "選" without concrete
		},
		{
			name:      "decision to other",
			text:      "決定權交給你，我都可以",
			wantClass: ClassFiller, // "交給你" "我都可以" are deference
		},
		{
			name:      "your turn",
			text:      "輪到你了",
			wantClass: ClassFiller, // "輪到" is handoff verb but no concrete task
		},
		{
			name:      "agreement only",
			text:      "同意你的建議：很好",
			wantClass: ClassFiller, // "同意" "很好" are deference, no concrete after colon
		},

		// Existing test cases
		{
			name:      "pure acknowledgement",
			text:      "收到",
			wantClass: ClassFiller,
		},
		{
			name:      "question",
			text:      "今天要開會嗎？",
			wantClass: ClassQuestion,
		},
		{
			name:      "completion with concrete data",
			text:      "已經完成 5 個測試",
			wantClass: ClassResult,
		},
		{
			name:      "new info with numbers",
			text:      "這個方案需要 3 天",
			wantClass: ClassNewInfo,
		},
		{
			name:      "handoff with action verb",
			text:      "請查一下這個問題",
			wantClass: ClassHandoff,
		},
		{
			name:      "long content with concrete",
			text:      "這個問題比較複雜，需要從多個角度來看。首先要確認需求，然後評估可行性，最後制定方案。預計需要兩週時間完成。",
			wantClass: ClassNewInfo,
		},
		{
			name:      "list format",
			text:      "完成的項目：\n- 測試環境\n- 文檔更新",
			wantClass: ClassNewInfo,
		},
		{
			name:      "colon with concrete",
			text:      "標語三條已寫好：1. 晨光剛好 2. 香氣四溢 3. 每日好咖啡",
			wantClass: ClassResult,
		},
		{
			name:      "pure thanks",
			text:      "謝謝你的幫忙",
			wantClass: ClassFiller,
		},
		{
			name:      "agreement without content",
			text:      "好的，沒問題",
			wantClass: ClassFiller,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyMessage(tt.text)
			if got != tt.wantClass {
				t.Errorf("ClassifyMessage(%q) = %v, want %v", tt.text, got, tt.wantClass)
			}
		})
	}
}
