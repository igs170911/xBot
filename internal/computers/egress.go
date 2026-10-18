package computers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"xbot/internal/box"
	"xbot/internal/store"
)

// 「從家裡出去」（CL-7）的設定與操作。做法見 internal/box/egress.go。
// 設定存在 setting 表：egress（exit node、小容器的名稱）與 egress.auth_key（加密的 Tailscale 登入金鑰）；
// 每個 Bot 開不開在 box.egress.<id>。

// 設定的 key。
const (
	EgressSettingKey = "egress"
	EgressKeySetting = "egress.auth_key"
	egressKeyAAD     = "egress:tailscale-auth-key"
)

// Sealer 加密登入金鑰（proxy.Keyring）。
type Sealer interface {
	Seal(plaintext []byte, aad string) ([]byte, error)
	Open(sealed []byte, aad string) ([]byte, error)
}

// EgressSettings 是「從家裡出去」的共用設定（所有主機一樣：都經同一台家裡的電腦出去）。
type EgressSettings struct {
	ExitNode string `json:"exit_node"`
	Hostname string `json:"hostname,omitempty"`
}

func (s *Service) egressSettings(ctx context.Context) (EgressSettings, error) {
	var set EgressSettings
	_, err := s.DB.GetSettingInto(ctx, EgressSettingKey, &set)
	return set, err
}

// egressKey 讀「從家裡出去」自己的登入金鑰；沒有時用內建 Tailscale（C35）的。shared 表示用的是 C35 的。
func (s *Service) egressKey(ctx context.Context) (key string, shared bool, err error) {
	if s.Sealer != nil {
		var sealed []byte
		ok, err := s.DB.GetSettingInto(ctx, EgressKeySetting, &sealed)
		if err != nil {
			return "", false, err
		}
		if ok {
			pt, err := s.Sealer.Open(sealed, egressKeyAAD)
			if err != nil {
				return "", false, fmt.Errorf("存著的 Tailscale 登入金鑰解不開，請重新輸入：%w", err)
			}
			return string(pt), false, nil
		}
	}
	if s.TailscaleKey != nil {
		k, err := s.TailscaleKey(ctx)
		return k, k != "", err
	}
	return "", false, nil
}

// EgressConfig 實作 box.EgressSource。
func (s *Service) EgressConfig(ctx context.Context) (box.EgressConfig, error) {
	set, err := s.egressSettings(ctx)
	if err != nil {
		return box.EgressConfig{}, err
	}
	key, _, err := s.egressKey(ctx)
	if err != nil {
		return box.EgressConfig{}, err
	}
	return box.EgressConfig{ExitNode: set.ExitNode, AuthKey: key, Hostname: set.Hostname}, nil
}

// EgressView 是「從家裡出去」的狀態（xbot.computers.set_egress、egress_status）。
type EgressView struct {
	// ExitNode 是家裡那台電腦（exit node）；HasAuthKey 是有沒有登入金鑰（不回傳金鑰本身）；SharedKey 表示用的是內建 Tailscale 的金鑰。
	ExitNode   string `json:"exit_node"`
	HasAuthKey bool   `json:"has_auth_key"`
	SharedKey  bool   `json:"shared_key" desc:"沒有另外設金鑰，用內建 Tailscale（設定 → 網路）的"`
	// Bots 是開了「從家裡出去」的 Bot。
	Bots []string `json:"bots"`
	// Hosts 是有 Bot 用到的主機上的小容器狀態。
	Hosts []box.EgressState `json:"hosts"`
	// Rebuild 表示這次改了設定、電腦要重建才生效；Rebuilt 表示已經重建好了；Pending 表示 Bot 在工作，下次開機才生效。
	Rebuild bool `json:"rebuild,omitempty"`
	Rebuilt bool `json:"rebuilt,omitempty"`
	Pending bool `json:"pending,omitempty"`
}

// EgressIn 是 xbot.computers.set_egress 的輸入。
type EgressIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Home  bool   `json:"home" desc:"true：這個 Bot 的電腦連外從家裡出去（經 Tailscale exit node）；false：關掉，直接從主機連外"`
	// ExitNode、AuthKey 不填時沿用存著的。
	ExitNode string `json:"exit_node,omitempty" max:"253" desc:"家裡那台電腦在 Tailscale 上的名稱或 100.x IP（要在 Tailscale 設成 exit node）；不填沿用上次的"`
	AuthKey  string `json:"auth_key,omitempty" max:"200" secret:"true" desc:"Tailscale 登入金鑰（tskey-auth-…），給主機上的小容器加入你的 tailnet；不填沿用存著的，或內建 Tailscale 的"`
}

// EgressStatusIn 是 xbot.computers.egress_status 的輸入。
type EgressStatusIn struct {
	HostID string `json:"host_id,omitempty" resource:"host" max:"64" desc:"只看這台主機；不填＝有 Bot 用到的主機"`
}

