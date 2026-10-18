package ops

import (
	"context"
	"encoding/json"
	"errors"

	"xbot/internal/store"
	"xbot/internal/version"
)

// RegisterBuiltin 登錄登錄表本身提供的操作（目前只用來驗證整條流程；正式操作在 C05）。
func RegisterBuiltin(r *Registry) {
	Register(r, Op[PingIn, PingOut]{
		Name:    "xbot.system.ping",
		Summary: "確認 XBot 有在運作",
		Scope:   ScopeRead,
		Risk:    RiskSafe,
		Handle: func(_ context.Context, call *Call, in PingIn) (PingOut, error) {
			return PingOut{Pong: true, Echo: in.Echo, API: Version, Build: version.Version, Caller: call.Identity.Actor()}, nil
		},
	})
	Register(r, Op[SettingsGetIn, SettingsGetOut]{
		Name:    "xbot.settings.get",
		Summary: "讀一項設定",
		Scope:   ScopeAdmin,
		Risk:    RiskSafe,
		Handle:  settingsGet,
	})
}

// PingIn 是 xbot.system.ping 的輸入。
type PingIn struct {
	Echo string `json:"echo,omitempty" desc:"會原樣回傳的文字" max:"200"`
}

// PingOut 是 xbot.system.ping 的輸出。
type PingOut struct {
	Pong   bool   `json:"pong"`
	Echo   string `json:"echo,omitempty"`
	API    string `json:"api" desc:"操作的版本，例：xbot.v1"`
	Build  string `json:"build" desc:"XBot 的建置版本"`
	Caller string `json:"caller" desc:"呼叫者代號：owner、agent:<id>、bot:<id>"`
}

// SettingsGetIn 是 xbot.settings.get 的輸入。
type SettingsGetIn struct {
	Name string `json:"name" required:"true" desc:"設定名稱" max:"200"`
}

// SettingsGetOut 是 xbot.settings.get 的輸出。密鑰類設定只回「已設定」，不回值（MCP-11）。
type SettingsGetOut struct {
	Name   string          `json:"name"`
	Set    bool            `json:"set" desc:"有沒有設定過"`
	Value  json.RawMessage `json:"value,omitempty" desc:"設定值（JSON）；密鑰類設定不會回傳"`
	Secret bool            `json:"secret,omitempty" desc:"這是密鑰，只能寫、不能讀"`
}

func settingsGet(ctx context.Context, call *Call, in SettingsGetIn) (SettingsGetOut, error) {
	out := SettingsGetOut{Name: in.Name, Secret: IsSecretName(in.Name)}
	v, err := call.DB.GetSetting(ctx, in.Name)
	if errors.Is(err, store.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Set = true
	if !out.Secret {
		out.Value = v
	}
	return out, nil
}
