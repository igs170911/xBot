package collab

// G2 情境測試（docs/PLAN.md M2）：3 個 Bot 在群組合作寫研究報告，用真的 runtime＋假的 Hermes gateway。
//   - 群組接力到 4 輪就停（迴圈會被停下）；
//   - 你在群組講話會立刻插隊（正在接力的成員先停、讀到你的話再回）；
//   - 往來次數、字數限制照 LazyBoy 的數字生效；轉派的結果回來才回覆；太長的結果存成檔案。

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

type g2Model struct{}

func (g2Model) ResolveModel(context.Context, store.Bot) (runtime.ModelRef, error) {
	return runtime.ModelRef{Provider: "openai", Model: "gpt-x"}, nil
}

type g2 struct {
	t     *testing.T
	ctx   context.Context
	db    *store.DB
	rt    *runtime.Runtime
	s     *Service
	files DirFiles
	bots  map[string]string
	names map[string]string

	mu     sync.Mutex
	script func(bot, prompt string, tr *hermestest.Turn)
	log    []string // bot：prompt 開頭
}

var meRE = regexp.MustCompile(`以及你（([^）]+)）`)

func newG2(t *testing.T) *g2 {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	gw := hermestest.New("k")
	t.Cleanup(gw.Close)
	drv := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: gw.Addr(), Key: "k"}, nil
	}, nil)
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: events.New(), Models: g2Model{}})
	e := &g2{t: t, ctx: ctx, db: db, rt: rt, files: DirFiles{Root: filepath.Join(dir, "bots")}, bots: map[string]string{}, names: map[string]string{}}
	e.s = New(Options{DB: db, Runtime: rt, Files: e.files})
	for _, n := range [][2]string{{"Chief", "總管"}, {"Writer", "文案"}, {"Scout", "研究"}} {
		b, err := db.CreateBot(ctx, store.Bot{Name: n[0], Title: n[1]})
		if err != nil {
			t.Fatal(err)
		}
		e.bots[n[0]], e.names[b.ID] = b.ID, n[0]
	}
	gw.OnPrompt = func(tr *hermestest.Turn) {
		bot := e.who(tr.Text)
		e.mu.Lock()
		e.log = append(e.log, bot+"："+firstLine(tr.Text))
		script := e.script
		e.mu.Unlock()
		script(bot, tr.Text, tr)
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// who 找出這個 prompt 是哪個 Bot 的：群組 prompt 寫著「以及你（名字）」；其他看哪個 Bot 正在做的工作是這段文字。
func (e *g2) who(prompt string) string {
	if m := meRE.FindStringSubmatch(prompt); m != nil {
		return m[1]
	}
	for name, id := range e.bots {
		if _, taskID := e.rt.Active(id); taskID != "" {
			if t, err := e.db.GetTask(e.ctx, taskID); err == nil && t.Input == prompt {
				return name
			}
		}
	}
	return "?"
}

func (e *g2) setScript(f func(bot, prompt string, tr *hermestest.Turn)) {
	e.mu.Lock()
	e.script = f
	e.mu.Unlock()
}

func (e *g2) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.mu.Lock()
			e.t.Fatalf("等不到：%s；收到的 prompt：%v", what, e.log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *g2) idle() bool {
	for _, id := range e.bots {
		tasks, _ := e.db.ListTasks(e.ctx, id, true)
		if len(tasks) > 0 {
			return false
		}
		if busy, _ := e.rt.Busy(id); busy {
			return false
		}
	}
	return true
}

func (e *g2) room(roomID string) []RoomMessage {
	msgs, _ := e.db.ListMessages(e.ctx, store.ScopeRoom, roomID, 0, 0)
	var out []RoomMessage
	for _, m := range msgs {
		out = append(out, roomMessageView(m))
	}
	return out
}

func TestG2ThreeBotsInARoom(t *testing.T) {
	e := newG2(t)
	chief, writer, scout := e.bots["Chief"], e.bots["Writer"], e.bots["Scout"]
	room, err := e.s.CreateRoom(e.ctx, []string{chief, writer, scout}, RoomOptions{Name: "研究報告"})
	if err != nil {
		t.Fatal(err)
	}

	// ── 1. 迴圈會被停下：三個 Bot 一直互相 @，到第 4 輪訊息照發、不再叫醒別人。
	next := map[string]string{"Chief": "Scout", "Scout": "Writer", "Writer": "Chief"}
	e.setScript(func(bot, prompt string, tr *hermestest.Turn) {
		tr.Complete("@"+next[bot]+" 這段我做好了，換你接著做", "complete", nil)
	})
	res, err := e.s.Post(e.ctx, room.ID, "@Chief 請大家合作完成一份研究報告", "")
	if err != nil || strings.Join(res.Woke, ",") != "Chief" {
		t.Fatalf("%+v %v", res, err)
	}
	e.waitFor("接力停下", func() bool {
		log := e.room(room.ID)
		return e.idle() && len(log) > 0 && log[len(log)-1].Notice == "capped"
	})
	var bots []RoomMessage
	for _, m := range e.room(room.ID) {
		if strings.HasPrefix(m.Sender, "bot:") {
			bots = append(bots, m)
		}
	}
	if len(bots) != limits.RoomRounds()+1 {
		t.Fatalf("你發言一次，Bot 互相叫醒最多 %d 輪：%d 則", limits.RoomRounds(), len(bots))
	}
	for i, m := range bots[:limits.RoomRounds()] {
		if m.Handoff != i+1 || m.Round != i {
			t.Fatalf("第 %d 則：%+v", i, m)
		}
	}
	if last := bots[len(bots)-1]; !last.Capped || last.Handoff != 0 {
		t.Fatalf("最後一則：%+v", last)
	}

	// ── 2. 你在群組講話會立刻插隊：Scout 正在接力（低優先），你 @ 它，它先停、讀到你的話再回。
	var scoutWaiting sync.WaitGroup
	scoutWaiting.Add(1)
	var once sync.Once
	e.setScript(func(bot, prompt string, tr *hermestest.Turn) {
		switch {
		case bot == "Chief":
			tr.Complete("@Scout 去查 A 公司的營收", "complete", nil)
		case bot == "Scout" && !strings.Contains(prompt, "改查 B"):
			once.Do(scoutWaiting.Done)
			<-tr.Interrupted()
			tr.Complete("", "interrupted", nil)
		case bot == "Scout":
			tr.Complete("B 公司的營收：三年成長 40%，來源已附。", "complete", nil)
		default:
			tr.Complete("[silent]", "complete", nil)
		}
	})
	if _, err := e.s.Post(e.ctx, room.ID, "@Chief 開始第二階段", ""); err != nil {
		t.Fatal(err)
	}
	scoutWaiting.Wait()
	_, relay := e.rt.Active(scout)
	res, err = e.s.Post(e.ctx, room.ID, "@Scout 先停，改查 B 公司", "")
	if err != nil || strings.Join(res.Stopped, ",") != "Scout" {
		t.Fatalf("要打斷 Scout 的接力：%+v %v", res, err)
	}
	e.waitFor("Scout 回覆你", func() bool {
		log := e.room(room.ID)
		return e.idle() && strings.HasPrefix(log[len(log)-1].Content, "B 公司的營收")
	})
	if task, _ := e.db.GetTask(e.ctx, relay); task.Verdict != runtime.VerdictCancelled {
		t.Fatalf("被打斷的接力：%+v", task)
	}
	open, _ := e.db.ListTasksByMeta(e.ctx, "$.room_id", room.ID, false)
	last := open[len(open)-1]
	if last.BotID != scout || !metaOf(last).ByOwner || last.LowPriority || last.Verdict != runtime.VerdictAnswer {
		t.Fatalf("你叫醒的那一輪：%+v %+v", last, metaOf(last))
	}

	// ── 3. Bot 之間傳話：一路轉派到第 6 次就擋下；最後的長回答存成檔案；結果一路整合回到你交辦的對話。
	var mu sync.Mutex
	var hopErr, sizeErr error
	order := []string{"Chief", "Writer", "Scout"}
	nextOf := func(bot string) string {
		for i, n := range order {
			if n == bot {
				return order[(i+1)%3]
			}
		}
		return ""
	}
	e.setScript(func(bot, prompt string, tr *hermestest.Turn) {
		id := e.bots[bot]
		switch {
		case strings.Contains(prompt, "的回覆]"):
			tr.Complete("整合（"+bot+"）：報告完成", "complete", nil)
		case strings.Contains(prompt, "接力"):
			if _, err := e.s.Send(e.ctx, id, nextOf(bot), "請繼續接力 下一棒，附上你的部分", "request", SendOptions{}); err != nil {
				mu.Lock()
				hopErr = err
				_, sizeErr = e.s.Send(e.ctx, id, nextOf(bot), strings.Repeat("長", limits.BotMessageMax+1), "request", SendOptions{})
				mu.Unlock()
				tr.Complete("已到往來上限，整理目前結果：\n"+strings.Repeat("資料", 7000), "complete", nil)
				return
			}
			tr.Complete("已派工給 "+nextOf(bot), "complete", nil)
		default:
			tr.Complete("[silent]", "complete", nil)
		}
	})
	start, err := e.rt.Chat(e.ctx, chief, "", "開始接力：請團隊完成研究報告")
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("結果回到你交辦的對話", func() bool {
		msgs, _ := e.db.ListMessages(e.ctx, store.ScopeThread, start.ThreadID, 0, 0)
		for _, m := range msgs {
			if m.Kind == "peer" && strings.HasPrefix(m.Content, "整合（Writer）") {
				return e.idle()
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	if hopErr == nil || !strings.Contains(hopErr.Error(), "已經 6 次") {
		t.Fatalf("第 7 次要擋：%v", hopErr)
	}
	if sizeErr == nil || !strings.Contains(sizeErr.Error(), "12000") {
		t.Fatalf("超過 12000 字要拒絕：%v", sizeErr)
	}
	msgs, _ := e.db.ListBotMessages(e.ctx, store.BotMessageFilter{})
	maxReq := 0
	results := 0
	for _, m := range msgs {
		if m.Intent == IntentRequest && m.Hop > maxReq {
			maxReq = m.Hop
		}
		if m.Intent == IntentResult {
			results++
		}
	}
	if maxReq != limits.MaxHops() || results != limits.MaxHops() {
		t.Fatalf("請求最多到第 %d 次（%d）、每一棒都有結果回傳（%d）", limits.MaxHops(), maxReq, results)
	}
	// 第 6 棒的長回答存在 Scout 的電腦，訊息只放開頭。
	var long *store.BotMessage
	for i, m := range msgs {
		if m.Intent == IntentResult && m.From == chief && m.To == scout && strings.Contains(m.Text, "已到往來上限") {
			long = &msgs[i]
		}
	}
	if long == nil || !strings.Contains(long.Text, "太長放不進訊息") {
		t.Fatalf("長回答：%+v", long)
	}
	p := long.Text[strings.LastIndex(long.Text, "/workspace/")+len("/workspace/"):]
	p = strings.TrimSuffix(p, "）")
	if body, err := e.files.Read(e.ctx, scout, p); err != nil || !strings.HasPrefix(string(body), "已到往來上限") {
		t.Fatalf("存檔：%s %v", p, err)
	}
	// 交辦紀錄：每一棒都是 done（轉派的在結果回來後結案）。
	asgs, _ := e.db.ListAssignments(e.ctx, start.TaskID)
	if len(asgs) != limits.MaxHops() {
		t.Fatalf("交辦 %d 筆", len(asgs))
	}
	for _, a := range asgs {
		if a.Status != store.AssignDone {
			t.Fatalf("交辦：%+v", a)
		}
	}
}
