package routines

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Bot 電腦主機或容器沒有 zoneinfo 時也認得時區

	"github.com/robfig/cron/v3"

	"xbot/internal/limits"
)

// DefaultTimezone 是沒指定時區時用的時區。LazyBoy server/scheduling.py schedule_zone（Asia/Taipei）
const DefaultTimezone = "Asia/Taipei"

// 排程種類（RT-1）。LazyBoy FREQUENCIES 加上 cron。
const (
	KindOnce     = "once"     // 一次：date＋time
	KindDaily    = "daily"    // 每天 time
	KindWeekdays = "weekdays" // 平日（週一到週五）time
	KindWeekly   = "weekly"   // 每週的 weekdays（1＝週一…7＝週日）time
	KindMonthly  = "monthly"  // 每月 day_of_month 號 time
	KindInterval = "interval" // 每 every_minutes 分鐘，從 time 起算；可設時段 active_from～active_to 與結束日 end_date
	KindCron     = "cron"     // cron 運算式（5 欄）＋時區
	KindTrigger  = "trigger"  // 沒有排程，只靠網址觸發（或手動）
)

// Kinds 是所有排程種類。
var Kinds = []string{KindOnce, KindDaily, KindWeekdays, KindWeekly, KindMonthly, KindInterval, KindCron, KindTrigger}

// Schedule 是一個定時任務的排程（存在 routine.schedule）。
type Schedule struct {
	Kind         string `json:"kind" enum:"once,daily,weekdays,weekly,monthly,interval,cron,trigger" desc:"once 一次、daily 每天、weekdays 平日、weekly 每週幾、monthly 每月幾號、interval 每隔幾分鐘、cron、trigger 只靠網址觸發"`
	Time         string `json:"time,omitempty" desc:"幾點，24 小時制 HH:MM，例：09:00"`
	Date         string `json:"date,omitempty" desc:"once：哪一天 YYYY-MM-DD"`
	Weekdays     []int  `json:"weekdays,omitempty" desc:"weekly：星期幾，1＝週一…7＝週日"`
	DayOfMonth   int    `json:"day_of_month,omitempty" desc:"monthly：每月幾號（1～31；沒有這天的月份略過）"`
	EveryMinutes int    `json:"every_minutes,omitempty" desc:"interval：每幾分鐘（5 分鐘～7 天）"`
	ActiveFrom   string `json:"active_from,omitempty" desc:"interval：只在這段時間內跑，開始 HH:MM"`
	ActiveTo     string `json:"active_to,omitempty" desc:"interval：只在這段時間內跑，結束 HH:MM（含）"`
	EndDate      string `json:"end_date,omitempty" desc:"重複的排程跑到哪一天為止 YYYY-MM-DD（含這天）"`
	Cron         string `json:"cron,omitempty" desc:"cron：5 欄運算式，例：0 9 * * 1-5"`
	Timezone     string `json:"timezone,omitempty" desc:"時區，例：Asia/Taipei；不填用 Asia/Taipei"`
}

// ScheduleError 是排程設定的問題（白話中文）。
type ScheduleError struct {
	Field, Message string
}

func (e *ScheduleError) Error() string { return e.Message }

func bad(field, msg string) error { return &ScheduleError{Field: field, Message: msg} }

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Location 回傳排程的時區。
func (s Schedule) Location() (*time.Location, error) {
	name := s.Timezone
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return nil, bad("timezone", "無效的時區")
	}
	return loc, nil
}

// ParseClock 讀「09:00」「9:05」「09:00:00」。LazyBoy parse_time
func ParseClock(v string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, bad("time", "時間格式要像 09:00")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, bad("time", "時間格式要像 09:00")
	}
	return h, m, nil
}

func clock(h, m int) string { return fmt.Sprintf("%02d:%02d", h, m) }

func parseDate(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if len(v) > 10 {
		v = v[:10] // 「2999-12-01T10:00」也接受（LazyBoy 取前 10 字）
	}
	return time.Parse("2006-01-02", v)
}

