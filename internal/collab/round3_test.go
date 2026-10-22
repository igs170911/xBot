package collab

import (
	"encoding/json"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestRound3ExactEvalLoop tests the exact loop message from the eval:
// '謝謝 @Chief！收到，我同意。' / '謝謝 @Writer！收到，我同意。'
// Must produce at most 2 messages. Pure agreement with no new content is filler.
func TestRound3ExactEvalLoop(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Eval 測試",
	}))

	// Start: user asks a question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 你們覺得如何？", ""))

	// Chief responds with the eval message
	chiefTask := tm.queued(chief)[0]
	tm.start(chiefTask)
	chiefMsg := "謝謝 @Writer！收到，我同意。"
	tm.finish(chiefTask.ID, runtime.VerdictAnswer, chiefMsg)

	// Check room log after Chief's message
	log := tm.roomLog(room.ID)
	t.Logf("Room log after Chief (%d messages):", len(log))
	for i, msg := range log {
		t.Logf("  [%d] %s: %s", i, msg.Sender, msg.Content)
	}

	// Check classification of Chief's message
	class := ClassifyMessage(chiefMsg)
	hasNew := HasNewInformation(chiefMsg)
	t.Logf("ClassifyMessage(%q) = %s, HasNewInformation = %v", chiefMsg, class, hasNew)

	// Writer should be woken
	if len(tm.queued(writer)) == 0 {
		t.Fatal("Writer 應該被 @ 叫醒")
	}

	writerTask := tm.queued(writer)[0]
	tm.start(writerTask)
	// Writer responds with the same pattern
	tm.finish(writerTask.ID, runtime.VerdictAnswer, "謝謝 @Chief！收到，我同意。")

	// Count bot messages in room
	log = tm.roomLog(room.ID)
	botMsgs := 0
	for _, msg := range log {
		if msg.Sender != "owner" {
			var rm roomMeta
			_ = json.Unmarshal(msg.Meta, &rm)
			if rm.Notice == "" { // Not a system message
				botMsgs++
			}
		}
	}

	if botMsgs > 2 {
		t.Errorf("純同意的循環應該在 2 則內停止，got %d 則 bot 訊息", botMsgs)
		for _, msg := range log {
			if msg.Sender != "owner" {
				t.Logf("  %s: %s", msg.Sender, msg.Content)
			}
		}
	}

	// Check that Chief was NOT woken again (loop stopped)
	chiefQueuedAgain := false
	for _, task := range tm.queued(chief) {
		if task.ID != chiefTask.ID {
			chiefQueuedAgain = true
			break
		}
	}
	if chiefQueuedAgain {
		t.Error("Chief 不應該再被叫醒（循環應該停止）")
	}
}

// TestRound3Issue3ConcludedRoomReopenOnUserMessage tests that:
// - Bots don't post after Conclusion
// - A new user message reopens the room
func TestRound3Issue3ConcludedRoomReopenOnUserMessage(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "結案測試",
	}))

	// User asks a question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 寫一個報告", ""))

	// Chief responds with conclusion
	task := tm.queued(chief)[0]
	tm.start(task)
	tm.finish(task.ID, runtime.VerdictAnswer, "報告完成。Conclusion: 已完成報告。")

	// Room should be concluded
	if !tm.s.isRoomConcluded(tm.ctx, room.ID) {
		t.Error("群組應該是已結案狀態")
	}

	// User posts another task - this should wake Chief again
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 再寫一個", ""))

	// Debug: check room log
	log := tm.roomLog(room.ID)
	t.Logf("Room log after user posts again (%d messages):", len(log))
	for i, msg := range log {
		t.Logf("  [%d] %s (created %d): %q", i, msg.Sender, msg.CreatedAt, msg.Content)
	}

	// Room should no longer be concluded
	if tm.s.isRoomConcluded(tm.ctx, room.ID) {
		t.Error("使用者發言後，群組應該解除結案狀態")
	}

	// Chief should be woken
	chiefQueued := false
	chiefTaskID := ""
	for _, task := range tm.queued(chief) {
		if task.State == store.TaskQueued {
			chiefQueued = true
			chiefTaskID = task.ID
			break
		}
	}
	if !chiefQueued {
		t.Fatal("使用者發言後，Chief 應該被叫醒")
	}

	// Chief responds again
	if chiefTaskID != "" {
		tm.start(tm.task(chiefTaskID))
		tm.finish(chiefTaskID, runtime.VerdictAnswer, "第二個報告完成。")

		// Check that Chief's messages were posted
		log := tm.roomLog(room.ID)
		chiefMsgs := 0
		for _, msg := range log {
			if msg.Sender == "bot:"+chief {
				chiefMsgs++
			}
		}
		if chiefMsgs != 2 {
			t.Errorf("Chief 應該發送 2 則訊息，got %d", chiefMsgs)
			for i, msg := range log {
				t.Logf("  [%d] %s: %q", i, msg.Sender, msg.Content)
			}
		}
	}
}

