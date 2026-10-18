package importer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"xbot/internal/bots"
	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/routines"
)

// OpenMausBot 的資料目錄（server/config.ts DATA_DIR：OMB_DATA_DIR，預設 ~/.openmausbot）：
//
//	bots.json                    Bot 陣列（name、title、description、soul、color、mascotBody、section、chiefOfStaff…）
//	groups.json                  群組陣列（name、memberIds、bulletin；dm=true 是兩個 Bot 之間的私訊，不帶）
//	routines.json                {routines:[{name,prompt,botId,enabled,schedule…}]}
//	section-contexts.json        團隊的共同說明 {contexts:{<團隊>:{text}}}
//	config.json                  金鑰：anthropic.key、openai.key、openrouter.key、xai.key、openaiCompat{key,url}…
//	workspaces/<botId>/          Bot 的工作資料夾：MEMORY.md、memory/*.md、skills/<名稱>/SKILL.md、其他檔案
//	messages.db                  對話紀錄（不帶）
//	attachments/                 頭像圖片等
const ombBots = "bots.json"

// IsOpenMausBot 回答 dir 像不像 OpenMausBot 的資料目錄。
func IsOpenMausBot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ombBots))
	return err == nil && st.Mode().IsRegular()
}

type ombBot struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Soul           string   `json:"soul"`
	Color          string   `json:"color"`
	MascotBody     string   `json:"mascotBody"`
	AvatarURL      string   `json:"avatarUrl"`
	Section        string   `json:"section"`
	ChiefOfStaff   bool     `json:"chiefOfStaff"`
	ManagedSection []string `json:"managedSections"`
	ApprovalMode   string   `json:"approvalMode"`
	Hidden         bool     `json:"hidden"`
	ModelSelection struct {
		InstanceID string `json:"instanceId"`
		Model      string `json:"model"`
	} `json:"modelSelection"`
}

type ombGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"memberIds"`
	Bulletin  string   `json:"bulletin"`
	DM        bool     `json:"dm"`
}

type ombRoutine struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Prompt   string         `json:"prompt"`
	Target   string         `json:"target"`
	BotID    string         `json:"botId"`
	Enabled  bool           `json:"enabled"`
	Schedule map[string]any `json:"schedule"`
}

func readJSON(p string, v any) error {
	raw, err := readSmall(p, limits.ImportJSONBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// ReadOpenMausBot 讀 OpenMausBot 的資料目錄（只讀）。
func ReadOpenMausBot(dir string) (*Source, error) {
	var list []ombBot
	if err := readJSON(filepath.Join(dir, ombBots), &list); err != nil {
		return nil, fmt.Errorf("讀不到 OpenMausBot 的 bots.json：%w", err)
	}
	src := &Source{Kind: KindOpenMausBot, Path: dir}
	known := map[string]bool{}
	for _, b := range list {
		if b.ID == "" || strings.TrimSpace(b.Name) == "" || known[b.ID] {
			continue
		}
		known[b.ID] = true
		src.Bots = append(src.Bots, readOMBBot(dir, b, src))
	}
	// 團隊：每個 section 一個（"" 是「一般」，不建團隊）；chiefOfStaff 是主管，其他成員向它報告。
	var ctxs struct {
		Contexts map[string]struct {
			Text string `json:"text"`
		} `json:"contexts"`
	}
	_ = readJSON(filepath.Join(dir, "section-contexts.json"), &ctxs)
	for _, b := range list {
		if !known[b.ID] || strings.TrimSpace(b.Section) == "" {
			continue
		}
		i := slices.IndexFunc(src.Teams, func(t Team) bool { return t.Name == b.Section })
		if i < 0 {
			src.Teams = append(src.Teams, Team{ID: "section:" + b.Section, Name: b.Section, About: ctxs.Contexts[b.Section].Text})
			i = len(src.Teams) - 1
		}
		src.Teams[i].Members = append(src.Teams[i].Members, b.ID)
		if b.ChiefOfStaff && src.Teams[i].ChiefID == "" {
			src.Teams[i].ChiefID = b.ID
		}
	}
	for i := range src.Bots {
		b := &src.Bots[i]
		if b.TeamID == "" {
			continue
		}
		for _, t := range src.Teams {
			if t.ID == b.TeamID && t.ChiefID != "" && t.ChiefID != b.ID {
				b.ReportsTo = t.ChiefID
			}
		}
	}
	var groups []ombGroup
	_ = readJSON(filepath.Join(dir, "groups.json"), &groups)
	for _, g := range groups {
		if g.ID == "" || g.DM {
			continue
		}
		r := Room{ID: g.ID, Name: g.Name, Bulletin: g.Bulletin}
		for _, m := range g.MemberIDs {
			if known[m] && !slices.Contains(r.Members, m) {
				r.Members = append(r.Members, m)
			}
		}
		src.Rooms = append(src.Rooms, r)
	}
	var rf struct {
		Routines []ombRoutine `json:"routines"`
	}
	_ = readJSON(filepath.Join(dir, "routines.json"), &rf)
	for _, r := range rf.Routines {
		if r.ID == "" || !known[r.BotID] {
			continue
		}
		src.Routines = append(src.Routines, ombRoutineOf(r))
	}
	src.Keys = readOMBKeys(dir)
	return src, nil
}

func readOMBBot(dir string, o ombBot, src *Source) Bot {
	b := Bot{ID: o.ID, Name: o.Name, Title: o.Title, Description: o.Description, Soul: o.Soul, Color: o.Color, Shape: o.MascotBody,
		Chief: o.ChiefOfStaff, Approval: o.ApprovalMode}
	if strings.TrimSpace(o.Section) != "" {
		b.TeamID = "section:" + o.Section
	}
	if o.ModelSelection.Model != "" {
		b.Model = o.ModelSelection.InstanceID + " / " + o.ModelSelection.Model
	}
	b.Image = ombAvatar(dir, o.AvatarURL)
	ws := filepath.Join(dir, "workspaces", o.ID)
	if sk, err := readSkillDirs(filepath.Join(ws, "skills"), nil); err != nil {
		src.Notes = append(src.Notes, fmt.Sprintf("%s 的技能讀不到：%v", o.Name, err))
	} else {
		b.Skills = sk
	}
	// 記憶：MEMORY.md 是索引（每段或每個項目一條）；memory/ 底下的主題檔原樣放進工作資料夾的「匯入的記憶」。
	if raw, err := readSmall(filepath.Join(ws, "MEMORY.md"), limits.ImportMemoryFileBytes); err == nil {
		b.Memory = splitNotes(string(raw))
		b.MemoryFiles = append(b.MemoryFiles, boxfs.File{Name: "MEMORY.md", Data: raw, Mode: 0o644})
	}
	memDir := filepath.Join(ws, "memory")
	_ = filepath.WalkDir(memDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := readSmall(p, limits.ImportMemoryFileBytes)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(memDir, p)
		b.MemoryFiles = append(b.MemoryFiles, boxfs.File{Name: path.Join("memory", filepath.ToSlash(rel)), Data: raw, Mode: 0o644})
		return nil
	})
	if isDir(ws) {
		b.Data = append(b.Data, Tree{Label: "工作資料夾", Dir: ws, Dest: box.WorkspaceDir, Skip: func(rel string, d fs.DirEntry) bool {
			top, _, _ := strings.Cut(rel, "/")
			// 技能、記憶另外帶；隱藏的檔案是設定。
			return top == "skills" || top == "memory" || top == "MEMORY.md" || strings.HasPrefix(top, ".") || skipCaches(rel, d)
		}})
	}
	return b
}

// ombAvatar 讀頭像圖片（/api/attachments/<檔名>）成 data URL；太大或格式不對就不帶，用吉祥物。
func ombAvatar(dir, url string) string {
	name, ok := strings.CutPrefix(url, "/api/attachments/")
	if !ok || name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return ""
	}
	mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[strings.ToLower(path.Ext(name))]
	if mime == "" {
		return ""
	}
	raw, err := readSmall(filepath.Join(dir, "attachments", name), limits.AvatarImageBytes)
	if err != nil {
		return ""
	}
	img := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	if bots.ValidImage(img) != nil {
		return ""
	}
	return img
}

// splitNotes 把一份 Markdown 筆記切成條目：每個清單項目一條，其他依空行分段；標題行略過。
func splitNotes(raw string) []string {
	var out []string
	var para []string
	flush := func() {
		if t := strings.TrimSpace(strings.Join(para, "\n")); t != "" {
			out = append(out, t)
		}
		para = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "#"):
			flush()
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			flush()
			para = append(para, strings.TrimSpace(t[2:]))
		default:
			para = append(para, t)
		}
	}
	flush()
	return out
}

// ombRoutineOf 把 OpenMausBot 的排程（shared/routine-schedule.ts）換成 XBot 的。
// daily、interval、once 用 OpenMausBot 服務那台電腦的時區（這裡用 XBot 的預設時區 Asia/Taipei，說明裡提醒）。
func ombRoutineOf(r ombRoutine) Routine {
	out := Routine{ID: r.ID, BotID: r.BotID, Title: r.Name, Prompt: r.Prompt, Enabled: r.Enabled}
	if r.Target == "room-goal" {
		out.Problem = "群組目標類的定時任務 XBot 還沒有"
		return out
	}
	s := r.Schedule
	str := func(k string) string {
		v, _ := s[k].(string)
		return strings.TrimSpace(v)
	}
	num := func(k string) int {
		v, _ := s[k].(float64)
		return int(v)
	}
	days := func(v any) []int {
		var out []int
		list, _ := v.([]any)
		for _, d := range list {
			if f, ok := d.(float64); ok {
				n := int(f)
				if n == 0 {
					n = 7 // OpenMausBot 0＝週日；XBot 7＝週日
				}
				if n >= 1 && n <= 7 && !slices.Contains(out, n) {
					out = append(out, n)
				}
			}
		}
		slices.Sort(out)
		return out
	}
	var sc routines.Schedule
	switch str("type") {
	case "cron":
		sc = routines.Schedule{Kind: "cron", Cron: str("expression"), Timezone: str("timeZone")}
	case "daily":
		sc = routines.Schedule{Kind: "daily", Time: str("time")}
		switch w := days(s["weekdays"]); {
		case len(w) == 0 || len(w) == 7:
		case slices.Equal(w, []int{1, 2, 3, 4, 5}):
			sc.Kind = "weekdays"
		default:
			sc.Kind, sc.Weekdays = "weekly", w
		}
		out.Note = "時間照 XBot 的時區（Asia/Taipei）"
	case "interval":
		sc = routines.Schedule{Kind: "interval", EveryMinutes: num("everyMinutes")}
		if w, ok := s["window"].(map[string]any); ok {
			sc.ActiveFrom, _ = w["start"].(string)
			sc.ActiveTo, _ = w["end"].(string)
		}
		if t := msTime(s["endsAt"]); !t.IsZero() {
			sc.EndDate = t.In(taipei()).Format("2006-01-02")
		}
		if sc.ActiveFrom == "" {
			sc.Time = "00:00"
		}
		out.Note = "時間照 XBot 的時區（Asia/Taipei）"
	case "once":
		t := msTime(s["at"])
		if t.IsZero() {
			out.Problem = "看不懂這個單次排程的時間"
			return out
		}
		lt := t.In(taipei())
		sc = routines.Schedule{Kind: "once", Date: lt.Format("2006-01-02"), Time: lt.Format("15:04")}
	default:
		out.Problem = "看不懂這個排程"
		return out
	}
	out.Schedule = sc
	return out
}

func taipei() *time.Location {
	if loc, err := time.LoadLocation(routines.DefaultTimezone); err == nil {
		return loc
	}
	return time.Local
}

// msTime 讀毫秒數字或 ISO 時間字串。
func msTime(v any) time.Time {
	switch x := v.(type) {
	case float64:
		if x > 0 {
			return time.UnixMilli(int64(x))
		}
	case string:
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ombKeyFields 是 config.json 裡的金鑰欄位對到 XBot 的供應商範本。
var ombKeyFields = []struct{ field, id, label string }{
	{"anthropic", "anthropic", "Anthropic"},
	{"openai", "openai-api", "OpenAI"},
	{"openrouter", "openrouter", "OpenRouter"},
	{"xai", "xai", "xAI"},
	{"groq", "groq", "Groq"},
	{"mistral", "mistral", "Mistral"},
	{"cerebras", "cerebras", "Cerebras"},
	{"openaiCompat", "openai-compat", "OpenAI 相容服務"},
}

// ombBaseURL 是沒有內建範本時的服務網址。
var ombBaseURL = map[string]string{
	"groq": "https://api.groq.com/openai/v1", "mistral": "https://api.mistral.ai/v1", "cerebras": "https://api.cerebras.ai/v1",
}

func readOMBKeys(dir string) []Key {
	var cfg map[string]json.RawMessage
	if readJSON(filepath.Join(dir, "config.json"), &cfg) != nil {
		return nil
	}
	var out []Key
	for _, f := range ombKeyFields {
		var v struct {
			Key    string `json:"key"`
			APIKey string `json:"apiKey"`
			URL    string `json:"url"`
		}
		if raw, ok := cfg[f.field]; !ok || json.Unmarshal(raw, &v) != nil {
			continue
		}
		key := cmpOr(v.Key, v.APIKey)
		if key == "" {
			continue // 桌面版把金鑰放在系統鑰匙圈（credentials.bin），讀不到
		}
		base := cmpOr(v.URL, ombBaseURL[f.id])
		k := keyOf(f.id, f.label, base, "", key)
		if k.BaseURL == "" {
			continue
		}
		out = append(out, k)
	}
	return out
}
