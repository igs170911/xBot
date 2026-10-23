package collab

import "testing"

// TestHasConcreteResidue tests the strict fallback logic (2026-10-22i)
func TestHasConcreteResidue(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		// === Filler phrases (should be false) ===
		{"filler1", "好的，謝謝", false},
		{"filler2", "收到", false},
		{"filler3", "了解，沒問題", false},
		{"filler4", "+1", false},
		{"filler5", "同上", false},
		{"filler6", "同意", false},
		{"filler7", "ok", false},
		{"filler8", "okay", false},
		{"filler9", "好的，照你說的", false},
		{"filler10", "交給你", false},
		{"filler11", "你決定就好", false},
		{"filler12", "我都可以", false},
		{"filler13", "就這樣吧", false},
		{"filler14", "照這個方向", false},
		{"filler15", "沒有要補充", false},
		{"filler16", "謝謝你，辛苦了", false},
		{"filler17", "好的，那我們就照這個方向走。", false},
		{"filler18", "了解，謝謝提醒", false},
		{"filler19", "looks good", false},
		{"filler20", "LGTM", false},
		{"filler21", "agree", false},
		{"filler22", "agreed", false},
		{"filler23", "sounds good", false},
		{"filler24", "got it", false},
		{"filler25", "@Writer 收到，謝謝", false},
		{"filler26", "好的，了解", false},
		{"filler27", "+1，同上", false},
		{"filler28", "Ok，照你說的做", false},

		// === Real content (should be true) ===
		{"real1", "我找到三個場地", true},                       // CJK bigrams
		{"real2", "預算 2 萬", true},                        // number
		{"real3", "10/25 下午可用嗎？", true},                  // question + number
		{"real4", "檔案在 /workspace/report.pdf", true},     // path (has /)
		{"real5", "請查收 https://example.com", true},       // URL
		{"real6", "建議選方案二", true},                        // CJK bigrams
		{"real7", "需要修改資料庫", true},                       // CJK bigrams
		{"real8", "等等，數字算錯了", true},                      // CJK bigrams
		{"real9", "已完成 80%", true},                       // number
		{"real10", "第四個有錯誤", true},                       // CJK bigrams
		{"real11", "資料和你說的不一致", true},                    // CJK bigrams
		{"real12", "需要登入 GitHub", true},                  // Latin word ≥3 chars
		{"real13", "@Chief 能不能請你處理？", true},              // question
		{"real14", "建議再加上錯誤處理", true},                    // CJK bigrams
		{"real15", "第二季成長 15%", true},                    // number
		{"real16", "發現安全漏洞", true},                       // CJK bigrams
		{"real17", "明天中午前交件", true},                      // CJK bigrams
		{"real18", "可以選擇 A 或 B 方案", true},                // CJK bigrams after stripping
		{"real19", "有三個風險：成本高", true},                    // number + CJK
		{"real20", "程式碼已 commit", true},                  // Latin word ≥3 chars
		{"real21", "iOS 15 會閃退", true},                   // number + CJK
		{"real22", "需要你核准這個改動", true},                    // CJK bigrams
		{"real23", "連結是 https://test.example.com", true}, // URL
		{"real24", "Conclusion: 專案完成", true},             // Latin + CJK
		{"real25", "這段我做好了，換你", true},                    // CJK bigrams
		{"real26", "我來改", true},                          // CJK bigrams (短但有內容)
		{"real27", "你負責這個任務", true},                      // CJK bigrams
		{"real28", "請問有什麼進展？", true},                     // question

		// === Edge cases ===
		{"edge_empty", "", false},
		{"edge_space", "   ", false},
		{"edge_single_char", "好", false},
		{"edge_at_mention_only", "@Writer", false},
		{"edge_question_with_filler", "謝謝？", true},  // question mark counts
		{"edge_number_with_filler", "好的 123", true}, // number counts
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasConcreteResidue(tt.text)
			if got != tt.expected {
				t.Errorf("hasConcreteResidue(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}
