package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"xbot/internal/engines"
	"xbot/internal/ops"
	"xbot/internal/store"
)

var ctx = context.Background()

func setup(t *testing.T, d Deps) (*store.DB, *ops.Registry) {
	t.Helper()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg := ops.New(ops.Config{DB: db})
	Register(reg, d)
	return db, reg
}

func call[T any](t *testing.T, reg *ops.Registry, op string, in any) (T, error) {
	t.Helper()
	var out T
	raw, _ := json.Marshal(in)
	res, err := reg.Invoke(ctx, ops.Owner("test"), op, raw, ops.Options{})
	if err == nil {
		err = json.Unmarshal(res.Output, &out)
	}
	return out, err
}

func TestStatusDetectsEnginesLocalModelsAndImage(t *testing.T) {
	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-8b"},{"id":"gemma-3"}]}`))
	}))
	defer lm.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer broken.Close()

	ready := false
	db, reg := setup(t, Deps{
		Engines: func() []engines.HostLogin {
			return []engines.HostLogin{{Engine: "claude-code", Label: "Claude Code", Plan: "Claude", LoggedIn: true}, {Engine: "codex", Label: "Codex", Plan: "ChatGPT", Login: "codex login"}}
		},
		Image: func(context.Context) (string, bool, error) { return "xbot-box:dev", ready, nil },
		Local: []LocalServer{
			{ID: "lmstudio", Label: "LM Studio", BaseURL: lm.URL + "/v1", Preset: "lmstudio"},
			{ID: "ollama", Label: "Ollama", BaseURL: "http://127.0.0.1:1/v1"}, // 沒開
			{ID: "broken", Label: "壞的", BaseURL: broken.URL + "/v1"},
		},
	})
	st, err := call[StatusOut](t, reg, "xbot.onboard.status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.FirstRun || st.Bots != 0 || len(st.Engines) != 2 || !st.Engines[0].LoggedIn || st.Engines[1].LoggedIn {
		t.Fatalf("狀態：%+v", st)
	}
	if len(st.Local) != 1 || st.Local[0].ID != "lmstudio" || len(st.Local[0].Models) != 2 || st.Local[0].Preset != "lmstudio" {
		t.Fatalf("本機模型：%+v", st.Local)
	}
	if st.Computer.Ready || !st.Computer.Docker || st.Computer.Hint == "" {
		t.Fatalf("映像還沒建：%+v", st.Computer)
	}
	ready = true
	st, _ = call[StatusOut](t, reg, "xbot.onboard.status", map[string]any{})
	if !st.Computer.Ready || st.Computer.Hint != "" {
		t.Fatalf("映像好了：%+v", st.Computer)
	}

	// 有 Bot 以後就不是第一次
	if _, err := db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes", ApprovalMode: "ask"}); err != nil {
		t.Fatal(err)
	}
	st, _ = call[StatusOut](t, reg, "xbot.onboard.status", map[string]any{"skip_detect": true})
	if st.FirstRun || st.Bots != 1 || len(st.Local) != 0 {
		t.Fatalf("有 Bot 以後：%+v", st)
	}
}

func TestStatusDockerDown(t *testing.T) {
	_, reg := setup(t, Deps{Image: func(context.Context) (string, bool, error) { return "xbot-box:dev", false, errors.New("連不上") }, Local: []LocalServer{}})
	st, err := call[StatusOut](t, reg, "xbot.onboard.status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Computer.Docker || st.Computer.Error == "" || st.Computer.Hint == "" {
		t.Fatalf("Docker 沒開：%+v", st.Computer)
	}
}

func TestUpdateRemembersState(t *testing.T) {
	_, reg := setup(t, Deps{Local: []LocalServer{}})
	st, err := call[State](t, reg, "xbot.onboard.update", map[string]any{"completed": true, "dismiss": "phone"})
	if err != nil || !st.Completed || st.CompletedAt == 0 || len(st.Dismissed) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	st, _ = call[State](t, reg, "xbot.onboard.update", map[string]any{"tour_done": true, "dismiss": "phone"})
	if !st.TourDone || len(st.Dismissed) != 1 || !st.Completed {
		t.Fatalf("重複關掉同一張只記一次：%+v", st)
	}
	out, _ := call[StatusOut](t, reg, "xbot.onboard.status", map[string]any{"skip_detect": true})
	if out.FirstRun || !out.State.TourDone {
		t.Fatalf("做完首次設定就不再顯示：%+v", out)
	}
	st, _ = call[State](t, reg, "xbot.onboard.update", map[string]any{"restore": true, "tour_done": false})
	if len(st.Dismissed) != 0 || st.TourDone {
		t.Fatalf("放回卡片、再看一次導覽：%+v", st)
	}
	if _, err := call[State](t, reg, "xbot.onboard.update", map[string]any{"dismiss": "nope"}); err == nil {
		t.Fatal("不認得的卡片要拒絕")
	}
}
