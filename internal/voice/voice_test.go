package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/proxy"
	"xbot/internal/store"
)

var ctx = context.Background()

const testKey = "sk-voice-REAL-key-0123456789"

// fakeUpstream 是假的 OpenAI 相容語音供應商：測試不打真的供應商。
type fakeUpstream struct {
	mu         sync.Mutex
	speech     []map[string]any
	transcribe []map[string]string // 表單欄位＋file 的檔名與格式
	transcript string
	fail       int
}

func (u *fakeUpstream) handler(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		http.Error(w, "no key", 401)
		return
	}
	if u.fail != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.fail)
		_, _ = io.WriteString(w, `{"error":{"message":"voice not available"}}`)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/audio/speech"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		u.speech = append(u.speech, body)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = io.WriteString(w, "ID3-mp3:"+body["voice"].(string))
	case strings.HasSuffix(r.URL.Path, "/audio/transcriptions"):
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		got := map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				got["filename"], got["type"], got["audio"] = p.FileName(), p.Header.Get("Content-Type"), string(b)
				continue
			}
			got[p.FormName()] = string(b)
		}
		u.transcribe = append(u.transcribe, got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"text": u.transcript, "usage": map[string]any{"type": "duration", "seconds": 2.1}})
	default:
		http.NotFound(w, r)
	}
}

