package push

import (
	"context"
	"errors"
	"strings"

	"xbot/internal/ops"
)

// Register 登錄推播操作：xbot.push.config、subscribe、unsubscribe、test。
// 推播是你本人的裝置設定：代理和 Bot 都不能訂閱或改。
func Register(r *ops.Registry, s *Service) {
	ops.Register(r, ops.Op[ConfigIn, ConfigOut]{
		Name:        "xbot.push.config",
		Summary:     "看推播設定：VAPID 公鑰與已訂閱的裝置",
		Description: "瀏覽器用 public_key 呼叫 pushManager.subscribe，再把結果交給 xbot.push.subscribe。",
		Scope:       ops.ScopeAdmin,
		Risk:        ops.RiskSafe,
		NoDelegate:  true,
		Handle: func(ctx context.Context, call *ops.Call, _ ConfigIn) (ConfigOut, error) {
			if err := ready(call, s, "看推播設定"); err != nil {
				return ConfigOut{}, err
			}
			out := ConfigOut{Subscriptions: []SubscriptionView{}}
			if call.DryRun {
				return out, nil
			}
			pk, err := s.PublicKey(ctx)
			if err != nil {
				return out, err
			}
			out.PublicKey = pk
			subs, err := s.List(ctx)
			if err != nil {
				return out, err
			}
			for _, x := range subs {
				out.Subscriptions = append(out.Subscriptions, SubscriptionView{ID: x.ID, Label: x.Label, NotifyDone: x.NotifyDone,
					CreatedAt: x.CreatedAt, EndpointHost: EndpointHost(x.Endpoint)})
			}
			return out, nil
		},
	})
	ops.Register(r, ops.Op[SubscribeIn, SubscribeOut]{
		Name:        "xbot.push.subscribe",
		Summary:     "讓這個裝置接收推播",
		Description: "同一個 endpoint 再送一次會更新設定（例：改「工作完成也通知」）。要你決定的新卡一定推；notify_done 為 true 時工作完成也推。",
		Scope:       ops.ScopeAdmin,
		Risk:        ops.RiskSafe,
		NoDelegate:  true,
		Handle: func(ctx context.Context, call *ops.Call, in SubscribeIn) (SubscribeOut, error) {
			if err := ready(call, s, "設定推播"); err != nil {
				return SubscribeOut{}, err
			}
			if err := ValidEndpoint(in.Endpoint); err != nil {
				return SubscribeOut{}, ops.InvalidInput(ops.FieldError{Field: "endpoint", Message: err.Error()})
			}
			if call.DryRun {
				return SubscribeOut{}, nil
			}
			id, err := s.Subscribe(ctx, Subscription{Endpoint: in.Endpoint, P256dh: strings.TrimSpace(in.P256dh), Auth: strings.TrimSpace(in.Auth),
				Label: strings.TrimSpace(in.Label), NotifyDone: in.NotifyDone})
			if errors.Is(err, ErrTooMany) {
				return SubscribeOut{}, ops.Limit(err.Error())
			}
			return SubscribeOut{ID: id}, err
		},
	})
	ops.Register(r, ops.Op[UnsubscribeIn, UnsubscribeOut]{
		Name:       "xbot.push.unsubscribe",
		Summary:    "讓一個裝置不再接收推播",
		Scope:      ops.ScopeAdmin,
		Risk:       ops.RiskSafe,
		NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in UnsubscribeIn) (UnsubscribeOut, error) {
			if err := ready(call, s, "關掉推播"); err != nil {
				return UnsubscribeOut{}, err
			}
			if in.Endpoint == "" && in.ID == "" {
				return UnsubscribeOut{}, ops.InvalidInput(ops.FieldError{Field: "endpoint", Message: "endpoint 或 id 要給一個"})
			}
			if call.DryRun {
				return UnsubscribeOut{}, nil
			}
			ok, err := s.Unsubscribe(ctx, in.Endpoint, in.ID)
			return UnsubscribeOut{Removed: ok}, err
		},
	})
	ops.Register(r, ops.Op[TestIn, TestOut]{
		Name:        "xbot.push.test",
		Summary:     "送一則測試推播",
		Description: "給 endpoint 時只送到那個裝置；不給就送到所有裝置。",
		Scope:       ops.ScopeAdmin,
		Risk:        ops.RiskSafe,
		NoDelegate:  true,
		Handle: func(ctx context.Context, call *ops.Call, in TestIn) (TestOut, error) {
			if err := ready(call, s, "送測試推播"); err != nil {
				return TestOut{}, err
			}
			if call.DryRun {
				return TestOut{}, nil
			}
			var pick func(Subscription) bool
			if in.Endpoint != "" {
				pick = func(x Subscription) bool { return x.Endpoint == in.Endpoint }
			}
			res, err := s.Send(ctx, Message{Title: "XBot 推播測試", Body: "看到這則就表示推播設定好了", URL: "/settings", Tag: "xbot-test", Kind: "test"}, pick)
			return TestOut{Sent: res.Sent, Failed: res.Failed}, err
		},
	})
}

