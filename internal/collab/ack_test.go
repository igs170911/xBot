package collab

import (
	"testing"
)

func TestIsAcknowledgement(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		// Filler 類別（應該回傳 true）：20+ 例子
		{name: "空字串", text: "", want: false},
		{name: "中文：好", text: "好", want: true},
		{name: "中文：好的", text: "好的", want: true},
		{name: "中文：收到", text: "收到", want: true},
		{name: "中文：謝謝", text: "謝謝", want: true},
		{name: "中文：了解", text: "了解", want: true},
		{name: "中文：明白了", text: "明白了", want: true},
		{name: "中文：沒問題", text: "沒問題", want: true},
		{name: "中文：太好了", text: "太好了", want: true},
		{name: "中文：嗯嗯", text: "嗯嗯", want: true},
		{name: "中文：哦哦", text: "哦哦", want: true},
		{name: "中文：好好好", text: "好好好", want: true},
		{name: "中文：讚", text: "讚", want: true},
		{name: "中文：很好", text: "很好", want: true},
		{name: "中文：不錯", text: "不錯", want: true},
		{name: "英文：ok", text: "ok", want: true},
		{name: "英文：OK", text: "OK", want: true},
		{name: "英文：thanks", text: "thanks", want: true},
		{name: "英文：thank you", text: "thank you", want: true},
		{name: "英文：got it", text: "got it", want: true},
		{name: "英文：sure", text: "sure", want: true},
		{name: "英文：cool", text: "cool", want: true},
		{name: "英文：great", text: "great", want: true},
		{name: "英文：nice", text: "nice", want: true},
		{name: "英文：awesome", text: "awesome", want: true},
		{name: "英文：perfect", text: "perfect", want: true},
		{name: "中英混合：ok 好的", text: "ok 好的", want: true},
		{name: "標點：好！", text: "好！", want: true},
		{name: "標點：OK.", text: "OK.", want: true},
		{name: "標點：收到～", text: "收到～", want: true},
		{name: "標點：謝謝！！", text: "謝謝！！", want: true},

		// 真實內容（應該回傳 false）：20+ 例子
		{name: "問句：好嗎？", text: "好嗎？", want: false},
		{name: "問句：收到了嗎？", text: "收到了嗎？", want: false},
		{name: "問句：ok?", text: "ok?", want: false},
		{name: "含數字：3 個問題", text: "3 個問題", want: false},
		{name: "含數字：收到 5 份", text: "收到 5 份", want: false},
		{name: "含數字：version 2.1", text: "version 2.1", want: false},
		{name: "含 URL：check https://example.com", text: "check https://example.com", want: false},
		{name: "含 @：@John 好的", text: "@John 好的", want: false},
		{name: "含程式碼：run `make test`", text: "run `make test`", want: false},
		{name: "超長：好的明白了這個我會處理謝謝提醒收到", text: "好的明白了這個我會處理謝謝提醒收到", want: false},
		{name: "實質內容：好的，我來修復這個 bug", text: "好的，我來修復這個 bug", want: false},
		{name: "實質內容：謝謝，我會檢查日誌", text: "謝謝，我會檢查日誌", want: false},
		{name: "實質內容：收到，開始測試新版本", text: "收到，開始測試新版本", want: false},
		{name: "實質內容：了解，需要更新文檔", text: "了解，需要更新文檔", want: false},
		{name: "實質內容：明白了，這個需要 review", text: "明白了，這個需要 review", want: false},
		{name: "實質內容：沒問題，我會推送到主分支", text: "沒問題，我會推送到主分支", want: false},
		{name: "實質內容：太好了，可以部署了", text: "太好了，可以部署了", want: false},
		{name: "實質內容：很好，繼續進行下一步", text: "很好，繼續進行下一步", want: false},
		{name: "實質內容：不錯，效能提升明顯", text: "不錯，效能提升明顯", want: false},
		{name: "實質內容：讚，記得更新版本號", text: "讚，記得更新版本號", want: false},
		{name: "實質內容：ok, but check the logs first", text: "ok, but check the logs first", want: false},
		{name: "實質內容：thanks for the update", text: "thanks for the update", want: false},
		{name: "實質內容：got it, will merge tomorrow", text: "got it, will merge tomorrow", want: false},
		{name: "實質內容：sure, just push to staging", text: "sure, just push to staging", want: false},
		{name: "實質內容：cool, deploy when ready", text: "cool, deploy when ready", want: false},
		{name: "實質內容：great work on the refactor", text: "great work on the refactor", want: false},
		{name: "實質內容：nice, remember to tag release", text: "nice, remember to tag release", want: false},
		{name: "實質內容：awesome, add tests before merge", text: "awesome, add tests before merge", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAcknowledgement(tt.text)
			if got != tt.want {
				t.Errorf("IsAcknowledgement(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestIsAcknowledgement_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "只有空格", text: "   ", want: false},
		{name: "只有標點", text: "！！！", want: false},
		{name: "只有 emoji", text: "👍", want: false},
		{name: "emoji + 好", text: "👍 好", want: true},
		{name: "20 個字：邊界", text: "好的了解明白收到謝謝可以沒問題行啊嗯哦喔", want: true},
		{name: "21 個字：超過", text: "好的了解明白收到謝謝可以沒問題行啊嗯哦喔呀", want: false},
		{name: "多個空格：ok  thanks", text: "ok  thanks", want: true},
		{name: "換行：ok\nthanks", text: "ok\nthanks", want: true},
		{name: "Tab：ok\tthanks", text: "ok\tthanks", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAcknowledgement(tt.text)
			if got != tt.want {
				t.Errorf("IsAcknowledgement(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestStripPunctWhitespaceEmoji(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"好的！", "好的"},
		{"OK.", "OK"},
		{"thank you~", "thankyou"},
		{"收到 👍", "收到"},
		{"  spaces  ", "spaces"},
		{"!!!", ""},
	}

	for _, tt := range tests {
		got := stripPunctWhitespaceEmoji(tt.input)
		if got != tt.want {
			t.Errorf("stripPunctWhitespaceEmoji(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsAllAckTokens(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"好的", true},
		{"收到", true},
		{"謝謝", true},
		{"ok", true},
		{"thanks", true},
		{"好的bug", false},
		{"修復", false},
		{"test", false},
		{"ok但是", false},
	}

	for _, tt := range tests {
		got := isAllAckTokens(tt.input)
		if got != tt.want {
			t.Errorf("isAllAckTokens(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
