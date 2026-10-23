package engine

import (
	"strings"
	"testing"

	"xbot/internal/limits"
)

func TestFinalAnswer(t *testing.T) {
	long := strings.Repeat("答", limits.InterimNarrationMax+1)
	many := make([]string, 20)
	for i := range many {
		many[i] = "接著把表格改窄，再截圖確認。"
	}
	for _, c := range []struct {
		name string
		segs []string
		want string
	}{
		{"沒用工具：照原本", []string{"你好"}, "你好"},
		{"一句旁白", []string{"我先看一下。", "答案是 42。"}, "答案是 42。"},
		{"很多句旁白加起來再長也是旁白", append(many, "做好了。"), "做好了。"},
		{"先寫完答案再存檔：答案留著", []string{long, "  ", "已存檔。"}, long + "\n\n已存檔。"},
		{"最後是工具呼叫、沒寫結論：照原本整輪的文字", []string{"我先看一下。", " "}, ""},
		{"沒有文字", nil, ""},
	} {
		if got := FinalAnswer(c.segs); got != c.want {
			t.Errorf("%s：%q", c.name, got)
		}
	}
}
