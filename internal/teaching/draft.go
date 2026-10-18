package teaching

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
)

// Draft 是從示範整理出來的技能草稿（E04「整理技能中」之後給你看、可以改）。
type Draft struct {
	Name         string   `json:"name" desc:"技能名稱"`
	Description  string   `json:"description" desc:"什麼時候使用"`
	Instructions string   `json:"instructions" desc:"怎麼做（SKILL.md 內文，含步驟、網址、定位、截圖）"`
	Shots        []string `json:"shots" desc:"會一起存進技能資料夾的截圖（shots/…）"`
	By           string   `json:"by" desc:"rules（照範本整理）或 model（快判模型潤飾過名稱與用途）"`
}

// sourceZH 是步驟來源的白話（E04 的類型標籤）。
var sourceZH = map[string]string{"browser": "瀏覽器", "desktop": "桌面", "user": "說明"}

// stepsText 是給快判模型看的步驟（一步一行）。
func stepsText(goal string, steps []Step) string {
	var b strings.Builder
	if goal != "" {
		fmt.Fprintf(&b, "目的：%s\n", goal)
	}
	for i, s := range steps {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, sourceZH[s.Source], s.Text)
	}
	return b.String()
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ruleName 是沒有快判時的技能名稱：你說的目的；沒說時用第一個頁面或視窗的標題。
func ruleName(goal string, steps []Step) string {
	if goal = clip(goal, limits.SkillNameMax); goal != "" {
		return goal
	}
	for _, s := range steps {
		if place := or(s.Title, s.Window); place != "" && s.Source != "user" {
			return clip("在「"+place+"」照示範操作", limits.SkillNameMax)
		}
	}
	return "照示範操作"
}

func ruleDescription(goal string, steps []Step) string {
	if goal != "" {
		return clip(fmt.Sprintf("要「%s」時使用（從你的示範學來，%d 步）", goal, len(steps)), limits.SkillDescriptionMax)
	}
	return fmt.Sprintf("要照你示範過的 %d 個步驟再做一次時使用", len(steps))
}

// buildDraft 照範本把步驟寫成 SKILL.md 的內文：目的、步驟（白話＋網址、定位、截圖）、注意事項。
// 截圖只放 keyShots 挑出來的（會一起存進技能資料夾）。
func buildDraft(goal string, steps []Step) Draft {
	shots := keyShots(steps, limits.TeachKeyShots)
	keep := map[string]bool{}
	for _, s := range shots {
		keep[s] = true
	}
	var b strings.Builder
	b.WriteString("## 目的\n")
	if goal != "" {
		b.WriteString(goal + "\n")
	} else {
		b.WriteString("照使用者示範過的步驟做一次（目的請從步驟推論；不確定時先問使用者）。\n")
	}
	b.WriteString("\n## 步驟\n")
	lastURL := ""
	n := 0
	for _, s := range steps {
		if s.Kind == "note" {
			indent := "   "
			if n == 0 {
				indent = ""
			}
			fmt.Fprintf(&b, "%s- 使用者補充：%s\n", indent, s.Value)
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. %s%s\n", n, map[string]string{"desktop": "（桌面）"}[s.Source], s.Text)
		if s.URL != "" && s.URL != lastURL && s.Source == "browser" {
			fmt.Fprintf(&b, "   - 網址：%s\n", s.URL)
		}
		if s.Source == "browser" {
			lastURL = s.URL
			if s.Result != nil && s.Result.URL != "" {
				lastURL = s.Result.URL
			}
		}
		if s.Target != nil && s.Target.Selector != "" {
			fmt.Fprintf(&b, "   - 定位：`%s`\n", s.Target.Selector)
		}
		if keep[s.Shot] {
			fmt.Fprintf(&b, "   - 截圖：%s\n", s.Shot)
			delete(keep, s.Shot) // 同一張只標一次
		}
	}
	b.WriteString("\n## 注意\n")
	b.WriteString("- 瀏覽器操作依元素的角色與文字找（上面的定位只是參考），不要依賴螢幕座標。\n")
	b.WriteString("- 示範裡的密碼只記成 ••••。需要登入時沿用電腦裡已登入的狀態；不行就請使用者接手電腦。\n")
	b.WriteString("- 每次會不一樣的值（日期、關鍵字、檔名、數量）照這次的交代填；不確定時先問。\n")
	b.WriteString("- 遇到付款、送出訂單、刪除、寄出這類無法復原的動作，先停下來問使用者。\n")
	if shots == nil {
		shots = []string{}
	}
	return Draft{Name: ruleName(goal, steps), Description: ruleDescription(goal, steps), Instructions: b.String(), Shots: shots, By: "rules"}
}

// usedShots 是內文裡還提到的截圖（你改草稿時刪掉的就不存）。
func usedShots(d Draft, instructions string) []string {
	var out []string
	for _, s := range d.Shots {
		if ValidShot(s) && strings.Contains(instructions, s) {
			out = append(out, s)
		}
	}
	return out
}

// trialPrompt 是存成技能後請 Bot 試做一次的提示（LazyBoy lesson_prompt() 第 5 點）。
func trialPrompt(name string) string {
	return "你剛學會一個新技能「" + name + "」（使用者在你的電腦上示範、你整理成的技能）。\n" +
		"請照這個技能，用示範裡的值在這台電腦上實際做一次，確認做得通；做不通就說明卡在哪一步，並建議怎麼修改技能。\n" +
		"遇到付款、送出訂單、刪除、寄出這類無法復原的動作時，停在那一步前面說明，不要按下去。\n" +
		"最後回報：試做結果，以及使用者以後可以怎麼叫你做這件事。"
}
