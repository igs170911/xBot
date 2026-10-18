package teaching

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"xbot/internal/limits"
)

// 錄製器寫的原始紀錄 → 你會怎麼描述的步驟。照 LazyBoy server/teaching.py（normalize、describe、scrub_url）。

// Target 是被操作的元素（錄製器依角色、可及名稱、標籤記下，不靠座標）。
type Target struct {
	Role     string    `json:"role,omitempty"`
	Name     string    `json:"name,omitempty"`
	Label    string    `json:"label,omitempty"`
	Tag      string    `json:"tag,omitempty"`
	Type     string    `json:"type,omitempty"`
	Selector string    `json:"selector,omitempty" desc:"CSS 定位（瀏覽器）"`
	Href     string    `json:"href,omitempty"`
	Frame    string    `json:"frame,omitempty"`
	Box      []float64 `json:"box,omitempty" desc:"元素的位置 [x, y, 寬, 高]（瀏覽器：視窗座標；桌面：螢幕座標）"`
	Viewport []float64 `json:"viewport,omitempty" desc:"瀏覽器視窗大小 [寬, 高]"`
}

// Result 是一步造成的換頁。
type Result struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	Shot  string `json:"shot,omitempty"`
}

// Step 是整理過的一步。
type Step struct {
	ID     string  `json:"id" desc:"步驟 id（刪除步驟時用）"`
	At     int64   `json:"at" desc:"發生時間（毫秒）"`
	Source string  `json:"source" desc:"browser 瀏覽器、desktop 桌面 App、user 你的補充說明"`
	Kind   string  `json:"kind" desc:"click、right_click、input、select、upload、submit、key、navigate、tab、note"`
	Text   string  `json:"text" desc:"白話的一步，例：點擊按鈕「搜尋」"`
	Value  string  `json:"value,omitempty" desc:"輸入或選擇的值（密碼記成 ••••）"`
	Key    string  `json:"key,omitempty"`
	Shot   string  `json:"shot,omitempty" desc:"截圖（教學資料夾裡的相對路徑，用 xbot.teaching.shot 讀）"`
	URL    string  `json:"url,omitempty"`
	Title  string  `json:"title,omitempty"`
	Window string  `json:"window,omitempty" desc:"桌面 App 的視窗標題"`
	NewTab bool    `json:"new_tab,omitempty"`
	Target *Target `json:"target,omitempty"`
	Result *Result `json:"result,omitempty"`
	// Mark 是截圖上要框起來的地方 [x, y, 寬, 高]（0–1 的比例）；E04 的紅框。
	Mark []float64 `json:"mark,omitempty"`

	checked *bool
	secret  bool
	atLast  int64
	point   []float64
	screen  []float64
}

// row 是錄製器寫的一列（browser.jsonl、desktop.jsonl）。
type row struct {
	At      float64   `json:"at"`
	Source  string    `json:"source"`
	Kind    string    `json:"kind"`
	Value   string    `json:"value"`
	Key     string    `json:"key"`
	Shot    string    `json:"shot"`
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Window  string    `json:"window"`
	Secret  bool      `json:"secret"`
	Checked *bool     `json:"checked"`
	Target  *Target   `json:"target"`
	Point   []float64 `json:"point"`
	Screen  []float64 `json:"screen"`
}

// Note 是你在示範中補充的一句說明。
type Note struct {
	At   int64  `json:"at"`
	Text string `json:"text"`
}

var secretParam = regexp.MustCompile(`(?i)pass|pwd|otp|token|secret|cvv|cvc|auth|session|code`)

// roleZH 是元素角色的白話。LazyBoy ROLE_ZH。
var roleZH = map[string]string{"button": "按鈕", "link": "連結", "textbox": "文字框", "searchbox": "搜尋框", "combobox": "下拉選單",
	"checkbox": "勾選框", "radio": "選項", "switch": "開關", "tab": "分頁標籤", "menuitem": "選單項目",
	"menu": "選單", "option": "選項", "img": "圖片", "heading": "標題", "listitem": "清單項目",
	"cell": "表格欄位", "password": "密碼欄", "toggle": "切換按鈕", "treeitem": "項目", "file": "檔案欄位"}

// causes：會讓頁面改變的步驟擁有它造成的換頁。
var causes = map[string]bool{"click": true, "submit": true, "key": true, "select": true}

