package teaching

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// 翻自 LazyBoy tests/test_teaching.py。

func field(name, selector, role string) *Target {
	if role == "" {
		role = "textbox"
	}
	return &Target{Role: role, Name: name, Selector: selector, Tag: "input"}
}

func texts(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Text
	}
	return out
}

func TestStepsReadLikeWhatThePersonDid(t *testing.T) {
	rows := []row{
		{At: 1000, Source: "browser", Kind: "navigate", URL: "https://shop.test/orders", Title: "訂單", Shot: "shots/b-1.jpg"},
		{At: 2000, Source: "browser", Kind: "input", Target: field("關鍵字", "#q", ""), Value: "耳", Shot: "shots/b-2.jpg"},
		{At: 2600, Source: "browser", Kind: "input", Target: field("關鍵字", "#q", ""), Value: "耳機", Shot: "shots/b-3.jpg"},
		{At: 3000, Source: "browser", Kind: "input", Target: field("密碼", "#pw", ""), Value: "hunter2", Secret: true},
		{At: 4000, Source: "browser", Kind: "click", Target: &Target{Role: "button", Name: "搜尋", Selector: "form > button"}, Shot: "shots/b-3.jpg"},
		{At: 4005, Source: "browser", Kind: "submit", Target: &Target{Role: "form"}},
		{At: 4500, Source: "browser", Kind: "navigate", URL: "https://shop.test/r?q=x&password=hunter2", Title: "結果"},
		{At: 9000, Source: "browser", Kind: "tab", URL: "chrome://new-tab-page/"},
		{At: 10000, Source: "browser", Kind: "navigate", URL: "https://shop.test/report", Title: "報表"},
		{At: 12000, Source: "desktop", Kind: "click", Window: "試算表", Target: &Target{Role: "menu", Name: "檔案"}},
	}
	steps := normalize(rows, []Note{{At: 3500, Text: "日期每次選上個月"}}, nil)
	want := []string{
		"前往「訂單」",
		"在文字框「關鍵字」輸入「耳機」",
		"在文字框「密碼」輸入「••••」",
		"使用者補充：日期每次選上個月",
		"點擊按鈕「搜尋」 → 進入「結果」",
		"在新分頁前往「報表」",
		"在「試算表」點擊選單「檔案」",
	}
	if got := texts(steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("步驟：\n%q\n要\n%q", got, want)
	}
	raw, _ := json.Marshal(steps)
	if strings.Contains(string(raw), "hunter2") {
		t.Fatalf("密碼不能出現在步驟裡：%s", raw)
	}
	if steps[4].Result.URL != "https://shop.test/r?q=x&password=••••" {
		t.Fatalf("結果網址要遮蔽密碼：%q", steps[4].Result.URL)
	}
	// 步驟 id 來自第一列：錄製中還有字打進來時，也能刪掉一步。
	first := normalize(rows[:2], nil, nil)[1].ID
	if steps[1].ID != first {
		t.Fatalf("id 要穩定：%s %s", steps[1].ID, first)
	}
	if got := texts(normalize(rows, nil, []string{first}))[1]; got != "在文字框「密碼」輸入「••••」" {
		t.Fatalf("刪掉一步後：%q", got)
	}
}

func TestEnterThatSubmitsAFormIsOneStep(t *testing.T) {
	rows := []row{{At: 1, Source: "browser", Kind: "key", Key: "Enter", Target: field("搜尋", "#s", "searchbox")},
		{At: 20, Source: "browser", Kind: "submit"},
		{At: 900, Source: "browser", Kind: "navigate", URL: "https://a.test/?s=x", Title: "搜尋結果"}}
	steps := normalize(rows, nil, nil)
	if len(steps) != 1 || steps[0].Kind != "submit" || steps[0].Text != "送出表單 → 進入「搜尋結果」" {
		t.Fatalf("%+v", steps)
	}
}