// TestRound3Issue4AutoStopDirectNotification tests that:
// - Auto-stop after 2 consecutive no-progress rounds adds a system message to the room
func TestRound3Issue4AutoStopDirectNotification(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Auto-stop 測試",
	}))

	// User posts a question
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 幫忙", ""))

	// Chief responds with filler (no new info)
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	tm.finish(task1.ID, runtime.VerdictAnswer, "收到")

	// Check: no auto-stop yet (only 1 round)
	log := tm.roomLog(room.ID)
	autoStopFound := false
	for _, msg := range log {
		if msg.Sender == "system" {
			var meta roomMeta
			_ = json.Unmarshal(msg.Meta, &meta)
			if meta.Notice == "auto_stop" {
				autoStopFound = true
			}
		}
	}
	if autoStopFound {
		t.Error("第一輪就自動停止（應該要兩輪）")
	}

	// User posts again
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 再幫忙", ""))

	// Chief responds with filler again
	task2 := tm.queued(chief)[0]
	tm.start(task2)
	tm.finish(task2.ID, runtime.VerdictAnswer, "好的")

	// Check: auto-stop notification should appear in room
	log = tm.roomLog(room.ID)
	t.Logf("Room log after second round (%d messages):", len(log))
	autoStopFound = false
	for i, msg := range log {
		t.Logf("  [%d] %s: %q", i, msg.Sender, msg.Content)
		if msg.Sender == "system" {
			var meta roomMeta
			_ = json.Unmarshal(msg.Meta, &meta)
			t.Logf("    meta.Notice: %s", meta.Notice)
			if meta.Notice == "auto_stop" {
				autoStopFound = true
				t.Logf("找到 auto-stop 系統訊息：%s", msg.Content)
			}
		}
	}
	if !autoStopFound {
		t.Error("第二輪沒有新進展應該產生 auto-stop 系統訊息")
	}
}

// TestRound3Issue5HopCarryFromSenderChain tests that:
// - DM hop=1 → room mention → room seat hop=2
func TestRound3Issue5HopCarryFromSenderChain(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Hop 測試",
	}))

	// Chief sends a DM to Writer with hop=1
	origin := tm.running(chief, map[string]any{"hop": 1})
	originTask := tm.task(origin)
	originMeta := metaOf(originTask)
	t.Logf("Chief origin task hop: %d", originMeta.Hop)

	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "請到群組回答", "request", SendOptions{}))

	// Writer should have a DM task with hop=2
	writerDMTask := tm.queued(writer)[0]
	writerDMMeta := metaOf(writerDMTask)
	if writerDMMeta.Hop != 2 {
		t.Errorf("Writer DM task hop 應該是 2，got %d", writerDMMeta.Hop)
	}

	// Writer responds and mentions Chief in the room
	tm.start(writerDMTask)
	tm.finish(writerDMTask.ID, runtime.VerdictAnswer, "好的")

	// User posts to the room, mentions Chief
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 寫一個報告", ""))

	// Chief should be woken with hop=1 (triggered by owner message with SenderHop=0)
	chiefRoomTask := tm.queued(chief)[0]
	chiefRoomMeta := metaOf(chiefRoomTask)
	if chiefRoomMeta.Hop != 1 {
		t.Errorf("Chief room task hop 應該是 1（owner SenderHop=0 +1），got %d", chiefRoomMeta.Hop)
	}

	// Chief responds and mentions Writer
	tm.start(chiefRoomTask)
	tm.finish(chiefRoomTask.ID, runtime.VerdictAnswer, "報告完成。@Writer 請檢查")

	// Check Chief's room message SenderHop
	log := tm.roomLog(room.ID)
	for _, msg := range log {
		if msg.Sender == "bot:"+chief {
			var meta roomMeta
			_ = json.Unmarshal(msg.Meta, &meta)
			t.Logf("Chief room message SenderHop: %d", meta.SenderHop)
		}
	}

	// Writer should be woken with hop=2 (Chief's message has SenderHop=1)
	writerRoomTask := tm.queued(writer)[0]
	if writerRoomTask.ID == writerDMTask.ID {
		// Skip the DM task, find the room task
		for _, task := range tm.queued(writer) {
			if task.ID != writerDMTask.ID {
				writerRoomTask = task
				break
			}
		}
	}
	writerRoomMeta := metaOf(writerRoomTask)
	if writerRoomMeta.Hop != 2 {
		t.Errorf("Writer room task hop 應該是 2（Chief SenderHop=1 +1），got %d", writerRoomMeta.Hop)
	}

	_ = origin
}

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