// Normalize 檢查並整理排程與隨機延後（分鐘）。LazyBoy normalize_schedule
// now 用來固定「每月幾號」（沒給時用今天）。
func Normalize(s Schedule, jitter int, now time.Time) (Schedule, error) {
	s.Kind = strings.TrimSpace(s.Kind)
	if s.Kind == "" {
		return s, bad("kind", "請指定重複方式和時間")
	}
	if !slices.Contains(Kinds, s.Kind) {
		return s, bad("kind", "無效的排程頻率")
	}
	if s.Timezone == "" {
		s.Timezone = DefaultTimezone
	}
	loc, err := s.Location()
	if err != nil {
		return s, err
	}
	if s.Kind == KindInterval && s.Time == "" {
		s.Time = s.ActiveFrom
	}
	needsTime := s.Kind != KindCron && s.Kind != KindTrigger && s.Kind != KindInterval
	if s.Time == "" && needsTime {
		return s, bad("time", "請指定重複方式和時間")
	}
	if s.Time != "" {
		h, m, err := ParseClock(s.Time)
		if err != nil {
			return s, err
		}
		s.Time = clock(h, m)
	}
	// 用不到的欄位清掉，存起來才乾淨。
	keep := Schedule{Kind: s.Kind, Time: s.Time, Timezone: s.Timezone}
	switch s.Kind {
	case KindTrigger:
		keep.Time = ""
	case KindOnce:
		d, err := parseDate(s.Date)
		if err != nil {
			return s, bad("date", "單次排程必須指定日期（YYYY-MM-DD）")
		}
		keep.Date = d.Format("2006-01-02")
	case KindWeekly:
		if len(s.Weekdays) == 0 {
			return s, bad("weekdays", "每週排程至少要選一個星期")
		}
		days := slices.Clone(s.Weekdays)
		for _, d := range days {
			if d < 1 || d > 7 {
				return s, bad("weekdays", "星期必須介於 1（週一）和 7（週日）")
			}
		}
		slices.Sort(days)
		keep.Weekdays = slices.Compact(days)
	case KindMonthly:
		day := s.DayOfMonth
		if day == 0 {
			day = now.In(loc).Day() // 現在就固定，之後重算才不會跟著漂（LazyBoy）
		}
		if day < 1 || day > 31 {
			return s, bad("day_of_month", "日期必須介於 1 和 31")
		}
		keep.DayOfMonth = day
	case KindInterval:
		every := time.Duration(s.EveryMinutes) * time.Minute
		if every < limits.RoutineIntervalMin || every > limits.RoutineIntervalMax {
			return s, bad("every_minutes", fmt.Sprintf("間隔要介於 %d 分鐘和 7 天之間", int(limits.RoutineIntervalMin/time.Minute)))
		}
		keep.EveryMinutes = s.EveryMinutes
		if keep.Time == "" {
			keep.Time = "00:00"
		}
		if (s.ActiveFrom == "") != (s.ActiveTo == "") {
			return s, bad("active_from", "時段要同時填開始和結束")
		}
		if s.ActiveFrom != "" {
			fh, fm, err := ParseClock(s.ActiveFrom)
			if err != nil {
				return s, bad("active_from", "時段的時間格式要像 09:00")
			}
			th, tm, err := ParseClock(s.ActiveTo)
			if err != nil {
				return s, bad("active_to", "時段的時間格式要像 18:00")
			}
			if th*60+tm <= fh*60+fm {
				return s, bad("active_to", "時段的結束要晚於開始")
			}
			keep.ActiveFrom, keep.ActiveTo = clock(fh, fm), clock(th, tm)
		}
	case KindCron:
		expr := strings.Join(strings.Fields(s.Cron), " ")
		if expr == "" {
			return s, bad("cron", "請填 cron 運算式，例：0 9 * * 1-5")
		}
		if strings.HasPrefix(strings.ToUpper(expr), "CRON_TZ=") || strings.HasPrefix(strings.ToUpper(expr), "TZ=") {
			return s, bad("cron", "時區請填在 timezone 欄位")
		}
		if _, err := cronParser.Parse(expr); err != nil {
			return s, bad("cron", "cron 運算式看不懂（5 欄：分 時 日 月 星期）")
		}
		keep.Cron, keep.Time = expr, ""
	}
	if s.EndDate != "" && s.Kind != KindOnce && s.Kind != KindTrigger {
		d, err := parseDate(s.EndDate)
		if err != nil {
			return s, bad("end_date", "結束日要像 2026-12-31")
		}
		keep.EndDate = d.Format("2006-01-02")
	}
	if jitter < 0 || jitter > limits.RoutineJitterMaxMinutes {
		return s, bad("jitter_minutes", fmt.Sprintf("隨機延後要介於 0 到 %d 分鐘", limits.RoutineJitterMaxMinutes))
	}
	// 下一次是「實際跑完之後」的下一格：延後和間隔一樣長會跳格，「每 30 分鐘、最多晚一小時」其實約一小時才跑一次。
	if jitter > 0 && keep.Kind == KindInterval && jitter >= keep.EveryMinutes {
		return s, bad("jitter_minutes", fmt.Sprintf("每 %d 分鐘執行時，隨機延後要少於 %d 分鐘", keep.EveryMinutes, keep.EveryMinutes))
	}
	return keep, nil
}

