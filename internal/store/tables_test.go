package store

import (
	"encoding/json"
	"errors"
	"testing"

	"xbot/internal/limits"
)

func TestBotCRUD(t *testing.T) {
	db := openTest(t)
	b := must[Bot](t)(db.CreateBot(ctx, Bot{
		Name: "小秘", Engine: "hermes", Fallback: []string{"a", "b"}, Avatar: json.RawMessage(`{"shape":"circle"}`),
		MemoryEnabled: true, IsChief: true,
	}))
	if b.ID == "" || b.CreatedAt == 0 || b.IdleMinutes != limits.BoxIdleMinutes {
		t.Fatalf("新增後欄位不對：%+v", b)
	}
	if string(b.ToolScope) != "{}" || len(b.Fallback) != 2 || !b.MemoryEnabled || !b.IsChief || b.TeamID != "" {
		t.Fatalf("預設值不對：%+v", b)
	}
	got := must[Bot](t)(db.GetBot(ctx, b.ID))
	if string(got.Avatar) != `{"shape":"circle"}` || got.Fallback[1] != "b" {
		t.Fatalf("讀回來不對：%+v", got)
	}

	got.Title = "秘書"
	got.TeamID = "team1"
	got.Fallback = nil
	got = must[Bot](t)(db.UpdateBot(ctx, got))
	if got.Title != "秘書" || got.TeamID != "team1" || len(got.Fallback) != 0 || got.CreatedAt != b.CreatedAt {
		t.Fatalf("更新後不對：%+v", got)
	}
	if _, err := db.UpdateBot(ctx, Bot{ID: "沒有"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的 Bot：%v", err)
	}

	other := newBot(t, db, "阿寶")
	check(t, db.SetBotArchived(ctx, other.ID, true))
	if bots := must[[]Bot](t)(db.ListBots(ctx, false)); len(bots) != 1 || bots[0].ID != b.ID {
		t.Fatalf("不含封存：%+v", bots)
	}
	if bots := must[[]Bot](t)(db.ListBots(ctx, true)); len(bots) != 2 {
		t.Fatalf("含封存：%d", len(bots))
	}
	if _, err := db.GetBot(ctx, "沒有"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("讀不存在的 Bot：%v", err)
	}
}

func TestHostAndBox(t *testing.T) {
	db := openTest(t)
	h := must[Host](t)(db.CreateHost(ctx, Host{Name: "這台主機", Kind: "local", MaxRunning: 2}))
	if h.Status != "unknown" {
		t.Fatalf("預設狀態：%s", h.Status)
	}
	if _, err := db.CreateHost(ctx, Host{Name: "這台主機", Kind: "local"}); err == nil {
		t.Fatal("主機名稱應該不能重複")
	}
	if _, err := db.CreateHost(ctx, Host{Name: "怪", Kind: "boat"}); err == nil {
		t.Fatal("不支援的主機類型應該失敗")
	}
	check(t, db.SetHostStatus(ctx, h.ID, "online"))
	got := must[Host](t)(db.GetHost(ctx, h.ID))
	if got.Status != "online" || got.LastSeenAt == 0 {
		t.Fatalf("在線狀態：%+v", got)
	}
	seen := got.LastSeenAt
	check(t, db.SetHostStatus(ctx, h.ID, "offline"))
	if got = must[Host](t)(db.GetHost(ctx, h.ID)); got.Status != "offline" || got.LastSeenAt != seen {
		t.Fatalf("離線不應該改 last_seen_at：%+v", got)
	}
	got.Address = "ssh://xbot@vps"
	got.MaxRunning = 4
	check(t, db.UpdateHost(ctx, got))
	if hs := must[[]Host](t)(db.ListHosts(ctx)); len(hs) != 1 || hs[0].MaxRunning != 4 || hs[0].Address != "ssh://xbot@vps" {
		t.Fatalf("更新主機：%+v", hs)
	}

	b := newBot(t, db, "小秘")
	if _, err := db.GetBox(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("還沒有電腦：%v", err)
	}
	check(t, db.UpsertBox(ctx, Box{BotID: b.ID, HostID: h.ID, State: "creating"}))
	box := must[Box](t)(db.GetBox(ctx, b.ID))
	if box.State != "creating" || box.HostID != h.ID || string(box.Limits) != "{}" {
		t.Fatalf("新增電腦：%+v", box)
	}
	created := box.CreatedAt
	box.ContainerID = "abc"
	box.State = "running"
	box.NoVNCPort = 6080
	check(t, db.UpsertBox(ctx, box))
	box = must[Box](t)(db.GetBox(ctx, b.ID))
	if box.ContainerID != "abc" || box.NoVNCPort != 6080 || box.CreatedAt != created {
		t.Fatalf("覆寫電腦：%+v", box)
	}
	if err := db.SetBoxState(ctx, b.ID, "exploded", ""); err == nil {
		t.Fatal("不合法的電腦狀態應該失敗")
	}
	check(t, db.SetBoxState(ctx, b.ID, "error", "映像拉不下來"))
	check(t, db.TouchBox(ctx, b.ID))
	box = must[Box](t)(db.GetBox(ctx, b.ID))
	if box.State != "error" || box.Error != "映像拉不下來" || box.LastActiveAt == 0 {
		t.Fatalf("狀態更新：%+v", box)
	}
	if bs := must[[]Box](t)(db.ListBoxes(ctx, h.ID)); len(bs) != 1 {
		t.Fatalf("依主機列電腦：%d", len(bs))
	}
	if bs := must[[]Box](t)(db.ListBoxes(ctx, "別台")); len(bs) != 0 {
		t.Fatalf("別台主機不應該有電腦：%d", len(bs))
	}
	if bs := must[[]Box](t)(db.ListBoxes(ctx, "")); len(bs) != 1 {
		t.Fatalf("全部電腦：%d", len(bs))
	}
	if err := db.SetBoxState(ctx, "沒有", "running", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的電腦：%v", err)
	}
}

func TestThreadsAndMessages(t *testing.T) {
	db := openTest(t)
	b := newBot(t, db, "小秘")
	t1 := must[Thread](t)(db.CreateThread(ctx, Thread{BotID: b.ID, Title: "一"}))
	t2 := must[Thread](t)(db.CreateThread(ctx, Thread{BotID: b.ID, Title: "二"}))

	must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: t1.ID, Sender: "owner", Content: "你好"}))
	m := must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: t1.ID, Sender: "bot:" + b.ID, Content: "嗨"}))
	if m.Seq == 0 || m.Kind != "text" || string(m.Attachments) != "[]" {
		t.Fatalf("新增訊息：%+v", m)
	}
	th := must[Thread](t)(db.GetThread(ctx, t1.ID))
	if th.Unread != 1 || th.LastMessageAt == 0 {
		t.Fatalf("Bot 的回覆應該算未讀、自己的不算：%+v", th)
	}
	check(t, db.MarkThreadRead(ctx, t1.ID))
	if th = must[Thread](t)(db.GetThread(ctx, t1.ID)); th.Unread != 0 {
		t.Fatalf("已讀：%d", th.Unread)
	}

	// 有訊息的 t1 排在前面；釘選的 t2 再排到最前。
	if ts := must[[]Thread](t)(db.ListThreads(ctx, b.ID, false)); ts[0].ID != t1.ID {
		t.Fatalf("最近有訊息的在前：%v", ts)
	}
	t2.Pinned = true
	check(t, db.UpdateThread(ctx, t2))
	if ts := must[[]Thread](t)(db.ListThreads(ctx, b.ID, false)); ts[0].ID != t2.ID {
		t.Fatalf("釘選的在前：%v", ts)
	}
	t2.Archived = true
	check(t, db.UpdateThread(ctx, t2))
	if ts := must[[]Thread](t)(db.ListThreads(ctx, b.ID, false)); len(ts) != 1 {
		t.Fatalf("不含封存：%d", len(ts))
	}
	check(t, db.SetThreadUsage(ctx, t1.ID, json.RawMessage(`{"tokens":3}`)))
	if th = must[Thread](t)(db.GetThread(ctx, t1.ID)); string(th.Usage) != `{"tokens":3}` {
		t.Fatalf("用量：%s", th.Usage)
	}

	// 分頁：往前翻。
	for i := range 5 {
		must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: t1.ID, Sender: "owner", Content: string(rune('a' + i))}))
	}
	all := must[[]Message](t)(db.ListMessages(ctx, ScopeThread, t1.ID, 0, 0))
	if len(all) != 7 || all[0].Content != "你好" || all[6].Content != "e" {
		t.Fatalf("全部訊息：%d", len(all))
	}
	last3 := must[[]Message](t)(db.ListMessages(ctx, ScopeThread, t1.ID, 0, 3))
	if len(last3) != 3 || last3[0].Content != "c" || last3[2].Content != "e" {
		t.Fatalf("最新 3 則：%+v", last3)
	}
	prev := must[[]Message](t)(db.ListMessages(ctx, ScopeThread, t1.ID, last3[0].Seq, 2))
	if len(prev) != 2 || prev[0].Content != "a" || prev[1].Content != "b" {
		t.Fatalf("往前 2 則：%+v", prev)
	}
	if n := must[int](t)(db.CountMessages(ctx, ScopeThread, t1.ID)); n != 7 {
		t.Fatalf("訊息數：%d", n)
	}

	reply := must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: t1.ID, ParentID: m.ID, Sender: "system", Kind: "activity"}))
	check(t, db.UpdateMessage(ctx, reply.ID, "完成", nil, json.RawMessage(`{"done":true}`)))
	got := must[Message](t)(db.GetMessage(ctx, reply.ID))
	if got.Content != "完成" || got.ParentID != m.ID || string(got.Meta) != `{"done":true}` {
		t.Fatalf("更新訊息：%+v", got)
	}
	if _, err := db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: t1.ID, Sender: "owner", Kind: "telepathy"}); err == nil {
		t.Fatal("不合法的訊息種類應該失敗")
	}
	if _, err := db.AddMessage(ctx, Message{Scope: "dm", ScopeID: "x", Sender: "owner"}); err == nil {
		t.Fatal("不合法的範圍應該失敗")
	}
	// 群組訊息不碰 thread 表。
	must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeRoom, ScopeID: "room1", Sender: "owner"}))

	// 刪對話串會連訊息一起刪，群組訊息不受影響。
	check(t, db.DeleteThread(ctx, t1.ID))
	if n := must[int](t)(db.CountMessages(ctx, ScopeThread, t1.ID)); n != 0 {
		t.Fatalf("刪對話串後還有 %d 則訊息", n)
	}
	if n := must[int](t)(db.CountMessages(ctx, ScopeRoom, "room1")); n != 1 {
		t.Fatalf("群組訊息不應該被刪：%d", n)
	}
}

