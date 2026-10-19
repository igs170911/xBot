// Package limitsops 是「可以調的限制」的操作（設定 → 進階 → 限制）：xbot.limits.get／set。
// 值存在 setting 表（limits.tunables），服務啟動時 Load 讀回來；改了馬上生效（limits.SetTunables）。
package limitsops

import (
	"context"
	"fmt"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// SettingKey 是存在 setting 表的 key。
const SettingKey = "limits.tunables"

// View 是 xbot.limits.get／set 的輸出。
type View struct {
	Values   limits.Tunables   `json:"values" desc:"目前生效的值"`
	Defaults limits.Tunables   `json:"defaults" desc:"預設值"`
	Ranges   map[string][2]int `json:"ranges" desc:"每個值能設的範圍（最小、最大）"`
}

// GetIn 是 xbot.limits.get 的輸入。
type GetIn struct{}

// SetIn 是 xbot.limits.set 的輸入：只改有給的；reset=true 全部回到預設。
type SetIn struct {
	MaxHops                 *int `json:"max_hops,omitempty" desc:"同一條 Bot 往來最多幾次"`
	RoomRounds              *int `json:"room_rounds,omitempty" desc:"群組裡 Bot 之間最多接力幾輪"`
	BotCreatesPerDay        *int `json:"bot_creates_per_day,omitempty" desc:"一般 Bot 每天最多建幾個 Bot"`
	ManagerBotCreatesPerDay *int `json:"manager_bot_creates_per_day,omitempty" desc:"有團隊管理權限的 Bot 每天最多建幾個 Bot"`
	BotsMax                 *int `json:"bots_max,omitempty" desc:"全部 Bot 的上限"`
	AutoContinueMax         *int `json:"auto_continue_max,omitempty" desc:"Bot 只說要做時最多自動請它繼續幾次"`
	AgendaOpenMax           *int `json:"agenda_open_max,omitempty" desc:"一個 Bot 同時在等你的提案＋討論最多幾件"`
	Reset                   bool `json:"reset,omitempty" desc:"全部回到預設"`
}

// Load 讀回存過的限制（沒存過就是預設）；服務啟動時呼叫。
func Load(ctx context.Context, db *store.DB) error {
	t := limits.DefaultTunables
	if _, err := db.GetSettingInto(ctx, SettingKey, &t); err != nil {
		return err
	}
	fix(&t) // 存的值不在範圍內（例：之後收緊了範圍）就用預設補上，不擋啟動
	limits.SetTunables(t)
	return nil
}

// fix 把不在範圍內的值換回預設；有換回傳 true。
func fix(t *limits.Tunables) bool {
	changed := false
	d := limits.DefaultTunables
	for _, f := range fields(t, &d) {
		r := limits.TunableRange[f.key]
		if *f.v < r[0] || *f.v > r[1] {
			*f.v = *f.def
			changed = true
		}
	}
	return changed
}

type field struct {
	key    string
	v, def *int
}

func fields(t, d *limits.Tunables) []field {
	return []field{
		{"max_hops", &t.MaxHops, &d.MaxHops},
		{"room_rounds", &t.RoomRounds, &d.RoomRounds},
		{"bot_creates_per_day", &t.BotCreatesPerDay, &d.BotCreatesPerDay},
		{"manager_bot_creates_per_day", &t.ManagerBotCreatesPerDay, &d.ManagerBotCreatesPerDay},
		{"bots_max", &t.BotsMax, &d.BotsMax},
		{"auto_continue_max", &t.AutoContinueMax, &d.AutoContinueMax},
		{"agenda_open_max", &t.AgendaOpenMax, &d.AgendaOpenMax},
	}
}

func view() View {
	return View{Values: limits.CurrentTunables(), Defaults: limits.DefaultTunables, Ranges: limits.TunableRange}
}

// Register 登錄 xbot.limits.*。
func Register(reg *ops.Registry) {
	ops.Register(reg, ops.Op[GetIn, View]{
		Name: "xbot.limits.get", Summary: "看可以調的限制（Bot 往來次數、群組接力、建 Bot 上限、自動續做、提案件數）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(context.Context, *ops.Call, GetIn) (View, error) { return view(), nil },
	})
	ops.Register(reg, ops.Op[SetIn, View]{
		Name: "xbot.limits.set", Summary: "改可以調的限制；只改有給的，reset 全部回到預設",
		Description: "只有你本人能改（Bot 和外部代理不能替自己放寬限制）。改了馬上生效。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SetIn) (View, error) {
			if call.Identity.Kind != ops.KindOwner {
				return View{}, ops.Forbidden("只有你本人能改限制")
			}
			t := limits.CurrentTunables()
			if in.Reset {
				t = limits.DefaultTunables
			}
			d := limits.DefaultTunables
			given := map[string]*int{"max_hops": in.MaxHops, "room_rounds": in.RoomRounds, "bot_creates_per_day": in.BotCreatesPerDay,
				"manager_bot_creates_per_day": in.ManagerBotCreatesPerDay, "bots_max": in.BotsMax, "auto_continue_max": in.AutoContinueMax,
				"agenda_open_max": in.AgendaOpenMax}
			var bad []ops.FieldError
			for _, f := range fields(&t, &d) {
				v := given[f.key]
				if v == nil {
					continue
				}
				r := limits.TunableRange[f.key]
				if *v < r[0] || *v > r[1] {
					bad = append(bad, ops.FieldError{Field: f.key, Message: fmt.Sprintf("要在 %d 到 %d 之間", r[0], r[1])})
					continue
				}
				*f.v = *v
			}
			if len(bad) > 0 {
				return View{}, ops.InvalidInput(bad...)
			}
			if call.DryRun {
				return View{Values: t, Defaults: d, Ranges: limits.TunableRange}, nil
			}
			if err := call.DB.SetSetting(ctx, SettingKey, t); err != nil {
				return View{}, err
			}
			limits.SetTunables(t)
			return view(), nil
		},
	})
}
