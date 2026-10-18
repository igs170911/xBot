package proxy

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// 假的語音供應商：audio/speech 回 mp3 位元組；audio/transcriptions 回文字與用量。
func audioUpstream(transcript string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/audio/speech"):
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3-fake-mp3-bytes"))
		case strings.HasSuffix(r.URL.Path, "/audio/transcriptions"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.FormValue("model") == "whisper-1" {
				_, _ = w.Write([]byte(`{"text":"` + transcript + `","usage":{"type":"duration","seconds":3.2}}`))
				return
			}
			_, _ = w.Write([]byte(`{"text":"` + transcript + `","usage":{"type":"tokens","input_tokens":100,"output_tokens":10}}`))
		default:
			http.NotFound(w, r)
		}
	}
}

func withPricing(o *Options) { o.Pricing = &Pricing{} }

func multipartAudio(t *testing.T, model string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", model)
	fw, _ := mw.CreateFormFile("file", "speech.webm")
	_, _ = fw.Write([]byte("OggS-fake-audio"))
	_ = mw.Close()
	return mw.FormDataContentType(), buf.Bytes()
}

func TestContainerSpeechIsMeteredByCharacters(t *testing.T) {
	f := newFixture(t, audioUpstream("你好"), withPricing)
	resp := f.do("POST", "/model/openai/v1/audio/speech", "Bearer "+f.botKey, `{"model":"tts-1","input":"今天天氣很好","voice":"coral"}`)
	body := readAll(t, resp)
	if resp.StatusCode != 200 || body != "ID3-fake-mp3-bytes" || resp.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("回應 %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	calls := f.upstreamCalls()
	if len(calls) != 1 || calls[0].Auth != "Bearer "+realKey {
		t.Fatalf("上游沒拿到真金鑰：%+v", calls)
	}
	u := f.mem.Usage()
	if len(u) != 1 {
		t.Fatalf("用量 %+v", u)
	}
	// 6 個字 × 每百萬字 15 美元 = 90 微美元
	if u[0].Endpoint != "audio/speech" || u[0].Model != "tts-1" || u[0].CostMicros != 90 || u[0].Unpriced || u[0].InputTokens != 0 {
		t.Fatalf("合成語音的用量 %+v", u[0])
	}
}

func TestContainerTranscriptionReadsFormModelAndSeconds(t *testing.T) {
	f := newFixture(t, audioUpstream("幫我訂會議室"), withPricing)
	ct, body := multipartAudio(t, "whisper-1")
	req, _ := http.NewRequest("POST", f.srv.URL+"/model/openai/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.botKey)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, resp)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(out, "幫我訂會議室") {
		t.Fatalf("回應 %d %s", resp.StatusCode, out)
	}
	u := f.mem.Usage()
	// 3.2 秒進位成 4 秒 × 每百萬秒 100 美元 = 400 微美元
	if len(u) != 1 || u[0].Model != "whisper-1" || u[0].CostMicros != 400 || u[0].Endpoint != "audio/transcriptions" {
		t.Fatalf("辨識的用量 %+v", u)
	}
}

func TestCallMediaRecordsBotAndEngine(t *testing.T) {
	f := newFixture(t, audioUpstream("好的"), withPricing)
	h := f.handler()
	out, err := h.CallMedia(context.Background(), MediaRequest{Provider: "openai", Path: "audio/speech", ContentType: "application/json",
		Body: []byte(`{"model":"tts-1-hd","input":"好","voice":"onyx"}`), BotID: "bot-1", Engine: "voice"})
	if err != nil || out.Status != 200 || string(out.Body) != "ID3-fake-mp3-bytes" || out.ContentType != "audio/mpeg" {
		t.Fatalf("CallMedia %+v %v", out, err)
	}
	ct, body := multipartAudio(t, "gpt-4o-mini-transcribe")
	tr, err := h.CallMedia(context.Background(), MediaRequest{Provider: "openai", Path: "audio/transcriptions", ContentType: ct, Body: body, BotID: "bot-1", Engine: "voice"})
	if err != nil || !strings.Contains(string(tr.Body), "好的") {
		t.Fatalf("CallMedia 辨識 %+v %v", tr, err)
	}
	u := f.mem.Usage()
	if len(u) != 2 || u[0].BotID != "bot-1" || u[0].Engine != "voice" || u[0].CostMicros != 30 {
		t.Fatalf("用量 %+v", u)
	}
	// token 計價：100 × 1.25 + 10 × 5 = 175
	if u[1].InputTokens != 100 || u[1].OutputTokens != 10 || u[1].CostMicros != 175 || u[1].Model != "gpt-4o-mini-transcribe" {
		t.Fatalf("辨識用量 %+v", u[1])
	}
	if _, err := h.CallMedia(context.Background(), MediaRequest{Provider: "openai", Path: "chat/completions", Body: []byte(`{}`)}); !errors.Is(err, ErrCallPath) {
		t.Fatalf("不是語音的路徑應拒絕：%v", err)
	}
	if _, err := h.CallMedia(context.Background(), MediaRequest{Provider: "nope", Path: "audio/speech", Body: []byte(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("沒設定的供應商：%v", err)
	}
}

func TestFailedSpeechIsNotCharged(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad voice"}}`, 400)
	}, withPricing)
	resp := f.do("POST", "/model/openai/v1/audio/speech", "Bearer "+f.botKey, `{"model":"tts-1","input":"今天天氣很好","voice":"x"}`)
	_ = readAll(t, resp)
	u := f.mem.Usage()
	if resp.StatusCode != 400 || len(u) != 1 || u[0].CostMicros != 0 || u[0].OK {
		t.Fatalf("失敗的合成不收錢：%d %+v", resp.StatusCode, u)
	}
}
