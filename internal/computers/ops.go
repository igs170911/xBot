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

// MoveIn 是 xbot.computers.move 的輸入。
type MoveIn struct {
	BotID  string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	HostID string `json:"host_id" required:"true" resource:"host" max:"64" desc:"搬到這台主機"`
}

// MoveAllIn 是 xbot.computers.move_all 的輸入。
type MoveAllIn struct {
	FromHostID string `json:"from_host_id" required:"true" resource:"host" max:"64" desc:"把這台主機上的 Bot"`
	ToHostID   string `json:"to_host_id" required:"true" resource:"host" max:"64" desc:"全部搬到這台主機"`
}

// MoveStatusIn 是 xbot.computers.move_status 的輸入。
type MoveStatusIn struct {
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"只看這個 Bot；不填＝全部"`
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
	registerMove(reg, svc, need)
	registerCleanup(reg, svc, need)
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

func registerMove(reg *ops.Registry, svc *Service, need func() error) {
	ops.Register(reg, ops.Op[MoveIn, MoveStatus]{
		Name: "xbot.computers.move", Summary: "搬家：把一個 Bot 的電腦搬到另一台主機，家目錄、工作資料夾、引擎紀錄與瀏覽器登入都跟著走",
		Description: "停止電腦 → 經 SSH 打包 home／workspace／hermes → 在新主機還原並逐檔驗證 → 切換過去（原本開著的會在新主機開機）。" +
			"在背景進行，進度看 computers.move 事件或 xbot.computers.move_status；途中失敗或取消時 Bot 留在原主機，原資料不動。" +
			"原主機上的舊資料保留 24 小時才刪。Bot 正在工作時不能搬（先停止工作）。兩台主機都要在線上。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in MoveIn) (MoveStatus, error) {
			if err := need(); err != nil {
				return MoveStatus{}, err
			}
			if call.DryRun {
				st, err := svc.PlanMove(ctx, in.BotID, in.HostID)
				return st, opErr(err)
			}
			st, err := svc.Move(ctx, in.BotID, in.HostID)
			return st, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[MoveAllIn, MoveStatus]{
		Name: "xbot.computers.move_all", Summary: "把一台主機上的 Bot 全部搬到另一台（一個做完再做下一個）",
		Description: "從驗證用的主機換到專用主機時用（16.1a）。每個 Bot 的搬法同 xbot.computers.move；輪到時正在工作的 Bot 會跳過（可以之後再搬）。" +
			"dry_run 只列出會搬哪些。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in MoveAllIn) (MoveStatus, error) {
			if err := need(); err != nil {
				return MoveStatus{}, err
			}
			if call.DryRun {
				jobs, err := svc.PlanMoveAll(ctx, in.FromHostID, in.ToHostID)
				if jobs == nil {
					jobs = []MoveJob{}
				}
				return MoveStatus{Jobs: jobs}, opErr(err)
			}
			st, err := svc.MoveAll(ctx, in.FromHostID, in.ToHostID)
			return st, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[struct{}, Progress]{
		Name: "xbot.computers.update_status", Summary: "看「更新電腦」的進度：正在進行或最近一次的結果（還沒更新過時 hosts 是空的）",
		Description: "make update 之後用來等電腦更新完；網頁的「更新電腦」按鈕也用它顯示進度。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, _ struct{}) (Progress, error) {
			if err := need(); err != nil {
				return Progress{}, err
			}
			p, ok := svc.Progress()
			if !ok {
				return Progress{Hosts: []HostUpdate{}}, nil
			}
			return p, nil
		},
	})
	ops.Register(reg, ops.Op[MoveStatusIn, MoveStatus]{
		Name: "xbot.computers.move_status", Summary: "看搬家的進度：進行中、排隊中與最近做完的搬家",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in MoveStatusIn) (MoveStatus, error) {
			if err := need(); err != nil {
				return MoveStatus{}, err
			}
			st, err := svc.MoveStatus(ctx, in.BotID)
			return st, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, MoveStatus]{
		Name: "xbot.computers.move_cancel", Summary: "取消一個 Bot 排隊中或進行中的搬家（Bot 留在原主機，原資料不動）",
		Description: "已經切換到新主機的搬家不能取消（要搬回去請再搬一次）。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (MoveStatus, error) {
			if err := need(); err != nil {
				return MoveStatus{}, err
			}
			if call.DryRun {
				st, err := svc.MoveStatus(ctx, in.BotID)
				return st, opErr(err)
			}
			st, err := svc.CancelMove(ctx, in.BotID)
			return st, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[EgressIn, EgressView]{
		Name: "xbot.computers.set_egress", Summary: "「從家裡出去」：讓一個 Bot 的電腦連外經過家裡那台電腦（Tailscale exit node）",
		Description: "雲端主機的機房 IP 容易被蝦皮、Threads 要求驗證；開了以後這個 Bot 的瀏覽器與工具經主機上的 Tailscale 小容器、從家裡的網路連外。" +
			"家裡那台電腦要在 Tailscale 設成 exit node，並在 Tailscale 的存取控制只允許小容器（tag:xbot-egress）連網際網路（autogroup:internet），" +
			"否則經家裡出去的連線也連得到家裡的區網。只對認得 proxy 設定的程式有效（瀏覽器、Python、curl 等）。" +
			"電腦開著時會重建（資料保留）。打開要你本人核准、不能代為核准；關掉不用核准。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		RiskOf: func(in EgressIn) ops.Risk {
			if !in.Home && in.AuthKey == "" && in.ExitNode == "" {
				return ops.RiskSafe
			}
			return ops.RiskConfirm
		},
		Handle: func(ctx context.Context, call *ops.Call, in EgressIn) (EgressView, error) {
			if err := need(); err != nil {
				return EgressView{}, err
			}
			if in.ExitNode != "" {
				if err := box.ValidExitNode(in.ExitNode); err != nil {
					return EgressView{}, ops.InvalidInput(ops.FieldError{Field: "exit_node", Message: err.Error()})
				}
			}
			if call.DryRun {
				v, err := svc.EgressStatus(ctx, "")
				return v, opErr(err)
			}
			v, err := svc.SetEgress(ctx, in)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[EgressStatusIn, EgressView]{
		Name: "xbot.computers.egress_status", Summary: "看「從家裡出去」的設定與各主機上 Tailscale 小容器的狀態",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in EgressStatusIn) (EgressView, error) {
			if err := need(); err != nil {
				return EgressView{}, err
			}
			v, err := svc.EgressStatus(ctx, in.HostID)
			return v, opErr(err)
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
	case errors.Is(err, box.ErrBusy), errors.Is(err, ErrCleanBusy), errors.Is(err, ErrUpdateRunning), errors.Is(err, box.ErrMoving), errors.Is(err, box.ErrSameHost),
		errors.Is(err, ErrMoveQueued), errors.Is(err, ErrHostOffline), errors.Is(err, ErrNoMove):
		return ops.Conflict(err.Error())
	case errors.Is(err, box.ErrEgressNotSet):
		return ops.InvalidInput(ops.FieldError{Field: "exit_node", Message: err.Error()})
	case errors.Is(err, hosts.ErrUnknownHost), errors.Is(err, hosts.ErrHostOffline):
		return ops.Conflict("這個 Bot 的電腦所在的主機連不上：" + err.Error())
	case errors.As(err, &ne):
		return ops.Internal(err)
	}
	return err
}

func registerCleanup(reg *ops.Registry, svc *Service, need func() error) {
	ops.Register(reg, ops.Op[BotIn, StorageView]{
		Name: "xbot.computers.storage", Summary: "看一個 Bot 的電腦空間花在哪裡：工作成果、你傳的附件、快取與暫存、瀏覽器登入、Bot 裝的工具",
		Description: "電腦沒開機也可以看：會開一個不連網的小容器來量，不會叫醒電腦。資料多時要幾秒。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (StorageView, error) {
			if err := need(); err != nil {
				return StorageView{}, err
			}
			if call.DryRun {
				return StorageView{BotID: in.BotID}, nil
			}
			v, err := svc.Storage(ctx, in.BotID)
			return v, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[BotIn, CleanView]{
		Name: "xbot.computers.clean", Summary: "立即清理一個 Bot 電腦裡的快取與暫存（會自己長回來的東西），回傳清出多少",
		Description: "清：引擎暫存與 /workspace/tmp 裡超過 3 天沒動的檔案、超過 200 MB 的瀏覽器快取。工作成果、你傳的附件、瀏覽器登入都不會刪。" +
			"每天也會自動清一次，那時也會刪放超過 30 天的附件。Bot 正在工作時不能清（等它做完）。電腦沒開機也可以清，不會叫醒它。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (CleanView, error) {
			if err := need(); err != nil {
				return CleanView{}, err
			}
			if call.DryRun {
				if svc.busy(in.BotID) {
					return CleanView{}, ops.Conflict(ErrCleanBusy.Error())
				}
				return CleanView{BotID: in.BotID}, nil
			}
			v, err := svc.Clean(ctx, in.BotID)
			return v, opErr(err)
		},
	})
}
