package routines

import (
	"slices"
	"strings"
	"testing"
)

var testBots = []BotRef{{ID: "b1", Name: "小秘"}, {ID: "b2", Name: "阿寶"}, {ID: "b3", Name: "阿寶二號"}}

// 現在是 2026-10-13（週二）上午 10:00（台北）。
var parseNow = at(taipei, 2026, 10, 13, 10, 0)

func TestParseSentences(t *testing.T) {
	cases := []struct {
		text string
		want Schedule // 只比對有填的欄位（Kind 一定比）
		bot  string
		task string
	}{
		{"每天早上 9 點整理新聞給我", Schedule{Kind: KindDaily, Time: "09:00"}, "b1", "整理新聞給我"},
		{"每天早上九點半整理新聞", Schedule{Kind: KindDaily, Time: "09:30"}, "b1", "整理新聞"},
		{"每日 21:00 寫日報", Schedule{Kind: KindDaily, Time: "21:00"}, "b1", "寫日報"},
		{"每晚十點提醒我睡覺", Schedule{Kind: KindDaily, Time: "22:00"}, "b1", "提醒我睡覺"},
		{"天天下午兩點十五分喝水", Schedule{Kind: KindDaily, Time: "14:15"}, "b1", "喝水"},
		{"平日下午三點請阿寶整理報表", Schedule{Kind: KindWeekdays, Time: "15:00"}, "b2", "整理報表"},
		{"工作日早上 8:30 看信箱", Schedule{Kind: KindWeekdays, Time: "08:30"}, "b1", "看信箱"},
		{"週一到週五 晚上 7 點 備份資料", Schedule{Kind: KindWeekdays, Time: "19:00"}, "b1", "備份資料"},
		{"每週一早上十點開週會前整理待辦", Schedule{Kind: KindWeekly, Time: "10:00", Weekdays: []int{1}}, "b1", "開週會前整理待辦"},
		{"每週一、三、五 上午 9 點 讓阿寶二號查股價", Schedule{Kind: KindWeekly, Time: "09:00", Weekdays: []int{1, 3, 5}}, "b3", "查股價"},
		{"每星期二和星期四中午12點訂便當", Schedule{Kind: KindWeekly, Time: "12:00", Weekdays: []int{2, 4}}, "b1", "訂便當"},
		{"每個禮拜天晚上八點整理照片", Schedule{Kind: KindWeekly, Time: "20:00", Weekdays: []int{7}}, "b1", "整理照片"},
		{"週末早上十點推薦電影", Schedule{Kind: KindWeekly, Time: "10:00", Weekdays: []int{6, 7}}, "b1", "推薦電影"},
		{"每月 5 號上午 10 點繳費提醒", Schedule{Kind: KindMonthly, Time: "10:00", DayOfMonth: 5}, "b1", "繳費提醒"},
		{"每個月的十五日下午四點對帳", Schedule{Kind: KindMonthly, Time: "16:00", DayOfMonth: 15}, "b1", "對帳"},
		{"每 30 分鐘檢查網站有沒有掛", Schedule{Kind: KindInterval, EveryMinutes: 30}, "b1", "檢查網站有沒有掛"},
		{"每隔兩小時看一下訂單", Schedule{Kind: KindInterval, EveryMinutes: 120}, "b1", "看一下訂單"},
		{"每小時同步一次行事曆", Schedule{Kind: KindInterval, EveryMinutes: 60}, "b1", "同步一次行事曆"},
		{"每半小時看新訊息", Schedule{Kind: KindInterval, EveryMinutes: 30}, "b1", "看新訊息"},
		{"上班時間每 15 分鐘看客服信", Schedule{Kind: KindInterval, EveryMinutes: 15, ActiveFrom: "09:00", ActiveTo: "18:00", Time: "09:00"}, "b1", "看客服信"},
		{"早上9點到下午6點每30分鐘巡一次 Threads", Schedule{Kind: KindInterval, EveryMinutes: 30, ActiveFrom: "09:00", ActiveTo: "18:00"}, "b1", "巡一次 Threads"},
		{"每 20 分鐘查天氣，到 12 月 31 日為止", Schedule{Kind: KindInterval, EveryMinutes: 20, EndDate: "2026-12-31"}, "b1", "查天氣"},
		{"每天早上 8 點寄摘要直到11月底", Schedule{Kind: KindDaily, Time: "08:00", EndDate: "2026-11-30"}, "b1", "寄摘要"},
		{"明天早上 8 點提醒我帶護照", Schedule{Kind: KindOnce, Date: "2026-10-14", Time: "08:00"}, "b1", "提醒我帶護照"},
		{"明晚七點訂位", Schedule{Kind: KindOnce, Date: "2026-10-14", Time: "19:00"}, "b1", "訂位"},
		{"後天下午三點半跟阿寶確認進度", Schedule{Kind: KindOnce, Date: "2026-10-15", Time: "15:30"}, "b2", "確認進度"},
		{"10月20日上午九點交報告", Schedule{Kind: KindOnce, Date: "2026-10-20", Time: "09:00"}, "b1", "交報告"},
		{"3/1 早上 9 點報稅", Schedule{Kind: KindOnce, Date: "2027-03-01", Time: "09:00"}, "b1", "報稅"},
		{"下週一下午兩點開會提醒", Schedule{Kind: KindOnce, Date: "2026-10-19", Time: "14:00"}, "b1", "開會提醒"},
		{"這週五晚上 9 點發電子報", Schedule{Kind: KindOnce, Date: "2026-10-16", Time: "21:00"}, "b1", "發電子報"},
		{"cron: 0 9 * * 1-5 跑報表", Schedule{Kind: KindCron, Cron: "0 9 * * 1-5"}, "b1", "跑報表"},
		{"每天１８：３０下班提醒", Schedule{Kind: KindDaily, Time: "18:30"}, "b1", "下班提醒"},
	}
	for _, c := range cases {
		p := Parse(c.text, parseNow, taipei, testBots)
		s := p.Schedule
		w := c.want
		ok := s.Kind == w.Kind && (w.Time == "" || s.Time == w.Time) && (w.Date == "" || s.Date == w.Date) &&
			(w.Weekdays == nil || slices.Equal(s.Weekdays, w.Weekdays)) && (w.DayOfMonth == 0 || s.DayOfMonth == w.DayOfMonth) &&
			(w.EveryMinutes == 0 || s.EveryMinutes == w.EveryMinutes) && s.ActiveFrom == w.ActiveFrom && s.ActiveTo == w.ActiveTo &&
			s.EndDate == w.EndDate && (w.Cron == "" || s.Cron == w.Cron)
		if !ok {
			t.Errorf("%q：排程 %+v，要 %+v", c.text, s, w)
		}
		if p.BotID != c.bot {
			t.Errorf("%q：Bot %s，要 %s", c.text, p.BotID, c.bot)
		}
		if p.Prompt != c.task {
			t.Errorf("%q：要做的事 %q，要 %q", c.text, p.Prompt, c.task)
		}
		if len(p.Missing) != 0 {
			t.Errorf("%q：不該缺 %v", c.text, p.Missing)
		}
		if _, err := Normalize(s, 0, parseNow); err != nil {
			t.Errorf("%q：解析出來的排程要能用：%v", c.text, err)
		}
	}
}

