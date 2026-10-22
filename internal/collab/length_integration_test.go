package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestLengthEnforcement_ResearchReply tests that a 1121-character research reply
// is enforced to ≤600 characters with a file link, and the full text is in the file
func TestLengthEnforcement_ResearchReply(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Create a 1121-character research reply (not a simple fact)
	researchText := strings.Repeat("This is detailed research analysis with substantial information that provides deep insights into the problem. ", 10) // ~1050 chars
	researchText += "Final conclusion here with additional details to reach exactly the required length for this test case yes indeed."                  // ~115 chars = 1165 total

	actualLen := len([]rune(researchText))
	t.Logf("Research text length: %d runes", actualLen)

	if actualLen < 1100 {
		t.Fatalf("Research text too short: %d runes, need >1100", actualLen)
	}

	// Chief sends research to Writer
	result, err := tm.s.Send(tm.ctx, chief, writer, researchText, IntentRequest, SendOptions{})
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
	t.Logf("Sent message preview: %s", plain(msg.Text, 100))

	// Check: message should be ≤700 characters (600 base + ~100 for file link overhead)
	if sentLen > 700 {
		t.Errorf("Sent message too long: %d runes, expected ≤700 (600 + file link)", sentLen)
	}

	// Check: message should be much shorter than original
	if sentLen >= actualLen {
		t.Errorf("Message not truncated: %d runes, original was %d", sentLen, actualLen)
	}

	// Check: message should contain a file link
	if !strings.Contains(msg.Text, "/workspace/") {
		t.Errorf("Message should contain file link, got: %s", msg.Text)
	}

	// Extract file path from message
	fileLink := ""
	if idx := strings.Index(msg.Text, "/workspace/"); idx >= 0 {
		// Find the end of the path (before ), ）, space, or newline)
		pathStart := msg.Text[idx:]
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

	// Check: full text should be in the file
	fileContent, err := tm.files.Read(tm.ctx, writer, strings.TrimPrefix(fileLink, "/workspace/"))
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(fileContent) != researchText {
		t.Errorf("File content doesn't match original text\nExpected length: %d\nActual length: %d",
			len([]rune(researchText)), len([]rune(string(fileContent))))
	}
}

// TestLengthEnforcement_FactReply tests that a 92-character fact reply
// is enforced to ≤60 characters
func TestLengthEnforcement_FactReply(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Create a scenario where Writer replies to Chief with a short fact
	tm.running(chief, nil)
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", "What is the answer?", "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)

	// Writer's 92-character fact reply (simple, direct answer)
	factText := "The answer is 42. This is a simple fact with some extra text to reach exactly 92 chars!!!!"

	actualLen := len([]rune(factText))
	t.Logf("Fact text length: %d runes", actualLen)

	if actualLen < 90 {
		t.Fatalf("Fact text too short: %d runes, need ~92", actualLen)
	}

	// Finish the task with the fact reply
	tm.finish(task.ID, runtime.VerdictAnswer, factText)

	// Get the message that was sent back to Chief
	msgs, err := tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{Pair: []string{chief, writer}})
	if err != nil || len(msgs) == 0 {
		t.Fatalf("No message sent back to Chief")
	}

	// Find the reply message (last message from Writer to Chief with IntentResult)
	var msg *store.BotMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].From == writer && msgs[i].To == chief && msgs[i].Intent == IntentResult {
			msg = &msgs[i]
			break
		}
	}
	if msg == nil {
		t.Fatalf("No result message found from Writer to Chief")
	}

	sentLen := len([]rune(msg.Text))
	t.Logf("Sent message length: %d runes", sentLen)
	t.Logf("Sent message: %s", msg.Text)

	// Check: message should be ≤60 characters (fact limit)
	if sentLen > 60 {
		t.Errorf("Sent message too long: %d runes, expected ≤60", sentLen)
	}

	// Fact replies should contain the core answer
	if !strings.Contains(msg.Text, "answer is 42") {
		t.Errorf("Message should contain core fact, got: %s", msg.Text)
	}
}

// TestLengthEnforcement_RoomPost tests length enforcement in room posts
func TestLengthEnforcement_RoomPost(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name: "Length Test Room",
	}))

	// Post a long message from Chief
	longText := strings.Repeat("This is a detailed room post with substantial content. ", 20) // ~1100 chars

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, longText, ""))

	// Chief responds with the long text
	task1 := tm.queued(chief)[0]
	tm.start(task1)
	tm.finish(task1.ID, runtime.VerdictAnswer, longText)

	// Check room messages
	msgs := tm.roomLog(room.ID)
	t.Logf("Room has %d messages", len(msgs))

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
	t.Logf("Chief room message length: %d runes", msgLen)

	// Room posts use ClassRoom (200 chars), but may have file link overhead
	if msgLen > 300 {
		t.Errorf("Room message too long: %d runes, expected ≤300 (200 + file link)", msgLen)
	}

	// Check that it was truncated from the original
	if msgLen >= len([]rune(longText)) {
		t.Errorf("Room message not truncated: %d runes, original was %d", msgLen, len([]rune(longText)))
	}
}