// scrubURL 把網址查詢字串裡像密碼、權杖的參數值換成 ••••。
func scrubURL(u string) string {
	q := strings.Index(u, "?")
	if u == "" || q < 0 {
		return u
	}
	base, query, frag := u[:q], u[q+1:], ""
	if h := strings.Index(query, "#"); h >= 0 {
		query, frag = query[:h], query[h:]
	}
	parts := strings.Split(query, "&")
	for i, p := range parts {
		k, _, _ := strings.Cut(p, "=")
		name, err := url.QueryUnescape(k)
		if err != nil {
			name = k
		}
		if secretParam.MatchString(name) {
			parts[i] = k + "=••••"
		}
	}
	return base + "?" + strings.Join(parts, "&") + frag
}

func targetText(t *Target) string {
	if t == nil {
		return ""
	}
	name := t.Name
	if name == "" {
		name = t.Label
	}
	role := roleZH[t.Role]
	if name != "" {
		return role + "「" + name + "」"
	}
	if role != "" {
		return role
	}
	return t.Tag
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// describe 把一步寫成白話。LazyBoy describe()。
func describe(s *Step) string {
	where := ""
	if s.Source == "desktop" && s.Window != "" {
		where = "在「" + s.Window + "」"
	}
	var text string
	switch s.Kind {
	case "click":
		verb := "點擊"
		if s.checked != nil {
			verb = map[bool]string{true: "勾選", false: "取消勾選"}[*s.checked]
		}
		text = where + verb + or(targetText(s.Target), "畫面上的位置")
	case "right_click":
		text = where + "右鍵點擊" + or(targetText(s.Target), "畫面上的位置")
	case "input":
		// 自動完成的搜尋框自稱 combobox；對人來說是文字框。
		field := s.Target
		if field != nil && field.Role == "combobox" {
			f := *field
			f.Role = "textbox"
			field = &f
		}
		text = where + "在" + or(targetText(field), "輸入框") + "輸入「" + s.Value + "」"
	case "select":
		text = "在" + or(targetText(s.Target), "下拉選單") + "選擇「" + s.Value + "」"
	case "upload":
		text = "在" + or(targetText(s.Target), "檔案欄位") + "上傳「" + s.Value + "」"
	case "submit":
		text = "送出表單"
	case "key":
		text = where + "按 " + s.Key
	case "navigate":
		if s.NewTab {
			text = "在新分頁"
		}
		text += "前往「" + or(s.Title, s.URL) + "」"
	case "tab":
		text = "開新分頁"
	case "note":
		text = "使用者補充：" + s.Value
	default:
		text = s.Kind
	}
	if s.Result != nil {
		text += " → 進入「" + or(s.Result.Title, s.Result.URL) + "」"
	}
	return text
}

// parseRows 讀錄製器的 JSON Lines（壞掉的列跳過）。
func parseRows(data ...[]byte) []row {
	var out []row
	for _, d := range data {
		sc := bufio.NewScanner(bytes.NewReader(d))
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			var r row
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind != "" && r.At > 0 {
				out = append(out, r)
			}
		}
	}
	return out
}

func sameField(a, b *Step) bool {
	ta, tb := a.Target, b.Target
	if ta == nil {
		ta = &Target{}
	}
	if tb == nil {
		tb = &Target{}
	}
	return a.Source == b.Source && ta.Selector == tb.Selector && ta.Name == tb.Name && a.Window == b.Window
}

func shotOr(a, b string) string { return or(a, b) }

