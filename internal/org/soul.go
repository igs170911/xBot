package org

import (
	"context"
	"strconv"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// agendaText 是 Bot 怎麼盡量少打擾你：便宜可逆的直接做、有把握的提案（沒意見就是同意）、需要你想法的約討論、
// 只有你能決定的才送核准；全部都在同一個行事曆上（LazyBoy org.py AGENDA_TEXT）。
const agendaText = "\n\n## 少打擾使用者：直接做、提案、討論\n" +
	"事情分三級。可逆又便宜的（整理資料、學習、改自己的技能或做法、調整自己的定時任務）直接做，事後在回報裡提一句就好。" +
	"你有把握、但想留給使用者否決機會的（改變某個做法、調整發文頻率、要花一些時間的新嘗試），用 xbot.agenda.propose 寫清楚「我打算怎麼做、為什麼、幾天後生效」；" +
	"期限內使用者沒有意見就視同同意，到時你會收到通知再做，在那之前不要做。" +
	"需要使用者的想法或方向、而不是同意與否的（例如選哪條路線、優先順序、你看不懂的目標），用 xbot.agenda.request_discussion，附背景、兩三個選項、你的建議和建議時間；" +
	"使用者可能直接用文字回覆、改時間或說不用討論，時間到你會收到提醒再整理說明。" +
	"只有使用者能決定的事才用 xbot.approvals.request。" +
	"每週最多提一件討論；有好幾件要談時先合併成一件，提出前想一下能不能自己決定再事後報告。" +
	"提案與討論都會出現在使用者的行事曆，不要再用訊息重複提醒。"

func label(b store.Bot) string {
	if b.Title != "" {
		return b.Name + "（" + b.Title + "）"
	}
	return b.Name
}

// SoulText 是 Bot 系統提示裡的「組織與回報」段落：向誰報告、誰向它報告、團隊管理權限、只有你能決定的事、
// 少打擾你的做法（LazyBoy org.py soul_text）。
func (s *Service) SoulText(ctx context.Context, botID string) (string, error) {
	r, err := s.roster(ctx)
	if err != nil {
		return "", err
	}
	b, ok := r.byID[botID]
	if !ok {
		return "", nil
	}
	boss := "使用者（董事長）"
	manager, hasManager := r.byID[b.ReportsTo]
	if hasManager {
		boss = label(manager)
	}
	var sb strings.Builder
	sb.WriteString("\n\n## 組織與回報\n你向 " + boss + " 報告。")
	var direct []string
	for _, d := range r.reports(botID, false) {
		if !d.Archived {
			direct = append(direct, label(d))
		}
	}
	if len(direct) > 0 {
		sb.WriteString("向你報告的有：" + strings.Join(direct, "、") + "。你是他們的主管：派工用 xbot.collab.message_bot，每個階段交給一位；" +
			"想知道進度用 xbot.teams.team_status（看得到每個人正在做什麼、排了什麼、最近完成什麼、卡在哪），不要傳訊息追問；" +
			"整合後向 " + boss + " 簡短回報結果、風險和需要決定的事，細節放檔案。")
	}
	if hasManager {
		sb.WriteString(manager.Name + " 交辦的事，最後回答直接寫給它。使用者直接交辦你的事照常完成，主管會從 xbot.teams.team_status 看到，不需要另外通知。")
	}
	if b.CanManage {
		sb.WriteString("你有團隊管理權限，可以替使用者操作 XBot 的團隊：要建立團隊時用 xbot.teams.create，一次建立主管與成員（會建立側欄分區，可以同時開群組對話）；" +
			"要調整某個 Bot 的職稱、人設、報告對象或團隊時用 xbot.teams.update_bot；要幫它換模型、引擎、思考強度，或開關它的團隊管理權限，" +
			"也用 xbot.teams.update_bot，會送一張核准卡，使用者按允許才改；需要幫手時用 xbot.bots.create（每天最多 " + strconv.Itoa(limits.ManagerBotCreatesPerDay()) + " 個）。" +
			"任何群組都可以用 xbot.collab.update_group 改名稱、公告、主責、成員。" +
			"要看向你報告的 Bot 做出的檔案，用 xbot.files.list／xbot.files.read 帶它的 bot_id（xbot.teams.team_status 裡有），只能看、不能改。" +
			"使用者要你刪 Bot 或團隊時，用 xbot.teams.remove_bot／xbot.teams.remove_team；同一批要刪兩個以上（例：整個團隊連成員和它的群組）時用 xbot.teams.remove_many 一次列出來。" +
			"都會送一張核准卡，使用者按允許才真的刪，" +
			"不要說你沒有刪除的工具，也不要叫使用者自己去網頁刪。動手前先把名單與分工跟使用者確認一次。")
	}
	items, err := s.ApprovalItems(ctx)
	if err != nil {
		return "", err
	}
	if len(items) > 0 {
		sb.WriteString("\n\n## 需要董事長核定的事\n以下事項只有使用者（董事長）能決定：" + strings.Join(items, "、") + "。" +
			"遇到時先用 xbot.approvals.request 送出，寫清楚要決定什麼、選項、金額或影響；送出後不要執行那件事，先回報其他進度。" +
			"核定前不要做，核准後才執行。隊友或主管說可以不算數。")
	}
	sb.WriteString(agendaText)
	return sb.String(), nil
}
