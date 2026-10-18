package hosts

import (
	"context"
	"errors"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ListIn 是 xbot.hosts.list 的輸入。
type ListIn struct{}

// ListOut 是 xbot.hosts.list 的輸出。
type ListOut struct {
	Hosts []View `json:"hosts"`
}

// IDIn 指定一台主機。
type IDIn struct {
	ID string `json:"id" required:"true" resource:"host" max:"64" desc:"主機 id"`
}

// InstallIn 是 xbot.hosts.install_script 的輸入。
type InstallIn struct {
	NoTailscale bool `json:"no_tailscale,omitempty" desc:"不要裝 Tailscale（主機已經用別的方式連得到）"`
}

// InstallOut 是 xbot.hosts.install_script 的輸出。
type InstallOut struct {
	// Command 是在要加入的主機上貼的一行指令（內容就是 Script，只放 XBot 的公鑰，沒有秘密）。
	Command string `json:"command" desc:"在要加入的主機上貼的一行指令"`
	Script  string `json:"script" desc:"指令展開後的安裝腳本（可重複執行）"`
	// PublicKey 是 XBot 的 SSH 公鑰；Fingerprint 是它的指紋。
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	User        string `json:"user" desc:"主機上 XBot 用的帳號"`
}

// AddIn 是 xbot.hosts.add 的輸入。
type AddIn struct {
	Address    string `json:"address" required:"true" max:"255" desc:"主機位址：Tailscale 名稱或 IP，可帶 :埠號（例：cloud-01、100.101.7.23、vps.example.com:2222）"`
	Name       string `json:"name,omitempty" max:"40" desc:"主機名稱；不填用位址"`
	MaxRunning int    `json:"max_running,omitempty" min:"0" max:"64" desc:"同時開機上限；不填是 3"`
	SkipImage  bool   `json:"skip_image,omitempty" desc:"只檢查，不要把電腦映像傳過去"`
}

// CheckIn 是 xbot.hosts.check 的輸入。
type CheckIn struct {
	ID          string `json:"id" required:"true" resource:"host" max:"64" desc:"主機 id"`
	UpdateImage bool   `json:"update_image,omitempty" desc:"主機上的電腦映像和這台不同時傳過去（更新電腦）"`
}

// LimitsIn 是 xbot.hosts.set_limits 的輸入。
type LimitsIn struct {
	ID         string  `json:"id" required:"true" resource:"host" max:"64" desc:"主機 id"`
	Name       *string `json:"name,omitempty" min:"1" max:"40" desc:"主機名稱"`
	MaxRunning *int    `json:"max_running,omitempty" min:"0" max:"64" desc:"同時開機上限；0＝不限"`
}

// DefaultIn 是 xbot.hosts.set_default 的輸入。
type DefaultIn struct {
	ID string `json:"id,omitempty" resource:"host" max:"64" desc:"新 Bot 預設放的主機；空白＝這台電腦"`
}

// DefaultOut 是 xbot.hosts.set_default 的輸出。
type DefaultOut struct {
	ID string `json:"id"`
}

// RemoveOut 是 xbot.hosts.remove 的輸出。
type RemoveOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Removed bool   `json:"removed" desc:"預演時是 false"`
}

