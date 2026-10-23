package runtime

import "testing"

func TestConclusionCheck(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		wantHas       bool
		minConfidence float64
	}{
		{
			name:          "clear conclusion with result",
			text:          "比價表做好了，在 /workspace/比價.xlsx。\n\nA 店最便宜，比我們低 8%。要照 A 店的價格調嗎？",
			wantHas:       true,
			minConfidence: 0.3,
		},
		{
			name:          "incomplete - trailing next step",
			text:          "我查了三家店的價格。接下來要整理成表格。",
			wantHas:       false,
			minConfidence: 0,
		},
		{
			name:          "incomplete - mid-work",
			text:          "正在搜尋價格資訊...",
			wantHas:       false,
			minConfidence: 0,
		},
		{
			name:          "answer explanation (not strong conclusion signal)",
			text:          "iPhone 不用特地關背景 App。iOS 會自己把背景的 App 凍結，划掉反而要重新開，更耗電。\n\n只有某個 App 卡住的時候，再把它划掉重開就好。",
			wantHas:       false, // No strong conclusion indicators, just explanation
			minConfidence: 0,
		},
		{
			name:          "incomplete - waiting for user",
			text:          "我需要你的 API 金鑰才能繼續。",
			wantHas:       false,
			minConfidence: 0,
		},
		{
			name:          "empty",
			text:          "",
			wantHas:       false,
			minConfidence: 0,
		},
		{
			name:          "too short",
			text:          "好的。",
			wantHas:       false,
			minConfidence: 0,
		},
		{
			name:          "conclusion with already done",
			text:          "團隊已經建好了：行銷長、文案、設計師都在「行銷部」群組裡，向你報告。可以開始交辦工作。",
			wantHas:       true,
			minConfidence: 0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			has, conf := conclusionCheck(tt.text)
			if has != tt.wantHas {
				t.Errorf("conclusionCheck() has = %v, want %v (confidence=%v)", has, tt.wantHas, conf)
			}
			if tt.wantHas && conf < tt.minConfidence {
				t.Errorf("conclusionCheck() confidence = %v, want >= %v", conf, tt.minConfidence)
			}
		})
	}
}
