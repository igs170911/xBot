package skills

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Service 是技能操作需要的元件。
type Service struct {
	// Locate 找 Bot 的電腦（runtime.Computers.BotFS）。
	Locate boxfs.Locator
	// Library 是工作區技能庫（<data>/skills）。
	Library *Library
}

// ── 輸入輸出 ──────────────────────────────────────────

// ListIn 是 xbot.skills.list 的輸入。
type ListIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"看這個 Bot 的技能；不填看工作區技能庫（所有 Bot 共用）。Bot 自己呼叫時就是自己"`
	Place Place  `json:"place,omitempty" desc:"只列這個位置的技能；Bot 找技能庫裡的技能用 library"`
	Query string `json:"query,omitempty" max:"200" desc:"關鍵字（名稱、用途）；有給時最多回 8 個最相關的"`
}

// ListOut 是 xbot.skills.list 的輸出。
type ListOut struct {
	Skills   []LibSkill     `json:"skills"`
	Counts   map[string]int `json:"counts" desc:"各位置的數量：list、library、off"`
	Computer bool           `json:"computer" desc:"Bot 已經有電腦（看 Bot 的技能時；沒有時清單是空的，第一次開機會帶入內建技能）"`
	Note     string         `json:"note,omitempty"`
}

// GetIn 是 xbot.skills.get 的輸入。
type GetIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot 的技能；不填是工作區技能庫"`
	ID    string `json:"id" required:"true" max:"200" desc:"技能 id（也可以給名稱）"`
}

// CreateIn 是 xbot.skills.create 的輸入。
type CreateIn struct {
	BotID        string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"加到這個 Bot；不填加到工作區技能庫"`
	Name         string `json:"name" required:"true" max:"64" desc:"技能名稱，例：週報彙整"`
	Description  string `json:"description" required:"true" max:"1024" desc:"什麼時候使用（一句話）"`
	Instructions string `json:"instructions" required:"true" max:"100000" desc:"怎麼做（步驟）"`
	Place        Place  `json:"place,omitempty" desc:"Bot 的技能放哪裡；預設 list"`
}

// UpdateIn 是 xbot.skills.update 的輸入：有給的欄位才改。
type UpdateIn struct {
	BotID        string  `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot 的技能；不填是工作區技能庫"`
	ID           string  `json:"id" required:"true" max:"200"`
	Name         *string `json:"name,omitempty" min:"1" max:"64"`
	Description  *string `json:"description,omitempty" max:"1024"`
	Instructions *string `json:"instructions,omitempty" min:"1" max:"100000"`
}

// EnableIn 是 xbot.skills.enable 的輸入。
type EnableIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot 的技能；不填是工作區技能庫"`
	ID    string `json:"id" required:"true" max:"200"`
	Place Place  `json:"place,omitempty" desc:"Bot 的技能放哪裡：list（預設，每次對話都帶上）或 library（需要時才載入）"`
}

// IDIn 指定一個技能。
type IDIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot 的技能；不填是工作區技能庫"`
	ID    string `json:"id" required:"true" max:"200"`
}

// ImportIn 是 xbot.skills.import 的輸入。
type ImportIn struct {
	BotID         string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"匯入到這個 Bot；不填匯入到工作區技能庫"`
	Filename      string `json:"filename" required:"true" max:"200" desc:"檔名：.md（SKILL.md）或 .zip（含 SKILL.md 與附帶的檔案）"`
	Content       string `json:"content,omitempty" max:"200000" desc:".md 的內容（文字）"`
	ContentBase64 string `json:"content_base64,omitempty" max:"11500000" desc:".zip（或 .md）的內容，base64"`
}

// InstallIn 是 xbot.skills.install_to_bot 的輸入。
type InstallIn struct {
	ID    string `json:"id" required:"true" max:"200" desc:"工作區技能庫的技能 id"`
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"裝到哪個 Bot"`
}

// ProposeIn 是 xbot.skills.propose 的輸入（Bot 用）。
type ProposeIn struct {
	Name         string `json:"name" required:"true" max:"64" desc:"技能名稱"`
	Description  string `json:"description" required:"true" max:"1024" desc:"什麼時候使用（一句話）"`
	Instructions string `json:"instructions" required:"true" max:"100000" desc:"怎麼做（步驟）"`
	Reason       string `json:"reason,omitempty" max:"500" desc:"為什麼值得存成技能（例：這次做了 12 步才完成）"`
}