// saveEgress 存 exit node 與登入金鑰（有填的才存）。
func (s *Service) saveEgress(ctx context.Context, exitNode, authKey string) error {
	exitNode, authKey = strings.TrimSpace(exitNode), strings.TrimSpace(authKey)
	if exitNode != "" {
		if err := box.ValidExitNode(exitNode); err != nil {
			return err
		}
		set, err := s.egressSettings(ctx)
		if err != nil {
			return err
		}
		set.ExitNode = exitNode
		if err := s.DB.SetSetting(ctx, EgressSettingKey, set); err != nil {
			return err
		}
	}
	if authKey != "" {
		if !strings.HasPrefix(authKey, "tskey-") {
			return errors.New("Tailscale 登入金鑰要以 tskey- 開頭")
		}
		if s.Sealer == nil {
			return errors.New("這台 XBot 不能存金鑰（沒有主金鑰）")
		}
		sealed, err := s.Sealer.Seal([]byte(authKey), egressKeyAAD)
		if err != nil {
			return err
		}
		if err := s.DB.SetSetting(ctx, EgressKeySetting, sealed); err != nil {
			return err
		}
	}
	return nil
}

// SetEgress 開關一個 Bot 的「從家裡出去」。開的時候要已經有 exit node 與登入金鑰。
// 電腦開著而且 Bot 沒在工作時立刻重建（資料保留）；在工作時下次開機才生效。
func (s *Service) SetEgress(ctx context.Context, in EgressIn) (EgressView, error) {
	if err := s.saveEgress(ctx, in.ExitNode, in.AuthKey); err != nil {
		return EgressView{}, err
	}
	if in.Home {
		cfg, err := s.EgressConfig(ctx)
		if err != nil {
			return EgressView{}, err
		}
		if cfg.ExitNode == "" || cfg.AuthKey == "" {
			return EgressView{}, box.ErrEgressNotSet
		}
	}
	rebuild, err := s.Boxes.SetEgress(ctx, in.BotID, in.Home)
	if err != nil {
		return EgressView{}, err
	}
	v, err := s.EgressStatus(ctx, "")
	if err != nil {
		return v, err
	}
	v.Rebuild = rebuild
	if rebuild {
		st, err := s.Boxes.Status(ctx, in.BotID)
		switch {
		case err != nil:
			return v, err
		case !st.Running:
			// 沒開機：下次開機前會自動重建。
		case s.Activity != nil && !s.Activity.Quiesce(in.BotID):
			v.Pending = true
		default:
			if _, err := s.Boxes.Rebuild(ctx, in.BotID); err != nil {
				return v, err
			}
			v.Rebuilt = true
		}
	}
	if !in.Home {
		s.dropUnusedEgress(ctx)
	}
	return v, nil
}

// dropUnusedEgress 刪掉沒有 Bot 要用的主機上的小容器（登入狀態留著）。
func (s *Service) dropUnusedEgress(ctx context.Context) {
	used, err := s.egressHosts(ctx)
	if err != nil {
		return
	}
	rows, err := s.DB.ListHosts(ctx)
	if err != nil {
		return
	}
	for _, h := range rows {
		if used[h.ID] || !online(h) {
			continue
		}
		if st, err := s.Boxes.EgressStatus(ctx, h.ID); err == nil && st.Present {
			if err := s.Boxes.RemoveEgress(ctx, h.ID); err != nil {
				s.log().Warn("刪除沒在用的「從家裡出去」小容器失敗", "host", h.ID, "err", err)
			}
		}
	}
}

// egressBots 列出開了「從家裡出去」的 Bot。
func (s *Service) egressBots(ctx context.Context) ([]store.Bot, error) {
	bots, err := s.DB.ListBots(ctx, true)
	if err != nil {
		return nil, err
	}
	var out []store.Bot
	for _, b := range bots {
		on, err := s.Boxes.EgressOn(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if on {
			out = append(out, b)
		}
	}
	return out, nil
}

// egressHosts 是有 Bot 用到「從家裡出去」的主機。
func (s *Service) egressHosts(ctx context.Context) (map[string]bool, error) {
	bots, err := s.egressBots(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range bots {
		if where, err := s.whereIs(ctx, b.ID); err == nil {
			out[where] = true
		}
	}
	return out, nil
}

// EgressStatus 回傳「從家裡出去」的設定與各主機小容器的狀態（hostID 空白＝有 Bot 用到的主機）。
func (s *Service) EgressStatus(ctx context.Context, hostID string) (EgressView, error) {
	v := EgressView{Bots: []string{}, Hosts: []box.EgressState{}}
	set, err := s.egressSettings(ctx)
	if err != nil {
		return v, err
	}
	v.ExitNode = set.ExitNode
	key, shared, err := s.egressKey(ctx)
	if err != nil {
		return v, err
	}
	v.HasAuthKey, v.SharedKey = key != "", shared
	bots, err := s.egressBots(ctx)
	if err != nil {
		return v, err
	}
	for _, b := range bots {
		v.Bots = append(v.Bots, b.ID)
	}
	ids := []string{hostID}
	if hostID == "" {
		used, err := s.egressHosts(ctx)
		if err != nil {
			return v, err
		}
		ids = ids[:0]
		rows, err := s.DB.ListHosts(ctx)
		if err != nil {
			return v, err
		}
		for _, h := range rows {
			if used[h.ID] {
				ids = append(ids, h.ID)
			}
		}
	}
	for _, id := range ids {
		st, err := s.Boxes.EgressStatus(ctx, id)
		if err != nil {
			st = box.EgressState{HostID: id, Error: err.Error()}
		}
		v.Hosts = append(v.Hosts, st)
	}
	return v, nil
}
