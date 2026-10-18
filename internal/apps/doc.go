// Package apps — 連接應用程式與登入（C34，PRD 6.6 SK-5、SK-6、BX-10、SEC-5；畫板 I01～I05、E09）。
//
//   - 目錄（catalog.json）：廠商自己架的遠端 MCP（Hermes optional-mcps＋LazyBoy 的中文名稱），驗證方式 none／api_key／oauth2。
//   - 連接：目錄裡的應用程式或自訂 MCP（遠端網址或本機程式）。OAuth 由 XBot 服務自己跑（PKCE、動態註冊、換新），
//     權杖與金鑰加密存在服務端（E11 金鑰），Bot 用的時候經 product.sock 由 XBot 轉送並帶上權杖：權杖永遠不進容器。
//   - 授權：每個連接給所有 Bot 或只給指定的 Bot；轉送時以 product.sock 的身分檢查（SEC-5 每 Bot 隔離）。
//   - 登入（cookies）：Bot 瀏覽器登入了哪些網站、移除、分享給其他 Bot（LazyBoy server/logins.py）。
package apps

// AuthKind 是連接的驗證方式。
type AuthKind string

// 驗證方式。
const (
	AuthNone   AuthKind = "none"
	AuthAPIKey AuthKind = "api_key"
	AuthOAuth2 AuthKind = "oauth2"
)

// EnumValues 給 JSON Schema 用。
func (AuthKind) EnumValues() []string { return []string{"none", "api_key", "oauth2"} }

// Transport 是連線方式。
type Transport string

// 連線方式。
const (
	TransportHTTP  Transport = "http"  // 遠端網址（Streamable HTTP），由 XBot 轉送
	TransportStdio Transport = "stdio" // 在 Bot 電腦裡執行的本機程式
)

// EnumValues 給 JSON Schema 用。
func (Transport) EnumValues() []string { return []string{"http", "stdio"} }