func TestTasks(t *testing.T) {
	db := openTest(t)
	b := newBot(t, db, "小秘")
	th := must[Thread](t)(db.CreateThread(ctx, Thread{BotID: b.ID}))
	task := must[Task](t)(db.CreateTask(ctx, Task{BotID: b.ID, ThreadID: th.ID, Source: "owner", Input: "整理報表"}))
	if task.State != TaskQueued {
		t.Fatalf("預設狀態：%s", task.State)
	}
	if _, err := db.CreateTask(ctx, Task{BotID: b.ID, Source: "alien"}); err == nil {
		t.Fatal("不合法的來源應該失敗")
	}
	check(t, db.SetTaskState(ctx, task.ID, TaskRunning, ""))
	started := must[Task](t)(db.GetTask(ctx, task.ID)).StartedAt
	if started == 0 {
		t.Fatal("進 running 應該記 started_at")
	}
	check(t, db.SetTaskState(ctx, task.ID, TaskWaitingApproval, ""))
	check(t, db.SetTaskState(ctx, task.ID, TaskRunning, ""))
	if got := must[Task](t)(db.GetTask(ctx, task.ID)); got.StartedAt != started {
		t.Fatal("再進 running 不應該改 started_at")
	}
	other := must[Task](t)(db.CreateTask(ctx, Task{BotID: b.ID, Source: "routine", LowPriority: true}))
	if open := must[[]Task](t)(db.ListOpenTasks(ctx)); len(open) != 2 {
		t.Fatalf("未完成任務：%d", len(open))
	}
	check(t, db.SetTaskState(ctx, task.ID, TaskDone, "completed"))
	got := must[Task](t)(db.GetTask(ctx, task.ID))
	if got.Verdict != "completed" || got.FinishedAt == 0 {
		t.Fatalf("完成：%+v", got)
	}
	if open := must[[]Task](t)(db.ListTasks(ctx, b.ID, true)); len(open) != 1 || open[0].ID != other.ID || !open[0].LowPriority {
		t.Fatalf("只列未完成：%+v", open)
	}
	if all := must[[]Task](t)(db.ListTasks(ctx, b.ID, false)); len(all) != 2 {
		t.Fatalf("全部任務：%d", len(all))
	}
	if err := db.SetTaskState(ctx, task.ID, "paused", ""); err == nil {
		t.Fatal("不合法的任務狀態應該失敗")
	}
	// 刪對話串：任務留下，thread_id 清空。
	check(t, db.DeleteThread(ctx, th.ID))
	if got := must[Task](t)(db.GetTask(ctx, task.ID)); got.ThreadID != "" {
		t.Fatalf("thread_id 應該清空：%q", got.ThreadID)
	}
}

