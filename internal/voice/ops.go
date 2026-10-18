package voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── 輸入輸出 ──────────────────────────────────────────

// SettingsIn 是 xbot.voice.settings_get 的輸入（沒有欄位）。
type SettingsIn struct{}

// Limits 是語音的固定限制（前端錄音、朗讀用）。
type Limits struct {
	MaxSeconds  int     `json:"max_seconds" desc:"一段錄音最長幾秒"`
	UploadBytes int64   `json:"upload_bytes" desc:"一段錄音最大幾個位元組"`
	SpeakChars  int     `json:"speak_chars" desc:"一次最多念幾個字"`
	SpeedMin    float64 `json:"speed_min"`
	SpeedMax    float64 `json:"speed_max"`
}

// SettingsView 是 xbot.voice.settings_get／settings_set 的輸出。
type SettingsView struct {
	Settings Settings       `json:"settings"`
	Voices   []Option       `json:"voices" desc:"可以挑的聲音"`
	Engines  []EngineOption `json:"engines" desc:"可以選的語音引擎（第一個是瀏覽器內建）"`
	// TTSReady／STTReady：合成、辨識是不是經 XBot 的引擎（false 時用瀏覽器內建的）。
	TTSReady bool   `json:"tts_ready" desc:"朗讀用 XBot 的語音引擎（false：用瀏覽器內建的聲音）"`
	STTReady bool   `json:"stt_ready" desc:"語音輸入用 XBot 的語音引擎（false：用瀏覽器內建的辨識，有的瀏覽器沒有）"`
	Limits   Limits `json:"limits"`
	Lang     string `json:"lang"`
}

// SettingsSetIn 是 xbot.voice.settings_set 的輸入；沒給的欄位不變。
type SettingsSetIn struct {
	TTS           *Engine `json:"tts,omitempty" desc:"朗讀用的引擎；provider 空白＝瀏覽器內建"`
	STT           *Engine `json:"stt,omitempty" desc:"語音輸入用的引擎；provider 空白＝瀏覽器內建"`
	HangupMinutes *int    `json:"hangup_minutes,omitempty" min:"1" max:"60" desc:"通話中多久沒人說話自動掛斷"`
}

// BotGetIn 是 xbot.voice.bot_get 的輸入。
type BotGetIn struct {
	BotID    string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	ThreadID string `json:"thread_id,omitempty" max:"64" desc:"一起讀這條對話串的自動朗讀設定"`
}

// BotView 是一個 Bot 的聲音（xbot.voice.bot_get／bot_set／thread_set 的輸出）。
type BotView struct {
	BotID string `json:"bot_id"`
	BotVoice
	VoiceName string `json:"voice_name"`
	Engine    string `json:"engine" desc:"proxy：XBot 的語音引擎；browser：瀏覽器內建的聲音"`
	// ThreadID／ThreadAutoRead：有給 thread_id 時，那條對話串自己的設定（null＝跟著 Bot）。
	ThreadID       string `json:"thread_id,omitempty"`
	ThreadAutoRead *bool  `json:"thread_auto_read,omitempty" desc:"這條對話串自己的自動朗讀；沒有時跟著 Bot"`
	// AutoReadNow 是實際上要不要自動朗讀（對話串的設定優先）。
	AutoReadNow bool `json:"auto_read_now"`
}

// BotSetIn 是 xbot.voice.bot_set 的輸入；沒給的欄位不變。
type BotSetIn struct {
	BotID    string   `json:"bot_id" required:"true" resource:"bot" max:"64"`
	Voice    *string  `json:"voice,omitempty" enum:"qing,an,kui,jie,ya,system" desc:"聲音代號"`
	Speed    *float64 `json:"speed,omitempty" min:"0.5" max:"2" desc:"語速，1 是正常"`
	AutoRead *bool    `json:"auto_read,omitempty" desc:"Bot 回完自動念出來（變成語音訊息）"`
}

// ThreadSetIn 是 xbot.voice.thread_set 的輸入。
type ThreadSetIn struct {
	ThreadID string `json:"thread_id" required:"true" max:"64"`
	AutoRead string `json:"auto_read" required:"true" enum:"on,off,inherit" desc:"on／off：這條對話串自己決定；inherit：跟著 Bot 的設定"`
}

