package voice

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 照 OpenMausBot server/tts/speech-text.test.ts 的情境，改成中文。
func TestSpeakable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"好的！\n```python\nprint(1)\n```\n做完了", "好的！ （一段Python 程式碼） 做完了。"},
		{"看 [報價單](https://x.y/z) 和 https://shopee.tw/abc", "看 報價單 和 連結。"},
		{"![價格圖](a.png)", "（圖片：價格圖）"},
		{"| 店家 | 價格 |\n|---|---|\n| 甲 | 990 |", "店家，價格。 甲，990。"},
		{"# 標題\n- 一\n- 二", "標題。 一。 二。"},
		{"**重點** 和 *斜體* 與 ~~刪除~~", "重點 和 斜體 與 刪除。"},
		{"檔案在 /workspace/reports/q3/summary.md 裡", "檔案在 /summary.md 裡。"},
		{"完成 ✅🎉", "完成。"},
		{"Done.\nNext step", "Done. Next step."},
		{"🎉", ""},
		{"- [x] 已完成", "已完成。"},
		{"用 `npm test` 跑", "用 npm test 跑。"},
	}
	for _, c := range cases {
		if got := Speakable(c.in); got != c.want {
			t.Errorf("Speakable(%q) = %q，要 %q", c.in, got, c.want)
		}
	}
}

func TestClip(t *testing.T) {
	text := strings.Repeat("這是一句話。", 100)
	got, cut := Clip(text, 50)
	if !cut || utf8.RuneCountInString(got) > 50 || !strings.HasSuffix(got, "後面請看文字。") || !strings.Contains(got, "這是一句話。……") {
		t.Fatalf("Clip %q %v", got, cut)
	}
	if got, cut := Clip("短的", 50); cut || got != "短的" {
		t.Fatal(got)
	}
}
