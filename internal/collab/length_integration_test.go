package collab

import (
	"strings"
	"testing"

	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestLengthEnforcement_NormalChatAnswer tests that a 120-character normal chat answer
// to a non-fact question passes through untouched
func TestLengthEnforcement_NormalChatAnswer(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Chief asks a normal (non-fact) question
	tm.running(chief, nil)
	normalQuestion := "Can you help me understand how the new feature works and what the implications are?"
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", normalQuestion, "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)

	// Writer's 120-character normal answer
	normalAnswer := "Sure! The feature works by processing requests in batches. It improves performance significantly and reduces latency."

	actualLen := len([]rune(normalAnswer))
	t.Logf("Normal answer length: %d runes", actualLen)

	if actualLen < 110 || actualLen > 130 {
		t.Fatalf("Normal answer wrong length: %d runes, need ~120", actualLen)
	}

	// Finish the task with the normal answer
	tm.finish(task.ID, runtime.VerdictAnswer, normalAnswer)

	// Get the message that was sent back to Chief
	msgs, err := tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{Pair: []string{chief, writer}})
	if err != nil || len(msgs) == 0 {
		t.Fatalf("No message sent back to Chief")
	}

	// Find the reply message
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

	// Check: message should pass through untouched (question is not a fact query)
	if msg.Text != normalAnswer {
		t.Errorf("Normal answer should pass through untouched.\nExpected: %s\nGot: %s", normalAnswer, msg.Text)
	}
}

// TestLengthEnforcement_ResearchReply tests that a 1121-character research reply
// is enforced to ≤600 characters with a file link, and the full text is in the file
func TestLengthEnforcement_ResearchReply(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	// Chief asks a complex (non-fact) question
	tm.running(chief, nil)
	complexQuestion := "Can you give me a comprehensive analysis of the system architecture and performance characteristics?"
	must[SendResult](t)(tm.s.Send(tm.ctx, chief, "Writer", complexQuestion, "request", SendOptions{}))
	task := tm.queued(writer)[0]
	tm.start(task)

	// Create a 1121-character research reply (not a simple fact)
	researchText := strings.Repeat("This is detailed research analysis with substantial information that provides deep insights into the problem. ", 10) // ~1050 chars
	researchText += "Final conclusion here with additional details to reach exactly the required length for this test case yes indeed."                  // ~115 chars = 1165 total

	actualLen := len([]rune(researchText))
	t.Logf("Research text length: %d runes", actualLen)

	if actualLen < 1100 {
		t.Fatalf("Research text too short: %d runes, need >1100", actualLen)
	}

	// Finish the task with research reply
	tm.finish(task.ID, runtime.VerdictAnswer, researchText)

	// Get the message that was sent back to Chief
	msgs, err := tm.db.ListBotMessages(tm.ctx, store.BotMessageFilter{Pair: []string{chief, writer}})
	if err != nil || len(msgs) == 0 {
		t.Fatalf("No message sent back to Chief")
	}

	// Find the reply message
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
	fileContent, err := tm.files.Read(tm.ctx, chief, strings.TrimPrefix(fileLink, "/workspace/"))
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(fileContent) != researchText {
		t.Errorf("File content doesn't match original text\nExpected length: %d\nActual length: %d",
			len([]rune(researchText)), len([]rune(string(fileContent))))
	}
}

// TestLengthEnforcement_RoomPost tests that a 1100-character room post is enforced to ≤200 characters with a file link
func TestLengthEnforcement_RoomPost(t *testing.T) {
	tm := setup(t)
	chief, writer := tm.bots["Chief"], tm.bots["Writer"]

	room := must[store.Room](t)(tm.s.CreateRoom(tm.ctx, []string{chief, writer}, RoomOptions{
		Name:          "Length Test Room",
		ResponderRule: store.RuleEveryone,
	}))

	longText := strings.Repeat("This is a detailed room post with substantial content. ", 20) // ~1100 chars

	must[PostResult](t)(tm.s.Post(tm.ctx, room.ID, longText, ""))

	task1 := tm.queued(chief)[0]
	tm.start(task1)
	// Bot responds with DIFFERENT text to avoid duplicate detection
	botResponse := strings.Repeat("I acknowledge your message with this long response containing substantive analysis. ", 15) // ~1200 chars
	tm.finish(task1.ID, runtime.VerdictAnswer, botResponse)

	msgs := tm.roomLog(room.ID)

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

	msgLen := len([]rune(chiefMsg.Content))
	t.Logf("Chief room message length: %d runes", msgLen)

	if msgLen > 300 {
		t.Errorf("Room message too long: %d runes, expected ≤300 (200 + file link)", msgLen)
	}

	if msgLen >= len([]rune(botResponse)) {
		t.Errorf("Room message not truncated: %d runes, original was %d", msgLen, len([]rune(botResponse)))
	}
}
