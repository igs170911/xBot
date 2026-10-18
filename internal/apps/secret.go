package apps

import (
	"encoding/json"
	"strings"
)

// Sealer 加密與解開祕密；*proxy.Keyring 符合（E11：和模型金鑰同一把主金鑰，用途用 aad 分開）。
type Sealer interface {
	Seal(plaintext []byte, aad string) ([]byte, error)
	Open(sealed []byte, aad string) ([]byte, error)
}

// secret 是一個連接的祕密（整組加密存在 app_connection.secret）。只在服務端使用，永遠不回傳、不進容器
// （唯一例外：自訂本機程式的 Env，見 hermes.AppServer）。
type secret struct {
	APIKey      string            `json:"api_key,omitempty"`
	TokenHeader string            `json:"token_header,omitempty"` // API 金鑰放哪個標頭；空白是 Authorization: Bearer
	Headers     map[string]string `json:"headers,omitempty"`      // 其他標頭（自訂連接）
	Env         map[string]string `json:"env,omitempty"`          // 本機程式的環境變數（自訂連接）
	OAuth       *oauthState       `json:"oauth,omitempty"`
}

// oauthState 是 OAuth 2（MCP 授權規格：PKCE、RFC 8707 resource）的用戶端與權杖。
type oauthState struct {
	Issuer          string   `json:"issuer,omitempty"`
	AuthURL         string   `json:"auth_url"`
	TokenURL        string   `json:"token_url"`
	RegistrationURL string   `json:"registration_url,omitempty"`
	Resource        string   `json:"resource,omitempty"`
	RedirectURI     string   `json:"redirect_uri"`
	ClientID        string   `json:"client_id"`
	ClientSecret    string   `json:"client_secret,omitempty"`
	AuthMethod      string   `json:"auth_method,omitempty"` // none／client_secret_post／client_secret_basic
	Registered      bool     `json:"registered,omitempty"`  // 用戶端是動態註冊來的（換了回呼網址要重新註冊）
	Scopes          []string `json:"scopes,omitempty"`
	AccessToken     string   `json:"access_token,omitempty"`
	RefreshToken    string   `json:"refresh_token,omitempty"`
	TokenType       string   `json:"token_type,omitempty"`
	Expiry          int64    `json:"expiry,omitempty"` // unix 秒；0 表示不知道
}

func aad(connectionID string) string { return "app:" + connectionID }

func (s *Service) seal(connectionID string, sec secret) ([]byte, error) {
	b, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	return s.o.Keys.Seal(b, aad(connectionID))
}

func (s *Service) open(connectionID string, sealed []byte) (secret, error) {
	var sec secret
	if len(sealed) == 0 {
		return sec, nil
	}
	b, err := s.o.Keys.Open(sealed, aad(connectionID))
	if err != nil {
		return sec, err
	}
	err = json.Unmarshal(b, &sec)
	return sec, err
}

// hint 是畫面上顯示的末四碼（I04「Token 已存時只顯示末四碼」）。
func hint(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "Bearer "))
	r := []rune(v)
	if len(r) <= 4 {
		return ""
	}
	return string(r[len(r)-4:])
}