func TestEvents(t *testing.T) {
	db := openTest(t)
	a := newBot(t, db, "甲")
	b := newBot(t, db, "乙")
	for i := range 10 {
		bot := a.ID
		if i%2 == 1 {
			bot = b.ID
		}
		e := must[Event](t)(db.AppendEvent(ctx, Event{BotID: bot, Kind: "delta", Payload: json.RawMessage(`{"i":1}`)}))
		if e.ID != int64(i+1) {
			t.Fatalf("事件 id 應該遞增：%d", e.ID)
		}
	}
	global := must[Event](t)(db.AppendEvent(ctx, Event{Kind: "host.online"}))
	if string(global.Payload) != "{}" {
		t.Fatalf("空 payload：%s", global.Payload)
	}
	if es := must[[]Event](t)(db.ListEvents(ctx, "", 8, 0)); len(es) != 3 || es[2].BotID != "" {
		t.Fatalf("since 8：%+v", es)
	}
	if es := must[[]Event](t)(db.ListEvents(ctx, a.ID, 0, 2)); len(es) != 2 || es[0].ID != 1 || es[1].ID != 3 {
		t.Fatalf("只看甲、最多 2 筆：%+v", es)
	}
	if id := must[int64](t)(db.LastEventID(ctx, b.ID)); id != 10 {
		t.Fatalf("乙最後事件：%d", id)
	}
	if id := must[int64](t)(db.LastEventID(ctx, "")); id != 11 {
		t.Fatalf("全部最後事件：%d", id)
	}
	check(t, db.PruneEvents(ctx, a.ID, 2))
	es := must[[]Event](t)(db.ListEvents(ctx, a.ID, 0, 0))
	if len(es) != 2 || es[0].ID != 7 || es[1].ID != 9 {
		t.Fatalf("保留最新 2 筆：%+v", es)
	}
	if es := must[[]Event](t)(db.ListEvents(ctx, b.ID, 0, 0)); len(es) != 5 {
		t.Fatalf("乙不受影響：%d", len(es))
	}
	check(t, db.PruneEvents(ctx, a.ID, limits.EventsKeepPerBot)) // 筆數不足時不刪
	if es := must[[]Event](t)(db.ListEvents(ctx, a.ID, 0, 0)); len(es) != 2 {
		t.Fatalf("筆數不足不應該刪：%d", len(es))
	}
}

