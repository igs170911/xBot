package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Bot 瀏覽器的登入（I05、E09，LazyBoy server/logins.py）：每個 Bot 有自己的瀏覽器，登入彼此獨立。
// 列出登入了哪些網站（只給網站名稱與 cookie 數，值永遠不離開電腦以外的地方，除了複製給另一個 Bot）、
// 移除某些網站的登入、把選定網站的登入複製給其他 Bot（分享前先記下對方原本的登入，10 分鐘內可以復原）。

// Cookie 是瀏覽器的一個 cookie（Playwright 的格式，原樣來回）。
type Cookie = map[string]any

// Browser 讀寫 Bot 瀏覽器的 cookies；DockerBrowser 在 Bot 的電腦裡用 Playwright 經 CDP 讀寫。
type Browser interface {
	Cookies(ctx context.Context, botID string) ([]Cookie, error)
	AddCookies(ctx context.Context, botID string, cookies []Cookie) (int, error)
	ClearSites(ctx context.Context, botID string, sites []string) error
}

// secondLevel 是國碼網域底下常見的第二層（shopee.tw 之外的 momoshop.com.tw）。LazyBoy SECOND_LEVEL。
var secondLevel = map[string]bool{"com": true, "co": true, "org": true, "net": true, "gov": true, "edu": true, "ac": true, "or": true, "ne": true, "go": true}

var localHosts = map[string]bool{"localhost": true, "127.0.0.1": true}

// SiteOf 是 cookie 所屬的網站：shopee.tw、momoshop.com.tw、google.com。LazyBoy site_of。
func SiteOf(domain string) string {
	labels := strings.Split(strings.ToLower(strings.TrimLeft(domain, ".")), ".")
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 && secondLevel[labels[len(labels)-2]] {
		return strings.Join(labels[len(labels)-3:], ".")
	}
	if len(labels) >= 2 {
		return strings.Join(labels[len(labels)-2:], ".")
	}
	return strings.Join(labels, ".")
}

// Belongs 回答 cookie 是不是屬於這些網站之一（網域相同或是子網域）。LazyBoy belongs。
func Belongs(c Cookie, sites []string) bool {
	d, _ := c["domain"].(string)
	d = strings.ToLower(strings.TrimLeft(d, "."))
	for _, s := range sites {
		if d == s || strings.HasSuffix(d, "."+s) {
			return true
		}
	}
	return false
}

// Site 是一個登入的網站。
type Site struct {
	Site    string `json:"site"`
	Cookies int    `json:"cookies"`
}

var siteRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func cleanSites(sites []string) ([]string, error) {
	set := map[string]bool{}
	for _, s := range sites {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !siteRE.MatchString(s) {
			return nil, ops.InvalidInput(ops.FieldError{Field: "sites", Message: "網站格式不對：" + s})
		}
		set[s] = true
	}
	if len(set) == 0 {
		return nil, ops.InvalidInput(ops.FieldError{Field: "sites", Message: "請選要的網站"})
	}
	if len(set) > limits.LoginSitesMax {
		return nil, ops.Limit(fmt.Sprintf("一次最多 %d 個網站", limits.LoginSitesMax))
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) browser() (Browser, error) {
	if s.o.Browser == nil {
		return nil, ops.Internal(errors.New("讀不到 Bot 的瀏覽器（沒有接上電腦）"))
	}
	return s.o.Browser, nil
}

func (s *Service) cookies(ctx context.Context, botID string) ([]Cookie, error) {
	b, err := s.browser()
	if err != nil {
		return nil, err
	}
	all, err := b.Cookies(ctx, botID)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return nil, ops.Errorf(ops.CodeInvalidInput, "這個 Bot 還沒有電腦，還沒有任何登入")
	}
	if err != nil {
		return nil, ops.Errorf(ops.CodeInternal, "讀不到這台電腦的瀏覽器登入，請確認電腦已開啟（%v）", err)
	}
	out := all[:0]
	for _, c := range all {
		d, _ := c["domain"].(string)
		if !localHosts[strings.TrimLeft(d, ".")] {
			out = append(out, c)
		}
	}
	return out, nil
}

