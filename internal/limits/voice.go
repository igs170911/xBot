package limits

import "time"

// 語音（E32，PRD §6.10 VC-1～VC-3）。LazyBoy 沒有語音；以下是 XBot 新增的值，
// 供應商的上限照 OpenAI 相容的 audio API（speech 的 input 最多 4096 字、transcriptions 的檔案最多 25 MB）。
const (
	// VoiceSpeakChars 是一次朗讀最多幾個字（OpenAI audio/speech 的 input 上限 4096）；超過的截斷並說「後面請看文字」。
	VoiceSpeakChars = 4096

	// VoiceAudioBytes 是一段合成語音最多收多少（4096 字的 mp3 約 5 MB，留一倍餘裕）。
	VoiceAudioBytes int64 = 16 << 20

	// VoiceUploadBytes 是一段錄音（語音輸入、通話的一句話）的大小上限。供應商上限 25 MB；
	// 瀏覽器錄的 Opus 約 4 KB／秒，2 分鐘不到 1 MB，10 MB 是很寬的上限。
	VoiceUploadBytes int64 = 10 << 20

	// VoiceMaxSeconds 是一段錄音最長幾秒（按住說話、通話的一句話）；前端到時間自動停止，服務端也檢查。
	VoiceMaxSeconds = 120

	// VoiceTranscriptChars 是辨識結果最多保留幾個字（放進輸入框、送給 Bot 的上限之內）。
	VoiceTranscriptChars = 8000

	// VoiceCacheBytes 是合成語音快取（資料夾 voice-cache）的總大小；超過時刪最舊的。
	VoiceCacheBytes int64 = 256 << 20

	// VoiceRequestTimeout 是一次合成或辨識最多等多久。
	VoiceRequestTimeout = 90 * time.Second

	// VoiceSpeedMin、VoiceSpeedMax 是語速範圍（OpenAI 0.25～4，介面只開放常用的這段）。
	VoiceSpeedMin = 0.5
	VoiceSpeedMax = 2.0

	// VoiceHangupMinutesDefault 是通話中多久沒人說話自動掛斷（K01：5 分鐘，可在進階改）。
	VoiceHangupMinutesDefault = 5
	// VoiceHangupMinutesMax 是自動掛斷最長可以設幾分鐘。
	VoiceHangupMinutesMax = 60
)
