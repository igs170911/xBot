package models

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
)

// fakeOAuth 是假的訂閱登入：第一次查詢還沒完成，第二次完成。
type fakeOAuth struct{ polls int }

func (f *fakeOAuth) StartLogin(context.Context) (proxy.DeviceLogin, error) {
	return proxy.DeviceLogin{UserCode: "WXYZ-0001", VerificationURL: "https://login.example/device", Handle: "h-1"}, nil
}

func (f *fakeOAuth) PollLogin(_ context.Context, l proxy.DeviceLogin) (proxy.OAuthTokens, error) {
	f.polls++
	if f.polls == 1 {
		return proxy.OAuthTokens{}, proxy.ErrLoginPending
	}
	return proxy.OAuthTokens{AccessToken: "at-SECRET-1", RefreshToken: "rt-SECRET-1", AccountID: "acct",
		ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeOAuth) Refresh(context.Context, proxy.OAuthTokens) (proxy.OAuthTokens, error) {
	return proxy.OAuthTokens{}, proxy.ErrOAuthRelogin
}

func (f *fakeOAuth) Defaults() proxy.Provider {
	return proxy.Provider{BaseURL: "https://codex.example/backend-api/codex", Label: "ChatGPT 方案", Transport: proxy.TransportResponses}
}

// codexProber 是 ChatGPT 方案這個帳號列出的模型（不連網路）。
type codexProber struct{ fakeProber }

func (*codexProber) ListModels(context.Context, proxy.Provider) ([]string, string) {
	return []string{"gpt-7-test", "gpt-5.5"}, ""
}

func TestOAuthStartComplete(t *testing.T) {
	e := setup(t)
	e.pv.SetOAuth(proxy.AuthOAuthCodex, &fakeOAuth{})
	e.reg = ops.New(ops.Config{DB: e.db})
	ops.RegisterBuiltin(e.reg)
	Register(e.reg, Deps{Providers: e.pv, Lister: proxy.NewDBStore(e.db), Prober: &codexProber{}})

	if _, err := e.call(ops.Bot("b1", "小幫手"), "xbot.models.oauth_start", map[string]any{"kind": "openai-codex"}); err == nil {
		t.Fatal("Bot 不能登入訂閱")
	}
	raw, err := e.call(owner, "xbot.models.oauth_start", map[string]any{"kind": "openai-codex"})
	if err != nil {
		t.Fatal(err)
	}
	var s OAuthStartOut
	if err := json.Unmarshal(raw, &s); err != nil || s.LoginID == "" || s.UserCode != "WXYZ-0001" || s.IntervalSeconds != 3 {
		t.Fatalf("%s %v", raw, err)
	}
	if strings.Contains(string(raw), "h-1") {
		t.Fatal("登入伺服器的識別不能外流")
	}
	raw, err = e.call(owner, "xbot.models.oauth_complete", map[string]any{"login_id": s.LoginID})
	if err != nil || !strings.Contains(string(raw), `"pending"`) {
		t.Fatalf("%s %v", raw, err)
	}
	time.Sleep(3100 * time.Millisecond) // 最短查詢間隔（limits.OAuthMinPollInterval）
	// 沒指定預設模型、也還沒有預設：用這個帳號列出的第一個（不然登入完還是沒有模型可以用）。
	raw, err = e.call(owner, "xbot.models.oauth_complete", map[string]any{"login_id": s.LoginID})
	if err != nil {
		t.Fatal(err)
	}
	var c OAuthCompleteOut
	if err := json.Unmarshal(raw, &c); err != nil || c.Status != "done" || c.Provider == nil || c.Provider.Auth != proxy.AuthOAuthCodex ||
		c.Provider.ID != "openai-codex" || c.Default == nil || c.Default.Transport != "codex_responses" {
		t.Fatalf("%s %v", raw, err)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("輸出不能有權杖：%s", raw)
	}
	var def runtime.ModelRef
	if _, err := e.db.GetSettingInto(bg, runtime.DefaultModelSetting, &def); err != nil || def.Provider != "openai-codex" || def.Model != "gpt-7-test" || def.Transport != "codex_responses" {
		t.Fatalf("%+v %v", def, err)
	}

	// models.get 列出訂閱登入的供應商與這個帳號真的能用的模型（登入時讀的，不是內建清單）。
	raw, _ = e.call(owner, "xbot.models.get", map[string]any{})
	if !strings.Contains(string(raw), `"auth":"oauth_codex"`) || !strings.Contains(string(raw), "gpt-7-test") || strings.Contains(string(raw), "gpt-5.4-mini") ||
		strings.Contains(string(raw), "SECRET") {
		t.Fatalf("%s", raw)
	}
	// 不能用金鑰蓋掉訂閱登入；探測只更新模型清單。
	if _, err := e.call(owner, "xbot.models.update_provider", map[string]any{"id": "openai-codex", "api_key": "sk-x"}); code(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
	if _, err := e.call(owner, "xbot.models.set_provider", map[string]any{"id": "openai-codex", "base_url": "https://x.example/v1", "api_key": "sk-x"}); code(err) != ops.CodeConflict {
		t.Fatal(err)
	}
	if raw, err := e.call(owner, "xbot.models.probe", map[string]any{"provider": "openai-codex"}); err != nil || !strings.Contains(string(raw), "gpt-7-test") ||
		strings.Contains(string(raw), `"check"`) {
		t.Fatalf("%s %v", raw, err)
	}
	// 改名稱時權杖要保留。
	if _, err := e.call(owner, "xbot.models.update_provider", map[string]any{"id": "openai-codex", "label": "我的 ChatGPT"}); err != nil {
		t.Fatal(err)
	}
	p, err := e.pv.Get(bg, "openai-codex")
	if err != nil || p.OAuth == nil || string(p.OAuth.RefreshToken) != "rt-SECRET-1" || p.Label != "我的 ChatGPT" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestOAuthStartRefusesAPIKeyProvider(t *testing.T) {
	e := setup(t)
	e.pv.SetOAuth(proxy.AuthOAuthCodex, &fakeOAuth{})
	if _, err := e.call(owner, "xbot.models.set_provider", map[string]any{"id": "openai-codex", "base_url": "https://x.example/v1", "api_key": "sk-x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.call(owner, "xbot.models.oauth_start", map[string]any{"kind": "openai-codex"}); code(err) != ops.CodeConflict {
		t.Fatal(err)
	}
	if _, err := e.call(owner, "xbot.models.oauth_complete", map[string]any{"login_id": "nope"}); code(err) != ops.CodeNotFound {
		t.Fatal(err)
	}
}
