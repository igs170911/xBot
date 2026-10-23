package collab

import (
	"context"
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestE6OwnerQuestionSubstantiveAlwaysPosts tests that substantive replies to owner questions always post
func TestE6OwnerQuestionSubstantiveAlwaysPosts(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Override with fake judge that always returns "skip"
	tm.s.o.PeerJudge = &fakeJudgeAlwaysSkip{}

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "E6 Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Owner asks question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "台北現在幾點？", ""))

	// Chief responds with substantive answer (not acknowledgement)
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "台北現在是下午03:20")

	// Check that message was posted (even though judge said skip)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "03:20") {
			botMsgs++
		}
	}
	if botMsgs != 1 {
		t.Errorf("Expected substantive reply to owner question to post despite judge saying skip, got %d bot messages", botMsgs)
	}
}

// TestE6OwnerThanksAckBlocked tests that acknowledgement replies to owner thanks are blocked
func TestE6OwnerThanksAckBlocked(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "E6 Thanks Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Owner says thanks
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "謝謝", ""))

	// Chief responds with acknowledgement only
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "不客氣")

	// Check that acknowledgement was blocked
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") {
			botMsgs++
		}
	}
	if botMsgs != 0 {
		t.Errorf("Expected acknowledgement reply to be blocked, got %d bot messages", botMsgs)
	}
}

// TestE6OwnerMentionSubstantiveAlwaysPosts tests that substantive replies to @mentions always post
func TestE6OwnerMentionSubstantiveAlwaysPosts(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Override with fake judge that always returns "skip"
	tm.s.o.PeerJudge = &fakeJudgeAlwaysSkip{}

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "E6 Mention Test",
	}))

	// Owner @mentions with question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 請查資料", ""))

	// Chief responds with substantive answer
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "我找到三筆資料，詳見報告。")

	// Check that message was posted (even though judge said skip)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "三筆") {
			botMsgs++
		}
	}
	if botMsgs != 1 {
		t.Errorf("Expected substantive reply to @mention to post despite judge saying skip, got %d bot messages", botMsgs)
	}
}

// fakeJudgeAlwaysSkip always returns "skip" verdict
type fakeJudgeAlwaysSkip struct{}

func (f *fakeJudgeAlwaysSkip) Classify(_ context.Context, _, _, _ string) (string, error) {
	return "skip", nil
}
