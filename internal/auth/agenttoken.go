package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// AgentTokenPrefix 讓人一眼看出這是 XBot 的代理權杖（也方便密鑰掃描工具辨認）。
const AgentTokenPrefix = "xbot_"

// ErrInvalidToken 表示權杖不存在、已撤銷或已過期。
var ErrInvalidToken = errors.New("權杖無效、已撤銷或已過期")

// HashToken 回傳權杖存進資料庫的雜湊（sha256 hex）。權杖本身是 256 位元的隨機值，不需要加鹽或慢雜湊。
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewAgentToken 產生一把新的代理權杖：raw 只給代理一次，資料庫只存 hash。
func NewAgentToken() (raw, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("產生權杖失敗：" + err.Error()) // crypto/rand 不會失敗（Go 1.24 起）
	}
	raw = AgentTokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	return raw, HashToken(raw)
}

// ReadAccessToken 讀本機的登入碼（環境變數 XBOT_ACCESS_TOKEN 優先），不會新增。
// 本機指令列（xbot <領域> <動作>、xbot mcp --owner）用它證明「我是這台機器上的使用者」：
// 能讀到資料目錄裡權限 600 的檔案，就是使用者本人。
func ReadAccessToken(dataDir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("XBOT_ACCESS_TOKEN")); v != "" {
		return v, nil
	}
	p := filepath.Join(dataDir, TokenFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("找不到登入碼 %s：XBot 服務還沒在這台機器啟動過，或資料目錄不同（XBOT_DATA_DIR）", p)
	}
	if err != nil {
		return "", fmt.Errorf("讀不到登入碼 %s：%w", p, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("登入碼檔案 %s 是空的", p)
	}
	return tok, nil
}

// Authenticator 把請求帶的權杖換成呼叫者身分：登入碼＝使用者本人，代理權杖＝那個代理。
type Authenticator struct {
	DB          *store.DB
	AccessToken string // 使用者的登入碼；空白表示不接受登入碼

	mu      sync.Mutex
	touched map[string]time.Time
}

// Identify 驗證 raw 權杖，回傳身分（via 寫進稽核，例：mcp、http）。
// 權杖不存在、撤銷、過期回 ErrInvalidToken。每次都查資料庫，所以撤銷立刻生效。
func (a *Authenticator) Identify(ctx context.Context, raw, via string) (ops.Identity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ops.Identity{}, ErrInvalidToken
	}
	if a.AccessToken != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(a.AccessToken)) == 1 {
		return ops.Owner(via), nil
	}
	t, err := a.DB.FindAgentToken(ctx, HashToken(raw))
	if errors.Is(err, store.ErrNotFound) {
		return ops.Identity{}, ErrInvalidToken
	}
	if err != nil {
		return ops.Identity{}, err
	}
	id, err := ops.Agent(t, via)
	if err != nil {
		return ops.Identity{}, err
	}
	a.touch(ctx, t.ID)
	return id, nil
}

// touch 記錄最後使用時間；同一把權杖一分鐘內只寫一次。
func (a *Authenticator) touch(ctx context.Context, id string) {
	a.mu.Lock()
	if a.touched == nil {
		a.touched = map[string]time.Time{}
	}
	now := time.Now()
	if last, ok := a.touched[id]; ok && now.Sub(last) < limits.AgentTokenTouchEvery {
		a.mu.Unlock()
		return
	}
	a.touched[id] = now
	a.mu.Unlock()
	_ = a.DB.TouchAgentToken(context.WithoutCancel(ctx), id) // 只是顯示用，失敗不影響呼叫
}