// normalize：原始紀錄 → 你會描述的步驟。步驟 id 來自它的第一列，錄製中後面的列進來時 id 不變（刪除步驟才穩定）。
func normalize(rows []row, notes []Note, dropped []string) []Step {
	all := make([]row, 0, len(rows)+len(notes))
	all = append(all, rows...)
	for _, n := range notes {
		all = append(all, row{At: float64(n.At), Source: "user", Kind: "note", Value: n.Text})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At < all[j].At })
	ms := func(d interface{ Milliseconds() int64 }) int64 { return d.Milliseconds() }
	var steps []*Step
	for _, r := range all {
		at := int64(r.At)
		src := r.Source
		if src == "" {
			src = "x"
		}
		s := &Step{ID: fmt.Sprintf("%c%d%c", src[0], at, r.Kind[0]), At: at, Source: r.Source, Kind: r.Kind, Value: r.Value, Key: r.Key,
			Shot: r.Shot, URL: scrubURL(r.URL), Title: r.Title, Window: r.Window, Target: r.Target, checked: r.Checked, secret: r.Secret,
			point: r.Point, screen: r.Screen}
		if s.secret {
			s.Value = "••••"
		}
		var last *Step
		gap := int64(1 << 62)
		if len(steps) > 0 {
			last = steps[len(steps)-1]
			gap = at - last.At
		}
		if last != nil && s.Kind == "input" && last.Kind == "input" && sameField(last, s) {
			last.Value, last.Shot, last.atLast = s.Value, shotOr(s.Shot, last.Shot), at
			last.secret = last.secret || s.secret
			continue
		}
		if last != nil && s.Kind == "submit" && (last.Kind == "click" || last.Kind == "key") && gap < ms(limits.TeachSubmitMerge) {
			if last.Kind == "key" {
				last.Kind = "submit"
			}
			continue
		}
		if s.Kind == "navigate" {
			if last != nil && last.Kind == "navigate" && gap < ms(limits.TeachNavigateMerge) && last.Result == nil {
				last.URL, last.Title, last.Shot = s.URL, s.Title, shotOr(s.Shot, last.Shot)
				continue
			}
			if last != nil && last.Kind == "tab" && gap < ms(limits.TeachTabMerge) {
				last.Kind, last.NewTab, last.URL, last.Title, last.Shot = "navigate", true, s.URL, s.Title, shotOr(s.Shot, last.Shot)
				continue
			}
			var owner *Step
			for i := len(steps) - 1; i >= 0 && i >= len(steps)-3; i-- {
				c := steps[i]
				since := c.At
				if c.atLast != 0 {
					since = c.atLast
				}
				if causes[c.Kind] && at-since < ms(limits.TeachCauseWindow) {
					owner = c
					break
				}
			}
			if owner != nil {
				owner.Result = &Result{URL: s.URL, Title: s.Title, Shot: s.Shot}
				continue
			}
		}
		steps = append(steps, s)
	}
	drop := map[string]bool{}
	for _, d := range dropped {
		drop[d] = true
	}
	out := make([]Step, 0, len(steps))
	for _, s := range steps {
		if drop[s.ID] {
			continue
		}
		s.Text = describe(s)
		s.Mark = mark(s)
		out = append(out, *s)
		if len(out) == limits.TeachStepsMax {
			break
		}
	}
	return out
}

// mark 算截圖上要框起來的地方（比例）：瀏覽器是元素位置／視窗大小，桌面是元素位置（或點擊點）／螢幕大小。
func mark(s *Step) []float64 {
	if s.Shot == "" || s.Kind == "navigate" || s.Kind == "tab" || s.Kind == "note" {
		return nil
	}
	size, box := s.screen, []float64(nil)
	if s.Target != nil {
		box = s.Target.Box
		if s.Source == "browser" {
			size = s.Target.Viewport
		}
	}
	if len(box) != 4 && len(s.point) == 2 {
		box = []float64{s.point[0] - 12, s.point[1] - 12, 24, 24}
	}
	if len(box) != 4 || len(size) != 2 || size[0] <= 0 || size[1] <= 0 || box[2] <= 0 || box[3] <= 0 {
		return nil
	}
	clamp := func(v float64) float64 { return max(0, min(1, v)) }
	x, y := clamp(box[0]/size[0]), clamp(box[1]/size[1])
	w, h := clamp((box[0]+box[2])/size[0])-x, clamp((box[1]+box[3])/size[1])-y
	if w <= 0 || h <= 0 {
		return nil
	}
	return []float64{round3(x), round3(y), round3(w), round3(h)}
}

func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

// keyShots 是值得放進技能的截圖：點了什麼、選了什麼，平均分布在整段示範裡。LazyBoy key_shots()。
func keyShots(steps []Step, limit int) []string {
	var shots []string
	seen := map[string]bool{}
	for _, s := range steps {
		switch s.Kind {
		case "click", "select", "submit", "input", "navigate", "key":
			if s.Shot != "" && !seen[s.Shot] {
				seen[s.Shot] = true
				shots = append(shots, s.Shot)
			}
		}
	}
	if len(shots) > limit && limit > 1 {
		picked := make([]string, limit)
		for i := range limit {
			picked[i] = shots[int(float64(i*(len(shots)-1))/float64(limit-1)+0.5)]
		}
		return picked
	}
	return shots
}

var shotName = regexp.MustCompile(`^shots/[bd]-[0-9]{4,6}\.jpg$`)

// ValidShot 回答 name 是不是錄製器產生的截圖檔名（擋路徑穿越）。
func ValidShot(name string) bool { return shotName.MatchString(name) }