// SpeakIn 是 xbot.voice.speak 的輸入。
type SpeakIn struct {
	BotID     string   `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"用哪個 Bot 的聲音"`
	Text      string   `json:"text,omitempty" max:"64000" desc:"要念的文字（Markdown 會整理成念得出來的句子）"`
	MessageID string   `json:"message_id,omitempty" max:"64" desc:"或：念這則 Bot 的回覆"`
	VoiceNote bool     `json:"voice_note,omitempty" desc:"和 message_id 一起用：把這則回覆標成語音訊息（對話裡顯示播放器）"`
	Voice     string   `json:"voice,omitempty" enum:"qing,an,kui,jie,ya,system" desc:"試聽別的聲音；不填用 Bot 的設定"`
	Speed     *float64 `json:"speed,omitempty" min:"0.5" max:"2"`
}

// SpeakOut 是 xbot.voice.speak 的輸出。
type SpeakOut struct {
	Engine      string  `json:"engine" desc:"proxy：用 url 播放 XBot 合成好的音訊；browser：沒有語音引擎，請用瀏覽器內建的聲音念 text"`
	AudioID     string  `json:"audio_id,omitempty"`
	URL         string  `json:"url,omitempty" desc:"GET 這個網址拿音訊"`
	ContentType string  `json:"content_type,omitempty"`
	Bytes       int64   `json:"bytes,omitempty"`
	Cached      bool    `json:"cached,omitempty" desc:"念過一樣的內容，直接用快取（不另外花錢）"`
	Text        string  `json:"text" desc:"實際要念的文字"`
	Truncated   bool    `json:"truncated,omitempty" desc:"太長，只念前面一段"`
	Voice       string  `json:"voice"`
	VoiceName   string  `json:"voice_name"`
	Speed       float64 `json:"speed"`
	Lang        string  `json:"lang"`
	MessageID   string  `json:"message_id,omitempty"`
}

// AudioIn 是 xbot.voice.audio 的輸入。
type AudioIn struct {
	ID string `json:"id" required:"true" max:"32" desc:"xbot.voice.speak 回的 audio_id"`
}

// AudioOut 是 xbot.voice.audio 的輸出（GET /api/v1/voice.audio?id=… 串流內容）。
type AudioOut struct {
	ID          string `json:"id"`
	BotID       string `json:"bot_id"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
}

// TranscribeIn 是 xbot.voice.transcribe 的輸入。網頁用 POST /api/v1/voice.transcribe（multipart）上傳錄音；
// MCP 等其他出口用 audio_base64。
type TranscribeIn struct {
	BotID       string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"替哪個 Bot 聽（花費算在它頭上）"`
	AudioBase64 string `json:"audio_base64,omitempty" max:"14000000" secret:"true" desc:"錄音內容（base64）；不寫進紀錄"`
	MimeType    string `json:"mime_type,omitempty" max:"100" desc:"例：audio/webm"`
	DurationMS  int    `json:"duration_ms,omitempty" min:"0" desc:"錄音長度（毫秒）"`
	Language    string `json:"language,omitempty" max:"16" desc:"例：zh；不填自動判斷"`
}

