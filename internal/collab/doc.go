// Package collab 是 Bot 之間的傳話、群組與迴圈防護（X02；PRD §6.4 AA-*、RM-*、LP-*、§18；移植自 LazyBoy server/collab.py）。
//
// 傳話是非同步的：Bot 用 xbot.collab.message_bot 傳出去，收件的 Bot 在自己的對話串（每位寄件者一條）醒來處理，
// 它的最後回答自動以 result 回到寄件者原本那條對話串。兩邊對話串都有一行「已傳訊息給…」「來自…的訊息」。
//
// 群組是你＋2–10 個 Bot。你 @ 誰就叫醒誰；沒有 @ 時照群組的回應規則（D01）。Bot 回答裡 @ 別人就把下一步交給它。
// 每位成員在每個群組有一條隱藏的座位對話串，輪到時才從上次讀到的地方組 prompt（RM-4）。
//
// 防止失控（LazyBoy 的做法，數字在 internal/limits）：你的訊息排在 Bot 交辦前面；同一條往來最多 MaxHops 次就要回報你；
// 你每發言一次，群組裡 Bot 互相喚醒最多 RoomRounds 輪；result、fyi 不要求回覆；停止一個 Bot 會停掉它派出去的工作；
// 每條工作鏈有 token 預算（XBot 新增，LP-8）。隊友與群組的內容一律包在資料框架裡，不是你的授權（SEC-8）。
package collab
