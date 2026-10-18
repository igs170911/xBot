package routines

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

var taipei = mustLoc("Asia/Taipei")
var newYork = mustLoc("America/New_York")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func at(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// 2026-10-13 是週二。
var tue0900 = at(taipei, 2026, 10, 13, 9, 0)

func norm(t *testing.T, s Schedule, jitter int) Schedule {
	t.Helper()
	out, err := Normalize(s, jitter, tue0900)
	if err != nil {
		t.Fatalf("Normalize(%+v)：%v", s, err)
	}
	return out
}

func slot(t *testing.T, s Schedule, after time.Time) time.Time {
	t.Helper()
	got, ok, err := s.Slot(after)
	if err != nil || !ok {
		t.Fatalf("Slot(%+v, %s)：ok=%v err=%v", s, after, ok, err)
	}
	return got
}

// LazyBoy test_time_is_normalized
func TestTimeIsNormalized(t *testing.T) {
	for raw, want := range map[string]string{"9:05": "09:05", "09:00:00": "09:00"} {
		if got := norm(t, Schedule{Kind: KindDaily, Time: raw}, 0).Time; got != want {
			t.Errorf("%s → %s，要 %s", raw, got, want)
		}
	}
}

// LazyBoy test_bad_schedules_get_readable_errors
func TestBadSchedulesGetReadableErrors(t *testing.T) {
	cases := []struct {
		s      Schedule
		jitter int
		msg    string
	}{
		{Schedule{Kind: KindDaily, Time: "9am"}, 0, "時間格式要像 09:00"},
		{Schedule{Kind: KindDaily}, 0, "請指定重複方式和時間"},
		{Schedule{Time: "09:00"}, 0, "請指定重複方式和時間"},
		{Schedule{Kind: "hourly", Time: "09:00"}, 0, "無效的排程頻率"},
		{Schedule{Kind: KindWeekly, Time: "09:00", Weekdays: []int{0}}, 0, "星期必須介於 1（週一）和 7（週日）"},
		{Schedule{Kind: KindWeekly, Time: "09:00"}, 0, "每週排程至少要選一個星期"},
		{Schedule{Kind: KindOnce, Time: "09:00", Date: "明天"}, 0, "單次排程必須指定日期"},
		{Schedule{Kind: KindDaily, Time: "09:00", Timezone: "Mars/Base"}, 0, "無效的時區"},
		{Schedule{Kind: KindMonthly, Time: "09:00", DayOfMonth: 32}, 0, "日期必須介於 1 和 31"},
		{Schedule{Kind: KindInterval, EveryMinutes: 4}, 0, "間隔要介於 5 分鐘和 7 天之間"},
		{Schedule{Kind: KindInterval, EveryMinutes: 7*24*60 + 1}, 0, "間隔要介於 5 分鐘和 7 天之間"},
		{Schedule{Kind: KindInterval, EveryMinutes: 30, ActiveFrom: "18:00", ActiveTo: "09:00"}, 0, "時段的結束要晚於開始"},
		{Schedule{Kind: KindInterval, EveryMinutes: 30, ActiveFrom: "09:00"}, 0, "時段要同時填開始和結束"},
		{Schedule{Kind: KindCron, Cron: "every day"}, 0, "cron 運算式看不懂"},
		{Schedule{Kind: KindCron, Cron: "CRON_TZ=UTC 0 9 * * *"}, 0, "時區請填在 timezone 欄位"},
		{Schedule{Kind: KindDaily, Time: "20:00"}, 999, "隨機延後要介於 0 到 240 分鐘"},
		{Schedule{Kind: KindDaily, Time: "20:00"}, -1, "隨機延後要介於 0 到 240 分鐘"},
		// 每 30 分鐘但最多晚一小時，會跳格，其實約一小時才跑一次。
		{Schedule{Kind: KindInterval, EveryMinutes: 30, Time: "09:00"}, 60, "少於 30 分鐘"},
		{Schedule{Kind: KindDaily, Time: "09:00", EndDate: "年底"}, 0, "結束日要像"},
	}
	for _, c := range cases {
		_, err := Normalize(c.s, c.jitter, tue0900)
		var se *ScheduleError
		if !errors.As(err, &se) || !strings.Contains(se.Message, c.msg) {
			t.Errorf("%+v jitter=%d：%v，要包含「%s」", c.s, c.jitter, err, c.msg)
		}
	}
	if _, err := Normalize(Schedule{Kind: KindInterval, EveryMinutes: 30, Time: "09:00"}, 20, tue0900); err != nil {
		t.Errorf("每 30 分鐘、延後 20 分鐘可以：%v", err)
	}
}

// LazyBoy test_weekdays_are_deduplicated_and_sorted
func TestWeekdaysAreDeduplicatedAndSorted(t *testing.T) {
	if got := norm(t, Schedule{Kind: KindWeekly, Time: "09:00", Weekdays: []int{5, 1, 5}}, 0).Weekdays; !slices.Equal(got, []int{1, 5}) {
		t.Fatalf("got %v", got)
	}
}

// LazyBoy test_monthly_routine_pins_its_day
func TestMonthlyPinsItsDay(t *testing.T) {
	if got := norm(t, Schedule{Kind: KindMonthly, Time: "09:00"}, 0).DayOfMonth; got != 13 {
		t.Fatalf("每月沒指定幾號要固定成今天（13）：%d", got)
	}
}

func TestNormalizeDropsUnusedFieldsAndDefaultsTimezone(t *testing.T) {
	got := norm(t, Schedule{Kind: KindDaily, Time: "09:00", Date: "2026-01-01", Weekdays: []int{1}, Cron: "x"}, 0)
	if got.Date != "" || got.Weekdays != nil || got.Cron != "" || got.Timezone != DefaultTimezone {
		t.Fatalf("got %+v", got)
	}
	trig := norm(t, Schedule{Kind: KindTrigger, Time: "09:00"}, 0)
	if trig.Time != "" {
		t.Fatalf("只靠網址觸發不需要時間：%+v", trig)
	}
	iv := norm(t, Schedule{Kind: KindInterval, EveryMinutes: 30, ActiveFrom: "9:00", ActiveTo: "18:00"}, 0)
	if iv.Time != "09:00" || iv.ActiveFrom != "09:00" {
		t.Fatalf("間隔型從時段開始算：%+v", iv)
	}
}

// LazyBoy test_next_occurrence_accepts_a_date_with_time_suffix
func TestOnceAcceptsADateWithTimeSuffix(t *testing.T) {
	s := Schedule{Kind: KindOnce, Time: "09:00", Date: "2999-12-01T10:00", Timezone: "Asia/Taipei"}
	got := slot(t, s, tue0900)
	if !got.Equal(time.Date(2999, 12, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %s", got.UTC())
	}
	if _, ok, _ := (Schedule{Kind: KindOnce, Time: "09:00", Date: "2020-01-01"}).Slot(tue0900); ok {
		t.Fatal("過去的單次排程沒有下一次")
	}
}

func TestSlotKinds(t *testing.T) {
	cases := []struct {
		name  string
		s     Schedule
		after time.Time
		want  time.Time
	}{
		{"每天，今天還沒到", Schedule{Kind: KindDaily, Time: "20:00"}, tue0900, at(taipei, 2026, 10, 13, 20, 0)},
		{"每天，剛好是現在就排明天", Schedule{Kind: KindDaily, Time: "09:00"}, tue0900, at(taipei, 2026, 10, 14, 9, 0)},
		{"平日，週五晚上排到週一", Schedule{Kind: KindWeekdays, Time: "09:00"}, at(taipei, 2026, 10, 16, 21, 0), at(taipei, 2026, 10, 19, 9, 0)},
		{"每週一、三", Schedule{Kind: KindWeekly, Time: "15:00", Weekdays: []int{1, 3}}, tue0900, at(taipei, 2026, 10, 14, 15, 0)},
		{"週末", Schedule{Kind: KindWeekly, Time: "10:00", Weekdays: []int{6, 7}}, tue0900, at(taipei, 2026, 10, 17, 10, 0)},
		{"每月 31 號跳過 11 月", Schedule{Kind: KindMonthly, Time: "09:00", DayOfMonth: 31}, at(taipei, 2026, 10, 31, 10, 0), at(taipei, 2026, 12, 31, 9, 0)},
		{"每 30 分鐘從 09:00 起算", Schedule{Kind: KindInterval, Time: "09:00", EveryMinutes: 30}, at(taipei, 2026, 10, 13, 9, 10), at(taipei, 2026, 10, 13, 9, 30)},
		{"每 90 分鐘，清晨的格子也存在", Schedule{Kind: KindInterval, Time: "09:00", EveryMinutes: 90}, at(taipei, 2026, 10, 13, 0, 10), at(taipei, 2026, 10, 13, 1, 30)},
		{"時段外跳到隔天開始", Schedule{Kind: KindInterval, Time: "09:00", EveryMinutes: 60, ActiveFrom: "09:00", ActiveTo: "18:00"}, at(taipei, 2026, 10, 13, 18, 5), at(taipei, 2026, 10, 14, 9, 0)},
		{"時段的結束含在內", Schedule{Kind: KindInterval, Time: "09:00", EveryMinutes: 60, ActiveFrom: "09:00", ActiveTo: "18:00"}, at(taipei, 2026, 10, 13, 17, 30), at(taipei, 2026, 10, 13, 18, 0)},
		{"cron 平日 9 點", Schedule{Kind: KindCron, Cron: "0 9 * * 1-5", Timezone: "Asia/Taipei"}, at(taipei, 2026, 10, 16, 10, 0), at(taipei, 2026, 10, 19, 9, 0)},
		{"cron 用自己的時區", Schedule{Kind: KindCron, Cron: "30 8 * * *", Timezone: "America/New_York"}, tue0900, at(newYork, 2026, 10, 13, 8, 30)},
		{"一次", Schedule{Kind: KindOnce, Date: "2026-10-20", Time: "07:15"}, tue0900, at(taipei, 2026, 10, 20, 7, 15)},
	}
	for _, c := range cases {
		s := c.s
		if s.Timezone == "" {
			s.Timezone = "Asia/Taipei"
		}
		if got := slot(t, s, c.after); !got.Equal(c.want) {
			t.Errorf("%s：got %s，要 %s", c.name, got, c.want)
		}
	}
}

func TestEndDateStopsRepeating(t *testing.T) {
	s := Schedule{Kind: KindDaily, Time: "09:00", EndDate: "2026-10-14", Timezone: "Asia/Taipei"}
	if got := slot(t, s, tue0900); !got.Equal(at(taipei, 2026, 10, 14, 9, 0)) {
		t.Fatalf("結束日當天還要跑：%s", got)
	}
	if _, ok, _ := s.Slot(at(taipei, 2026, 10, 14, 9, 0)); ok {
		t.Fatal("過了結束日就沒有下一次")
	}
	iv := Schedule{Kind: KindInterval, Time: "00:00", EveryMinutes: 60, EndDate: "2026-10-13", Timezone: "Asia/Taipei"}
	if _, ok, _ := iv.Slot(at(taipei, 2026, 10, 13, 23, 30)); ok {
		t.Fatal("間隔型過了結束日也停")
	}
	cr := Schedule{Kind: KindCron, Cron: "0 9 * * *", EndDate: "2026-10-13", Timezone: "Asia/Taipei"}
	if _, ok, _ := cr.Slot(tue0900); ok {
		t.Fatal("cron 過了結束日也停")
	}
}

// 夏令時間：不存在的時刻往後推、重複的時刻只跑一次；間隔型照實際經過的時間。
func TestDaylightSaving(t *testing.T) {
	ny := func(s Schedule) Schedule { s.Timezone = "America/New_York"; return s }
	// 2026-03-08 02:00 美東跳到 03:00：02:30 不存在，當天在 03:30（EDT）跑。
	spring := slot(t, ny(Schedule{Kind: KindDaily, Time: "02:30"}), at(newYork, 2026, 3, 7, 12, 0))
	if !spring.Equal(time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)) {
		t.Fatalf("春季：%s", spring.UTC())
	}
	// 2026-11-01 01:30 出現兩次：只跑一次，下一次是隔天。
	daily := ny(Schedule{Kind: KindDaily, Time: "01:30"})
	first := slot(t, daily, at(newYork, 2026, 10, 31, 12, 0))
	if first.In(newYork).Day() != 1 || first.In(newYork).Hour() != 1 {
		t.Fatalf("秋季第一次：%s", first)
	}
	second := slot(t, daily, first)
	if second.In(newYork).Day() != 2 || second.Sub(first) < 24*time.Hour {
		t.Fatalf("秋季同一天不能跑兩次：%s → %s", first, second)
	}
	// 每週也一樣。
	weekly := ny(Schedule{Kind: KindWeekly, Time: "02:30", Weekdays: []int{7}})
	if got := slot(t, weekly, at(newYork, 2026, 3, 7, 12, 0)); !got.Equal(spring) {
		t.Fatalf("每週日 02:30：%s", got.UTC())
	}
	// 間隔型：每 60 分鐘照實際時間走，跨過跳時不會停也不會連跑。
	iv := ny(Schedule{Kind: KindInterval, Time: "00:00", EveryMinutes: 60})
	prev := at(newYork, 2026, 3, 8, 0, 30)
	for range 4 {
		next := slot(t, iv, prev)
		if d := next.Sub(prev); d <= 0 || d > time.Hour {
			t.Fatalf("間隔型跨夏令時間：%s → %s", prev, next)
		}
		prev = next
	}
	// cron 在跳時那天也照樣有下一次。
	cr := slot(t, ny(Schedule{Kind: KindCron, Cron: "30 2 * * *"}), at(newYork, 2026, 3, 7, 12, 0))
	if cr.Before(at(newYork, 2026, 3, 8, 0, 0)) || cr.After(at(newYork, 2026, 3, 9, 3, 0)) {
		t.Fatalf("cron 春季：%s", cr)
	}
}

// LazyBoy test_jitter_runs_each_time_at_a_random_moment_after_the_set_time
func TestJitterRunsEachTimeAtARandomMomentAfterTheSetTime(t *testing.T) {
	s := norm(t, Schedule{Kind: KindDaily, Time: "20:00"}, 40)
	after := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC) // 台北 09:00
	want := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) // 台北 20:00
	r := rand.New(rand.NewPCG(1, 2))
	seen := map[time.Time]bool{}
	for range 200 {
		got, ok, err := s.Next(after, 40, r.Int64N)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if d := got.Sub(want); d < 0 || d > 40*time.Minute {
			t.Fatalf("延後超出 0～40 分鐘：%s", d)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatal("每次延後要不一樣")
	}
	// 上下限：亂數取到最小與最大。
	lo, _, _ := s.Next(after, 40, func(int64) int64 { return 0 })
	hi, _, _ := s.Next(after, 40, func(n int64) int64 { return n - 1 })
	if !lo.Equal(want) || !hi.Equal(want.Add(40*time.Minute)) {
		t.Fatalf("上下限：%s %s", lo, hi)
	}
	if got, _, _ := s.Next(after, 0, func(int64) int64 { panic("沒有延後不用亂數") }); !got.Equal(want) {
		t.Fatalf("沒有延後：%s", got)
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]Schedule{
		"每天上午 9:00":                     {Kind: KindDaily, Time: "09:00"},
		"平日下午 3:30":                     {Kind: KindWeekdays, Time: "15:30"},
		"每週一、三、五晚上 8:00":                {Kind: KindWeekly, Time: "20:00", Weekdays: []int{1, 3, 5}},
		"週末中午 12:00":                    {Kind: KindWeekly, Time: "12:00", Weekdays: []int{6, 7}},
		"每月 5 號凌晨 12:30":                {Kind: KindMonthly, Time: "00:30", DayOfMonth: 5},
		"每 30 分鐘（上午 9:00～晚上 6:00）":      {Kind: KindInterval, Time: "09:00", EveryMinutes: 30, ActiveFrom: "09:00", ActiveTo: "18:00"},
		"每小時":                           {Kind: KindInterval, Time: "00:00", EveryMinutes: 60},
		"每 2 小時，到 12月31日":               {Kind: KindInterval, Time: "00:00", EveryMinutes: 120, EndDate: "2026-12-31"},
		"10月20日（週二） 上午 7:15":            {Kind: KindOnce, Date: "2026-10-20", Time: "07:15"},
		"cron「0 9 * * 1-5」（Asia/Tokyo）": {Kind: KindCron, Cron: "0 9 * * 1-5", Timezone: "Asia/Tokyo"},
		"收到網址觸發時":                       {Kind: KindTrigger},
	}
	for want, s := range cases {
		if got := Describe(s); got != want {
			t.Errorf("Describe(%+v) = %q，要 %q", s, got, want)
		}
	}
	for _, c := range []struct {
		h, m int
		want string
	}{{0, 5, "凌晨 12:05"}, {9, 0, "上午 9:00"}, {12, 0, "中午 12:00"}, {15, 0, "下午 3:00"}, {21, 45, "晚上 9:45"}} {
		if got := ClockText(c.h, c.m); got != c.want || strings.ContainsAny(got, "AP") {
			t.Errorf("ClockText(%d,%d) = %s", c.h, c.m, got)
		}
	}
}
