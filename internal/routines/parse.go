package routines

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"xbot/internal/limits"
)

// 一句話建立定時任務（PRD §21：15 個欄位 → 1 句話＋確認；H02、L17）。
// 先用規則解析常見的中文說法；快判（§20 用途 9、X05）之後可以在這之前先試，失敗再退回這裡。

// BotRef 是解析時認得的 Bot（名字出現在句子裡就指派給它）。
type BotRef struct {
	ID, Name string
}

// Parsed 是從一句話解析出來的草稿。
type Parsed struct {
	BotID    string   `json:"bot_id"`
	BotName  string   `json:"bot_name"`
	Title    string   `json:"title"`
	Prompt   string   `json:"prompt"`
	Schedule Schedule `json:"schedule"`
	// Missing 是還缺的：time（沒看到時間）、task（沒看到要做什麼）、bot（沒有 Bot）。
	Missing []string `json:"missing"`
	// Notes 是解析時做的假設，確認卡上要讓你看到（例：每隔一段時間最短 5 分鐘，已改成 5 分鐘）。
	Notes []string `json:"notes"`
}

const numPat = `([0-9]{1,2}|[零〇一二兩两三四五六七八九十]{1,3})`

var (
	reCron      = regexp.MustCompile(`(?i)cron\s*[:：]?\s*((?:[0-9*/,\-A-Za-z?]+\s+){4}[0-9*/,\-A-Za-z?]+)`)
	reInterval  = regexp.MustCompile(`每\s*(?:隔\s*)?(半|` + numPat[1:len(numPat)-1] + `)?\s*個?\s*(分鐘|分|小時|鐘頭)`)
	reWindow    = regexp.MustCompile(`(?:在\s*|從\s*)?(凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上)?\s*` + numPat + `\s*(?:點|:00|：00)\s*(?:到|至|-|~|～|—)\s*(凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上)?\s*` + numPat + `\s*(?:點|:00|：00)(?:\s*之間)?`)
	reWorkHours = regexp.MustCompile(`上班時間(?:內)?`)
	reWeekRange = regexp.MustCompile(`每?\s*(?:週|周|星期|禮拜)\s*([一二三四五六日天1-7])\s*(?:到|至|-|~|～)\s*(?:週|周|星期|禮拜)?\s*([一二三四五六日天1-7])`)
	reWeekdays  = regexp.MustCompile(`(?:每個?\s*)?(?:平日|工作日|上班日|工作天)`)
	reWeekend   = regexp.MustCompile(`(?:每個?\s*|每逢\s*)?週末`)
	reWeekly    = regexp.MustCompile(`每\s*個?\s*(?:週|周|星期|禮拜)\s*([一二三四五六日天1-7](?:\s*[、,，和及跟與]\s*(?:週|周|星期|禮拜)?\s*[一二三四五六日天1-7])*)`)
	reWeekBare  = regexp.MustCompile(`每\s*個?\s*(?:週|周|星期|禮拜)`)
	reMonthly   = regexp.MustCompile(`每\s*個?\s*月\s*(?:的\s*)?` + numPat + `\s*(?:號|日)`)
	reMonthHead = regexp.MustCompile(`每\s*個?\s*月\s*初`)
	reDaily     = regexp.MustCompile(`每個?早上|每個?晚上|每天|每日|天天|每晚|每早`)
	reEndDate   = regexp.MustCompile(`(?:直到|到|至)\s*` + numPat + `\s*月\s*(?:` + numPat + `\s*(?:號|日)|(底))\s*(?:為止|結束)?`)
	reDate      = regexp.MustCompile(numPat + `\s*月\s*` + numPat + `\s*(?:號|日)`)
	reSlashDate = regexp.MustCompile(`\b([0-9]{1,2})/([0-9]{1,2})\b`)
	reRelDay    = regexp.MustCompile(`大後天|後天|明天|明早|明晚|今天|今晚|今早`)
	reNextWeek  = regexp.MustCompile(`(下|這|本)\s*(?:個)?\s*(?:週|周|星期|禮拜)\s*([一二三四五六日天1-7])`)
	reClock     = regexp.MustCompile(`(凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上|晚間|夜裡|半夜)?\s*` + numPat + `\s*(?:點|時)\s*(?:(半)|([0-9]{1,2})\s*分?|([零〇一二兩两三四五六七八九十]{1,3})\s*分)?`)
	reColon     = regexp.MustCompile(`(凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上|晚間|夜裡|半夜)?\s*([0-9]{1,2})\s*[:：]\s*([0-9]{2})`)
	rePeriod    = regexp.MustCompile(`凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上|晚間|夜裡|半夜`)
	reLead      = regexp.MustCompile(`^(?:請|麻煩|幫我|替我|要|會|就|都|的|在|，|,|、|。|\s)+`)
	reTrail     = regexp.MustCompile(`(?:，|,|、|。|！|!|\s|的|吧|喔|哦)+$`)
)

