// Package bots 是 Bot 管理的操作（PRD §6.2 BT-*、§22.3 bots）：list、get、create、update、archive、delete。
package bots

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Shape 是頭像形狀（design/N02-Mascot、web/src/ui/avatar-data.ts SHAPES）。
type Shape string

// Shapes 是所有形狀，順序同前端。
var Shapes = []string{"cursor", "blob", "circle", "squircle", "capsule", "drop", "shield", "hexagon", "diamond", "star"}

// EnumValues 讓 JSON Schema 列出可用值。
func (Shape) EnumValues() []string { return Shapes }

// Color 是頭像顏色（web/src/ui/avatar-data.ts COLORS）。
type Color string

// Colors 是所有顏色，順序同前端。
var Colors = []string{"green", "blue", "red", "orange", "purple", "cyan", "pink", "yellow", "teal", "coral"}

// colorHex 是自選的顏色（調色盤）：#RRGGBB。
var colorHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidColor 回答是不是能用的頭像顏色：內建的 10 色之一，或自選的 #RRGGBB。空白表示不改／自動挑。
func ValidColor(c string) bool {
	return c == "" || slices.Contains(Colors, c) || colorHex.MatchString(c)
}

func colorErr() error {
	return ops.InvalidInput(ops.FieldError{Field: "color", Message: "要是 " + strings.Join(Colors, "、") + " 之一，或 #RRGGBB"})
}

// Eyes 是頭像平常的眼睛（G03：圓點、看旁邊、開心、睜大、半閉、閉眼；web/src/ui/avatar-data.ts EYE_VARIANTS）。
// 只是待命時的樣子，對話中仍會跟著狀態變。
type Eyes string

// EyeVariants 是所有眼睛，順序同前端。
var EyeVariants = []string{"dot", "aside", "happy", "wide", "half", "closed"}

// EnumValues 讓 JSON Schema 列出可用值。
func (Eyes) EnumValues() []string { return EyeVariants }

// ApprovalMode 是權限等級（BT-5、design/specs/G08-Access）：先問我、自動接受編輯、自動、完全存取。
// 值沿用 OpenMausBot shared/approval-mode.ts。
type ApprovalMode string

// 權限等級。新 Bot 預設 ask（SEC-1）。
const (
	ModeAsk   ApprovalMode = "ask"
	ModeEdits ApprovalMode = "edits"
	ModeAuto  ApprovalMode = "auto"
	ModeFull  ApprovalMode = "full"
)

// EnumValues 讓 JSON Schema 列出可用值。
func (ApprovalMode) EnumValues() []string { return []string{"ask", "edits", "auto", "full"} }

// Engine 是 Bot 用的引擎：Hermes（預設，經 Model Proxy）、Claude Code、Codex CLI（ACP 驅動，E12）、
// Gemini CLI、OpenCode、Grok（ACP 驅動，E31）；ACP 引擎要你同意後用你在這台電腦上的訂閱登入，見 xbot.engines.link_credentials。
type Engine string

// 引擎。
const (
	EngineHermes Engine = "hermes"
	EngineClaude Engine = "claude-code"
	EngineCodex  Engine = "codex"
)

// EnumValues 讓 JSON Schema 列出可用值。
func (Engine) EnumValues() []string {
	return []string{"hermes", "claude-code", "codex", "gemini", "opencode", "grok"}
}

// Effort 是思考強度（Hermes 的 reasoning_effort；空白＝medium）。
type Effort string

// EnumValues 讓 JSON Schema 列出可用值。
func (Effort) EnumValues() []string { return []string{"low", "medium", "high"} }

