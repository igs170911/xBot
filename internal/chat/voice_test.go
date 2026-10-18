package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	"xbot/internal/ops"
)

// E32：通話中用說的允許只能處理低風險的事；危險的指令要在畫面上按，用說的拒絕一律可以。
func TestRespondByVoice(t *testing.T) {
	e, _ := setupTTL(t, time.Minute)
	e.g.OnPrompt = askThenEcho("approval", map[string]any{"command": "rm -rf /workspace/old", "description": "刪掉舊資料夾",
		"choices": []string{"once", "always", "deny"}})
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "清一下"})
	sent := must[SendOut](t, raw, err)
	req := e.needs(t, sent.TaskID)
	for _, choice := range []string{"once", "always"} {
		_, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": choice, "via": "voice"})
		var oe *ops.Error
		if !errors.As(err, &oe) || oe.Code != ops.CodeForbidden || !strings.Contains(oe.Message, "畫面上按確認") {
			t.Fatalf("用說的允許危險指令（%s）應被擋：%v", choice, err)
		}
	}
	if card, _ := e.db.GetInboxItem(bg, req.InboxID); card.Status != "open" {
		t.Fatalf("被擋下的不能動到卡：%+v", card)
	}
	// 用說的拒絕可以。
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": "deny", "via": "voice"})
	if r := must[RespondOut](t, raw, err); r.Resolution != "deny" {
		t.Fatalf("用說的拒絕：%+v", r)
	}
	e.waitFor(t, owner, sent.TaskID)

	// 沒有指令、看不出風險的（例：讀檔）可以用說的允許一次。
	e.g.OnPrompt = askThenEcho("approval", map[string]any{"description": "讀取 /workspace/報表.csv", "choices": []string{"once", "deny"}})
	raw, err = e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "看報表", "thread_id": sent.ThreadID})
	sent2 := must[SendOut](t, raw, err)
	req2 := e.needs(t, sent2.TaskID)
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req2.InboxID, "choice": "once", "via": "voice"})
	if r := must[RespondOut](t, raw, err); r.Resolution != "once" {
		t.Fatalf("低風險用說的允許：%+v", r)
	}
}
