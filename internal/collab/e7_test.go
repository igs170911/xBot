package collab

import (
	"context"
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestE7ThreeBotAgreementAutoStop tests that 3 bots posting agreement after conclusion triggers auto-stop with 1 inbox card
func TestE7ThreeBotAgreementAutoStop(t *testing.T) {
	tm := setup(t)
	chief, writer, scout := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]

	// Override with fake judge that returns "skip" for all agreement messages
	tm.s.o.PeerJudge = &fakeJudgeSkipAgreement{}

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer, scout}, RoomOptions{
		Name:          "E7 Agreement Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Owner posts a message that all bots respond to
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "請討論這個方案", ""))

	// All three bots post agreement (which should be blocked as "skip")
	// Chief posts first
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "同意這個結論")

	// Writer posts second
	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "我也同意")

	// Scout posts third
	scoutTask := tm.queued(scout)[0]
	tm.start(scoutTask)
	tm.finish(scoutTask.ID, runtime.VerdictAnswer, "+1")

	// Check that at most 1 message posted (the rest blocked)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") {
			botMsgs++
		}
	}
	if botMsgs > 1 {
		t.Errorf("Expected at most 1 agreement message to post, got %d", botMsgs)
	}

	// Check that exactly 1 auto-stop inbox card was created
	ref := "auto_stop:" + room.ID
	exists, err := tm.db.InboxRefExists(tm.ctx, ref)
	if err != nil {
		t.Fatalf("Error checking inbox ref: %v", err)
	}
	if !exists {
		t.Error("Expected exactly 1 auto-stop inbox card to be created for the room")
	}

	// Check that consec_empty reached threshold
	room2, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room2.ConsecEmpty < 2 {
		t.Errorf("Expected consec_empty >= 2 after blocked messages, got %d", room2.ConsecEmpty)
	}
}

// fakeJudgeSkipAgreement returns "skip" for agreement messages
type fakeJudgeSkipAgreement struct{}

func (f *fakeJudgeSkipAgreement) Classify(_ context.Context, _, msg, _ string) (string, error) {
	// Classify agreement messages as "skip"
	if strings.Contains(msg, "同意") || strings.Contains(msg, "+1") {
		return "skip", nil
	}
	return "handle", nil
}