func TestAudit(t *testing.T) {
	db := openTest(t)
	for i, actor := range []string{"owner", "agent:t1", "owner"} {
		a := must[AuditEntry](t)(db.AddAudit(ctx, AuditEntry{Actor: actor, Via: "mcp", Op: "xbot.bots.create", Target: "bot:x", Status: "ok",
			Params: json.RawMessage(`{"key":"***"}`)}))
		if a.ID != int64(i+1) {
			t.Fatalf("id：%d", a.ID)
		}
	}
	all := must[[]AuditEntry](t)(db.ListAudit(ctx, AuditFilter{}))
	if len(all) != 3 || all[0].ID != 3 || string(all[0].Params) != `{"key":"***"}` {
		t.Fatalf("新到舊：%+v", all)
	}
	if owner := must[[]AuditEntry](t)(db.ListAudit(ctx, AuditFilter{Actor: "owner"})); len(owner) != 2 {
		t.Fatalf("依 actor：%d", len(owner))
	}
	if page := must[[]AuditEntry](t)(db.ListAudit(ctx, AuditFilter{Before: 3, Limit: 1})); len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("翻頁：%+v", page)
	}
	if none := must[[]AuditEntry](t)(db.ListAudit(ctx, AuditFilter{Target: "host:y"})); len(none) != 0 {
		t.Fatalf("依 target：%d", len(none))
	}
}

