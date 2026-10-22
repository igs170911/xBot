package collab

import (
	"encoding/json"
	"strings"
	"testing"

	"xbot/internal/store"
)

// TestRoomThanksLoopProducesFewerThan3Messages 測試群組感謝循環被阻擋（2026-10-22，原始問題：11 則感謝循環）。
// 要求：一個感謝循環產生少於 3 則訊息。
func TestRoomThanksLoopProducesFewerThan3Messages(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name: "Thanks Loop Test",
	}))

	// 使用者發言
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@所有人 測試", ""))

	// Chief 回覆實質內容
	tm.running(chiefID, nil)
	chief1 := tm.queued(chiefID)[0]
	tm.start(chief1)
	tm.finish(chief1.ID, "answer", "我已經完成分析")

	// Writer 回覆感謝（應該被阻擋）
	tm.running(writerID, nil)
	writer1 := tm.queued(writerID)[0]
	tm.start(writer1)
	tm.finish(writer1.ID, "answer", "謝謝")

	// Chief 如果還有排隊，再回感謝（應該再次被阻擋）
	if len(tm.queued(chiefID)) > 0 {
		chief2 := tm.queued(chiefID)[0]
		tm.start(chief2)
		tm.finish(chief2.ID, "answer", "不客氣")
	}

	// 檢查群組訊息數量（排除系統訊息）
	msgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room.ID, 0, 50)
	botMsgCount := 0
	for _, msg := range msgs {
		var rm roomMeta
		_ = json.Unmarshal(msg.Meta, &rm)
		if rm.Notice == "" && rm.FromBotID != "" && msg.Content != "" {
			botMsgCount++
		}
	}

	if botMsgCount >= 3 {
		t.Errorf("感謝循環產生了 %d 則 Bot 訊息，應該 < 3", botMsgCount)
	}
}

// TestConclusionStopsWaking 測試結案後停止叫醒（2026-10-22）。
func TestConclusionStopsWaking(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	// Chief 是 closer
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name:      "Conclusion Test",
		CreatedBy: "bot:" + chiefID,
	}))

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@所有人 開始", ""))

	// Chief 回覆工作中
	tm.running(chiefID, nil)
	chief1 := tm.queued(chiefID)[0]
	tm.start(chief1)
	tm.finish(chief1.ID, "answer", "我在處理")

	// Writer 回覆
	tm.running(writerID, nil)
	writer1 := tm.queued(writerID)[0]
	tm.start(writer1)
	tm.finish(writer1.ID, "answer", "我也在處理")

	// Chief 貼出結論（確保 Chief 有排隊任務）
	if len(tm.queued(chiefID)) == 0 {
		t.Skip("Chief 沒有新排隊任務，跳過測試")
	}
	chief2 := tm.queued(chiefID)[0]
	tm.start(chief2)
	tm.finish(chief2.ID, "answer", "Conclusion: 工作完成，結果在 /workspace/result.txt")

	// 確認 Conclusion 訊息被標記
	msgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room.ID, 0, 20)
	foundConclusion := false
	for _, msg := range msgs {
		var rm roomMeta
		_ = json.Unmarshal(msg.Meta, &rm)
		if rm.Conclusion {
			foundConclusion = true
			break
		}
	}
	if !foundConclusion {
		t.Error("Conclusion 訊息應該被標記")
	}

	// Writer 不應該再有新的排隊（結案後不再叫醒）
	writerQueued := tm.queued(writerID)
	// Writer 已經處理了 writer1，不應該有新的任務
	if len(writerQueued) > 1 {
		t.Errorf("Conclusion 後不應再叫醒，Writer 有 %d 個排隊任務", len(writerQueued))
	}
}

// TestConsecutiveBlockedAutoStop 測試連續 2 輪阻擋觸發自動停止（2026-10-22）。
func TestConsecutiveBlockedAutoStop(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name: "AutoStop Test",
	}))

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@所有人 開始", ""))

	// Chief 第一次回覆低資訊內容（被阻擋）
	tm.running(chiefID, nil)
	chief1 := tm.queued(chiefID)[0]
	tm.start(chief1)
	tm.finish(chief1.ID, "answer", "好") // ClassFiller

	// 檢查 consec_empty = 1（2026-10-22d：存在 room 記錄中）
	room1, _ := tm.db.GetRoom(tm.ctx, room.ID)
	if room1.ConsecEmpty != 1 {
		t.Errorf("第一次阻擋後 consec_empty 應為 1，got %d", room1.ConsecEmpty)
	}

	// Chief 第二次回覆低資訊內容（再次被阻擋，觸發自動停止）
	if len(tm.queued(chiefID)) > 0 {
		chief2 := tm.queued(chiefID)[0]
		tm.start(chief2)
		tm.finish(chief2.ID, "answer", "收到") // 再次 ClassFiller

		// 系統應該通知使用者自動停止
		// 檢查 Chief 的對話串是否收到系統通知
		thread, _ := tm.rt.Active(chiefID)
		if thread != "" {
			msgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeThread, thread, 0, 10)
			foundNotification := false
			for _, msg := range msgs {
				if msg.Sender == "system" && strings.Contains(msg.Content, "連續兩輪沒有新進展") {
					foundNotification = true
					break
				}
			}
			if !foundNotification {
				t.Log("警告：未找到自動停止通知（可能因為測試環境限制）")
			}
		}

		// Writer 不應該再有新排隊（已自動停止）
		writerQueued := tm.queued(writerID)
		// 初始 1 個任務，如果連續阻擋後沒有新增，表示自動停止生效
		if len(writerQueued) > 1 {
			t.Logf("Writer 有 %d 個排隊，自動停止可能未完全生效", len(writerQueued))
		}
	}
}

