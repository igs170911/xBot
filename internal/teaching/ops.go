package teaching

import (
	"context"
	"errors"

	"xbot/internal/boxfs"
	"xbot/internal/ops"
	"xbot/internal/screen"
	"xbot/internal/skills"
	"xbot/internal/store"
)

// BotIn 指定一個 Bot。
type BotIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// LessonIn 指定一次教學。
type LessonIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string `json:"lesson_id" required:"true" max:"64" desc:"教學 id（xbot.teaching.start 的 id）"`
}

// StartIn 是 xbot.teaching.start 的輸入。
type StartIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Goal     string `json:"goal,omitempty" max:"200" desc:"要教什麼（選填；可以做完再補）"`
	ThreadID string `json:"thread_id,omitempty" max:"64" desc:"從哪條對話串開始（選填）：在那裡記「你開始示範」等系統訊息"`
}

// PauseIn 是 xbot.teaching.pause 的輸入。
type PauseIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string `json:"lesson_id" required:"true" max:"64"`
	Paused   bool   `json:"paused" desc:"true 暫停（這段時間的操作不記）；false 繼續"`
}

// NoteIn 是 xbot.teaching.note 的輸入。
type NoteIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string `json:"lesson_id" required:"true" max:"64"`
	Text     string `json:"text" required:"true" min:"1" max:"500" desc:"補充說明，例：每次都先清空購物車"`
}

// DropIn 是 xbot.teaching.drop 的輸入。
type DropIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string `json:"lesson_id" required:"true" max:"64"`
	StepID   string `json:"step_id" required:"true" max:"64" desc:"要刪掉的步驟 id"`
}

// StopIn 是 xbot.teaching.stop 的輸入。
type StopIn struct {
	BotID    string  `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string  `json:"lesson_id" required:"true" max:"64"`
	Goal     *string `json:"goal,omitempty" max:"200" desc:"順便補上要教什麼（選填）"`
}

// DraftIn 是 xbot.teaching.draft 的輸入。
type DraftIn struct {
	BotID    string  `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string  `json:"lesson_id" required:"true" max:"64"`
	Goal     *string `json:"goal,omitempty" max:"200" desc:"補上或改掉要教什麼，並重新整理草稿（選填；不填只讀草稿）"`
	Redo     bool    `json:"redo,omitempty" desc:"重新整理草稿（你改過的內容不會保留）"`
}

// SaveIn 是 xbot.teaching.save 的輸入。
type SaveIn struct {
	BotID        string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID     string `json:"lesson_id" required:"true" max:"64"`
	Name         string `json:"name" required:"true" min:"1" max:"64" desc:"技能名稱"`
	Description  string `json:"description" required:"true" max:"1024" desc:"什麼時候使用（一句話）"`
	Instructions string `json:"instructions" required:"true" min:"1" max:"100000" desc:"怎麼做（草稿的內文，可以改）；提到的截圖（shots/…）會一起存進技能"`
	SkipTrial    bool   `json:"skip_trial,omitempty" desc:"存好後不要請 Bot 試做（預設會試做一次）"`
}

// ShotIn 是 xbot.teaching.shot 的輸入。
type ShotIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	LessonID string `json:"lesson_id" required:"true" max:"64"`
	Name     string `json:"name" required:"true" max:"64" desc:"步驟的截圖（shots/b-0001.jpg 這種）"`
}

// CurrentOut 是 xbot.teaching.get 的輸出。
type CurrentOut struct {
	Lesson *Lesson `json:"lesson" desc:"進行中的教學（錄製中、暫停、草稿等你確認）；沒有時是 null"`
}

// ShotOut 是 xbot.teaching.shot 的輸出。
type ShotOut struct {
	Image     []byte `json:"image" desc:"JPEG（base64）"`
	MediaType string `json:"media_type"`
}

const ownerOnly = "只有你本人可以教 Bot（示範教學要在你的電腦上操作）"