// sites 列出一個 Bot 的瀏覽器登入了哪些網站（只有名稱與 cookie 數，依數量多到少）。
func (s *Service) sites(ctx context.Context, botID string) ([]Site, error) {
	cs, err := s.cookies(ctx, botID)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, c := range cs {
		d, _ := c["domain"].(string)
		if site := SiteOf(d); site != "" {
			count[site]++
		}
	}
	out := []Site{}
	for site, n := range count {
		out = append(out, Site{Site: site, Cookies: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cookies != out[j].Cookies {
			return out[i].Cookies > out[j].Cookies
		}
		return out[i].Site < out[j].Site
	})
	return out, nil
}

func (s *Service) forget(ctx context.Context, botID string, sites []string) (int, error) {
	sites, err := cleanSites(sites)
	if err != nil {
		return 0, err
	}
	b, err := s.browser()
	if err != nil {
		return 0, err
	}
	cs, err := s.cookies(ctx, botID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cs {
		if Belongs(c, sites) {
			n++
		}
	}
	if err := b.ClearSites(ctx, botID, sites); err != nil {
		return 0, ops.Errorf(ops.CodeInternal, "移除登入失敗：%v", err)
	}
	return n, nil
}

// ShareIn 是 xbot.logins.share 的輸入。
type ShareIn struct {
	FromBotID string   `json:"from_bot_id" required:"true" resource:"bot" max:"64" desc:"登入來源的 Bot"`
	ToBotIDs  []string `json:"to_bot_ids" required:"true" resource:"bot" min:"1" max:"50" desc:"要分享給哪些 Bot（它們原本同網站的登入會被取代）"`
	Sites     []string `json:"sites" required:"true" min:"1" max:"50" desc:"網站，例：shopee.tw（xbot.logins.list 列出的名稱）"`
}

// ShareOut 是 xbot.logins.share 的輸出。
type ShareOut struct {
	ShareID string         `json:"share_id" desc:"10 分鐘內可以用 xbot.logins.undo_share 復原"`
	Sites   []string       `json:"sites"`
	Targets []string       `json:"targets"`
	Copied  map[string]int `json:"copied" desc:"每個 Bot 寫入了幾個 cookie"`
}

type snapshot map[string][]Cookie // 目標 Bot → 分享前它在這些網站的 cookies

func (s *Service) share(ctx context.Context, call *ops.Call, in ShareIn) (ShareOut, error) {
	sites, err := cleanSites(in.Sites)
	if err != nil {
		return ShareOut{}, err
	}
	var targets []string
	for _, t := range in.ToBotIDs {
		if t == in.FromBotID {
			return ShareOut{}, ops.InvalidInput(ops.FieldError{Field: "to_bot_ids", Message: "請選另一個 Bot"})
		}
		if !slices.Contains(targets, t) {
			targets = append(targets, t)
		}
	}
	for _, id := range append([]string{in.FromBotID}, targets...) {
		if _, err := s.o.DB.GetBot(ctx, id); errors.Is(err, store.ErrNotFound) {
			return ShareOut{}, ops.NotFound("找不到這個 Bot：" + id)
		}
	}
	src, err := s.cookies(ctx, in.FromBotID)
	if err != nil {
		return ShareOut{}, err
	}
	var picked []Cookie
	for _, c := range src {
		if Belongs(c, sites) {
			picked = append(picked, c)
		}
	}
	if len(picked) == 0 {
		return ShareOut{}, ops.InvalidInput(ops.FieldError{Field: "sites", Message: "這些網站在來源 Bot 的瀏覽器裡沒有登入資料"})
	}
	if call.DryRun {
		return ShareOut{Sites: sites, Targets: targets, Copied: map[string]int{}}, nil
	}
	b, _ := s.browser()
	snap := snapshot{}
	out := ShareOut{Sites: sites, Targets: targets, Copied: map[string]int{}}
	for _, t := range targets {
		before, err := s.cookies(ctx, t)
		if err != nil {
			return out, err
		}
		var mine []Cookie
		for _, c := range before {
			if Belongs(c, sites) {
				mine = append(mine, c)
			}
		}
		snap[t] = mine
		// 對方原本的登入整個換掉（E09「對方原本的登入會被取代」）。
		if err := b.ClearSites(ctx, t, sites); err != nil {
			return out, ops.Errorf(ops.CodeInternal, "清掉對方原本的登入失敗：%v", err)
		}
		n, err := b.AddCookies(ctx, t, picked)
		if err != nil {
			return out, ops.Errorf(ops.CodeInternal, "寫入對方瀏覽器失敗：%v", err)
		}
		out.Copied[t] = n
	}
	raw, _ := json.Marshal(snap)
	rec := store.LoginShare{ID: store.NewID(), SourceBot: in.FromBotID, Targets: targets, Sites: sites, Actor: call.Identity.Actor(), CreatedAt: s.o.Now().UnixMilli()}
	if rec.Snapshot, err = s.o.Keys.Seal(raw, "login-share:"+rec.ID); err != nil {
		return out, ops.Internal(err)
	}
	if _, err := s.o.DB.CreateLoginShare(ctx, rec); err != nil {
		return out, ops.Internal(err)
	}
	_ = s.o.DB.ForgetLoginShareSnapshots(ctx, s.o.Now().Add(-limits.LoginShareUndo).UnixMilli())
	out.ShareID = rec.ID
	return out, nil
}

func (s *Service) undoShare(ctx context.Context, id string) (ShareOut, error) {
	rec, err := s.o.DB.GetLoginShare(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ShareOut{}, ops.NotFound("找不到這次分享")
	}
	if err != nil {
		return ShareOut{}, ops.Internal(err)
	}
	if rec.UndoneAt != 0 {
		return ShareOut{}, ops.Conflict("這次分享已經復原過了")
	}
	if s.o.Now().UnixMilli()-rec.CreatedAt > limits.LoginShareUndo.Milliseconds() || len(rec.Snapshot) == 0 {
		return ShareOut{}, ops.Conflict("超過 10 分鐘，不能復原了")
	}
	raw, err := s.o.Keys.Open(rec.Snapshot, "login-share:"+rec.ID)
	if err != nil {
		return ShareOut{}, ops.Internal(err)
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return ShareOut{}, ops.Internal(err)
	}
	b, err := s.browser()
	if err != nil {
		return ShareOut{}, err
	}
	out := ShareOut{ShareID: rec.ID, Sites: rec.Sites, Targets: rec.Targets, Copied: map[string]int{}}
	for _, t := range rec.Targets {
		if err := b.ClearSites(ctx, t, rec.Sites); err != nil {
			return out, ops.Errorf(ops.CodeInternal, "復原失敗：%v", err)
		}
		if len(snap[t]) > 0 {
			n, err := b.AddCookies(ctx, t, snap[t])
			if err != nil {
				return out, ops.Errorf(ops.CodeInternal, "復原失敗：%v", err)
			}
			out.Copied[t] = n
		}
	}
	if err := s.o.DB.MarkLoginShareUndone(ctx, rec.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return out, ops.Internal(err)
	}
	return out, nil
}
