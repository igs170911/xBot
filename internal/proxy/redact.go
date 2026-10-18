package proxy

import (
	"bytes"
	"io"
	"strings"
)

// redactWriter 把寫過去的內容裡的真金鑰換成遮蔽字樣。
// 金鑰可能被切在兩次 Write 之間，所以結尾若「可能是金鑰的開頭」就先留著，等下一段或 flush。
// SSE 每個事件以換行結束，而金鑰不會以換行開頭，所以不會拖慢事件送達。
type redactWriter struct {
	w      io.Writer
	secret []byte
	held   []byte
}

func newRedactWriter(w io.Writer, secret string) *redactWriter {
	return &redactWriter{w: w, secret: []byte(secret)}
}

func (r *redactWriter) Write(p []byte) (int, error) {
	if len(r.secret) == 0 {
		return r.w.Write(p)
	}
	buf := append(r.held, p...)
	buf = bytes.ReplaceAll(buf, r.secret, []byte(Masked))
	k := partialSuffix(buf, r.secret)
	r.held = append([]byte(nil), buf[len(buf)-k:]...)
	if out := buf[:len(buf)-k]; len(out) > 0 {
		if _, err := r.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// flush 送出留著的尾巴（回應結束時呼叫）。
func (r *redactWriter) flush() error {
	if len(r.held) == 0 {
		return nil
	}
	_, err := r.w.Write(r.held)
	r.held = nil
	return err
}

// partialSuffix 回傳 buf 結尾和 secret 開頭重疊的最長長度（小於 len(secret)）。
func partialSuffix(buf, secret []byte) int {
	for k := min(len(secret)-1, len(buf)); k > 0; k-- {
		if bytes.Equal(buf[len(buf)-k:], secret[:k]) {
			return k
		}
	}
	return 0
}

// redactString 用在紀錄與錯誤訊息。
func redactString(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, Masked)
}
