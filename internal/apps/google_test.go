package apps

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Google 要 access_type=offline 才會給 refresh token，不然大約每小時要重新登入；別家不加。
func TestAuthorizeURLGoogleOffline(t *testing.T) {
	g, _ := url.Parse(authorizeURL(&oauthState{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", ClientID: "c", RedirectURI: "https://x/cb"}, "s", "ch"))
	if g.Query().Get("access_type") != "offline" || g.Query().Get("prompt") != "consent" {
		t.Fatalf("Google 要 offline：%s", g)
	}
	o, _ := url.Parse(authorizeURL(&oauthState{AuthURL: "https://auth.example.com/authorize", ClientID: "c"}, "s", "ch"))
	if o.Query().Has("access_type") || o.Query().Has("prompt") {
		t.Fatalf("別家不加：%s", o)
	}
}

// Gmail 的服務列出「全權管理信箱」「寄信」等權限；目錄只要讀信與草稿（Google 文件的最小權限）。
func TestGoogleCatalogScopesAreMinimal(t *testing.T) {
	gmail, ok := Lookup("gmail")
	if !ok || !gmail.NeedsClient || gmail.URL != "https://gmailmcp.googleapis.com/mcp/v1" {
		t.Fatalf("%+v", gmail)
	}
	for _, s := range gmail.Scopes {
		if strings.HasSuffix(s, "mail.google.com/") || strings.HasSuffix(s, "gmail.send") || strings.HasSuffix(s, "gmail.modify") {
			t.Fatalf("Gmail 不該要 %s", s)
		}
	}
	if !slices.Contains(gmail.Scopes, "https://www.googleapis.com/auth/gmail.readonly") {
		t.Fatalf("%v", gmail.Scopes)
	}
	for _, id := range []string{"google-calendar", "google-drive", "google-sheets"} {
		if a, ok := Lookup(id); !ok || len(a.Scopes) == 0 || !a.NeedsClient {
			t.Fatalf("%s：%+v", id, a)
		}
	}
}
