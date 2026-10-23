package runtime

import (
	"context"
	"errors"
	"strings"

	"xbot/internal/judge"
	"xbot/internal/store"
)

// 重播快判（harness2 的做法用在 XBot：同一批真實的工作、換一版規則、比判斷差在哪）。
// 改了完成判定的規則以後，用你的「不對？」改正和最近的工作重新判一次，看會不會更對、會不會冒出新的誤判（2026-10-20：
// 打招呼被當成沒做完、流程圖被當成在問你，都是上線後才發現）。只用規則，不呼叫模型。

// ReplayInput 從一件已經結束的工作重建當時交給完成判定的內容：最後一則回覆、有沒有用工具、計畫還剩什麼、
// 原本怎麼交代的、成功的搜尋與讀網頁次數（2026-10-23 的「沒查就說研究」「交代了卻問要不要做」規則要用）。
// 背景子任務的狀態（Stranded）重建不了，當成沒有。工作沒有回覆時 ok 是 false。
func ReplayInput(ctx context.Context, db *store.DB, t store.Task) (judge.Input, bool, error) {
	in := judge.Input{Outcome: t.Verdict, TaskID: t.ID, BotID: t.BotID}
	if t.ThreadID == "" {
		return in, false, nil
	}
	m, err := db.TaskReply(ctx, t.ThreadID, t.ID)
	if errors.Is(err, store.ErrNotFound) {
		return in, false, nil
	} else if err != nil {
		return in, false, err
	}
	in.Text = m.Content
	if in.UsedTools, err = db.TaskUsedTools(ctx, t.ID); err != nil {
		return in, false, err
	}
	ok, err := db.TaskToolsOK(ctx, t.ID)
	if err != nil {
		return in, false, err
	}
	for _, name := range ok {
		if judge.IsResearchTool(name) {
			in.Research++
		}
	}
	in.Asked, in.Continued = askedFor(db, t)
	plan, err := db.TaskLastStatus(ctx, t.ID, "計畫：")
	if err != nil {
		return in, false, err
	}
	if left, ok := planLeft(plan); ok {
		in.PlanLeft = left
	}
	if strings.TrimSpace(in.Text) == "" {
		return in, false, nil
	}
	return in, true, nil
}
