package opsclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type nested struct {
	Bots  []string `json:"bots,omitempty"`
	Level int      `json:"level,omitempty"`
}

type flagIn struct {
	Name    string            `json:"name" required:"true"`
	Count   int               `json:"count,omitempty"`
	Ratio   float64           `json:"ratio,omitempty"`
	On      bool              `json:"on,omitempty"`
	Tags    []string          `json:"tags,omitempty"`
	Nums    []int             `json:"nums,omitempty"`
	Opts    nested            `json:"opts,omitzero"`
	Labels  map[string]string `json:"labels,omitempty"`
	Payload json.RawMessage   `json:"payload,omitempty"`
}

func testInfo(t *testing.T) ops.Info {
	t.Helper()
	reg := ops.New(ops.Config{})
	ops.Register(reg, ops.Op[flagIn, struct{}]{Name: "xbot.test.do", Summary: "測試", Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(context.Context, *ops.Call, flagIn) (struct{}, error) { return struct{}{}, nil }})
	info, _ := reg.Get("xbot.test.do")
	return info
}

func parse(t *testing.T, args ...string) (map[string]any, ops.Options) {
	t.Helper()
	p, err := ParseArgs(testInfo(t), args, strings.NewReader(`{"name":"from-stdin","count":9}`))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(p.Input, &m); err != nil {
		t.Fatal(err)
	}
	return m, p.Opts
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestParseArgsScalarsAndArrays(t *testing.T) {
	m, opts := parse(t, "--name", "x", "--count=3", "--ratio", "0.5", "--on", "--tags", "a", "--tags", "b", "--nums", "[1,2]", "--dry-run", "--request-id", "r1")
	want := `{"count":3,"name":"x","nums":[1,2],"on":true,"ratio":0.5,"tags":["a","b"]}`
	if jsonOf(m) != want {
		t.Fatalf("得到 %s", jsonOf(m))
	}
	if !opts.DryRun || opts.RequestID != "r1" {
		t.Fatalf("共用旗標不對：%+v", opts)
	}
	m, _ = parse(t, "--name", "x", "--on=false", "--nums", "4", "--nums", "5")
	if jsonOf(m) != `{"name":"x","nums":[4,5],"on":false}` {
		t.Fatalf("得到 %s", jsonOf(m))
	}
	m, _ = parse(t, "--name", "x", "--on", "false")
	if m["on"] != false {
		t.Fatal("--on false 應該是 false")
	}
}

func TestParseArgsNested(t *testing.T) {
	m, _ := parse(t, "--name", "x", "--opts.bots", "b1", "--opts.bots", "b2", "--opts.level", "2", "--labels", `{"k":"v"}`, "--payload", `{"any":[1]}`)
	want := `{"labels":{"k":"v"},"name":"x","opts":{"bots":["b1","b2"],"level":2},"payload":{"any":[1]}}`
	if jsonOf(m) != want {
		t.Fatalf("得到 %s", jsonOf(m))
	}
	// 整個物件給 JSON，再用巢狀旗標覆蓋其中一欄。
	m, _ = parse(t, "--json", `{"name":"j","opts":{"bots":["z"],"level":1}}`, "--opts.level", "5")
	if jsonOf(m) != `{"name":"j","opts":{"bots":["z"],"level":5}}` {
		t.Fatalf("得到 %s", jsonOf(m))
	}
	m, _ = parse(t, "--json", "-", "--count", "1")
	if jsonOf(m) != `{"count":1,"name":"from-stdin"}` {
		t.Fatalf("得到 %s", jsonOf(m))
	}
	m, _ = parse(t, "--payload", "plain text")
	if m["payload"] != "plain text" {
		t.Fatal("任意 JSON 欄位看不懂時當文字")
	}
}

func TestParseArgsErrors(t *testing.T) {
	info := testInfo(t)
	for _, args := range [][]string{
		{"--nope", "1"},
		{"--count", "abc"},
		{"--nums", "x"},
		{"--name"},
		{"positional"},
		{"--opts.nope", "1"},
		{"--json", "[1]"},
		{"--labels", "{bad"},
	} {
		if _, err := ParseArgs(info, args, nil); err == nil {
			t.Errorf("%v 應該失敗", args)
		}
	}
	if _, err := ParseArgs(info, []string{"--help"}, nil); !errors.Is(err, ErrHelp) {
		t.Fatal("--help 應該回 ErrHelp")
	}
	u := Usage(info)
	for _, s := range []string{"--name", "--opts.bots", "必填", "--dry-run"} {
		if !strings.Contains(u, s) {
			t.Errorf("說明少了 %s", s)
		}
	}
}

func newReg(t *testing.T) (*ops.Registry, *auth.Pairing, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := auth.NewPairing(db)
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	auth.RegisterAgentOps(reg, p)
	return reg, p, db
}

// 對著真的 HTTP 出口（api.New）測：路由、標頭、錯誤格式和服務一致。
func TestClientHTTPRoute(t *testing.T) {
	reg, p, db := newReg(t)
	h := api.New(api.Options{
		Registry: reg, Bus: events.New(), DB: db, AgentPair: p.Handler(),
		Auth: auth.NewHTTP(auth.HTTPConfig{DB: db, AccessToken: "secret"}), // 關掉本機免登入，才測得到沒帶身分
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()

	c := &Client{BaseURL: srv.URL, Token: "secret"}
	res, err := c.Call(ctx, "xbot.system.ping", json.RawMessage(`{"echo":"x"}`), ops.Options{})
	if err != nil || !strings.Contains(string(res.Output), `"caller":"owner"`) {
		t.Fatalf("%s %v", res.Output, err)
	}
	res, err = c.Call(ctx, "xbot.agents.pair_start", json.RawMessage(`{"scopes":["read"]}`), ops.Options{DryRun: true})
	if err != nil || !res.DryRun {
		t.Fatalf("預演標頭沒傳到：%+v %v", res, err)
	}
	res, err = c.Call(ctx, "xbot.agents.pair_start", json.RawMessage(`{"scopes":["read"]}`), ops.Options{RequestID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Call(ctx, "xbot.agents.pair_start", json.RawMessage(`{"scopes":["read"]}`), ops.Options{RequestID: "a"})
	if err != nil || !again.Replayed {
		t.Fatalf("request_id 標頭沒傳到：%+v %v", again, err)
	}
	var out struct{ Code string }
	_ = json.Unmarshal(res.Output, &out)
	pr, err := (&Client{BaseURL: srv.URL}).Pair(ctx, out.Code, "CLI")
	if err != nil || pr.Token == "" {
		t.Fatalf("換權杖失敗：%v", err)
	}
	_, err = (&Client{BaseURL: srv.URL}).Pair(ctx, out.Code, "CLI")
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeForbidden {
		t.Fatalf("配對碼只能用一次：%v", err)
	}
	// 沒帶身分 → 服務回的 MCP-5 錯誤原樣交回。
	_, err = (&Client{BaseURL: srv.URL}).Call(ctx, "xbot.system.ping", nil, ops.Options{})
	if !errors.As(err, &oe) || oe.Code != ops.CodeForbidden {
		t.Fatalf("應該 forbidden：%v", err)
	}
}
