package memory

import (
	"context"
	"encoding/json"
	"log/slog"

	"xbot/internal/engine"
	"xbot/internal/events"
	"xbot/internal/limits"
)

// ToolName 是 Hermes 內建記憶工具的名稱（tools/memory_tool.py MEMORY_SCHEMA）：Bot 用它改 MEMORY.md／USER.md。
const ToolName = "memory"

// Watch 訂閱工具結果事件（kind 是 runtime.KindToolResult；memory 不能 import runtime）：Bot 用記憶工具改完記憶，
// 馬上記一筆變更，時間和對話串才對（不然要等有人打開記憶頁才發現，幾次修改還會併成一筆）。直到 ctx 結束。
func (s *Service) Watch(ctx context.Context, bus *events.Bus, kind string) {
	sub := bus.Subscribe(func(e events.Event) bool { return e.Kind == kind }, limits.MemoryWatchQueue)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if e.BotID == "" || !memoryWrite(e.Payload) {
				continue
			}
			if err := s.Notice(ctx, e.BotID, e.ThreadID, e.At); err != nil && ctx.Err() == nil {
				slog.Warn("記下 Bot 的記憶變更失敗，下次打開記憶頁時補記", "bot", e.BotID, "err", err)
			}
		}
	}
}

// memoryWrite 判斷工具結果是不是記憶工具成功跑完（失敗的沒有改到檔案）。
func memoryWrite(payload json.RawMessage) bool {
	var ev engine.Event
	return json.Unmarshal(payload, &ev) == nil && ev.Tool != nil && ev.Tool.Name == ToolName && ev.Tool.OK
}