// Deps 是 Bot 操作需要的服務元件；都可以是 nil（例：指令列只產生說明時）。
type Deps struct {
	// Stop 停止 Bot 正在進行的一輪（封存、刪除前呼叫）。
	Stop func(ctx context.Context, botID string)
	// Teardown 收掉 Bot 的電腦與引擎連線（刪除時呼叫；連資料區一起刪）。
	Teardown func(ctx context.Context, botID string) error
	// Active 回傳 Bot 正在進行的對話串與工作（Bot 建幫手、改自己資料時在那條對話記一行）；nil 時不記。
	Active func(botID string) (threadID, taskID string)
	// SoulChanged 在名稱、職稱、介紹或人設改了以後呼叫：把新的 SOUL.md 寫進 Bot 的電腦（C31）。可為 nil。
	SoulChanged func(ctx context.Context, botID string)
}

// View 是對外的 Bot 資料。
type View struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	Soul          string       `json:"soul"`
	Color         Color        `json:"color"`
	Shape         Shape        `json:"shape"`
	Eyes          Eyes         `json:"eyes,omitempty" desc:"平常的眼睛；空白是圓點"`
	Image         string       `json:"image,omitempty" desc:"頭像圖片（裁成形狀的 data URL）；有圖片時不疊眼睛"`
	Engine        string       `json:"engine"`
	Model         string       `json:"model" desc:"空白表示用預設模型；「<供應商>/<模型>」或只寫模型"`
	Fallback      string       `json:"fallback" desc:"備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback）"`
	Effort        string       `json:"effort"`
	ApprovalMode  ApprovalMode `json:"approval_mode"`
	MemoryEnabled bool         `json:"memory_enabled" desc:"記憶有沒有打開；關掉時 Bot 不載入、也不寫記憶"`
	Archived      bool         `json:"archived"`
	HostID        string       `json:"host_id" desc:"放在哪台主機（xbot.hosts.list）；空白＝第一次開機時自動擺放"`
	TeamID        string       `json:"team_id" desc:"所在團隊；空白是未分區"`
	ReportsTo     string       `json:"reports_to" desc:"向哪個 Bot 報告；空白是直接向你報告"`
	CanManage     bool         `json:"can_manage" desc:"團隊管理權限：可以替你建團隊、調整別的 Bot；每天可建 20 個 Bot"`
	Primary       bool         `json:"primary" desc:"你的主要 Bot（is_chief）"`
	CreatedBy     string       `json:"created_by,omitempty" desc:"誰建立的：owner、agent:<id>、bot:<id>"`
	UpdatedBy     string       `json:"updated_by,omitempty" desc:"最後是誰改的；代理改的介面會標「由 ○○（MCP）修改」"`
	CreatedAt     int64        `json:"created_at"`
	UpdatedAt     int64        `json:"updated_at"`
}

type avatar struct {
	Shape string `json:"shape,omitempty"`
	Eyes  string `json:"eyes,omitempty"`
	Image string `json:"image,omitempty"`
}