// ProposeOut 是 xbot.skills.propose 的輸出。
type ProposeOut struct {
	ApprovalID string `json:"approval_id,omitempty"`
	Status     string `json:"status" desc:"pending：等使用者決定；approved：已加進你的技能"`
	Skill      *Skill `json:"skill,omitempty"`
	Note       string `json:"note"`
}

// Register 登錄 xbot.skills.* 操作。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	need := func() error {
		if s == nil {
			return ops.Internal(errors.New("技能服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.skills.list", Summary: "列出技能",
		Description: "帶 bot_id 是那個 Bot 的技能（技能清單／技能庫／已停用）；不帶是工作區技能庫（所有 Bot 共用，可裝到 Bot 身上）。" +
			"Bot 自己呼叫時看自己的；技能庫裡的技能平常不帶進對話，用 place=library 加 query 找，再用 xbot.skills.get 載入步驟。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			return s.list(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[GetIn, LibSkill]{
		Name: "xbot.skills.get", Summary: "讀一個技能的完整內容",
		Description: "名稱、什麼時候使用、怎麼做、附帶的檔案。Bot 用它載入技能庫裡的技能步驟。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in GetIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.get(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[CreateIn, LibSkill]{
		Name: "xbot.skills.create", Summary: "新增一個技能",
		Description: "寫給 Bot 的做法：名稱、什麼時候使用、怎麼做。帶 bot_id 加到那個 Bot（下一個對話開始生效），不帶加到工作區技能庫。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in CreateIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.create(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, LibSkill]{
		Name: "xbot.skills.update", Summary: "修改一個技能",
		Description: "只改有給的欄位。SKILL.md 其他的設定（Hermes 的 platforms、tags 等）保留；位置不變。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.update(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[EnableIn, LibSkill]{
		Name: "xbot.skills.enable", Summary: "打開一個技能",
		Description: "Bot 的技能可以放到技能清單（每次對話帶上名稱與用途）或技能庫（需要時才載入）。從外面匯入的技能請先看過內容再打開。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in EnableIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			p := in.Place
			if p == "" {
				p = PlaceList
			}
			if p == PlaceOff {
				return LibSkill{}, ops.InvalidInput(ops.FieldError{Field: "place", Message: "要停用請用 xbot.skills.disable"})
			}
			return s.setPlace(ctx, call, in.BotID, in.ID, p)
		},
	})
	ops.Register(reg, ops.Op[IDIn, LibSkill]{
		Name: "xbot.skills.disable", Summary: "停用一個技能",
		Description: "Bot 不會再用它；內容保留，可以再打開。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.setPlace(ctx, call, in.BotID, in.ID, PlaceOff)
		},
	})
	ops.Register(reg, ops.Op[IDIn, LibSkill]{
		Name: "xbot.skills.delete", Summary: "刪除一個技能",
		Description: "連同附帶的檔案一起刪掉，無法復原（只想先不用請停用）。要你本人核准。刪 Bot 的技能時，它的電腦沒開會先開機。",
		Scope:       ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.delete(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[ImportIn, LibSkill]{
		Name: "xbot.skills.import", Summary: "匯入一個技能（預設停用）",
		Description: "從 .md（SKILL.md）或 .zip 匯入。格式不對會說明哪裡有問題。匯入的技能一律先停用：看過內容（review 列出會執行的指令與附帶的程式）再打開。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ImportIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.importSkill(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[InstallIn, LibSkill]{
		Name: "xbot.skills.install_to_bot", Summary: "把技能庫的技能裝到一個 Bot 身上",
		Description: "複製一份到 Bot 的技能（之後各改各的）。技能庫裡停用的技能裝上去也是停用。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in InstallIn) (LibSkill, error) {
			if err := need(); err != nil {
				return LibSkill{}, err
			}
			return s.install(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[ProposeIn, ProposeOut]{
		Name: "xbot.skills.propose", Summary: "提議把一個做法存成你的技能",
		Description: "做完一件多步驟、以後還會再做的事時，把做法整理成技能提議給使用者。會出現在「待我處理」，使用者核准後才加進你的技能。" +
			"不要用它改或刪使用者寫的技能。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true,
		Handle: func(ctx context.Context, call *ops.Call, in ProposeIn) (ProposeOut, error) {
			if err := need(); err != nil {
				return ProposeOut{}, err
			}
			return s.propose(ctx, reg, call, in)
		},
	})
}

// bot 找 Bot 的技能（create：沒有電腦時先建立）。
func (s *Service) bot(ctx context.Context, call *ops.Call, botID string, create bool) (Bot, error) {
	if _, err := call.DB.GetBot(ctx, botID); errors.Is(err, store.ErrNotFound) {
		return Bot{}, ops.NotFound("找不到這個 Bot")
	} else if err != nil {
		return Bot{}, err
	}
	f, err := s.Locate(ctx, botID, create)
	if err != nil {
		return Bot{}, err
	}
	return Bot{FS: f, BotID: botID}, nil
}

func asOps(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return ops.NotFound(err.Error())
	case errors.Is(err, ErrExists):
		return ops.Conflict(err.Error())
	case err == nil:
		return nil
	}
	var oe *ops.Error
	if errors.As(err, &oe) || errors.Is(err, boxfs.ErrNoComputer) {
		return err
	}
	if msg := err.Error(); isValidation(msg) {
		return ops.InvalidInput(ops.FieldError{Message: msg})
	}
	return err
}

// isValidation：Validate／ParseImport 的錯誤都是給人看的說明（不是系統錯誤）。
func isValidation(msg string) bool {
	for _, k := range []string{"技能", "SKILL.md", "frontmatter", ".zip", "檔案太大", "解開後", "檔名", "路徑", "什麼時候使用"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

func lib(s Skill) LibSkill { return LibSkill{Skill: s} }

func count(list []LibSkill) map[string]int {
	out := map[string]int{"list": 0, "library": 0, "off": 0}
	for _, s := range list {
		out[string(s.Place)]++
	}
	return out
}

func (s *Service) list(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
	isBot := call.Identity.Kind == ops.KindBot
	botID := in.BotID
	if isBot {
		botID = call.Identity.ID
	}
	var all []LibSkill
	out := ListOut{}
	if botID == "" {
		l, err := s.Library.List()
		if err != nil {
			return out, err
		}
		all = l
	} else {
		b, err := s.bot(ctx, call, botID, false)
		if errors.Is(err, boxfs.ErrNoComputer) {
			return ListOut{Skills: []LibSkill{}, Counts: count(nil), Note: "這個 Bot 還沒有電腦；第一次開機會帶入內建技能。"}, nil
		}
		if err != nil {
			return out, err
		}
		list, err := b.List(ctx)
		if err != nil {
			return out, err
		}
		for _, x := range list {
			all = append(all, lib(x))
		}
		out.Computer = true
	}
	out.Counts = count(all)
	var picked []Skill
	for _, x := range all {
		if in.Place == "" || x.Place == in.Place {
			picked = append(picked, x.Skill)
		}
	}
	if strings.TrimSpace(in.Query) != "" {
		picked = Search(picked, in.Query)
	}
	byID := map[string]LibSkill{}
	for _, x := range all {
		byID[x.ID] = x
	}
	out.Skills = []LibSkill{}
	for _, p := range picked {
		x := byID[p.ID]
		if isBot {
			x.Instructions = "" // 只給名稱與用途；要步驟用 xbot.skills.get
		}
		out.Skills = append(out.Skills, x)
	}
	if isBot {
		if len(out.Skills) == 0 {
			out.Note = "沒有相符的技能；換個關鍵字，或不靠技能直接做。"
		} else {
			out.Note = "用 xbot.skills.get 載入完整步驟。"
		}
	}
	return out, nil
}

func (s *Service) get(ctx context.Context, call *ops.Call, in GetIn) (LibSkill, error) {
	isBot := call.Identity.Kind == ops.KindBot
	botID := in.BotID
	if isBot {
		botID = call.Identity.ID
	}
	if botID == "" {
		x, err := s.Library.Get(in.ID)
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, botID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return LibSkill{}, ops.NotFound("找不到這個技能")
	}
	if err != nil {
		return LibSkill{}, err
	}
	x, err := b.Get(ctx, in.ID)
	if err != nil {
		return LibSkill{}, asOps(err)
	}
	if isBot {
		if x.Place == PlaceOff {
			return LibSkill{}, ops.Conflict("使用者停用了這個技能")
		}
		if utf8.RuneCountInString(x.Instructions) > limits.SkillLibraryText {
			x.Instructions = string([]rune(x.Instructions)[:limits.SkillLibraryText]) + "\n…（太長，後面省略）"
		}
	}
	return lib(x), nil
}

func (s *Service) create(ctx context.Context, call *ops.Call, in CreateIn) (LibSkill, error) {
	d := Doc{Name: oneLine(in.Name), Description: oneLine(in.Description), Instructions: in.Instructions}
	if err := d.Validate(); err != nil {
		return LibSkill{}, ops.InvalidInput(ops.FieldError{Message: err.Error()})
	}
	if call.DryRun {
		return LibSkill{Skill: Skill{Name: d.Name, Description: d.Description, Instructions: d.Instructions, Place: PlaceList, Enabled: true, Source: "custom"}}, nil
	}
	if in.BotID == "" {
		x, err := s.Library.Create(d, nil, libMetaData{Enabled: true, Source: "custom", CreatedBy: call.Identity.Actor()})
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, in.BotID, true)
	if err != nil {
		return LibSkill{}, err
	}
	x, err := b.Create(ctx, Edit{Name: &d.Name, Description: &d.Description, Instructions: &d.Instructions, Place: in.Place})
	return lib(x), asOps(err)
}

func (s *Service) update(ctx context.Context, call *ops.Call, in UpdateIn) (LibSkill, error) {
	e := Edit{Name: in.Name, Description: in.Description, Instructions: in.Instructions}
	if in.BotID == "" {
		if call.DryRun {
			x, err := s.Library.Get(in.ID)
			return x, asOps(err)
		}
		x, err := s.Library.Update(in.ID, e)
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, in.BotID, !call.DryRun)
	if err != nil {
		if errors.Is(err, boxfs.ErrNoComputer) {
			return LibSkill{}, ops.NotFound("找不到這個技能")
		}
		return LibSkill{}, err
	}
	if call.DryRun {
		x, err := b.Get(ctx, in.ID)
		return lib(x), asOps(err)
	}
	x, err := b.Update(ctx, in.ID, e)
	return lib(x), asOps(err)
}

func (s *Service) setPlace(ctx context.Context, call *ops.Call, botID, id string, p Place) (LibSkill, error) {
	if botID == "" {
		if p == PlaceLibrary {
			return LibSkill{}, ops.InvalidInput(ops.FieldError{Field: "place", Message: "工作區技能庫的技能只有開和關"})
		}
		if call.DryRun {
			x, err := s.Library.Get(id)
			return x, asOps(err)
		}
		x, err := s.Library.SetEnabled(id, p != PlaceOff)
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, botID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return LibSkill{}, ops.NotFound("找不到這個技能")
	}
	if err != nil {
		return LibSkill{}, err
	}
	if call.DryRun {
		x, err := b.Get(ctx, id)
		return lib(x), asOps(err)
	}
	x, err := b.SetPlace(ctx, id, p)
	return lib(x), asOps(err)
}

func (s *Service) delete(ctx context.Context, call *ops.Call, in IDIn) (LibSkill, error) {
	if in.BotID == "" {
		if call.DryRun {
			x, err := s.Library.Get(in.ID)
			return x, asOps(err)
		}
		x, err := s.Library.Delete(in.ID)
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, in.BotID, false)
	if errors.Is(err, boxfs.ErrNoComputer) {
		return LibSkill{}, ops.NotFound("找不到這個技能")
	}
	if err != nil {
		return LibSkill{}, err
	}
	if call.DryRun {
		x, err := b.Get(ctx, in.ID)
		return lib(x), asOps(err)
	}
	x, err := b.Delete(ctx, in.ID)
	return lib(x), asOps(err)
}

func (s *Service) importSkill(ctx context.Context, call *ops.Call, in ImportIn) (LibSkill, error) {
	data := []byte(in.Content)
	if in.ContentBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil {
			return LibSkill{}, ops.InvalidInput(ops.FieldError{Field: "content_base64", Message: "不是正確的 base64"})
		}
		data = b
	}
	if len(data) == 0 {
		return LibSkill{}, ops.InvalidInput(ops.FieldError{Field: "content", Message: "要給 content 或 content_base64"})
	}
	p, err := ParseImport(in.Filename, data)
	if err != nil {
		return LibSkill{}, ops.InvalidInput(ops.FieldError{Message: err.Error()})
	}
	var names []string
	for _, f := range p.Files {
		names = append(names, f.Name)
	}
	if call.DryRun {
		return LibSkill{Skill: Skill{Name: p.Doc.Name, Description: p.Doc.Description, Instructions: p.Doc.Instructions, Place: PlaceOff,
			Source: "imported", Files: names}, Origin: in.Filename, Review: review(p.Doc.Instructions, names)}, nil
	}
	if in.BotID == "" {
		x, err := s.Library.Create(p.Doc, p.Files, libMetaData{Enabled: false, Source: "imported", Origin: in.Filename, CreatedBy: call.Identity.Actor()})
		return x, asOps(err)
	}
	b, err := s.bot(ctx, call, in.BotID, true)
	if err != nil {
		return LibSkill{}, err
	}
	x, err := b.Create(ctx, Edit{Name: &p.Doc.Name, Description: &p.Doc.Description, Instructions: &p.Doc.Instructions, Place: PlaceOff,
		Source: "imported", Files: p.Files})
	if err != nil {
		return LibSkill{}, asOps(err)
	}
	out := lib(x)
	out.Origin, out.Review = in.Filename, review(x.Instructions, x.Files)
	return out, nil
}

func (s *Service) install(ctx context.Context, call *ops.Call, in InstallIn) (LibSkill, error) {
	src, files, err := s.Library.Bundle(in.ID)
	if err != nil {
		return LibSkill{}, asOps(err)
	}
	place := PlaceList
	if !src.Enabled {
		place = PlaceOff
	}
	if call.DryRun {
		x := src
		x.Place, x.Enabled = place, place != PlaceOff
		return x, nil
	}
	b, err := s.bot(ctx, call, in.BotID, true)
	if err != nil {
		return LibSkill{}, err
	}
	x, err := b.Create(ctx, Edit{Name: &src.Name, Description: &src.Description, Instructions: &src.Instructions, Place: place,
		Source: "imported", Files: files})
	return lib(x), asOps(err)
}

const proposeNote = "已送給使用者核准。核准後會加進你的技能，從下一個對話開始可以用；不要自己寫進 /hermes/skills。"

func (s *Service) propose(ctx context.Context, reg *ops.Registry, call *ops.Call, in ProposeIn) (ProposeOut, error) {
	if call.Identity.Kind != ops.KindBot {
		return ProposeOut{}, ops.Forbidden("這是給 Bot 自己用的操作；要新增技能請用 xbot.skills.create")
	}
	botID := call.Identity.ID
	d := Doc{Name: oneLine(in.Name), Description: oneLine(in.Description), Instructions: in.Instructions}
	if err := d.Validate(); err != nil {
		return ProposeOut{}, ops.InvalidInput(ops.FieldError{Message: err.Error()})
	}
	if call.ApprovalID != "" {
		// 使用者核准了：登錄表以這個核准再執行一次。
		b, err := s.bot(ctx, call, botID, true)
		if err != nil {
			return ProposeOut{}, err
		}
		x, err := b.Create(ctx, Edit{Name: &d.Name, Description: &d.Description, Instructions: &d.Instructions, Source: "proposed"})
		if err != nil {
			return ProposeOut{}, asOps(err)
		}
		return ProposeOut{ApprovalID: call.ApprovalID, Status: "approved", Skill: &x, Note: "使用者核准了，已加進你的技能，下一個對話開始生效。"}, nil
	}
	pending, err := call.DB.ListApprovals(ctx, "pending")
	if err != nil {
		return ProposeOut{}, err
	}
	open := 0
	for _, a := range pending {
		if a.RequestedBy != call.Identity.Actor() || a.Action != call.Op.Name {
			continue
		}
		open++
		var prev ProposeIn
		if json.Unmarshal(a.Input, &prev) == nil && oneLine(prev.Name) == d.Name {
			return ProposeOut{ApprovalID: a.ID, Status: "pending", Note: "同名的技能已經在等使用者核准，不需要重送。"}, nil
		}
	}
	if open >= limits.ApprovalPendingMax {
		return ProposeOut{}, ops.Limit(fmt.Sprintf("你已經有 %d 個技能提議在等核准，先等使用者處理", limits.ApprovalPendingMax))
	}
	if call.DryRun {
		return ProposeOut{Status: "pending", Note: "會送給使用者核准。"}, nil
	}
	raw, err := json.Marshal(ProposeIn{Name: d.Name, Description: d.Description, Instructions: d.Instructions, Reason: oneLine(in.Reason)})
	if err != nil {
		return ProposeOut{}, err
	}
	body := "用途：" + d.Description
	if r := oneLine(in.Reason); r != "" {
		body += "\n為什麼：" + r
	}
	body += "\n\n" + clip(d.Instructions, 1200)
	t, err := reg.RequestApproval(ctx, call, raw, botID, ops.ApprovalCard{
		Title: fmt.Sprintf("%s 想學一個技能：%s", call.Identity.DisplayName(), d.Name), Body: body,
	})
	if err != nil {
		return ProposeOut{}, err
	}
	return ProposeOut{ApprovalID: t.ApprovalID, Status: "pending", Note: proposeNote}, nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
