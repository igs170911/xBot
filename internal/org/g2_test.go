package org

// G2 情境（docs/PLAN.md M2，團隊版）：你用範本外的方式建一個團隊，交辦主管一件事；主管用 xbot.teams.team_status 看團隊、
// 用 xbot.collab.message_bot 分派給兩位成員，等兩份結果回來後彙整回報你。真的 runtime＋假的 Hermes gateway，
// Bot 的操作都經過操作登錄表（和 product.sock 上的 XBot MCP 同一條路）。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/bots"
	"xbot/internal/collab"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

type g2Model struct{}

func (g2Model) ResolveModel(context.Context, store.Bot) (runtime.ModelRef, error) {
	return runtime.ModelRef{Provider: "openai", Model: "gpt-x"}, nil
}

func TestG2ChiefDispatchesToItsTeamAndSummarizes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
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
	co := collab.New(collab.Options{DB: db, Runtime: rt})
	s := New(Options{DB: db, Runtime: rt, Collab: co})
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	bots.Register(reg, bots.Deps{Active: rt.Active})
	collab.Register(reg, co)
	Register(reg, s)
	owner := ops.Owner("ui")

	invoke := func(id ops.Identity, op string, in any, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, id, op, raw, ops.Options{})
		if err != nil {
			t.Errorf("%s：%v", op, err)
			return
		}
		if out != nil {
			_ = json.Unmarshal(res.Output, out)
		}
	}

	// 1. 你建一個團隊：主管＋兩位成員，都向主管報告。
	var made CreateTeamOut
	invoke(owner, "xbot.teams.create", CreateTeamIn{Name: "研究組", Lead: &Member{Name: "主管", Title: "研究主管"},
		Members: []Member{{Name: "Scout", Title: "研究"}, {Name: "Writer", Title: "文案"}}}, &made)
	if made.LeadID == "" || len(made.MemberIDs) != 2 || made.RoomID == "" {
		t.Fatalf("%+v", made)
	}
	names := map[string]string{made.LeadID: "主管", made.MemberIDs[0]: "Scout", made.MemberIDs[1]: "Writer"}
	chief := ops.Bot(made.LeadID, "主管")

	var mu sync.Mutex
	var log []string
	var seen TeamStatusOut
	results := 0
	gw.OnPrompt = func(tr *hermestest.Turn) {
		who := "?"
		for id, n := range names {
			if _, taskID := rt.Active(id); taskID != "" {
				if task, err := db.GetTask(ctx, taskID); err == nil && task.Input == tr.Text {
					who = n
				}
			}
		}
		mu.Lock()
		log = append(log, who+"："+strings.SplitN(tr.Text, "\n", 2)[0])
		mu.Unlock()
		switch {
		case who == "主管" && strings.Contains(tr.Text, "市場報告"):
			// 主管先看團隊，再分派：每個階段交給一位。
			invoke(chief, "xbot.teams.team_status", TeamStatusIn{}, &seen)
			invoke(chief, "xbot.collab.message_bot", collab.MessageBotIn{Bot: "Scout", Text: "查 A 公司近三年營收"}, nil)
			invoke(chief, "xbot.collab.message_bot", collab.MessageBotIn{Bot: "Writer", Text: "寫報告開頭段落"}, nil)
			tr.Complete("已分派給 Scout 與 Writer，結果回來後彙整。", "complete", nil)
		case who == "Scout":
			tr.Complete("A 公司營收三年成長 40%。", "complete", nil)
		case who == "Writer":
			tr.Complete("開頭段落已寫好：市場正在擴大。", "complete", nil)
		case strings.Contains(tr.Text, "隊友的回覆一起到了"):
			// 兩份結果在主管還在忙時一起到：XBot 併成一則交給主管（機器忙的時候常這樣），一次彙整。
			mu.Lock()
			results += 2
			mu.Unlock()
			tr.Complete("彙整：A 公司營收三年成長 40%；報告開頭已完成。", "complete", nil)
		case who == "主管":
			mu.Lock()
			results++
			n := results
			mu.Unlock()
			if n < 2 {
				tr.Complete("[silent]", "complete", nil)
				return
			}
			tr.Complete("彙整：A 公司營收三年成長 40%；報告開頭已完成。", "complete", nil)
		default:
			tr.Complete("[silent]", "complete", nil)
		}
	}

	// 2. 你交辦主管。
	if _, err := rt.Start(ctx, runtime.ChatRequest{BotID: made.LeadID, Text: "幫我做一份市場報告"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		mu.Lock()
		n := results
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("等不到兩份結果；收到的 prompt：%v", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 主管看到的團隊：兩位成員，都向它報告。
	if len(seen.Team) != 2 || seen.Team[0].ReportsTo != "主管" || seen.Team[0].Team != "研究組" {
		t.Fatalf("team_status：%+v", seen)
	}
	// 3. 等主管彙整完：它的對話裡最後的回覆是彙整。
	var summary string
	for time.Now().Before(deadline) && !strings.HasPrefix(summary, "彙整") {
		threads, _ := db.ListThreads(ctx, made.LeadID, false)
		for _, th := range threads {
			msgs, _ := db.ListMessages(ctx, store.ScopeThread, th.ID, 0, 0)
			for _, m := range msgs {
				if m.Sender == "bot:"+made.LeadID && strings.HasPrefix(m.Content, "彙整") {
					summary = m.Content
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(summary, "40%") || !strings.Contains(summary, "開頭") {
		t.Fatalf("主管沒有彙整：%q；prompt：%v", summary, log)
	}
	// 交辦都有紀錄（C13 的往來）：主管 → Scout、主管 → Writer。
	msgs, err := db.ListBotMessages(ctx, store.BotMessageFilter{Pair: []string{made.LeadID, made.MemberIDs[0]}})
	if err != nil || len(msgs) < 2 {
		t.Fatalf("主管和 Scout 的往來：%v %v", msgs, err)
	}
}