func TestDescribeKinds(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		s    Step
		want string
	}{
		{Step{Kind: "click", checked: &yes, Target: &Target{Role: "checkbox", Name: "同意"}}, "勾選勾選框「同意」"},
		{Step{Kind: "click", checked: &no, Target: &Target{Role: "checkbox", Label: "訂閱"}}, "取消勾選勾選框「訂閱」"},
		{Step{Kind: "click"}, "點擊畫面上的位置"},
		{Step{Kind: "right_click", Source: "desktop", Window: "檔案管理員"}, "在「檔案管理員」右鍵點擊畫面上的位置"},
		{Step{Kind: "input", Target: &Target{Role: "combobox", Name: "城市"}, Value: "台北"}, "在文字框「城市」輸入「台北」"},
		{Step{Kind: "select", Target: &Target{Role: "combobox", Name: "月份"}, Value: "九月"}, "在下拉選單「月份」選擇「九月」"},
		{Step{Kind: "upload", Value: "a.pdf"}, "在檔案欄位上傳「a.pdf」"},
		{Step{Kind: "key", Key: "Ctrl+S", Source: "desktop", Window: "文件"}, "在「文件」按 Ctrl+S"},
		{Step{Kind: "tab"}, "開新分頁"},
		{Step{Kind: "navigate", URL: "https://x.test/"}, "前往「https://x.test/」"},
		{Step{Kind: "click", Target: &Target{Tag: "div"}}, "點擊div"},
	}
	for _, c := range cases {
		if got := describe(&c.s); got != c.want {
			t.Errorf("%+v：%q，要 %q", c.s, got, c.want)
		}
	}
}

func TestScrubURL(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"https://a.test/x":                  "https://a.test/x",
		"https://a.test/?q=1&token=abc#top": "https://a.test/?q=1&token=••••#top",
		"https://a.test/?Pass%20Word=x&b":   "https://a.test/?Pass%20Word=••••&b",
		"https://a.test/?otp=1&auth_code=2": "https://a.test/?otp=••••&auth_code=••••",
	}
	for in, want := range cases {
		if got := scrubURL(in); got != want {
			t.Errorf("%q → %q，要 %q", in, got, want)
		}
	}
}

func TestKeyShotsSpreadOverTheDemo(t *testing.T) {
	var steps []Step
	for i := range 20 {
		steps = append(steps, Step{Kind: "click", Shot: "shots/b-" + string(rune('a'+i)) + ".jpg"})
	}
	steps = append(steps, Step{Kind: "note", Shot: "shots/x.jpg"}, Step{Kind: "click", Shot: "shots/b-a.jpg"})
	got := keyShots(steps, 8)
	if len(got) != 8 || got[0] != "shots/b-a.jpg" || got[7] != "shots/b-t.jpg" {
		t.Fatalf("%v", got)
	}
	if got := keyShots(steps[:3], 8); len(got) != 3 {
		t.Fatalf("%v", got)
	}
}

func TestMarkFramesWhatWasClicked(t *testing.T) {
	browser := normalize([]row{{At: 1, Source: "browser", Kind: "click", Shot: "shots/b-0001.jpg",
		Target: &Target{Role: "button", Name: "套用", Box: []float64{100, 50, 200, 25}, Viewport: []float64{1000, 500}}}}, nil, nil)
	if !reflect.DeepEqual(browser[0].Mark, []float64{0.1, 0.1, 0.2, 0.05}) {
		t.Fatalf("瀏覽器的框：%v", browser[0].Mark)
	}
	desktop := normalize([]row{{At: 1, Source: "desktop", Kind: "click", Shot: "shots/d-0001.jpg", Point: []float64{500, 400}, Screen: []float64{1000, 800}}}, nil, nil)
	if m := desktop[0].Mark; len(m) != 4 || m[0] != 0.488 || m[1] != 0.485 {
		t.Fatalf("桌面的框：%v", m)
	}
	nav := normalize([]row{{At: 1, Source: "browser", Kind: "navigate", Shot: "shots/b-0001.jpg"}}, nil, nil)
	if nav[0].Mark != nil {
		t.Fatalf("換頁不用框：%v", nav[0].Mark)
	}
}