// Register 登錄 xbot.teaching.* 操作。svc 是 nil 時只登錄定義。全部只給你本人用（外部代理與 Bot 不能）。
func Register(reg *ops.Registry, svc *Service) {
	need := func(call *ops.Call) error {
		if svc == nil {
			return ops.Internal(errors.New("示範教學服務沒有啟動"))
		}
		if call.Identity.Kind != ops.KindOwner {
			return ops.Forbidden(ownerOnly)
		}
		return nil
	}
	ops.Register(reg, ops.Op[BotIn, CurrentOut]{
		Name: "xbot.teaching.get", Summary: "看一個 Bot 進行中的示範教學：錄到的步驟、草稿",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (CurrentOut, error) {
			if err := need(call); err != nil {
				return CurrentOut{}, err
			}
			l, err := svc.Current(ctx, in.BotID)
			return CurrentOut{Lesson: l}, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[StartIn, Lesson]{
		Name: "xbot.teaching.start", Summary: "開始示範教學（教它一件事）：XBot 接手 Bot 的電腦並開始記錄你的每一步",
		Description: "電腦沒開機時先開機（桌面就緒最多 75 秒）。Bot 會暫停，你直接在即時畫面（mode=control）操作；瀏覽器與桌面 App 的操作都會記下" +
			"（密碼欄位只記成 ••••）。示範中可以補充說明（xbot.teaching.note）、暫停（pause）、刪掉做錯的步驟（drop）；" +
			"做完用 xbot.teaching.stop 產生技能草稿。Bot 正在工作時回 conflict。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StartIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			if call.DryRun {
				return Lesson{BotID: in.BotID, State: store.LessonRecording, Steps: []Step{}}, nil
			}
			l, err := svc.Start(ctx, StartRequest{BotID: in.BotID, Goal: in.Goal, ThreadID: in.ThreadID, By: call.Identity.Actor(),
				ByName: call.Identity.DisplayName()})
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[PauseIn, Lesson]{
		Name: "xbot.teaching.pause", Summary: "暫停或繼續示範錄製（暫停時的操作不記）",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in PauseIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			l, err := svc.Pause(ctx, in.BotID, in.LessonID, in.Paused)
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[NoteIn, Lesson]{
		Name: "xbot.teaching.note", Summary: "示範中補充一句說明（會成為一步）",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in NoteIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			l, err := svc.AddNote(ctx, in.BotID, in.LessonID, in.Text)
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[DropIn, Lesson]{
		Name: "xbot.teaching.drop", Summary: "刪掉示範中做錯的一步",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DropIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			l, err := svc.Drop(ctx, in.BotID, in.LessonID, in.StepID)
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[StopIn, Lesson]{
		Name: "xbot.teaching.stop", Summary: "完成示範：停止記錄、把電腦交還給 Bot，並把步驟整理成技能草稿",
		Description: "回傳的 draft 是技能草稿（名稱、什麼時候使用、步驟），看過、改過後用 xbot.teaching.save 存成技能，或用 xbot.teaching.discard 捨棄。" +
			"還沒記到任何操作時回 invalid_input，示範繼續。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StopIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			if call.DryRun {
				return Lesson{BotID: in.BotID, ID: in.LessonID, State: store.LessonStopped, Steps: []Step{}}, nil
			}
			l, err := svc.Finish(ctx, in.BotID, in.LessonID, call.Identity.Actor(), in.Goal)
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[DraftIn, Lesson]{
		Name: "xbot.teaching.draft", Summary: "讀示範整理出的技能草稿；補上要教什麼時重新整理",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DraftIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			if in.Goal == nil && !in.Redo || call.DryRun {
				l, err := svc.lesson(ctx, in.BotID, in.LessonID)
				if err != nil {
					return Lesson{}, opErr(err)
				}
				return svc.view(l), nil
			}
			l, err := svc.Redraft(ctx, in.BotID, in.LessonID, in.Goal)
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[SaveIn, SaveResult]{
		Name: "xbot.teaching.save", Summary: "把示範的技能草稿存成 Bot 的技能（預設存好後請它試做一次）",
		Description: "name、description、instructions 是你確認（可能改過）的內容。內文提到的截圖（shots/…）會一起存進技能資料夾。" +
			"同名的技能已經存在時回 conflict。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SaveIn) (SaveResult, error) {
			if err := need(call); err != nil {
				return SaveResult{}, err
			}
			d := skills.Doc{Name: in.Name, Description: in.Description, Instructions: in.Instructions}
			if err := d.Validate(); err != nil {
				return SaveResult{}, ops.InvalidInput(ops.FieldError{Message: err.Error()})
			}
			if call.DryRun {
				return SaveResult{}, nil
			}
			out, err := svc.Save(ctx, SaveRequest{BotID: in.BotID, LessonID: in.LessonID, Name: in.Name, Description: in.Description,
				Instructions: in.Instructions, Trial: !in.SkipTrial, By: call.Identity.Actor()})
			return out, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[LessonIn, Lesson]{
		Name: "xbot.teaching.discard", Summary: "取消示範或捨棄技能草稿（錄到的步驟與截圖會刪掉）",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LessonIn) (Lesson, error) {
			if err := need(call); err != nil {
				return Lesson{}, err
			}
			if call.DryRun {
				return Lesson{BotID: in.BotID, ID: in.LessonID, State: store.LessonDiscarded, Steps: []Step{}}, nil
			}
			l, err := svc.Discard(ctx, in.BotID, in.LessonID, call.Identity.Actor())
			return l, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[ShotIn, ShotOut]{
		Name: "xbot.teaching.shot", Summary: "讀示範中一步的截圖（JPEG）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ShotIn) (ShotOut, error) {
			if err := need(call); err != nil {
				return ShotOut{}, err
			}
			b, err := svc.Shot(ctx, in.BotID, in.LessonID, in.Name)
			if err != nil {
				return ShotOut{}, opErr(err)
			}
			return ShotOut{Image: b, MediaType: "image/jpeg"}, nil
		},
	})
}

func opErr(err error) error {
	var oe *ops.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &oe):
		return err
	case errors.Is(err, store.ErrNotFound):
		return ops.NotFound("找不到這個 Bot 或這次教學")
	case errors.Is(err, ErrNoShot):
		return ops.NotFound(err.Error())
	case errors.Is(err, ErrBusy), errors.Is(err, ErrStarting), errors.Is(err, ErrNotOpen), errors.Is(err, ErrDraftPending),
		errors.Is(err, screen.ErrTakenByOther), errors.Is(err, skills.ErrExists):
		return ops.Conflict(err.Error())
	case errors.Is(err, ErrNoSteps), errors.Is(err, ErrUnsupported), errors.Is(err, ErrNotReady):
		return ops.InvalidInput(ops.FieldError{Message: err.Error()})
	case errors.Is(err, boxfs.ErrNoComputer), errors.Is(err, boxfs.ErrNotRunning):
		return ops.Conflict(err.Error())
	case errors.Is(err, ErrEmptyNote):
		return ops.InvalidInput(ops.FieldError{Field: "text", Message: err.Error()})
	}
	return err
}