func TestAgentTokens(t *testing.T) {
	db := openTest(t)
	tok := must[AgentToken](t)(db.CreateAgentToken(ctx, AgentToken{Name: "Claude Code", TokenHash: "h1", Scopes: []string{"read", "operate"}}))
	if _, err := db.CreateAgentToken(ctx, AgentToken{Name: "重複", TokenHash: "h1"}); err == nil {
		t.Fatal("雜湊不能重複")
	}
	found := must[AgentToken](t)(db.FindAgentToken(ctx, "h1"))
	if found.ID != tok.ID || len(found.Scopes) != 2 || found.Scopes[1] != "operate" {
		t.Fatalf("用雜湊找：%+v", found)
	}
	check(t, db.TouchAgentToken(ctx, tok.ID))
	if got := must[AgentToken](t)(db.GetAgentToken(ctx, tok.ID)); got.LastUsedAt == 0 {
		t.Fatal("應該記最後使用時間")
	}
	expired := must[AgentToken](t)(db.CreateAgentToken(ctx, AgentToken{Name: "過期", TokenHash: "h2", ExpiresAt: 1}))
	if _, err := db.FindAgentToken(ctx, "h2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("過期的不能用：%v", err)
	}
	check(t, db.RevokeAgentToken(ctx, tok.ID))
	revokedAt := must[AgentToken](t)(db.GetAgentToken(ctx, tok.ID)).RevokedAt
	if _, err := db.FindAgentToken(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("撤銷的不能用：%v", err)
	}
	check(t, db.RevokeAgentToken(ctx, tok.ID))
	if got := must[AgentToken](t)(db.GetAgentToken(ctx, tok.ID)); got.RevokedAt != revokedAt || revokedAt == 0 {
		t.Fatal("重複撤銷不應該改時間")
	}
	if ts := must[[]AgentToken](t)(db.ListAgentTokens(ctx)); len(ts) != 2 || ts[1].ID != expired.ID {
		t.Fatalf("列出權杖：%+v", ts)
	}
	if err := db.RevokeAgentToken(ctx, "沒有"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("撤銷不存在的：%v", err)
	}
}

func TestApprovalsAndInbox(t *testing.T) {
	db := openTest(t)
	b := newBot(t, db, "小秘")
	a := must[Approval](t)(db.CreateApproval(ctx, Approval{BotID: b.ID, Action: "email.send", Payload: json.RawMessage(`{"to":"x"}`), RequestedBy: "bot:" + b.ID}))
	if a.Status != "pending" {
		t.Fatalf("預設 pending：%s", a.Status)
	}
	item := must[InboxItem](t)(db.CreateInboxItem(ctx, InboxItem{Kind: "approval", BotID: b.ID, Ref: a.ID, Title: "要寄信"}))
	must[InboxItem](t)(db.CreateInboxItem(ctx, InboxItem{Kind: "budget", Title: "快到上限"}))
	if _, err := db.CreateInboxItem(ctx, InboxItem{Kind: "spam"}); err == nil {
		t.Fatal("不合法的種類應該失敗")
	}
	if ps := must[[]Approval](t)(db.ListApprovals(ctx, "pending")); len(ps) != 1 {
		t.Fatalf("待核准：%d", len(ps))
	}

	check(t, db.DecideApproval(ctx, a.ID, "approved", "owner"))
	got := must[Approval](t)(db.GetApproval(ctx, a.ID))
	if got.Status != "approved" || got.DecidedBy != "owner" || got.DecidedAt == 0 {
		t.Fatalf("核准後：%+v", got)
	}
	if err := db.DecideApproval(ctx, a.ID, "denied", "owner"); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("不能決定兩次：%v", err)
	}
	if err := db.DecideApproval(ctx, "沒有", "denied", "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的核准：%v", err)
	}
	if ps := must[[]Approval](t)(db.ListApprovals(ctx, "pending")); len(ps) != 0 {
		t.Fatalf("待核准應該清空：%d", len(ps))
	}

	if n := must[int64](t)(db.ResolveInboxByRef(ctx, a.ID, "owner")); n != 1 {
		t.Fatalf("依 ref 結掉：%d", n)
	}
	if err := db.ResolveInboxItem(ctx, item.ID, "dismissed", "owner"); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("已結掉的卡：%v", err)
	}
	open := must[[]InboxItem](t)(db.ListInbox(ctx, "open"))
	if len(open) != 1 || open[0].Kind != "budget" || open[0].BotID != "" {
		t.Fatalf("剩下的待我處理：%+v", open)
	}
	check(t, db.ResolveInboxItem(ctx, open[0].ID, "dismissed", "owner"))
	if got := must[InboxItem](t)(db.GetInboxItem(ctx, open[0].ID)); got.Status != "dismissed" || got.ResolvedAt == 0 {
		t.Fatalf("略過：%+v", got)
	}
	if all := must[[]InboxItem](t)(db.ListInbox(ctx, "")); len(all) != 2 {
		t.Fatalf("全部：%d", len(all))
	}
}

