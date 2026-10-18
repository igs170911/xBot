package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// TestBudgetCapStopsBotAndCreatesCard 是 E11 的驗收：設 1 美元每日上限的 Bot，花到上限時
// 模型請求被擋下、正在做的工作停下、「待我處理」出現一張（只有一張）卡片；之後再交辦也會被擋。
// 引擎是假的 Hermes gateway：每一輪不停地經真的 Model Proxy 呼叫假的供應商（每次回報花費 $0.40）。
func TestBudgetCapStopsBotAndCreatesCard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"…"}}],"usage":{"prompt_tokens":1000,"completion_tokens":100,"cost":0.4}}`)
	}))
	defer upstream.Close()

	kr, _ := proxy.NewKeyring(bytes.Repeat([]byte{3}, 32))
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	comp := &runtime.Computers{DB: db, Providers: pstore}
	var rt *runtime.Runtime
	budgets := &proxy.Budgets{DB: db, OnExceeded: func(e *proxy.BudgetError) {
		go func() { _, _ = rt.Stop(context.Background(), e.BotID, "") }()
	}}
	model := httptest.NewServer(proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore,
		Bindings: comp, Budget: budgets, Pricing: &proxy.Pricing{DB: db}}))
	defer model.Close()

	g := hermestest.New("wk")
	defer g.Close()
	var workerKey string
	g.OnPrompt = func(turn *hermestest.Turn) {
		turn.Event("message.start", nil)
		// 像真的 agent 一樣一直呼叫模型，直到被擋或被停下。
		for range 20 {
			select {
			case <-turn.Interrupted():
				turn.Complete("", "interrupted", nil)
				return
			default:
			}
			req, _ := http.NewRequest("POST", model.URL+"/model/paid/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
			req.Header.Set("Authorization", "Bearer "+workerKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				turn.Complete("", "error", nil)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusPaymentRequired {
				break // Hermes 收到 402 會把錯誤顯示出來，這一輪等著被停下
			}
		}
		select {
		case <-turn.Interrupted():
			turn.Complete("", "interrupted", nil)
		case <-time.After(10 * time.Second):
			turn.Complete("沒有被停下", "complete", nil)
		}
	}
	drv := hermes.New(func(context.Context, string) (hermes.Endpoint, error) {
		return hermes.Endpoint{Addr: g.Addr(), Key: "wk"}, nil
	}, nil)
	rt = runtime.New(runtime.Options{DB: db, Driver: drv, Bus: events.New(), Models: comp,
		Budget: func(ctx context.Context, botID, providerID string) error {
			if err := budgets.Check(ctx, botID); err != nil {
				return err
			}
			return budgets.CheckProvider(ctx, botID, providerID)
		}})
	reg := ops.New(ops.Config{DB: db})
	ops.RegisterBuiltin(reg)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore, Budgets: budgets})
	owner := ops.Owner("test")
	invoke := func(op string, in any, out any) error {
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(ctx, owner, op, raw, ops.Options{})
		if err != nil {
			return err
		}
		if out != nil {
			return json.Unmarshal(res.Output, out)
		}
		return nil
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(invoke("xbot.models.add_provider", map[string]any{"id": "paid", "base_url": upstream.URL + "/v1", "api_key": "sk-paid", "default_model": "m"}, nil))
	var bot struct{ ID string }
	must(invoke("xbot.bots.create", map[string]any{"name": "小秘"}, &bot))
	k, err := keys.Issue(ctx, bot.ID)
	must(err)
	workerKey = string(k)
	var capView struct {
		DailyUSD float64 `json:"daily_usd"`
	}
	must(invoke("xbot.usage.set_caps", map[string]any{"bot_id": bot.ID, "daily_usd": 1}, &capView))
	if capView.DailyUSD != 1 {
		t.Fatalf("上限：%+v", capView)
	}

	var sent struct {
		TaskID string `json:"task_id"`
	}
	must(invoke("xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "做一份很長的研究"}, &sent))
	task, err := rt.Wait(ctx, sent.TaskID)
	must(err)
	if task.Verdict != runtime.VerdictCancelled {
		t.Fatalf("工作應該被停下：%+v", task)
	}
	// $0.40 × 3 = $1.20 ≥ $1：第 3 次之後就不再送到供應商。
	if n := upstreamCalls.Load(); n != 3 {
		t.Fatalf("供應商被呼叫 %d 次", n)
	}
	items, err := db.ListInbox(ctx, "open")
	must(err)
	if len(items) != 1 || items[0].Kind != "budget" || items[0].BotID != bot.ID || !strings.Contains(items[0].Title, "小秘") {
		t.Fatalf("待我處理：%+v", items)
	}
	// 再交辦：直接回 budget，不會開新的一輪，也不會再發卡。
	err = invoke("xbot.chat.send", map[string]any{"bot_id": bot.ID, "text": "再做一次"}, nil)
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeBudget {
		t.Fatalf("超過上限再交辦：%v", err)
	}
	if items, _ := db.ListInbox(ctx, "open"); len(items) != 1 {
		t.Fatalf("卡片只能有一張：%d", len(items))
	}
	// 用量查得到，花費正確。
	var sum struct {
		Total struct {
			Calls      int64 `json:"calls"`
			CostMicros int64 `json:"cost_micros"`
		} `json:"total"`
	}
	must(invoke("xbot.usage.summary", map[string]any{"bot_id": bot.ID}, &sum))
	if sum.Total.Calls != 3 || sum.Total.CostMicros != 1_200_000 {
		t.Fatalf("用量：%+v", sum)
	}
	// 降低上限是 safe，但提高上限要核准：代理送出會進待我處理。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeManage}}
	raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "daily_usd": 5})
	if _, err := reg.Invoke(ctx, agent, "xbot.usage.set_caps", raw, ops.Options{}); !errors.As(err, &oe) || oe.Code != ops.CodePendingApproval {
		t.Fatalf("提高上限要核准：%v", err)
	}
	raw, _ = json.Marshal(map[string]any{"bot_id": bot.ID, "daily_usd": 0.5})
	if _, err := reg.Invoke(ctx, agent, "xbot.usage.set_caps", raw, ops.Options{}); err != nil {
		t.Fatalf("降低上限直接做：%v", err)
	}
}
