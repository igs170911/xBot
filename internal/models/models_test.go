package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

var bg = context.Background()

const secret = "sk-REAL-SECRET-123456"

type env struct {
	db  *store.DB
	reg *ops.Registry
	pv  *proxy.Providers
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	kr, err := proxy.NewKeyring(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ps := proxy.NewDBStore(db)
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), pv: proxy.NewProviders(ps, kr)}
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, Deps{Providers: e.pv, Lister: ps})
	return e
}

var owner = ops.Owner("test")

func (e *env) call(id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(bg, id, op, raw, o)
	return res.Output, err
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func TestSetProviderAndGet(t *testing.T) {
	e := setup(t)
	var g GetOut
	raw, err := e.call(owner, "xbot.models.get", map[string]any{})
	if err != nil || json.Unmarshal(raw, &g) != nil || len(g.Providers) != 0 || g.Default != nil || g.Ready {
		t.Fatalf("一開始：%s %v", raw, err)
	}
	raw, err = e.call(owner, "xbot.models.set_provider", map[string]any{"id": "openai", "base_url": "https://api.example.com/v1/", "api_key": secret, "default_model": "gpt-x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("輸出不能有金鑰：%s", raw)
	}
	p, err := e.pv.Get(bg, "openai")
	if err != nil || string(p.APIKey) != secret || p.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("存進 Model Proxy：%+v %v", p, err)
	}
	var def runtime.ModelRef
	if ok, _ := e.db.GetSettingInto(bg, runtime.DefaultModelSetting, &def); !ok || def.Provider != "openai" || def.Model != "gpt-x" {
		t.Fatalf("預設模型：%+v", def)
	}
	raw, _ = e.call(owner, "xbot.models.get", map[string]any{})
	_ = json.Unmarshal(raw, &g)
	if len(g.Providers) != 1 || !g.Providers[0].APIKeySet || g.Default == nil || g.Default.Model != "gpt-x" || !g.Ready || strings.Contains(string(raw), secret) {
		t.Fatalf("%s", raw)
	}
	// 只改網址：金鑰保留。
	if _, err := e.call(owner, "xbot.models.set_provider", map[string]any{"id": "openai", "base_url": "https://other.example.com/v1"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.pv.Get(bg, "openai"); string(p.APIKey) != secret || p.BaseURL != "https://other.example.com/v1" {
		t.Fatalf("%+v", p)
	}
	// 稽核與資料庫的設定值都看不到金鑰。
	audit, _ := e.db.ListAudit(bg, store.AuditFilter{})
	settings, _ := e.db.ListSettings(bg)
	all, _ := json.Marshal(map[string]any{"audit": audit, "settings": settings})
	if strings.Contains(string(all), secret) {
		t.Fatal("金鑰外流到稽核或設定表")
	}
	// set_default。
	raw, err = e.call(owner, "xbot.models.set_default", map[string]any{"provider": "openai", "model": "gpt-y", "transport": "codex_responses"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.db.GetSettingInto(bg, runtime.DefaultModelSetting, &def); !ok || def.Model != "gpt-y" || def.Transport != "codex_responses" {
		t.Fatalf("%+v %s", def, raw)
	}
	if _, err := e.call(owner, "xbot.models.set_default", map[string]any{"provider": "nope", "model": "m"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("沒設定的供應商：%v", err)
	}
}

func TestSetProviderValidationAndPermissions(t *testing.T) {
	e := setup(t)
	for _, in := range []map[string]any{
		{"id": "OpenAI", "base_url": "https://x"}, {"id": "openai", "base_url": "ftp://x"},
		{"id": "openai", "base_url": "https://u:p@x"}, {"id": "openai", "base_url": "https://x", "transport": "chat_completions"},
		{"id": "openai"},
	} {
		if _, err := e.call(owner, "xbot.models.set_provider", in); code(err) != ops.CodeInvalidInput {
			t.Errorf("%v：%v", in, err)
		}
	}
	admin := ops.Identity{Kind: ops.KindAgent, ID: "a", Scopes: []ops.Scope{ops.ScopeAdmin, ops.ScopeRead}}
	if _, err := e.call(admin, "xbot.models.set_provider", map[string]any{"id": "openai", "base_url": "https://x", "api_key": secret}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理就算有 admin 也不能設金鑰：%v", err)
	}
	reader := ops.Identity{Kind: ops.KindAgent, ID: "r", Scopes: []ops.Scope{ops.ScopeRead}}
	if _, err := e.call(reader, "xbot.models.set_default", map[string]any{"provider": "openai", "model": "m"}); code(err) != ops.CodeForbidden {
		t.Fatalf("read 不能改預設模型：%v", err)
	}
	if _, err := e.call(reader, "xbot.models.get", map[string]any{}); err != nil {
		t.Fatalf("read 可以看：%v", err)
	}
	// 預演不寫。
	if _, err := e.call(owner, "xbot.models.set_provider", map[string]any{"id": "openai", "base_url": "https://x", "api_key": secret, "default_model": "m"}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pv.Get(bg, "openai"); !errors.Is(err, proxy.ErrNotFound) {
		t.Fatal("預演不應該寫入")
	}
	if _, err := e.db.GetSetting(bg, runtime.DefaultModelSetting); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("預演不應該設預設模型")
	}
}
