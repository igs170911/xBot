package limits

import "sync/atomic"

// 使用者可以調的限制（設定 → 進階 → 限制，xbot.limits.get／set）。
// 原本照 LazyBoy 寫死（MaxHops 6、RoomRounds 4、建 Bot 每天 3／20、Bot 上限 50、自動續做 2、提案 3）；
// 使用者決定（2026-10-19）：預設放寬、而且可以自己改——「限制太多不好用」。
// 這裡只放「會擋住工作」的數字；技術上的逾時、大小上限還是常數。

// Tunables 是可以調的限制。
type Tunables struct {
	// MaxHops 是同一條 Bot 往來最多幾次；超過就停下來回報你。結果回傳不算。
	MaxHops int `json:"max_hops"`
	// RoomRounds 是你在群組說一次話，Bot 之間最多互相接力幾輪。
	RoomRounds int `json:"room_rounds"`
	// BotCreatesPerDay 是一般 Bot 每 24 小時最多建幾個 Bot。
	BotCreatesPerDay int `json:"bot_creates_per_day"`
	// ManagerBotCreatesPerDay 是有團隊管理權限的 Bot 每 24 小時最多建幾個 Bot（建團隊也算）。
	ManagerBotCreatesPerDay int `json:"manager_bot_creates_per_day"`
	// BotsMax 是全部 Bot 的上限（Bot 建 Bot、建團隊、匯入時檢查）。
	BotsMax int `json:"bots_max"`
	// AutoContinueMax 是 Bot 只說要做、沒做完時，最多自動請它繼續幾次。
	AutoContinueMax int `json:"auto_continue_max"`
	// AgendaOpenMax 是一個 Bot 同時在等你的提案＋討論最多幾件。
	AgendaOpenMax int `json:"agenda_open_max"`
}

// DefaultTunables 是預設值（降回 LazyBoy MaxHops=6 以防 Bot 對話循環，2026-10-22 bot loop fix）。
var DefaultTunables = Tunables{
	MaxHops:                 6,
	RoomRounds:              10,
	BotCreatesPerDay:        10,
	ManagerBotCreatesPerDay: 50,
	BotsMax:                 200,
	AutoContinueMax:         5,
	AgendaOpenMax:           10,
}

// LazyBoyTunables 是 LazyBoy 原本寫死的值（collab.py、agenda.py、runtime.py）。測試用它驗證機制（例：續做兩次就停）。
var LazyBoyTunables = Tunables{
	MaxHops:                 6,
	RoomRounds:              4,
	BotCreatesPerDay:        3,
	ManagerBotCreatesPerDay: 20,
	BotsMax:                 50,
	AutoContinueMax:         2,
	AgendaOpenMax:           3,
}

// TunableRange 是每個數字能設的範圍（最小、最大）。
var TunableRange = map[string][2]int{
	"max_hops":                    {1, 100},
	"room_rounds":                 {1, 50},
	"bot_creates_per_day":         {0, 500},
	"manager_bot_creates_per_day": {0, 500},
	"bots_max":                    {1, 1000},
	"auto_continue_max":           {0, 20},
	"agenda_open_max":             {1, 50},
}

var current atomic.Pointer[Tunables]

func init() {
	t := DefaultTunables
	current.Store(&t)
}

// CurrentTunables 回傳目前生效的限制。
func CurrentTunables() Tunables { return *current.Load() }

// SetTunables 換成新的限制（存檔由 xbot.limits.set 負責；這裡只換記憶體裡的值）。
func SetTunables(t Tunables) { current.Store(&t) }

// MaxHops 是同一條 Bot 往來最多幾次。
func MaxHops() int { return current.Load().MaxHops }

// RoomRounds 是群組裡 Bot 之間最多接力幾輪。
func RoomRounds() int { return current.Load().RoomRounds }

// BotCreatesPerDay 是一般 Bot 每 24 小時最多建幾個 Bot。
func BotCreatesPerDay() int { return current.Load().BotCreatesPerDay }

// ManagerBotCreatesPerDay 是有團隊管理權限的 Bot 每 24 小時最多建幾個 Bot。
func ManagerBotCreatesPerDay() int { return current.Load().ManagerBotCreatesPerDay }

// BotsMax 是全部 Bot 的上限。
func BotsMax() int { return current.Load().BotsMax }

// AutoContinueMax 是最多自動請 Bot 繼續幾次。
func AutoContinueMax() int { return current.Load().AutoContinueMax }

// AgendaOpenMax 是一個 Bot 同時在等你的提案＋討論最多幾件。
func AgendaOpenMax() int { return current.Load().AgendaOpenMax }
