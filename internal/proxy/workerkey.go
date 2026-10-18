package proxy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// WorkerKeyPrefix 是 worker-key 的開頭，方便一眼認出（和 secret scanner 辨識）。
const WorkerKeyPrefix = "xbw_"

// ErrUnauthorized 表示 worker-key 缺少、格式不對、簽章不符或已被收回。
// 刻意不區分原因，免得給猜測的人線索。
var ErrUnauthorized = errors.New("worker-key 無效")

var botIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// WorkerKeys 發給每個 Bot 一把 worker-key：xbw_<botID>.<簽章>。
// 簽章 = HMAC-SHA256(伺服器金鑰, "v1|botID|nonce")；換掉該 Bot 的 nonce，舊 key 立刻失效。
// 驗證只需查一次 nonce，不必逐一比對所有 Bot（LazyBoy 是逐一比對檔案裡的亂數）。
type WorkerKeys struct {
	store NonceStore
	keys  *Keyring
}

// NewWorkerKeys 建立 WorkerKeys。
func NewWorkerKeys(store NonceStore, keys *Keyring) *WorkerKeys {
	return &WorkerKeys{store: store, keys: keys}
}

// Issue 回傳 Bot 目前的 worker-key；還沒有就先建立。同一個 nonce 會得到同一把 key。
func (w *WorkerKeys) Issue(ctx context.Context, botID string) (Secret, error) {
	if !botIDPattern.MatchString(botID) {
		return "", fmt.Errorf("這個 Bot 代號格式不對：%q", botID)
	}
	nonce, err := w.store.WorkerNonce(ctx, botID)
	if errors.Is(err, ErrNotFound) {
		return w.Rotate(ctx, botID)
	}
	if err != nil {
		return "", err
	}
	return w.token(botID, nonce), nil
}

// Rotate 換一個新的 nonce 並回傳新 key；舊 key 從此失效。
func (w *WorkerKeys) Rotate(ctx context.Context, botID string) (Secret, error) {
	if !botIDPattern.MatchString(botID) {
		return "", fmt.Errorf("這個 Bot 代號格式不對：%q", botID)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	if err := w.store.SetWorkerNonce(ctx, botID, nonce); err != nil {
		return "", err
	}
	return w.token(botID, nonce), nil
}

// Revoke 收回 Bot 的 worker-key（刪除 Bot 時用）；之後要再用必須重新 Issue。
func (w *WorkerKeys) Revoke(ctx context.Context, botID string) error {
	return w.store.DeleteWorkerNonce(ctx, botID)
}

// Verify 檢查 worker-key，成功時回傳它所屬的 Bot 代號。
func (w *WorkerKeys) Verify(ctx context.Context, key string) (string, error) {
	rest, ok := strings.CutPrefix(key, WorkerKeyPrefix)
	if !ok {
		return "", ErrUnauthorized
	}
	botID, sig, ok := strings.Cut(rest, ".")
	if !ok || !botIDPattern.MatchString(botID) {
		return "", ErrUnauthorized
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return "", ErrUnauthorized
	}
	nonce, err := w.store.WorkerNonce(ctx, botID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	if !hmac.Equal(got, w.sign(botID, nonce)) {
		return "", ErrUnauthorized
	}
	return botID, nil
}

func (w *WorkerKeys) sign(botID, nonce string) []byte {
	m := hmac.New(sha256.New, w.keys.workerKey)
	m.Write([]byte("v1|" + botID + "|" + nonce))
	return m.Sum(nil)
}

func (w *WorkerKeys) token(botID, nonce string) Secret {
	return Secret(WorkerKeyPrefix + botID + "." + base64.RawURLEncoding.EncodeToString(w.sign(botID, nonce)))
}
