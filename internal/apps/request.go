package apps

import (
	"context"
	"encoding/json"
	"strings"

	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Bot 請你連接應用程式（CH-7「連接 App」卡、IN-1 登入請求）：在「待我處理」出一張 signin 卡，
// 連接好並授權給這個 Bot 後自動結掉。

// RequestIn 是 xbot.apps.request 的輸入（Bot 用）。
type RequestIn struct {
	AppID  string `json:"app_id" required:"true" max:"64" desc:"想用的應用程式（xbot.apps.catalog 的 id）；清單外的服務寫 custom"`
	Reason string `json:"reason,omitempty" max:"500" desc:"為什麼需要（一句話，給使用者看）"`
}

// RequestOut 是 xbot.apps.request 的輸出。
type RequestOut struct {
	ItemID string `json:"item_id,omitempty"`
	Status string `json:"status" desc:"pending：已請使用者連接；ready：你已經可以用了"`
	Note   string `json:"note"`
}

// requestPayload 是 signin 卡的內容（畫面用：開 I01／I02）。
type requestPayload struct {
	AppID  string `json:"app_id"`
	Title  string `json:"title"`
	Reason string `json:"reason,omitempty"`
}

func (s *Service) request(ctx context.Context, call *ops.Call, in RequestIn) (RequestOut, error) {
	botID := call.Identity.ID
	if call.Identity.Kind != ops.KindBot || botID == "" {
		return RequestOut{}, ops.Forbidden("只有 Bot 自己可以請使用者連接應用程式")
	}
	title := in.AppID
	if app, ok := Lookup(in.AppID); ok {
		title = app.Title
	} else if in.AppID != CustomApp {
		return RequestOut{}, ops.NotFound("目錄裡沒有這個應用程式，清單外的服務請用 custom")
	}
	conns, err := s.o.DB.AppConnectionsForBot(ctx, botID)
	if err != nil {
		return RequestOut{}, ops.Internal(err)
	}
	for _, c := range conns {
		if c.AppID == in.AppID && c.Status == StatusOK {
			return RequestOut{Status: "ready", Note: "你已經可以用「" + c.Title + "」了（工具名稱 mcp__" + c.Name + "__*）；下一個對話開始會出現在工具清單。"}, nil
		}
	}
	ref := "app-request:" + in.AppID + ":" + botID
	open, err := s.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"signin"}, BotID: botID})
	if err != nil {
		return RequestOut{}, ops.Internal(err)
	}
	for _, it := range open {
		if it.Ref == ref {
			return RequestOut{ItemID: it.ID, Status: "pending", Note: "已經請使用者連接了，連接好會通知你；先做其他能做的部分。"}, nil
		}
	}
	if call.DryRun {
		return RequestOut{Status: "pending", Note: "會請使用者連接「" + title + "」"}, nil
	}
	payload, _ := json.Marshal(requestPayload{AppID: in.AppID, Title: title, Reason: strings.TrimSpace(in.Reason)})
	var created store.Event
	var item store.InboxItem
	err = s.o.DB.Tx(ctx, func(q *store.Queries) error {
		var err error
		item, err = q.CreateInboxItem(ctx, store.InboxItem{Kind: "signin", BotID: botID, Ref: ref, Title: "想連接「" + title + "」",
			Body: strings.TrimSpace(in.Reason), Payload: payload})
		if err != nil {
			return err
		}
		created, err = q.AppendEvent(ctx, notify.Event(notify.KindCreated, item))
		return err
	})
	if err != nil {
		return RequestOut{}, ops.Internal(err)
	}
	s.publish(created)
	return RequestOut{ItemID: item.ID, Status: "pending", Note: "已請使用者連接「" + title + "」。連接好之後下一個對話就能用；先做其他能做的部分。"}, nil
}

// closeSigninCards 結掉「想連接」的卡：連接 a 好了，而且卡片的 Bot 現在可以用它。
func (s *Service) closeSigninCards(ctx context.Context, call *ops.Call, a store.AppConnection) {
	if a.Status != StatusOK {
		return
	}
	items, err := s.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"signin"}})
	if err != nil {
		return
	}
	actor := "owner"
	if call != nil {
		actor = call.Identity.Actor()
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Ref, "app-request:"+a.AppID+":") {
			continue
		}
		if ok, err := s.o.DB.AppGranted(ctx, a.ID, it.BotID); err != nil || !ok {
			continue
		}
		var ev store.Event
		err := s.o.DB.Tx(ctx, func(q *store.Queries) error {
			if err := q.SettleInboxItem(ctx, it.ID, store.InboxResolved, "provided", actor, nil); err != nil {
				return err
			}
			cur, err := q.GetInboxItem(ctx, it.ID)
			if err != nil {
				return err
			}
			ev, err = q.AppendEvent(ctx, notify.Event(notify.KindUpdated, cur))
			return err
		})
		if err == nil {
			s.publish(ev)
		}
	}
}

func (s *Service) publish(evs ...store.Event) {
	if s.notify != nil {
		s.notify(evs...)
	}
}
