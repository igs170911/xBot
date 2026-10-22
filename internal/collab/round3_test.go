package collab

import (
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestRound3Issue1AddressedCheckOnlyTriggeringMessage tests that:
// - Only the triggering message counts for @addressed check, not all recent messages
// - '@Writer Slack 還是 Discord？' -> '選 Slack。' posts successfully
// - An @-thanks ping-pong between two bots produces at most 2 messages
func TestRound3Issue1AddressedCheckOnlyTriggeringMessage(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "選工具",
	}))

	// Test case 1: Short answer to @question should post
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Writer Slack 還是 Discord？", ""))
	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "選 Slack。")

	log := tm.roomLog(room.ID)
	if len(log) < 2 {
		t.Fatalf("期待至少 2 則訊息（問題+回答），got %d", len(log))
	}
	lastMsg := log[len(log)-1]
	if lastMsg.Content != "選 Slack。" {
		t.Errorf("最後一則應該是「選 Slack。」，got %q", lastMsg.Content)
	}

	// Test case 2: @-thanks ping-pong should be limited
	// Chief thanks Writer
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Writer 謝謝！", ""))
	if q := tm.queued(writer); len(q) > 0 {
		writerTask2 := q[0]
		tm.start(writerTask2)
		tm.finish(writerTask2.ID, runtime.VerdictAnswer, "不客氣") // Should be blocked as filler

		// Writer's filler reply should not post
		log2 := tm.roomLog(room.ID)
		foundFiller := false
		for _, msg := range log2 {
			if msg.Content == "不客氣" {
				foundFiller = true
				break
			}
		}
		if foundFiller {
			t.Error("純客套話「不客氣」應該被阻擋，不應出現在群組")
		}

		// Chief thanks again - should not wake Writer again because previous message didn't post
		chiefQueued := tm.queued(chief)
		if len(chiefQueued) > 0 {
			t.Error("Writer 的回覆被阻擋後，不應再產生新的 Chief 任務（避免 ping-pong）")
		}
	}
}

// TestRound3Issue2ConclusionExemptionBeforeClassFiller tests that:
// - Conclusion: messages pass even if they contain pleasantries
// - Works in DM context
// - Works in room context without @
func TestRound3Issue2ConclusionExemptionBeforeClassFiller(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Test case 1: Conclusion in DM with pleasantries should pass
	result, err := tm.s.Send(tm.ctx, chief, writer, "Conclusion: 謝謝配合，任務完成。", "result", SendOptions{})
	if err != nil {
		t.Fatalf("Send 失敗：%v", err)
	}
	if !result.Sent {
		t.Error("Conclusion: 訊息應該發送成功，即使包含客套話")
	}

	// Test case 2: Conclusion in room without @ should pass
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "測試結案",
		ResponderRule: store.RuleLead,
		LeadID:        chief,
	}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 開始工作", ""))

	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	// Chief posts conclusion without @-ing anyone
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, "Conclusion: 感謝大家，工作已完成。")

	log := tm.roomLog(room.ID)
	foundConclusion := false
	for _, msg := range log {
		if msg.Content == "Conclusion: 感謝大家，工作已完成。" {
			foundConclusion = true
			break
		}
	}
	if !foundConclusion {
		t.Error("Conclusion: 訊息應該貼到群組，即使沒有 @ 且包含客套話")
	}
}

// TestRound3Issue6FyiNotBlockedForOutgoing tests that:
// - Room replies use isOutgoing=true, so 'fyi' verdicts don't block
// - DM replies use isOutgoing=true, so 'fyi' verdicts don't block
// - Only filler is blocked, results/answers/handoffs pass
func TestRound3Issue6FyiNotBlockedForOutgoing(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Test case 1: Room reply with potential 'fyi' content should pass if it has substance
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "FYI 測試",
	}))
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Writer 請查資料", ""))

	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	// Writer sends informational reply (would be 'fyi' if judge ran)
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "查到了，數據在 /data/report.csv")

	log := tm.roomLog(room.ID)
	foundReply := false
	for _, msg := range log {
		if msg.Content == "查到了，數據在 /data/report.csv" {
			foundReply = true
			break
		}
	}
	if !foundReply {
		t.Error("有實質內容的回覆應該通過，即使可能被判為 fyi")
	}

	// Test case 2: DM reply with result should pass
	result, err := tm.s.Send(tm.ctx, chief, writer, "幫我整理資料", "request", SendOptions{})
	if err != nil {
		t.Fatalf("Send 失敗：%v", err)
	}
	if !result.Sent {
		t.Fatal("Request 應該發送成功")
	}

	// Writer finishes and sends result (not pure filler)
	writerDMTask := tm.queued(writer)[0]
	tm.start(writerDMTask)
	tm.finish(writerDMTask.ID, runtime.VerdictAnswer, "已完成，詳見 report.xlsx")

	// Check that the reply was delivered (not blocked)
	chiefTasks := tm.queued(chief)
	foundResult := false
	for _, t := range chiefTasks {
		m := metaOf(t)
		if m.FromBot == writer && m.Intent == IntentResult {
			foundResult = true
			break
		}
	}
	if !foundResult {
		t.Error("結果訊息應該送達 Chief，不應被 fyi 阻擋")
	}
}
