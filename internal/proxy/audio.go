package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"
)

// 語音（E32，VC-1、VC-2）：OpenAI 相容的 audio/speech（合成）與 audio/transcriptions（辨識）。
// 容器經 /model/ 呼叫、XBot 服務自己經 CallMedia 呼叫，金鑰都只在 Model Proxy；用量照常記帳、花費上限照常檢查。

// audioPaths 是語音的路徑：會花錢（檢查上限、記用量），但計量方式和文字模型不同。
var audioPaths = map[string]bool{
	"audio/speech":         true,
	"audio/transcriptions": true,
}

// formModel 讀出 multipart 表單裡的 model 欄位（audio/transcriptions 用表單上傳）；讀不到回空字串。
func formModel(contentType string, body []byte) string {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		return ""
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return ""
		}
		if part.FormName() == "model" && part.FileName() == "" {
			v, _ := io.ReadAll(io.LimitReader(part, 256))
			return strings.TrimSpace(string(v))
		}
	}
}

// audioUnits 回傳語音請求按「量」計價的數量：合成是 input 的字數；辨識只回報秒數（沒有 token）時是秒數（無條件進位）。
// 其他情況回 0，照 token 算。
func audioUnits(apiPath string, reqBody []byte, c counts) int64 {
	switch apiPath {
	case "audio/speech":
		var req struct {
			Input string `json:"input"`
		}
		if json.Unmarshal(reqBody, &req) != nil {
			return 0
		}
		return int64(utf8.RuneCountInString(req.Input))
	case "audio/transcriptions":
		if c.Input == 0 && c.Output == 0 && c.Seconds > 0 {
			return int64(math.Ceil(c.Seconds))
		}
	}
	return 0
}

// costOf 算一次請求的花費：units > 0 時照「每百萬單位」的 Input 價格算（語音），否則照 token（cost）。
func (h *Handler) costOf(ctx context.Context, provider, model string, c counts, units int64) (int64, bool) {
	if units <= 0 {
		return h.cost(ctx, provider, model, c)
	}
	if c.Cost > 0 {
		return int64(math.Round(c.Cost * 1e6)), false
	}
	if h.o.Pricing == nil {
		return 0, false
	}
	pr, _, ok, err := h.o.Pricing.Lookup(ctx, provider, model)
	if err != nil {
		h.o.Logger.Warn("模型代理：讀不到價目表", "err", err)
	}
	if !ok {
		return 0, true
	}
	return pr.CostMicros(units, 0, 0, 0), false
}

// MediaRequest 是 XBot 服務自己呼叫語音 API 的請求（例：朗讀、語音輸入，E32）。
type MediaRequest struct {
	Provider string
	// Path 是 audio/speech 或 audio/transcriptions。
	Path string
	// ContentType 是請求內容的格式：合成是 application/json，辨識是 multipart/form-data; boundary=…。
	ContentType string
	Body        []byte
	// BotID 是替哪個 Bot 用的（花費算在它頭上、照它的上限檢查）；可空白。
	BotID string
	// Engine 是用量帳本上的標記，例：voice。
	Engine string
	// MaxBytes 是最多讀多少回應；0 用 16 MiB。
	MaxBytes int64
}

// MediaResponse 是供應商的回應。
type MediaResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// ErrMediaTooLarge 表示回應超過 MaxBytes。
var ErrMediaTooLarge = errors.New("供應商回的內容太大")

// CallMedia 由 XBot 服務內部呼叫語音 API：讀出供應商 → 檢查花費上限（全域、供應商、Bot）→ 送出 → 讀回應 → 記用量。
// 供應商沒設定時回 ErrNotFound；超過上限回 *BudgetError；訂閱登入的供應商不開放語音（回 ErrCallPath）。
func (h *Handler) CallMedia(ctx context.Context, req MediaRequest) (MediaResponse, error) {
	if !audioPaths[req.Path] {
		return MediaResponse{}, fmt.Errorf("%w：%s", ErrCallPath, req.Path)
	}
	provider, err := h.load(ctx, req.Provider, false)
	if err != nil {
		return MediaResponse{}, err
	}
	if provider.Auth != "" {
		return MediaResponse{}, fmt.Errorf("%w：訂閱登入的供應商沒有 %s", ErrCallPath, req.Path)
	}
	if h.o.Budget != nil {
		if err := h.o.Budget.Check(ctx, req.BotID); err != nil {
			return MediaResponse{}, err
		}
		if err := h.o.Budget.CheckProvider(ctx, req.BotID, provider.ID); err != nil {
			return MediaResponse{}, err
		}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, Prefix+provider.ID+"/v1/"+req.Path, nil)
	if err != nil {
		return MediaResponse{}, err
	}
	r.Header.Set("Content-Type", req.ContentType)
	model := requestModel(req.Body)
	if model == "" {
		model = formModel(req.ContentType, req.Body)
	}
	session := "xbot-" + req.Engine
	if req.BotID != "" {
		session = h.session(req.BotID)
	}
	start := h.o.Now()
	resp, cancel, err := h.forward(r, session, provider, req.Path, req.Body)
	if err != nil {
		return MediaResponse{}, errors.New(redactString(err.Error(), string(provider.APIKey)))
	}
	defer cancel()
	defer resp.Body.Close()
	limit := req.MaxBytes
	if limit <= 0 {
		limit = 16 << 20
	}
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if rerr == nil && int64(len(body)) > limit {
		rerr = ErrMediaTooLarge
		body = body[:limit]
	}
	var c counts
	if req.Path == "audio/transcriptions" {
		m := newMeter(strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"))
		_, _ = m.Write(body)
		c, _ = m.result()
	}
	units := audioUnits(req.Path, req.Body, c)
	ok := resp.StatusCode < 400 && rerr == nil
	if h.o.Usage != nil {
		uctx := context.WithoutCancel(ctx)
		entry := UsageEntry{
			BotID: req.BotID, Provider: provider.ID, Model: model, Endpoint: req.Path, Engine: req.Engine,
			InputTokens: c.Input, OutputTokens: c.Output, CachedTokens: c.Cached, Cost: c.Cost,
			Status: resp.StatusCode, OK: ok, At: start,
		}
		if ok { // 失敗的請求供應商不收錢
			entry.CostMicros, entry.Unpriced = h.costOf(uctx, provider.ID, model, c, units)
		}
		if err := h.o.Usage.RecordUsage(uctx, entry); err != nil {
			h.o.Logger.Error("模型代理：記錄用量失敗", "engine", req.Engine, "provider", provider.ID, "err", err)
		}
	}
	if rerr != nil {
		return MediaResponse{Status: resp.StatusCode}, errors.New(redactString(rerr.Error(), string(provider.APIKey)))
	}
	out := MediaResponse{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}
	if resp.StatusCode >= 400 || !strings.HasPrefix(out.ContentType, "audio/") {
		// 錯誤訊息、辨識結果是文字：遮掉可能被回顯的金鑰。音訊原樣回傳。
		out.Body = []byte(redactString(string(body), string(provider.APIKey)))
	}
	return out, nil
}
