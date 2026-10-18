package limits

import "time"

// Web Push（W31，PRD §19.2「XBot 加 Web Push」、MB-1）。全部是 XBot 新增。

const (
	// PushMaxSubscriptions 是最多記住幾個裝置的推播訂閱（一個瀏覽器／手機一個），超過時要先關掉舊的。
	PushMaxSubscriptions = 20
	// PushTTL 是推播服務（FCM、Apple、Mozilla）替離線裝置保留通知多久；超過就丟掉（卡片仍在「待我處理」）。
	PushTTL = 12 * time.Hour
	// PushSendTimeout 是送一則推播給推播服務的時限。
	PushSendTimeout = 15 * time.Second
	// PushWorkers 是同時送推播的數量上限（訂閱多時不要一次開太多連線）。
	PushWorkers = 4
	// PushQueue 是等著送的推播事件緩衝；滿了就丟（卡片本身還在，畫面上看得到）。
	PushQueue = 256
)
