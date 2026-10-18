package routines

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/runtime"
	"xbot/internal/store"
)

// HookPrefix 是網址觸發的路徑開頭：POST /hooks/<hook_id>。
const HookPrefix = "/hooks/"

// NewHookID、NewSecret 產生網址觸發的 id 與金鑰。
func NewHookID() string { return "h" + store.NewID()[12:] }

// NewSecret 產生一把新的金鑰。
func NewSecret() string {
	b := make([]byte, limits.RoutineHookSecretBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Sign 算出內容的簽章（X-XBot-Signature 的值）。
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// authorized 檢查 Bearer 金鑰或 HMAC 簽章（固定時間比對）。
func authorized(r *http.Request, secret string, body []byte) bool {
	if secret == "" {
		return false
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		token, ok := strings.CutPrefix(auth, "Bearer ")
		return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(secret)) == 1
	}
	if sig := r.Header.Get(limits.RoutineHookSigHeader); sig != "" {
		return hmac.Equal([]byte(strings.TrimSpace(sig)), []byte(Sign(secret, body)))
	}
	return false
}

// HookResult 是網址觸發的回應內容。
type HookResult struct {
	OK     bool   `json:"ok"`
	Test   bool   `json:"test,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// HookHandler 是網址觸發的入口（不需要登入：網址的 id＋金鑰就是憑證）。LazyBoy main.py routine_hook
//
//	200：已交給 Bot（或測試請求已收到）
//	401：網址或金鑰不對；405：不是 POST；409：定時任務已暫停；413：內容超過 64 KiB；
//	429：同一個網址不到 60 秒又觸發（Retry-After）；503：Bot 正在忙或開不了工（Retry-After，可以重送）
func (s *Service) HookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeHook(w, http.StatusMethodNotAllowed, HookResult{Error: "請用 POST"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, HookPrefix)
		// 沒登入的網址：知道大小之前什麼都不讀，太大的內容不能塞滿記憶體。
		if r.ContentLength > limits.RoutineHookBytes {
			writeHook(w, http.StatusRequestEntityTooLarge, HookResult{Error: "內容太大（最多 64 KiB）"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.RoutineHookBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeHook(w, http.StatusRequestEntityTooLarge, HookResult{Error: "內容太大（最多 64 KiB）"})
				return
			}
			writeHook(w, http.StatusBadRequest, HookResult{Error: "讀不到內容"})
			return
		}
		test := r.Header.Get(limits.RoutineHookTestHeader) == "1" || r.URL.Query().Get("test") == "1"
		code, res := s.Hook(r.Context(), id, func(secret string) bool { return authorized(r, secret, body) }, body, test)
		if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", strconv.Itoa(int(limits.RoutineHookSpacing/time.Second)))
		}
		writeHook(w, code, res)
	})
}

func writeHook(w http.ResponseWriter, code int, res HookResult) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(res)
}

// Hook 處理一次網址觸發：檢查金鑰、暫停、間隔，再把內容當成資料交給 Bot。auth 依存好的金鑰檢查這次請求。
func (s *Service) Hook(ctx context.Context, hookID string, auth func(secret string) bool, body []byte, test bool) (int, HookResult) {
	ctx = context.WithoutCancel(ctx) // 對方斷線也要把紀錄寫完
	unauthorized := HookResult{Error: "觸發網址或金鑰不對"}
	if hookID == "" || strings.Contains(hookID, "/") {
		return http.StatusUnauthorized, unauthorized
	}
	r, err := s.o.DB.GetRoutineByHook(ctx, hookID)
	if err != nil || !auth(r.HookSecret) {
		return http.StatusUnauthorized, unauthorized
	}
	record := func(trigger, status, msg string) store.RoutineRun {
		run, _, err := s.o.DB.ClaimRoutineRun(ctx, store.RoutineRun{RoutineID: r.ID, BotID: r.BotID, Occurrence: trigger + ":" + store.NewID(),
			Trigger: trigger, Status: status, Error: msg, PayloadBytes: len(body), ScheduledAt: s.o.Now().UnixMilli()})
		if err != nil {
			s.log.Warn("寫入網址觸發紀錄失敗", "routine", r.ID, "err", err)
		}
		s.setRun(ctx, run.ID, status, "", msg) // 記下結束時間
		s.prune(ctx, r.ID)
		return run
	}
	if test {
		run := record(store.TriggerTest, store.RunTest, "")
		return http.StatusOK, HookResult{OK: true, Test: true, RunID: run.ID}
	}
	if r.Status != store.RoutineActive {
		return http.StatusConflict, HookResult{Error: "這個定時任務已暫停"}
	}
	s.mu.Lock()
	cur, err := s.o.DB.GetRoutine(ctx, r.ID)
	if err != nil {
		s.mu.Unlock()
		return http.StatusUnauthorized, unauthorized
	}
	now := s.o.Now()
	if cur.LastHookAt != 0 && now.Sub(time.UnixMilli(cur.LastHookAt)) < limits.RoutineHookSpacing {
		s.mu.Unlock()
		record(store.TriggerWebhook, store.RunSkipped, "不到 60 秒又觸發，已略過")
		return http.StatusTooManyRequests, HookResult{Error: "同一個網址每 60 秒最多觸發一次"}
	}
	cur.LastHookAt = now.UnixMilli()
	s.save(ctx, cur)
	s.mu.Unlock()

	text := string(body)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	run, err := s.RunNow(ctx, r.ID, store.TriggerWebhook, HookData(text), len(body))
	if err != nil {
		// 沒開始工作：不算這次間隔，對方可以稍後重送。
		s.mu.Lock()
		if c, gerr := s.o.DB.GetRoutine(ctx, r.ID); gerr == nil && c.LastHookAt == cur.LastHookAt {
			c.LastHookAt = 0
			s.save(ctx, c)
		}
		s.mu.Unlock()
		msg := err.Error()
		if errors.Is(err, runtime.ErrBusy) {
			msg = "Bot 正在忙，請稍後再送"
		}
		return http.StatusServiceUnavailable, HookResult{RunID: run.ID, Error: msg}
	}
	return http.StatusOK, HookResult{OK: true, RunID: run.ID, TaskID: run.TaskID}
}
