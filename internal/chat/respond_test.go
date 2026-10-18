package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// setupTTL 和 setup 一樣，但提問過期時間可以調短，並記下資料庫的位置（檢查密碼有沒有寫進檔案）。
func setupTTL(t *testing.T, ttl time.Duration) (*env, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(bg, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, g: hermestest.New("k"), models: &models{}}
	t.Cleanup(e.g.Close)
	d := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: e.g.Addr(), Key: "k"}, nil
	}, nil)
	e.rt = runtime.New(runtime.Options{DB: db, Driver: d, Bus: events.New(), Models: e.models, QuestionTTL: ttl})
	e.reg = ops.New(ops.Config{DB: db})
	Register(e.reg, Deps{Runtime: e.rt})
	if e.bot, err = db.CreateBot(bg, store.Bot{Name: "帳房"}); err != nil {
		t.Fatal(err)
	}
	return e, dir
}

// askThenEcho：Bot 問一個問題，把收到的回覆原樣寫在最後的回答裡。
func askThenEcho(method string, params map[string]any) func(*hermestest.Turn) {
	return func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		tr.Event("tool.start", map[string]any{"tool_id": "t1", "name": "terminal", "context": "準備付款"})
		tr.Event("tool.complete", map[string]any{"tool_id": "t1", "name": "terminal", "result": "ok"})
		res := tr.Request(method, params)
		tr.Complete("收到："+string(res), "complete", nil)
	}
}

func (e *env) needs(t *testing.T, task string) *Request {
	t.Helper()
	w := e.waitFor(t, owner, task)
	if !w.NeedsPerson || w.Request == nil || w.Request.InboxID == "" {
		t.Fatalf("應該在等人：%+v %+v", w, w.Request)
	}
	return w.Request
}

func TestRespondApprovalFromChat(t *testing.T) {
	e, _ := setupTTL(t, time.Minute)
	e.g.OnPrompt = askThenEcho("approval", map[string]any{"command": "python3 pay.py --amount 1280", "description": "付款 NT$1,280 給晨光廣告",
		"choices": []string{"once", "session", "always", "deny"}})
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "付廣告費"})
	sent := must[SendOut](t, raw, err)
	req := e.needs(t, sent.TaskID)

	// 卡片：核准、屬於這件工作與對話串、可以一律允許、你自己交辦的不會過期。
	card, err := e.db.GetInboxItem(bg, req.InboxID)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Command    string   `json:"command"`
		Choices    []string `json:"choices"`
		Unattended bool     `json:"unattended"`
		BotName    string   `json:"bot_name"`
	}
	_ = json.Unmarshal(card.Payload, &p)
	if card.Kind != "approval" || card.TaskID != sent.TaskID || card.ThreadID != sent.ThreadID || card.ExpiresAt != 0 || card.Status != "open" ||
		p.Command != "python3 pay.py --amount 1280" || strings.Join(p.Choices, ",") != "once,always,deny" || p.Unattended || p.BotName != "帳房" {
		t.Fatalf("卡片：%+v %+v", card, p)
	}

	// 代理不能回覆（SEC-8）；回覆的選項要合法。
	ag := ops.Identity{Kind: ops.KindAgent, ID: "ag", Name: "Agent", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}, Via: "mcp"}
	if _, err := e.call(ag, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": "once"}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("沒選：%v", err)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "task_id": sent.TaskID, "choice": "once"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("擇一：%v", err)
	}
	// 預演不送出。
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"task_id": sent.TaskID, "choice": "always"}, ops.Options{DryRun: true})
	if r := must[RespondOut](t, raw, err); r.Status != "open" || r.InboxID != req.InboxID {
		t.Fatalf("預演：%+v", r)
	}

	// 用 task_id 也找得到同一張卡；一律允許。
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"task_id": sent.TaskID, "choice": "always"})
	r := must[RespondOut](t, raw, err)
	if r.Status != "resolved" || r.Resolution != "always" || r.InboxID != req.InboxID {
		t.Fatalf("回覆：%+v", r)
	}
	w := e.waitFor(t, owner, sent.TaskID)
	if w.State != store.TaskDone || !strings.Contains(w.Text, `"choice":"always"`) {
		t.Fatalf("Bot 收到的回覆：%+v", w)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": "once"}); code(err) != ops.CodeConflict {
		t.Fatalf("回過了：%v", err)
	}
	card, _ = e.db.GetInboxItem(bg, req.InboxID)
	if card.Status != "resolved" || card.Resolution != "always" || card.ResolvedBy != "owner" || card.ReadAt == 0 {
		t.Fatalf("回覆後的卡：%+v", card)
	}

	// 完成後有一張「完成」通知；工作步驟讀得回來（含提問與回覆），每筆都帶對話串。
	done, _ := e.db.ListInboxFiltered(bg, store.InboxFilter{Kinds: []string{"done"}, TaskID: sent.TaskID})
	if len(done) != 1 || done[0].Status != "resolved" || done[0].ThreadID != sent.ThreadID || !strings.HasPrefix(done[0].Title, "帳房") {
		t.Fatalf("完成通知：%+v", done)
	}
	raw, err = e.call(owner, "xbot.chat.steps", map[string]any{"thread_id": sent.ThreadID})
	st := must[StepsOut](t, raw, err)
	var kinds []string
	for _, ev := range st.Events {
		kinds = append(kinds, ev.Kind)
		if ev.ThreadID != sent.ThreadID || ev.TaskID != sent.TaskID {
			t.Fatalf("事件要帶對話串與工作：%+v", ev)
		}
	}
	got := strings.Join(kinds, ",")
	for _, k := range []string{runtime.KindToolCall, runtime.KindToolResult, runtime.KindNeedsApproval, runtime.KindAnswered, runtime.KindTaskState} {
		if !strings.Contains(got, k) {
			t.Fatalf("步驟少了 %s：%s", k, got)
		}
	}
	if strings.Contains(got, "chat.done") || strings.Contains(got, "message.created") {
		t.Fatalf("步驟不含訊息：%s", got)
	}
	raw, err = e.call(owner, "xbot.chat.steps", map[string]any{"thread_id": sent.ThreadID, "limit": 2})
	if page := must[StepsOut](t, raw, err); len(page.Events) != 2 || !page.HasMore || page.NextAfter != page.Events[1].ID {
		t.Fatalf("分頁：%+v", page)
	}
}