var cnDigits = map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '兩': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// cnNumber 讀「9」「十二」「二十」「兩」「三十」；讀不懂回 -1。
func cnNumber(s string) int {
	if s == "" {
		return -1
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	runes := []rune(s)
	if i := slices.Index(runes, '十'); i >= 0 {
		tens, ones := 1, 0
		if i > 0 {
			v, ok := cnDigits[runes[0]]
			if !ok || i != 1 {
				return -1
			}
			tens = v
		}
		if i+1 < len(runes) {
			v, ok := cnDigits[runes[i+1]]
			if !ok || i+2 != len(runes) {
				return -1
			}
			ones = v
		}
		return tens*10 + ones
	}
	if len(runes) == 1 {
		if v, ok := cnDigits[runes[0]]; ok {
			return v
		}
	}
	return -1
}

func weekdayOf(s string) int {
	switch s {
	case "一", "1":
		return 1
	case "二", "2":
		return 2
	case "三", "3":
		return 3
	case "四", "4":
		return 4
	case "五", "5":
		return 5
	case "六", "6":
		return 6
	case "日", "天", "7":
		return 7
	}
	return 0
}

// normalizeText 把全形數字與冒號換成半形。
func normalizeText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return '0' + (r - '０')
		case r == '：':
			return ':'
		case r == '　':
			return ' '
		}
		return r
	}, s)
}

// applyPeriod 依「下午」「晚上」等把小時換成 24 小時制。
func applyPeriod(period string, h int) int {
	switch period {
	case "下午", "傍晚", "晚上", "晚間", "夜裡", "今晚", "明晚", "每晚":
		if h < 12 {
			return h + 12
		}
	case "中午":
		if h < 6 {
			return h + 12
		}
	case "凌晨", "半夜":
		if h == 12 {
			return 0
		}
	case "早上", "早晨", "上午", "清晨":
		if h == 12 {
			return 0
		}
	}
	return h
}

type cut struct{ start, end int }

// text 是正在解析的句子；用過的片段記在 cuts，剩下的就是「要做什麼」。
type text struct {
	s    string
	cuts []cut
}

func (t *text) used(i, j int) bool {
	for _, c := range t.cuts {
		if i < c.end && j > c.start {
			return true
		}
	}
	return false
}

// find 找第一個還沒被用過的符合片段，記成用過並回傳子群組（找不到回 nil）。
func (t *text) find(re *regexp.Regexp) []string {
	for _, loc := range re.FindAllStringSubmatchIndex(t.s, -1) {
		if t.used(loc[0], loc[1]) {
			continue
		}
		t.cuts = append(t.cuts, cut{loc[0], loc[1]})
		out := make([]string, len(loc)/2)
		for i := range out {
			if loc[2*i] >= 0 {
				out[i] = t.s[loc[2*i]:loc[2*i+1]]
			}
		}
		return out
	}
	return nil
}

