package voice

import (
	"context"
	"errors"
	"sort"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// LoadSettings 讀全域設定；沒設定過時回預設值。
func LoadSettings(ctx context.Context, db *store.DB) (Settings, error) {
	set := DefaultSettings()
	if _, err := db.GetSettingInto(ctx, SettingsKey, &set); err != nil && !errors.Is(err, store.ErrNotFound) {
		return set, err
	}
	if set.HangupMinutes <= 0 {
		set.HangupMinutes = limits.VoiceHangupMinutesDefault
	}
	return set, nil
}

// LoadBot 讀一個 Bot 的聲音；沒設定過時回預設值。
func LoadBot(ctx context.Context, db *store.DB, botID string) (BotVoice, error) {
	v := DefaultBotVoice()
	if _, err := db.GetSettingInto(ctx, botKeyPrefix+botID, &v); err != nil && !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	if _, ok := VoiceByID(v.Voice); !ok {
		v.Voice = DefaultBotVoice().Voice
	}
	v.Speed = clampSpeed(v.Speed)
	return v, nil
}

// LoadThread 讀對話串的自動朗讀設定；nil 表示跟著 Bot。
func LoadThread(ctx context.Context, db *store.DB, threadID string) (*bool, error) {
	var v struct {
		AutoRead *bool `json:"auto_read"`
	}
	if _, err := db.GetSettingInto(ctx, threadKeyPrefix+threadID, &v); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return v.AutoRead, nil
}

func saveThread(ctx context.Context, db *store.DB, threadID string, auto *bool) error {
	if auto == nil {
		err := db.DeleteSetting(ctx, threadKeyPrefix+threadID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	return db.SetSetting(ctx, threadKeyPrefix+threadID, map[string]any{"auto_read": *auto})
}

// ForgetBot 刪掉 Bot 的聲音設定（刪除 Bot 時）。
func ForgetBot(ctx context.Context, db *store.DB, botID string) {
	_ = db.DeleteSetting(ctx, botKeyPrefix+botID)
}

func clampSpeed(s float64) float64 {
	if s == 0 {
		return 1
	}
	return min(max(s, limits.VoiceSpeedMin), limits.VoiceSpeedMax)
}

// EngineOption 是「語音引擎」下拉選單的一項。
type EngineOption struct {
	Provider string `json:"provider" desc:"空白是瀏覽器內建"`
	Label    string `json:"label"`
	// Usable 為 false 時不能用（訂閱登入只開放對話）。
	Usable bool   `json:"usable"`
	Note   string `json:"note,omitempty"`
}

// engines 列出可以選的語音引擎：瀏覽器內建＋每個設定好的模型供應商。
func (s *Service) engines(ctx context.Context) ([]EngineOption, map[string]bool, error) {
	out := []EngineOption{{Provider: "", Label: "瀏覽器內建", Usable: true, Note: "免費，不用金鑰；聲音依你的裝置而定"}}
	usable := map[string]bool{}
	if s == nil || s.Providers == nil {
		return out, usable, nil
	}
	ps, err := s.Providers(ctx)
	if err != nil {
		return out, usable, err
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	for _, p := range ps {
		label := p.Label
		if label == "" {
			label = p.ID
		}
		o := EngineOption{Provider: p.ID, Label: label, Usable: !p.OAuth}
		if p.OAuth {
			o.Note = "訂閱登入只能用來對話，不能用來說話"
		} else {
			o.Note = "要支援 OpenAI 相容的 audio API（例：OpenAI）"
		}
		usable[p.ID] = o.Usable
		out = append(out, o)
	}
	return out, usable, nil
}

// effective 回傳實際用的合成引擎：引擎沒設定、供應商不能用、或 Bot 選了系統內建聲音時是瀏覽器。
func effective(set Settings, usable map[string]bool, voice Option) (Engine, bool) {
	e := set.TTS
	if e.Provider == "" || !usable[e.Provider] || voice.Upstream == "" {
		return Engine{}, false
	}
	if strings.TrimSpace(e.Model) == "" {
		e.Model = DefaultTTSModel
	}
	return e, true
}
