// Package botshare 是分享與匯入單一 Bot（PRD BT-8、SEC-1，畫板 G13、G14）：xbot.bots.export、xbot.bots.import。
//
// .xbot 檔是一份 JSON：名字、職稱、介紹、人設、頭像，Bot 自己的技能（只有文字），要連的應用程式（只有名稱）。
// 一律不帶：登入資料、密碼、API 金鑰、對話紀錄、電腦裡的檔案、記憶。
// 匯入的 Bot 一律從「先問我」開始（SEC-1），引擎與模型用這裡的預設；技能放進工作區技能庫並先停用（SK-1）。
package botshare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"xbot/internal/bots"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/skills"
	"xbot/internal/store"
)

// Format 是 .xbot 檔的格式名稱。
const Format = "xbot.bot"

// File 是 .xbot 檔的內容。
type File struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	ExportedAt int64           `json:"exported_at"`
	Bot        FileBot         `json:"bot"`
	Skills     []skills.Shared `json:"skills"`
	Apps       []FileApp       `json:"apps"`
	Settings   FileSettings    `json:"settings"`
}

// FileBot 是 Bot 的身分與人設。
type FileBot struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Soul        string `json:"soul,omitempty"`
	Color       string `json:"color,omitempty"`
	Shape       string `json:"shape,omitempty"`
	Eyes        string `json:"eyes,omitempty"`
	Image       string `json:"image,omitempty"`
}

// FileApp 是 Bot 要連的一個應用程式（只有名稱，不含任何登入）。
type FileApp struct {
	AppID string `json:"app_id"`
	Title string `json:"title"`
}

// FileSettings 是分享者那邊的設定，只供參考：匯入一律從「先問我」開始。
type FileSettings struct {
	ApprovalMode  string `json:"approval_mode,omitempty"`
	Effort        string `json:"effort,omitempty"`
	MemoryEnabled bool   `json:"memory_enabled"`
}

// Deps 是分享與匯入需要的元件；Skills 是 nil 時不帶技能。
type Deps struct {
	Skills *skills.Service
}

