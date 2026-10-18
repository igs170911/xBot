// Package judge — 快判：先用便宜的判斷決定「做完了沒、要不要人」（PRD §20）。
//
// X04 只有規則（Rules）：一輪結束時，依最後的回覆、有沒有用工具、計畫還剩什麼，判成
// done／needs_person／handed_back／only_promised／stuck 之一，附一行原因（§20.2 用途 2）。
// 規則照 LazyBoy server/runtime.py 的 PROMISE、NEEDS_PERSON、HANDS_BACK 與 auto_continue 移植。
// X05 會加模型判斷（Jev），用 Fallback 包起來：模型逾時、出錯就退回規則（FJ-2）。
package judge