// ViewOf 把資料庫的 Bot 轉成對外的資料。
func ViewOf(b store.Bot) View {
	var a avatar
	_ = json.Unmarshal(b.Avatar, &a)
	return View{ID: b.ID, Name: b.Name, Title: b.Title, Description: b.Description, Soul: b.Soul,
		Color: Color(b.Color), Shape: Shape(a.Shape), Eyes: Eyes(a.Eyes), Image: a.Image, Engine: b.Engine, Model: b.Model, Fallback: first(b.Fallback), Effort: b.Effort,
		ApprovalMode: ApprovalMode(b.ApprovalMode), MemoryEnabled: b.MemoryEnabled, Archived: b.Archived, HostID: b.HostID,
		TeamID: b.TeamID, ReportsTo: b.ReportsTo, CanManage: b.CanManage, Primary: b.IsChief, CreatedBy: b.CreatedBy, UpdatedBy: b.UpdatedBy,
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

// ── 輸入輸出 ──────────────────────────────────────────

// ListIn 是 xbot.bots.list 的輸入。
type ListIn struct {
	IncludeArchived bool `json:"include_archived,omitempty" desc:"也列出已封存的 Bot"`
}

// ListOut 是 xbot.bots.list 的輸出。
type ListOut struct {
	Bots []View `json:"bots"`
}

// IDIn 指定一個 Bot。
type IDIn struct {
	ID string `json:"id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// CreateIn 是 xbot.bots.create 的輸入。
type CreateIn struct {
	Name         string       `json:"name" required:"true" max:"100" desc:"名稱，例：小秘"`
	Title        string       `json:"title,omitempty" max:"100" desc:"職稱，例：幕僚長"`
	Description  string       `json:"description,omitempty" max:"1000" desc:"一兩句話說明它負責什麼"`
	Soul         string       `json:"soul,omitempty" max:"4000" desc:"常駐指示（SOUL.md）"`
	Color        Color        `json:"color,omitempty" desc:"頭像顏色；不填自動挑"`
	Shape        Shape        `json:"shape,omitempty" desc:"頭像形狀；不填自動挑"`
	Eyes         Eyes         `json:"eyes,omitempty" desc:"頭像平常的眼睛；不填是圓點"`
	Engine       Engine       `json:"engine,omitempty" desc:"引擎；預設 hermes。claude-code、codex、gemini、opencode、grok 要再用 xbot.engines.link_credentials 同意使用你的訂閱"`
	Model        string       `json:"model,omitempty" max:"200" desc:"模型；不填用預設模型（xbot.models.get 看設定）"`
	Effort       Effort       `json:"effort,omitempty" desc:"思考強度；不填是 medium"`
	ApprovalMode ApprovalMode `json:"approval_mode,omitempty" desc:"權限等級；預設 ask（先問我）。設成其他等級要你核准"`
}

// UpdateIn 是 xbot.bots.update 的輸入：有給的欄位才改。
type UpdateIn struct {
	ID            string        `json:"id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Name          *string       `json:"name,omitempty" min:"1" max:"100"`
	Title         *string       `json:"title,omitempty" max:"100"`
	Description   *string       `json:"description,omitempty" max:"1000"`
	Soul          *string       `json:"soul,omitempty" max:"4000"`
	Color         *Color        `json:"color,omitempty" desc:"頭像顏色：green、blue… 或自選 #RRGGBB"`
	Shape         *Shape        `json:"shape,omitempty"`
	Eyes          *Eyes         `json:"eyes,omitempty" desc:"頭像平常的眼睛"`
	Image         *string       `json:"image,omitempty" blob:"true" max:"400000" desc:"頭像圖片：裁成形狀、256×256 的 data URL（PNG、JPEG、GIF、WebP）；空白字串改回吉祥物"`
	Engine        *Engine       `json:"engine,omitempty"`
	Model         *string       `json:"model,omitempty" max:"200" desc:"空白字串表示改回預設模型"`
	Fallback      *string       `json:"fallback,omitempty" max:"200" desc:"備援模型「<供應商>/<模型>」：主要模型忙碌或出錯時改用；空白字串表示用全域備援"`
	Effort        *Effort       `json:"effort,omitempty"`
	ApprovalMode  *ApprovalMode `json:"approval_mode,omitempty" desc:"改成 ask 以外的等級要你核准"`
	MemoryEnabled *bool         `json:"memory_enabled,omitempty" desc:"打開或關掉記憶（下次開機生效）"`
	HostID        *string       `json:"host_id,omitempty" resource:"host" max:"64" desc:"放在哪台主機（CL-1）；空白字串＝自動擺放。已經有電腦的 Bot 換主機要等搬家（CL-5）"`
	CanManage     *bool         `json:"can_manage,omitempty" desc:"團隊管理權限；打開要你核准"`
}

// ArchiveIn 是 xbot.bots.archive 的輸入。
type ArchiveIn struct {
	ID       string `json:"id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Archived *bool  `json:"archived,omitempty" desc:"false 表示取消封存；不填是封存"`
}

// DeleteOut 是 xbot.bots.delete 的輸出。
type DeleteOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted" desc:"預演時是 false"`
	Threads int    `json:"threads" desc:"會一起刪掉的對話串數"`
}

// Register 登錄 Bot 管理的操作。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.bots.list", Summary: "列出 Bot", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
			bs, err := call.DB.ListBots(ctx, in.IncludeArchived)
			if err != nil {
				return ListOut{}, err
			}
			out := ListOut{Bots: []View{}}
			for _, b := range bs {
				if call.Identity.CanAccess(ops.ResourceBot, b.ID) {
					out.Bots = append(out.Bots, ViewOf(b))
				}
			}
			return out, nil
		},
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.bots.get", Summary: "讀一個 Bot 的設定", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (View, error) {
			b, err := getBot(ctx, call.DB, in.ID)
			return ViewOf(b), err
		},
	})
	ops.Register(reg, ops.Op[CreateIn, View]{
		Name: "xbot.bots.create", Summary: "建立一個 Bot",
		Description: "新 Bot 的權限等級預設是「先問我」（SEC-1）。它的電腦在第一次對話時才開機。" +
			"代理建立的 Bot 記在它名下：只能碰自己建立的東西的代理（預設）也能碰它（MCP-8）。" +
			"Bot 自己也能建立幫手 Bot（LP-9）：缺少某種專長、使用者也同意時才建，一次一個；每 24 小時最多 3 個" +
			"（有團隊管理權限 20 個），全部最多 50 個；名字不能和現有的 Bot 重複。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe, ForBot: true,
		RiskOf: func(in CreateIn) ops.Risk { return modeRisk(in.ApprovalMode) },
		Handle: func(ctx context.Context, call *ops.Call, in CreateIn) (View, error) {
			if call.Identity.Kind == ops.KindBot {
				return d.createByBot(ctx, reg, call, in)
			}
			if err := CheckQuota(ctx, call.DB, nil, 1); err != nil {
				return View{}, err
			}
			return create(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[ProfileIn, ProfileOut]{
		Name: "xbot.bots.update_my_profile", Summary: "改自己的職稱或頭像",
		Description: "Bot 調整自己的職稱（60 字內）、頭像顏色或形狀。名字、人設、權限只有使用者能改。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in ProfileIn) (ProfileOut, error) {
			return d.updateProfile(ctx, reg, call, in)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, View]{
		Name: "xbot.bots.update", Summary: "修改一個 Bot 的設定",
		Description: "只改有給的欄位。把權限等級改成 ask 以外要你核准。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		RiskOf: func(in UpdateIn) ops.Risk {
			if in.CanManage != nil && *in.CanManage {
				return ops.RiskConfirm // 讓 Bot 管理別的 Bot 是放寬權限
			}
			if in.ApprovalMode == nil {
				return ops.RiskSafe
			}
			return modeRisk(*in.ApprovalMode)
		},
		Handle: func(ctx context.Context, call *ops.Call, in UpdateIn) (View, error) {
			b, err := getBot(ctx, call.DB, in.ID)
			if err != nil {
				return View{}, err
			}
			before := soulOf(b)
			if err := apply(&b, in); err != nil {
				return View{}, err
			}
			box, err := placeHost(ctx, call.DB, &b, in.HostID)
			if err != nil {
				return View{}, err
			}
			b.UpdatedBy = call.Identity.Actor()
			if call.DryRun {
				return ViewOf(b), nil
			}
			if box != nil {
				if err := call.DB.UpsertBox(ctx, *box); err != nil {
					return View{}, err
				}
			}
			b, err = call.DB.UpdateBot(ctx, b)
			if err == nil && d.SoulChanged != nil && soulOf(b) != before {
				d.SoulChanged(ctx, b.ID)
			}
			return ViewOf(b), err
		},
	})
	ops.Register(reg, ops.Op[ArchiveIn, View]{
		Name: "xbot.bots.archive", Summary: "封存（或取消封存）一個 Bot",
		Description: "封存的 Bot 不會出現在清單，也不能對話；資料都保留，可以取消封存。封存時會先停下它正在做的事。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ArchiveIn) (View, error) {
			b, err := getBot(ctx, call.DB, in.ID)
			if err != nil {
				return View{}, err
			}
			archived := in.Archived == nil || *in.Archived
			b.Archived = archived
			if call.DryRun {
				return ViewOf(b), nil
			}
			if archived && d.Stop != nil {
				d.Stop(ctx, b.ID)
			}
			if err := call.DB.SetBotArchived(ctx, b.ID, archived); err != nil {
				return View{}, err
			}
			b, err = call.DB.GetBot(ctx, b.ID)
			return ViewOf(b), err
		},
	})
	ops.Register(reg, ops.Op[IDIn, DeleteOut]{
		Name: "xbot.bots.delete", Summary: "刪除一個 Bot",
		Description: "連同它的電腦（含檔案）、對話、任務一起刪除，無法復原。一定要你本人核准，不能代為核准。",
		Scope:       ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (DeleteOut, error) {
			b, err := getBot(ctx, call.DB, in.ID)
			if err != nil {
				return DeleteOut{}, err
			}
			threads, err := call.DB.ListThreads(ctx, b.ID, true)
			if err != nil {
				return DeleteOut{}, err
			}
			out := DeleteOut{ID: b.ID, Name: b.Name, Threads: len(threads)}
			if call.DryRun {
				return out, nil
			}
			if d.Stop != nil {
				d.Stop(ctx, b.ID)
			}
			if d.Teardown != nil {
				if err := d.Teardown(ctx, b.ID); err != nil {
					return out, err
				}
			}
			if err := call.DB.DeleteBot(ctx, b.ID); err != nil {
				return out, err
			}
			out.Deleted = true
			return out, nil
		},
	})
}

