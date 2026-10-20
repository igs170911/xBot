package engines

// EN-7：引擎額度用完時，額度卡列出你有的其他引擎與模型；xbot.engines.switch 把 Bot 換過去（需要時順便同意使用訂閱），
// 收掉額度卡，並可以把那一輪的訊息再送一次。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ModelChoice 是 Hermes 可以換用的一個模型（Model Proxy 設定好的供應商）。
type ModelChoice struct {
	Provider, Model string
	// ProviderLabel 是供應商給人看的名稱（例：OpenCode Go）。
	ProviderLabel string
}

// ResolutionSwitched 是額度卡因為換了引擎而收掉。
const ResolutionSwitched = "switched"

// QuotaCard 給 runtime 開額度卡用：這個引擎（或 Hermes 的供應商）給人看的名稱，以及可以換用的選項。
// 選項不含出問題的那個引擎（Hermes 時是那個供應商）；ACP 引擎只列這台電腦上登入過的。
func (s *Service) QuotaCard(ctx context.Context, botID string, q *engine.QuotaError) (string, []engine.SwitchOption) {
	var models []ModelChoice
	if s.Models != nil {
		var err error
		if models, err = s.Models(ctx); err != nil {
			s.log().Warn("讀不到可以換用的模型", "err", err)
		}
	}
	name := label(q.Engine)
	if q.Engine == hermes.Name && q.Provider != "" {
		name = q.Provider
		for _, m := range models {
			if m.Provider == q.Provider && m.ProviderLabel != "" {
				name = m.ProviderLabel
				break
			}
		}
	}
	var out []engine.SwitchOption
	for _, e := range acp.Engines {
		if e == q.Engine || len(out) >= limits.QuotaChoicesMax {
			continue
		}
		sp := Specs[e]
		ready, err := s.Ready(ctx, botID, e)
		if err != nil {
			continue
		}
		if !ready {
			if _, err := s.ReadHost(sp); err != nil {
				continue // 這個 Bot 沒登入、這台電腦也沒登入：換過去也不能用
			}
		}
		o := engine.SwitchOption{Engine: e, Label: sp.Label + "（" + sp.Plan + " 帳號）", Note: "已登入", NeedsLink: !ready}
		if !ready {
			o.Note = "要你同意用這台電腦的登入"
		}
		out = append(out, o)
	}
	seen := map[string]bool{}
	for _, m := range models {
		ref := m.Provider + "/" + m.Model
		if len(out) >= limits.QuotaChoicesMax || m.Model == "" || seen[ref] || (q.Engine == hermes.Name && m.Provider == q.Provider) {
			continue
		}
		seen[ref] = true
		out = append(out, engine.SwitchOption{Engine: hermes.Name, Model: ref,
			Label: "Hermes · " + m.Model + "（" + firstNonEmpty(m.ProviderLabel, m.Provider) + "）", Note: "用 AI 模型設定"})
	}
	return name, out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// SwitchEngine 是可以換過去的引擎（含 Hermes）。
type SwitchEngine string

// EnumValues 讓 JSON Schema 列出可用值。
func (SwitchEngine) EnumValues() []string { return append([]string{hermes.Name}, acp.Engines...) }

// SwitchIn 是 xbot.engines.switch 的輸入。
type SwitchIn struct {
	BotID  string       `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Engine SwitchEngine `json:"engine" required:"true" desc:"換到哪個引擎"`
	Model  string       `json:"model,omitempty" max:"200" desc:"Hermes：「<供應商>/<模型>」，空白用預設模型；ACP 引擎空白＝它自己的預設模型"`
	Link   bool         `json:"link,omitempty" desc:"同意讓這個 Bot 用你在這台電腦上這個 CLI 的訂閱登入（換到要訂閱的引擎、還沒同意過時要帶）"`
	Resend bool         `json:"resend,omitempty" desc:"換好之後，把額度用完那一輪的訊息在同一個對話串再送一次（要帶 inbox_id）"`
	// InboxID 是額度用完卡；換好後收掉，重送時用它記下的對話串與訊息。
	InboxID string `json:"inbox_id,omitempty" max:"64" desc:"額度用完卡的 id（xbot.inbox.list，kind=engine_quota）"`
}

// SwitchOut 是 xbot.engines.switch 的輸出。
type SwitchOut struct {
	Bot   BotStatus `json:"bot"`
	Model string    `json:"model" desc:"Bot 現在的模型設定；空白是預設"`
	// TaskID、ThreadID：重送的那一則（resend 時）。
	TaskID   string `json:"task_id,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

// quotaPayload 是額度卡 payload 裡 switch 用得到的欄位（runtime 寫的）。
type quotaPayload struct {
	ThreadID  string `json:"thread_id"`
	RetryText string `json:"retry_text"`
}

func registerSwitch(reg *ops.Registry, svc *Service) {
	ops.Register(reg, ops.Op[SwitchIn, SwitchOut]{
		Name: "xbot.engines.switch", Summary: "把一個 Bot 換到另一個引擎或模型（額度用完時）",
		Description: "改 Bot 的引擎與模型；要用你的訂閱而還沒同意過時，帶 link=true 表示你同意（和 xbot.engines.link_credentials 一樣）。" +
			"帶 inbox_id 會收掉那張額度用完卡，再帶 resend=true 會把那一輪的訊息在同一個對話串再送一次。Bot 正在做事時不能換。只有你本人能做。",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SwitchIn) (SwitchOut, error) {
			if svc == nil {
				return SwitchOut{}, ops.Internal(errors.New("引擎服務沒有啟動"))
			}
			return svc.switchEngine(ctx, reg, call, in)
		},
	})
}

func (s *Service) switchEngine(ctx context.Context, reg *ops.Registry, call *ops.Call, in SwitchIn) (SwitchOut, error) {
	if call.Identity.Kind != ops.KindOwner {
		return SwitchOut{}, ops.Forbidden("只有你本人能幫 Bot 換引擎（可能要用你的訂閱）")
	}
	b, err := getBot(ctx, call, in.BotID)
	if err != nil {
		return SwitchOut{}, err
	}
	target := string(in.Engine)
	if !slices.Contains((SwitchEngine("")).EnumValues(), target) {
		return SwitchOut{}, ops.InvalidInput(ops.FieldError{Field: "engine", Message: "不支援這個引擎"})
	}
	model := strings.TrimSpace(in.Model)
	if target == hermes.Name && model != "" && !strings.Contains(model, "/") {
		return SwitchOut{}, ops.InvalidInput(ops.FieldError{Field: "model", Message: "Hermes 的模型要寫成「<供應商>/<模型>」"})
	}
	var card store.InboxItem
	var p quotaPayload
	if in.InboxID != "" {
		if card, err = call.DB.GetInboxItem(ctx, in.InboxID); errors.Is(err, store.ErrNotFound) || (err == nil && (card.Kind != engine.QuotaCardKind || card.BotID != b.ID)) {
			return SwitchOut{}, ops.NotFound("找不到這個 Bot 的額度用完卡")
		} else if err != nil {
			return SwitchOut{}, err
		}
		_ = json.Unmarshal(card.Payload, &p)
	}
	if in.Resend && (p.ThreadID == "" || strings.TrimSpace(p.RetryText) == "") {
		return SwitchOut{}, ops.InvalidInput(ops.FieldError{Field: "resend", Message: "重送要帶額度用完卡（inbox_id），而且那張卡記得要重送的訊息"})
	}
	if s.Busy != nil && s.Busy(b.ID) {
		return SwitchOut{}, ops.Conflict("這個 Bot 正在做事，等它停下來再換引擎")
	}
	sp, needsSub := Specs[target]
	needLink := false
	if needsSub {
		ready, err := s.Ready(ctx, b.ID, target)
		if err != nil {
			return SwitchOut{}, err
		}
		if !ready {
			if !in.Link {
				return SwitchOut{}, ops.Conflict(fmt.Sprintf("%s 要用你的 %s 訂閱。確定讓%s用的話，帶 link=true 表示同意", sp.Label, sp.Plan, b.Name))
			}
			if _, err := s.ReadHost(sp); err != nil {
				if errors.Is(err, ErrNoHostLogin) {
					return SwitchOut{}, ops.Conflict(err.Error())
				}
				return SwitchOut{}, err
			}
			needLink = true
		}
	}
	b.Engine, b.Model = target, model
	b.UpdatedBy = call.Identity.Actor()
	if call.DryRun {
		v := BotStatus{BotID: b.ID, Name: b.Name, Engine: target, EngineLabel: label(target), NeedsLink: needsSub, Linked: needsSub}
		if needsSub {
			v.LinkedEngine, v.Marker = target, sp.Marker()
		}
		return SwitchOut{Bot: v, Model: model}, nil
	}
	if b, err = call.DB.UpdateBot(ctx, b); err != nil {
		return SwitchOut{}, err
	}
	if needLink {
		if _, err := s.Link(ctx, b.ID, target); err != nil {
			return SwitchOut{}, err
		}
	}
	// 舊引擎的行程不再用：關掉（下次對話照新的引擎開）；之前記下的額度錯誤也不算了。
	if s.Close != nil {
		s.Close(b.ID)
	}
	// Bot 自己登入的憑證跟著換：新的放進家目錄，舊的收回（EN-2a）。
	if err := s.Sync(ctx, b.ID); err != nil {
		s.log().Warn("換引擎後搬登入資料失敗，開機前再搬", "bot", b.ID, "err", err)
	}
	if s.Forget != nil {
		s.Forget(b.ID)
	}
	if err := s.settleCards(ctx, reg, call, b.ID); err != nil {
		s.log().Warn("收掉額度用完卡失敗", "bot", b.ID, "err", err)
	}
	out := SwitchOut{Model: b.Model}
	if out.Bot, err = s.view(ctx, b); err != nil {
		return SwitchOut{}, err
	}
	if in.Resend {
		if s.Resend == nil {
			return out, ops.Internal(errors.New("不能重送訊息"))
		}
		task, err := s.Resend(ctx, b.ID, p.ThreadID, p.RetryText)
		if err != nil {
			return out, fmt.Errorf("已經換好引擎，但重送訊息失敗：%w", err)
		}
		out.TaskID, out.ThreadID = task, p.ThreadID
	}
	return out, nil
}

// settleCards 收掉這個 Bot 還開著的額度用完卡（換了引擎就不用再等）。
func (s *Service) settleCards(ctx context.Context, reg *ops.Registry, call *ops.Call, botID string) error {
	open, err := call.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{engine.QuotaCardKind}, BotID: botID})
	if err != nil {
		return err
	}
	var evs []store.Event
	err = call.DB.Tx(ctx, func(q *store.Queries) error {
		for _, it := range open {
			if err := q.SettleInboxItem(ctx, it.ID, store.InboxResolved, ResolutionSwitched, call.Identity.Actor(), nil); err != nil {
				if errors.Is(err, store.ErrAlreadyDecided) {
					continue
				}
				return err
			}
			cur, err := q.GetInboxItem(ctx, it.ID)
			if err != nil {
				return err
			}
			e, err := q.AppendEvent(ctx, notify.Event(notify.KindUpdated, cur))
			if err != nil {
				return err
			}
			evs = append(evs, e)
		}
		return nil
	})
	if err == nil {
		reg.Notify(evs...)
	}
	return err
}
