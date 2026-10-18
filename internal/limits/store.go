package limits

import "time"

// 資料庫（C01）。

// DBBusyTimeout 是 SQLite 等待鎖的時間。出自 LazyBoy server/store.py（PRAGMA busy_timeout=5000）。
const DBBusyTimeout = 5 * time.Second

// DBReadConns 是唯讀連線池的大小；寫入一律走單一連線。XBot 新增（LazyBoy 是一條寫入＋一條唯讀連線）。
const DBReadConns = 4

// BoxIdleMinutes 是 Bot 電腦閒置多久後休眠的預設值（分鐘）。出自 PRD BX-12（與 LazyBoy 規劃 X-REL-7 一致）。
const BoxIdleMinutes = 30

// EventsKeepPerBot 是每個 Bot 保留的事件筆數，較舊的會被清掉。出自 LazyBoy server/store.py prune_events(keep=5000)。
const EventsKeepPerBot = 5000