type env struct {
	db    *store.DB
	reg   *ops.Registry
	svc   *Service
	up    *fakeUpstream
	srv   *httptest.Server
	ps    *proxy.DBStore
	bot   store.Bot
	bus   *events.Bus
	cache string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	up := &fakeUpstream{transcript: "幫我訂明天下午的會議室"}
	upSrv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(upSrv.Close)
	kr, err := proxy.NewKeyring(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ps := proxy.NewDBStore(db)
	providers := proxy.NewProviders(ps, kr)
	if err := providers.Put(ctx, proxy.Provider{ID: "openai", Label: "OpenAI", BaseURL: upSrv.URL + "/v1", APIKey: testKey}); err != nil {
		t.Fatal(err)
	}
	h := proxy.NewHandler(proxy.Options{Keys: proxy.NewWorkerKeys(ps, kr), Providers: providers, Usage: ps, Pricing: &proxy.Pricing{DB: db}})
	e := &env{db: db, up: up, ps: ps, bus: events.New(), cache: filepath.Join(dir, "voice-cache")}
	e.svc = &Service{DB: db, Bus: e.bus, CacheDir: e.cache,
		Media: func(ctx context.Context, req MediaRequest) (MediaResponse, error) {
			resp, err := h.CallMedia(ctx, proxy.MediaRequest{Provider: req.Provider, Path: req.Path, ContentType: req.ContentType, Body: req.Body,
				BotID: req.BotID, Engine: UsageEngine, MaxBytes: req.MaxBytes})
			if errors.Is(err, proxy.ErrNotFound) {
				return MediaResponse{}, ErrNoEngine
			}
			return MediaResponse{Status: resp.Status, ContentType: resp.ContentType, Body: resp.Body}, err
		},
		Providers: func(ctx context.Context) ([]Provider, error) {
			recs, err := ps.ListProviders(ctx)
			var out []Provider
			for _, r := range recs {
				out = append(out, Provider{ID: r.ID, Label: r.Label, OAuth: r.Auth != ""})
			}
			return out, err
		}}
	e.reg = ops.New(ops.Config{DB: db})
	Register(e.reg, e.svc)
	e.bot, _ = db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes"})
	e.srv = httptest.NewServer(api.New(api.Options{Registry: e.reg, Bus: e.bus, DB: db,
		Auth: auth.NewHTTP(auth.HTTPConfig{DB: db, AccessToken: "tok", LocalNoAuth: true}),
		Streams: map[string]api.StreamHandler{
			"GET /api/v1/voice.audio":       e.svc.AudioHandler(e.reg),
			"POST /api/v1/voice.transcribe": e.svc.TranscribeHandler(e.reg),
		}}))
	t.Cleanup(e.srv.Close)
	return e
}

var owner = ops.Owner("test")

func call[T any](t *testing.T, e *env, id ops.Identity, op string, in any) (T, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	var out T
	res, err := e.reg.Invoke(ctx, id, op, raw, ops.Options{})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func (e *env) useEngines(t *testing.T) {
	t.Helper()
	if _, err := call[SettingsView](t, e, owner, "xbot.voice.settings_set", SettingsSetIn{TTS: &Engine{Provider: "openai"}, STT: &Engine{Provider: "openai", Model: "whisper-1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsUseTheBrowser(t *testing.T) {
	e := setup(t)
	v, err := call[SettingsView](t, e, owner, "xbot.voice.settings_get", SettingsIn{})
	if err != nil || v.TTSReady || v.STTReady || len(v.Voices) != 6 || v.Settings.HangupMinutes != 5 || v.Lang != "zh-TW" {
		t.Fatalf("預設 %+v %v", v, err)
	}
	if len(v.Engines) != 2 || v.Engines[0].Provider != "" || v.Engines[1].Provider != "openai" || !v.Engines[1].Usable {
		t.Fatalf("引擎選項 %+v", v.Engines)
	}
	if v.Limits.MaxSeconds != limits.VoiceMaxSeconds || v.Limits.UploadBytes != limits.VoiceUploadBytes {
		t.Fatalf("限制 %+v", v.Limits)
	}
	bv, err := call[BotView](t, e, owner, "xbot.voice.bot_get", BotGetIn{BotID: e.bot.ID})
	if err != nil || bv.Voice != "qing" || bv.VoiceName != "晴" || bv.Speed != 1 || bv.Engine != EngineBrowser || bv.AutoReadNow {
		t.Fatalf("Bot 預設 %+v %v", bv, err)
	}
	// 沒有引擎：回 browser 與整理好的文字，不打供應商。
	out, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "## 結果\n- 價格 **NT$990**\n- 見 https://shopee.tw/x"})
	if err != nil || out.Engine != EngineBrowser || out.URL != "" || out.Text != "結果。 價格 NT$990。 見 連結。" || out.Lang != "zh-TW" {
		t.Fatalf("瀏覽器朗讀 %+v %v", out, err)
	}
	if len(e.up.speech) != 0 {
		t.Fatal("沒有引擎不該打供應商")
	}
	// 辨識沒有引擎：conflict（前端改用瀏覽器內建）。
	if _, err := call[TranscribeOut](t, e, owner, "xbot.voice.transcribe", TranscribeIn{AudioBase64: "T2dnUw==", MimeType: "audio/ogg"}); code(err) != ops.CodeConflict {
		t.Fatalf("沒有辨識引擎：%v", err)
	}
}

func TestSpeakThroughProxyIsCachedAndStreamed(t *testing.T) {
	e := setup(t)
	e.useEngines(t)
	speed := 1.25
	voice := "an"
	if _, err := call[BotView](t, e, owner, "xbot.voice.bot_set", BotSetIn{BotID: e.bot.ID, Voice: &voice, Speed: &speed}); err != nil {
		t.Fatal(err)
	}
	out, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "好的，我去查。"})
	if err != nil || out.Engine != EngineProxy || out.AudioID == "" || out.URL != "/api/v1/voice.audio?id="+out.AudioID || out.Cached || out.ContentType != "audio/mpeg" {
		t.Fatalf("合成 %+v %v", out, err)
	}
	if len(e.up.speech) != 1 {
		t.Fatalf("上游 %+v", e.up.speech)
	}
	got := e.up.speech[0]
	if got["voice"] != "onyx" || got["speed"] != 1.25 || got["model"] != DefaultTTSModel || got["input"] != "好的，我去查。" || got["response_format"] != "mp3" {
		t.Fatalf("送給供應商的 %+v", got)
	}
	// 一樣的內容：快取，不再打供應商。
	again, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "好的，我去查。"})
	if err != nil || !again.Cached || again.AudioID != out.AudioID || len(e.up.speech) != 1 {
		t.Fatalf("快取 %+v %v（上游 %d 次）", again, err, len(e.up.speech))
	}
	// 試聽別的聲音：不同的快取。
	try, _ := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "好的，我去查。", Voice: "kui"})
	if try.AudioID == out.AudioID || e.up.speech[1]["voice"] != "nova" {
		t.Fatalf("試聽 %+v", try)
	}
	// 用量記在這個 Bot 頭上，標 voice。
	rows, err := e.db.ListUsage(ctx, store.UsageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].BotID != e.bot.ID || rows[0].Engine != UsageEngine || rows[0].Endpoint != "audio/speech" {
		t.Fatalf("用量 %+v", rows)
	}
	// HTTP 串流音訊（支援 Range）。
	resp, err := http.Get(e.srv.URL + out.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ID3-mp3:onyx" || resp.Header.Get("Content-Type") != "audio/mpeg" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("音訊 %d %q %v", resp.StatusCode, body, resp.Header)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+out.URL, nil)
	req.Header.Set("Range", "bytes=0-2")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "ID3" {
		t.Fatalf("Range %d %q", resp.StatusCode, body)
	}
	for _, bad := range []string{"nope", "../../etc/passwd", strings.Repeat("a", 32)} {
		resp, err := http.Get(e.srv.URL + "/api/v1/voice.audio?id=" + bad)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 && resp.StatusCode != 400 {
			t.Fatalf("%s 應該找不到：%d", bad, resp.StatusCode)
		}
	}
	// 系統內建聲音：就算有引擎也用瀏覽器。
	sys := "system"
	_, _ = call[BotView](t, e, owner, "xbot.voice.bot_set", BotSetIn{BotID: e.bot.ID, Voice: &sys})
	if o, _ := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "嗨"}); o.Engine != EngineBrowser {
		t.Fatalf("系統內建 %+v", o)
	}
}

