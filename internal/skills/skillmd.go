package skills

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"xbot/internal/limits"
)

// Doc 是一個 SKILL.md：YAML frontmatter（name、description，以及 Hermes 用的 platforms、tags 等）＋內文。
type Doc struct {
	Name         string
	Description  string
	Instructions string
	// meta 是整個 frontmatter（保留順序與其他欄位）；沒有 frontmatter 時是 nil。
	meta *yaml.Node
}

var frontEnd = regexp.MustCompile(`\n---[ \t]*\r?\n`)

// Parse 讀 SKILL.md。沒有 frontmatter 或格式不對時，整份當內文、名稱空白（同 LazyBoy server/skills.py list()）。
func Parse(raw string) Doc {
	raw = strings.TrimPrefix(raw, "\ufeff")
	meta, body, ok := split(raw)
	if !ok {
		return Doc{Instructions: strings.TrimSpace(raw)}
	}
	d := Doc{Instructions: strings.TrimSpace(body), meta: meta}
	d.Name = scalar(meta, "name")
	d.Description = scalar(meta, "description")
	return d
}

// split 拆出 frontmatter 的 mapping 與內文。
func split(raw string) (*yaml.Node, string, bool) {
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return nil, "", false
	}
	rest := raw[3:]
	loc := frontEnd.FindStringIndex(rest)
	if loc == nil {
		// 只有 frontmatter、沒有內文。
		if strings.HasSuffix(strings.TrimRight(rest, " \t\r\n"), "\n---") {
			loc = []int{strings.LastIndex(rest, "\n---"), len(rest)}
		} else {
			return nil, "", false
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(rest[:loc[0]]), &doc); err != nil {
		return nil, "", false
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, rest[loc[1]:], true
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, "", false
	}
	return m, rest[loc[1]:], true
}

func scalar(m *yaml.Node, key string) string {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind == yaml.ScalarNode {
				return strings.TrimSpace(v.Value)
			}
			return ""
		}
	}
	return ""
}

func setScalar(m *yaml.Node, key, value string, at int) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	kv := []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: value}}
	at = min(at*2, len(m.Content))
	m.Content = append(m.Content[:at], append(kv, m.Content[at:]...)...)
}

// String 寫回 SKILL.md：frontmatter 只換 name、description，其他欄位（platforms、tags、需要的金鑰…）照舊
// （LazyBoy：改技能不能弄掉 Hermes 的 frontmatter）。
func (d Doc) String() string {
	m := d.meta
	if m == nil {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	setScalar(m, "name", d.Name, 0)
	setScalar(m, "description", d.Description, 1)
	out, err := yaml.Marshal(m)
	if err != nil {
		out = []byte(fmt.Sprintf("name: %q\ndescription: %q\n", d.Name, d.Description))
	}
	return "---\n" + string(out) + "---\n\n" + strings.TrimSpace(d.Instructions) + "\n"
}

// Validate 檢查名稱、用途、內文（長度照 Hermes tools/skill_manager_tool.py）。
func (d Doc) Validate() error {
	switch {
	case strings.TrimSpace(d.Name) == "":
		return errors.New("技能要有名稱")
	case utf8.RuneCountInString(d.Name) > limits.SkillNameMax:
		return fmt.Errorf("技能名稱最多 %d 字", limits.SkillNameMax)
	case strings.ContainsAny(d.Name, "\n\r/\\"):
		return errors.New("技能名稱不能有換行或斜線")
	case utf8.RuneCountInString(d.Description) > limits.SkillDescriptionMax:
		return fmt.Errorf("「什麼時候使用」最多 %d 字", limits.SkillDescriptionMax)
	case strings.TrimSpace(d.Instructions) == "":
		return errors.New("技能要寫怎麼做")
	}
	if n := utf8.RuneCountInString(d.String()); n > limits.SkillContentMax {
		return fmt.Errorf("技能內容太長（%d 字，上限 %d 字）；把細節放到附帶的檔案", n, limits.SkillContentMax)
	}
	return nil
}

// ValidateImport 檢查外面來的 SKILL.md（J09：格式不對就拒絕並說明哪裡有問題；規則同 Hermes _validate_frontmatter）。
func ValidateImport(raw string) (Doc, error) {
	raw = strings.TrimPrefix(raw, "\ufeff")
	if strings.TrimSpace(raw) == "" {
		return Doc{}, errors.New("SKILL.md 是空的")
	}
	if !strings.HasPrefix(raw, "---") {
		return Doc{}, errors.New("SKILL.md 要以 YAML frontmatter（---）開頭，裡面寫 name 和 description")
	}
	meta, body, ok := split(raw)
	if !ok {
		return Doc{}, errors.New("SKILL.md 的 frontmatter 沒有用 --- 收尾，或不是 key: value 的格式")
	}
	for _, k := range []string{"name", "description"} {
		found := false
		for i := 0; i+1 < len(meta.Content); i += 2 {
			found = found || meta.Content[i].Value == k
		}
		if !found {
			return Doc{}, fmt.Errorf("frontmatter 少了 %s", k)
		}
	}
	d := Doc{Name: scalar(meta, "name"), Description: scalar(meta, "description"), Instructions: strings.TrimSpace(body), meta: meta}
	if d.Instructions == "" {
		return Doc{}, errors.New("frontmatter 後面要有內文（怎麼做）")
	}
	return d, d.Validate()
}

var validDir = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// DirName 依技能名稱決定資料夾名稱：符合 Hermes 的名稱規則（小寫英數、.-_）就用名稱，否則用 skill-<亂碼>。
func DirName(name string, random func() string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Join(strings.Fields(n), "-")
	if validDir.MatchString(n) && len(n) <= limits.SkillNameMax {
		return n
	}
	return "skill-" + random()
}
