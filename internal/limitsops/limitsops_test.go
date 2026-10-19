package limitsops

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

func TestSetLoadAndOwnerOnly(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Cleanup(func() { limits.SetTunables(limits.DefaultTunables) })
	reg := ops.New(ops.Config{DB: db})
	Register(reg)
	call := func(id ops.Identity, op string, in any) (View, error) {
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, id, op, raw, ops.Options{})
		var v View
		if err == nil {
			_ = json.Unmarshal(res.Output, &v)
		}
		return v, err
	}
	owner := ops.Owner("test")
	v, err := call(owner, "xbot.limits.get", map[string]any{})
	if err != nil || v.Values != limits.DefaultTunables || v.Ranges["max_hops"] != [2]int{1, 100} {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = call(owner, "xbot.limits.set", map[string]any{"max_hops": 30, "bots_max": 500})
	if err != nil || v.Values.MaxHops != 30 || v.Values.BotsMax != 500 || v.Values.RoomRounds != limits.DefaultTunables.RoomRounds {
		t.Fatalf("只改有給的：%+v %v", v, err)
	}
	if limits.MaxHops() != 30 {
		t.Fatal("改了馬上生效")
	}
	if _, err := call(owner, "xbot.limits.set", map[string]any{"max_hops": 0}); ops.AsError(err).Code != ops.CodeInvalidInput {
		t.Fatalf("超出範圍：%v", err)
	}
	bot := ops.Identity{Kind: ops.KindBot, ID: "b1", Scopes: []ops.Scope{ops.ScopeAdmin}}
	if _, err := call(bot, "xbot.limits.set", map[string]any{"max_hops": 99}); err == nil {
		t.Fatal("Bot 不能替自己放寬限制")
	}
	// 重開服務：讀回存過的值
	limits.SetTunables(limits.DefaultTunables)
	if err := Load(ctx, db); err != nil || limits.MaxHops() != 30 || limits.BotsMax() != 500 {
		t.Fatalf("讀回：%v %d", err, limits.MaxHops())
	}
	v, err = call(owner, "xbot.limits.set", map[string]any{"reset": true})
	if err != nil || v.Values != limits.DefaultTunables {
		t.Fatalf("回到預設：%+v %v", v, err)
	}
}
