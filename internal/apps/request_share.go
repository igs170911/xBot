package apps

import (
	"context"
	"encoding/json"
	"strings"

	"xbot/internal/ops"
)

// Bot 需要別的 Bot 已登入的網站時（例：水哥哥登入了 Threads，小編也要用），可以請使用者分享（使用者決定 2026-10-19：
// 「可以問我可不可以分享，不要不能共用」）。送一張核准卡給使用者；允許後用 xbot.logins.share 複製那幾個網站的登入。

// RequestShareIn 是 xbot.logins.request_share 的輸入。
type RequestShareIn struct {
	From   string   `json:"from" required:"true" max:"100" desc:"已經登入的 Bot（名字或 id）"`
	Sites  []string `json:"sites" required:"true" min:"1" max:"20" desc:"要分享的網站，例：threads.com"`
	Reason string   `json:"reason,omitempty" max:"500" desc:"為什麼需要（給使用者看）"`
}

// RequestShareOut 是 xbot.logins.request_share 的輸出（使用者允許後）。
type RequestShareOut struct {
	From    string   `json:"from"`
	Sites   []string `json:"sites"`
	ShareID string   `json:"share_id,omitempty"`
}

// shareSource 檢查請求：呼叫的是 Bot、來源 Bot 存在而且不是自己、網站名稱正確。送核准卡之前和允許之後都跑。
func shareSource(ctx context.Context, call *ops.Call, in RequestShareIn) (string, string, []string, error) {
	if call.Identity.Kind != ops.KindBot {
		return "", "", nil, ops.Forbidden("這是給 Bot 用的；你本人請用 xbot.logins.share")
	}
	sites, err := cleanSites(in.Sites)
	if err != nil {
		return "", "", nil, err
	}
	bots, err := call.DB.ListBots(ctx, false)
	if err != nil {
		return "", "", nil, err
	}
	ref := strings.TrimSpace(in.From)
	for _, b := range bots {
		if b.ID == ref || b.Name == ref {
			if b.ID == call.Identity.ID {
				return "", "", nil, ops.InvalidInput(ops.FieldError{Field: "from", Message: "不能分享自己的登入給自己"})
			}
			return b.ID, b.Name, sites, nil
		}
	}
	return "", "", nil, ops.NotFound("找不到這個 Bot：" + ref)
}

func registerRequestShare(reg *ops.Registry) {
	ops.Register(reg, ops.Op[RequestShareIn, RequestShareOut]{
		Name: "xbot.logins.request_share", Summary: "請使用者把另一個 Bot 已登入的網站分享給你",
		Description: "例：水哥哥登入了 threads.com，你也需要時用它。會送一張核准卡給使用者，使用者允許後才複製登入（cookies，看不到密碼）；" +
			"你原本同網站的登入會被取代。不要因為「登入不能共用」就放棄，先用這個問使用者。",
		Scope: ops.ScopeOperate, Risk: ops.RiskConfirm, ForBot: true,
		Precheck: func(ctx context.Context, call *ops.Call, in RequestShareIn) error {
			_, _, _, err := shareSource(ctx, call, in)
			return err
		},
		Handle: func(ctx context.Context, call *ops.Call, in RequestShareIn) (RequestShareOut, error) {
			from, name, sites, err := shareSource(ctx, call, in)
			if err != nil {
				return RequestShareOut{}, err
			}
			out := RequestShareOut{From: name, Sites: sites}
			if call.DryRun {
				return out, nil
			}
			// 到這裡代表使用者已經按了允許：用使用者分享登入的同一個操作
			raw, _ := json.Marshal(ShareIn{FromBotID: from, ToBotIDs: []string{call.Identity.ID}, Sites: sites})
			res, err := reg.Invoke(ctx, ops.Owner("approved"), "xbot.logins.share", raw, ops.Options{})
			if err != nil {
				return out, err
			}
			var shared ShareOut
			_ = json.Unmarshal(res.Output, &shared)
			out.ShareID = shared.ShareID
			return out, nil
		},
	})
}