// TranscribeOut 是 xbot.voice.transcribe 的輸出。
type TranscribeOut struct {
	Text     string `json:"text" desc:"聽到的文字（沒聽到話時是空白）"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Bytes    int    `json:"bytes"`
}

// CallEndIn 是 xbot.voice.call_end 的輸入。
type CallEndIn struct {
	BotID     string `json:"bot_id" required:"true" resource:"bot" max:"64"`
	ThreadID  string `json:"thread_id" required:"true" max:"64"`
	DurationS int    `json:"duration_s" min:"0" max:"86400" desc:"通話多久（秒）"`
	Turns     int    `json:"turns,omitempty" min:"0" max:"10000" desc:"你說了幾句"`
	Reason    string `json:"reason,omitempty" enum:"hangup,idle,error" desc:"hangup 你掛斷、idle 太久沒人說話、error 斷線"`
}

// CallEndOut 是 xbot.voice.call_end 的輸出。
type CallEndOut struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// ── 登錄 ─────────────────────────────────────────────

// Register 登錄語音操作。s 是 nil 時只登錄定義（指令列、產生前端型別）。
func Register(reg *ops.Registry, s *Service) {
	ready := func() error {
		if s == nil {
			return ops.Internal(errors.New("語音服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[SettingsIn, SettingsView]{
		Name: "xbot.voice.settings_get", Summary: "看語音設定（朗讀與語音輸入的引擎、可以挑的聲音）",
		Description: "引擎是設定好的模型供應商（OpenAI 相容的 audio API），金鑰只在 XBot 的 Model Proxy；沒有設定時用瀏覽器內建的聲音與辨識。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ SettingsIn) (SettingsView, error) {
			if err := ready(); err != nil {
				return SettingsView{}, err
			}
			set, err := LoadSettings(ctx, call.DB)
			if err != nil {
				return SettingsView{}, err
			}
			return s.view(ctx, set)
		},
	})
	ops.Register(reg, ops.Op[SettingsSetIn, SettingsView]{
		Name: "xbot.voice.settings_set", Summary: "改語音設定（朗讀、語音輸入用哪個引擎、通話多久沒說話就掛斷）",
		Scope: ops.ScopeAdmin, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SettingsSetIn) (SettingsView, error) {
			if err := ready(); err != nil {
				return SettingsView{}, err
			}
			set, err := LoadSettings(ctx, call.DB)
			if err != nil {
				return SettingsView{}, err
			}
			_, usable, err := s.engines(ctx)
			if err != nil {
				return SettingsView{}, err
			}
			for _, e := range []struct {
				in    *Engine
				dst   *Engine
				field string
				model string
			}{{in.TTS, &set.TTS, "tts", DefaultTTSModel}, {in.STT, &set.STT, "stt", DefaultSTTModel}} {
				if e.in == nil {
					continue
				}
				eng := Engine{Provider: strings.TrimSpace(e.in.Provider), Model: strings.TrimSpace(e.in.Model)}
				if eng.Provider != "" {
					ok, found := usable[eng.Provider]
					if !found {
						return SettingsView{}, ops.InvalidInput(ops.FieldError{Field: e.field + ".provider", Message: "還沒設定這個模型供應商：" + eng.Provider})
					}
					if !ok {
						return SettingsView{}, ops.InvalidInput(ops.FieldError{Field: e.field + ".provider", Message: "訂閱登入的供應商不能用來說話或聽，請選用 API 金鑰的供應商"})
					}
					if eng.Model == "" {
						eng.Model = e.model
					}
				} else {
					eng.Model = ""
				}
				*e.dst = eng
			}
			if in.HangupMinutes != nil {
				set.HangupMinutes = *in.HangupMinutes
			}
			if !call.DryRun {
				if err := call.DB.SetSetting(ctx, SettingsKey, set); err != nil {
					return SettingsView{}, err
				}
			}
			return s.view(ctx, set)
		},
	})
	ops.Register(reg, ops.Op[BotGetIn, BotView]{
		Name: "xbot.voice.bot_get", Summary: "看一個 Bot 的聲音（聲音、語速、自動朗讀）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotGetIn) (BotView, error) {
			if err := ready(); err != nil {
				return BotView{}, err
			}
			if err := botExists(ctx, call.DB, in.BotID); err != nil {
				return BotView{}, err
			}
			v, err := LoadBot(ctx, call.DB, in.BotID)
			if err != nil {
				return BotView{}, err
			}
			var thr *bool
			if in.ThreadID != "" {
				th, err := threadOf(ctx, call, in.ThreadID)
				if err != nil {
					return BotView{}, err
				}
				if th.BotID != in.BotID {
					return BotView{}, ops.InvalidInput(ops.FieldError{Field: "thread_id", Message: "這條對話串不是這個 Bot 的"})
				}
				if thr, err = LoadThread(ctx, call.DB, in.ThreadID); err != nil {
					return BotView{}, err
				}
			}
			return s.botView(ctx, call.DB, in.BotID, v, in.ThreadID, thr)
		},
	})
	ops.Register(reg, ops.Op[BotSetIn, BotView]{
		Name: "xbot.voice.bot_set", Summary: "幫 Bot 挑聲音、改語速、開關自動朗讀",
		Description: "選了就生效（K02：自動儲存）。自動朗讀開著時，Bot 回完會念出來，對話裡變成語音訊息。",
		Scope:       ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotSetIn) (BotView, error) {
			if err := ready(); err != nil {
				return BotView{}, err
			}
			if err := botExists(ctx, call.DB, in.BotID); err != nil {
				return BotView{}, err
			}
			v, err := LoadBot(ctx, call.DB, in.BotID)
			if err != nil {
				return BotView{}, err
			}
			if in.Voice != nil {
				if _, ok := VoiceByID(*in.Voice); !ok {
					return BotView{}, ops.InvalidInput(ops.FieldError{Field: "voice", Message: "沒有這個聲音"})
				}
				v.Voice = *in.Voice
			}
			if in.Speed != nil {
				v.Speed = clampSpeed(*in.Speed)
			}
			if in.AutoRead != nil {
				v.AutoRead = *in.AutoRead
			}
			if !call.DryRun {
				if err := call.DB.SetSetting(ctx, botKeyPrefix+in.BotID, v); err != nil {
					return BotView{}, err
				}
			}
			return s.botView(ctx, call.DB, in.BotID, v, "", nil)
		},
	})
	ops.Register(reg, ops.Op[ThreadSetIn, BotView]{
		Name: "xbot.voice.thread_set", Summary: "這條對話串要不要自動朗讀 Bot 的回覆",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in ThreadSetIn) (BotView, error) {
			if err := ready(); err != nil {
				return BotView{}, err
			}
			th, err := threadOf(ctx, call, in.ThreadID)
			if err != nil {
				return BotView{}, err
			}
			var auto *bool
			if in.AutoRead != "inherit" {
				b := in.AutoRead == "on"
				auto = &b
			}
			if !call.DryRun {
				if err := saveThread(ctx, call.DB, th.ID, auto); err != nil {
					return BotView{}, err
				}
			}
			v, err := LoadBot(ctx, call.DB, th.BotID)
			if err != nil {
				return BotView{}, err
			}
			return s.botView(ctx, call.DB, th.BotID, v, th.ID, auto)
		},
	})
	ops.Register(reg, ops.Op[SpeakIn, SpeakOut]{
		Name: "xbot.voice.speak", Summary: "用 Bot 的聲音念一段文字（朗讀、語音訊息、通話）",
		Description: "經 XBot 的語音引擎合成，回傳音訊網址（GET /api/v1/voice.audio?id=…）；一樣的內容直接用快取。" +
			"沒有語音引擎、或 Bot 選了系統內建聲音時回 engine=browser，請用瀏覽器內建的聲音念 text。" +
			"給 message_id 念 Bot 的那則回覆；再加 voice_note=true 把它標成語音訊息。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in SpeakIn) (SpeakOut, error) {
			if err := ready(); err != nil {
				return SpeakOut{}, err
			}
			return s.speak(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[AudioIn, AudioOut]{
		Name: "xbot.voice.audio", Summary: "查一段合成好的音訊（內容用 GET /api/v1/voice.audio?id=… 拿）",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in AudioIn) (AudioOut, error) {
			if err := ready(); err != nil {
				return AudioOut{}, err
			}
			m, _, err := s.Audio(in.ID)
			if err != nil {
				return AudioOut{}, ops.NotFound("找不到這段聲音（可能已經從快取清掉了，請再念一次）")
			}
			if m.BotID != "" && !call.Identity.CanAccess(ops.ResourceBot, m.BotID) {
				return AudioOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這個 Bot")
			}
			return AudioOut{ID: in.ID, BotID: m.BotID, ContentType: m.ContentType, Bytes: m.Bytes}, nil
		},
	})
	ops.Register(reg, ops.Op[TranscribeIn, TranscribeOut]{
		Name: "xbot.voice.transcribe", Summary: "把一段錄音轉成文字（語音輸入、通話）",
		Description: fmt.Sprintf("經 XBot 的語音引擎辨識；網頁用 POST /api/v1/voice.transcribe 以 multipart 上傳（欄位 file），"+
			"其他出口用 audio_base64。一段最長 %d 秒、%d MB。沒有設定語音輸入引擎時回 conflict（請改用瀏覽器內建的辨識）。",
			limits.VoiceMaxSeconds, limits.VoiceUploadBytes>>20),
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in TranscribeIn) (TranscribeOut, error) {
			if err := ready(); err != nil {
				return TranscribeOut{}, err
			}
			return s.transcribeOp(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[CallEndIn, CallEndOut]{
		Name: "xbot.voice.call_end", Summary: "通話結束：在對話裡留一列通話紀錄",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in CallEndIn) (CallEndOut, error) {
			if err := ready(); err != nil {
				return CallEndOut{}, err
			}
			th, err := threadOf(ctx, call, in.ThreadID)
			if err != nil {
				return CallEndOut{}, err
			}
			if th.BotID != in.BotID {
				return CallEndOut{}, ops.InvalidInput(ops.FieldError{Field: "thread_id", Message: "這條對話串不是這個 Bot 的"})
			}
			text := CallLine(in.DurationS, in.Turns, in.Reason)
			if call.DryRun {
				return CallEndOut{Text: text}, nil
			}
			meta, _ := json.Marshal(map[string]any{"activity": "call", "call": map[string]any{"duration_s": in.DurationS, "turns": in.Turns, "reason": in.Reason}})
			m, err := call.DB.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "system", Kind: "activity", Content: text, Meta: meta})
			if err != nil {
				return CallEndOut{}, err
			}
			s.publish(call.DB, in.BotID, th.ID, m)
			return CallEndOut{MessageID: m.ID, Text: text}, nil
		},
	})
}

// CallLine 是對話裡那列通話紀錄的文字，例：「通話 3 分 12 秒 · 你說了 4 句」。
func CallLine(seconds, turns int, reason string) string {
	d := fmt.Sprintf("%d 秒", seconds)
	if seconds >= 60 {
		d = fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
	}
	out := "通話 " + d
	if turns > 0 {
		out += fmt.Sprintf(" · 你說了 %d 句", turns)
	}
	switch reason {
	case "idle":
		out += " · 太久沒人說話，自動掛斷"
	case "error":
		out += " · 連線中斷"
	}
	return out
}

// ── 實作 ─────────────────────────────────────────────

func (s *Service) view(ctx context.Context, set Settings) (SettingsView, error) {
	engines, usable, err := s.engines(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{
		Settings: set, Voices: Voices, Engines: engines,
		TTSReady: set.TTS.Provider != "" && usable[set.TTS.Provider],
		STTReady: set.STT.Provider != "" && usable[set.STT.Provider],
		Limits: Limits{MaxSeconds: limits.VoiceMaxSeconds, UploadBytes: limits.VoiceUploadBytes, SpeakChars: limits.VoiceSpeakChars,
			SpeedMin: limits.VoiceSpeedMin, SpeedMax: limits.VoiceSpeedMax},
		Lang: Lang,
	}, nil
}

func (s *Service) botView(ctx context.Context, db *store.DB, botID string, v BotVoice, threadID string, thr *bool) (BotView, error) {
	set, err := LoadSettings(ctx, db)
	if err != nil {
		return BotView{}, err
	}
	_, usable, err := s.engines(ctx)
	if err != nil {
		return BotView{}, err
	}
	opt, _ := VoiceByID(v.Voice)
	out := BotView{BotID: botID, BotVoice: v, VoiceName: opt.Name, Engine: EngineBrowser, ThreadID: threadID, ThreadAutoRead: thr, AutoReadNow: v.AutoRead}
	if _, ok := effective(set, usable, opt); ok {
		out.Engine = EngineProxy
	}
	if thr != nil {
		out.AutoReadNow = *thr
	}
	return out, nil
}

func botExists(ctx context.Context, db *store.DB, id string) error {
	if _, err := db.GetBot(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ops.NotFound("找不到這個 Bot")
	} else if err != nil {
		return err
	}
	return nil
}

// threadOf 讀對話串並確認呼叫者能碰它的 Bot。
func threadOf(ctx context.Context, call *ops.Call, id string) (store.Thread, error) {
	th, err := call.DB.GetThread(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return th, ops.NotFound("找不到這條對話串")
	}
	if err != nil {
		return th, err
	}
	if !call.Identity.CanAccess(ops.ResourceBot, th.BotID) {
		return th, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這條對話串的 Bot")
	}
	return th, nil
}

func (s *Service) speak(ctx context.Context, call *ops.Call, in SpeakIn) (SpeakOut, error) {
	if err := botExists(ctx, call.DB, in.BotID); err != nil {
		return SpeakOut{}, err
	}
	text := in.Text
	var msg store.Message
	if in.MessageID != "" {
		var err error
		if msg, err = s.botMessage(ctx, call, in.BotID, in.MessageID); err != nil {
			return SpeakOut{}, err
		}
		text = msg.Content
	} else if in.VoiceNote {
		return SpeakOut{}, ops.InvalidInput(ops.FieldError{Field: "voice_note", Message: "要和 message_id 一起用"})
	}
	if strings.TrimSpace(text) == "" {
		return SpeakOut{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: "text、message_id 要擇一"})
	}
	spoken, truncated := Clip(Speakable(text), limits.VoiceSpeakChars)
	if spoken == "" {
		return SpeakOut{}, ops.InvalidInput(ops.FieldError{Field: "text", Message: "沒有念得出來的字"})
	}
	bv, err := LoadBot(ctx, call.DB, in.BotID)
	if err != nil {
		return SpeakOut{}, err
	}
	if in.Voice != "" {
		bv.Voice = in.Voice
	}
	if in.Speed != nil {
		bv.Speed = clampSpeed(*in.Speed)
	}
	opt, ok := VoiceByID(bv.Voice)
	if !ok {
		return SpeakOut{}, ops.InvalidInput(ops.FieldError{Field: "voice", Message: "沒有這個聲音"})
	}
	set, err := LoadSettings(ctx, call.DB)
	if err != nil {
		return SpeakOut{}, err
	}
	_, usable, err := s.engines(ctx)
	if err != nil {
		return SpeakOut{}, err
	}
	out := SpeakOut{Engine: EngineBrowser, Text: spoken, Truncated: truncated, Voice: opt.ID, VoiceName: opt.Name, Speed: bv.Speed, Lang: Lang, MessageID: in.MessageID}
	if eng, ok := effective(set, usable, opt); ok && !call.DryRun {
		id, m, cached, err := s.synth(ctx, synthRequest{BotID: in.BotID, Engine: eng, Voice: opt.Upstream, Speed: bv.Speed, Text: spoken})
		if err != nil {
			return SpeakOut{}, mediaErr(err)
		}
		out.Engine, out.AudioID, out.URL, out.ContentType, out.Bytes, out.Cached = EngineProxy, id, AudioURL(id), m.ContentType, m.Bytes, cached
	} else if ok {
		out.Engine = EngineProxy // 預演：會經語音引擎合成
	}
	if in.VoiceNote && !call.DryRun {
		if err := s.markVoiceNote(ctx, call.DB, in.BotID, msg, out); err != nil {
			return SpeakOut{}, err
		}
	}
	return out, nil
}

// AudioURL 是拿一段音訊的網址。
func AudioURL(id string) string { return "/api/v1/voice.audio?id=" + id }

// botMessage 讀 Bot 在某條對話串的一則回覆，確認是這個 Bot 的、已經寫完。
func (s *Service) botMessage(ctx context.Context, call *ops.Call, botID, id string) (store.Message, error) {
	m, err := call.DB.GetMessage(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return m, ops.NotFound("找不到這則訊息")
	}
	if err != nil {
		return m, err
	}
	if m.Scope != store.ScopeThread || m.Sender != "bot:"+botID {
		return m, ops.InvalidInput(ops.FieldError{Field: "message_id", Message: "只能念這個 Bot 在對話裡的回覆"})
	}
	if _, err := threadOf(ctx, call, m.ScopeID); err != nil {
		return m, err
	}
	var meta struct {
		Streaming bool `json:"streaming"`
	}
	_ = json.Unmarshal(m.Meta, &meta)
	if meta.Streaming {
		return m, ops.Conflict("這則回覆還沒寫完，寫完再念")
	}
	return m, nil
}

// VoiceMeta 是語音訊息在 message.meta 裡的 voice 欄位。
type VoiceMeta struct {
	Engine  string  `json:"engine"`
	AudioID string  `json:"audio_id,omitempty"`
	Voice   string  `json:"voice"`
	Speed   float64 `json:"speed"`
	Chars   int     `json:"chars"`
	Bytes   int64   `json:"bytes,omitempty"`
}

// markVoiceNote 把這則回覆標成語音訊息（meta.voice），並推 message.created 讓畫面換成播放器。
func (s *Service) markVoiceNote(ctx context.Context, db *store.DB, botID string, m store.Message, out SpeakOut) error {
	meta := map[string]json.RawMessage{}
	_ = json.Unmarshal(m.Meta, &meta)
	vm, _ := json.Marshal(VoiceMeta{Engine: out.Engine, AudioID: out.AudioID, Voice: out.Voice, Speed: out.Speed, Chars: len([]rune(out.Text)), Bytes: out.Bytes})
	meta["voice"] = vm
	raw, _ := json.Marshal(meta)
	if err := db.UpdateMessage(ctx, m.ID, m.Content, m.Attachments, raw); err != nil {
		return err
	}
	fresh, err := db.GetMessage(ctx, m.ID)
	if err != nil {
		return err
	}
	s.publish(db, botID, m.ScopeID, fresh)
	return nil
}

func (s *Service) publish(db *store.DB, botID, threadID string, m store.Message) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	e := events.Event{Kind: "message.created", BotID: botID, ThreadID: threadID, Payload: raw, At: time.Now().UnixMilli()}
	if row, err := db.AppendEvent(context.Background(), store.Event{BotID: botID, ThreadID: threadID, Kind: e.Kind, Payload: raw}); err == nil {
		e = events.FromStore(row)
	} else {
		s.log().Warn("寫入事件失敗", "kind", e.Kind, "err", err)
	}
	if s.Bus != nil {
		s.Bus.Publish(e)
	}
}

func (s *Service) transcribeOp(ctx context.Context, call *ops.Call, in TranscribeIn) (TranscribeOut, error) {
	if in.BotID != "" {
		if err := botExists(ctx, call.DB, in.BotID); err != nil {
			return TranscribeOut{}, err
		}
	}
	rec, ok := recordingFrom(ctx)
	if !ok {
		if in.AudioBase64 == "" {
			return TranscribeOut{}, ops.InvalidInput(ops.FieldError{Field: "audio_base64", Message: "沒有收到錄音"})
		}
		data, err := base64.StdEncoding.DecodeString(in.AudioBase64)
		if err != nil {
			return TranscribeOut{}, ops.InvalidInput(ops.FieldError{Field: "audio_base64", Message: "不是 base64"})
		}
		rec = Recording{Data: data, MimeType: in.MimeType}
	}
	if rec.MimeType == "" {
		rec.MimeType = in.MimeType
	}
	if len(rec.Data) == 0 {
		return TranscribeOut{}, ops.InvalidInput(ops.FieldError{Field: "file", Message: "沒有錄到聲音"})
	}
	if int64(len(rec.Data)) > limits.VoiceUploadBytes {
		return TranscribeOut{}, ops.Limit(fmt.Sprintf("一段錄音最多 %d MB", limits.VoiceUploadBytes>>20))
	}
	// 前端到時間會自動停，多給 2 秒容錯。
	if in.DurationMS > (limits.VoiceMaxSeconds+2)*1000 {
		return TranscribeOut{}, ops.Limit(fmt.Sprintf("一段錄音最長 %d 秒，請分段說", limits.VoiceMaxSeconds))
	}
	set, err := LoadSettings(ctx, call.DB)
	if err != nil {
		return TranscribeOut{}, err
	}
	_, usable, err := s.engines(ctx)
	if err != nil {
		return TranscribeOut{}, err
	}
	e := set.STT
	if e.Provider == "" || !usable[e.Provider] {
		return TranscribeOut{}, ops.Conflict("還沒設定語音輸入的引擎；請到語音設定選一個，或用瀏覽器內建的辨識")
	}
	if e.Model == "" {
		e.Model = DefaultSTTModel
	}
	out := TranscribeOut{Provider: e.Provider, Model: e.Model, Bytes: len(rec.Data)}
	if call.DryRun {
		return out, nil
	}
	text, err := s.transcribe(ctx, e, in.BotID, rec, in.Language)
	if err != nil {
		return TranscribeOut{}, mediaErr(err)
	}
	out.Text = text
	return out, nil
}

// mediaErr 把呼叫語音引擎的錯誤轉成給人看的錯誤。
func mediaErr(err error) error {
	if be, ok := isBudget(err); ok {
		return ops.Budget(be.Message)
	}
	if errors.Is(err, ErrNoEngine) {
		return ops.Conflict("語音引擎的模型供應商不能用，請到語音設定換一個（或改用瀏覽器內建）")
	}
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return &ops.Error{Code: ops.CodeConflict, Message: ue.Error(), Retryable: ue.Status == http.StatusTooManyRequests || ue.Status >= 500}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ops.Error{Code: ops.CodeConflict, Message: "語音引擎太久沒有回應，請再試一次", Retryable: true}
	}
	if errors.Is(err, os.ErrPermission) {
		return ops.Internal(err)
	}
	return &ops.Error{Code: ops.CodeConflict, Message: "連不上語音引擎：" + clipRunes(err.Error(), 200), Retryable: true}
}
