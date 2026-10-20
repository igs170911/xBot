package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// ChatGPT 方案的模型清單：要帶帳號，藏起來的不列，照 priority 排；新版號回空清單時用 0.0.0 再問一次。
func TestCodexModels(t *testing.T) {
	var versions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("ChatGPT-Account-ID") != "acct-1" || r.Header.Get("Authorization") != "Bearer at-1" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		v := r.URL.Query().Get("client_version")
		versions = append(versions, v)
		if v == "99.0.0" {
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.5","priority":2},{"slug":"gpt-hidden","visibility":"hide","priority":0},` +
			`{"slug":"gpt-6-sol","priority":1},{"slug":"gpt-x"}]}`))
	}))
	defer srv.Close()
	pr := Provider{ID: "openai-codex", BaseURL: srv.URL, Auth: AuthOAuthCodex, APIKey: "at-1", OAuth: &OAuthTokens{AccountID: "acct-1"}}
	got, why := (&Prober{}).ListModels(context.Background(), pr)
	if why != "" || !slices.Equal(got, []string{"gpt-6-sol", "gpt-5.5", "gpt-x"}) || !slices.Equal(versions, []string{"99.0.0", "0.0.0"}) {
		t.Fatalf("%v %q %v", got, why, versions)
	}
	pr.OAuth.AccountID = "other"
	if got, why := (&Prober{}).ListModels(context.Background(), pr); len(got) != 0 || why == "" {
		t.Fatalf("帳號不對要回原因：%v %q", got, why)
	}
}