// ExportIn 是 xbot.bots.export 的輸入。
type ExportIn struct {
	ID       string `json:"id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	NoSkills bool   `json:"no_skills,omitempty" desc:"不帶技能"`
	NoApps   bool   `json:"no_apps,omitempty" desc:"不帶要連的應用程式"`
	NoSoul   bool   `json:"no_soul,omitempty" desc:"不帶人設（只帶名字、職稱、頭像）"`
}

// ExportOut 是 xbot.bots.export 的輸出。
type ExportOut struct {
	Filename string `json:"filename" desc:"建議的檔名，例：小秘.xbot"`
	Content  string `json:"content" desc:".xbot 檔的內容（JSON）"`
	Bytes    int    `json:"bytes"`
	Skills   int    `json:"skills"`
	Apps     int    `json:"apps"`
	Note     string `json:"note,omitempty" desc:"例：Bot 還沒有電腦，技能沒有帶上"`
}

// ImportIn 是 xbot.bots.import 的輸入。
type ImportIn struct {
	Content  string `json:"content" required:"true" blob:"true" max:"800000" desc:".xbot 檔的內容（JSON 文字）"`
	Filename string `json:"filename,omitempty" max:"200" desc:"檔名（記在技能的來源）"`
	Name     string `json:"name,omitempty" max:"100" desc:"匯入後的名字；不填用檔案裡的，和現有的重複時自動加編號"`
	Preview  bool   `json:"preview,omitempty" desc:"只檢查內容、不建立（G14 第 2 步）"`
}

// Item 是匯入內容清單的一列（G14：綠勾＝直接帶入、橘色＝要你處理）。
type Item struct {
	Kind   string `json:"kind" desc:"persona、skills、apps、permissions"`
	Status string `json:"status" desc:"ok：直接帶入；action：要你處理；info：說明"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// ImportOut 是 xbot.bots.import 的輸出。
type ImportOut struct {
	Preview bool      `json:"preview"`
	Bot     bots.View `json:"bot" desc:"建立的 Bot（預覽時是會建立的樣子，沒有 id）"`
	Renamed bool      `json:"renamed" desc:"和現有 Bot 同名，自動改了名字"`
	// Original 是檔案裡的名字。
	Original     string   `json:"original"`
	Items        []Item   `json:"items"`
	SkillsAdded  []string `json:"skills_added" desc:"放進工作區技能庫（先停用）的技能 id"`
	SkillsMerged []string `json:"skills_merged" desc:"技能庫已經有、合併掉的技能名稱"`
	AppsMissing  []string `json:"apps_missing" desc:"還沒連接的應用程式（之後再登入）"`
	Remaining    int      `json:"remaining" desc:"還可以建立幾個 Bot（全部最多 50 個）"`
	CanCreate    bool     `json:"can_create"`
	Reason       string   `json:"reason,omitempty" desc:"不能建立的原因"`
	ExportedAt   int64    `json:"exported_at"`
	Bytes        int      `json:"bytes"`
}

// Register 登錄 xbot.bots.export 與 xbot.bots.import。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[ExportIn, ExportOut]{
		Name: "xbot.bots.export", Summary: "把一個 Bot 做成可以分享的 .xbot 檔",
		Description: "帶名字、職稱、介紹、人設、頭像、它自己的技能（只有文字）與要連的應用程式名稱。" +
			"一律不帶登入資料、密碼、API 金鑰、對話紀錄、電腦裡的檔案與記憶。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ExportIn) (ExportOut, error) {
			return d.export(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[ImportIn, ImportOut]{
		Name: "xbot.bots.import", Summary: "從 .xbot 檔匯入一個 Bot",
		Description: "匯入的 Bot 一律從「先問我」開始（SEC-1），引擎與模型用這裡的預設；技能放進工作區技能庫並先停用，" +
			"要你看過再裝上；要連的應用程式只列名稱，之後再登入。和現有 Bot 同名時自動加編號。preview=true 只檢查不建立。" +
			"建立數量照一般規則（全部最多 50 個）。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ImportIn) (ImportOut, error) {
			return d.importBot(ctx, call, in)
		},
	})
}

func (d Deps) export(ctx context.Context, call *ops.Call, in ExportIn) (ExportOut, error) {
	b, err := call.DB.GetBot(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		return ExportOut{}, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return ExportOut{}, err
	}
	v := bots.ViewOf(b)
	f := File{Format: Format, Version: limits.BotFileVersion, ExportedAt: store.NowMS(), Skills: []skills.Shared{}, Apps: []FileApp{},
		Bot:      FileBot{Name: v.Name, Title: v.Title, Color: string(v.Color), Shape: string(v.Shape), Eyes: string(v.Eyes), Image: v.Image},
		Settings: FileSettings{ApprovalMode: string(v.ApprovalMode), Effort: v.Effort, MemoryEnabled: v.MemoryEnabled}}
	if !in.NoSoul {
		f.Bot.Description, f.Bot.Soul = v.Description, v.Soul
	}
	out := ExportOut{}
	if !in.NoSkills {
		list, has, err := d.Skills.ExportBot(ctx, b.ID)
		switch {
		case err != nil:
			out.Note = "讀不到它電腦裡的技能，這次沒有帶上：" + err.Error()
		case !has:
			out.Note = "它還沒有電腦，沒有自己的技能可以帶。"
		default:
			if len(list) > limits.BotFileSkillsMax {
				list = list[:limits.BotFileSkillsMax]
				out.Note = fmt.Sprintf("技能太多，只帶前 %d 個。", limits.BotFileSkillsMax)
			}
			f.Skills = list
		}
	}
	if !in.NoApps {
		conns, err := call.DB.AppConnectionsForBot(ctx, b.ID)
		if err != nil {
			return ExportOut{}, err
		}
		seen := map[string]bool{}
		for _, c := range conns {
			if !c.Enabled || seen[c.AppID+c.Title] {
				continue
			}
			seen[c.AppID+c.Title] = true
			f.Apps = append(f.Apps, FileApp{AppID: c.AppID, Title: c.Title})
		}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return ExportOut{}, err
	}
	if len(raw) > limits.BotFileBytes {
		return ExportOut{}, ops.Limit("這個 Bot 做成檔案太大了；試試不帶技能")
	}
	out.Filename = safeName(v.Name) + ".xbot"
	out.Content, out.Bytes, out.Skills, out.Apps = string(raw), len(raw), len(f.Skills), len(f.Apps)
	return out, nil
}