func ready(call *ops.Call, s *Service, what string) error {
	if call.Identity.Kind != ops.KindOwner {
		return ops.Forbidden("只有你本人能" + what)
	}
	if s == nil || s.DB == nil {
		return ops.Internal(errors.New("推播服務沒有啟動"))
	}
	return nil
}

// ConfigIn 是 xbot.push.config 的輸入（沒有欄位）。
type ConfigIn struct{}

// ConfigOut 是 xbot.push.config 的輸出。
type ConfigOut struct {
	PublicKey     string             `json:"public_key" desc:"VAPID 公鑰（base64url），給 pushManager.subscribe 的 applicationServerKey"`
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

// SubscriptionView 是一個已訂閱的裝置（不含金鑰）。
type SubscriptionView struct {
	ID           string `json:"id"`
	Label        string `json:"label,omitempty" desc:"裝置名稱，例：Pixel 7"`
	NotifyDone   bool   `json:"notify_done" desc:"工作完成也通知"`
	CreatedAt    int64  `json:"created_at"`
	EndpointHost string `json:"endpoint_host" desc:"推播服務，例：fcm.googleapis.com"`
}

// SubscribeIn 是 xbot.push.subscribe 的輸入（瀏覽器 PushSubscription.toJSON() 的內容）。
type SubscribeIn struct {
	Endpoint   string `json:"endpoint" required:"true" max:"2000" desc:"PushSubscription.endpoint（https）"`
	P256dh     string `json:"p256dh" required:"true" max:"200" desc:"keys.p256dh"`
	Auth       string `json:"auth" required:"true" max:"100" desc:"keys.auth"`
	Label      string `json:"label,omitempty" max:"60" desc:"裝置名稱，例：Pixel 7"`
	NotifyDone bool   `json:"notify_done,omitempty" desc:"工作完成也通知（預設只推要你決定的事）"`
}

// SubscribeOut 是 xbot.push.subscribe 的輸出。
type SubscribeOut struct {
	ID string `json:"id"`
}

// UnsubscribeIn 是 xbot.push.unsubscribe 的輸入。
type UnsubscribeIn struct {
	Endpoint string `json:"endpoint,omitempty" max:"2000" desc:"要關掉的裝置的 endpoint"`
	ID       string `json:"id,omitempty" max:"40" desc:"或用 xbot.push.config 列出的 id"`
}

// UnsubscribeOut 是 xbot.push.unsubscribe 的輸出。
type UnsubscribeOut struct {
	Removed bool `json:"removed"`
}

// TestIn 是 xbot.push.test 的輸入。
type TestIn struct {
	Endpoint string `json:"endpoint,omitempty" max:"2000" desc:"只送到這個裝置；不給就送到全部"`
}

// TestOut 是 xbot.push.test 的輸出。
type TestOut struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}