func TestParseRowsSkipsBrokenLines(t *testing.T) {
	data := []byte(`{"at": 1, "source": "browser", "kind": "click"}
not json
{"at": 0, "kind": "click"}
{"at": 2, "source": "browser"}
{"at": 3.5, "source": "desktop", "kind": "key", "key": "Enter", "app": "x"}
`)
	rows := parseRows(data, []byte(`{"at": 9, "source": "browser", "kind": "tab"}`))
	if len(rows) != 3 || rows[1].Key != "Enter" || rows[2].Kind != "tab" {
		t.Fatalf("%+v", rows)
	}
}

func TestDraftHasStepsSelectorsAndShots(t *testing.T) {
	steps := normalize([]row{
		{At: 1000, Source: "browser", Kind: "navigate", URL: "https://shop.test/p/1", Title: "經典帆布托特包", Shot: "shots/b-0001.jpg"},
		{At: 3000, Source: "browser", Kind: "click", URL: "https://shop.test/p/1", Target: &Target{Role: "button", Name: "加入購物車", Selector: "#add"}, Shot: "shots/b-0002.jpg"},
		{At: 9000, Source: "browser", Kind: "input", URL: "https://shop.test/checkout", Target: field("折扣碼", "input[name=\"coupon\"]", ""), Value: "FALL10", Shot: "shots/b-0003.jpg"},
		{At: 20000, Source: "desktop", Kind: "key", Key: "Ctrl+S", Window: "記事本", Shot: "shots/d-0001.jpg"},
	}, []Note{{At: 10000, Text: "運費應該是 NT$60"}}, nil)
	d := buildDraft("重現結帳運費歸零", steps)
	for _, want := range []string{"## 目的\n重現結帳運費歸零", "1. 前往「經典帆布托特包」", "   - 網址：https://shop.test/p/1", "2. 點擊按鈕「加入購物車」\n   - 定位：`#add`",
		"   - 網址：https://shop.test/checkout", "定位：`input[name=\"coupon\"]`", "   - 使用者補充：運費應該是 NT$60", "4. （桌面）在「記事本」按 Ctrl+S",
		"截圖：shots/b-0001.jpg", "截圖：shots/d-0001.jpg", "密碼只記成 ••••"} {
		if !strings.Contains(d.Instructions, want) {
			t.Errorf("草稿少了 %q：\n%s", want, d.Instructions)
		}
	}
	if strings.Count(d.Instructions, "網址：https://shop.test/p/1") != 1 {
		t.Errorf("同一頁的網址只寫一次：\n%s", d.Instructions)
	}
	if d.Name != "重現結帳運費歸零" || !strings.Contains(d.Description, "重現結帳運費歸零") || d.By != "rules" || len(d.Shots) != 4 {
		t.Fatalf("%+v", d)
	}
	// 沒說要教什麼：用第一個頁面的標題。
	if d := buildDraft("", steps); d.Name != "在「經典帆布托特包」照示範操作" || !strings.Contains(d.Instructions, "請從步驟推論") {
		t.Fatalf("%+v", d)
	}
	// 你從內文刪掉的截圖不存。
	if got := usedShots(d, "截圖：shots/b-0002.jpg"); !reflect.DeepEqual(got, []string{"shots/b-0002.jpg"}) {
		t.Fatalf("%v", got)
	}
}

func TestValidShot(t *testing.T) {
	for name, ok := range map[string]bool{"shots/b-0001.jpg": true, "shots/d-0042.jpg": true, "../x.jpg": false, "shots/../../etc/passwd": false,
		"shots/b-1.jpg": false, "/shots/b-0001.jpg": false} {
		if ValidShot(name) != ok {
			t.Errorf("%q", name)
		}
	}
}
