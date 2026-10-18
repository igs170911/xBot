package computers

import (
	"context"
	"errors"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// BotIn 指定一個 Bot 的電腦。
type BotIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// WakeIn 是 xbot.computers.wake 的輸入。
type WakeIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Wait  bool   `json:"wait,omitempty" desc:"等到桌面與 Hermes 都就緒才回傳（最多 75 秒）"`
}

// NetworkIn 是 xbot.computers.set_network 的輸入。
type NetworkIn struct {
	BotID        string   `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	AllowPrivate []string `json:"allow_private" max:"32" desc:"這個 Bot 的電腦可以連的私有網路：IP（192.168.1.10）、網段（192.168.1.0/24）、IP:埠或網址（http://192.168.1.10:8080）。空的＝全部關掉（預設）"`
}

// UpdateIn 是 xbot.computers.update 的輸入。
type UpdateIn struct {
	HostID string `json:"host_id,omitempty" resource:"host" max:"64" desc:"只更新這台主機；不填＝所有主機，一台做完再做下一台"`
}

// Register 登錄 xbot.computers.* 操作。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil || svc.Boxes == nil {
			return ops.Internal(errors.New("電腦管理沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[BotIn, View]{
		Name: "xbot.computers.status", Summary: "看一個 Bot 的電腦：開機或休眠、在哪台主機、什麼時候會閒置休眠、要不要更新",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in BotIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			v, err := svc.Status(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[WakeIn, View]{
		Name: "xbot.computers.wake", Summary: "叫醒（開機）一個 Bot 的電腦；還沒有電腦時先建立",
		Description: "有工作時電腦本來就會自動開機，這個只是提早開好。主機滿了時照樣開（不排隊）。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in WakeIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				v, err := svc.Status(ctx, in.BotID)
				return v, opErr(err)
			}
			v, err := svc.Wake(ctx, in.BotID, in.Wait)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, View]{
		Name: "xbot.computers.sleep", Summary: "讓一個 Bot 的電腦休眠（關機，資料與瀏覽器登入都保留）",
		Description: "Bot 正在工作時不能休眠（先停止工作）。之後有工作時會自動開機。沒人用的電腦本來就會在閒置 30 分鐘（可每 Bot 調整）後自動休眠。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				return dry(ctx, svc, in.BotID, true)
			}
			v, err := svc.Sleep(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, View]{
		Name: "xbot.computers.restart", Summary: "重開一個 Bot 的電腦（資料保留）",
		Description: "Bot 正在工作時不能重開（先停止工作）。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				return dry(ctx, svc, in.BotID, true)
			}
			v, err := svc.Restart(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, View]{
		Name: "xbot.computers.rebuild", Summary: "重建一個 Bot 的電腦：用目前的映像與設定重新建立，家目錄、工作資料夾、瀏覽器登入都保留",
		Description: "電腦怪怪的、或映像更新後用。Bot 正在工作時不能重建（先停止工作）。原本開著的電腦重建後會再開機。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				return dry(ctx, svc, in.BotID, true)
			}
			v, err := svc.Rebuild(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, View]{
		Name: "xbot.computers.purge", Summary: "刪掉一個 Bot 的電腦和裡面所有資料（家目錄、工作資料夾、瀏覽器登入、引擎紀錄），無法復原",
		Description: "Bot 本身保留，下次有工作時開一台全新的電腦。Bot 正在工作時不能刪（先停止工作）。一定要你本人核准，不能代為核准。",
		Scope:       ops.ScopeManage, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				return dry(ctx, svc, in.BotID, true)
			}
			v, err := svc.Purge(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[NetworkIn, View]{
		Name: "xbot.computers.set_network", Summary: "設定一個 Bot 的電腦可以連哪些私有網路（區網、主機上的服務）",
		Description: "預設 Bot 的電腦連不到 XBot 所在的主機與任何私有網段（10.x、172.16-31.x、192.168.x、100.64.x/Tailscale 等），只連得到網際網路（SEC-4）。" +
			"這裡列出的位址會為這個 Bot 打開，立刻生效。打開要你本人核准、不能代為核准；全部關掉不用核准。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		RiskOf: func(in NetworkIn) ops.Risk {
			if len(in.AllowPrivate) == 0 {
				return ops.RiskSafe
			}
			return ops.RiskConfirm
		},
		Handle: func(ctx context.Context, call *ops.Call, in NetworkIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			allow, err := box.ParseAllowList(in.AllowPrivate)
			if err != nil {
				return View{}, ops.InvalidInput(ops.FieldError{Field: "allow_private", Message: err.Error()})
			}
			if call.DryRun {
				v, err := dry(ctx, svc, in.BotID, false)
				v.AllowPrivate = []string{}
				for _, a := range allow {
					v.AllowPrivate = append(v.AllowPrivate, a.String())
				}
				return v, err
			}
			v, err := svc.SetNetwork(ctx, in.BotID, in.AllowPrivate)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, Progress]{
		Name: "xbot.computers.update", Summary: "更新電腦：把新的電腦映像傳到主機，並重建上面要更新的電腦（資料保留）",
		Description: "make box 換了映像、或 XBot 更新了電腦的安全設定之後用。一台主機做完再做下一台；正在工作的 Bot 會跳過。" +
			"在背景進行，進度看 computers.update 事件（也可以再呼叫一次看目前進度）。dry_run 只列出會更新哪些。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateIn) (Progress, error) {
			if err := need(); err != nil {
				return Progress{}, err
			}
			if call.DryRun {
				p, err := svc.Plan(ctx, in.HostID)
				return p, opErr(err)
			}
			p, err := svc.Update(ctx, in.HostID)
			if errors.Is(err, ErrUpdateRunning) {
				return p, nil // 回目前的進度
			}
			return p, opErr(err)
		},
	})
}

// dry 是預演：讀狀態但不動電腦（不向 Docker 查，避免預演也寫 box 表）。
func dry(ctx context.Context, svc *Service, botID string, idle bool) (View, error) {
	bot, err := svc.DB.GetBot(ctx, botID)
	if err != nil {
		return View{}, opErr(err)
	}
	v := View{BotName: bot.Name, IdleMinutes: int(box.IdleFor(bot).Minutes()), Busy: svc.busy(botID), AllowPrivate: []string{}}
	if b, err := svc.DB.GetBox(ctx, botID); err == nil {
		v.Computer = box.Status{BotID: botID, HostID: b.HostID, ContainerID: b.ContainerID, State: b.State, Error: b.Error, Running: b.State == box.StateRunning}
		v.LastActiveAt, v.NetIP = b.LastActiveAt, b.NetIP
	} else {
		v.Computer = box.Status{BotID: botID, State: box.StateNone}
	}
	if idle && v.Busy {
		return v, ops.Conflict(box.ErrBusy.Error())
	}
	return v, nil
}

func opErr(err error) error {
	var ne *box.NetError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ops.NotFound("找不到這個 Bot 或它的主機")
	case errors.Is(err, box.ErrBusy), errors.Is(err, ErrUpdateRunning):
		return ops.Conflict(err.Error())
	case errors.Is(err, hosts.ErrUnknownHost), errors.Is(err, hosts.ErrHostOffline):
		return ops.Conflict("這個 Bot 的電腦所在的主機連不上：" + err.Error())
	case errors.As(err, &ne):
		return ops.Internal(err)
	}
	return err
}
