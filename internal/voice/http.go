package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// HTTP 出口（api.Options.Streams）：音訊內容不經過 JSON。身分由 api 驗證後傳進來；
// 權限、稽核都經操作登錄表（xbot.voice.audio、xbot.voice.transcribe），和其他出口一樣。

// AudioHandler：GET /api/v1/voice.audio?id=…，串流一段合成好的音訊（支援 Range，瀏覽器可以拖曳進度）。
func (s *Service) AudioHandler(reg *ops.Registry) func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
	return func(w http.ResponseWriter, r *http.Request, ident ops.Identity) error {
		raw, _ := json.Marshal(AudioIn{ID: r.URL.Query().Get("id")})
		res, err := reg.Invoke(r.Context(), ident, "xbot.voice.audio", raw, ops.Options{})
		if err != nil {
			return err
		}
		var meta AudioOut
		if err := json.Unmarshal(res.Output, &meta); err != nil {
			return ops.Internal(err)
		}
		_, path, err := s.Audio(meta.ID)
		if err != nil {
			return ops.NotFound("找不到這段聲音")
		}
		f, err := os.Open(path)
		if err != nil {
			return ops.NotFound("找不到這段聲音")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return ops.Internal(err)
		}
		h := w.Header()
		h.Set("Content-Type", meta.ContentType)
		h.Set("X-Content-Type-Options", "nosniff")
		// 內容由雜湊決定、不會變：瀏覽器可以放心快取。
		h.Set("Cache-Control", "private, max-age=86400, immutable")
		http.ServeContent(w, r, meta.ID+".mp3", info.ModTime(), f)
		return nil
	}
}

// TranscribeHandler：POST /api/v1/voice.transcribe，multipart/form-data（欄位 file＝錄音；bot_id、duration_ms、language 可放表單或網址）。
// 錄音先讀進記憶體（上限 limits.VoiceUploadBytes），再經 xbot.voice.transcribe 辨識。回 TranscribeOut。
func (s *Service) TranscribeHandler(reg *ops.Registry) func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
	return func(w http.ResponseWriter, r *http.Request, ident ops.Identity) error {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
			return ops.InvalidInput(ops.FieldError{Message: "錄音要用 multipart/form-data 上傳"})
		}
		q := r.URL.Query()
		in := TranscribeIn{BotID: q.Get("bot_id"), Language: q.Get("language")}
		in.DurationMS, _ = strconv.Atoi(q.Get("duration_ms"))
		body := http.MaxBytesReader(w, r.Body, limits.VoiceUploadBytes+(64<<10))
		mr := multipart.NewReader(body, params["boundary"])
		var rec Recording
		got := false
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return uploadErr(err)
			}
			if part.FileName() == "" && part.FormName() != "file" {
				v, _ := io.ReadAll(io.LimitReader(part, 256))
				val := strings.TrimSpace(string(v))
				switch part.FormName() {
				case "bot_id":
					in.BotID = val
				case "duration_ms":
					in.DurationMS, _ = strconv.Atoi(val)
				case "language":
					in.Language = val
				}
				continue
			}
			if got {
				return ops.InvalidInput(ops.FieldError{Field: "file", Message: "一次只能上傳一段錄音"})
			}
			data, err := io.ReadAll(io.LimitReader(part, limits.VoiceUploadBytes+1))
			if err != nil {
				return uploadErr(err)
			}
			if int64(len(data)) > limits.VoiceUploadBytes {
				return ops.Limit(fmt.Sprintf("一段錄音最多 %d MB", limits.VoiceUploadBytes>>20))
			}
			rec = Recording{Data: data, MimeType: part.Header.Get("Content-Type")}
			got = true
		}
		if !got {
			return ops.InvalidInput(ops.FieldError{Field: "file", Message: "沒有收到錄音"})
		}
		in.MimeType = rec.MimeType
		raw, _ := json.Marshal(in)
		ctx := WithRecording(r.Context(), rec)
		res, err := reg.Invoke(ctx, ident, "xbot.voice.transcribe", raw, ops.Options{RequestID: store.NewID()})
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(res.Output) // 已經開始送內容了，對方斷線也寫不出錯誤
		return nil
	}
}

func uploadErr(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return ops.Limit(fmt.Sprintf("一段錄音最多 %d MB", limits.VoiceUploadBytes>>20))
	}
	return ops.InvalidInput(ops.FieldError{Message: "錄音上傳不完整，請再試一次"})
}