func TestUsage(t *testing.T) {
	db := openTest(t)
	for i, e := range []UsageEntry{
		{BotID: "a", Model: "m1", InputTokens: 100, OutputTokens: 10, CostMicros: 1000, CreatedAt: 1000},
		{BotID: "a", Model: "m1", InputTokens: 200, CacheReadTokens: 50, CostMicros: 2000, CreatedAt: 2000},
		{BotID: "b", Model: "m2", InputTokens: 5, CostMicros: 7, CreatedAt: 3000},
	} {
		if u := must[UsageEntry](t)(db.AddUsage(ctx, e)); u.ID != int64(i+1) {
			t.Fatalf("id：%d", u.ID)
		}
	}
	tot := must[UsageTotals](t)(db.SumUsage(ctx, UsageFilter{BotID: "a"}))
	if tot != (UsageTotals{Calls: 2, InputTokens: 300, OutputTokens: 10, CacheReadTokens: 50, CostMicros: 3000}) {
		t.Fatalf("甲的合計：%+v", tot)
	}
	if tot := must[UsageTotals](t)(db.SumUsage(ctx, UsageFilter{Since: 2000, Until: 3000})); tot.Calls != 1 || tot.CostMicros != 2000 {
		t.Fatalf("時間區間 [2000,3000)：%+v", tot)
	}
	if tot := must[UsageTotals](t)(db.SumUsage(ctx, UsageFilter{BotID: "沒有"})); tot != (UsageTotals{}) {
		t.Fatalf("沒有資料應該全 0：%+v", tot)
	}
	if es := must[[]UsageEntry](t)(db.ListUsage(ctx, UsageFilter{Since: 2000})); len(es) != 2 || es[0].Model != "m1" || es[1].BotID != "b" {
		t.Fatalf("明細：%+v", es)
	}
	if u := must[UsageEntry](t)(db.AddUsage(ctx, UsageEntry{BotID: "c"})); u.CreatedAt == 0 {
		t.Fatal("沒給時間應該用現在")
	}
}

func TestSettings(t *testing.T) {
	db := openTest(t)
	if _, err := db.GetSetting(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒有的設定：%v", err)
	}
	var v struct{ Port int }
	if ok := must[bool](t)(db.GetSettingInto(ctx, "x", &v)); ok {
		t.Fatal("沒有的設定應該回 false")
	}
	check(t, db.SetSetting(ctx, "x", map[string]int{"Port": 1}))
	check(t, db.SetSetting(ctx, "x", map[string]int{"Port": 2}))
	check(t, db.SetSetting(ctx, "theme", "dark"))
	if ok := must[bool](t)(db.GetSettingInto(ctx, "x", &v)); !ok || v.Port != 2 {
		t.Fatalf("覆寫後：%+v", v)
	}
	all := must[map[string]json.RawMessage](t)(db.ListSettings(ctx))
	if len(all) != 2 || string(all["theme"]) != `"dark"` {
		t.Fatalf("全部設定：%v", all)
	}
	check(t, db.DeleteSetting(ctx, "x"))
	check(t, db.DeleteSetting(ctx, "x"))
	if _, err := db.GetSetting(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("刪掉後：%v", err)
	}
}
