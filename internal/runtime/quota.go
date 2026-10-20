package runtime

// EN-7：引擎額度用完（或限流、餘額不足、登入過期）時，這一輪以清楚的說明結束，並開一張「額度用完」卡（要你決定）：
// 換成你有的另一個引擎或模型（xbot.engines.switch），或等額度恢復。卡片內容也放在那則回覆的 meta.quota，對話裡同一張卡。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// ResolutionReplaced 是舊的額度卡被同一個 Bot 新的額度卡取代。
const ResolutionReplaced = "replaced"

// QuotaView 是額度卡的內容（inbox payload，也是回覆訊息的 meta.quota）。
type QuotaView struct {
	InboxID  string `json:"inbox_id,omitempty" desc:"額度用完卡（待我處理）的 id"`
	Engine   string `json:"engine"`
	Provider string `json:"provider,omitempty" desc:"Hermes 的 Model Proxy 供應商"`
	Kind     string `json:"kind" desc:"quota 額度用完、rate_limit 限流、funds 餘額不足、auth 登入過期"`
	ResetAt  int64  `json:"reset_at,omitempty" desc:"預計恢復的時間（毫秒）；0 是不知道"`
	Message  string `json:"message,omitempty" desc:"引擎或供應商原本的訊息"`
	// Label 是引擎（Hermes 時是供應商）給人看的名稱。
	Label    string `json:"label"`
	BotName  string `json:"bot_name"`
	ThreadID string `json:"thread_id"`
	TaskID   string `json:"task_id"`
	// RetryText 是可以重送的那則訊息（你自己交辦的才有）。
	RetryText string                `json:"retry_text,omitempty"`
	Options   []engine.SwitchOption `json:"options"`
}

// quotaOf 找出這一輪失敗是不是額度類的錯誤：引擎事件帶的 → Model Proxy 這一輪看到的 → 錯誤文字。
func (r *Runtime) quotaOf(t *turn, text string) *engine.QuotaError {
	t.mu.Lock()
	q, eng, provider, started := t.quota, t.engine, t.provider, t.started
	t.mu.Unlock()
	if eng == "" {
		return nil // 還沒開始就失敗（例：讀不到 Bot），不是引擎的問題
	}
	if q == nil && eng == hermes.Name && r.o.QuotaLog != nil && !started.IsZero() {
		q = r.o.QuotaLog.Since(t.botID, started)
	}
	if q == nil {
		q = engine.ClassifyQuota(eng, text, time.Now())
	}
	if q == nil {
		return nil
	}
	out := *q
	if out.Engine == "" {
		out.Engine = eng
	}
	if out.Engine == hermes.Name && out.Provider == "" {
		out.Provider = provider
	}
	return &out
}

// quotaTitle 是額度卡的標題，例：「Claude Code 的額度用完了（約 14:20 恢復）」。
func quotaTitle(label string, q engine.QuotaError, now time.Time) string {
	var t string
	switch q.Kind {
	case engine.QuotaFunds:
		t = label + " 的餘額不足"
	case engine.QuotaAuth:
		return label + " 的登入過期了"
	case engine.QuotaRateLimit:
		t = label + " 暫時用量太多，被限流了"
	default:
		t = label + " 的額度用完了"
	}
	if q.ResetAt > 0 {
		at := time.UnixMilli(q.ResetAt).In(now.Location())
		y1, m1, d1 := at.Date()
		y2, m2, d2 := now.Date()
		if y1 == y2 && m1 == m2 && d1 == d2 {
			t += "（約 " + at.Format("15:04") + " 恢復）"
		} else {
			t += "（約 " + at.Format("1/2 15:04") + " 恢復）"
		}
	}
	return t
}

func quotaBody(q engine.QuotaError, options int) string {
	var b string
	switch q.Kind {
	case engine.QuotaAuth:
		// 登入過期不會自己恢復：說去哪裡重新登入，不寫「等額度恢復」（2026-10-21 使用者截圖）
		if q.Engine == hermes.Name {
			b = "金鑰或登入失效了，要到「AI 模型」重新設定。"
		} else {
			b = "到 Bot 設定 ›「AI 帳號與模型」重新登入。"
		}
		if options > 0 {
			return b + "也可以換成你有的其他引擎或模型繼續。"
		}
		return b
	case engine.QuotaFunds:
		b = "要先儲值。"
	}
	if options > 0 {
		return b + "可以換成你有的其他引擎或模型繼續，或等額度恢復再請它做。"
	}
	return b + "等額度恢復再請它做；也可以到 Bot 設定換別的引擎。"
}

// quotaCard 開額度卡（同一個 Bot 舊的、還開著的額度卡收掉），回傳要放進回覆 meta 的內容與給人看的說明。
func (r *Runtime) quotaCard(t *turn, q *engine.QuotaError) (QuotaView, string) {
	ctx := context.Background()
	label, options := q.Engine, []engine.SwitchOption(nil)
	if a, ok := acp.Agents[q.Engine]; ok {
		label = a.Label
	} else if q.Engine == hermes.Name {
		label = firstNonEmpty(q.Provider, "Hermes")
	}
	if r.o.QuotaCard != nil {
		label, options = r.o.QuotaCard(ctx, t.botID, q)
	}
	if options == nil {
		options = []engine.SwitchOption{}
	}
	t.mu.Lock()
	v := QuotaView{Engine: q.Engine, Provider: q.Provider, Kind: q.Kind, ResetAt: q.ResetAt, Message: q.Message, Label: label, BotName: t.botName, ThreadID: t.threadID, TaskID: t.taskID, Options: options}
	if t.source == SourceOwner {
		v.RetryText = t.orig
	}
	t.mu.Unlock()
	title := quotaTitle(label, *q, time.Now())
	body := quotaBody(*q, len(options))
	text := title + "。" + body
	// 供應商原本的訊息（例：限流的說明）附在後面；登入過期的那段是 XBot 自己寫的同一件事，不重複（細節留在卡片裡）
	if q.Message != "" && q.Kind != engine.QuotaAuth {
		text += "\n\n原本的訊息：" + q.Message
	}
	if old, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{engine.QuotaCardKind}, BotID: t.botID}); err == nil {
		for _, it := range old {
			if r.o.DB.SettleInboxItem(ctx, it.ID, store.InboxResolved, ResolutionReplaced, "system", nil) == nil {
				if cur, err := r.o.DB.GetInboxItem(ctx, it.ID); err == nil {
					r.publishStored(notify.Event(notify.KindUpdated, cur))
				}
			}
		}
	}
	raw, _ := json.Marshal(v)
	created, err := r.o.DB.CreateInboxItem(ctx, store.InboxItem{Kind: engine.QuotaCardKind, BotID: t.botID, TaskID: t.taskID, ThreadID: t.threadID,
		Ref: fmt.Sprintf("quota:%s:%s", t.botID, t.taskID), Title: title, Body: body, Payload: raw})
	if err != nil {
		r.log.Warn("寫入額度用完卡失敗", "bot", t.botID, "err", err)
		return v, text
	}
	v.InboxID = created.ID
	r.emitStored(t, notify.Event(notify.KindCreated, created))
	r.log.Info("引擎額度用完", "bot", t.botID, "engine", q.Engine, "provider", q.Provider, "kind", q.Kind, "reset_at", q.ResetAt)
	return v, text
}
