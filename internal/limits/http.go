package limits

import "time"

// HTTP 出口、登入與 SSE（C03）。

// SessionTTL 是登入後 cookie 的有效期。出自 LazyBoy server/main.py login（max_age=30*86400）。
const SessionTTL = 30 * 24 * time.Hour

// LoginFailures 是同一個來源在 LoginWindow 內最多可以失敗幾次，超過就暫時擋下（SEC-7）。
// XBot 新增：LazyBoy 沒有登入失敗限速（PRD 標為 LB 缺口）。
const LoginFailures = 10

// LoginWindow 是計算登入失敗次數的時間窗，也是被擋下後要等多久。XBot 新增。
const LoginWindow = 15 * time.Minute

// APIBodyMax 是 /api/v1 操作請求內容的大小上限。XBot 新增。
const APIBodyMax = 1 << 20

// SSEHeartbeat 是 SSE 沒有事件時送心跳的間隔，讓代理伺服器不會切斷連線、也能發現斷線。
// XBot 新增（LazyBoy server/main.py 的事件流是 10 秒醒來一次）。
const SSEHeartbeat = 15 * time.Second

// SSERetry 是告訴瀏覽器斷線後多久重連（SSE 的 retry 欄位）。XBot 新增。
const SSERetry = 3 * time.Second

// SSEBatch 是 SSE 續傳時一次從資料庫讀出的事件筆數。XBot 新增。
const SSEBatch = 500

// LoginTrackMax 是登入限速最多記住幾個來源，超過時先清掉過期的。XBot 新增。
const LoginTrackMax = 10000
