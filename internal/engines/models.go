package engines

import (
	"context"

	"xbot/internal/engine/acp"
	"xbot/internal/store"
)

// 訂閱 CLI 列出的模型：agent 開 session 時回報（acp.Options.OnModels），記在 setting，
// 對話頁的模型選擇器用它列出這個方案能用的模型。同一個引擎的帳號相同，所以按引擎記，不按 Bot。

// modelsKey 是一個引擎的模型清單在 setting 表的 key。
func modelsKey(engine string) string { return "engine.models." + engine }

// EngineModels 是一個訂閱 CLI 上次回報的模型清單。
type EngineModels struct {
	// Default 是 agent 自己的預設（Bot 沒指定模型時用的）。
	Default string            `json:"default,omitempty"`
	Models  []acp.ModelChoice `json:"models"`
	At      int64             `json:"at,omitempty"`
}

// RememberModels 記下一個引擎回報的模型清單；寫不進去只記紀錄（不影響對話）。
func (s *Service) RememberModels(engine, current string, models []acp.ModelChoice) {
	if s == nil || s.DB == nil || len(models) == 0 {
		return
	}
	v := EngineModels{Default: current, Models: models, At: store.NowMS()}
	if err := s.DB.SetSetting(context.Background(), modelsKey(engine), v); err != nil {
		s.log().Warn("記下引擎的模型清單失敗", "engine", engine, "err", err)
	}
}

// engineModels 讀一個引擎上次回報的模型清單；沒有時是空的。
func (s *Service) engineModels(ctx context.Context, engine string) (EngineModels, error) {
	var v EngineModels
	if s.DB == nil {
		return v, nil
	}
	_, err := s.DB.GetSettingInto(ctx, modelsKey(engine), &v)
	return v, err
}