func TestUpstreamErrorsAreReadable(t *testing.T) {
	e := setup(t)
	e.useEngines(t)
	e.up.fail = 429
	_, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "你好"})
	var oe *ops.Error
	if !errors.As(err, &oe) || oe.Code != ops.CodeConflict || !oe.Retryable || !strings.Contains(oe.Message, "voice not available") || strings.Contains(oe.Message, testKey) {
		t.Fatalf("錯誤 %v", err)
	}
	// 出錯的不會進快取：恢復後重新合成。
	e.up.fail = 0
	if o, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "你好"}); err != nil || o.Cached {
		t.Fatalf("恢復 %+v %v", o, err)
	}
	if be := mediaErr(&BudgetError{Message: "超過上限"}); code(be) != ops.CodeBudget {
		t.Fatalf("花費上限 %v", be)
	}
}

func addThread(t *testing.T, e *env) (store.Thread, store.Message, store.Message) {
	t.Helper()
	th, err := e.db.CreateThread(ctx, store.Thread{BotID: e.bot.ID, Title: "比價"})
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := e.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "owner", Content: "幫我比價"})
	reply, _ := e.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "bot:" + e.bot.ID, Content: "三家分別是 **NT$990**、NT$1,080。",
		Meta: json.RawMessage(`{"task_id":"k1","status":"complete"}`)})
	return th, mine, reply
}

func TestVoiceNoteMarksTheReply(t *testing.T) {
	e := setup(t)
	e.useEngines(t)
	th, mine, reply := addThread(t, e)
	sub := e.bus.Subscribe(nil, 0)
	defer sub.Close()
	out, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, MessageID: reply.ID, VoiceNote: true})
	if err != nil || out.Engine != EngineProxy || out.MessageID != reply.ID || out.Text != "三家分別是 NT$990、NT$1,080。" {
		t.Fatalf("語音訊息 %+v %v", out, err)
	}
	m, _ := e.db.GetMessage(ctx, reply.ID)
	var meta struct {
		TaskID string     `json:"task_id"`
		Status string     `json:"status"`
		Voice  *VoiceMeta `json:"voice"`
	}
	_ = json.Unmarshal(m.Meta, &meta)
	if meta.TaskID != "k1" || meta.Status != "complete" || meta.Voice == nil || meta.Voice.AudioID != out.AudioID || meta.Voice.Voice != "qing" {
		t.Fatalf("meta %s", m.Meta)
	}
	select {
	case ev := <-sub.C:
		if ev.Kind != "message.created" || ev.ThreadID != th.ID || ev.ID == 0 || !strings.Contains(string(ev.Payload), `"voice"`) {
			t.Fatalf("事件 %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("沒有推 message.created")
	}
	// 你的訊息、還在寫的回覆不能念成語音訊息。
	if _, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, MessageID: mine.ID, VoiceNote: true}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("你的訊息：%v", err)
	}
	streaming, _ := e.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "bot:" + e.bot.ID, Content: "寫到一半",
		Meta: json.RawMessage(`{"streaming":true}`)})
	if _, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, MessageID: streaming.ID}); code(err) != ops.CodeConflict {
		t.Fatalf("還在寫：%v", err)
	}
	if _, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, Text: "x", VoiceNote: true}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("voice_note 要 message_id：%v", err)
	}
	// 沒有引擎時也標得起來（engine=browser，瀏覽器念）。
	if _, err := call[SettingsView](t, e, owner, "xbot.voice.settings_set", SettingsSetIn{TTS: &Engine{}}); err != nil {
		t.Fatal(err)
	}
	reply2, _ := e.db.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: th.ID, Sender: "bot:" + e.bot.ID, Content: "好了"})
	if o, err := call[SpeakOut](t, e, owner, "xbot.voice.speak", SpeakIn{BotID: e.bot.ID, MessageID: reply2.ID, VoiceNote: true}); err != nil || o.Engine != EngineBrowser {
		t.Fatalf("瀏覽器語音訊息 %+v %v", o, err)
	}
	m2, _ := e.db.GetMessage(ctx, reply2.ID)
	if !strings.Contains(string(m2.Meta), `"engine":"browser"`) {
		t.Fatalf("meta %s", m2.Meta)
	}
}