// Register 登錄 xbot.hosts.* 操作。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil {
			return ops.Internal(errors.New("主機管理沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.hosts.list", Summary: "列出放 Bot 電腦的主機（狀態、資源、開機台數、上面有哪些 Bot）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			all, err := svc.List(ctx)
			out := ListOut{Hosts: []View{}}
			for _, v := range all {
				if call.Identity.CanAccess(ops.ResourceHost, v.ID) {
					out.Hosts = append(out.Hosts, v)
				}
			}
			return out, err
		},
	})
	ops.Register(reg, ops.Op[IDIn, View]{
		Name: "xbot.hosts.get", Summary: "讀一台主機的狀態", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in IDIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			v, err := svc.Get(ctx, in.ID)
			return v, hostErr(err)
		},
	})
	ops.Register(reg, ops.Op[InstallIn, InstallOut]{
		Name: "xbot.hosts.install_script", Summary: "產生新增主機第 1 步要在主機上貼的一行安裝指令",
		Description: "指令會在那台 Linux 主機上裝 Docker 與 Tailscale（已經有就略過）、建 xbot 帳號（docker 群組）只放 XBot 的 SSH 公鑰、" +
			"設定 sshd 允許 XBot 的轉發。可以重複執行。之後用 xbot.hosts.add 加入。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, _ *ops.Call, in InstallIn) (InstallOut, error) {
			if err := need(); err != nil {
				return InstallOut{}, err
			}
			id, err := svc.Identity()
			if err != nil {
				return InstallOut{}, err
			}
			return InstallOut{Command: InstallCommand(id.AuthorizedKey, !in.NoTailscale), Script: InstallScript(id.AuthorizedKey),
				PublicKey: id.AuthorizedKey, Fingerprint: id.Fingerprint(), User: limits.HostSSHUser}, nil
		},
	})
	ops.Register(reg, ops.Op[AddIn, AddResult]{
		Name: "xbot.hosts.add", Summary: "加入一台遠端主機（自動檢查 SSH、Docker、資源、架構、轉發，並把電腦映像傳過去）",
		Description: "先在主機上貼 xbot.hosts.install_script 的指令。第一次連線會記下主機金鑰，之後金鑰不同就拒絕連線。" +
			"檢查沒過時不會加入，report 說明原因與修法。電腦映像在背景傳，傳完主機變成在線（進度看 host.image 事件）。" +
			"同一個位址重複加入會重新檢查，不會重複新增。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in AddIn) (AddResult, error) {
			if err := need(); err != nil {
				return AddResult{}, err
			}
			t, err := ParseTarget(in.Address)
			if err != nil {
				return AddResult{}, ops.InvalidInput(ops.FieldError{Field: "address", Message: err.Error()})
			}
			if call.DryRun {
				return AddResult{Host: View{Name: in.Name, Kind: "ssh", Address: t.Address, Port: t.Port, User: t.User, Status: store.HostUnknown}}, nil
			}
			res, err := svc.Add(ctx, AddRequest{Target: t, Name: in.Name, MaxRunning: in.MaxRunning, SkipImage: in.SkipImage})
			if errors.Is(err, ErrNameTaken) {
				return res, ops.InvalidInput(ops.FieldError{Field: "name", Message: err.Error()})
			}
			return res, err
		},
	})
	ops.Register(reg, ops.Op[CheckIn, AddResult]{
		Name: "xbot.hosts.check", Summary: "重新檢查一台主機（離線時順便重新連線）",
		Description: "更新資源、Docker 版本與連線狀態。update_image 為 true 時，主機上的電腦映像和這台不同就傳過去。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in CheckIn) (AddResult, error) {
			if err := need(); err != nil {
				return AddResult{}, err
			}
			if call.DryRun {
				v, err := svc.Get(ctx, in.ID)
				return AddResult{Host: v, Added: err == nil}, hostErr(err)
			}
			res, err := svc.Check(ctx, in.ID, in.UpdateImage)
			return res, hostErr(err)
		},
	})
	ops.Register(reg, ops.Op[LimitsIn, View]{
		Name: "xbot.hosts.set_limits", Summary: "改主機名稱或同時開機上限",
		Description: "上限滿了時新的工作會排隊（等電腦空出來）；改大之後排隊的工作會自動開機。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in LimitsIn) (View, error) {
			if err := need(); err != nil {
				return View{}, err
			}
			if call.DryRun {
				v, err := svc.Get(ctx, in.ID)
				if in.Name != nil {
					v.Name = *in.Name
				}
				if in.MaxRunning != nil {
					v.MaxRunning = *in.MaxRunning
				}
				return v, hostErr(err)
			}
			v, err := svc.SetLimits(ctx, in.ID, in.Name, in.MaxRunning)
			if errors.Is(err, ErrNameTaken) {
				return v, ops.InvalidInput(ops.FieldError{Field: "name", Message: err.Error()})
			}
			return v, hostErr(err)
		},
	})
	ops.Register(reg, ops.Op[DefaultIn, DefaultOut]{
		Name: "xbot.hosts.set_default", Summary: "設定新 Bot 預設放哪台主機",
		Description: "預設主機滿了或離線時，新 Bot 會自動放到其他有空位的主機（CL-4）。",
		Scope:       ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in DefaultIn) (DefaultOut, error) {
			if err := need(); err != nil {
				return DefaultOut{}, err
			}
			if call.DryRun {
				return DefaultOut(in), nil
			}
			return DefaultOut(in), hostErr(svc.SetDefault(ctx, in.ID))
		},
	})
	ops.Register(reg, ops.Op[IDIn, RemoveOut]{
		Name: "xbot.hosts.remove", Summary: "移除一台遠端主機",
		Description: "上面還有 Bot 的電腦時不能移除（先搬走或刪掉）。只會斷線並忘掉這台主機，不會動主機上的任何東西。" +
			"一定要你本人核准，不能代為核准。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in IDIn) (RemoveOut, error) {
			if err := need(); err != nil {
				return RemoveOut{}, err
			}
			var p RemovePlan
			var err error
			if call.DryRun {
				p, err = svc.PlanRemove(ctx, in.ID)
			} else {
				p, err = svc.Remove(ctx, in.ID)
			}
			out := RemoveOut{ID: in.ID, Name: p.Host.Name, Removed: err == nil && !call.DryRun}
			return out, hostErr(err)
		},
	})
}

func hostErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ops.NotFound("找不到這台主機")
	case errors.Is(err, ErrHostInUse), errors.Is(err, ErrLocalHost):
		return ops.Conflict(err.Error())
	}
	return err
}