var unsafeFile = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func safeName(name string) string {
	n := strings.TrimSpace(unsafeFile.ReplaceAllString(name, "-"))
	if n == "" {
		return "bot"
	}
	return n
}

// Parse 讀 .xbot 檔並檢查格式與版本。
func Parse(content string) (File, error) {
	var f File
	if len(content) > limits.BotFileBytes {
		return f, ops.InvalidInput(ops.FieldError{Field: "content", Message: "檔案太大，不像是 .xbot 檔"})
	}
	if err := json.Unmarshal([]byte(content), &f); err != nil || f.Format != Format {
		return f, ops.InvalidInput(ops.FieldError{Field: "content", Message: "檔案損壞或不是 .xbot 檔"})
	}
	if f.Version > limits.BotFileVersion {
		return f, ops.InvalidInput(ops.FieldError{Field: "content", Message: "這個檔案的版本比較新，請先更新 XBot 再匯入"})
	}
	if strings.TrimSpace(f.Bot.Name) == "" {
		return f, ops.InvalidInput(ops.FieldError{Field: "content", Message: "檔案裡沒有 Bot 的名字"})
	}
	return f, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func oneOf(v string, all []string, def string) string {
	if slices.Contains(all, v) {
		return v
	}
	return def
}

// uniqueName 和現有 Bot 同名時加編號：「小秘」→「小秘 2」（G14）。
func uniqueName(name string, taken map[string]bool) (string, bool) {
	if !taken[name] {
		return name, false
	}
	for i := 2; ; i++ {
		n := clip(name, limits.BotNameMax-1-len(strconv.Itoa(i))) + " " + strconv.Itoa(i)
		if !taken[n] {
			return n, true
		}
	}
}

func (d Deps) importBot(ctx context.Context, call *ops.Call, in ImportIn) (ImportOut, error) {
	f, err := Parse(in.Content)
	if err != nil {
		return ImportOut{}, err
	}
	all, err := call.DB.ListBots(ctx, true)
	if err != nil {
		return ImportOut{}, err
	}
	taken := map[string]bool{}
	for _, b := range all {
		taken[b.Name] = true
	}
	want := clip(f.Bot.Name, limits.BotNameMax)
	if n := strings.TrimSpace(in.Name); n != "" {
		want = clip(n, limits.BotNameMax)
	}
	name, renamed := uniqueName(want, taken)

	out := ImportOut{Preview: in.Preview, Original: f.Bot.Name, Renamed: renamed, ExportedAt: f.ExportedAt, Bytes: len(in.Content),
		SkillsAdded: []string{}, SkillsMerged: []string{}, AppsMissing: []string{}, Remaining: max(0, limits.BotsMax-len(all)), CanCreate: true}
	if err := bots.CheckQuota(ctx, call.DB, nil, 1); err != nil {
		out.CanCreate, out.Reason = false, err.Error()
	}

	image := f.Bot.Image
	if bots.ValidImage(image) != nil {
		image = "" // 圖片不對就用吉祥物，不擋匯入
	}
	color := f.Bot.Color // 自選的 #RRGGBB 也帶得過來
	if color == "" || !bots.ValidColor(color) {
		color = bots.Colors[len(all)%len(bots.Colors)]
	}
	av, _ := json.Marshal(map[string]string{"shape": oneOf(f.Bot.Shape, bots.Shapes, "cursor"), "eyes": oneOf(f.Bot.Eyes, bots.EyeVariants, ""), "image": image})
	b := store.Bot{Name: name, Title: clip(f.Bot.Title, 100), Description: clip(f.Bot.Description, 1000), Soul: clip(f.Bot.Soul, limits.BotSoulMax),
		Color: color, Avatar: av,
		Engine: string(bots.EngineHermes), Effort: oneOf(f.Settings.Effort, []string{"low", "medium", "high"}, ""),
		ApprovalMode:  string(bots.ModeAsk), // SEC-1：匯入的 Bot 一律先問我
		MemoryEnabled: true, CreatedBy: call.Identity.Actor()}

	// 內容清單
	persona := "照原樣帶入"
	if renamed {
		persona = fmt.Sprintf("你已經有「%s」，先叫「%s」", want, name)
	}
	out.Items = append(out.Items, Item{Kind: "persona", Status: "ok", Title: "人設與頭像", Detail: persona})
	existing, err := d.Skills.LibraryNames()
	if err != nil {
		return ImportOut{}, err
	}
	skillList := f.Skills
	if len(skillList) > limits.BotFileSkillsMax {
		skillList = skillList[:limits.BotFileSkillsMax]
	}
	if len(skillList) > 0 {
		var dup []string
		for _, s := range skillList {
			if existing[strings.TrimSpace(s.Name)] {
				dup = append(dup, s.Name)
			}
		}
		detail := "放進技能庫，先停用；你看過內容再裝上"
		if len(dup) > 0 {
			detail = fmt.Sprintf("「%s」和你現有的重複，會合併；其他放進技能庫，先停用", strings.Join(dup, "、"))
		}
		out.Items = append(out.Items, Item{Kind: "skills", Status: "ok", Title: fmt.Sprintf("技能 %d 個", len(skillList)), Detail: detail})
	}
	if len(f.Apps) > 0 {
		conns, err := call.DB.ListAppConnections(ctx)
		if err != nil {
			return ImportOut{}, err
		}
		for _, a := range f.Apps {
			if !slices.ContainsFunc(conns, func(c store.AppConnection) bool { return c.AppID == a.AppID && a.AppID != "custom" && c.Enabled }) {
				out.AppsMissing = append(out.AppsMissing, a.Title)
			}
		}
		st, detail := "ok", "都已經連接；到「應用程式與電腦」打開給它用"
		if len(out.AppsMissing) > 0 {
			st, detail = "action", strings.Join(out.AppsMissing, "、")+" 要你登入；可以之後再登入，Bot 先建立"
		}
		out.Items = append(out.Items, Item{Kind: "apps", Status: st, Title: fmt.Sprintf("應用程式 %d 個", len(f.Apps)), Detail: detail})
	}
	out.Items = append(out.Items, Item{Kind: "permissions", Status: "info", Title: "權限", Detail: "一律從「先問我」開始；引擎與模型用你這裡的預設"})

	if in.Preview || call.DryRun {
		out.Bot = bots.ViewOf(b)
		return out, nil
	}
	if !out.CanCreate {
		return out, ops.Limit(out.Reason)
	}
	created, err := call.DB.CreateBot(ctx, b)
	if err != nil {
		return ImportOut{}, err
	}
	out.Bot = bots.ViewOf(created)
	if len(skillList) > 0 {
		added, merged, err := d.Skills.ImportToLibrary(skillList, safeName(f.Bot.Name)+".xbot", call.Identity.Actor())
		for _, s := range added {
			out.SkillsAdded = append(out.SkillsAdded, s.ID)
		}
		out.SkillsMerged = append(out.SkillsMerged, merged...)
		if err != nil {
			out.Items = append(out.Items, Item{Kind: "skills", Status: "action", Title: "有些技能沒有放進技能庫", Detail: err.Error()})
		}
	}
	return out, nil
}