func TestRespondQuestionAndAlwaysNotOffered(t *testing.T) {
	e, _ := setupTTL(t, time.Minute)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		a1 := tr.Request("approval", map[string]any{"command": "rm -rf /workspace/tmp", "choices": []string{"once", "deny"}})
		a2 := tr.Request("clarify", map[string]any{"questions": []map[string]any{
			{"qid": "q1", "question": "要比哪幾家？", "choices": []string{"蝦皮", "momo"}},
			{"qid": "q2", "question": "每家幾項？"},
		}})
		tr.Complete("收到："+string(a1)+" "+string(a2), "complete", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "比價"})
	sent := must[SendOut](t, raw, err)
	req := e.needs(t, sent.TaskID)
	// 這個指令 Hermes 不讓一律允許。
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": "always"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("不能一律允許：%v", err)
	}
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "decline": true})
	if r := must[RespondOut](t, raw, err); r.Resolution != "deny" {
		t.Fatalf("拒絕：%+v", r)
	}
	// 下一個問題：兩題，要逐題回答。
	var q *Request
	for i := 0; i < 50; i++ {
		if q = e.needs(t, sent.TaskID); q.InboxID != req.InboxID {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if q.Kind != "clarify" || q.InboxID == req.InboxID {
		t.Fatalf("第二個問題：%+v", q)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": q.InboxID, "text": "蝦皮"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("多題要逐題：%v", err)
	}
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": q.InboxID, "answers": []string{"蝦皮", "10 項"}})
	if r := must[RespondOut](t, raw, err); r.Resolution != "answered" {
		t.Fatalf("回答：%+v", r)
	}
	w := e.waitFor(t, owner, sent.TaskID)
	if !strings.Contains(w.Text, `"choice":"deny"`) || !strings.Contains(w.Text, `"q1":"蝦皮"`) || !strings.Contains(w.Text, `"q2":"10 項"`) {
		t.Fatalf("Bot 收到：%s", w.Text)
	}
	card, _ := e.db.GetInboxItem(bg, q.InboxID)
	if !strings.Contains(string(card.Payload), `"answer":"蝦皮；10 項"`) {
		t.Fatalf("卡片要記下回答：%s", card.Payload)
	}
}

// SEC-6：密碼只交給 Bot 的電腦，資料庫（卡片、事件、稽核、去重紀錄）裡都找不到。
func TestRespondSecretNeverStored(t *testing.T) {
	e, dir := setupTTL(t, time.Minute)
	const secret = "sk-SHOPEE-7f3a9c-secret"
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		res := tr.Request("secret", map[string]any{"prompt": "請提供蝦皮的 API 金鑰", "env_var": "SHOPEE_API_KEY"})
		ok := "沒拿到"
		if strings.Contains(string(res), secret) {
			ok = "拿到了"
		}
		tr.Complete(ok, "complete", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "接蝦皮"})
	sent := must[SendOut](t, raw, err)
	req := e.needs(t, sent.TaskID)
	card, _ := e.db.GetInboxItem(bg, req.InboxID)
	if card.Kind != "secret" || !strings.Contains(string(card.Payload), `"kind":"password"`) {
		t.Fatalf("密碼卡：%+v", card)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("空白：%v", err)
	}
	raw, err = e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "value": secret}, ops.Options{RequestID: "sec-1"})
	if r := must[RespondOut](t, raw, err); r.Resolution != "provided" {
		t.Fatalf("%+v", r)
	}
	if w := e.waitFor(t, owner, sent.TaskID); w.Text != "拿到了" {
		t.Fatalf("Bot 要拿到值：%+v", w)
	}
	// 稽核有這次回覆，但值被遮蔽。
	au, _ := e.db.ListAudit(bg, store.AuditFilter{Actor: "owner"})
	found := false
	for _, a := range au {
		if a.Op == "xbot.chat.respond" && a.Status == "ok" {
			found = true
			if !strings.Contains(string(a.Params), `"value":"***"`) {
				t.Fatalf("稽核要遮蔽：%s", a.Params)
			}
		}
	}
	if !found {
		t.Fatal("稽核沒有這次回覆")
	}
	files, _ := filepath.Glob(filepath.Join(dir, store.FileName+"*"))
	if len(files) == 0 {
		t.Fatal("找不到資料庫檔")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("密碼寫進了 %s", f)
		}
	}
}

