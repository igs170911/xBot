package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"xbot/internal/limits"
)

// Recording 是一段錄音（語音輸入、通話的一句話）。
type Recording struct {
	Data     []byte
	MimeType string
}

type recordingKey struct{}

// WithRecording 把 HTTP 上傳的錄音放進 context，交給 xbot.voice.transcribe（內容不經過 JSON、不寫進紀錄）。
func WithRecording(ctx context.Context, r Recording) context.Context {
	return context.WithValue(ctx, recordingKey{}, r)
}

func recordingFrom(ctx context.Context) (Recording, bool) {
	r, ok := ctx.Value(recordingKey{}).(Recording)
	return r, ok
}

// extOf 依錄音格式給檔名副檔名（供應商靠副檔名判斷格式）。
func extOf(mimeType string) string {
	mt, _, _ := mime.ParseMediaType(mimeType)
	switch mt {
	case "audio/ogg":
		return "ogg"
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "video/mp4":
		return "m4a"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/flac":
		return "flac"
	}
	return "webm"
}

// transcribe 把錄音送到辨識引擎，回傳文字。
func (s *Service) transcribe(ctx context.Context, e Engine, botID string, rec Recording, language string) (string, error) {
	if s.Media == nil {
		return "", ErrNoEngine
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", e.Model)
	_ = mw.WriteField("response_format", "json")
	if language != "" {
		_ = mw.WriteField("language", language)
	}
	ct := rec.MimeType
	if mt, _, err := mime.ParseMediaType(ct); err != nil || !strings.HasPrefix(mt, "audio/") && mt != "video/webm" && mt != "video/mp4" {
		ct = "audio/webm"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="speech.`+extOf(ct)+`"`)
	h.Set("Content-Type", ct)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(rec.Data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.VoiceRequestTimeout)
	defer cancel()
	resp, err := s.Media(ctx, MediaRequest{Provider: e.Provider, Path: "audio/transcriptions", ContentType: mw.FormDataContentType(),
		Body: buf.Bytes(), BotID: botID, MaxBytes: 1 << 20})
	if err != nil {
		return "", err
	}
	if resp.Status >= 400 {
		return "", &UpstreamError{Status: resp.Status, Message: upstreamMessage(resp.Body)}
	}
	var out struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(resp.Body, &out) != nil {
		// response_format 不支援 json 的供應商：整段當文字
		out.Text = string(resp.Body)
	}
	text := strings.TrimSpace(out.Text)
	if rs := []rune(text); len(rs) > limits.VoiceTranscriptChars {
		text = string(rs[:limits.VoiceTranscriptChars])
	}
	return text, nil
}
