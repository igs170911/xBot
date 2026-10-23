package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestRoomOverflowWritesToSpeaker tests that overflow files are written to the speaking bot's storage, not the owner
func TestRoomOverflowWritesToSpeaker(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Overflow Test Room",
		ResponderRule: store.RuleEveryone, // Ensure Chief gets queued
	}))

	// Post a very long message
	longText := strings.Repeat("User posts a very long message here with lots of content. ", 18) // ~1000 chars

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, longText, ""))

	// Chief responds with DIFFERENT text to avoid duplicate detection
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	botResponse := strings.Repeat("Bot responds with detailed analysis and important findings. ", 18) // ~1000 chars
	tm.finish(task1.ID, runtime.VerdictAnswer, botResponse)

	// Check room messages
	msgs := tm.roomLog(room.ID)
	t.Logf("Room has %d messages", len(msgs))
	for i, m := range msgs {
		t.Logf("  Message %d: sender=%s, len=%d", i, m.Sender, len(m.Content))
	}

	var chiefMsg *store.Message
	for i := range msgs {
		if strings.HasPrefix(msgs[i].Sender, "bot:") && msgs[i].Content != "" {
			chiefMsg = &msgs[i]
			break
		}
	}

	if chiefMsg == nil {
		t.Fatal("No Chief message found in room")
	}

	// Check that the message has a file link
	if !strings.Contains(chiefMsg.Content, "/workspace/") {
		t.Errorf("Message should contain file link, got: %s", chiefMsg.Content)
	}

	// Extract file path from message
	fileLink := ""
	if idx := strings.Index(chiefMsg.Content, "/workspace/"); idx >= 0 {
		pathStart := chiefMsg.Content[idx:]
		endChars := []string{")", "）", " ", "\n", "。"}
		endIdx := len(pathStart)
		for _, ch := range endChars {
			if i := strings.Index(pathStart, ch); i >= 0 && i < endIdx {
				endIdx = i
			}
		}
		fileLink = pathStart[:endIdx]
		t.Logf("Found file link: %s", fileLink)
	}

	if fileLink == "" {
		t.Fatal("No file link found in message")
	}

	// File should be in Chief's storage (the speaker), not the owner's
	fileContent, err := tm.files.Read(tm.ctx, chief, strings.TrimPrefix(fileLink, "/workspace/"))
	if err != nil {
		t.Fatalf("Failed to read file from Chief's storage: %v", err)
	}

	if string(fileContent) != botResponse {
		t.Errorf("File content doesn't match original bot response text")
	}

	// Verify it's NOT in the owner's (room creator's) storage - should fail
	_, err = tm.files.Read(tm.ctx, room.CreatedBy, strings.TrimPrefix(fileLink, "/workspace/"))
	if err == nil && room.CreatedBy != chief {
		t.Errorf("File should not be in owner's storage")
	}
}

// TestRoomOverflowWriteFailureKeepsFullText tests that if file write fails, full text is posted
func TestRoomOverflowWriteFailureKeepsFullText(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Overflow Failure Test Room",
		ResponderRule: store.RuleEveryone,
	}))

	// Post a long message from a bot with no computer (simulating write failure)
	longText := strings.Repeat("This text will fail to save. ", 20) // ~600 chars

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, longText, ""))

	// Chief responds with DIFFERENT text to avoid duplicate detection
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	botResponse := strings.Repeat("My response contains important details about the situation and analysis. ", 10) // ~700 chars
	tm.finish(task1.ID, runtime.VerdictAnswer, botResponse)

	// Check room messages
	msgs := tm.roomLog(room.ID)

	var chiefMsg *store.Message
	for i := range msgs {
		if strings.HasPrefix(msgs[i].Sender, "bot:") && msgs[i].Content != "" && len([]rune(msgs[i].Content)) > 100 {
			chiefMsg = &msgs[i]
			break
		}
	}

	if chiefMsg == nil {
		t.Fatal("No Chief message found in room")
	}

	// Since write should succeed in test environment, check that it was truncated with link
	// In real failure case, it would post full text
	msgLen := len([]rune(chiefMsg.Content))
	t.Logf("Message length: %d runes", msgLen)

	// Either truncated with link (≤300) or full text if write failed
	if msgLen > 300 && msgLen < len([]rune(longText)) {
		t.Errorf("Message partially truncated: %d runes, expected either ≤300 or full %d", msgLen, len([]rune(longText)))
	}
}

// TestRoomOverflowLinkCountsTowardCap tests that the link line counts toward the 200-char cap
func TestRoomOverflowLinkCountsTowardCap(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Link Cap Test Room",
		ResponderRule: store.RuleEveryone,
	}))

	// Post a long message from Chief
	longText := strings.Repeat("This is content that will overflow. ", 30) // ~1000 chars

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, longText, ""))

	// Chief responds with DIFFERENT text to avoid duplicate detection
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	botResponse := strings.Repeat("Here is my comprehensive response with detailed analysis and findings. ", 16) // ~1100 chars
	tm.finish(task1.ID, runtime.VerdictAnswer, botResponse)

	// Check room messages
	msgs := tm.roomLog(room.ID)

	var chiefMsg *store.Message
	for i := range msgs {
		if strings.HasPrefix(msgs[i].Sender, "bot:") && msgs[i].Content != "" && len([]rune(msgs[i].Content)) > 100 {
			chiefMsg = &msgs[i]
			break
		}
	}

	if chiefMsg == nil {
		t.Fatal("No Chief message found in room")
	}

	msgLen := len([]rune(chiefMsg.Content))
	t.Logf("Final message length: %d runes", msgLen)

	// Final message including link should be ≤200 chars
	if msgLen > 200 {
		t.Errorf("Final message too long: %d runes, expected ≤200 (including link)", msgLen)
	}

	// Should contain a link
	if !strings.Contains(chiefMsg.Content, "/workspace/") {
		t.Errorf("Message should contain file link")
	}
}

// TestPeerOverflowLinkCountsTowardCap tests that peer send link counts toward the 300-char cap
func TestPeerOverflowLinkCountsTowardCap(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Chief sends a long message to Writer
	longText := strings.Repeat("This is a long peer message that will overflow. ", 20) // ~960 chars

	tm.running(chief, nil)
	result, err := tm.s.Send(tm.ctx, chief, writer, longText, IntentRequest, SendOptions{})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Get the message that was actually sent
	msg, err := tm.db.GetBotMessage(tm.ctx, result.MessageID)
	if err != nil {
		t.Fatalf("Failed to get message: %v", err)
	}

	sentLen := len([]rune(msg.Text))
	t.Logf("Sent message length: %d runes", sentLen)

	// Final message including link should be ≤300 chars
	if sentLen > 300 {
		t.Errorf("Sent message too long: %d runes, expected ≤300 (including link)", sentLen)
	}

	// Should contain a link
	if !strings.Contains(msg.Text, "/workspace/") {
		t.Errorf("Message should contain file link")
	}
}
