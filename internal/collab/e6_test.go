package collab

import (
	"context"
	"fmt"
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

// TestE6bAgreementWithReasonPosts tests E6b: agreement + reason posts (2026-10-23)
func TestE6bAgreementWithReasonPosts(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Override with fake judge that always returns "skip"
	tm.s.o.PeerJudge = &fakeJudgeAlwaysSkip{}

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "E6b Agreement Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Owner asks question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "要用方案B嗎？", ""))

	// Chief responds with agreement + substantive reason
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "同意，因為方案B成本低一半，而且不用改資料庫")

	// Check that message was posted (agreement + reason = substantive)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "成本低") {
			botMsgs++
		}
	}
	if botMsgs != 1 {
		t.Errorf("Expected agreement with reason to post despite judge saying skip, got %d bot messages", botMsgs)
	}
}

// TestE6bAgreementWithDataPosts tests E6b: agreement + data posts (2026-10-23)
func TestE6bAgreementWithDataPosts(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Override with fake judge that always returns "skip"
	tm.s.o.PeerJudge = &fakeJudgeAlwaysSkip{}

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "E6b Data Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Owner asks question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "台北現在幾點？", ""))

	// Chief responds with agreement + data
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "好的，台北現在是下午3:20")

	// Check that message was posted (agreement + data = substantive)
	msgs := tm.roomLog(room.ID)
	botMsgs := 0
	for _, m := range msgs {
		if strings.HasPrefix(m.Sender, "bot:") && strings.Contains(m.Content, "3:20") {
			botMsgs++
		}
	}
	if botMsgs != 1 {
		t.Errorf("Expected agreement with data to post despite judge saying skip, got %d bot messages", botMsgs)
	}
}

// TestE6bPureAgreementBlocked tests E6b: pure agreement stays blocked (2026-10-23)
func TestE6bPureAgreementBlocked(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "E6b Pure Agreement Test",
		ResponderRule: store.RuleEveryone,
	}))

	// Test cases: pure agreement messages that should be blocked
	testCases := []string{
		"同意",
		"+1",
		"我也同意",
		"好的，那我們就照這個方向走。",
	}

	for i, reply := range testCases {
		// Owner asks question (different each time to avoid G5 dedupe)
		question := fmt.Sprintf("要用這個方案嗎？ (test %d)", i+1)
		must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, question, ""))

		// Chief responds with pure agreement
		chiefTask := tm.queued(chief)[0]
		tm.start(chiefTask)
		tm.finish(chiefTask.ID, runtime.VerdictAnswer, reply)

		// Check that pure agreement was blocked
		msgs := tm.roomLog(room.ID)
		found := false
		for _, m := range msgs {
			if strings.HasPrefix(m.Sender, "bot:") && m.Content == reply {
				found = true
				break
			}
		}
		if found {
			t.Errorf("Expected pure agreement %q to be blocked, but it was posted", reply)
		}
	}
}