func TestAutoReadPerBotAndThread(t *testing.T) {
	e := setup(t)
	th, _, _ := addThread(t, e)
	on := true
	bv, err := call[BotView](t, e, owner, "xbot.voice.bot_set", BotSetIn{BotID: e.bot.ID, AutoRead: &on})
	if err != nil || !bv.AutoRead || !bv.AutoReadNow {
		t.Fatalf("Bot 自動朗讀 %+v %v", bv, err)
	}
	bv, _ = call[BotView](t, e, owner, "xbot.voice.thread_set", ThreadSetIn{ThreadID: th.ID, AutoRead: "off"})
	if bv.AutoReadNow || bv.ThreadAutoRead == nil || *bv.ThreadAutoRead {
		t.Fatalf("對話串關掉 %+v", bv)
	}
	bv, _ = call[BotView](t, e, owner, "xbot.voice.bot_get", BotGetIn{BotID: e.bot.ID, ThreadID: th.ID})
	if bv.AutoReadNow || !bv.AutoRead {
		t.Fatalf("讀回 %+v", bv)
	}
	bv, _ = call[BotView](t, e, owner, "xbot.voice.thread_set", ThreadSetIn{ThreadID: th.ID, AutoRead: "inherit"})
	if !bv.AutoReadNow || bv.ThreadAutoRead != nil {
		t.Fatalf("跟著 Bot %+v", bv)
	}
	other, _ := e.db.CreateBot(ctx, store.Bot{Name: "阿哲", Engine: "hermes"})
	if _, err := call[BotView](t, e, owner, "xbot.voice.bot_get", BotGetIn{BotID: other.ID, ThreadID: th.ID}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("別的 Bot 的對話串：%v", err)
	}
	if _, err := call[BotView](t, e, owner, "xbot.voice.bot_get", BotGetIn{BotID: "nope"}); code(err) != ops.CodeNotFound {
		t.Fatalf("沒有這個 Bot：%v", err)
	}
	// 只能碰自己被授權的 Bot。
	limited := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "代理", Via: "mcp", Scopes: ops.Scopes, Resources: ops.Resources{Bots: []string{other.ID}}}
	if _, err := call[BotView](t, e, limited, "xbot.voice.thread_set", ThreadSetIn{ThreadID: th.ID, AutoRead: "on"}); code(err) != ops.CodeForbidden {
		t.Fatalf("沒授權的 Bot：%v", err)
	}
}

