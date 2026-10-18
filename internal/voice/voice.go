// Package voice 是語音（E32，PRD §6.10 VC-1～VC-3）：朗讀與自動朗讀、每個 Bot 的聲音、Bot 的語音訊息、
// 語音輸入（錄音→文字），以及通話用的同一組操作。
//
// 合成與辨識都經 Model Proxy（OpenAI 相容的 audio/speech、audio/transcriptions），金鑰不離開服務端；
// 用量記在 usage_entry（engine=voice，算在那個 Bot 頭上）。沒有設定語音引擎時回 engine=browser，
// 由瀏覽器內建的聲音念（K02：沒有金鑰時預設用系統內建聲音）。
//
// 設定存在 setting 表：voice.settings（全域的引擎）、voice.bot.<id>（每個 Bot 的聲音）、voice.thread.<id>（對話串的自動朗讀）。
// 合成好的音訊以內容的雜湊快取在 <資料夾>/voice-cache，GET /api/v1/voice.audio?id=… 串流給瀏覽器。
package voice

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// UsageEngine 是語音在用量帳本上的標記（usage_entry.engine）。
const UsageEngine = "voice"

// 設定在 setting 表的 key。
const (
	SettingsKey     = "voice.settings"
	botKeyPrefix    = "voice.bot."
	threadKeyPrefix = "voice.thread."
)

// 預設的模型（OpenAI）。
const (
	DefaultTTSModel = "gpt-4o-mini-tts"
	DefaultSTTModel = "gpt-4o-mini-transcribe"
)

// 引擎種類（SpeakOut.Engine、BotView.Engine）。
const (
	EngineProxy   = "proxy"   // 經 Model Proxy 合成好的音訊
	EngineBrowser = "browser" // 沒有語音引擎，或選了系統內建聲音：瀏覽器自己念
)

// Lang 是瀏覽器念、辨識時用的語言。
const Lang = "zh-TW"

// Engine 是一個語音引擎：哪個模型供應商的哪個模型。Provider 空白表示用瀏覽器內建的。
type Engine struct {
	Provider string `json:"provider" max:"32" desc:"模型供應商代號（xbot.models.get）；空白表示用瀏覽器內建的"`
	Model    string `json:"model" max:"128" desc:"例：gpt-4o-mini-tts、gpt-4o-mini-transcribe、whisper-1"`
}

// Settings 是全域的語音設定。
type Settings struct {
	TTS Engine `json:"tts" desc:"朗讀（合成）用的引擎"`
	STT Engine `json:"stt" desc:"語音輸入（辨識）用的引擎"`
	// HangupMinutes 是通話中多久沒人說話自動掛斷（K01）。
	HangupMinutes int `json:"hangup_minutes"`
}

// DefaultSettings 是還沒設定時的值：都用瀏覽器內建的。
func DefaultSettings() Settings {
	return Settings{HangupMinutes: limits.VoiceHangupMinutesDefault}
}

// BotVoice 是一個 Bot 的聲音。
type BotVoice struct {
	Voice    string  `json:"voice" desc:"聲音代號（voices 清單）"`
	Speed    float64 `json:"speed" desc:"語速，1 是正常"`
	AutoRead bool    `json:"auto_read" desc:"Bot 回完自動念出來（變成語音訊息）"`
}

// DefaultBotVoice 是 Bot 還沒挑聲音時用的（K02 預設「晴」）。
func DefaultBotVoice() BotVoice { return BotVoice{Voice: "qing", Speed: 1} }

// Option 是一個可以挑的聲音（K02 的 A～F）。
type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Upstream 是送給供應商的 voice（OpenAI 的聲音名稱）；空白表示用瀏覽器內建的聲音。
	Upstream string `json:"upstream,omitempty"`
}

// Voices 是可以挑的聲音（design/K02-VoiceSetup）。名字是給人看的；送給供應商的是 OpenAI 的聲音。
var Voices = []Option{
	{ID: "qing", Name: "晴", Desc: "溫和的女聲 · 推薦給幕僚長", Upstream: "coral"},
	{ID: "an", Name: "安", Desc: "沉穩的男聲", Upstream: "onyx"},
	{ID: "kui", Name: "小葵", Desc: "活潑的女聲", Upstream: "nova"},
	{ID: "jie", Name: "阿杰", Desc: "爽朗的男聲", Upstream: "ash"},
	{ID: "ya", Name: "雅", Desc: "像新聞主播，咬字清楚", Upstream: "sage"},
	{ID: "system", Name: "系統內建", Desc: "免費，聽起來比較機械"},
}

// VoiceByID 找聲音。
func VoiceByID(id string) (Option, bool) {
	for _, v := range Voices {
		if v.ID == id {
			return v, true
		}
	}
	return Option{}, false
}

// Provider 是一個設定好的模型供應商（給「語音引擎」下拉選單用）。
type Provider struct {
	ID    string
	Label string
	// OAuth 是訂閱登入（只開放 responses，沒有語音）。
	OAuth bool
}

// MediaRequest／MediaResponse 是經 Model Proxy 呼叫語音 API 的請求與回應（proxy.Handler.CallMedia）。
type MediaRequest struct {
	Provider    string
	Path        string // audio/speech、audio/transcriptions
	ContentType string
	Body        []byte
	BotID       string
	MaxBytes    int64
}

// MediaResponse 是供應商的回應。
type MediaResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// 呼叫 Media 時的錯誤（由服務組裝時從 proxy 的錯誤轉過來）。
var (
	// ErrNoEngine：引擎指定的供應商不存在，或不能用語音（訂閱登入）。
	ErrNoEngine = errors.New("語音引擎的模型供應商不能用")
)

// BudgetError 表示超過花費上限（訊息給人看）。
type BudgetError struct{ Message string }

func (e *BudgetError) Error() string { return e.Message }

// Service 是語音服務。
type Service struct {
	DB *store.DB
	// Bus 推 message.created（語音訊息標記好了）；可為 nil。
	Bus *events.Bus
	// CacheDir 是合成語音的快取資料夾。
	CacheDir string
	// Media 經 Model Proxy 呼叫語音 API。
	Media func(ctx context.Context, req MediaRequest) (MediaResponse, error)
	// Providers 列出設定好的模型供應商。
	Providers func(ctx context.Context) ([]Provider, error)
	Logger    *slog.Logger

	mu       sync.Mutex
	inflight map[string]*flight
}

type flight struct {
	done chan struct{}
	meta audioMeta
	err  error
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
