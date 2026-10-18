package apps

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"xbot/internal/ops"
)

// 翻自 LazyBoy tests/test_logins.py。

func TestCookieSitesGroupByRegistrableDomain(t *testing.T) {
	cases := map[string]string{".shopee.tw": "shopee.tw", "www.momoshop.com.tw": "momoshop.com.tw", "accounts.google.com": "google.com"}
	for in, want := range cases {
		if got := SiteOf(in); got != want {
			t.Fatalf("SiteOf(%q) = %q，要 %q", in, got, want)
		}
	}
	if !Belongs(Cookie{"domain": ".seller.shopee.tw"}, []string{"shopee.tw"}) || Belongs(Cookie{"domain": "notshopee.tw"}, []string{"shopee.tw"}) {
		t.Fatal("Belongs 不對")
	}
}

func TestOnlyChosenSitesAreCopiedValuesNeverListedAndShareCanBeUndone(t *testing.T) {
	e := setup(t)
	a, b := e.bot.ID, e.other.ID
	e.browser.jars[a] = []Cookie{
		{"name": "SPC_EC", "value": "secret-1", "domain": ".shopee.tw", "path": "/"},
		{"name": "SID", "value": "secret-2", "domain": ".google.com", "path": "/"},
		{"name": "dev", "value": "x", "domain": "localhost", "path": "/"},
	}
	e.browser.jars[b] = []Cookie{{"name": "OLD", "value": "mine", "domain": "seller.shopee.tw", "path": "/"}, {"name": "keep", "value": "k", "domain": ".canva.com"}}
	var list LoginsOut
	e.must(t, "xbot.logins.list", LoginsIn{BotID: a}, &list)
	if len(list.Sites) != 2 || list.Sites[0] != (Site{"google.com", 1}) || list.Sites[1] != (Site{"shopee.tw", 1}) {
		t.Fatalf("網站：%+v", list.Sites)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("不能回傳 cookie 的值")
	}
	// 代理分享要核准；本人直接做。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "Claude Code", Scopes: ops.Scopes}
	if err := e.call(t, agent, "xbot.logins.share", ShareIn{FromBotID: a, ToBotIDs: []string{b}, Sites: []string{"shopee.tw"}}, nil); codeOf(err) != ops.CodePendingApproval {
		t.Fatalf("分享要核准：%v", err)
	}
	var out ShareOut
	e.must(t, "xbot.logins.share", ShareIn{FromBotID: a, ToBotIDs: []string{b}, Sites: []string{"Shopee.tw"}}, &out)
	if out.Copied[b] != 1 || !slices.Equal(out.Sites, []string{"shopee.tw"}) || out.ShareID == "" {
		t.Fatalf("分享：%+v", out)
	}
	names := func(id string) []string {
		var n []string
		for _, c := range e.browser.jars[id] {
			n = append(n, c["name"].(string))
		}
		slices.Sort(n)
		return n
	}
	if got := names(b); !slices.Equal(got, []string{"SPC_EC", "keep"}) {
		t.Fatalf("對方同網站的登入被取代、其他網站不動：%v", got)
	}
	if err := e.call(t, owner, "xbot.logins.share", ShareIn{FromBotID: a, ToBotIDs: []string{a}, Sites: []string{"shopee.tw"}}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("不能分享給自己：%v", err)
	}
	if err := e.call(t, owner, "xbot.logins.share", ShareIn{FromBotID: a, ToBotIDs: []string{b}, Sites: []string{"example.com"}}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("沒有登入的網站：%v", err)
	}
	// 復原：對方換回原本的登入。
	e.must(t, "xbot.logins.undo_share", UndoShareIn{ShareID: out.ShareID}, nil)
	if got := names(b); !slices.Equal(got, []string{"OLD", "keep"}) {
		t.Fatalf("復原後：%v", got)
	}
	if err := e.call(t, owner, "xbot.logins.undo_share", UndoShareIn{ShareID: out.ShareID}, nil); codeOf(err) != ops.CodeConflict {
		t.Fatalf("只能復原一次：%v", err)
	}
	e.must(t, "xbot.logins.share", ShareIn{FromBotID: a, ToBotIDs: []string{b}, Sites: []string{"shopee.tw"}}, &out)
	e.advance(11 * time.Minute)
	if err := e.call(t, owner, "xbot.logins.undo_share", UndoShareIn{ShareID: out.ShareID}, nil); codeOf(err) != ops.CodeConflict {
		t.Fatalf("超過 10 分鐘不能復原：%v", err)
	}
	// 移除登入。
	var fo ForgetOut
	e.must(t, "xbot.logins.forget", ForgetIn{BotID: a, Sites: []string{"google.com"}}, &fo)
	if fo.Removed != 1 || slices.Contains(names(a), "SID") {
		t.Fatalf("移除：%+v %v", fo, names(a))
	}
	if err := e.call(t, owner, "xbot.logins.forget", ForgetIn{BotID: a, Sites: []string{"evil/../x"}}, nil); codeOf(err) != ops.CodeInvalidInput {
		t.Fatalf("網站格式：%v", err)
	}
}