func TestParseMissingAndAssumptions(t *testing.T) {
	// 沒寫時間：什麼時候欄要問（H02「沒看到時間，要幾點做？」）。
	p := Parse("每天整理新聞給我", parseNow, taipei, testBots)
	if p.Schedule.Kind != KindDaily || !slices.Contains(p.Missing, "time") || p.Prompt != "整理新聞給我" {
		t.Fatalf("缺時間：%+v", p)
	}
	p = Parse("每天早上整理新聞", parseNow, taipei, testBots)
	if !slices.Contains(p.Missing, "time") || p.Prompt != "整理新聞" {
		t.Fatalf("只寫早上：%+v", p)
	}
	// 沒寫要做什麼。
	if p := Parse("每天早上 9 點", parseNow, taipei, testBots); !slices.Contains(p.Missing, "task") {
		t.Fatalf("缺內容：%+v", p)
	}
	// 沒有 Bot。
	if p := Parse("每天早上 9 點整理新聞", parseNow, taipei, nil); !slices.Contains(p.Missing, "bot") {
		t.Fatalf("缺 Bot：%+v", p)
	}
	// 每隔少於 5 分鐘：改成 5 分鐘並提示（L17）。
	p = Parse("每 2 分鐘看股價", parseNow, taipei, testBots)
	if p.Schedule.EveryMinutes != 5 || len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "最短 5 分鐘") {
		t.Fatalf("間隔太短：%+v", p)
	}
	// 沒寫上午下午的 1～6 點當成下午。
	p = Parse("每天三點開會", parseNow, taipei, testBots)
	if p.Schedule.Time != "15:00" || len(p.Notes) == 0 {
		t.Fatalf("三點：%+v", p)
	}
	// 只寫幾點：跑一次，今天已過就明天。
	p = Parse("早上八點提醒我繳費", parseNow, taipei, testBots)
	if p.Schedule.Kind != KindOnce || p.Schedule.Date != "2026-10-14" || p.Schedule.Time != "08:00" {
		t.Fatalf("只寫幾點：%+v", p)
	}
	p = Parse("晚上八點提醒我繳費", parseNow, taipei, testBots)
	if p.Schedule.Kind != KindOnce || p.Schedule.Date != "2026-10-13" || p.Schedule.Time != "20:00" {
		t.Fatalf("今天還沒到：%+v", p)
	}
	// 每週沒寫星期幾：當成今天（週二）。
	p = Parse("每週早上九點寫週報", parseNow, taipei, testBots)
	if p.Schedule.Kind != KindWeekly || !slices.Equal(p.Schedule.Weekdays, []int{2}) {
		t.Fatalf("每週：%+v", p)
	}
	// 最長的 Bot 名字優先。
	if p := Parse("每天九點叫阿寶二號查信", parseNow, taipei, testBots); p.BotID != "b3" || p.Prompt != "查信" {
		t.Fatalf("Bot 名字：%+v", p)
	}
	if got := TitleOf("整理今天的科技新聞，挑三則最重要的寄給我。再附上來源"); got != "整理今天的科技新聞，挑三則最重要的寄給我" {
		t.Fatalf("標題：%s", got)
	}
}

func TestCNNumber(t *testing.T) {
	for s, want := range map[string]int{"9": 9, "十": 10, "十二": 12, "二十": 20, "二十三": 23, "兩": 2, "三十": 30, "零": 0, "百": -1, "": -1} {
		if got := cnNumber(s); got != want {
			t.Errorf("cnNumber(%q) = %d，要 %d", s, got, want)
		}
	}
}