func modeRisk(m ApprovalMode) ops.Risk {
	if m == "" || m == ModeAsk {
		return ops.RiskSafe
	}
	return ops.RiskConfirm // 放寬 Bot 的權限要你核准
}

func getBot(ctx context.Context, db *store.DB, id string) (store.Bot, error) {
	b, err := db.GetBot(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return b, ops.NotFound("找不到這個 Bot")
	}
	return b, err
}

func create(ctx context.Context, call *ops.Call, in CreateIn) (View, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return View{}, ops.InvalidInput(ops.FieldError{Field: "name", Message: "不能是空的"})
	}
	all, err := call.DB.ListBots(ctx, true)
	if err != nil {
		return View{}, err
	}
	// 沒指定時依現有 Bot 數輪流挑顏色與形狀，新 Bot 一眼就和別人不一樣。
	color, shape := string(in.Color), string(in.Shape)
	if !ValidColor(color) {
		return View{}, colorErr()
	}
	if color == "" {
		color = Colors[len(all)%len(Colors)]
	}
	if shape == "" {
		shape = Shapes[len(all)%len(Shapes)]
	}
	av, _ := json.Marshal(avatar{Shape: shape, Eyes: string(in.Eyes)})
	b := store.Bot{Name: name, Title: strings.TrimSpace(in.Title), Description: strings.TrimSpace(in.Description), Soul: in.Soul,
		Color: color, Avatar: av, Engine: string(in.Engine), Model: strings.TrimSpace(in.Model), Effort: string(in.Effort),
		ApprovalMode: string(in.ApprovalMode), MemoryEnabled: true, CreatedBy: call.Identity.Actor()}
	if b.Engine == "" {
		b.Engine = string(EngineHermes)
	}
	if b.ApprovalMode == "" {
		b.ApprovalMode = string(ModeAsk) // SEC-1
	}
	if call.DryRun {
		return ViewOf(b), nil
	}
	b, err = call.DB.CreateBot(ctx, b)
	return ViewOf(b), err
}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// placeHost 換 Bot 的主機（CL-1）。電腦已經建在別台時不能直接換（那是搬家，CL-5）；
// 電腦列還在但容器沒了（state none）時，一起改電腦列的主機，下次開機就建在新主機。回傳要寫回的電腦列。
func placeHost(ctx context.Context, db *store.DB, b *store.Bot, hostID *string) (*store.Box, error) {
	if hostID == nil {
		return nil, nil
	}
	id := strings.TrimSpace(*hostID)
	if id != "" {
		if _, err := db.GetHost(ctx, id); errors.Is(err, store.ErrNotFound) {
			return nil, ops.InvalidInput(ops.FieldError{Field: "host_id", Message: "找不到這台主機"})
		} else if err != nil {
			return nil, err
		}
	}
	b.HostID = id
	box, err := db.GetBox(ctx, b.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if id == "" || box.HostID == id {
		return nil, nil
	}
	if box.ContainerID != "" || box.State != "none" {
		return nil, ops.Conflict("這個 Bot 的電腦已經在另一台主機上；換主機要搬家（之後的版本會支援），或先刪掉它的電腦")
	}
	box.HostID = id
	return &box, nil
}

func apply(b *store.Bot, in UpdateIn) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return ops.InvalidInput(ops.FieldError{Field: "name", Message: "不能是空的"})
		}
		b.Name = n
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&b.Title, in.Title)
	set(&b.Description, in.Description)
	set(&b.Model, in.Model)
	if in.Fallback != nil {
		b.Fallback = nil
		if f := strings.TrimSpace(*in.Fallback); f != "" {
			if p, m, ok := strings.Cut(f, "/"); !ok || p == "" || m == "" {
				return ops.InvalidInput(ops.FieldError{Field: "fallback", Message: "要寫成「<供應商>/<模型>」"})
			}
			b.Fallback = []string{f}
		}
	}
	if in.Soul != nil {
		b.Soul = *in.Soul
	}
	if in.Color != nil {
		if !ValidColor(string(*in.Color)) {
			return colorErr()
		}
		b.Color = string(*in.Color)
	}
	if in.Shape != nil || in.Eyes != nil || in.Image != nil {
		a := map[string]any{} // 保留頭像的其他欄位
		_ = json.Unmarshal(b.Avatar, &a)
		if a == nil {
			a = map[string]any{}
		}
		if in.Shape != nil {
			a["shape"] = string(*in.Shape)
		}
		if in.Eyes != nil {
			a["eyes"] = string(*in.Eyes)
		}
		if in.Image != nil {
			img := strings.TrimSpace(*in.Image)
			if err := ValidImage(img); err != nil {
				return err
			}
			if img == "" {
				delete(a, "image")
			} else {
				a["image"] = img
			}
		}
		b.Avatar, _ = json.Marshal(a)
	}
	if in.Engine != nil {
		b.Engine = string(*in.Engine)
	}
	if in.Effort != nil {
		b.Effort = string(*in.Effort)
	}
	if in.ApprovalMode != nil {
		b.ApprovalMode = string(*in.ApprovalMode)
	}
	if in.MemoryEnabled != nil {
		b.MemoryEnabled = *in.MemoryEnabled
	}
	if in.CanManage != nil {
		b.CanManage = *in.CanManage
	}
	return nil
}

// ValidImage 檢查頭像圖片：空白（改回吉祥物），或 PNG、JPEG、GIF、WebP 的 base64 data URL，長度在上限內。
func ValidImage(img string) error {
	if img == "" {
		return nil
	}
	if len(img) > limits.AvatarImageBytes {
		return ops.InvalidInput(ops.FieldError{Field: "image", Message: "圖片太大；請裁小一點再試"})
	}
	head, data, ok := strings.Cut(img, ",")
	if !ok || data == "" {
		return ops.InvalidInput(ops.FieldError{Field: "image", Message: "要是圖片的 data URL"})
	}
	switch head {
	case "data:image/png;base64", "data:image/jpeg;base64", "data:image/gif;base64", "data:image/webp;base64":
	default:
		return ops.InvalidInput(ops.FieldError{Field: "image", Message: "只接受 PNG、JPEG、GIF、WebP"})
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return ops.InvalidInput(ops.FieldError{Field: "image", Message: "圖片內容不對"})
	}
	return nil
}

// soulOf 是會寫進 SOUL.md 的欄位。
func soulOf(b store.Bot) [4]string { return [4]string{b.Name, b.Title, b.Description, b.Soul} }