// Slot 回傳 after 之後的下一個排定時間（不含隨機延後）；沒有下一次（單次已過、過了結束日、只靠網址觸發）時 ok 是 false。
// LazyBoy scheduled_slot
func (s Schedule) Slot(after time.Time) (time.Time, bool, error) {
	loc, err := s.Location()
	if err != nil {
		return time.Time{}, false, err
	}
	local := after.In(loc)
	var end time.Time // 結束日的隔天 00:00（不含）
	if s.EndDate != "" {
		d, err := parseDate(s.EndDate)
		if err != nil {
			return time.Time{}, false, bad("end_date", "結束日要像 2026-12-31")
		}
		end = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	}
	within := func(t time.Time) (time.Time, bool, error) {
		if !end.IsZero() && !t.Before(end) {
			return time.Time{}, false, nil
		}
		return t, true, nil
	}
	switch s.Kind {
	case KindTrigger:
		return time.Time{}, false, nil
	case KindCron:
		sched, err := cronParser.Parse(s.Cron)
		if err != nil {
			return time.Time{}, false, bad("cron", "cron 運算式看不懂（5 欄：分 時 日 月 星期）")
		}
		next := sched.Next(local)
		if next.IsZero() {
			return time.Time{}, false, nil
		}
		return within(next)
	}
	h, m, err := ParseClock(firstNonEmpty(s.Time, "00:00"))
	if err != nil {
		return time.Time{}, false, err
	}
	switch s.Kind {
	case KindInterval:
		every := time.Duration(max(s.EveryMinutes, int(limits.RoutineIntervalMin/time.Minute))) * time.Minute
		// 從前一天的起始時間開始每 N 分鐘一格，清晨的格子才存在（LazyBoy）。
		anchor := time.Date(local.Year(), local.Month(), local.Day()-1, h, m, 0, 0, loc)
		steps := local.Sub(anchor)/every + 1
		next := anchor.Add(steps * every)
		if s.ActiveFrom != "" {
			fh, fm, _ := ParseClock(s.ActiveFrom)
			th, tm, _ := ParseClock(s.ActiveTo)
			from, to := fh*60+fm, th*60+tm
			// 最多看 8 天的格子；時段外的跳過。
			for i := 0; ; i++ {
				lt := next.In(loc)
				if mins := lt.Hour()*60 + lt.Minute(); mins >= from && mins <= to {
					break
				}
				if i > int(8*24*time.Hour/every) {
					return time.Time{}, false, nil
				}
				next = next.Add(every)
			}
		}
		return within(next)
	case KindOnce:
		d, err := parseDate(s.Date)
		if err != nil {
			return time.Time{}, false, bad("date", "單次排程必須指定日期")
		}
		cand := wallTime(d.Year(), d.Month(), d.Day(), h, m, loc)
		if cand.After(after) {
			return cand, true, nil
		}
		return time.Time{}, false, nil
	}
	day := s.DayOfMonth
	if day == 0 {
		day = local.Day()
	}
	for offset := 0; offset < 367*2; offset++ {
		y, mo, d := local.Date()
		date := time.Date(y, mo, d+offset, 12, 0, 0, 0, loc) // 中午算日期，不受夏令時間影響
		if !end.IsZero() && !date.Before(end) {
			return time.Time{}, false, nil
		}
		wd := isoWeekday(date)
		switch s.Kind {
		case KindWeekdays:
			if wd > 5 {
				continue
			}
		case KindWeekly:
			if len(s.Weekdays) == 0 {
				return time.Time{}, false, bad("weekdays", "每週排程至少要選一個星期")
			}
			if !slices.Contains(s.Weekdays, wd) {
				continue
			}
		case KindMonthly:
			if date.Day() != day {
				continue
			}
		case KindDaily:
		default:
			return time.Time{}, false, bad("kind", "無效的排程頻率")
		}
		cand := wallTime(date.Year(), date.Month(), date.Day(), h, m, loc)
		if !cand.After(after) {
			continue
		}
		return cand, true, nil
	}
	return time.Time{}, false, errors.New("找不到有效的下次執行時間")
}

// Next 回傳下一次要跑的時間：下一個排定時間，再加上 0～jitter 分鐘的隨機延後。LazyBoy next_occurrence
// rnd(n) 回傳 [0, n) 的亂數（測試可以固定）。
func (s Schedule) Next(after time.Time, jitter int, rnd func(n int64) int64) (time.Time, bool, error) {
	slot, ok, err := s.Slot(after)
	if err != nil || !ok || jitter <= 0 {
		return slot, ok, err
	}
	return slot.Add(time.Duration(rnd(int64(jitter)*60+1)) * time.Second), true, nil
}

