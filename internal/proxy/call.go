package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CallRequest 是 XBot 服務自己（不是 Bot 的電腦）呼叫模型的請求，例：快判（X05）。
// 金鑰一樣只在 Model Proxy 裡；用量照常記帳，Engine 標記是誰用的（例：judge），花費上限照全域與供應商的上限檢查。
type CallRequest struct {
	Provider string
	// Path 是 /v1/ 之後的路徑，例：chat/completions。
	Path string
	Body []byte
	// Engine 是用量帳本上的標記（usage_entry.engine），例：judge。
	Engine string
	// Session 是給要求對話識別標頭的供應商（OpenCode 的 x-opencode-session）的 id；空白時用「xbot-<Engine>」。
	Session string
	// MaxBytes 是最多讀多少回應；0 用 1 MiB。
	MaxBytes int64
}

// CallResponse 是供應商的回應（狀態碼與內容）。
type CallResponse struct {
	Status int
	Body   []byte
}

// ErrCallPath 表示這個路徑不能由服務內部呼叫。
var ErrCallPath = errors.New("模型代理不開放這個路徑")

// Call 由 XBot 服務內部呼叫模型：讀出供應商（含真金鑰）→ 檢查全域與供應商的花費上限 → 送出 → 讀回應 → 記用量。
// 供應商沒設定時回 ErrNotFound；超過花費上限時回 *BudgetError（不發卡片：內部用途會退回規則）。
// 不做備援（內部用途有自己的退路），不串流。
func (h *Handler) Call(ctx context.Context, req CallRequest) (CallResponse, error) {
	if !meteredPaths[req.Path] {
		return CallResponse{}, fmt.Errorf("%w：%s", ErrCallPath, req.Path)
	}
	provider, err := h.load(ctx, req.Provider, false)
	if err != nil {
		return CallResponse{}, err
	}
	if provider.Auth != "" && !codexPathAllowed(http.MethodPost, req.Path) {
		return CallResponse{}, fmt.Errorf("%w：%s", ErrCallPath, req.Path)
	}
	if h.o.Budget != nil {
		if err := h.o.Budget.Check(ctx, ""); err != nil {
			return CallResponse{}, err
		}
		if err := h.o.Budget.CheckProvider(ctx, "", provider.ID); err != nil {
			return CallResponse{}, err
		}
	}
	session := req.Session
	if session == "" {
		session = "xbot-" + req.Engine
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, Prefix+provider.ID+"/v1/"+req.Path, nil)
	if err != nil {
		return CallResponse{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	start := h.o.Now()
	model := requestModel(req.Body)
	resp, cancel, err := h.forward(r, session, provider, req.Path, req.Body)
	if err != nil {
		return CallResponse{}, errors.New(redactString(err.Error(), string(provider.APIKey)))
	}
	defer cancel()
	defer resp.Body.Close()
	limit := req.MaxBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, limit))
	// ChatGPT 方案一律串流（codexBody），回應是 SSE：照 SSE 讀用量，不然記成 0（同 handler 的修正）。
	m := newMeter(provider.Auth == AuthOAuthCodex || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"))
	_, _ = m.Write(body)
	if h.o.Usage != nil {
		c, _ := m.result()
		uctx := context.WithoutCancel(ctx)
		entry := UsageEntry{
			Provider: provider.ID, Model: model, Endpoint: req.Path, Engine: req.Engine,
			InputTokens: c.Input, OutputTokens: c.Output, CachedTokens: c.Cached, CacheWriteTokens: c.CacheWrite,
			ReasoningTokens: c.Reasoning, Cost: c.Cost, Status: resp.StatusCode, OK: resp.StatusCode < 400 && rerr == nil, At: start,
		}
		if provider.Auth == "" {
			entry.CostMicros, entry.Unpriced = h.cost(uctx, provider.ID, model, c)
		}
		if err := h.o.Usage.RecordUsage(uctx, entry); err != nil {
			h.o.Logger.Error("模型代理：記錄用量失敗", "engine", req.Engine, "provider", provider.ID, "err", err)
		}
	}
	if rerr != nil {
		return CallResponse{Status: resp.StatusCode}, errors.New(redactString(rerr.Error(), string(provider.APIKey)))
	}
	return CallResponse{Status: resp.StatusCode, Body: []byte(redactString(string(body), string(provider.APIKey)))}, nil
}