func (t *text) rest() string {
	slices.SortFunc(t.cuts, func(a, b cut) int { return a.start - b.start })
	var b strings.Builder
	pos := 0
	for _, c := range t.cuts {
		if c.start > pos {
			b.WriteString(t.s[pos:c.start])
			b.WriteByte(' ')
		}
		pos = max(pos, c.end)
	}
	b.WriteString(t.s[pos:])
	return b.String()
}

// Parse 把一句話解析成定時任務草稿。now 是現在（決定「明天」「下週一」與單次排程的日期），loc 是時區。
// bots 是可以指派的 Bot（依順序，第一個是預設，H02「沒提到預設小秘」）。
func Parse(sentence string, now time.Time, loc *time.Location, bots []BotRef) Parsed {
	p := Parsed{Missing: []string{}, Notes: []string{}}
	t := &text{s: normalizeText(strings.TrimSpace(sentence))}
	local := now.In(loc)
	p.Schedule.Timezone = loc.String()

	// 誰做：句子裡出現最長的 Bot 名字。
	var bot *BotRef
	for i := range bots {
		b := &bots[i]
		if b.Name == "" || !strings.Contains(t.s, b.Name) {
			continue
		}
		if bot == nil || utf8.RuneCountInString(b.Name) > utf8.RuneCountInString(bot.Name) {
			bot = b
		}
	}
	if bot != nil {
		re := regexp.MustCompile(`(?:請|叫|讓|要|由|找|跟|和|與)?\s*` + regexp.QuoteMeta(bot.Name) + `\s*(?:來|去|幫我|幫忙)?`)
		t.find(re)
	} else if len(bots) > 0 {
		bot = &bots[0]
	}
	if bot != nil {
		p.BotID, p.BotName = bot.ID, bot.Name
	} else {
		p.Missing = append(p.Missing, "bot")
	}

	s := &p.Schedule
	period := ""
	// cron 運算式原樣照用。
	if m := t.find(reCron); m != nil {
		s.Kind, s.Cron = KindCron, strings.Join(strings.Fields(m[1]), " ")
	}
	// 每隔一段時間。
	if s.Kind == "" {
		if m := t.find(reInterval); m != nil {
			n := 1
			switch {
			case m[1] == "半":
				n = 0
			case m[1] != "":
				n = cnNumber(m[1])
			}
			minutes := n
			if m[2] == "小時" || m[2] == "鐘頭" {
				minutes = n * 60
				if m[1] == "半" {
					minutes = 30
				}
			} else if m[1] == "半" {
				minutes = 30
			}
			minMin, maxMin := int(limits.RoutineIntervalMin/time.Minute), int(limits.RoutineIntervalMax/time.Minute)
			switch {
			case minutes < minMin:
				p.Notes = append(p.Notes, fmt.Sprintf("每隔一段時間最短 %d 分鐘，已改成 %d 分鐘", minMin, minMin))
				minutes = minMin
			case minutes > maxMin:
				p.Notes = append(p.Notes, "每隔一段時間最長 7 天，已改成 7 天")
				minutes = maxMin
			}
			s.Kind, s.EveryMinutes = KindInterval, minutes
			if m := t.find(reWindow); m != nil {
				fh, th := cnNumber(m[2]), cnNumber(m[4])
				if fh >= 0 && th >= 0 {
					fh, th = applyPeriod(m[1], fh), applyPeriod(firstNonEmpty(m[3], m[1]), th)
					if th <= fh && th < 12 && m[3] == "" {
						th += 12 // 「9 點到 6 點」是到晚上 6 點
					}
					if fh < 24 && th < 24 && th > fh {
						s.ActiveFrom, s.ActiveTo = clock(fh, 0), clock(th, 0)
					}
				}
			} else if t.find(reWorkHours) != nil {
				s.ActiveFrom, s.ActiveTo = "09:00", "18:00"
			}
		}
	}
	// 哪幾天。
	if s.Kind == "" {
		if m := t.find(reWeekRange); m != nil {
			a, b := weekdayOf(m[1]), weekdayOf(m[2])
			if a == 1 && b == 5 {
				s.Kind = KindWeekdays
			} else if a > 0 && b >= a {
				s.Kind = KindWeekly
				for d := a; d <= b; d++ {
					s.Weekdays = append(s.Weekdays, d)
				}
			}
		}
	}
	if s.Kind == "" && t.find(reWeekdays) != nil {
		s.Kind = KindWeekdays
	}
	if s.Kind == "" && t.find(reWeekend) != nil {
		s.Kind, s.Weekdays = KindWeekly, []int{6, 7}
	}
	if s.Kind == "" {
		if m := t.find(reWeekly); m != nil {
			s.Kind = KindWeekly
			for _, r := range m[1] {
				if d := weekdayOf(string(r)); d > 0 && !slices.Contains(s.Weekdays, d) {
					s.Weekdays = append(s.Weekdays, d)
				}
			}
			slices.Sort(s.Weekdays)
		} else if t.find(reWeekBare) != nil {
			s.Kind, s.Weekdays = KindWeekly, []int{isoWeekday(local)}
			p.Notes = append(p.Notes, "沒寫星期幾，當成每週"+weekdayNames[isoWeekday(local)])
		}
	}
	if s.Kind == "" {
		if m := t.find(reMonthly); m != nil {
			if d := cnNumber(m[1]); d >= 1 && d <= 31 {
				s.Kind, s.DayOfMonth = KindMonthly, d
			}
		} else if t.find(reMonthHead) != nil {
			s.Kind, s.DayOfMonth = KindMonthly, 1
		}
	}
	if s.Kind == "" {
		if m := t.find(reDaily); m != nil {
			s.Kind = KindDaily
			switch {
			case strings.Contains(m[0], "晚"):
				period = "晚上"
			case strings.Contains(m[0], "早"):
				period = "早上"
			}
		}
	}
	// 跑到哪一天為止（重複的排程才有）。
	if s.Kind != "" && s.Kind != KindOnce {
		if m := t.find(reEndDate); m != nil {
			mo := cnNumber(m[1])
			if mo >= 1 && mo <= 12 {
				var d time.Time
				if m[3] != "" {
					d = time.Date(local.Year(), time.Month(mo)+1, 0, 0, 0, 0, 0, loc) // 月底
				} else if day := cnNumber(m[2]); day >= 1 && day <= 31 {
					d = time.Date(local.Year(), time.Month(mo), day, 0, 0, 0, 0, loc)
				}
				if !d.IsZero() {
					if d.Before(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)) {
						d = d.AddDate(1, 0, 0)
					}
					s.EndDate = d.Format("2006-01-02")
				}
			}
		}
	}
	// 單次：哪一天。
	var onceDate time.Time
	if s.Kind == "" {
		today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		if m := t.find(reNextWeek); m != nil {
			wd := weekdayOf(m[2])
			monday := today.AddDate(0, 0, 1-isoWeekday(today))
			if m[1] == "下" {
				monday = monday.AddDate(0, 0, 7)
			}
			onceDate = monday.AddDate(0, 0, wd-1)
		} else if m := t.find(reRelDay); m != nil {
			switch m[0] {
			case "今天", "今早":
				onceDate = today
			case "今晚":
				onceDate, period = today, "晚上"
			case "明天":
				onceDate = today.AddDate(0, 0, 1)
			case "明早":
				onceDate, period = today.AddDate(0, 0, 1), "早上"
			case "明晚":
				onceDate, period = today.AddDate(0, 0, 1), "晚上"
			case "後天":
				onceDate = today.AddDate(0, 0, 2)
			case "大後天":
				onceDate = today.AddDate(0, 0, 3)
			}
		} else if m := t.find(reDate); m != nil {
			mo, d := cnNumber(m[1]), cnNumber(m[2])
			if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
				onceDate = time.Date(local.Year(), time.Month(mo), d, 0, 0, 0, 0, loc)
				if onceDate.Before(today) {
					onceDate = onceDate.AddDate(1, 0, 0)
				}
			}
		} else if m := t.find(reSlashDate); m != nil {
			mo, _ := strconv.Atoi(m[1])
			d, _ := strconv.Atoi(m[2])
			if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
				onceDate = time.Date(local.Year(), time.Month(mo), d, 0, 0, 0, 0, loc)
				if onceDate.Before(today) {
					onceDate = onceDate.AddDate(1, 0, 0)
				}
			}
		}
		if !onceDate.IsZero() {
			s.Kind, s.Date = KindOnce, onceDate.Format("2006-01-02")
		}
	}
	// 幾點。
	hour, minute := -1, 0
	if s.Kind != KindCron {
		if m := t.find(reColon); m != nil {
			h, _ := strconv.Atoi(m[2])
			mi, _ := strconv.Atoi(m[3])
			if h < 24 && mi < 60 {
				hour, minute = applyPeriod(firstNonEmpty(m[1], period), h), mi
			}
		} else if m := t.find(reClock); m != nil {
			h := cnNumber(m[2])
			mi := 0
			switch {
			case m[3] == "半":
				mi = 30
			case m[4] != "":
				mi, _ = strconv.Atoi(m[4])
			case m[5] != "":
				mi = cnNumber(m[5])
			}
			if h >= 0 && h <= 24 && mi >= 0 && mi < 60 {
				if h == 24 {
					h = 0
				}
				explicit := firstNonEmpty(m[1], period)
				if explicit == "" && h >= 1 && h <= 6 {
					// 沒寫上午下午：1～6 點多半是下午（「三點開會」）。
					p.Notes = append(p.Notes, fmt.Sprintf("沒寫上午或下午，當成%s", ClockText(h+12, mi)))
					h += 12
				}
				hour, minute = applyPeriod(explicit, h), mi
			}
		}
		// 只寫「早上」「晚上」沒寫幾點：拿掉這個詞，不當成要做的事。
		if hour < 0 {
			t.find(rePeriod)
		}
	}
	if hour >= 0 {
		switch s.Kind {
		case KindInterval:
			if s.ActiveFrom == "" {
				s.Time = clock(hour, minute) // 「每 2 小時，早上 8 點開始」
			}
		default:
			s.Time = clock(hour, minute)
		}
	}
	// 只說了幾點、沒說哪天也沒說重複：跑一次，今天還沒過就今天，不然明天。
	if s.Kind == "" && hour >= 0 {
		d := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
		if !d.After(now) {
			d = d.AddDate(0, 0, 1)
		}
		s.Kind, s.Date = KindOnce, d.Format("2006-01-02")
		p.Notes = append(p.Notes, "沒寫要重複，當成只跑一次")
	}
	if s.Kind == "" {
		s.Kind = KindDaily
	}
	if s.Time == "" && s.Kind != KindInterval && s.Kind != KindCron {
		p.Missing = append(p.Missing, "time")
	}
	if s.Kind == KindInterval && s.Time == "" {
		s.Time = firstNonEmpty(s.ActiveFrom, "00:00")
	}

	// 剩下的就是要做什麼。
	rest := strings.Join(strings.Fields(t.rest()), " ")
	for {
		trimmed := reTrail.ReplaceAllString(reLead.ReplaceAllString(rest, ""), "")
		if trimmed == rest {
			break
		}
		rest = trimmed
	}
	rest = strings.TrimFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) && r != '「' && r != '」' })
	p.Prompt = rest
	p.Title = TitleOf(rest)
	if rest == "" {
		p.Missing = append(p.Missing, "task")
	}
	return p
}

// TitleOf 從要做的事取一個短標題：第一句、最多 30 字。
func TitleOf(prompt string) string {
	line := strings.TrimSpace(prompt)
	if i := strings.IndexAny(line, "。\n！？!?；;"); i > 0 {
		line = line[:i]
	}
	r := []rune(line)
	if len(r) > 30 {
		return string(r[:30]) + "…"
	}
	return line
}