// wallTime 是 loc 當地 y-mo-d h:mi 的時刻。夏令時間跳過的時刻（例：美東 02:30）用跳之前的時差，
// 落在跳過之後（03:30），和 LazyBoy（Python zoneinfo fold=0）一樣；重複的時刻取 Go 給的那一個，一天只跑一次。
func wallTime(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	t := time.Date(y, mo, d, h, mi, 0, 0, loc)
	if t.Hour() == h && t.Minute() == mi {
		return t
	}
	_, off := t.Add(-3 * time.Hour).Zone()
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC).Add(-time.Duration(off) * time.Second).In(loc)
}

func isoWeekday(t time.Time) int {
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}
	return 7
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ── 給人看的寫法 ──────────────────────────────────────

var weekdayNames = []string{"", "一", "二", "三", "四", "五", "六", "日"}

// ClockText 把 24 小時制寫成「上午 9:00」「下午 3:30」（不出現 AM/PM，H02）。
func ClockText(h, m int) string {
	period, hh := "上午", h
	switch {
	case h < 5:
		period = "凌晨"
		if h == 0 {
			hh = 12
		}
	case h < 12:
	case h == 12:
		period = "中午"
	case h < 18:
		period, hh = "下午", h-12
	default:
		period, hh = "晚上", h-12
	}
	return fmt.Sprintf("%s %d:%02d", period, hh, m)
}

func clockText(v string) string {
	h, m, err := ParseClock(v)
	if err != nil {
		return v
	}
	return ClockText(h, m)
}

// DateText 寫成「10月20日（週二）」。
func DateText(t time.Time) string {
	return fmt.Sprintf("%d月%d日（週%s）", int(t.Month()), t.Day(), weekdayNames[isoWeekday(t)])
}

// Describe 把排程寫成一句白話，例：「每天上午 9:00」「平日上午 9:00」「每 30 分鐘（上午 9:00～晚上 6:00）」。
func Describe(s Schedule) string {
	var b strings.Builder
	switch s.Kind {
	case KindOnce:
		if d, err := parseDate(s.Date); err == nil {
			b.WriteString(DateText(d) + " ")
		}
		b.WriteString(clockText(s.Time))
	case KindDaily:
		b.WriteString("每天" + clockText(s.Time))
	case KindWeekdays:
		b.WriteString("平日" + clockText(s.Time))
	case KindWeekly:
		if slices.Equal(s.Weekdays, []int{6, 7}) {
			b.WriteString("週末")
		} else {
			names := make([]string, 0, len(s.Weekdays))
			for _, d := range s.Weekdays {
				if d >= 1 && d <= 7 {
					names = append(names, weekdayNames[d])
				}
			}
			b.WriteString("每週" + strings.Join(names, "、"))
		}
		b.WriteString(clockText(s.Time))
	case KindMonthly:
		fmt.Fprintf(&b, "每月 %d 號%s", s.DayOfMonth, clockText(s.Time))
	case KindInterval:
		b.WriteString(EveryText(s.EveryMinutes))
		if s.ActiveFrom != "" {
			fmt.Fprintf(&b, "（%s～%s）", clockText(s.ActiveFrom), clockText(s.ActiveTo))
		}
	case KindCron:
		b.WriteString("cron「" + s.Cron + "」")
	case KindTrigger:
		b.WriteString("收到網址觸發時")
	default:
		b.WriteString(s.Kind)
	}
	if s.EndDate != "" {
		if d, err := parseDate(s.EndDate); err == nil {
			fmt.Fprintf(&b, "，到 %d月%d日", int(d.Month()), d.Day())
		}
	}
	if s.Timezone != "" && s.Timezone != DefaultTimezone && s.Kind != KindTrigger {
		b.WriteString("（" + s.Timezone + "）")
	}
	return b.String()
}

// EveryText 寫成「每 30 分鐘」「每小時」「每 2 小時」「每 1 天」。
func EveryText(minutes int) string {
	switch {
	case minutes == 60:
		return "每小時"
	case minutes%(24*60) == 0:
		return fmt.Sprintf("每 %d 天", minutes/(24*60))
	case minutes%60 == 0:
		return fmt.Sprintf("每 %d 小時", minutes/60)
	}
	return fmt.Sprintf("每 %d 分鐘", minutes)
}
