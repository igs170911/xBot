// Package bots 是 Bot 管理的操作（PRD §6.2 BT-*、§22.3 bots）：list、get、create、update、archive、delete。
package bots

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

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

// EnumValues 讓 JSON Schema 列出可用值。
func (Color) EnumValues() []string { return Colors }

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

// Engine 是 Bot 用的引擎。目前只有 Hermes（ACP 驅動在 E12）。
type Engine string

// EngineHermes 是預設引擎。
const EngineHermes Engine = "hermes"

// EnumValues 讓 JSON Schema 列出可用值。
func (Engine) EnumValues() []string { return []string{"hermes"} }

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
}

// View 是對外的 Bot 資料。
type View struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Soul         string       `json:"soul"`
	Color        Color        `json:"color"`
	Shape        Shape        `json:"shape"`
	Engine       string       `json:"engine"`
	Model        string       `json:"model" desc:"空白表示用預設模型；「<供應商>/<模型>」或只寫模型"`
	Effort       string       `json:"effort"`
	ApprovalMode ApprovalMode `json:"approval_mode"`
	Archived     bool         `json:"archived"`
	CreatedAt    int64        `json:"created_at"`
	UpdatedAt    int64        `json:"updated_at"`
}

type avatar struct {
	Shape string `json:"shape,omitempty"`
}

// ViewOf 把資料庫的 Bot 轉成對外的資料。
func ViewOf(b store.Bot) View {
	var a avatar
	_ = json.Unmarshal(b.Avatar, &a)
	return View{ID: b.ID, Name: b.Name, Title: b.Title, Description: b.Description, Soul: b.Soul,
		Color: Color(b.Color), Shape: Shape(a.Shape), Engine: b.Engine, Model: b.Model, Effort: b.Effort,
		ApprovalMode: ApprovalMode(b.ApprovalMode), Archived: b.Archived, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
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
	Engine       Engine       `json:"engine,omitempty" desc:"引擎；預設 hermes"`
	Model        string       `json:"model,omitempty" max:"200" desc:"模型；不填用預設模型（xbot.models.get 看設定）"`
	Effort       Effort       `json:"effort,omitempty" desc:"思考強度；不填是 medium"`
	ApprovalMode ApprovalMode `json:"approval_mode,omitempty" desc:"權限等級；預設 ask（先問我）。設成其他等級要你核准"`
}

// UpdateIn 是 xbot.bots.update 的輸入：有給的欄位才改。
type UpdateIn struct {
	ID           string        `json:"id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Name         *string       `json:"name,omitempty" min:"1" max:"100"`
	Title        *string       `json:"title,omitempty" max:"100"`
	Description  *string       `json:"description,omitempty" max:"1000"`
	Soul         *string       `json:"soul,omitempty" max:"4000"`
	Color        *Color        `json:"color,omitempty"`
	Shape        *Shape        `json:"shape,omitempty"`
	Engine       *Engine       `json:"engine,omitempty"`
	Model        *string       `json:"model,omitempty" max:"200" desc:"空白字串表示改回預設模型"`
	Effort       *Effort       `json:"effort,omitempty"`
	ApprovalMode *ApprovalMode `json:"approval_mode,omitempty" desc:"改成 ask 以外的等級要你核准"`
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
			"受資源範圍限制的代理建立的 Bot，會自動加進它能碰的 Bot。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		RiskOf: func(in CreateIn) ops.Risk { return modeRisk(in.ApprovalMode) },
		Handle: func(ctx context.Context, call *ops.Call, in CreateIn) (View, error) {
			return create(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, View]{
		Name: "xbot.bots.update", Summary: "修改一個 Bot 的設定",
		Description: "只改有給的欄位。把權限等級改成 ask 以外要你核准。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		RiskOf: func(in UpdateIn) ops.Risk {
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
			if err := apply(&b, in); err != nil {
				return View{}, err
			}
			if call.DryRun {
				return ViewOf(b), nil
			}
			b, err = call.DB.UpdateBot(ctx, b)
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
	if color == "" {
		color = Colors[len(all)%len(Colors)]
	}
	if shape == "" {
		shape = Shapes[len(all)%len(Shapes)]
	}
	av, _ := json.Marshal(avatar{Shape: shape})
	b := store.Bot{Name: name, Title: strings.TrimSpace(in.Title), Description: strings.TrimSpace(in.Description), Soul: in.Soul,
		Color: color, Avatar: av, Engine: string(in.Engine), Model: strings.TrimSpace(in.Model), Effort: string(in.Effort),
		ApprovalMode: string(in.ApprovalMode), MemoryEnabled: true}
	if b.Engine == "" {
		b.Engine = string(EngineHermes)
	}
	if b.ApprovalMode == "" {
		b.ApprovalMode = string(ModeAsk) // SEC-1
	}
	if call.DryRun {
		return ViewOf(b), nil
	}
	err = call.DB.Tx(ctx, func(q *store.Queries) error {
		if b, err = q.CreateBot(ctx, b); err != nil {
			return err
		}
		return grantCreator(ctx, q, call.Identity, b.ID)
	})
	return ViewOf(b), err
}

// grantCreator：受資源範圍限制的代理建立的 Bot，加進它能碰的 Bot（MCP-8「預設只能碰自己建立的東西」）。
func grantCreator(ctx context.Context, q *store.Queries, id ops.Identity, botID string) error {
	if id.Kind != ops.KindAgent || id.Resources.Bots == nil {
		return nil
	}
	t, err := q.GetAgentToken(ctx, id.ID)
	if err != nil {
		return err
	}
	var res ops.Resources
	if len(t.Resources) > 0 {
		if err := json.Unmarshal(t.Resources, &res); err != nil {
			return err
		}
	}
	if res.Bots == nil || slices.Contains(res.Bots, botID) {
		return nil
	}
	res.Bots = append(res.Bots, botID)
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return q.UpdateAgentTokenScopes(ctx, t.ID, t.Scopes, raw)
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
	if in.Soul != nil {
		b.Soul = *in.Soul
	}
	if in.Color != nil {
		b.Color = string(*in.Color)
	}
	if in.Shape != nil {
		a := map[string]any{} // 保留頭像的其他欄位
		_ = json.Unmarshal(b.Avatar, &a)
		if a == nil {
			a = map[string]any{}
		}
		a["shape"] = string(*in.Shape)
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
	return nil
}
