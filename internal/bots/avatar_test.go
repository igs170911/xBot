package bots

import (
	"strings"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

// G03／G04：形狀、眼睛、顏色、圖片都存在頭像裡，改一項不動其他項。
func TestAvatarEyesAndImage(t *testing.T) {
	e := setup(t)
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "小秘", "shape": "cursor", "eyes": "happy"}, &v)
	if v.Eyes != "happy" || v.Shape != "cursor" || v.Image != "" {
		t.Fatalf("建立：%+v", v)
	}
	img := "data:image/webp;base64,UklGRg=="
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "image": img}, &v)
	if v.Image != img || v.Eyes != "happy" || v.Shape != "cursor" {
		t.Fatalf("上傳圖片：%+v", v)
	}
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "shape": "star", "eyes": "wide"}, &v)
	if v.Image != img || v.Shape != "star" || v.Eyes != "wide" {
		t.Fatalf("換形狀保留圖片：%+v", v)
	}
	var back View
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "image": ""}, &back)
	if back.Image != "" || back.Shape != "star" {
		t.Fatalf("還原吉祥物：%+v", v)
	}

	for name, bad := range map[string]string{
		"not data url": "https://example.com/a.png",
		"svg":          "data:image/svg+xml;base64,PHN2Zz4=",
		"bad base64":   "data:image/png;base64,@@@",
		"too big":      "data:image/png;base64," + strings.Repeat("A", limits.AvatarImageBytes),
	} {
		_, err := e.call(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "image": bad})
		if code(err) != ops.CodeInvalidInput {
			t.Errorf("%s：要拒絕，得到 %v", name, err)
		}
	}
	if _, err := e.call(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "eyes": "angry"}); code(err) != ops.CodeInvalidInput {
		t.Errorf("不認得的眼睛要拒絕：%v", err)
	}
}
