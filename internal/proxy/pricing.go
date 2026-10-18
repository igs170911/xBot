package proxy

import (
	"context"
	"math"
	"regexp"
	"strings"

	"xbot/internal/store"
)

// PricingSetting 是使用者自訂價格在 setting 表的 key；值是 {"<模型>" 或 "<供應商>/<模型>": Price}。
const PricingSetting = "model.pricing"

// Price 是一個模型的價格，單位是「美元／每百萬 token」。
// CacheRead、CacheWrite 是 0 時照 Input 算（不知道有沒有快取折扣，就當沒有）。
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// 價格來源。
const (
	PriceSourceCustom  = "custom"  // 使用者設定
	PriceSourceDefault = "default" // 內建預設
)

// defaultPrices 是常見模型的預設價格（美元／每百萬 token），使用者可以用 xbot.models.set_price 覆寫。
// Claude 取自 Anthropic 2026-10 公告的第一方價格（快取寫入以 5 分鐘快取計，＝輸入 × 1.25）；
// 其他供應商取自各家公開價目，可能已經調整，不確定時請自行覆寫。
// 不在表上的模型花費記成 0，並在用量裡標示「沒有價格」。
var defaultPrices = map[string]Price{
	// Anthropic
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5},
	"claude-opus-5":     {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-8":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-7":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-6":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-5":   {Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-1":   {Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75},
	"claude-opus-4":     {Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5},
	"claude-sonnet-4-6": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-sonnet-4-5": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-sonnet-4":   {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-haiku-5-5":  {Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125},
	"claude-haiku-4-5":  {Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25},
	// OpenAI
	"gpt-5":        {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5-mini":   {Input: 0.25, Output: 2, CacheRead: 0.025},
	"gpt-5-nano":   {Input: 0.05, Output: 0.4, CacheRead: 0.005},
	"gpt-4.1":      {Input: 2, Output: 8, CacheRead: 0.5},
	"gpt-4.1-mini": {Input: 0.4, Output: 1.6, CacheRead: 0.1},
	"gpt-4.1-nano": {Input: 0.1, Output: 0.4, CacheRead: 0.025},
	"gpt-4o":       {Input: 2.5, Output: 10, CacheRead: 1.25},
	"gpt-4o-mini":  {Input: 0.15, Output: 0.6, CacheRead: 0.075},
	"o3":           {Input: 2, Output: 8, CacheRead: 0.5},
	"o4-mini":      {Input: 1.1, Output: 4.4, CacheRead: 0.275},
	// Google
	"gemini-2.5-pro":   {Input: 1.25, Output: 10, CacheRead: 0.31},
	"gemini-2.5-flash": {Input: 0.3, Output: 2.5, CacheRead: 0.075},
	// DeepSeek
	"deepseek-chat":     {Input: 0.28, Output: 0.42, CacheRead: 0.028},
	"deepseek-reasoner": {Input: 0.28, Output: 0.42, CacheRead: 0.028},
	// xAI
	"grok-4": {Input: 3, Output: 15, CacheRead: 0.75},
	// 語音（E32）。合成（audio/speech）回應裡沒有用量，照請求的字數算：Input 是「每百萬字」的價格；
	// gpt-4o-mini-tts 官方以 token 計價（約每分鐘 0.015 美元），這裡用每百萬字 15 美元估算，不準時請自行覆寫。
	// 辨識（audio/transcriptions）回報 token 時照 token 算；whisper-1 回報秒數，Input 是「每百萬秒」（每分鐘 0.006 美元）。
	"tts-1":                  {Input: 15},
	"tts-1-hd":               {Input: 30},
	"gpt-4o-mini-tts":        {Input: 15},
	"whisper-1":              {Input: 100},
	"gpt-4o-transcribe":      {Input: 2.5, Output: 10},
	"gpt-4o-mini-transcribe": {Input: 1.25, Output: 5},
}

// providerDefaultPrices 是只適用某個供應商的預設價格（key：<供應商>/<模型>，模型名照原樣）。
// 取自 OpenCode 的 models.json（2026-10）。
var providerDefaultPrices = map[string]Price{
	"opencode-go/deepseek-v4-flash":   {Input: 0.15, Output: 0.6, CacheRead: 0.003},
	"opencode-go/deepseek-v4.1-flash": {Input: 0.15, Output: 0.6, CacheRead: 0.003},
	"opencode-zen/jev-1.13":           {Input: 0.042, Output: 0},
}

var normalizedDefaults = func() map[string]Price {
	out := make(map[string]Price, len(defaultPrices))
	for k, v := range defaultPrices {
		out[NormalizeModel(k)] = v
	}
	return out
}()

var dateSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// NormalizeModel 把模型名稱整理成查價格用的形式：小寫、去掉轉售商前綴（anthropic/…）、
// 「.」換成「-」、去掉日期版本（-20250514）。例：anthropic/claude-sonnet-4.6 → claude-sonnet-4-6。
func NormalizeModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m = strings.ReplaceAll(m, ".", "-")
	return dateSuffix.ReplaceAllString(m, "")
}

// DefaultPrices 回傳內建價格表（key 是原本的寫法）。
func DefaultPrices() map[string]Price {
	out := make(map[string]Price, len(defaultPrices)+len(providerDefaultPrices))
	for k, v := range defaultPrices {
		out[k] = v
	}
	for k, v := range providerDefaultPrices {
		out[k] = v
	}
	return out
}

// Pricing 查模型的價格：先看使用者設定（<供應商>/<模型>、<模型>、整理過的模型名），再看內建表。
type Pricing struct {
	DB *store.DB
}

// Custom 讀使用者設定的價格。
func (p *Pricing) Custom(ctx context.Context) (map[string]Price, error) {
	out := map[string]Price{}
	if p == nil || p.DB == nil {
		return out, nil
	}
	_, err := p.DB.GetSettingInto(ctx, PricingSetting, &out)
	return out, err
}

// Lookup 回傳模型的價格與來源；ok 為 false 表示不知道價格。
func (p *Pricing) Lookup(ctx context.Context, provider, model string) (Price, string, bool, error) {
	custom, err := p.Custom(ctx)
	if err != nil {
		return Price{}, "", false, err
	}
	pr, src, ok := LookupPrice(custom, provider, model)
	return pr, src, ok, nil
}

// LookupPrice 在 custom 與內建表裡找價格。
func LookupPrice(custom map[string]Price, provider, model string) (Price, string, bool) {
	norm := NormalizeModel(model)
	for _, k := range []string{provider + "/" + model, model, norm} {
		if pr, ok := custom[k]; ok {
			return pr, PriceSourceCustom, true
		}
	}
	for k, pr := range custom {
		if NormalizeModel(k) == norm && !strings.Contains(k, "/") {
			return pr, PriceSourceCustom, true
		}
	}
	if pr, ok := providerDefaultPrices[provider+"/"+model]; ok {
		return pr, PriceSourceDefault, true
	}
	if pr, ok := normalizedDefaults[norm]; ok {
		return pr, PriceSourceDefault, true
	}
	return Price{}, "", false
}

// CostMicros 依價格算出花費（百萬分之一美元）。input 已包含快取命中與寫入快取的量（照 normalize）。
func (pr Price) CostMicros(input, cacheRead, cacheWrite, output int64) int64 {
	plain := max(input-cacheRead-cacheWrite, 0)
	cr, cw := pr.CacheRead, pr.CacheWrite
	if cr == 0 {
		cr = pr.Input
	}
	if cw == 0 {
		cw = pr.Input
	}
	// 美元＝token × 每百萬價格 ÷ 1e6；百萬分之一美元＝token × 每百萬價格。
	return int64(math.Round(float64(plain)*pr.Input + float64(cacheRead)*cr + float64(cacheWrite)*cw + float64(output)*pr.Output))
}

// ValidPrice 檢查價格不是負數、不是 NaN。
func ValidPrice(pr Price) bool {
	for _, v := range []float64{pr.Input, pr.Output, pr.CacheRead, pr.CacheWrite} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || v > 1e6 {
			return false
		}
	}
	return true
}
