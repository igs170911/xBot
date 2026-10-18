// Package org 是 Bot 的組織（X03，PRD §6.5 TM-*；移植自 LazyBoy server/org.py、agenda.py）：
//
//   - 匯報線：每個 Bot 向一位主管 Bot 報告，沒有主管就直接向你（董事長）報告；不能繞成一圈。
//     主管用 xbot.collab.message_bot 派工，用 xbot.teams.team_status 看團隊進度，不用傳訊息追問。
//   - 團隊：側欄的一個分區，一位主管帶成員（含主管最多 10 位）；可以從範本一次建好（主管＋成員＋群組）。
//     你信任的 Bot（團隊管理權限 can_manage）也能替你建團隊、調整別的 Bot 的職稱與報告對象，但不能刪除。
//   - 提案與討論：Bot 用 propose 寫「我打算怎麼做、幾天後生效」，沒意見就視同同意；用 request_discussion
//     約時間討論。和核准分開：只有你能決定的事（花錢、簽約、上架…）一律走 xbot.approvals.request。
package org
