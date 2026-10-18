package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDBStoreProvidersOnlyCiphertext(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := NewDBStore(db)
	p := NewProviders(s, testKeyring(t))
	if _, err := p.Get(ctx, "openai"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("還沒設定時應該是 ErrNotFound：%v", err)
	}
	if err := p.Put(ctx, Provider{ID: "openai", BaseURL: "https://api.example.com/v1/", APIKey: realKey}); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(ctx, "openai")
	if err != nil || string(got.APIKey) != realKey || got.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("讀回：%+v %v", got, err)
	}
	all, err := db.ListSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(all[ProviderSettingPrefix+"openai"])
	if raw == "" || strings.Contains(raw, realKey) {
		t.Fatalf("setting 表裡應該只有密文：%s", raw)
	}
	ids, err := s.ProviderIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "openai" {
		t.Fatalf("供應商清單：%v %v", ids, err)
	}
	// 用另一把主金鑰解不開。
	other, _ := NewKeyring([]byte(strings.Repeat("x", 32)))
	if _, err := NewProviders(s, other).Get(ctx, "openai"); !errors.Is(err, ErrSealed) {
		t.Fatalf("換金鑰應該解不開：%v", err)
	}
}

func TestDBStoreWorkerKeys(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	bot, err := db.CreateBot(ctx, store.Bot{Name: "小幫手"})
	if err != nil {
		t.Fatal(err)
	}
	keys := NewWorkerKeys(NewDBStore(db), testKeyring(t))
	k1, err := keys.Issue(ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k2, _ := keys.Issue(ctx, bot.ID); k2 != k1 {
		t.Fatal("同一個 Bot 應該拿到同一把 key")
	}
	if id, err := keys.Verify(ctx, string(k1)); err != nil || id != bot.ID {
		t.Fatalf("驗證：%q %v", id, err)
	}
	k3, err := keys.Rotate(ctx, bot.ID)
	if err != nil || k3 == k1 {
		t.Fatalf("換 key：%v", err)
	}
	if _, err := keys.Verify(ctx, string(k1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("舊 key 應該失效：%v", err)
	}
	// 刪掉 Bot 時亂數一起刪（外鍵 cascade），key 自然失效。
	if err := db.DeleteBot(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Verify(ctx, string(k3)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("刪 Bot 後 key 應該失效：%v", err)
	}
	// 不存在的 Bot 不能發 key（外鍵）。
	if _, err := keys.Issue(ctx, "nobody"); err == nil {
		t.Fatal("不存在的 Bot 不應該拿到 key")
	}
}

func TestDBStoreHandlerRecordsUsage(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	bot, err := db.CreateBot(ctx, store.Bot{Name: "小幫手"})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+realKey {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4},"cost":0.0025}}`)
	}))
	defer upstream.Close()

	s := NewDBStore(db)
	s.Attribute = func(botID string) (string, string) { return "thread-1", "task-1" }
	kr := testKeyring(t)
	providers := NewProviders(s, kr)
	if err := providers.Put(ctx, Provider{ID: "openai", BaseURL: upstream.URL + "/v1", APIKey: realKey}); err != nil {
		t.Fatal(err)
	}
	keys := NewWorkerKeys(s, kr)
	k, err := keys.Issue(ctx, bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(Options{Keys: keys, Providers: providers, Usage: s}))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/model/openai/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("Authorization", "Bearer "+string(k))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.Contains(string(body), realKey) {
		t.Fatalf("回應：%d %s", resp.StatusCode, body)
	}
	rows, err := db.ListUsage(ctx, store.UsageFilter{BotID: bot.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("用量：%+v %v", rows, err)
	}
	u := rows[0]
	if u.InputTokens != 10 || u.OutputTokens != 3 || u.CacheReadTokens != 4 || u.CostMicros != 2500 ||
		u.Provider != "openai" || u.Model != "gpt-test" || u.Engine != "hermes" || u.ThreadID != "thread-1" || u.TaskID != "task-1" || u.CreatedAt == 0 {
		t.Fatalf("用量內容：%+v", u)
	}
}
