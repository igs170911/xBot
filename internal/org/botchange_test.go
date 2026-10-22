package org

import (
	"strings"
	"testing"

	"xbot/internal/ops"
)

// 主管 Bot 幫隊友換模型、引擎、思考強度、人設，或開關團隊管理權限（使用者回報 2026-10-19）：
// 一定先送核准卡（寫出每一項從什麼改成什麼），使用者按允許才用 xbot.bots.update 改；不能代為核准。
// 職稱、報告對象、團隊照舊直接改。（B.2: 人設現在需要核准）
func TestManagerChangesTeammateModelOnlyAfterOwnerApproves(t *testing.T) {
	e := setupOps(t)
	ceo := ops.Bot(e.bots["CEO"], "CEO")
	info, _ := e.reg.Get("xbot.teams.update_bot")
	if !info.NoDelegate || !info.DynamicRisk || !info.ForBot || !info.ManagerOnly {
		t.Fatalf("要核准的欄位不能代為核准：%+v", info)
	}
	// 沒有團隊管理權限：送核准卡之前就擋下
	if err := e.call(ceo, "xbot.teams.update_bot", map[string]any{"bot": "Dev", "model": "openai/gpt-5"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("沒權限：%v", err)
	}
	e.manage("CEO")
	// 職稱：直接改
	var out UpdateBotOut
	if err := e.call(ceo, "xbot.teams.update_bot", map[string]any{"bot": "Dev", "title": "前端"}, &out); err != nil || e.bot("Dev").Title != "前端" {
		t.Fatalf("%+v %v", out, err)
	}
	// 報告關係繞圈：送核准卡之前就擋下
	if err := e.call(ceo, "xbot.teams.update_bot", map[string]any{"bot": "CEO", "reports_to": "Dev", "model": "x"}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("繞圈：%v", err)
	}
	if pending, _ := e.db.ListApprovals(e.ctx, "pending"); len(pending) != 0 {
		t.Fatalf("不合格的請求不能送核准卡：%d", len(pending))
	}

	in := map[string]any{"bot": "Dev", "model": "openai/gpt-5", "effort": "high", "can_manage": true, "engine": "claude-code", "reason": "前端要更強的模型"}
	if err := e.call(ceo, "xbot.teams.update_bot", in, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("要先核准：%v", err)
	}
	if dev := e.bot("Dev"); dev.Model != "" || dev.CanManage || dev.Engine == "claude-code" {
		t.Fatalf("還沒核准不能改：%+v", dev)
	}
	cards, _ := e.db.ListInbox(e.ctx, "open")
	body := ""
	for _, c := range cards {
		if c.Kind == "approval" {
			body = c.Body
		}
	}
	for _, want := range []string{"Dev（前端）", "模型：預設模型 → openai/gpt-5", "思考強度：中 → 高", "團隊管理權限：關 → 開", "引擎：Hermes → Claude Code", "另外同意", "原因：前端要更強的模型"} {
		if !strings.Contains(body, want) {
			t.Fatalf("核准卡要寫出 %q：%s", want, body)
		}
	}
	pending, _ := e.db.ListApprovals(e.ctx, "pending")
	if len(pending) != 1 || pending[0].BotID != e.bots["CEO"] {
		t.Fatalf("核准卡算在發起的 Bot 頭上：%+v", pending)
	}
	if err := e.db.DecideApproval(e.ctx, pending[0].ID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := e.reg.ExecuteApproved(e.ctx, pending[0].ID)
	if err != nil {
		t.Fatalf("允許後執行：%v", err)
	}
	dev := e.bot("Dev")
	if dev.Model != "openai/gpt-5" || dev.Effort != "high" || !dev.CanManage || dev.Engine != "claude-code" || dev.UpdatedBy != "owner" {
		t.Fatalf("允許後要改：%+v", dev)
	}
	if !strings.Contains(string(res.Output), `"can_manage"`) {
		t.Fatalf("回報改了哪些：%s", res.Output)
	}
	// 關掉團隊管理權限也要核准（誰能管理團隊由使用者決定）
	if err := e.call(ceo, "xbot.teams.update_bot", map[string]any{"bot": "Dev", "can_manage": false}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("關掉也要核准：%v", err)
	}
	// 你本人改 Bot 用 xbot.bots.update
	if err := e.call(ops.Owner("ui"), "xbot.teams.update_bot", map[string]any{"bot": "Dev", "title": "x"}, nil); codeOf(err) != ops.CodeForbidden {
		t.Fatalf("%v", err)
	}
}

// 2026-10-20：水哥哥把自己整份 SOUL.md（基本守則、隊友名冊）存成人設，介紹也跟著變成整段。
// 人設只收人設本身，「它做什麼」不跟著改。
// B.2: 改為需要核准（會改變 Bot 的身分和行為）。
func TestUpdatePersonaKeepsDescriptionAndDropsGeneratedRules(t *testing.T) {
	e := setupOps(t)
	ceo := ops.Bot(e.bots["CEO"], "CEO")
	e.manage("CEO")
	before := e.bot("Dev").Description
	persona := "你是 Dev，寫前端。\n\n你是常駐在自己電腦裡的 Bot。一律使用繁體中文。\n\n## 隊友與協作\nCEO"

	// B.2: persona 更新現在需要核准
	if err := e.call(ceo, "xbot.teams.update_bot", map[string]any{"bot": "Dev", "persona": persona}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("persona 更新需要核准：%v", err)
	}

	// 核准並執行
	pending, _ := e.db.ListApprovals(e.ctx, "pending")
	if len(pending) != 1 {
		t.Fatalf("應該有一個待核准的請求：%d", len(pending))
	}
	if err := e.db.DecideApproval(e.ctx, pending[0].ID, "approved", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.ExecuteApproved(e.ctx, pending[0].ID); err != nil {
		t.Fatalf("執行核准失敗：%v", err)
	}

	if b := e.bot("Dev"); b.Soul != "你是 Dev，寫前端。" || b.Description != before {
		t.Fatalf("人設 %q、介紹 %q（原本 %q）", b.Soul, b.Description, before)
	}
}