func multipartRecording(t *testing.T, fields map[string]string, audio []byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if audio != nil {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="file"; filename="speech.webm"`}
		h["Content-Type"] = []string{"audio/webm;codecs=opus"}
		w, _ := mw.CreatePart(h)
		_, _ = w.Write(audio)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), &buf
}

func postRecording(t *testing.T, e *env, fields map[string]string, audio []byte) (int, map[string]any) {
	t.Helper()
	ct, body := multipartRecording(t, fields, audio)
	resp, err := http.Post(e.srv.URL+"/api/v1/voice.transcribe", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTranscribeUpload(t *testing.T) {
	e := setup(t)
	status, out := postRecording(t, e, map[string]string{"bot_id": e.bot.ID, "duration_ms": "2100"}, []byte("OggS-fake"))
	if status != http.StatusConflict {
		t.Fatalf("沒有引擎應回 409：%d %v", status, out)
	}
	e.useEngines(t)
	status, out = postRecording(t, e, map[string]string{"bot_id": e.bot.ID, "duration_ms": "2100", "language": "zh"}, []byte("OggS-fake"))
	if status != 200 || out["text"] != "幫我訂明天下午的會議室" || out["model"] != "whisper-1" || out["provider"] != "openai" {
		t.Fatalf("辨識 %d %v", status, out)
	}
	got := e.up.transcribe[0]
	if got["filename"] != "speech.webm" || got["type"] != "audio/webm;codecs=opus" || got["audio"] != "OggS-fake" || got["model"] != "whisper-1" || got["language"] != "zh" {
		t.Fatalf("送給供應商的 %+v", got)
	}
	rows, _ := e.db.ListUsage(ctx, store.UsageFilter{})
	if len(rows) != 1 || rows[0].Endpoint != "audio/transcriptions" || rows[0].BotID != e.bot.ID || rows[0].CostMicros != 300 {
		t.Fatalf("辨識用量（3 秒 × 100）%+v", rows)
	}
	// 太長、太大、沒有錄音。
	status, out = postRecording(t, e, map[string]string{"duration_ms": "200000"}, []byte("x"))
	if errOf(out) != string(ops.CodeLimit) {
		t.Fatalf("太長 %d %v", status, out)
	}
	_, out = postRecording(t, e, nil, bytes.Repeat([]byte("a"), int(limits.VoiceUploadBytes)+10))
	if errOf(out) != string(ops.CodeLimit) {
		t.Fatalf("太大 %v", out)
	}
	_, out = postRecording(t, e, map[string]string{"bot_id": e.bot.ID}, nil)
	if errOf(out) != string(ops.CodeInvalidInput) {
		t.Fatalf("沒有錄音 %v", out)
	}
	_, out = postRecording(t, e, nil, []byte{})
	if errOf(out) != string(ops.CodeInvalidInput) {
		t.Fatalf("空的錄音 %v", out)
	}
	// MCP 等出口用 base64。
	tr, err := call[TranscribeOut](t, e, owner, "xbot.voice.transcribe", TranscribeIn{AudioBase64: "T2dnUw==", MimeType: "audio/ogg"})
	if err != nil || tr.Text == "" || e.up.transcribe[1]["filename"] != "speech.ogg" {
		t.Fatalf("base64 %+v %v", tr, err)
	}
}

func errOf(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestCallEndLeavesALine(t *testing.T) {
	e := setup(t)
	th, _, _ := addThread(t, e)
	out, err := call[CallEndOut](t, e, owner, "xbot.voice.call_end", CallEndIn{BotID: e.bot.ID, ThreadID: th.ID, DurationS: 192, Turns: 4})
	if err != nil || out.Text != "通話 3 分 12 秒 · 你說了 4 句" {
		t.Fatalf("通話紀錄 %+v %v", out, err)
	}
	m, _ := e.db.GetMessage(ctx, out.MessageID)
	if m.Kind != "activity" || m.Sender != "system" || !strings.Contains(string(m.Meta), `"activity":"call"`) {
		t.Fatalf("訊息 %+v", m)
	}
	if CallLine(42, 0, "idle") != "通話 42 秒 · 太久沒人說話，自動掛斷" {
		t.Fatal(CallLine(42, 0, "idle"))
	}
}

func TestSettingsValidation(t *testing.T) {
	e := setup(t)
	if _, err := call[SettingsView](t, e, owner, "xbot.voice.settings_set", SettingsSetIn{TTS: &Engine{Provider: "nope"}}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("沒有的供應商：%v", err)
	}
	e.svc.Providers = func(context.Context) ([]Provider, error) {
		return []Provider{{ID: "codex", Label: "ChatGPT 訂閱", OAuth: true}}, nil
	}
	if _, err := call[SettingsView](t, e, owner, "xbot.voice.settings_set", SettingsSetIn{STT: &Engine{Provider: "codex"}}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("訂閱登入：%v", err)
	}
	v, _ := call[SettingsView](t, e, owner, "xbot.voice.settings_get", SettingsIn{})
	if len(v.Engines) != 2 || v.Engines[1].Usable {
		t.Fatalf("訂閱登入不能用 %+v", v.Engines)
	}
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a1", Name: "代理", Via: "mcp", Scopes: []ops.Scope{ops.ScopeRead, ops.ScopeOperate}}
	if _, err := call[SettingsView](t, e, agent, "xbot.voice.settings_set", SettingsSetIn{}); code(err) != ops.CodeForbidden {
		t.Fatalf("沒有 admin 不能改：%v", err)
	}
}

func TestCachePrune(t *testing.T) {
	dir := t.TempDir()
	s := &Service{CacheDir: dir}
	old := time.Now().Add(-time.Hour)
	for i, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)} {
		if err := s.store(id, bytes.Repeat([]byte{1}, 100), audioMeta{ContentType: "audio/mpeg", Bytes: 100}); err != nil {
			t.Fatal(err)
		}
		at := old.Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(s.audioPath(id), at, at)
	}
	s.prune(250)
	if _, _, err := s.Audio(strings.Repeat("a", 32)); err == nil {
		t.Fatal("最舊的應該刪掉")
	}
	for _, id := range []string{strings.Repeat("b", 32), strings.Repeat("c", 32)} {
		if _, _, err := s.Audio(id); err != nil {
			t.Fatalf("%s 應該留著：%v", id, err)
		}
	}
	if _, _, err := s.Audio("../../x"); err == nil {
		t.Fatal("不合法的 id")
	}
}
