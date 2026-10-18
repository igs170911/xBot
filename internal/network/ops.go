package network

import (
	"context"
	"errors"

	"xbot/internal/ops"
)

// StatusIn 是 xbot.network.status 的輸入。
type StatusIn struct{}

// TailscaleSetIn 是 xbot.network.tailscale_set 的輸入。
type TailscaleSetIn struct {
	Enabled  bool   `json:"enabled" required:"true" desc:"開啟或關閉內建 Tailscale"`
	Hostname string `json:"hostname,omitempty" desc:"在 tailnet 上的名字（小寫英數字與 -）；預設 xbot，網址就是 https://<名字>.<tailnet>.ts.net" max:"63"`
	AuthKey  string `json:"auth_key,omitempty" secret:"true" desc:"Tailscale 登入金鑰（tskey-auth-…，加密後存在服務端）；空白就用互動登入（狀態會給登入網址）" max:"500"`
	// ClearAuthKey 刪掉存著的登入金鑰（節點已經登入的話不受影響）。
	ClearAuthKey bool `json:"clear_auth_key,omitempty" desc:"刪掉存著的登入金鑰"`
}

// FunnelSetIn 是 xbot.network.funnel_set 的輸入。
type FunnelSetIn struct {
	Enabled  bool     `json:"enabled" required:"true" desc:"開啟或關閉外部入口（Tailscale Funnel）"`
	AllowIPs []string `json:"allow_ips" desc:"IP 白名單（IP 或 CIDR，例：203.0.113.7、198.51.100.0/24）；空白表示不限。Funnel 與反向代理都適用" max:"50"`
}

// Register 登錄網路相關的操作。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil {
			return ops.Internal(errors.New("網路服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[StatusIn, Status]{
		Name: "xbot.network.status", Summary: "看網路狀態：本機網址、內建 Tailscale（tailnet 網址、憑證、裝置數）、外部入口（Funnel／反向代理）",
		Description: "網址觸發與代理配對說明用 public_url（外部連得到的網址）。登入網址只給有 admin 的身分。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(_ context.Context, call *ops.Call, _ StatusIn) (Status, error) {
			if err := need(); err != nil {
				return Status{}, err
			}
			return svc.Status(call.Identity.HasScope(ops.ScopeAdmin)), nil
		},
	})
	ops.Register(reg, ops.Op[TailscaleSetIn, Status]{
		Name: "xbot.network.tailscale_set", Summary: "開啟或關閉內建 Tailscale（手機、筆電經 tailnet 用正式 HTTPS 連 XBot）",
		Description: "開啟後這台 XBot 會成為你 tailnet 上的一台機器（預設名字 xbot），網址 https://xbot.<tailnet>.ts.net。" +
			"tailnet 來的連線一樣要登入或權杖。可以給登入金鑰（tskey-auth-…），或不給、用狀態裡的登入網址核准。開啟要你本人核准、不能代為核准；關閉不用。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		RiskOf: func(in TailscaleSetIn) ops.Risk {
			if in.Enabled {
				return ops.RiskConfirm
			}
			return ops.RiskSafe
		},
		Handle: func(ctx context.Context, call *ops.Call, in TailscaleSetIn) (Status, error) {
			if err := need(); err != nil {
				return Status{}, err
			}
			if in.Hostname != "" && !ValidHostname(in.Hostname) {
				return Status{}, ops.InvalidInput(ops.FieldError{Field: "hostname", Message: "只能用小寫英文字母、數字和 -，不能用 - 開頭或結尾"})
			}
			if call.DryRun {
				st := svc.Status(false)
				set := svc.Preview(func(s *Settings) {
					s.Tailscale = in.Enabled
					if in.Hostname != "" {
						s.Hostname = in.Hostname
					}
				})
				st.Tailscale.Enabled, st.Tailscale.Hostname = set.Tailscale, set.Hostname
				st.Tailscale.HasAuthKey = (st.Tailscale.HasAuthKey || in.AuthKey != "") && !in.ClearAuthKey
				return st, nil
			}
			if err := svc.SetTailscale(ctx, TailscaleChange(in)); err != nil {
				return Status{}, err
			}
			return svc.Status(true), nil
		},
	})
	ops.Register(reg, ops.Op[FunnelSetIn, Status]{
		Name: "xbot.network.funnel_set", Summary: "開啟或關閉外部入口（Tailscale Funnel）：讓 tailnet 外的代理與網址觸發連得到 XBot",
		Description: "外部入口只開放 /mcp（一律要代理權杖）、/api/v1/agents.pair（配對碼）與 /hooks/<id>（網址觸發的金鑰），其他路徑一律 404；" +
			"可設 IP 白名單，速率上限比 tailnet 內嚴。要先開啟內建 Tailscale，tailnet 也要允許 Funnel。開啟要你本人核准、不能代為核准；關閉不用。",
		Scope: ops.ScopeAdmin, Risk: ops.RiskConfirm, NoDelegate: true,
		RiskOf: func(in FunnelSetIn) ops.Risk {
			if in.Enabled {
				return ops.RiskConfirm
			}
			return ops.RiskSafe
		},
		Handle: func(ctx context.Context, call *ops.Call, in FunnelSetIn) (Status, error) {
			if err := need(); err != nil {
				return Status{}, err
			}
			_, clean, err := ParseAllowIPs(in.AllowIPs)
			if err != nil {
				return Status{}, ops.InvalidInput(ops.FieldError{Field: "allow_ips", Message: err.Error()})
			}
			if in.Enabled && !svc.settings().Tailscale {
				return Status{}, ops.Conflict("要先開啟內建 Tailscale，才能開外部入口（Funnel）")
			}
			if call.DryRun {
				st := svc.Status(false)
				st.Funnel.Enabled, st.Funnel.AllowIPs = in.Enabled, clean
				if clean == nil {
					st.Funnel.AllowIPs = []string{}
				}
				return st, nil
			}
			if err := svc.SetFunnel(ctx, in.Enabled, clean); err != nil {
				return Status{}, err
			}
			return svc.Status(true), nil
		},
	})
}
