// Package auth 處理登入碼、裝置配對與代理權杖。
//
// 登入碼（access-token）、HTTP 驗證（session cookie、Bearer、本機免登入、登入失敗限速）、代理權杖雜湊。
// 配對碼換權杖在 C04。
package auth

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TokenFile 是登入碼在資料目錄中的檔名。
const TokenFile = "access-token"

// EnsureAccessToken 讀取登入碼；不存在就產生一個新的並寫入（權限 600）。
// 環境變數 XBOT_ACCESS_TOKEN 有值時直接使用它。
func EnsureAccessToken(dataDir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("XBOT_ACCESS_TOKEN")); v != "" {
		return v, nil
	}
	p := filepath.Join(dataDir, TokenFile)
	b, err := os.ReadFile(p)
	if err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("讀不到登入碼 %s：%w", p, err)
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("建立資料目錄失敗：%w", err)
	}
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("寫入登入碼失敗：%w", err)
	}
	return tok, nil
}

func newToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("產生登入碼失敗：%w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}
