package voice

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Speakable 把 Bot 的回覆整理成念得出來的文字（照 OpenMausBot server/tts/speech-text.ts 的作法，改成中文）：
// 程式碼區塊說「一段程式碼」、圖片說它的說明、連結只念文字、網址說「連結」、表格一列念成一句、
// 去掉 Markdown 記號與表情符號、長路徑只念檔名。沒有可以念的字時回空字串（合成器會把孤零零的句號念成雜音）。
func Speakable(in string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}
	t := strings.ReplaceAll(in, "\r\n", "\n")
	t = fence.ReplaceAllStringFunc(t, func(m string) string {
		return "（一段" + codeLang(fence.FindStringSubmatch(m)[1]) + "程式碼）\n"
	})
	t = tildeFence.ReplaceAllStringFunc(t, func(m string) string {
		return "（一段" + codeLang(tildeFence.FindStringSubmatch(m)[1]) + "程式碼）\n"
	})
	t = image.ReplaceAllStringFunc(t, func(m string) string {
		if alt := strings.TrimSpace(image.FindStringSubmatch(m)[1]); alt != "" {
			return "（圖片：" + alt + "）"
		}
		return "（一張圖片）"
	})
	t = link.ReplaceAllString(t, "$1")
	t = angleURL.ReplaceAllString(t, "連結")
	t = bareURL.ReplaceAllString(t, "連結")
	t = tableRule.ReplaceAllString(t, "")
	t = tableRow.ReplaceAllStringFunc(t, func(m string) string {
		var cells []string
		for _, c := range strings.Split(strings.Trim(strings.TrimSpace(m), "|"), "|") {
			if c = strings.TrimSpace(c); c != "" {
				cells = append(cells, c)
			}
		}
		return strings.Join(cells, "，")
	})
	t = inlineCode.ReplaceAllStringFunc(t, func(m string) string {
		code := inlineCode.FindStringSubmatch(m)[1]
		if utf8.RuneCountInString(code) <= 40 {
			return code
		}
		return "那段程式碼"
	})
	t = heading.ReplaceAllStringFunc(t, func(m string) string {
		h := strings.TrimSpace(heading.FindStringSubmatch(m)[1])
		if endsSentence(h) {
			return h
		}
		return h + "。"
	})
	t = bullet.ReplaceAllString(t, "")
	t = numbered.ReplaceAllString(t, "")
	t = quote.ReplaceAllString(t, "")
	t = hrule.ReplaceAllString(t, "")
	t = bold.ReplaceAllString(t, "$2")
	t = strike.ReplaceAllString(t, "$1")
	t = checkbox.ReplaceAllString(t, "")
	t = italicStar.ReplaceAllString(t, "$1")
	t = path.ReplaceAllString(t, "$1")
	t = strings.Map(func(r rune) rune {
		if isEmoji(r) {
			return -1
		}
		return r
	}, t)
	// 換行：前一句沒有標點就補一個句號，念起來才會停頓。
	lines := strings.Split(t, "\n")
	var b strings.Builder
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(l)
		if !endsSentence(l) {
			if hasCJK(l) {
				b.WriteString("。")
			} else {
				b.WriteString(".")
			}
		}
	}
	t = spaces.ReplaceAllString(b.String(), " ")
	t = spaceBeforePunct.ReplaceAllString(t, "$1")
	t = strings.TrimSpace(t)
	if !strings.ContainsFunc(t, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
		return ""
	}
	return t
}

// Clip 把文字截到 max 個字以內，盡量切在句子結尾；截掉時補一句「後面請看文字」。
func Clip(text string, max int) (string, bool) {
	rs := []rune(text)
	if len(rs) <= max {
		return text, false
	}
	const tail = "……後面請看文字。"
	limit := max - utf8.RuneCountInString(tail)
	cut := limit
	for i := limit - 1; i > limit/2; i-- {
		if strings.ContainsRune("。！？.!?；;", rs[i]) {
			cut = i + 1
			break
		}
	}
	return strings.TrimSpace(string(rs[:cut])) + tail, true
}

var (
	fence      = regexp.MustCompile("(?s)```([^\\n]*)\\n.*?(?:```|$)")
	tildeFence = regexp.MustCompile("(?s)~~~([^\\n]*)\\n.*?(?:~~~|$)")
	image      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	link       = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	angleURL   = regexp.MustCompile(`<https?://[^>\s]+>`)
	bareURL    = regexp.MustCompile(`\bhttps?://[^\s）)」』，。]+`)
	// 以行為單位的規則用 [ \t]（\s 會吃掉換行，把兩行黏在一起）。
	tableRule        = regexp.MustCompile(`(?m)^[ \t]*\|?[ \t:|-]*-[ \t:|-]*\|?[ \t]*$`)
	tableRow         = regexp.MustCompile(`(?m)^[ \t]*\|.+\|[ \t]*$`)
	inlineCode       = regexp.MustCompile("`([^`\\n]+)`")
	heading          = regexp.MustCompile(`(?m)^[ \t]{0,3}#{1,6}[ \t]+(.*)$`)
	bullet           = regexp.MustCompile(`(?m)^[ \t]*[-*+•][ \t]+`)
	numbered         = regexp.MustCompile(`(?m)^[ \t]*\d+[.)、][ \t]+`)
	quote            = regexp.MustCompile(`(?m)^[ \t]*>[ \t]?`)
	hrule            = regexp.MustCompile(`(?m)^[ \t]*(?:[-*_][ \t]*){3,}$`)
	bold             = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	strike           = regexp.MustCompile(`~~(.+?)~~`)
	checkbox         = regexp.MustCompile(`\[[ xX]\]\s*`)
	italicStar       = regexp.MustCompile(`\*([^*\s][^*]*?)\*`)
	path             = regexp.MustCompile(`(?:[\w.@-]+/)+([\w.-]+\.\w{1,6})\b`)
	spaces           = regexp.MustCompile(`\s+`)
	spaceBeforePunct = regexp.MustCompile(`\s+([.,!?;:。，！？；：])`)
)

var langNames = map[string]string{
	"ts": "TypeScript ", "tsx": "TypeScript ", "js": "JavaScript ", "jsx": "JavaScript ", "py": "Python ", "python": "Python ",
	"sh": "指令列", "bash": "指令列", "zsh": "指令列", "shell": "指令列", "json": "JSON ", "yaml": "YAML ", "yml": "YAML ",
	"sql": "SQL ", "go": "Go ", "rust": "Rust ", "rs": "Rust ", "html": "HTML ", "css": "CSS ", "diff": "差異",
}

func codeLang(fenceInfo string) string {
	f := strings.Fields(fenceInfo)
	if len(f) == 0 {
		return ""
	}
	return langNames[strings.ToLower(f[0])]
}

func endsSentence(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(s))
	return strings.ContainsRune(".!?:;。！？：；…）)」』", r)
}

func hasCJK(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

func isEmoji(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || (r >= 0xFE00 && r <= 0xFE0F) ||
		(r >= 0x2190 && r <= 0x21FF) || (r >= 0x2B00 && r <= 0x2BFF) || r == 0x200D
}
