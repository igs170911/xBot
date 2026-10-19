package bots

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// 團隊管理權限開關以後要通知（Bot 看得到的 XBot MCP 工具跟著變，要請引擎重新載入）；改別的欄位不用。
func TestManageChangedOnPermissionToggle(t *testing.T) {
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	var changed []string
	Register(reg, Deps{ManageChanged: func(_ context.Context, id string) { changed = append(changed, id) }})
	e := &env{db: db, reg: reg}
	var v View
	e.must(t, owner, "xbot.bots.create", map[string]any{"name": "小秘"}, &v)
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "can_manage": true}, &v)
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "title": "主管", "can_manage": true}, &v)
	e.must(t, owner, "xbot.bots.update", map[string]any{"id": v.ID, "can_manage": false}, &v)
	if !slices.Equal(changed, []string{v.ID, v.ID}) || v.CanManage {
		t.Fatalf("開、關各通知一次：%v %+v", changed, v)
	}
}