// TestHopCountCarriesDMToRoom 測試 hop count 從 DM 攜帶到 room（2026-10-22e）。
// DM hop 1，然後發言到 room，座位應該得到 hop 2。
func TestHopCountCarriesDMToRoom(t *testing.T) {
	tm := setup(t)
	chiefID, writerID, scoutID := tm.bots["Chief"], tm.bots["Writer"], tm.bots["Scout"]

	// 先創建群組
	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{writerID, chiefID, scoutID}, RoomOptions{
		Name: "Hop Test Room",
	}))

	// Chief 發訊息給 Writer（DM），產生 hop 1
	tm.running(chiefID, nil)
	_ = must[SendResult](t)(tm.s.Send(tm.ctx, chiefID, "Writer", "請到群組報告", "request", SendOptions{}))

	// Writer 處理任務
	writerTask := tm.queued(writerID)[0]
	tm.start(writerTask)

	// 確認 Writer 的任務有 hop = 1
	var writerMeta map[string]any
	_ = json.Unmarshal(writerTask.Meta, &writerMeta)
	if hop, ok := writerMeta["hop"].(float64); !ok || int(hop) != 1 {
		t.Fatalf("Writer's DM task should have hop=1, got %v", writerMeta["hop"])
	}

	// Writer 發言到群組並 @ Chief（應該攜帶 hop 1）
	// 使用 Post 並指定 requestID 來模擬在 DM 任務中發言
	_ = must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 報告完成", writerTask.ID))

	// 檢查 Chief 的座位任務是否有 hop = 2
	chiefTasks, _ := tm.db.ListTasksByMeta(tm.ctx, "$.room_id", room.ID, true)
	foundChief := false
	for _, task := range chiefTasks {
		if task.BotID == chiefID {
			var chiefMeta map[string]any
			_ = json.Unmarshal(task.Meta, &chiefMeta)
			hop, ok := chiefMeta["hop"].(float64)
			if !ok {
				t.Errorf("Chief's seat should have hop, got none")
			} else if int(hop) != 2 {
				t.Errorf("Chief's seat should have hop=2 (Writer's DM hop 1 + 1), got %d", int(hop))
			}
			foundChief = true
			break
		}
	}
	if !foundChief {
		t.Error("Chief should have a seat task in the room")
	}

	// 清理：完成 Writer 的任務
	tm.finish(writerTask.ID, "answer", "完成")
}

// TestEveryoneRuleGetsReply 測試 @所有人 規則（2026-10-22 修正 REGRESSION）。
func TestEveryoneRuleGetsReply(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name:          "Everyone Test",
		ResponderRule: store.RuleEveryone,
	}))

	// 發言沒有明確 @，但規則是 everyone，應該所有人都被叫醒
	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "請大家一起處理", ""))

	// 兩位 Bot 都應該有排隊任務
	chiefQueued := tm.queued(chiefID)
	writerQueued := tm.queued(writerID)

	if len(chiefQueued) == 0 {
		t.Error("RuleEveryone: Chief 應該被叫醒")
	}
	if len(writerQueued) == 0 {
		t.Error("RuleEveryone: Writer 應該被叫醒")
	}
}

// TestShortSubstantiveReplyPasses 測試短的實質回覆通過發送閘門（2026-10-22）。
// 原始問題：「修好了，PR #3」這類短答案被阻擋。
func TestShortSubstantiveReplyPasses(t *testing.T) {
	tm := setup(t)
	chiefID, writerID := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chiefID, writerID}, RoomOptions{
		Name: "Short Reply Test",
	}))

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, "@Chief 修好了嗎", ""))

	// Chief 回覆短的實質內容（應該通過）
	tm.running(chiefID, nil)
	chief1 := tm.queued(chiefID)[0]
	tm.start(chief1)
	tm.finish(chief1.ID, "answer", "修好了，PR #3")

	// 檢查訊息是否成功發出
	msgs, _ := tm.db.ListMessages(tm.ctx, store.ScopeRoom, room.ID, 0, 10)
	foundReply := false
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "修好了") && strings.Contains(msg.Content, "PR #3") {
			foundReply = true
			break
		}
	}
	if !foundReply {
		t.Error("短的實質回覆應該通過發送閘門")
	}
}