// PRD §18：無人在場的工作，提問 10 分鐘沒回就過期（核准自動拒絕），之後可以請它再問一次。
func TestUnattendedApprovalExpiresAndAskAgain(t *testing.T) {
	e, _ := setupTTL(t, 300*time.Millisecond)
	answered := make(chan string, 1)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		if strings.Contains(tr.Text, "再問我一次") {
			tr.Complete("好，要不要付款？", "complete", nil)
			return
		}
		res := tr.Request("approval", map[string]any{"command": "pay", "choices": []string{"once", "always", "deny"}})
		answered <- string(res)
		tr.Complete("沒有核准，先停下", "complete", nil)
	}
	res, err := e.rt.Start(bg, runtime.ChatRequest{BotID: e.bot.ID, Text: "（定時任務）結清廣告費", Source: "routine"})
	if err != nil {
		t.Fatal(err)
	}
	req := e.needs(t, res.TaskID)
	card, _ := e.db.GetInboxItem(bg, req.InboxID)
	if card.ExpiresAt == 0 || !strings.Contains(string(card.Payload), `"unattended":true`) {
		t.Fatalf("無人在場要有過期時間：%+v", card)
	}
	select {
	case got := <-answered:
		if !strings.Contains(got, `"choice":"deny"`) {
			t.Fatalf("過期要自動拒絕：%s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("沒有過期")
	}
	e.waitFor(t, owner, res.TaskID)
	card, _ = e.db.GetInboxItem(bg, req.InboxID)
	if card.Status != store.InboxExpired || card.Resolution != "expired" || card.ResolvedBy != "system" {
		t.Fatalf("過期的卡：%+v", card)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "choice": "once"}); code(err) != ops.CodeConflict {
		t.Fatalf("過期了不能回：%v", err)
	}
	// 請它再問一次：同一條對話串開一件新工作。
	raw, err := e.call(owner, "xbot.chat.ask_again", map[string]any{"inbox_id": req.InboxID})
	again := must[SendOut](t, raw, err)
	if again.ThreadID != res.ThreadID || again.TaskID == "" || again.TaskID == res.TaskID {
		t.Fatalf("再問：%+v", again)
	}
	if w := e.waitFor(t, owner, again.TaskID); w.Text != "好，要不要付款？" {
		t.Fatalf("%+v", w)
	}
	card, _ = e.db.GetInboxItem(bg, req.InboxID)
	if card.Resolution != "asked_again" {
		t.Fatalf("%+v", card)
	}
	if _, err := e.call(owner, "xbot.chat.ask_again", map[string]any{"inbox_id": req.InboxID}); code(err) != ops.CodeConflict {
		t.Fatalf("不能再問第二次：%v", err)
	}
	msgs, _ := e.db.ListMessages(bg, store.ScopeThread, res.ThreadID, 0, 0)
	if !strings.Contains(msgs[len(msgs)-2].Content, "再問我一次") {
		t.Fatalf("對話裡要看得到：%+v", msgs)
	}
}

// 你停掉工作：Bot 不再等，卡片收掉；之後回覆會說已經處理過。
func TestStopDismissesQuestion(t *testing.T) {
	e, _ := setupTTL(t, time.Minute)
	e.g.OnPrompt = func(tr *hermestest.Turn) {
		tr.Event("message.start", nil)
		go tr.Request("clarify", map[string]any{"questions": []map[string]any{{"qid": "q1", "question": "要哪一版？"}}})
		<-tr.Interrupted()
		tr.Complete("", "interrupted", nil)
	}
	raw, err := e.call(owner, "xbot.chat.send", map[string]any{"bot_id": e.bot.ID, "text": "寫文案"})
	sent := must[SendOut](t, raw, err)
	req := e.needs(t, sent.TaskID)
	if _, err := e.rt.Stop(bg, e.bot.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, owner, sent.TaskID)
	card, _ := e.db.GetInboxItem(bg, req.InboxID)
	if card.Status != store.InboxDismissed || card.Resolution != "gone" {
		t.Fatalf("停掉後：%+v", card)
	}
	if _, err := e.call(owner, "xbot.chat.respond", map[string]any{"inbox_id": req.InboxID, "text": "A"}); code(err) != ops.CodeConflict {
		t.Fatalf("%v", err)
	}
	// 停掉的工作不發「完成」通知。
	if n, _ := e.db.ListInboxFiltered(bg, store.InboxFilter{TaskID: sent.TaskID, Kinds: []string{"done", "error"}}); len(n) != 0 {
		t.Fatalf("%+v", n)
	}
}
