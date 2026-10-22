package collab

import (
	"testing"
)

// TestTwoBotLoopPrevention 測試兩個 Bot 互相對話不會無限循環。
func TestTwoBotLoopPrevention(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		wantClass MessageClass
		wantNew   bool
	}{
		// 應該被阻擋的（純致謝、無實質內容）
		{
			name:      "pure acknowledgement",
			message:   "收到",
			wantClass: ClassFiller,
			wantNew:   false,
		},
		{
			name:      "thanks only",
			message:   "謝謝",
			wantClass: ClassFiller,
			wantNew:   false,
		},
		{
			name:      "ok only",
			message:   "OK",
			wantClass: ClassFiller,
			wantNew:   false,
		},
		{
			name:      "got it",
			message:   "got it",
			wantClass: ClassFiller,
			wantNew:   false,
		},
		{
			name:      "understood",
			message:   "了解",
			wantClass: ClassFiller,
			wantNew:   false,
		},
		{
			name:      "filler conversation",
			message:   "好的我知道了",
			wantClass: ClassFiller,
			wantNew:   false,
		},

		// 應該通過的（有實質內容）
		{
			name:      "short substantive reply with result",
			message:   "修好了，PR #3",
			wantClass: ClassResult,
			wantNew:   true,
		},
		{
			name:      "result with number",
			message:   "完成，共 5 個檔案",
			wantClass: ClassResult, // "完成" is a result indicator
			wantNew:   true,
		},
		{
			name:      "question",
			message:   "要用哪個分支？",
			wantClass: ClassQuestion,
			wantNew:   true,
		},
		{
			name:      "handoff",
			message:   "請 @測試員 檢查",
			wantClass: ClassHandoff,
			wantNew:   true,
		},
		{
			name:      "new info with path",
			message:   "檔案在 /workspace/src/main.go",
			wantClass: ClassNewInfo,
			wantNew:   true,
		},
		{
			name:      "result with done",
			message:   "done",
			wantClass: ClassResult,
			wantNew:   true,
		},
		{
			name:      "longer substantive content",
			message:   "我已經把資料整理好放在報告裡了，請查收",
			wantClass: ClassHandoff, // contains "請", <30 chars
			wantNew:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotClass := ClassifyMessage(tt.message)
			if gotClass != tt.wantClass {
				t.Errorf("ClassifyMessage(%q) = %v, want %v", tt.message, gotClass, tt.wantClass)
			}

			gotNew := HasNewInformation(tt.message)
			if gotNew != tt.wantNew {
				t.Errorf("HasNewInformation(%q) = %v, want %v", tt.message, gotNew, tt.wantNew)
			}
		})
	}
}

// TestMessageClassification 測試各類訊息的分類。
func TestMessageClassification(t *testing.T) {
	tests := []struct {
		message string
		want    MessageClass
	}{
		// new_info
		{"這個數字是 42", ClassNewInfo},
		{"路徑：/workspace/test.txt", ClassNewInfo},
		{"網址 https://example.com", ClassNewInfo},
		{"程式碼：\n```go\nfunc main() {}\n```", ClassNewInfo},
		// 31+ chars → classified as new_info based on length
		{"這是一段超過三十個中文字元的測試訊息內容用來驗證長度判斷邏輯是否正確運作", ClassNewInfo},

		// question
		{"要繼續嗎？", ClassQuestion},
		{"請問現在的進度如何", ClassQuestion},
		{"能否幫我確認一下", ClassQuestion},
		{"怎麼做比較好？", ClassQuestion},

		// handoff
		{"@工程師 請處理", ClassHandoff},
		{"請你檢查一下", ClassHandoff},
		{"已派工給測試組", ClassHandoff},
		{"幫我看看這個問題", ClassHandoff},

		// result
		{"完成了，檔案已存", ClassResult},
		{"修好了 bug", ClassResult},
		{"已經處理好了", ClassResult},
		{"done", ClassResult},
		{"fixed the issue", ClassResult},
		{"完成，共 5 個檔案", ClassResult}, // "完成" is checked first

		// filler
		{"好", ClassFiller},
		{"收到", ClassFiller},
		{"明白", ClassFiller},
		{"", ClassFiller},
		{"   ", ClassFiller},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			got := ClassifyMessage(tt.message)
			if got != tt.want {
				t.Errorf("ClassifyMessage(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

// TestAcknowledgementDetection 測試致謝/確認訊息的檢測。
func TestAcknowledgementDetection(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		// 應該被識別為致謝
		{"收到", true},
		{"了解", true},
		{"明白", true},
		{"好的", true},
		{"知道了", true},
		{"謝謝", true},
		{"感謝", true},
		{"好", true},
		{"行", true},
		{"OK", true},
		{"okay", true},
		{"got it", true},
		{"received", true},
		{"understood", true},
		{"thanks", true},
		{"thank you", true},
		{"will do", true},
		{"on it", true},
		{"sure", true},
		{"noted", true},
		{"ack", true},
		{"roger", true},
		{"copy", true},
		{"收到。", true},
		{"了解！", true},
		{"好的，", true},
		{"同意", true},
		{"沒問題", true},
		{"可以", true},
		{"沒錯", true},
		{"對", true},
		{"是的", true},
		{"收到你的訊息", true},
		{"收到了", true},
		{"got your message", true},

		// 不應該被識別為致謝（有實質內容）
		{"收到 5 個檔案", false},
		{"了解，我現在開始處理", false},
		{"好的我去查一下資料", false},
		{"謝謝提供的資訊，我會根據這些數據分析", false},
		{"OK, here's the result", false},
		{"got it, the issue is in line 42", false},
		// Complex case: starts with acknowledgement but has substantive content
		// Current implementation treats this conservatively as acknowledgement
		// to avoid missing loop prevention opportunities
		{"收到你的請求", true},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			got := IsAcknowledgement(tt.message)
			if got != tt.want {
				t.Errorf("IsAcknowledgement(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

// TestShortSubstantiveReplies 測試短而實質的回覆能通過（如"修好了，PR #3"）。
func TestShortSubstantiveReplies(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		// 短而實質的回覆應該通過
		{"修好了，PR #3", true},
		{"完成，PR #5", true},
		{"done, PR #10", true},
		{"搞定 issue 42", true},
		{"處理好了 ticket 123", true},
		{"fixed bug #7", true},
		{"已提交 commit abc123", true},
		{"成功部署到 prod", true},

		// 純致謝不通過
		{"收到", false},
		{"謝謝", false},
		{"OK", false},

		// 空白不通過
		{"", false},
		{"   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			got := HasNewInformation(tt.message)
			if got != tt.want {
				t.Errorf("HasNewInformation(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}
